package storage

import (
	"os"
	"path/filepath"
	"runtime"
)

// getDefaultDownloadDir trả về thư mục download mặc định theo OS
func getDefaultDownloadDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "."
	}

	var dir string
	switch runtime.GOOS {
	case "windows":
		dir = filepath.Join(homeDir, "Downloads", "UniversalDL")
	case "darwin":
		dir = filepath.Join(homeDir, "Downloads", "UniversalDL")
	default:
		dir = filepath.Join(homeDir, "Downloads", "UniversalDL")
	}

	os.MkdirAll(dir, 0755)
	return dir
}
