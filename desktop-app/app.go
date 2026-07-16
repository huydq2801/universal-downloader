package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

// fetchHTML tải mã nguồn HTML của một URL với headers giả lập trình duyệt
func (a *App) fetchHTML(ctx context.Context, targetURL, referer string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
	if err != nil {
		return "", fmt.Errorf("tạo request thất bại cho %s: %v", targetURL, err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "vi-VN,vi;q=0.9,en-VN;q=0.8,en-US;q=0.7,en;q=0.6")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}

	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("kết nối URL thất bại %s: %v", targetURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("không thể tải URL %s (HTTP %d)", targetURL, resp.StatusCode)
	}

	bytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("đọc dữ liệu thất bại từ %s: %v", targetURL, err)
	}

	return string(bytes), nil
}

// extractIframeURLs tìm các link iframe nhúng và các link máy chủ video tiềm năng trong HTML
func (a *App) extractIframeURLs(htmlContent, baseURL string) []string {
	var urls []string
	
	// 1. Quét thẻ iframe src
	iframeRe := regexp.MustCompile(`(?i)<iframe[^>]+src=["']([^"']+)["']`)
	matches := iframeRe.FindAllStringSubmatch(htmlContent, -1)
	
	base, err := url.Parse(baseURL)
	if err != nil {
		return urls
	}
	
	seen := make(map[string]bool)
	for _, m := range matches {
		if len(m) > 1 {
			uStr := strings.TrimSpace(m[1])
			if uStr == "" || strings.HasPrefix(uStr, "javascript:") {
				continue
			}
			u, err := url.Parse(uStr)
			if err == nil {
				absURL := base.ResolveReference(u).String()
				if !seen[absURL] {
					seen[absURL] = true
					urls = append(urls, absURL)
				}
			}
		}
	}
	
	// 2. Quét các link hosting video trực tiếp (doodstream, streamtape, mixdrop, fembed, voe, vidsrc, v.v.)
	playerRe := regexp.MustCompile(`(?i)https?://[^\s"'<>]+?(?:dood|streamtape|mixdrop|fembed|voe|vidsrc|embed|player|video|play)[^\s"'<>]*`)
	playerMatches := playerRe.FindAllString(htmlContent, -1)
	for _, uStr := range playerMatches {
		if !seen[uStr] {
			seen[uStr] = true
			urls = append(urls, uStr)
		}
	}
	
	return urls
}

// AnalyzeWithAI cào trang web chính, quét các iframe nhúng bên dưới và dùng Gemini AI để trích xuất link video ẩn
func (a *App) AnalyzeWithAI(pageURL string) (*downloader.ExtractResult, error) {
	// 1. Lấy API Key từ Settings
	settings, err := a.db.GetSettings()
	if err != nil || settings.GeminiAPIKey == "" {
		return nil, fmt.Errorf("Hãy thiết lập Gemini API Key trong Cài đặt trước khi dùng AI")
	}

	// 2. Tải HTML trang web chính
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	runtime.LogInfof(a.ctx, "AI Extractor: Đang tải trang web gốc: %s", pageURL)
	mainHTML, err := a.fetchHTML(ctx, pageURL, "")
	if err != nil {
		return nil, fmt.Errorf("Lỗi tải trang gốc: %v", err)
	}

	// Trích xuất các iframe/player URLs liên quan
	iframeURLs := a.extractIframeURLs(mainHTML, pageURL)
	
	// Gộp HTML chính và HTML của iframe để AI phân tích toàn diện
	var combinedHTML strings.Builder
	combinedHTML.WriteString(fmt.Sprintf("--- ORIGINAL PAGE URL: %s ---\n", pageURL))
	combinedHTML.WriteString(mainHTML)
	combinedHTML.WriteString("\n\n")

	// Quét sâu thêm tối đa 3 iframe/player nhúng để tìm link stream thực tế
	iframeCount := 0
	for _, iframeURL := range iframeURLs {
		if iframeCount >= 3 {
			break
		}
		// Bỏ qua các URL quảng cáo/analytics không cần thiết
		if strings.Contains(iframeURL, "google") || strings.Contains(iframeURL, "doubleclick") || 
		   strings.Contains(iframeURL, "facebook") || strings.Contains(iframeURL, "analytics") || 
		   strings.Contains(iframeURL, "adnxs") || strings.Contains(iframeURL, "banner") {
			continue
		}
		
		runtime.LogInfof(a.ctx, "AI Extractor: Đang quét sâu vào link nhúng: %s", iframeURL)
		iframeHTML, err := a.fetchHTML(ctx, iframeURL, pageURL)
		if err == nil {
			combinedHTML.WriteString(fmt.Sprintf("--- EMBEDDED PLAYER/IFRAME URL: %s ---\n", iframeURL))
			combinedHTML.WriteString(iframeHTML)
			combinedHTML.WriteString("\n\n")
			iframeCount++
		}
	}

	runtime.LogInfof(a.ctx, "AI Extractor: Đang gửi dữ liệu HTML tổng hợp (chứa %d links nhúng) lên Gemini...", iframeCount)

	// 3. Dùng Gemini AI trích xuất link video từ toàn bộ HTML tổng hợp này
	return downloader.ExtractMediaWithGemini(ctx, pageURL, combinedHTML.String(), settings.GeminiAPIKey, settings.GeminiModel)
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
