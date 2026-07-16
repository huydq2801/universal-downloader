package downloader

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Duration    float64        `json:"duration"`
	Thumbnail   string         `json:"thumbnail"`
	Uploader    string         `json:"uploader"`
	Formats     []FormatInfo   `json:"formats"`
	URL         string         `json:"webpage_url"`
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

// ToolsDir trả về thư mục công cụ
func (e *Engine) ToolsDir() string {
	return e.toolsDir
}

// SetPaths thiết lập đường dẫn tùy chỉnh cho yt-dlp và ffmpeg
func (e *Engine) SetPaths(ytdlp, ffmpeg string) {
	if ytdlp != "" && ytdlp != "yt-dlp" {
		e.ytdlp = ytdlp
	}
	if ffmpeg != "" && ffmpeg != "ffmpeg" {
		e.ffmpeg = ffmpeg
	}
}

// findTool tìm executable tool
func (e *Engine) findTool(name string) string {
	// Tìm trong tools dir trước
	localPath := filepath.Join(e.toolsDir, name+".exe")
	if _, err := os.Stat(localPath); err == nil {
		return localPath
	}

	// Tìm trong PATH
	path, err := exec.LookPath(name)
	if err == nil {
		return path
	}

	return name // Trả về tên, để lỗi khi chạy nếu không tìm thấy
}

// CheckTools kiểm tra yt-dlp và ffmpeg có sẵn không
func (e *Engine) CheckTools() map[string]bool {
	result := map[string]bool{}

	// Kiểm tra yt-dlp
	cmd := exec.Command(e.ytdlp, "--version")
	if err := cmd.Run(); err == nil {
		result["ytdlp"] = true
	}

	// Kiểm tra ffmpeg
	cmd = exec.Command(e.ffmpeg, "-version")
	if err := cmd.Run(); err == nil {
		result["ffmpeg"] = true
	}

	return result
}

// GetInfo lấy thông tin media từ URL
func (e *Engine) GetInfo(url string) (*MediaInfo, error) {
	args := []string{
		"--dump-json",
		"--no-playlist",
		"--no-warnings",
		url,
	}

	out, err := exec.Command(e.ytdlp, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp error: %v", err)
	}

	var info MediaInfo
	if err := json.Unmarshal(out, &info); err != nil {
		return nil, fmt.Errorf("parse error: %v", err)
	}

	return &info, nil
}

// isYouTubeStreamURL kiểm tra có phải stream URL tạm thời của YouTube không
func isYouTubeStreamURL(url string) bool {
	return strings.Contains(url, "googlevideo.com") ||
		strings.Contains(url, "videoplayback")
}

// isYouTubePage kiểm tra pageUrl có phải YouTube không
func isYouTubePage(pageURL string) bool {
	return strings.Contains(pageURL, "youtube.com/watch") ||
		strings.Contains(pageURL, "youtu.be/")
}

// Download tải một URL với progress callback
func (e *Engine) Download(ctx context.Context, req DownloadRequest, savePath string, onProgress ProgressCallback) error {
	// Nếu là blob data, lưu trực tiếp
	if req.BlobData != "" && strings.HasPrefix(req.URL, "blob:") {
		return e.saveBlobData(req.BlobData, savePath)
	}

	// === QUAN TRỌNG ===
	// YouTube stream URL (googlevideo.com) có TTL rất ngắn và cần auth phức tạp.
	// Thay vào đó, dùng yt-dlp với YouTube page URL để tải đúng cách.
	if isYouTubeStreamURL(req.URL) && isYouTubePage(req.PageURL) {
		req.URL = req.PageURL // Dùng page URL để yt-dlp tự xử lý
	}

	// Xây dựng yt-dlp command
	args := e.buildYtdlpArgs(req, savePath)

	cmd := exec.CommandContext(ctx, e.ytdlp, args...)

	// Capture stderr cho progress
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("không thể khởi động yt-dlp: %v\nĐường dẫn: %s\nHãy tải yt-dlp tại: https://github.com/yt-dlp/yt-dlp/releases", err, e.ytdlp)
	}

	// Parse progress từ stderr
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		line := scanner.Text()
		progress, speed, eta := parseYtdlpProgress(line)
		if progress >= 0 && onProgress != nil {
			onProgress(progress, speed, eta, "downloading")
		}
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("tải thất bại: %v", err)
	}

	return nil
}

// buildYtdlpArgs xây dựng arguments cho yt-dlp
func (e *Engine) buildYtdlpArgs(req DownloadRequest, savePath string) []string {
	args := []string{
		"--no-playlist",
		"--newline",
		"--progress",
		"--progress-template", "%(progress.downloaded_bytes)s|%(progress.total_bytes)s|%(progress.speed)s|%(progress.eta)s|%(progress._percent_str)s",
	}

	// Chuẩn hoá quality: bỏ đuôi 'p' nếu có (vd "720p" -> "720")
	quality := strings.TrimSuffix(strings.ToLower(req.Quality), "p")

	// Format/Quality selection
	switch quality {
	case "audio":
		args = append(args, "-x", "--audio-format", "mp3", "--audio-quality", "0")
	case "best", "":
		// Ưu tiên mp4 có sẵn, nếu không merge video+audio
		args = append(args,
			"-f", "bestvideo[ext=mp4]+bestaudio[ext=m4a]/bestvideo+bestaudio/best")
	case "1080":
		args = append(args,
			"-f", "bestvideo[height<=1080][ext=mp4]+bestaudio[ext=m4a]/bestvideo[height<=1080]+bestaudio/best[height<=1080]")
	case "720":
		args = append(args,
			"-f", "bestvideo[height<=720][ext=mp4]+bestaudio[ext=m4a]/bestvideo[height<=720]+bestaudio/best[height<=720]")
	case "480":
		args = append(args,
			"-f", "bestvideo[height<=480]+bestaudio/best[height<=480]")
	case "360":
		args = append(args,
			"-f", "bestvideo[height<=360]+bestaudio/best[height<=360]")
	default:
		// Giá trị khác: thử parse số
		args = append(args,
			"-f", fmt.Sprintf("bestvideo[height<=%s]+bestaudio/best[height<=%s]", quality, quality))
	}

	// Output format container (merge)
	if req.Format != "" && quality != "audio" {
		args = append(args, "--merge-output-format", req.Format)
	}

	// Output path
	fileName := sanitizeFilename(req.Title)
	if fileName == "" {
		fileName = "%(title)s"
	}
	outputTemplate := filepath.Join(savePath, fileName+".%(ext)s")
	args = append(args, "-o", outputTemplate)

	// Custom headers
	for key, val := range req.Headers {
		args = append(args, "--add-header", fmt.Sprintf("%s: %s", key, val))
	}

	// Cookies file
	if req.Cookies != "" {
		args = append(args, "--cookies", req.Cookies)
	}

	// ffmpeg location
	if e.ffmpeg != "ffmpeg" {
		args = append(args, "--ffmpeg-location", filepath.Dir(e.ffmpeg))
	}

	args = append(args, req.URL)
	return args
}

// saveBlobData lưu base64 blob data thành file
func (e *Engine) saveBlobData(base64Data, savePath string) error {
	// Decode base64 data URL (data:video/mp4;base64,XXXXX)
	parts := strings.SplitN(base64Data, ",", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid blob data format")
	}

	// Lấy MIME type
	mimeStr := parts[0] // "data:video/mp4;base64"
	ext := ".mp4"
	if strings.Contains(mimeStr, "webm") {
		ext = ".webm"
	} else if strings.Contains(mimeStr, "audio") {
		ext = ".mp3"
	}

	// Decode base64
	decoded, err := base64Decode(parts[1])
	if err != nil {
		return fmt.Errorf("base64 decode error: %v", err)
	}

	// Tạo file
	fileName := fmt.Sprintf("blob_%d%s", time.Now().Unix(), ext)
	filePath := filepath.Join(savePath, fileName)
	return os.WriteFile(filePath, decoded, 0644)
}

// parseYtdlpProgress parse progress từ output của yt-dlp
func parseYtdlpProgress(line string) (float64, string, string) {
	// Format: downloaded|total|speed|eta|percent
	parts := strings.Split(line, "|")
	if len(parts) >= 5 {
		percentStr := strings.TrimSpace(parts[4])
		percentStr = strings.TrimSuffix(percentStr, "%")
		var pct float64
		fmt.Sscanf(percentStr, "%f", &pct)
		speed := strings.TrimSpace(parts[2])
		eta := strings.TrimSpace(parts[3])
		return pct, speed, eta
	}

	// Fallback: parse "[download] XX.X% of YYY at ZZZ ETA HH:MM"
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
	// Xóa ký tự không hợp lệ
	invalid := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|"}
	result := name
	for _, c := range invalid {
		result = strings.ReplaceAll(result, c, "_")
	}
	// Giới hạn độ dài
	if len(result) > 100 {
		result = result[:100]
	}
	return strings.TrimSpace(result)
}
