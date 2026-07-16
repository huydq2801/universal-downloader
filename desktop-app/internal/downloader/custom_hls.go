package downloader

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
		u, err := url.Parse(line)
		if err != nil {
			continue
		}
		segments = append(segments, baseParsed.ResolveReference(u).String())
	}

	totalSegs := len(segments)
	if totalSegs == 0 {
		return fmt.Errorf("không tìm thấy segment nào trong playlist")
	}

	// 4. Create temporary folder for segment parts
	tempDir := filepath.Join(savePath, fmt.Sprintf(".tmp_%s_%d", fileName, time.Now().Unix()))
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return fmt.Errorf("không thể tạo thư mục tạm: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Configure client with custom DNS resolver
	dialer := &net.Dialer{
		Resolver: &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{Timeout: 4 * time.Second}
				conn, err := d.DialContext(ctx, "udp", "1.1.1.1:53")
				if err == nil {
					return conn, nil
				}
				return d.DialContext(ctx, "udp", "8.8.8.8:53")
			},
		},
	}
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: dialer.DialContext,
		},
		Timeout: 30 * time.Second,
	}

	// Start concurrent worker pool
	numWorkers := 8 // Tải song song 8 luồng để tăng tốc độ gấp 8 lần
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
				err := e.downloadSegmentToFile(workerCtx, client, job.url, req.PageURL, partPath, 3)
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

	// 5. Merge all files into final output file sequentially
	outFile, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("không thể tạo file đầu ra: %v", err)
	}
	defer outFile.Close()

	if onProgress != nil {
		onProgress(99.9, "", "", "stitching")
	}

	for i := 0; i < totalSegs; i++ {
		partPath := filepath.Join(tempDir, fmt.Sprintf("%05d.ts", i))
		partFile, err := os.Open(partPath)
		if err != nil {
			return fmt.Errorf("lỗi mở tệp phân mảnh %d: %v", i, err)
		}
		_, err = io.Copy(outFile, partFile)
		partFile.Close()
		if err != nil {
			return fmt.Errorf("lỗi ghép tệp phân mảnh %d: %v", i, err)
		}
	}

	if onProgress != nil {
		onProgress(100.0, "", "", "completed")
	}

	return nil
}

func (e *Engine) resolveMediaPlaylist(ctx context.Context, playlistURL, referer string) (string, error) {
	lines, err := e.fetchPlaylistLines(ctx, playlistURL, referer)
	if err != nil {
		return "", err
	}

	baseParsed, _ := url.Parse(playlistURL)
	isMaster := false
	var variantURLs []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#EXT-X-STREAM-INF") {
			isMaster = true
			continue
		}
		if line != "" && !strings.HasPrefix(line, "#") {
			u, err := url.Parse(line)
			if err == nil {
				variantURLs = append(variantURLs, baseParsed.ResolveReference(u).String())
			}
		}
	}

	if isMaster && len(variantURLs) > 0 {
		bestVariant := variantURLs[len(variantURLs)-1]
		return e.resolveMediaPlaylist(ctx, bestVariant, referer)
	}

	return playlistURL, nil
}

func (e *Engine) fetchPlaylistLines(ctx context.Context, playlistURL, referer string) ([]string, error) {
	dialer := &net.Dialer{
		Resolver: &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{Timeout: 4 * time.Second}
				conn, err := d.DialContext(ctx, "udp", "1.1.1.1:53")
				if err == nil {
					return conn, nil
				}
				return d.DialContext(ctx, "udp", "8.8.8.8:53")
			},
		},
	}
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: dialer.DialContext,
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
