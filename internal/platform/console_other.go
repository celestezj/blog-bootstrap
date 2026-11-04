//go:build !windows

package platform

// EnsureUTF8Console is a no-op outside Windows.
func EnsureUTF8Console() {}
