package downloader

import (
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
	threads  int
}

// NewEngine tạo Engine mới
func NewEngine(toolsDir string) *Engine {
	e := &Engine{toolsDir: toolsDir, threads: 8}
	e.ytdlp = e.findTool("yt-dlp")
	e.ffmpeg = e.findTool("ffmpeg")
	return e
}

func (e *Engine) SetThreads(threads int) {
	if threads > 0 {
		e.threads = threads
	}
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

// isHLSStreamURL kiểm tra có phải HLS/m3u8/vl stream không
func isHLSStreamURL(u string) bool {
	lower := strings.ToLower(u)
	return strings.Contains(lower, ".m3u8") ||
		strings.Contains(lower, ".m3u") ||
		strings.HasSuffix(strings.Split(lower, "?")[0], ".vl") ||
		strings.Contains(lower, "/manifest") ||
		strings.Contains(lower, "/hls/")
}

// isDirectMediaURL kiểm tra có phải URL file media trực tiếp không
func isDirectMediaURL(url string) bool {
	lower := strings.ToLower(url)
	exts := []string{".mp4", ".webm", ".mkv", ".avi", ".mov", ".flv",
		".mp3", ".aac", ".ogg", ".wav", ".m3u8", ".mpd", ".vl"}
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

	// Case 0: URL là HLS stream từ CDN Javplayer đã hết hạn (wowstream2.cloud, v.v.)
	// → tái-lấy URL stream mới bằng cách extract videoID từ URL, sau đó gọi lại Javplayer API
	if isJavplayerExpiredURL(req.URL) {
		if onProgress != nil {
			onProgress(0, "", "", "refreshing stream URL...")
		}
		refreshed := false

		// Bước 1: Thử extract videoID trực tiếp từ URL CDN wowstream (nhanh nhất, không cần pageURL)
		videoID := extractVideoIDFromWowstreamURL(req.URL)
		if videoID != "" {
			freshURL, err := e.fetchJavplayerStreamURL(ctx, videoID)
			if err == nil && freshURL != "" {
				req.URL = freshURL
				downloadURL = freshURL
				refreshed = true
			}
		}

		// Bước 2: Nếu không extract được từ URL, thử tải lại HTML từ pageURL
		if !refreshed && req.PageURL != "" && isJavplayerPage(req.PageURL) {
			freshURL, err := e.RefreshJavplayerURL(ctx, req.PageURL)
			if err == nil && freshURL != "" {
				req.URL = freshURL
				downloadURL = freshURL
			}
		}
	}

	// Case 3: URL là HLS/DASH/VL stream → dùng custom Go downloader (nhanh, chính xác, giải mã PNG tự động)
	if req.Type == "hls" || req.Type == "dash" || req.Type == "m3u8" || isHLSStreamURL(req.URL) {
		return e.downloadHLSWithGo(ctx, req, savePath, onProgress)
	}

	// Case 4: URL là file media trực tiếp từ trang không được hỗ trợ bởi yt-dlp
	// → thử direct HTTP download trước (đa luồng song song)
	if isDirectMediaURL(req.URL) {
		err := e.downloadParallel(ctx, req, savePath, e.threads, onProgress)
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
		return e.downloadParallel(ctx, req, savePath, e.threads, onProgress)
	}
	return nil
}

// ===== FFMPEG HLS DOWNLOADER =====

// runFFmpegHLS dùng ffmpeg trực tiếp để tải HLS/m3u8/vl stream
// Nhanh hơn, đơn giản hơn, không có lối yt-dlp compatibility
func (e *Engine) runFFmpegHLS(ctx context.Context, req DownloadRequest, savePath string, onProgress ProgressCallback) error {
	fileName := sanitizeFilename(req.Title)
	if fileName == "" {
		fileName = fmt.Sprintf("video_%d", time.Now().Unix())
	}
	outPath := filepath.Join(savePath, fileName+".mp4")

	// Xây dựng header string cho ffmpeg
	headers := ""
	if req.PageURL != "" {
		headers += fmt.Sprintf("Referer: %s\r\n", req.PageURL)
		headers += fmt.Sprintf("Origin: %s\r\n", extractOrigin(req.PageURL))
	}
	headers += "User-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36\r\n"
	// Custom headers
	for k, v := range req.Headers {
		headers += fmt.Sprintf("%s: %s\r\n", k, v)
	}

	args := []string{
		"-y",                          // ghi đè file nếu tồn tại
		"-loglevel", "error",           // chỉ in lỗi thực sự
		"-extension_picky", "0",        // tắt lọc đuôi mở rộng khắt khe đối với HLS segment
		"-allowed_extensions", "ALL",   // cho phép tất cả các loại đuôi mở rộng
		"-allowed_segment_extensions", "ALL", // cho phép mọi loại đuôi segment
		"-progress", "pipe:2",          // gửi progress ra stderr
		"-stats_period", "1",           // cập nhật mỗi 1 giây
	}
	if headers != "" {
		args = append(args, "-headers", headers)
	}
	args = append(args,
		"-i", req.URL,
		"-c", "copy",                   // copy codec, không re-encode
		"-movflags", "+faststart",      // web-friendly mp4
		outPath,
	)

	cmd := exec.CommandContext(ctx, e.ffmpeg, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	// Đọc progress từ stderr
	pipeR, pipeW, err := os.Pipe()
	if err != nil {
		return err
	}
	cmd.Stderr = pipeW

	if err := cmd.Start(); err != nil {
		pipeW.Close()
		pipeR.Close()
		return fmt.Errorf("không thể khởi động ffmpeg: %v", err)
	}
	pipeW.Close()

	// Parse ffmpeg progress (format: key=value\n)
	buf := make([]byte, 4096)
	var fragment strings.Builder
	var totalDuration float64
	var errorLogs []string
	for {
		n, readErr := pipeR.Read(buf)
		if n > 0 {
			for _, b := range buf[:n] {
				if b == '\n' {
					line := strings.TrimSpace(fragment.String())
					fragment.Reset()
					if line == "" {
						continue
					}
					parts := strings.SplitN(line, "=", 2)
					if len(parts) != 2 {
						// Quét tìm thông tin Duration từ dòng log tiêu chuẩn của FFmpeg
						if strings.Contains(line, "Duration:") {
							idx := strings.Index(line, "Duration:")
							rest := strings.TrimSpace(line[idx+len("Duration:"):])
							commaIdx := strings.Index(rest, ",")
							if commaIdx > 0 {
								durationStr := strings.TrimSpace(rest[:commaIdx])
								totalDuration = parseFFmpegTime(durationStr)
							}
						}

						if len(errorLogs) < 100 {
							errorLogs = append(errorLogs, line)
						} else {
							errorLogs = append(errorLogs[1:], line)
						}
						continue
					}
					key, val := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
					switch key {
					case "duration":
						// format: HH:MM:SS.ms
						totalDuration = parseFFmpegTime(val)
					case "out_time_ms":
						if totalDuration > 0 && onProgress != nil {
							var outMs float64
							fmt.Sscanf(val, "%f", &outMs)
							pct := (outMs / 1_000_000) / totalDuration * 100
							if pct > 100 {
								pct = 100
							}
							onProgress(pct, "", "", "downloading")
						}
					case "speed":
						if onProgress != nil && val != "0x" {
							onProgress(-1, val, "", "downloading")
						}
					}
				} else {
					fragment.WriteByte(b)
				}
			}
		}
		if readErr != nil {
			break
		}
	}
	pipeR.Close()

	if err := cmd.Wait(); err != nil {
		if len(errorLogs) > 0 {
			return fmt.Errorf("ffmpeg thất bại: %v\nChi tiết:\n%s", err, strings.Join(errorLogs, "\n"))
		}
		return fmt.Errorf("ffmpeg thất bại: %v", err)
	}
	return nil
}

// parseFFmpegTime parse thời gian HH:MM:SS.ms thành giây
func parseFFmpegTime(s string) float64 {
	var h, m, sec float64
	if n, _ := fmt.Sscanf(s, "%f:%f:%f", &h, &m, &sec); n == 3 {
		return h*3600 + m*60 + sec
	}
	return 0
}

// ===== YT-DLP RUNNER =====

func (e *Engine) runYtdlp(ctx context.Context, req DownloadRequest, targetURL string, savePath string, onProgress ProgressCallback) error {
	args := e.buildYtdlpArgs(req, targetURL, savePath)
	cmd := exec.CommandContext(ctx, e.ytdlp, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	// yt-dlp gửi progress ra stderr (HLS dùng \r, không phải \n)
	pipeR, pipeW, err := os.Pipe()
	if err != nil {
		return err
	}
	cmd.Stdout = pipeW
	cmd.Stderr = pipeW

	if err := cmd.Start(); err != nil {
		pipeW.Close()
		pipeR.Close()
		return fmt.Errorf("không thể khởi động yt-dlp: %v\nĐường dẫn: %s", err, e.ytdlp)
	}
	pipeW.Close() // writer đóng ở parent process

	// Đọc output theo từng token \r hoặc \n
	buf := make([]byte, 4096)
	var fragment strings.Builder
	for {
		n, readErr := pipeR.Read(buf)
		if n > 0 {
			for _, b := range buf[:n] {
				if b == '\r' || b == '\n' {
					line := fragment.String()
					fragment.Reset()
					if line != "" {
						progress, speed, eta := parseYtdlpProgress(line)
						if progress >= 0 && onProgress != nil {
							onProgress(progress, speed, eta, "downloading")
						}
					}
				} else {
					fragment.WriteByte(b)
				}
			}
		}
		if readErr != nil {
			break
		}
	}
	pipeR.Close()

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

	// Với HLS/VL stream: bắt buộc dùng ffmpeg downloader để ghép segment
	isHLS := isHLSStreamURL(targetURL) || req.Type == "hls" || req.Type == "m3u8"
	if isHLS {
		args = append(args, "--downloader", "ffmpeg", "--hls-use-mpegts")
	}

	// Chuẩn hoá quality: bỏ đuôi 'p' (vd "720p" → "720")
	quality := strings.TrimSuffix(strings.ToLower(req.Quality), "p")

	switch quality {
	case "audio":
		args = append(args, "-x", "--audio-format", "mp3", "--audio-quality", "0")
	case "best", "":
		if !isHLS {
			args = append(args, "-f", "bestvideo[ext=mp4]+bestaudio[ext=m4a]/bestvideo+bestaudio/best")
		}
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
// Hỗ trợ cả format pipe template và format [download] XX%
func parseYtdlpProgress(line string) (float64, string, string) {
	line = strings.TrimSpace(line)
	// Format: downloaded|total|speed|eta|percent
	parts := strings.Split(line, "|")
	if len(parts) >= 5 {
		percentStr := strings.TrimSpace(strings.TrimSuffix(parts[4], "%"))
		var pct float64
		fmt.Sscanf(percentStr, "%f", &pct)
		speed := strings.TrimSpace(parts[2])
		eta := strings.TrimSpace(parts[3])
		if speed == "None" || speed == "N/A" {
			speed = ""
		}
		if eta == "None" || eta == "N/A" {
			eta = ""
		}
		return pct, speed, eta
	}
	// Format: [download]  5.5% of ~2.00GiB at 42.00MiB/s ETA 00:48
	if strings.Contains(line, "[download]") && strings.Contains(line, "%") {
		// Tìm phần trăm
		idx := strings.Index(line, "[download]")
		rest := line[idx+len("[download]"):]
		var pct float64
		if _, err := fmt.Sscanf(strings.TrimSpace(rest), "%f%%", &pct); err == nil {
			speed := ""
			eta := ""
			if atIdx := strings.Index(rest, " at "); atIdx >= 0 {
				speedEta := rest[atIdx+4:]
				if etaIdx := strings.Index(speedEta, " ETA "); etaIdx >= 0 {
					speed = strings.TrimSpace(speedEta[:etaIdx])
					eta = strings.TrimSpace(speedEta[etaIdx+5:])
				} else {
					speed = strings.Fields(speedEta)[0]
				}
			}
			return pct, speed, eta
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
