package main

import (
	"os/exec"
)

// openExplorer mở Windows Explorer tại path
func openExplorer(path string) error {
	cmd := exec.Command("explorer.exe", path)
	return cmd.Start()
}
