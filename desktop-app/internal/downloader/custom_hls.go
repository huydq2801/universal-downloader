package downloader

import (
	"bufio"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type segmentJob struct {
	index int
	url   string
}

// downloadHLSWithGo uses Go native http client to download HLS streams concurrently.
// It bypasses FFmpeg's DNS/keepalive issues, handles PNG encapsulation, and downloads in parallel for maximum speed.
func (e *Engine) downloadHLSWithGo(ctx context.Context, req DownloadRequest, savePath string, onProgress ProgressCallback) error {
	fileName := sanitizeFilename(req.Title)
	if fileName == "" {
		fileName = fmt.Sprintf("video_%d", time.Now().Unix())
	}
	outPath := filepath.Join(savePath, fileName+".mp4")

	// 1. Resolve media playlist URL recursively
	mediaPlaylistURL, err := e.resolveMediaPlaylist(ctx, req.URL, req.PageURL)
	if err != nil {
		return fmt.Errorf("không thể phân giải playlist: %v", err)
	}

	// 2. Fetch media playlist
	lines, err := e.fetchPlaylistLines(ctx, mediaPlaylistURL, req.PageURL)
	if err != nil {
		return fmt.Errorf("không thể tải media playlist: %v", err)
	}

	// 3. Extract segment URLs
	baseParsed, _ := url.Parse(mediaPlaylistURL)
	var segments []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		segments = append(segments, resolveURLWithQuery(baseParsed, line))
	}

	totalSegs := len(segments)
	if totalSegs == 0 {
		return fmt.Errorf("không tìm thấy segment nào trong playlist")
	}

	// 4. Create temporary folder for segment parts (MD5-hashed based on resolved playlist URL)
	hasher := md5.New()
	hasher.Write([]byte(mediaPlaylistURL))
	tempDirName := fmt.Sprintf(".tmp_hls_%s", hex.EncodeToString(hasher.Sum(nil)))
	tempDir := filepath.Join(savePath, tempDirName)
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return fmt.Errorf("không thể tạo thư mục tạm: %v", err)
	}
	
	// Only remove temporary directory on successful download completion
	downloadSuccess := false
	defer func() {
		if downloadSuccess {
			os.RemoveAll(tempDir)
		}
	}()

	// Configure client with DPI bypass resolver
	dialer := NewDPIBypassDialer()
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return DPIBypassDialContext(ctx, dialer, network, addr)
			},
		},
		Timeout: 30 * time.Second,
	}

	// Start concurrent worker pool
	numWorkers := e.threads
	if numWorkers <= 0 {
		numWorkers = 8
	}
	jobsChan := make(chan segmentJob, totalSegs)
	for i, segURL := range segments {
		jobsChan <- segmentJob{index: i, url: segURL}
	}
	close(jobsChan)

	var wg sync.WaitGroup
	var downloadedCount int64
	var workerErr error
	var errOnce sync.Once
	
	workerCtx, cancelWorker := context.WithCancel(ctx)
	defer cancelWorker()

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobsChan {
				if workerCtx.Err() != nil {
					return
				}

				partPath := filepath.Join(tempDir, fmt.Sprintf("%05d.ts", job.index))
				
				// HLS Resume Check: Skip downloading if segment already exists and is valid size
				stat, err := os.Stat(partPath)
				if err == nil && stat.Size() > 1000 {
					count := atomic.AddInt64(&downloadedCount, 1)
					if onProgress != nil {
						pct := float64(count) / float64(totalSegs) * 100
						onProgress(pct, "", "", "downloading")
					}
					continue
				}

				err = e.downloadSegmentToFile(workerCtx, client, job.url, req.PageURL, partPath, 3)
				if err != nil {
					errOnce.Do(func() {
						workerErr = err
						cancelWorker() // Cancel other workers
					})
					return
				}

				count := atomic.AddInt64(&downloadedCount, 1)
				if onProgress != nil {
					pct := float64(count) / float64(totalSegs) * 100
					onProgress(pct, "", "", "downloading")
				}
			}
		}()
	}

	wg.Wait()

	if workerErr != nil {
		return fmt.Errorf("lỗi tải song song: %v", workerErr)
	}

	// 5. Merge all segment files into a single temporary .ts file
	tempTSPath := filepath.Join(tempDir, "merged_temp.ts")
	tempTSFile, err := os.Create(tempTSPath)
	if err != nil {
		return fmt.Errorf("không thể tạo tệp TS tạm thời: %v", err)
	}
	
	if onProgress != nil {
		onProgress(99.0, "", "", "stitching")
	}

	for i := 0; i < totalSegs; i++ {
		partPath := filepath.Join(tempDir, fmt.Sprintf("%05d.ts", i))
		partFile, err := os.Open(partPath)
		if err != nil {
			tempTSFile.Close()
			return fmt.Errorf("lỗi mở tệp phân mảnh %d: %v", i, err)
		}
		_, err = io.Copy(tempTSFile, partFile)
		partFile.Close()
		if err != nil {
			tempTSFile.Close()
			return fmt.Errorf("lỗi ghép tệp phân mảnh %d: %v", i, err)
		}
	}
	tempTSFile.Close()

	// 6. Remux the merged .ts file to a proper .mp4 container using ffmpeg
	if onProgress != nil {
		onProgress(99.5, "", "", "remuxing")
	}

	ffmpegExecutable := e.ffmpeg
	if ffmpegExecutable == "" {
		ffmpegExecutable = "ffmpeg"
	}

	cmd := exec.CommandContext(ctx, ffmpegExecutable, "-y", "-i", tempTSPath, "-c", "copy", outPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	if err := cmd.Run(); err != nil {
		// Fallback: If ffmpeg fails, just rename/copy the merged .ts file to outPath
		if onProgress != nil {
			onProgress(99.8, "", "", "stitching (fallback)")
		}
		
		srcFile, srcErr := os.Open(tempTSPath)
		if srcErr != nil {
			return fmt.Errorf("ffmpeg lỗi và không thể mở tệp TS để sao chép: %v (lỗi ffmpeg: %v)", srcErr, err)
		}
		defer srcFile.Close()

		destFile, destErr := os.Create(outPath)
		if destErr != nil {
			return fmt.Errorf("ffmpeg lỗi và không thể tạo tệp đầu ra: %v (lỗi ffmpeg: %v)", destErr, err)
		}
		defer destFile.Close()

		if _, copyErr := io.Copy(destFile, srcFile); copyErr != nil {
			return fmt.Errorf("ffmpeg lỗi và sao chép phân mảnh thất bại: %v (lỗi ffmpeg: %v)", copyErr, err)
		}
	}

	if onProgress != nil {
		onProgress(100.0, "", "", "completed")
	}

	downloadSuccess = true
	return nil
}

func (e *Engine) resolveMediaPlaylist(ctx context.Context, playlistURL, referer string) (string, error) {
	if (strings.Contains(playlistURL, "/api/") || strings.Contains(playlistURL, "/source/")) && !strings.Contains(playlistURL, ".m3u8") {
		resolvedURL, err := e.resolveAPISourceURL(ctx, playlistURL, referer)
		if err == nil && resolvedURL != "" {
			playlistURL = resolvedURL
		}
	}

	lines, err := e.fetchPlaylistLines(ctx, playlistURL, referer)
	if err != nil {
		return "", err
	}

	baseParsed, _ := url.Parse(playlistURL)
	isMaster := false

	type hlsVariant struct {
		url       string
		bandwidth int
	}
	var variants []hlsVariant

	var currentBandwidth int
	bandwidthRe := regexp.MustCompile(`BANDWIDTH=(\d+)`)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#EXT-X-STREAM-INF") {
			isMaster = true
			currentBandwidth = 0
			if m := bandwidthRe.FindStringSubmatch(line); len(m) > 1 {
				fmt.Sscanf(m[1], "%d", &currentBandwidth)
			}
			continue
		}
		if line != "" && !strings.HasPrefix(line, "#") {
			resolved := resolveURLWithQuery(baseParsed, line)
			variants = append(variants, hlsVariant{
				url:       resolved,
				bandwidth: currentBandwidth,
			})
		}
	}

	if isMaster && len(variants) > 0 {
		// Sort variants by bandwidth descending (highest quality first)
		sort.Slice(variants, func(i, j int) bool {
			return variants[i].bandwidth > variants[j].bandwidth
		})

		var lastErr error
		for _, v := range variants {
			fmt.Printf("[DEBUG] resolveMediaPlaylist trying variant (bandwidth: %d): %s\n", v.bandwidth, v.url)
			resolvedURL, err := e.resolveMediaPlaylist(ctx, v.url, referer)
			if err == nil && resolvedURL != "" {
				return resolvedURL, nil
			}
			lastErr = err
			fmt.Printf("[DEBUG] resolveMediaPlaylist variant failed: %v\n", err)
		}
		if lastErr != nil {
			return "", lastErr
		}
		return "", fmt.Errorf("không có variant nào hợp lệ trong playlist")
	}

	return playlistURL, nil
}

// resolveAPISourceURL sends a POST request to api sources to extract the real stream link
func (e *Engine) resolveAPISourceURL(ctx context.Context, apiURL, referer string) (string, error) {
	dialer := NewDPIBypassDialer()
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return DPIBypassDialContext(ctx, dialer, network, addr)
			},
		},
		Timeout: 10 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
	if referer != "" {
		req.Header.Set("Referer", referer)
		req.Header.Set("Origin", extractOrigin(referer))
	}
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API HTTP %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	bodyStr := string(bodyBytes)
	
	// Scan JSON for any URL ending with .m3u8
	m3u8Re := regexp.MustCompile(`https?://[^\s"'<>\\#]+?\.m3u8[^\s"'<>\\#]*`)
	match := m3u8Re.FindString(bodyStr)
	if match != "" {
		match = strings.ReplaceAll(match, "\\/", "/")
		return match, nil
	}

	// Fallback to any MP4/direct link
	mp4Re := regexp.MustCompile(`https?://[^\s"'<>\\#]+?\.(?:mp4|webm|vl)[^\s"'<>\\#]*`)
	match = mp4Re.FindString(bodyStr)
	if match != "" {
		match = strings.ReplaceAll(match, "\\/", "/")
		return match, nil
	}

	return "", fmt.Errorf("không tìm thấy link stream trong API response")
}

func (e *Engine) fetchPlaylistLines(ctx context.Context, playlistURL, referer string) ([]string, error) {
	dialer := NewDPIBypassDialer()
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return DPIBypassDialContext(ctx, dialer, network, addr)
			},
		},
		Timeout: 10 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, "GET", playlistURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
	if referer != "" {
		req.Header.Set("Referer", referer)
		req.Header.Set("Origin", extractOrigin(referer))
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var lines []string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, nil
}

func (e *Engine) downloadSegmentToFile(ctx context.Context, client *http.Client, urlStr, referer string, partPath string, retries int) error {
	var lastErr error
	for attempt := 0; attempt < retries; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
		if referer != "" {
			if !strings.Contains(urlStr, "googleusercontent.com") {
				req.Header.Set("Referer", referer)
				req.Header.Set("Origin", extractOrigin(referer))
			}
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(1<<attempt) * time.Second)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			time.Sleep(time.Duration(1<<attempt) * time.Second)
			continue
		}

		partFile, err := os.Create(partPath)
		if err != nil {
			resp.Body.Close()
			return err
		}

		// Read & decrypt on-the-fly or in memory
		bodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			partFile.Close()
			os.Remove(partPath)
			lastErr = err
			time.Sleep(time.Duration(1<<attempt) * time.Second)
			continue
		}

		// Decrypt PNG encapsulation if present
		if len(bodyBytes) > 941 && bodyBytes[0] == 0x89 && bodyBytes[1] == 0x50 && bodyBytes[2] == 0x4e && bodyBytes[3] == 0x47 {
			_, err = partFile.Write(bodyBytes[941:])
		} else {
			_, err = partFile.Write(bodyBytes)
		}
		partFile.Close()

		if err != nil {
			os.Remove(partPath)
			return err
		}
		return nil // Success
	}
	return fmt.Errorf("sau %d lần thử: %v", retries, lastErr)
}

func resolveURLWithQuery(base *url.URL, refStr string) string {
	ref, err := url.Parse(refStr)
	if err != nil {
		return refStr
	}
	resolved := base.ResolveReference(ref)
	// Copy query parameters from base URL if resolved URL has none
	if resolved.RawQuery == "" && base.RawQuery != "" {
		resolved.RawQuery = base.RawQuery
	}
	return resolved.String()
}
