//go:build windows

package platform

import "golang.org/x/sys/windows"

// EnsureUTF8Console switches the console code pages to UTF-8 so Chinese output
// renders correctly. Failures are non-fatal (pipes have no console).
func EnsureUTF8Console() {
	_ = windows.SetConsoleOutputCP(65001)
	_ = windows.SetConsoleCP(65001)
}
