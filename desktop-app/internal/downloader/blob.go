package downloader

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// base64Decode decode base64 string thành bytes
func base64Decode(s string) ([]byte, error) {
	// Thử standard encoding trước
	decoded, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		// Thử URL-safe encoding
		decoded, err = base64.URLEncoding.DecodeString(s)
		if err != nil {
			// Thử RawStdEncoding (không có padding)
			decoded, err = base64.RawStdEncoding.DecodeString(s)
			if err != nil {
				return nil, fmt.Errorf("base64 decode failed: %v", err)
			}
		}
	}
	return decoded, nil
}

// SaveBlobToFile lưu blob base64 data thành file
func SaveBlobToFile(base64Data, mimeType, saveDir string) (string, error) {
	// Xác định extension từ MIME type
	extMap := map[string]string{
		"video/mp4":       ".mp4",
		"video/webm":      ".webm",
		"video/ogg":       ".ogv",
		"audio/mp3":       ".mp3",
		"audio/mpeg":      ".mp3",
		"audio/ogg":       ".ogg",
		"audio/wav":       ".wav",
		"audio/aac":       ".aac",
	}

	ext := extMap[mimeType]
	if ext == "" {
		ext = ".bin"
	}

	decoded, err := base64Decode(base64Data)
	if err != nil {
		return "", err
	}

	// Tạo tên file unique
	fileName := fmt.Sprintf("blob_%s%s", time.Now().Format("20060102_150405"), ext)
	filePath := filepath.Join(saveDir, fileName)

	if err := os.MkdirAll(saveDir, 0755); err != nil {
		return "", err
	}

	if err := os.WriteFile(filePath, decoded, 0644); err != nil {
		return "", err
	}

	return filePath, nil
}
