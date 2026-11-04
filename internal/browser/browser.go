// Package browser opens GitHub URLs in the user's default browser via argv.
package browser

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

const allowedPrefix = "https://github.com/"

// Open launches the default browser for a GitHub URL. Any other host is
// rejected so a malformed configuration cannot send the user elsewhere.
func Open(url string) error {
	if !strings.HasPrefix(url, allowedPrefix) {
		return fmt.Errorf("refusing to open non-GitHub URL")
	}
	var name string
	var args []string
	switch runtime.GOOS {
	case "windows":
		name = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	case "darwin":
		name = "open"
		args = []string{url}
	default:
		name = "xdg-open"
		args = []string{url}
	}
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
