package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
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

// GetDownloadList trả về danh sách lịch sử download (chỉ lấy các mục đã kết thúc)
func (a *App) GetDownloadList() []storage.DownloadRecord {
	records, _ := a.db.GetRecentDownloads(100)
	var history []storage.DownloadRecord
	for _, r := range records {
		if r.Status == "completed" || r.Status == "failed" || r.Status == "cancelled" {
			history = append(history, r)
		}
	}
	return history
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

// ClearHistory xóa toàn bộ lịch sử tải xuống (completed, failed, cancelled)
func (a *App) ClearHistory() error {
	return a.db.ClearHistory()
}

// GetInfo lấy thông tin media từ URL (dùng yt-dlp --dump-json)
func (a *App) GetInfo(url string) (*downloader.MediaInfo, error) {
	engine := downloader.NewEngine(getToolsDir())
	if settings, err := a.db.GetSettings(); err == nil {
		engine.SetPaths(settings.YtdlpPath, settings.FfmpegPath)
	}
	return engine.GetInfo(url)
}

// fetchHTML tải mã nguồn HTML của một URL với headers giả lập trình duyệt và DNS công cộng
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

	dialer := downloader.NewDPIBypassDialer()
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return downloader.DPIBypassDialContext(ctx, dialer, network, addr)
			},
		},
		Timeout: 15 * time.Second,
	}

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

// fetchAjaxPlayer gửi POST tới /ajax.php để lấy player HTML kiểu vlxx
func (a *App) fetchAjaxPlayer(ctx context.Context, ajaxURL, id, server, referer string) (string, error) {
	data := url.Values{}
	data.Set("vlxx_server", "1")
	data.Set("id", id)
	data.Set("server", server)

	req, err := http.NewRequestWithContext(ctx, "POST", ajaxURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Referer", referer)

	dialer := downloader.NewDPIBypassDialer()
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return downloader.DPIBypassDialContext(ctx, dialer, network, addr)
			},
		},
		Timeout: 10 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Response JSON: {"player": "...", "download": "..."}
	var res struct {
		Player   string `json:"player"`
		Download string `json:"download"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return string(body), nil
	}
	return res.Player + "\n" + res.Download, nil
}

// extractWindowSRC đọc window.__SRC từ player HTML (kiểu vlstream.net)
// Trả về danh sách ExtractResult trực tiếp không cần AI
func extractWindowSRC(html, pageURL string) []downloader.AIFormatInfo {
	var formats []downloader.AIFormatInfo

	// window.__SRC = [{"message":"OK","title":"02620","label":"Auto","type":"hls","file":"https://...02620.vl"}]
	srcRe := regexp.MustCompile(`window\.__SRC\s*=\s*(\[.*?\]);`)
	match := srcRe.FindStringSubmatch(html)
	if len(match) < 2 {
		return formats
	}

	var srcs []struct {
		Title string `json:"title"`
		Label string `json:"label"`
		Type  string `json:"type"`
		File  string `json:"file"`
	}
	if err := json.Unmarshal([]byte(match[1]), &srcs); err != nil {
		return formats
	}

	for _, s := range srcs {
		if s.File == "" {
			continue
		}
		ext := s.Type
		if ext == "" {
			ext = "mp4"
		}
		quality := s.Label
		if quality == "" {
			quality = "Tự động"
		}
		title := "Video Stream"
		if s.Label != "" {
			title = "Stream " + s.Label
		}
		formats = append(formats, downloader.AIFormatInfo{
			Title:   title,
			URL:     s.File,
			Quality: quality,
			Ext:     ext,
		})
	}
	return formats
}

// extractDirectURLs tìm các link MP4/M3U8/HLS trực tiếp trong HTML bằng regex
func extractDirectURLs(html string) []downloader.AIFormatInfo {
	var formats []downloader.AIFormatInfo
	seen := make(map[string]bool)

	urlRe := regexp.MustCompile(`https?://[^\s"'<>\\#]+?\.(?:mp4|m3u8|mpd|webm|ts|mkv|vl|m3u)[^\s"'<>\\#"']*`)
	matches := urlRe.FindAllString(html, -1)

	for _, u := range matches {
		u = strings.ReplaceAll(u, "\\/", "/")
		// Bỏ qua quảng cáo
		if strings.Contains(u, "google") || strings.Contains(u, "doubleclick") ||
			strings.Contains(u, "facebook") || strings.Contains(u, "analytics") ||
			strings.Contains(u, "cloudflare") {
			continue
		}
		if seen[u] {
			continue
		}
		seen[u] = true

		ext := "mp4"
		if strings.Contains(u, ".m3u8") || strings.Contains(u, ".vl") || strings.Contains(u, ".m3u") {
			ext = "m3u8"
		} else if strings.Contains(u, ".mpd") {
			ext = "mpd"
		} else if strings.Contains(u, ".webm") {
			ext = "webm"
		}

		formats = append(formats, downloader.AIFormatInfo{
			Title:   "Video trực tiếp",
			URL:     u,
			Quality: "Tự động",
			Ext:     ext,
		})
	}
	return formats
}

// extractIframeURLs tìm các link iframe nhúng và các link máy chủ video tiềm năng trong HTML
func (a *App) extractIframeURLs(htmlContent, baseURL string) []string {
	var urls []string

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
			uStr = strings.ReplaceAll(uStr, "\\/", "/")
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

	playerRe := regexp.MustCompile(`(?i)https?://[^\s"'<>\\#]+?(?:dood|streamtape|mixdrop|fembed|voe|vidsrc|vlstream|embed|player)[^\s"'<>\\#]*`)
	playerMatches := playerRe.FindAllString(htmlContent, -1)
	for _, uStr := range playerMatches {
		uStr = strings.ReplaceAll(uStr, "\\/", "/")
		if !seen[uStr] {
			seen[uStr] = true
			urls = append(urls, uStr)
		}
	}

	return urls
}

// AnalyzeWithAI cào trang, phát hiện player nhúng, trích xuất link stream trực tiếp
// Không cần Gemini AI — dùng parser tự viết nhanh và chính xác 100%
func (a *App) AnalyzeWithAI(pageURL string) (*downloader.ExtractResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	// Lấy thumbnail/title từ og:image và <title>
	getTitleAndThumb := func(html string) (string, string) {
		title := ""
		titleRe := regexp.MustCompile(`(?i)<title>(.*?)</title>`)
		if m := titleRe.FindStringSubmatch(html); len(m) > 1 {
			title = strings.TrimSpace(m[1])
			// Bỏ tên site thường đứng sau " - "
			if idx := strings.LastIndex(title, " - "); idx > 0 {
				title = strings.TrimSpace(title[:idx])
			}
		}
		thumb := ""
		thumbRe := regexp.MustCompile(`(?i)<meta\s+property="og:image"\s+content="([^"]+)"`)
		if m := thumbRe.FindStringSubmatch(html); len(m) > 1 {
			thumb = strings.TrimSpace(m[1])
		}
		return title, thumb
	}

	runtime.LogInfof(a.ctx, "Extractor: Tải trang gốc %s", pageURL)
	mainHTML, err := a.fetchHTML(ctx, pageURL, "")
	if err != nil {
		return nil, fmt.Errorf("Lỗi tải trang: %v", err)
	}

	title, thumb := getTitleAndThumb(mainHTML)

	var allFormats []downloader.AIFormatInfo

	// --- Bước 00: Phát hiện trang sử dụng javplayer.cc (ví dụ 123av.com) ---
	if strings.Contains(mainHTML, "javplayer.cc") {
		runtime.LogInfof(a.ctx, "Extractor: Phát hiện javplayer.cc, đang trích xuất videoID...")
		videoID := downloader.ExtractJavplayerVideoID(mainHTML)
		runtime.LogInfof(a.ctx, "Extractor: videoID = %q", videoID)
		if videoID != "" {
			streamAPIURL := fmt.Sprintf("https://javplayer.cc/stream?id=%s", videoID)
			iframeURL := fmt.Sprintf("https://javplayer.cc/e/%s", videoID)
			apiReq, err := http.NewRequestWithContext(ctx, "GET", streamAPIURL, nil)
			if err == nil {
				apiReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
				apiReq.Header.Set("Referer", iframeURL)
				apiReq.Header.Set("Origin", "https://javplayer.cc")
				dialer := downloader.NewDPIBypassDialer()
				apiClient := &http.Client{
					Transport: &http.Transport{
						DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
							return downloader.DPIBypassDialContext(ctx, dialer, network, addr)
						},
					},
					Timeout: 15 * time.Second,
				}
				resp, err := apiClient.Do(apiReq)
				if err == nil {
					defer resp.Body.Close()
					if resp.StatusCode == http.StatusOK {
						var res struct {
							Media struct {
								Stream string `json:"stream"`
							} `json:"media"`
						}
						bodyBytes, _ := io.ReadAll(resp.Body)
						if json.Unmarshal(bodyBytes, &res) == nil && res.Media.Stream != "" {
							runtime.LogInfof(a.ctx, "Extractor: Trích xuất thành công m3u8 từ javplayer.cc: %s", res.Media.Stream)
							allFormats = append(allFormats, downloader.AIFormatInfo{
								Title:   "HLS Stream (Javplayer)",
								URL:     res.Media.Stream,
								Quality: "Tự động",
								Ext:     "m3u8",
							})
						}
					} else {
						runtime.LogErrorf(a.ctx, "Extractor: Javplayer API trả về HTTP %d cho videoID=%q", resp.StatusCode, videoID)
					}
				} else {
					runtime.LogErrorf(a.ctx, "Extractor: Lỗi gọi Javplayer API: %v", err)
				}
			}
		}
	}


	// --- Bước 0: Phát hiện trang supjav.com ---
	if strings.Contains(strings.ToLower(pageURL), "supjav.com") {
		runtime.LogInfof(a.ctx, "Extractor: Phát hiện trang supjav.com, đang chạy trích xuất tự động...")
		// 1. Quét các server links
		serverRe := regexp.MustCompile(`(?i)class=["']btn-server[^"']*["']\s+data-link=["']([^"']+)["']|data-link=["']([^"']+)["']\s+class=["']btn-server[^"']*["']`)
		serverMatches := serverRe.FindAllStringSubmatch(mainHTML, -1)

		// 2. Tìm bg từ class="player" hoặc id="dz_video"
		bgRe := regexp.MustCompile(`bg=["']([^"']+)["']`)
		bgMatch := bgRe.FindStringSubmatch(mainHTML)
		bg := ""
		if len(bgMatch) > 1 {
			bg = bgMatch[1]
		}

		var serverLinks []string
		for _, m := range serverMatches {
			val := ""
			if len(m) > 1 && m[1] != "" {
				val = m[1]
			} else if len(m) > 2 && m[2] != "" {
				val = m[2]
			}
			if val != "" {
				serverLinks = append(serverLinks, val)
			}
		}

		runtime.LogInfof(a.ctx, "Extractor: Tìm thấy %d máy chủ video, bg=%s", len(serverLinks), bg)

		for _, sLink := range serverLinks {
			// 3. Build link đầu tiên
			firstIframeURL := fmt.Sprintf("https://lk1.supremejav.com/supjav.php?l=%s&bg=%s", sLink, bg)
			runtime.LogInfof(a.ctx, "Extractor: Đang quét server iframe cấp 1: %s", firstIframeURL)

			firstHTML, err := a.fetchHTML(ctx, firstIframeURL, pageURL)
			if err != nil {
				continue
			}

			// 4. Tìm OLID
			olidRe := regexp.MustCompile(`var\s+OLID\s*=\s*['"]([^'"]+)['"]`)
			olidMatch := olidRe.FindStringSubmatch(firstHTML)
			if len(olidMatch) < 2 {
				continue
			}
			olid := olidMatch[1]

			// 5. Đảo ngược OLID
			runes := []rune(olid)
			for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
				runes[i], runes[j] = runes[j], runes[i]
			}
			reversedOLID := string(runes)

			// 6. Build link iframe thứ hai
			secondIframeURL := fmt.Sprintf("https://lk1.supremejav.com/supjav.php?c=%s", reversedOLID)
			runtime.LogInfof(a.ctx, "Extractor: Đang quét server iframe cấp 2: %s", secondIframeURL)

			secondHTML, err := a.fetchHTML(ctx, secondIframeURL, firstIframeURL)
			if err != nil {
				continue
			}

			// 7. Trích xuất stream URL (m3u8) từ secondHTML
			m3u8Re := regexp.MustCompile(`(?i)urlPlay\s*=\s*['"]([^'"]+m3u8[^'"]*)['"]|data-hash\s*=\s*['"]([^'"]+m3u8[^'"]*)['"]`)
			m3u8Matches := m3u8Re.FindAllStringSubmatch(secondHTML, -1)

			for _, m := range m3u8Matches {
				streamURL := ""
				if len(m) > 1 && m[1] != "" {
					streamURL = m[1]
				} else if len(m) > 2 && m[2] != "" {
					streamURL = m[2]
				}

				if streamURL != "" {
					allFormats = append(allFormats, downloader.AIFormatInfo{
						Title:   "HLS Stream (Supjav)",
						URL:     streamURL,
						Quality: "Tự động",
						Ext:     "m3u8",
					})
				}
			}

			if len(allFormats) > 0 {
				break
			}
		}
	}

	// --- Bước 1: Phát hiện site dùng AJAX player (kiểu vlxx/vlstream) ---
	idRe := regexp.MustCompile(`data-id=["'](\d+)["']`)
	if idMatch := idRe.FindStringSubmatch(mainHTML); len(idMatch) > 1 {
		videoID := idMatch[1]
		parsedURL, err := url.Parse(pageURL)
		if err == nil {
			ajaxURL := fmt.Sprintf("%s://%s/ajax.php", parsedURL.Scheme, parsedURL.Host)
			runtime.LogInfof(a.ctx, "Extractor: Phát hiện AJAX player, ID=%s, gọi %s", videoID, ajaxURL)

			for _, sv := range []string{"1", "2"} {
				playerHTML, err := a.fetchAjaxPlayer(ctx, ajaxURL, videoID, sv, pageURL)
				if err != nil {
					continue
				}

				// Tìm link nhúng trong player response
				iframeFound := false
				playerIframes := a.extractIframeURLs(playerHTML, pageURL)
				for _, iframeURL := range playerIframes {
					runtime.LogInfof(a.ctx, "Extractor: Tải player embed URL: %s", iframeURL)
					embedHTML, err := a.fetchHTML(ctx, iframeURL, pageURL)
					if err != nil {
						continue
					}

					// Ưu tiên: đọc window.__SRC trước
					srcFormats := extractWindowSRC(embedHTML, pageURL)
					if len(srcFormats) > 0 {
						allFormats = append(allFormats, srcFormats...)
						iframeFound = true
						continue
					}

					// Fallback: tìm URL trực tiếp trong iframe HTML
					directFormats := extractDirectURLs(embedHTML)
					if len(directFormats) > 0 {
						allFormats = append(allFormats, directFormats...)
						iframeFound = true
					}
				}

				// Chỉ scan playerHTML nếu iframe không tìm thấy gì
				if !iframeFound {
					directFormats := extractDirectURLs(playerHTML)
					allFormats = append(allFormats, directFormats...)
				}

				// Nếu đã tìm được stream từ server này → dừng, không thử server tiếp
				if len(allFormats) > 0 {
					break
				}
			}
		}
	}

	// --- Bước 2: Thử tìm URL trực tiếp trong HTML trang gốc ---
	if len(allFormats) == 0 {
		runtime.LogInfof(a.ctx, "Extractor: Tìm URL trực tiếp trong trang gốc")
		directFormats := extractDirectURLs(mainHTML)
		allFormats = append(allFormats, directFormats...)
	}

	// --- Bước 3: Quét iframe thông thường (không AJAX) ---
	if len(allFormats) == 0 {
		iframeURLs := a.extractIframeURLs(mainHTML, pageURL)
		iframeCount := 0
		for _, iframeURL := range iframeURLs {
			if iframeCount >= 3 {
				break
			}
			if strings.Contains(iframeURL, "google") || strings.Contains(iframeURL, "doubleclick") ||
				strings.Contains(iframeURL, "facebook") || strings.Contains(iframeURL, "analytics") ||
				strings.Contains(iframeURL, "adnxs") {
				continue
			}

			runtime.LogInfof(a.ctx, "Extractor: Tải iframe thông thường: %s", iframeURL)
			iframeHTML, err := a.fetchHTML(ctx, iframeURL, pageURL)
			if err != nil {
				continue
			}

			srcFormats := extractWindowSRC(iframeHTML, pageURL)
			if len(srcFormats) > 0 {
				allFormats = append(allFormats, srcFormats...)
			} else {
				directFormats := extractDirectURLs(iframeHTML)
				allFormats = append(allFormats, directFormats...)
			}
			iframeCount++
		}
	}

	// --- Bước 4: Dùng Gemini AI như fallback cuối cùng (nếu có API key) ---
	if len(allFormats) == 0 {
		settings, err := a.db.GetSettings()
		if err == nil && settings.GeminiAPIKey != "" {
			runtime.LogInfof(a.ctx, "Extractor: Không tự trích xuất được, thử Gemini AI...")
			var combinedHTML strings.Builder
			combinedHTML.WriteString(fmt.Sprintf("--- ORIGINAL PAGE URL: %s ---\n", pageURL))
			combinedHTML.WriteString(mainHTML)
			aiResult, err := downloader.ExtractMediaWithGemini(ctx, pageURL, combinedHTML.String(), settings.GeminiAPIKey, settings.GeminiModel)
			if err == nil {
				return aiResult, nil
			}
			runtime.LogErrorf(a.ctx, "Gemini AI fallback lỗi: %v", err)
		}
		return nil, fmt.Errorf("Không tìm thấy link video trực tiếp nào trên trang này")
	}

	// Loại bỏ duplicate URL
	seenURL := make(map[string]bool)
	var uniqueFormats []downloader.AIFormatInfo
	for _, f := range allFormats {
		if !seenURL[f.URL] {
			seenURL[f.URL] = true
			uniqueFormats = append(uniqueFormats, f)
		}
	}

	return &downloader.ExtractResult{
		Title:     title,
		Thumbnail: thumb,
		Formats:   uniqueFormats,
	}, nil
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
