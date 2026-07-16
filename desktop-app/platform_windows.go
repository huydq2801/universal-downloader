package main

import (
	"os/exec"
	"syscall"
)

// openExplorer mở Windows Explorer tại path
func openExplorer(path string) error {
	cmd := exec.Command("explorer", path)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Start()
}
