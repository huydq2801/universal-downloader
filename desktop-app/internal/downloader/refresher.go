package downloader

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// isJavplayerExpiredURL kiểm tra URL có phải CDN Javplayer đã/sẽ hết hạn không
func isJavplayerExpiredURL(u string) bool {
	lower := strings.ToLower(u)
	return strings.Contains(lower, "wowstream") ||
		strings.Contains(lower, "streamlayer") ||
		strings.Contains(lower, "cache-xx") ||
		(strings.Contains(lower, "video.m3u8") && strings.Contains(lower, "token="))
}

// isJavplayerPage kiểm tra pageURL có phải trang dùng javplayer không
func isJavplayerPage(pageURL string) bool {
	lower := strings.ToLower(pageURL)
	return strings.Contains(lower, "123av.com") ||
		strings.Contains(lower, "avgle.com") ||
		strings.Contains(lower, "tokyomotion")
}

// extractVideoIDFromWowstreamURL trích xuất Javplayer Video ID trực tiếp từ wowstream CDN URL
// Ví dụ: cache-xx17.wowstream2.cloud/.../csKWrfI_ASajUxUzNFgkMJUFWztIg/video.m3u8
//        → "csKWrfI_ASajUxUzNFgkMJUFWztIg"
// Javplayer videoID có thể là: chữ hoa/thường + số + dấu gạch dưới, 8+ ký tự
func extractVideoIDFromWowstreamURL(rawURL string) string {
	// Lấy phần path của URL
	parts := strings.Split(rawURL, "/")
	// Tìm segment trước "video.m3u8" hoặc segment có dạng base64url (22+ ký tự alphanum + _)
	idRe := regexp.MustCompile(`^[A-Za-z0-9_\-]{8,}$`)
	for i := len(parts) - 1; i >= 0; i-- {
		seg := strings.Split(parts[i], "?")[0] // loại bỏ query string
		if seg == "video.m3u8" || seg == "" {
			continue
		}
		if idRe.MatchString(seg) && len(seg) >= 8 {
			return seg
		}
	}
	return ""
}

// extractJavplayerVideoID trích xuất videoID từ HTML của trang.
// Xử lý tất cả biến thể escape trong HTML:
//   - Normal:          javplayer.cc/e/2L947GZ2?
//   - JSON-escaped:    javplayer.cc\/e\/2L947GZ2?
//   - Double-escaped:  javplayer.cc\\/e\\/2L947GZ2?
//   - Unicode:         javplayer.cc\\\\/e\\\\/2L947GZ2?
func extractJavplayerVideoID(html string) string {
	// Dùng pattern flexible: / hoặc bất kỳ tổ hợp \ nào trước /
	re := regexp.MustCompile(`(?i)javplayer\.cc(?:[\\]*/)e(?:[\\]*/)([A-Za-z0-9]{6,12})(?:[?&\s"'\\])`)
	m := re.FindStringSubmatch(html)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

// ExtractJavplayerVideoID exported version for use in other packages
func ExtractJavplayerVideoID(html string) string {
	return extractJavplayerVideoID(html)
}

// newDPIClient tạo HTTP client dùng DPI bypass
func newDPIClient(timeout time.Duration) *http.Client {
	dialer := NewDPIBypassDialer()
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return DPIBypassDialContext(ctx, dialer, network, addr)
			},
		},
		Timeout: timeout,
	}
}

// RefreshJavplayerURL tái-lấy URL stream mới từ pageURL khi URL cũ đã hết hạn
func (e *Engine) RefreshJavplayerURL(ctx context.Context, pageURL string) (string, error) {
	client := newDPIClient(20 * time.Second)

	req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
	if err != nil {
		return "", fmt.Errorf("không thể tạo request: %v", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("không thể tải trang: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("không thể đọc HTML: %v", err)
	}
	mainHTML := string(bodyBytes)

	videoID := extractJavplayerVideoID(mainHTML)
	if videoID != "" {
		streamURL, err := e.fetchJavplayerStreamURL(ctx, videoID)
		if err == nil && streamURL != "" {
			return streamURL, nil
		}
	}

	return "", fmt.Errorf("không tìm thấy stream URL mới trong trang %s", pageURL)
}

// fetchJavplayerStreamURL gọi API javplayer.cc để lấy URL m3u8 mới
func (e *Engine) fetchJavplayerStreamURL(ctx context.Context, videoID string) (string, error) {
	streamAPIURL := fmt.Sprintf("https://javplayer.cc/stream?id=%s", videoID)
	iframeURL := fmt.Sprintf("https://javplayer.cc/e/%s", videoID)

	apiClient := newDPIClient(15 * time.Second)

	apiReq, err := http.NewRequestWithContext(ctx, "GET", streamAPIURL, nil)
	if err != nil {
		return "", err
	}
	apiReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
	apiReq.Header.Set("Referer", iframeURL)
	apiReq.Header.Set("Accept", "application/json, text/plain, */*")

	resp, err := apiClient.Do(apiReq)
	if err != nil {
		return "", fmt.Errorf("không thể gọi API Javplayer: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Javplayer API trả về HTTP %d", resp.StatusCode)
	}

	var res struct {
		Media struct {
			Stream string `json:"stream"`
		} `json:"media"`
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(bodyBytes, &res); err != nil {
		return "", fmt.Errorf("không thể parse JSON Javplayer: %v", err)
	}
	if res.Media.Stream == "" {
		return "", fmt.Errorf("Javplayer API không trả về stream URL")
	}
	return res.Media.Stream, nil
}
