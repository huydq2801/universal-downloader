package downloader

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// AIFormatInfo kết quả định dạng video trích xuất bằng AI
type AIFormatInfo struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Quality string `json:"quality"`
	Ext     string `json:"ext"`
}

// ExtractResult kết quả phân tích AI đầy đủ
type ExtractResult struct {
	Title     string         `json:"title"`
	Thumbnail string         `json:"thumbnail"`
	Formats   []AIFormatInfo `json:"formats"`
}

// CleanHTML rút gọn mã nguồn HTML chỉ giữ lại các tags liên quan đến media để tiết kiệm token
func CleanHTML(html string) string {
	// Lọc các thẻ script, video, iframe, source, a
	re := regexp.MustCompile(`(?i)<(video|audio|source|iframe|embed|object|script|a)[^>]*>`)
	matches := re.FindAllString(html, -1)
	
	// Tìm các chuỗi URL trong HTML (các chuỗi kết thúc bằng .mp4, .m3u8, .mpd, .webm)
	urlRe := regexp.MustCompile(`https?://[^\s"'<>]+?\.(?:mp4|m3u8|mpd|webm|ts|mkv|aac|mp3)[^\s"'<>]*`)
	urls := urlRe.FindAllString(html, -1)
	
	// Gộp lại thành một đoạn text ngắn gọn chứa các thành phần quan trọng
	var buf bytes.Buffer
	buf.WriteString("--- MEDIA TAGS FOUND ---\n")
	for _, m := range matches {
		if len(m) < 400 { // Bỏ qua script quá dài
			buf.WriteString(m + "\n")
		}
	}
	
	buf.WriteString("\n--- CDN/STREAM URLS FOUND ---\n")
	seen := make(map[string]bool)
	for _, u := range urls {
		if !seen[u] {
			seen[u] = true
			buf.WriteString(u + "\n")
		}
	}
	
	return buf.String()
}

// ExtractMediaWithGemini gửi HTML thu gọn lên Gemini API để trích xuất link video
func ExtractMediaWithGemini(ctx context.Context, pageURL, htmlContent, apiKey, modelName string) (*ExtractResult, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("Gemini API Key chưa được thiết lập trong Cài đặt")
	}
	
	if modelName == "" {
		modelName = "gemini-3.1-flash-lite" // default model
	}

	cleanedHTML := CleanHTML(htmlContent)
	
	// Lấy title từ HTML (nếu có)
	title := "Video được trích xuất bằng AI"
	titleRe := regexp.MustCompile(`(?i)<title>(.*?)</title>`)
	if titleMatch := titleRe.FindStringSubmatch(htmlContent); len(titleMatch) > 1 {
		title = strings.TrimSpace(titleMatch[1])
	}
	
	// Xác định thumbnail dự phòng từ domain hoặc og:image
	thumbnail := ""
	thumbRe := regexp.MustCompile(`(?i)<meta\s+property="og:image"\s+content="(.*?)"`)
	if thumbMatch := thumbRe.FindStringSubmatch(htmlContent); len(thumbMatch) > 1 {
		thumbnail = strings.TrimSpace(thumbMatch[1])
	}

	// Tạo prompt chi tiết cho Gemini
	prompt := fmt.Sprintf(`Bạn là chuyên gia lập trình và trích xuất dữ liệu đa phương tiện từ trang web. 
Nhiệm vụ của bạn: Tìm và trích xuất tất cả các đường dẫn phát video trực tiếp (direct video streams) từ mã nguồn trang web được cung cấp dưới đây.
Trang gốc: %s

Mã nguồn trang web thu gọn:
"""
%s
"""

Hãy tìm các đường dẫn video trực tiếp (ví dụ: các link kết thúc bằng .mp4, .webm, .m3u8, .mpd hoặc các luồng phát video CDN).
Quy tắc:
1. Hãy loại bỏ các liên kết quảng cáo hoặc tệp video cực kỳ nhỏ không phải là nội dung chính.
2. Trả về kết quả dưới dạng JSON có cấu trúc chính xác sau:
{
  "title": "[Tiêu đề trang hoặc tiêu đề video]",
  "formats": [
    {
      "title": "[Mô tả định dạng ví dụ: Video MP4 độ phân giải cao hoặc HLS Stream]",
      "url": "[Đường dẫn liên kết tuyệt đối đầy đủ bắt đầu bằng http hoặc https]",
      "quality": "[Chất lượng ví dụ 1080p, 720p hoặc Tự động]",
      "ext": "[Đuôi định dạng file ví dụ mp4 hoặc m3u8]"
    }
  ]
}

LƯU Ý QUAN TRỌNG: Chỉ trả về mã JSON hợp lệ duy nhất, không thêm bất kỳ văn bản giải thích, không gói trong dấu nháy ngược markdown (ví dụ không dùng `+"`json ...`"+`). Chỉ trả về chuỗi JSON bắt đầu bằng { và kết thúc bằng }.`, pageURL, cleanedHTML)

	// Tạo request payload cho Gemini API với cấu hình vô hiệu hóa bộ lọc an toàn
	type ContentPart struct {
		Text string `json:"text"`
	}
	type RequestContent struct {
		Parts []ContentPart `json:"parts"`
	}
	type SafetySetting struct {
		Category  string `json:"category"`
		Threshold string `json:"threshold"`
	}
	type GeminiRequest struct {
		Contents       []RequestContent `json:"contents"`
		SafetySettings []SafetySetting  `json:"safetySettings"`
	}
	
	reqBody := GeminiRequest{
		Contents: []RequestContent{
			{
				Parts: []ContentPart{
					{Text: prompt},
				},
			},
		},
		SafetySettings: []SafetySetting{
			{Category: "HARM_CATEGORY_SEXUALLY_EXPLICIT", Threshold: "BLOCK_NONE"},
			{Category: "HARM_CATEGORY_HATE_SPEECH", Threshold: "BLOCK_NONE"},
			{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "BLOCK_NONE"},
			{Category: "HARM_CATEGORY_DANGEROUS_CONTENT", Threshold: "BLOCK_NONE"},
		},
	}
	
	reqBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("json marshal error: %v", err)
	}

	// Gemini API endpoint
	apiURL := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", modelName, apiKey)
	
	httpReq, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(reqBytes))
	if err != nil {
		return nil, fmt.Errorf("tạo HTTP request lỗi: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("kết nối Gemini API thất bại: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Gemini API báo lỗi (HTTP %d): %s", resp.StatusCode, string(respBytes))
	}

	// Parse kết quả
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("đọc dữ liệu phản hồi lỗi: %v", err)
	}

	// Parse Gemini API JSON response
	type SafetyRating struct {
		Category    string `json:"category"`
		Probability string `json:"probability"`
	}
	type Candidate struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	}
	type GeminiResponse struct {
		Candidates []Candidate `json:"candidates"`
	}

	var geminiResp GeminiResponse
	if err := json.Unmarshal(respBytes, &geminiResp); err != nil {
		return nil, fmt.Errorf("unmarshal Gemini response error: %v", err)
	}

	if len(geminiResp.Candidates) > 0 && geminiResp.Candidates[0].FinishReason == "SAFETY" {
		return nil, fmt.Errorf("Gemini đã chặn phân tích trang này do bộ lọc an toàn nội dung nhạy cảm (Safety Block). Bạn hãy đổi API Key khác hoặc chọn Model thông minh hơn (như Gemini 3.1) trong Cài đặt.")
	}

	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("Gemini không trả về kết quả phân tích nào (Mảng Candidates trống hoặc bị chặn)")
	}

	aiText := geminiResp.Candidates[0].Content.Parts[0].Text
	
	// Làm sạch phản hồi từ markdown nếu có
	aiText = strings.TrimSpace(aiText)
	if strings.HasPrefix(aiText, "```") {
		// Tìm và cắt bỏ ```json và ```
		re := regexp.MustCompile("(?s)^```(?:json)?(.*)```$")
		if matches := re.FindStringSubmatch(aiText); len(matches) > 1 {
			aiText = strings.TrimSpace(matches[1])
		}
	}

	// Parse JSON kết quả từ AI
	var aiResult ExtractResult
	if err := json.Unmarshal([]byte(aiText), &aiResult); err != nil {
		return nil, fmt.Errorf("AI trả về định dạng JSON không hợp lệ: %v\nKết quả thô của AI:\n%s", err, aiText)
	}

	if aiResult.Title == "" {
		aiResult.Title = title
	}
	if aiResult.Thumbnail == "" {
		aiResult.Thumbnail = thumbnail
	}

	// Đảm bảo các URL được phân tích là tuyệt đối
	base, err := url.Parse(pageURL)
	if err == nil {
		for i, fmtItem := range aiResult.Formats {
			u, err := url.Parse(fmtItem.URL)
			if err == nil {
				aiResult.Formats[i].URL = base.ResolveReference(u).String()
			}
		}
	}

	return &aiResult, nil
}
