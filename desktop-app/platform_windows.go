package main

import (
	"os/exec"
)

// openExplorer mở Windows Explorer tại path
func openExplorer(path string) error {
	return exec.Command("explorer", path).Start()
}
