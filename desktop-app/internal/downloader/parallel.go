package downloader

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// checkRangeSupport sends a GET request with Range: bytes=0-0 to check range support, size, and content type.
func (e *Engine) checkRangeSupport(ctx context.Context, client *http.Client, urlStr string, headers map[string]string, referer string) (int64, bool, string) {
	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return 0, false, ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
	if referer != "" {
		req.Header.Set("Referer", referer)
		req.Header.Set("Origin", extractOrigin(referer))
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Range", "bytes=0-0")

	resp, err := client.Do(req)
	if err != nil {
		return 0, false, ""
	}
	defer resp.Body.Close()

	contentType := resp.Header.Get("Content-Type")

	if resp.StatusCode == http.StatusPartialContent {
		contentRange := resp.Header.Get("Content-Range")
		if contentRange != "" {
			parts := strings.Split(contentRange, "/")
			if len(parts) == 2 {
				var total int64
				if _, err := fmt.Sscanf(parts[1], "%d", &total); err == nil {
					return total, true, contentType
				}
			}
		}
	}

	if resp.StatusCode == http.StatusOK {
		return resp.ContentLength, false, contentType
	}

	return 0, false, contentType
}

// downloadParallel downloads direct URL using concurrent chunks if Range headers are supported.
func (e *Engine) downloadParallel(ctx context.Context, req DownloadRequest, savePath string, numThreads int, onProgress ProgressCallback) error {
	if numThreads <= 0 {
		numThreads = 8
	}

	fileName := sanitizeFilename(req.Title)
	if fileName == "" {
		fileName = fmt.Sprintf("download_%d", time.Now().Unix())
	}

	dialer := NewDPIBypassDialer()
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return DPIBypassDialContext(ctx, dialer, network, addr)
			},
		},
		Timeout: 0, // No timeout
	}

	totalBytes, rangeSupported, contentType := e.checkRangeSupport(ctx, client, req.URL, req.Headers, req.PageURL)

	// If ranges are not supported, size is invalid, or file is too small (less than 1MB), fall back to standard download
	if !rangeSupported || totalBytes <= 0 || totalBytes < 1024*1024 {
		return e.directDownload(ctx, req, savePath, onProgress)
	}

	ext := guessExtFromContentType(contentType, req.URL)
	filePath := filepath.Join(savePath, fileName+ext)

	// Pre-create file and pre-allocate its size
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("không thể tạo file đầu ra: %v", err)
	}
	defer f.Close()

	if err := f.Truncate(totalBytes); err != nil {
		return fmt.Errorf("không thể phân bổ không gian file: %v", err)
	}

	// Calculate chunk size
	chunkSize := totalBytes / int64(numThreads)

	var wg sync.WaitGroup
	var downloadedBytes int64
	var workerErr error
	var errOnce sync.Once

	workerCtx, cancelWorker := context.WithCancel(ctx)
	defer cancelWorker()

	// Progress reporting variables
	var lastUpdate time.Time = time.Now()
	var lastBytes int64 = 0
	var speedStr string = ""
	var etaStr string = ""

	for i := 0; i < numThreads; i++ {
		start := int64(i) * chunkSize
		end := start + chunkSize - 1
		if i == numThreads-1 {
			end = totalBytes - 1
		}

		wg.Add(1)
		go func(partIndex int, start, end int64) {
			defer wg.Done()

			var lastChunkErr error
			for attempt := 0; attempt < 3; attempt++ {
				if workerCtx.Err() != nil {
					return
				}

				err := e.downloadChunk(workerCtx, client, req.URL, req.Headers, req.PageURL, f, start, end, &downloadedBytes, func() {
					if onProgress != nil {
						now := time.Now()
						elapsed := now.Sub(lastUpdate)
						if elapsed >= 500*time.Millisecond {
							currentBytes := atomic.LoadInt64(&downloadedBytes)
							diffBytes := currentBytes - lastBytes
							speedSec := float64(diffBytes) / elapsed.Seconds()

							if speedSec > 1024*1024 {
								speedStr = fmt.Sprintf("%.1f MB/s", speedSec/1024/1024)
							} else if speedSec > 1024 {
								speedStr = fmt.Sprintf("%.0f KB/s", speedSec/1024)
							} else {
								speedStr = fmt.Sprintf("%.0f B/s", speedSec)
							}

							remainingBytes := totalBytes - currentBytes
							if speedSec > 0 {
								etaSecs := float64(remainingBytes) / speedSec
								dur := time.Duration(etaSecs) * time.Second
								etaStr = dur.Round(time.Second).String()
							} else {
								etaStr = ""
							}

							pct := float64(currentBytes) / float64(totalBytes) * 100
							onProgress(pct, speedStr, etaStr, "downloading")

							lastBytes = currentBytes
							lastUpdate = now
						}
					}
				})

				if err == nil {
					return // Done with this chunk!
				}

				lastChunkErr = err
				time.Sleep(time.Duration(1<<attempt) * time.Second)
			}

			errOnce.Do(func() {
				workerErr = fmt.Errorf("lỗi tải phần %d: %v", partIndex, lastChunkErr)
				cancelWorker()
			})
		}(i, start, end)
	}

	wg.Wait()

	if workerErr != nil {
		f.Close()
		os.Remove(filePath)
		return workerErr
	}

	if onProgress != nil {
		onProgress(100.0, speedStr, "0s", "completed")
	}

	return nil
}

// downloadChunk downloads a specific range of bytes and writes it directly to os.File at correct offset.
func (e *Engine) downloadChunk(ctx context.Context, client *http.Client, urlStr string, headers map[string]string, referer string, f *os.File, start, end int64, downloadedCounter *int64, updateCallback func()) error {
	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
	if referer != "" {
		req.Header.Set("Referer", referer)
		req.Header.Set("Origin", extractOrigin(referer))
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP status %d", resp.StatusCode)
	}

	buf := make([]byte, 64*1024) // 64KB high-speed buffer
	writeOffset := start
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			_, writeErr := f.WriteAt(buf[:n], writeOffset)
			if writeErr != nil {
				return fmt.Errorf("ghi file lỗi ở offset %d: %v", writeOffset, writeErr)
			}
			writeOffset += int64(n)
			atomic.AddInt64(downloadedCounter, int64(n))
			updateCallback()
		}

		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}

	expectedBytes := end - start + 1
	actualBytes := writeOffset - start
	if actualBytes != expectedBytes {
		return fmt.Errorf("phần tải không khớp kích thước: mong đợi %d, thực tế %d", expectedBytes, actualBytes)
	}

	return nil
}
