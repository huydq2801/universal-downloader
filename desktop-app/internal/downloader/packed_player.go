package downloader

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ExtractPackedPlayer thực hiện trích xuất các luồng video từ các trang phát video sử dụng mã hóa Packer hoặc token xoay vòng
func ExtractPackedPlayer(ctx context.Context, pageURL string) (*ExtractResult, error) {
	// Khởi tạo HTTP client với DPI Bypass và CookieJar
	dialer := NewDPIBypassDialer()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(c context.Context, network, addr string) (net.Conn, error) {
				return DPIBypassDialContext(c, dialer, network, addr)
			},
		},
		Timeout: 20 * time.Second,
		Jar:     jar,
	}

	// 1. Tải mã nguồn trang HTML chính
	req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("không thể khởi tạo request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("không thể tải trang HTML chính: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("không thể đọc dữ liệu phản hồi: %w", err)
	}
	html := string(bodyBytes)

	// Trích xuất Title từ og:title hoặc <title>
	title := ""
	titleRe := regexp.MustCompile(`(?i)<title>(.*?)</title>`)
	if m := titleRe.FindStringSubmatch(html); len(m) > 1 {
		title = strings.TrimSpace(m[1])
		// Dọn dẹp các hậu tố thừa của site
		for _, suffix := range []string{" - SEXTB.NET", " - SEXTB NET"} {
			title = strings.TrimSuffix(strings.ToUpper(title), suffix)
		}
	}
	if title == "" {
		title = "Video Player"
	}

	// Trích xuất Thumbnail từ og:image
	thumbnail := ""
	thumbRe := regexp.MustCompile(`(?i)property=["']og:image["']\s+content=["']([^"']+)["']|content=["']([^"']+)["']\s+property=["']og:image["']`)
	if m := thumbRe.FindStringSubmatch(html); len(m) > 0 {
		if m[1] != "" {
			thumbnail = m[1]
		} else if len(m) > 2 {
			thumbnail = m[2]
		}
	}

	// Trích xuất các tham số filmId, window.__pt, window.__pk
	filmId := ""
	filmIdRe := regexp.MustCompile(`(?i)filmId\s*=\s*['"]?(\d+)['"]?`)
	if m := filmIdRe.FindStringSubmatch(html); len(m) > 1 {
		filmId = m[1]
	}
	pt := ""
	ptRe := regexp.MustCompile(`(?i)window\.__pt\s*=\s*['"]([^'"]+)['"]`)
	if m := ptRe.FindStringSubmatch(html); len(m) > 1 {
		pt = m[1]
	}
	pk := ""
	pkRe := regexp.MustCompile(`(?i)window\.__pk\s*=\s*['"]([^'"]+)['"]`)
	if m := pkRe.FindStringSubmatch(html); len(m) > 1 {
		pk = m[1]
	}

	// Trích xuất _token và _socket từ meta tags
	token := ""
	tokenRe := regexp.MustCompile(`(?i)<meta[^>]+name=["']_token["'][^>]*value=["']([^"']+)["']|<meta[^>]+value=["']([^"']+)["'][^>]*name=["']_token["']`)
	if m := tokenRe.FindStringSubmatch(html); len(m) > 0 {
		if m[1] != "" {
			token = m[1]
		} else if len(m) > 2 {
			token = m[2]
		}
	}
	socket := ""
	socketRe := regexp.MustCompile(`(?i)<meta[^>]+name=["']_socket["'][^>]*value=["']([^"']+)["']|<meta[^>]+value=["']([^"']+)["'][^>]*name=["']_socket["']`)
	if m := socketRe.FindStringSubmatch(html); len(m) > 0 {
		if m[1] != "" {
			socket = m[1]
		} else if len(m) > 2 {
			socket = m[2]
		}
	}

	if filmId == "" || pt == "" || pk == "" {
		return nil, fmt.Errorf("không tìm thấy cấu hình trình phát video trong mã nguồn")
	}

	// Thiết lập Header Authorization nếu tìm thấy token và socket
	authHeader := ""
	if token != "" && socket != "" {
		rawAuth := fmt.Sprintf("%s:%s", token, socket)
		authHeader = "Basic " + base64.StdEncoding.EncodeToString([]byte(rawAuth))
	}

	// Trích xuất tất cả các servers từ các nút bấm (button)
	buttonRe := regexp.MustCompile(`(?i)data-id=["'](\d+)["'][^>]*>(?:<i[^>]*><\/i>)?\s*([^<]+)`)
	btnMatches := buttonRe.FindAllStringSubmatch(html, -1)

	type serverItem struct {
		id   string
		name string
	}
	var servers []serverItem
	seen := make(map[string]bool)
	for _, m := range btnMatches {
		id := m[1]
		name := strings.TrimSpace(m[2])
		// Tránh trùng lặp và bỏ qua các server VIP hiển thị thông tin nâng cấp
		if !seen[id] && name != "" && !strings.Contains(strings.ToLower(name), "vip 1") && !strings.Contains(strings.ToLower(name), "vip 2") {
			seen[id] = true
			servers = append(servers, serverItem{id: id, name: name})
		}
	}

	if len(servers) == 0 {
		return nil, fmt.Errorf("không tìm thấy bất kỳ máy chủ (server) video khả dụng nào")
	}

	var formats []AIFormatInfo

	// 2. Lặp qua các server, thực hiện quay vòng token pt/pk và lấy iframe tương ứng
	for _, sv := range servers {
		// Dò tìm endpoint phát video của trang tương ứng
		u, err := url.Parse(pageURL)
		if err != nil {
			continue
		}
		apiURL := fmt.Sprintf("%s://%s/ajax/player", u.Scheme, u.Host)
		
		form := url.Values{}
		form.Set("episode", sv.id)
		form.Set("filmId", filmId)
		form.Set("pt", pt)

		apiReq, err := http.NewRequestWithContext(ctx, "POST", apiURL, strings.NewReader(form.Encode()))
		if err != nil {
			continue
		}
		apiReq.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
		apiReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
		apiReq.Header.Set("Referer", pageURL)
		apiReq.Header.Set("X-Requested-With", "XMLHttpRequest")
		if authHeader != "" {
			apiReq.Header.Set("Authorization", authHeader)
		}

		apiResp, err := client.Do(apiReq)
		if err != nil {
			continue
		}

		apiBodyBytes, err := io.ReadAll(apiResp.Body)
		apiResp.Body.Close()
		if err != nil {
			continue
		}

		var apiRes struct {
			Error     bool   `json:"error"`
			PlayerEnc string `json:"player_enc"`
			NextPT    string `json:"next_pt"`
			NextPK    string `json:"next_pk"`
		}
		if err := json.Unmarshal(apiBodyBytes, &apiRes); err != nil {
			continue
		}

		if apiRes.Error {
			continue
		}

		// Giải mã chuỗi player_enc bằng thuật toán XOR
		decryptedHTML := xorDecryptPacked(apiRes.PlayerEnc, pk)
		if decryptedHTML == "" {
			continue
		}

		// Xoay vòng các token pt và pk cho lượt gọi kế tiếp
		if apiRes.NextPT != "" {
			pt = apiRes.NextPT
		}
		if apiRes.NextPK != "" {
			pk = apiRes.NextPK
		}

		// Trích xuất src của iframe từ HTML đã giải mã
		iframeSrcRe := regexp.MustCompile(`(?i)src=["'](https?://[^"']+)["']`)
		iframeMatches := iframeSrcRe.FindStringSubmatch(decryptedHTML)
		if len(iframeMatches) < 2 {
			continue
		}
		iframeURL := iframeMatches[1]

		// 3. Phân tích nội dung bên trong iframe của server
		iframeReq, err := http.NewRequestWithContext(ctx, "GET", iframeURL, nil)
		if err != nil {
			continue
		}
		iframeReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
		iframeReq.Header.Set("Referer", pageURL)
		iframeResp, err := client.Do(iframeReq)
		if err != nil {
			continue
		}

		iframeBytes, err := io.ReadAll(iframeResp.Body)
		iframeResp.Body.Close()
		if err != nil {
			continue
		}
		iframeHTML := string(iframeBytes)

		// Kiểm tra xem trang iframe có bị nén bằng P.A.C.K.E.R. hay không
		hasPacker := false
		rePacker := regexp.MustCompile(`\}\s*\(\s*['"](.*?)['"]\s*,\s*(\d+)\s*,\s*(\d+)\s*,\s*['"](.*?)['"]\.split\(['"]\|['"]\)`)
		m := rePacker.FindStringSubmatch(iframeHTML)
		if len(m) < 5 {
			rePacker2 := regexp.MustCompile(`\}\s*\(\s*['"](.*?)['"]\s*,\s*(\d+)\s*,\s*(\d+)\s*,\s*\[(.*?)\]`)
			m = rePacker2.FindStringSubmatch(iframeHTML)
		}

		if len(m) >= 5 {
			p := m[1]
			radix, _ := strconv.Atoi(m[2])
			count, _ := strconv.Atoi(m[3])
			wordsStr := m[4]

			p = strings.ReplaceAll(p, `\'`, `'`)
			p = strings.ReplaceAll(p, `\"`, `"`)
			words := strings.Split(wordsStr, "|")

			unpackedJS := unpackPackerStream(p, radix, count, words)
			if unpackedJS != "" {
				// Tìm các liên kết video luồng trực tiếp (.m3u8) trong JS đã giải nén
				m3u8Re := regexp.MustCompile(`https?://[^\s'"]+\.m3u8[^\s'"]*`)
				m3u8Matches := m3u8Re.FindAllString(unpackedJS, -1)
				for _, streamURL := range m3u8Matches {
					hasPacker = true
					formats = append(formats, AIFormatInfo{
						Title:   fmt.Sprintf("%s - Server %s (Direct HLS)", title, sv.name),
						URL:     streamURL,
						Quality: fmt.Sprintf("Server %s", sv.name),
						Ext:     "mp4",
					})
				}
			}
		}

		// Nếu không phải dạng Packer hoặc giải nén Packer thất bại, thử tìm các link m3u8 trực tiếp trong mã HTML iframe
		if !hasPacker {
			m3u8Re := regexp.MustCompile(`https?://[^\s'"]+\.m3u8[^\s'"]*`)
			m3u8Matches := m3u8Re.FindAllString(iframeHTML, -1)
			if len(m3u8Matches) > 0 {
				for _, streamURL := range m3u8Matches {
					formats = append(formats, AIFormatInfo{
						Title:   fmt.Sprintf("%s - Server %s", title, sv.name),
						URL:     streamURL,
						Quality: fmt.Sprintf("Server %s", sv.name),
						Ext:     "mp4",
					})
				}
			} else {
				// Nếu là các server host khác như Streamtape, DoodStream, upn.one,... ta liệt kê đường dẫn iframe để user tải gián tiếp
				formats = append(formats, AIFormatInfo{
					Title:   fmt.Sprintf("[Iframe] %s - Server %s", title, sv.name),
					URL:     iframeURL,
					Quality: fmt.Sprintf("Mirror %s", sv.name),
					Ext:     "html",
				})
			}
		}
	}

	if len(formats) == 0 {
		return nil, fmt.Errorf("không thể tìm thấy bất kỳ luồng video trực tiếp nào từ các server")
	}

	return &ExtractResult{
		Title:     title,
		Thumbnail: thumbnail,
		Formats:   formats,
	}, nil
}

// xorDecryptPacked giải mã chuỗi base64 đã mã hóa XOR
func xorDecryptPacked(encoded, key string) string {
	if encoded == "" || key == "" {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return ""
	}
	result := make([]byte, len(decoded))
	for i := 0; i < len(decoded); i++ {
		result[i] = decoded[i] ^ key[i%len(key)]
	}
	return string(result)
}

// unpackPackerStream giải mã chuỗi mã hóa P.A.C.K.E.R. bằng Go
func unpackPackerStream(p string, a int, c int, k []string) string {
	baseDecode := func(s string, base int) int {
		val, _ := strconv.ParseInt(s, base, 64)
		return int(val)
	}

	decodeWord := func(word string, radix int, k []string) string {
		alphabet := "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
		val := big.NewInt(0)
		rad := big.NewInt(int64(radix))
		for i := 0; i < len(word); i++ {
			ch := word[i]
			idx := strings.IndexByte(alphabet[:radix], ch)
			if idx == -1 {
				return word
			}
			val.Mul(val, rad)
			val.Add(val, big.NewInt(int64(idx)))
		}
		valInt := val.Int64()
		if valInt >= 0 && valInt < int64(len(k)) && k[valInt] != "" {
			return k[valInt]
		}
		return word
	}

	reWord := regexp.MustCompile(`\b\w+\b`)
	result := reWord.ReplaceAllStringFunc(p, func(word string) string {
		val := baseDecode(word, a)
		if val < len(k) && k[val] != "" {
			return k[val]
		}
		return decodeWord(word, a, k)
	})

	return result
}
