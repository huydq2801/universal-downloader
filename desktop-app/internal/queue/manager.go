package queue

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"universal-downloader/internal/downloader"
	"universal-downloader/internal/storage"

	"github.com/google/uuid"
)

// Status của một download item
type Status string

const (
	StatusPending    Status = "pending"
	StatusDownloading Status = "downloading"
	StatusCompleted  Status = "completed"
	StatusFailed     Status = "failed"
	StatusCancelled  Status = "cancelled"
	StatusPaused     Status = "paused"
)

// Item là một download item trong queue
type Item struct {
	ID        string                    `json:"id"`
	Request   downloader.DownloadRequest `json:"request"`
	Status    Status                    `json:"status"`
	Progress  float64                   `json:"progress"`
	Speed     string                    `json:"speed"`
	ETA       string                    `json:"eta"`
	Error     string                    `json:"error"`
	SavePath  string                    `json:"savePath"`
	CreatedAt time.Time                 `json:"createdAt"`
	cancel    context.CancelFunc
}

// ProgressUpdate là update gửi về frontend/extension
type ProgressUpdate struct {
	ID       string  `json:"id"`
	Status   Status  `json:"status"`
	Progress float64 `json:"progress"`
	Speed    string  `json:"speed"`
	ETA      string  `json:"eta"`
	Error    string  `json:"error"`
	SavePath string  `json:"savePath"`
}

// OnProgressFunc callback khi có update
type OnProgressFunc func(update ProgressUpdate)

// Manager quản lý download queue
type Manager struct {
	mu         sync.RWMutex
	items      map[string]*Item
	pending    chan string // IDs chờ xử lý
	engine     *downloader.Engine
	db         *storage.Database
	onProgress OnProgressFunc
	workers    int
	stopCh     chan struct{}
}

// NewManager tạo Manager mới
func NewManager(engine *downloader.Engine, db *storage.Database, onProgress OnProgressFunc) *Manager {
	return &Manager{
		items:      make(map[string]*Item),
		pending:    make(chan string, 100),
		engine:     engine,
		db:         db,
		onProgress: onProgress,
		stopCh:     make(chan struct{}),
	}
}

// Start khởi động N workers
func (m *Manager) Start(workers int) {
	m.workers = workers
	for i := 0; i < workers; i++ {
		go m.worker()
	}
}

// Stop dừng tất cả workers
func (m *Manager) Stop() {
	close(m.stopCh)
	// Cancel tất cả downloads đang chạy
	m.mu.Lock()
	for _, item := range m.items {
		if item.cancel != nil && item.Status == StatusDownloading {
			item.cancel()
		}
	}
	m.mu.Unlock()
}

// Add thêm download mới vào queue
func (m *Manager) Add(req downloader.DownloadRequest) (*Item, error) {
	// Lấy save path từ settings nếu chưa có
	if req.SavePath == "" {
		settings, err := m.db.GetSettings()
		if err == nil {
			req.SavePath = settings.SaveDirectory
		}
	}

	item := &Item{
		ID:        fmt.Sprintf("%s", uuid.New().String()),
		Request:   req,
		Status:    StatusPending,
		CreatedAt: time.Now(),
		SavePath:  req.SavePath,
	}

	m.mu.Lock()
	m.items[item.ID] = item
	m.mu.Unlock()

	// Lưu vào DB
	record := &storage.DownloadRecord{
		ID:      item.ID,
		URL:     req.URL,
		PageURL: req.PageURL,
		Title:   req.Title,
		Type:    req.Type,
		Status:  string(StatusPending),
		Quality: req.Quality,
		Format:  req.Format,
		SavePath: req.SavePath,
	}
	m.db.CreateRecord(record)

	// Đưa vào pending queue
	select {
	case m.pending <- item.ID:
	default:
		return nil, fmt.Errorf("queue đầy")
	}

	return item, nil
}

// Cancel hủy download
func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	item, ok := m.items[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("không tìm thấy download: %s", id)
	}
	if item.cancel != nil {
		item.cancel()
	}
	item.Status = StatusCancelled
	m.mu.Unlock()

	m.db.UpdateStatus(id, string(StatusCancelled), item.Progress, "", "", "")
	m.notify(item)
	return nil
}

// Retry thử lại download đã thất bại
func (m *Manager) Retry(id string) error {
	m.mu.Lock()
	item, ok := m.items[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("không tìm thấy download: %s", id)
	}
	item.Status = StatusPending
	item.Error = ""
	item.Progress = 0
	m.mu.Unlock()

	m.pending <- id
	return nil
}

// Remove xóa item khỏi danh sách
func (m *Manager) Remove(id string) error {
	m.mu.Lock()
	item, ok := m.items[id]
	if ok && item.cancel != nil && item.Status == StatusDownloading {
		item.cancel()
	}
	delete(m.items, id)
	m.mu.Unlock()

	return m.db.DeleteRecord(id)
}

// GetAll trả về tất cả items
func (m *Manager) GetAll() []*Item {
	m.mu.RLock()
	defer m.mu.RUnlock()

	items := make([]*Item, 0, len(m.items))
	for _, item := range m.items {
		items = append(items, item)
	}
	return items
}

// Get lấy item theo ID
func (m *Manager) Get(id string) (*Item, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	item, ok := m.items[id]
	return item, ok
}

// worker xử lý download từ queue
func (m *Manager) worker() {
	for {
		select {
		case <-m.stopCh:
			return
		case id := <-m.pending:
			m.processDownload(id)
		}
	}
}

// processDownload xử lý một download
func (m *Manager) processDownload(id string) {
	m.mu.Lock()
	item, ok := m.items[id]
	if !ok {
		m.mu.Unlock()
		return
	}
	
	if item.Status == StatusCancelled {
		m.mu.Unlock()
		return
	}

	// Tạo cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	item.cancel = cancel
	item.Status = StatusDownloading
	m.mu.Unlock()

	// Notify bắt đầu
	m.db.UpdateStatus(id, string(StatusDownloading), 0, "", "", "")
	m.notify(item)

	// Xác định save path
	savePath := item.SavePath
	if savePath == "" {
		settings, _ := m.db.GetSettings()
		if settings != nil {
			savePath = settings.SaveDirectory
		}
	}

	// Bắt đầu download
	err := m.engine.Download(ctx, item.Request, savePath, func(progress float64, speed, eta, status string) {
		m.mu.Lock()
		item.Progress = progress
		item.Speed = speed
		item.ETA = eta
		m.mu.Unlock()

		m.db.UpdateStatus(id, string(StatusDownloading), progress, speed, eta, "")
		m.notify(item)
	})

	m.mu.Lock()
	if err != nil {
		if ctx.Err() != nil {
			// Cancelled
			item.Status = StatusCancelled
			item.Error = "Đã hủy"
		} else {
			item.Status = StatusFailed
			item.Error = err.Error()
		}
	} else {
		item.Status = StatusCompleted
		item.Progress = 100
		// Tìm file đã tải
		item.SavePath = findDownloadedFile(savePath, item.Request.Title)
	}
	m.mu.Unlock()

	m.db.UpdateStatus(id, string(item.Status), item.Progress, "", "", item.Error)
	m.notify(item)
}

// notify gửi progress update
func (m *Manager) notify(item *Item) {
	if m.onProgress == nil {
		return
	}
	m.onProgress(ProgressUpdate{
		ID:       item.ID,
		Status:   item.Status,
		Progress: item.Progress,
		Speed:    item.Speed,
		ETA:      item.ETA,
		Error:    item.Error,
		SavePath: item.SavePath,
	})
}

// findDownloadedFile tìm file đã tải trong thư mục
func findDownloadedFile(dir, title string) string {
	if title == "" {
		return dir
	}
	// Trả về thư mục vì tên file chính xác phụ thuộc vào yt-dlp
	return filepath.Clean(dir)
}
