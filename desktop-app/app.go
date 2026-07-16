package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"universal-downloader/internal/downloader"
	"universal-downloader/internal/queue"
	"universal-downloader/internal/storage"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct — Wails app
type App struct {
	ctx   context.Context
	db    *storage.Database
	queue *queue.Manager
}

// NewApp tạo App instance mới
func NewApp() *App {
	return &App{}
}

// startup được gọi khi app khởi động
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Khởi tạo database
	dbPath := getDBPath()
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		runtime.LogErrorf(ctx, "Không thể khởi tạo DB: %v", err)
		return
	}
	a.db = db
	runtime.LogInfof(ctx, "Database: %s", dbPath)

	// Khởi tạo download engine
	engine := downloader.NewEngine(getToolsDir())

	// Khởi tạo queue manager
	a.queue = queue.NewManager(engine, db, func(update queue.ProgressUpdate) {
		// Gửi progress update lên frontend qua Wails events
		runtime.EventsEmit(ctx, "download:progress", update)
	})
	a.queue.Start(3) // 3 workers song song

	runtime.LogInfo(ctx, "UniversalDownloader đã khởi động ✓")
}

// shutdown được gọi khi app tắt
func (a *App) shutdown(ctx context.Context) {
	if a.queue != nil {
		a.queue.Stop()
	}
	if a.db != nil {
		a.db.Close()
	}
	runtime.LogInfo(ctx, "UniversalDownloader đã tắt")
}

// ===== WAILS BINDINGS — Các hàm có thể gọi từ JS frontend =====

// GetDownloadList trả về danh sách download hiện tại
func (a *App) GetDownloadList() []storage.DownloadRecord {
	records, _ := a.db.GetRecentDownloads(100)
	return records
}

// GetQueueItems trả về items đang trong queue
func (a *App) GetQueueItems() []*queue.Item {
	return a.queue.GetAll()
}

// AddDownload thêm URL vào queue tải
func (a *App) AddDownload(req downloader.DownloadRequest) (*queue.Item, error) {
	return a.queue.Add(req)
}

// CancelDownload hủy một download theo ID
func (a *App) CancelDownload(id string) error {
	return a.queue.Cancel(id)
}

// RetryDownload thử lại download đã thất bại
func (a *App) RetryDownload(id string) error {
	return a.queue.Retry(id)
}

// RemoveDownload xóa khỏi queue/history
func (a *App) RemoveDownload(id string) error {
	return a.queue.Remove(id)
}

// GetSettings lấy settings hiện tại
func (a *App) GetSettings() (*storage.Settings, error) {
	return a.db.GetSettings()
}

// SaveSettings lưu settings
func (a *App) SaveSettings(settings storage.Settings) error {
	return a.db.SaveSettings(settings)
}

// OpenFolder mở thư mục trong Windows Explorer
func (a *App) OpenFolder(path string) error {
	return openFolder(path)
}

// SelectFolder mở dialog chọn thư mục
func (a *App) SelectFolder() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Chọn thư mục lưu file",
	})
}

// GetInfo lấy thông tin media từ URL (dùng yt-dlp --dump-json)
func (a *App) GetInfo(url string) (*downloader.MediaInfo, error) {
	engine := downloader.NewEngine(getToolsDir())
	if settings, err := a.db.GetSettings(); err == nil {
		engine.SetPaths(settings.YtdlpPath, settings.FfmpegPath)
	}
	return engine.GetInfo(url)
}

// AnalyzeWithAI cào trang web và dùng Gemini AI để trích xuất link video ẩn
func (a *App) AnalyzeWithAI(pageURL string) (*downloader.ExtractResult, error) {
	// 1. Lấy API Key từ Settings
	settings, err := a.db.GetSettings()
	if err != nil || settings.GeminiAPIKey == "" {
		return nil, fmt.Errorf("Hãy thiết lập Gemini API Key trong Cài đặt trước khi dùng AI")
	}

	// 2. Tải HTML nguồn của trang web
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("tạo request tải HTML thất bại: %v", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("không thể tải mã nguồn trang web: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tải trang web thất bại, mã HTTP: %d", resp.StatusCode)
	}

	htmlBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("đọc mã nguồn trang web lỗi: %v", err)
	}
	htmlContent := string(htmlBytes)

	// 3. Dùng Gemini AI trích xuất link video từ HTML
	return downloader.ExtractMediaWithGemini(ctx, pageURL, htmlContent, settings.GeminiAPIKey, settings.GeminiModel)
}


// CheckTools kiểm tra yt-dlp và ffmpeg có sẵn không
func (a *App) CheckTools() map[string]bool {
	engine := downloader.NewEngine(getToolsDir())
	if settings, err := a.db.GetSettings(); err == nil {
		engine.SetPaths(settings.YtdlpPath, settings.FfmpegPath)
	}
	return engine.CheckTools()
}

// GetVersion trả về version của app
func (a *App) GetVersion() string {
	return "1.0.0"
}

// ===== HELPERS =====

func getDBPath() string {
	homeDir, _ := os.UserHomeDir()
	dir := filepath.Join(homeDir, "UniversalDownloader")
	os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "data.db")
}

func getToolsDir() string {
	exe, _ := os.Executable()
	return filepath.Dir(exe)
}

func openFolder(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("thư mục không tồn tại: %s", path)
	}
	return openExplorer(path)
}
