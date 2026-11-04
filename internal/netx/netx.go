// Package netx classifies transport failures reported by external tools.
package netx

import "strings"

// IsConnectFailure reports whether output looks like a transport failure rather
// than an authentication or protocol response.
func IsConnectFailure(output string) bool {
	lower := strings.ToLower(output)
	for _, n := range []string{
		"connection timed out", "connection refused", "could not resolve hostname",
		"network is unreachable", "no route to host", "operation timed out",
		"connection reset by peer", "connection closed by",
	} {
		if strings.Contains(lower, n) {
			return true
		}
	}
	return false
}
