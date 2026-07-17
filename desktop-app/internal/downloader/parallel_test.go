package downloader

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParallelDownload(t *testing.T) {
	// Create a dummy file with 2MB of sequential bytes
	dataSize := 1024 * 1024 * 2
	dummyData := make([]byte, dataSize)
	for i := range dummyData {
		dummyData[i] = byte(i % 256)
	}

	// Create a mock HTTP server that automatically handles Range requests
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "test.bin", time.Now(), bytes.NewReader(dummyData))
	}))
	defer server.Close()

	// Initialize Engine
	tmpDir, err := os.MkdirTemp("", "ud_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	engine := NewEngine(tmpDir)
	engine.SetThreads(4)

	req := DownloadRequest{
		URL:   server.URL,
		Title: "test_file",
	}

	// Download using our multi-threaded downloader
	err = engine.downloadParallel(context.Background(), req, tmpDir, 4, nil)
	if err != nil {
		t.Fatalf("downloadParallel failed: %v", err)
	}

	// Verify file exists and content matches byte-for-byte
	downloadedFile := filepath.Join(tmpDir, "test_file.mp4")
	downloadedData, err := os.ReadFile(downloadedFile)
	if err != nil {
		t.Fatalf("Failed to read downloaded file: %v", err)
	}

	if len(downloadedData) != dataSize {
		t.Fatalf("Size mismatch: expected %d, got %d", dataSize, len(downloadedData))
	}

	for i := range downloadedData {
		if downloadedData[i] != dummyData[i] {
			t.Fatalf("Data mismatch at byte %d: expected %d, got %d", i, dummyData[i], downloadedData[i])
		}
	}
}
