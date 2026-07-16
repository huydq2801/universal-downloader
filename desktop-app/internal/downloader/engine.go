package downloader

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// DownloadRequest là yêu cầu tải từ extension hoặc UI
type DownloadRequest struct {
	URL      string            `json:"url"`
	PageURL  string            `json:"pageUrl"`
	Title    string            `json:"title"`
	Type     string            `json:"type"`    // video, hls, dash, audio, blob
	Quality  string            `json:"quality"` // best, 1080, 720, 480, 360, audio
	Format   string            `json:"format"`  // mp4, webm, mp3
	SavePath string            `json:"savePath"`
	Headers  map[string]string `json:"headers"`
	Cookies  string            `json:"cookies"`
	BlobData string            `json:"blobData"` // base64 nếu là blob URL
}

// MediaInfo thông tin về media (từ yt-dlp --dump-json)
type MediaInfo struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Description string       `json:"description"`
	Duration    float64      `json:"duration"`
	Thumbnail   string       `json:"thumbnail"`
	Uploader    string       `json:"uploader"`
	Formats     []FormatInfo `json:"formats"`
	URL         string       `json:"webpage_url"`
}

// FormatInfo thông tin một format/quality
type FormatInfo struct {
	FormatID   string  `json:"format_id"`
	FormatNote string  `json:"format_note"`
	Extension  string  `json:"ext"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	FPS        float64 `json:"fps"`
	VCodec     string  `json:"vcodec"`
	ACodec     string  `json:"acodec"`
	Filesize   int64   `json:"filesize"`
	TBR        float64 `json:"tbr"`
	URL        string  `json:"url"`
}

// ProgressCallback callback khi có progress update
type ProgressCallback func(progress float64, speed, eta, status string)

// Engine là download engine chính
type Engine struct {
	toolsDir string
	ytdlp    string
	ffmpeg   string
}

// NewEngine tạo Engine mới
func NewEngine(toolsDir string) *Engine {
	e := &Engine{toolsDir: toolsDir}
	e.ytdlp = e.findTool("yt-dlp")
	e.ffmpeg = e.findTool("ffmpeg")
	return e
}

func (e *Engine) ToolsDir() string { return e.toolsDir }

func (e *Engine) SetPaths(ytdlp, ffmpeg string) {
	if ytdlp != "" && ytdlp != "yt-dlp" {
		e.ytdlp = ytdlp
	}
	if ffmpeg != "" && ffmpeg != "ffmpeg" {
		e.ffmpeg = ffmpeg
	}
}

func (e *Engine) findTool(name string) string {
	localPath := filepath.Join(e.toolsDir, name+".exe")
	if _, err := os.Stat(localPath); err == nil {
		return localPath
	}
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	return name
}

func (e *Engine) CheckTools() map[string]bool {
	result := map[string]bool{}
	
	cmd1 := exec.Command(e.ytdlp, "--version")
	cmd1.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if cmd1.Run() == nil {
		result["ytdlp"] = true
	}
	
	cmd2 := exec.Command(e.ffmpeg, "-version")
	cmd2.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if cmd2.Run() == nil {
		result["ffmpeg"] = true
	}
	
	return result
}

// GetInfo lấy thông tin media từ URL
func (e *Engine) GetInfo(url string) (*MediaInfo, error) {
	args := []string{"--dump-json", "--no-playlist", "--no-warnings", url}
	cmd := exec.Command(e.ytdlp, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp error: %v", err)
	}
	var info MediaInfo
	if err := json.Unmarshal(out, &info); err != nil {
		return nil, fmt.Errorf("parse error: %v", err)
	}
	return &info, nil
}

// ===== DOMAIN ROUTING =====

// Các trang yt-dlp hỗ trợ đầy đủ → dùng pageUrl thay vì stream URL
var ytdlpSupportedPageDomains = []string{
	"youtube.com", "youtu.be",
	"tiktok.com", "vm.tiktok.com",
	"instagram.com",
	"facebook.com", "fb.watch",
	"twitter.com", "x.com",
	"twitch.tv", "clips.twitch.tv",
	"vimeo.com",
	"dailymotion.com",
	"bilibili.com",
	"nicovideo.jp",
	"soundcloud.com",
	"reddit.com",
	"pornhub.com",
	"xvideos.com",
	"xnxx.com",
	"xhamster.com",
	"streamable.com",
	"rumble.com",
	"odysee.com",
	"bandcamp.com",
	"mixcloud.com",
	"ted.com",
	"naver.com",
	"vlive.tv",
}

// CDN stream domain của các trang supported - URL này hết hạn nhanh
var expiredStreamPatterns = []string{
	"googlevideo.com",    // YouTube
	"videoplayback",      // YouTube
	"fbcdn.net",          // Facebook CDN
	"cdninstagram.com",   // Instagram CDN
	"video.twimg.com",    // Twitter video CDN
	"media.discordapp.net", // Discord
	"cdn.discordapp.com",
}

func isExpiredStreamURL(url string) bool {
	for _, p := range expiredStreamPatterns {
		if strings.Contains(url, p) {
			return true
		}
	}
	return false
}

func matchesDomain(url string, domains []string) bool {
	// Normalize: remove www.
	lower := strings.ToLower(url)
	for _, d := range domains {
		if strings.Contains(lower, d) {
			return true
		}
	}
	return false
}

func isYtdlpSupportedPage(pageURL string) bool {
	return matchesDomain(pageURL, ytdlpSupportedPageDomains)
}

// isDirectMediaURL kiểm tra có phải URL file media trực tiếp không
func isDirectMediaURL(url string) bool {
	lower := strings.ToLower(url)
	exts := []string{".mp4", ".webm", ".mkv", ".avi", ".mov", ".flv",
		".mp3", ".aac", ".ogg", ".wav", ".m3u8", ".mpd"}
	for _, ext := range exts {
		if strings.Contains(lower, ext) {
			return true
		}
	}
	return false
}

// ===== MAIN DOWNLOAD ROUTER =====

// Download tải một URL - tự động chọn phương thức phù hợp
func (e *Engine) Download(ctx context.Context, req DownloadRequest, savePath string, onProgress ProgressCallback) error {
	// Blob data
	if req.BlobData != "" && strings.HasPrefix(req.URL, "blob:") {
		return e.saveBlobData(req.BlobData, savePath)
	}

	// --- ROUTING LOGIC ---
	downloadURL := req.URL

	// Case 1: URL là stream CDN hết hạn nhanh VÀ pageUrl là trang supported
	// → dùng pageUrl để yt-dlp tự lấy URL mới
	if isExpiredStreamURL(req.URL) && isYtdlpSupportedPage(req.PageURL) {
		downloadURL = req.PageURL
		return e.runYtdlp(ctx, req, downloadURL, savePath, onProgress)
	}

	// Case 2: pageUrl là trang yt-dlp supported (YouTube, TikTok, ...)
	// → luôn dùng yt-dlp với pageUrl để đảm bảo chất lượng tốt nhất
	if isYtdlpSupportedPage(req.PageURL) {
		downloadURL = req.PageURL
		return e.runYtdlp(ctx, req, downloadURL, savePath, onProgress)
	}

	// Case 3: URL là HLS/DASH stream → dùng yt-dlp hoặc ffmpeg
	if req.Type == "hls" || req.Type == "dash" ||
		strings.Contains(req.URL, ".m3u8") || strings.Contains(req.URL, ".mpd") {
		return e.runYtdlp(ctx, req, req.URL, savePath, onProgress)
	}

	// Case 4: URL là file media trực tiếp từ trang không được hỗ trợ bởi yt-dlp
	// → thử direct HTTP download trước
	if isDirectMediaURL(req.URL) {
		err := e.directDownload(ctx, req, savePath, onProgress)
		if err == nil {
			return nil
		}
		// Fallback: thử yt-dlp
		if onProgress != nil {
			onProgress(0, "", "", "retrying with yt-dlp")
		}
		return e.runYtdlp(ctx, req, req.URL, savePath, onProgress)
	}

	// Case 5: Mọi trường hợp khác → thử yt-dlp trước, nếu fail → direct download
	err := e.runYtdlp(ctx, req, req.URL, savePath, onProgress)
	if err != nil {
		// Fallback: direct download
		if onProgress != nil {
			onProgress(0, "", "", "fallback to direct download")
		}
		return e.directDownload(ctx, req, savePath, onProgress)
	}
	return nil
}

// ===== YT-DLP RUNNER =====

func (e *Engine) runYtdlp(ctx context.Context, req DownloadRequest, targetURL string, savePath string, onProgress ProgressCallback) error {
	args := e.buildYtdlpArgs(req, targetURL, savePath)
	cmd := exec.CommandContext(ctx, e.ytdlp, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("không thể khởi động yt-dlp: %v\nĐường dẫn: %s", err, e.ytdlp)
	}

	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		line := scanner.Text()
		progress, speed, eta := parseYtdlpProgress(line)
		if progress >= 0 && onProgress != nil {
			onProgress(progress, speed, eta, "downloading")
		}
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("yt-dlp thất bại: %v", err)
	}
	return nil
}

func (e *Engine) buildYtdlpArgs(req DownloadRequest, targetURL, savePath string) []string {
	args := []string{
		"--no-playlist",
		"--newline",
		"--progress",
		"--progress-template", "%(progress.downloaded_bytes)s|%(progress.total_bytes)s|%(progress.speed)s|%(progress.eta)s|%(progress._percent_str)s",
	}

	// Chuẩn hoá quality: bỏ đuôi 'p' (vd "720p" → "720")
	quality := strings.TrimSuffix(strings.ToLower(req.Quality), "p")

	switch quality {
	case "audio":
		args = append(args, "-x", "--audio-format", "mp3", "--audio-quality", "0")
	case "best", "":
		args = append(args, "-f", "bestvideo[ext=mp4]+bestaudio[ext=m4a]/bestvideo+bestaudio/best")
	case "1080":
		args = append(args, "-f", "bestvideo[height<=1080][ext=mp4]+bestaudio[ext=m4a]/bestvideo[height<=1080]+bestaudio/best[height<=1080]")
	case "720":
		args = append(args, "-f", "bestvideo[height<=720][ext=mp4]+bestaudio[ext=m4a]/bestvideo[height<=720]+bestaudio/best[height<=720]")
	case "480":
		args = append(args, "-f", "bestvideo[height<=480]+bestaudio/best[height<=480]")
	case "360":
		args = append(args, "-f", "bestvideo[height<=360]+bestaudio/best[height<=360]")
	default:
		args = append(args, "-f", fmt.Sprintf("bestvideo[height<=%s]+bestaudio/best[height<=%s]", quality, quality))
	}

	if req.Format != "" && quality != "audio" {
		args = append(args, "--merge-output-format", req.Format)
	}

	// Output path
	fileName := sanitizeFilename(req.Title)
	if fileName == "" {
		fileName = "%(title)s"
	}
	args = append(args, "-o", filepath.Join(savePath, fileName+".%(ext)s"))

	// Referer từ pageUrl (giúp bypass hotlink protection)
	if req.PageURL != "" {
		args = append(args, "--referer", req.PageURL)
		args = append(args, "--add-header", "Origin: "+extractOrigin(req.PageURL))
	}

	// Custom headers
	for key, val := range req.Headers {
		args = append(args, "--add-header", fmt.Sprintf("%s: %s", key, val))
	}

	// Cookies
	if req.Cookies != "" {
		args = append(args, "--cookies", req.Cookies)
	}

	// ffmpeg location
	if e.ffmpeg != "ffmpeg" {
		args = append(args, "--ffmpeg-location", filepath.Dir(e.ffmpeg))
	}

	args = append(args, targetURL)
	return args
}

// ===== DIRECT HTTP DOWNLOAD =====

func (e *Engine) directDownload(ctx context.Context, req DownloadRequest, savePath string, onProgress ProgressCallback) error {
	client := &http.Client{Timeout: 0} // No timeout for large files

	httpReq, err := http.NewRequestWithContext(ctx, "GET", req.URL, nil)
	if err != nil {
		return fmt.Errorf("tạo request lỗi: %v", err)
	}

	// Set headers để bypass hotlink protection
	httpReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	if req.PageURL != "" {
		httpReq.Header.Set("Referer", req.PageURL)
		httpReq.Header.Set("Origin", extractOrigin(req.PageURL))
	}
	for key, val := range req.Headers {
		httpReq.Header.Set(key, val)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("HTTP request lỗi: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 206 {
		return fmt.Errorf("HTTP %d: server từ chối request", resp.StatusCode)
	}

	// Xác định tên file và extension
	fileName := sanitizeFilename(req.Title)
	if fileName == "" {
		fileName = "download_" + fmt.Sprintf("%d", time.Now().Unix())
	}
	ext := guessExtFromContentType(resp.Header.Get("Content-Type"), req.URL)
	filePath := filepath.Join(savePath, fileName+ext)

	// Tạo file
	f, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("tạo file lỗi: %v", err)
	}
	defer f.Close()

	totalBytes := resp.ContentLength
	var downloadedBytes int64

	// Stream download với progress
	buf := make([]byte, 32*1024) // 32KB buffer
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := f.Write(buf[:n]); writeErr != nil {
				return fmt.Errorf("ghi file lỗi: %v", writeErr)
			}
			downloadedBytes += int64(n)

			if onProgress != nil && totalBytes > 0 {
				pct := float64(downloadedBytes) / float64(totalBytes) * 100
				speed := fmt.Sprintf("%.1f MB/s", float64(n)/1024/1024)
				onProgress(pct, speed, "", "downloading")
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("đọc stream lỗi: %v", readErr)
		}
	}

	return nil
}

// ===== HELPERS =====

func extractOrigin(pageURL string) string {
	if idx := strings.Index(pageURL, "://"); idx >= 0 {
		rest := pageURL[idx+3:]
		if slashIdx := strings.Index(rest, "/"); slashIdx >= 0 {
			return pageURL[:idx+3] + rest[:slashIdx]
		}
		return pageURL
	}
	return pageURL
}

func guessExtFromContentType(ct, url string) string {
	ct = strings.ToLower(ct)
	if strings.Contains(ct, "mp4") || strings.Contains(ct, "mpeg4") {
		return ".mp4"
	}
	if strings.Contains(ct, "webm") {
		return ".webm"
	}
	if strings.Contains(ct, "x-matroska") {
		return ".mkv"
	}
	if strings.Contains(ct, "mpeg") || strings.Contains(ct, "audio/mpeg") {
		return ".mp3"
	}
	if strings.Contains(ct, "aac") {
		return ".aac"
	}
	if strings.Contains(ct, "ogg") {
		return ".ogg"
	}
	// Guess from URL
	lower := strings.ToLower(url)
	for _, ext := range []string{".mp4", ".webm", ".mkv", ".mp3", ".aac", ".m4a", ".mov", ".flv"} {
		if strings.Contains(lower, ext) {
			return ext
		}
	}
	return ".mp4"
}

// saveBlobData lưu base64 blob data thành file
func (e *Engine) saveBlobData(base64Data, savePath string) error {
	parts := strings.SplitN(base64Data, ",", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid blob data format")
	}
	mimeStr := parts[0]
	ext := ".mp4"
	if strings.Contains(mimeStr, "webm") {
		ext = ".webm"
	} else if strings.Contains(mimeStr, "audio") {
		ext = ".mp3"
	}
	decoded, err := base64Decode(parts[1])
	if err != nil {
		return fmt.Errorf("base64 decode error: %v", err)
	}
	filePath := filepath.Join(savePath, fmt.Sprintf("blob_%d%s", time.Now().Unix(), ext))
	return os.WriteFile(filePath, decoded, 0644)
}

// parseYtdlpProgress parse progress từ output của yt-dlp
func parseYtdlpProgress(line string) (float64, string, string) {
	parts := strings.Split(line, "|")
	if len(parts) >= 5 {
		percentStr := strings.TrimSpace(strings.TrimSuffix(parts[4], "%"))
		var pct float64
		fmt.Sscanf(percentStr, "%f", &pct)
		return pct, strings.TrimSpace(parts[2]), strings.TrimSpace(parts[3])
	}
	if strings.Contains(line, "[download]") && strings.Contains(line, "%") {
		var pct float64
		if _, err := fmt.Sscanf(line, "[download] %f%%", &pct); err == nil {
			return pct, "", ""
		}
	}
	return -1, "", ""
}

// sanitizeFilename làm sạch tên file
func sanitizeFilename(name string) string {
	if name == "" {
		return ""
	}
	invalid := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|"}
	result := name
	for _, c := range invalid {
		result = strings.ReplaceAll(result, c, "_")
	}
	if len(result) > 100 {
		result = result[:100]
	}
	return strings.TrimSpace(result)
}
