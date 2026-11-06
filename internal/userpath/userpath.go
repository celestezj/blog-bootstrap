// Package userpath manages the user-level PATH entry used by portable tools.
package userpath

import "strings"

// Value is a raw PATH value and whether it is stored expandable.
type Value struct {
	Raw    string
	Expand bool
}

// Store reads and writes the user-level PATH.
type Store interface {
	Read() (Value, error)
	Write(Value) error
}

// Result describes the change Ensure would make (or made).
type Result struct {
	Changed bool
	Before  string
	After   string
	Added   []string
	Removed []string
}

func splitEntries(raw string) []string {
	parts := strings.Split(raw, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// norm canonicalizes an entry for comparison. Windows PATH comparisons are
// case-insensitive and tolerate either separator.
func norm(entry string) string {
	e := strings.TrimSpace(entry)
	if len(e) >= 2 && e[0] == '"' && e[len(e)-1] == '"' {
		e = e[1 : len(e)-1]
	}
	e = strings.ReplaceAll(e, "/", `\`)
	e = strings.TrimRight(e, `\`)
	return strings.ToLower(e)
}

// Within reports whether path is located under root (case-insensitive,
// Windows-style separators).
func Within(root, path string) bool {
	r, p := norm(root), norm(path)
	if r == "" || p == "" || p == r {
		return false
	}
	return strings.HasPrefix(p, r+`\`)
}

// Ensure prepends the desired entries to raw, removes stale entries under
// managedRoot, deduplicates case-insensitively, and keeps every unrelated
// entry in its original order.
func Ensure(raw, managedRoot string, desired ...string) Result {
	result := Result{Before: raw}
	var wanted []string
	seen := map[string]bool{}
	for _, d := range desired {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		key := norm(d)
		if seen[key] {
			continue
		}
		seen[key] = true
		wanted = append(wanted, d)
	}
	wantedSet := map[string]bool{}
	for _, d := range wanted {
		wantedSet[norm(d)] = true
	}

	entries := splitEntries(raw)
	kept := make([]string, 0, len(entries)+len(wanted))
	for _, e := range entries {
		key := norm(e)
		if seen[key] {
			continue
		}
		if Within(managedRoot, e) && !wantedSet[key] {
			result.Removed = append(result.Removed, e)
			continue
		}
		seen[key] = true
		kept = append(kept, e)
	}

	after := make([]string, 0, len(wanted)+len(kept))
	after = append(after, wanted...)
	after = append(after, kept...)

	if equalEntries(entries, after) {
		result.After = raw
		return result
	}
	present := map[string]bool{}
	for _, e := range entries {
		present[norm(e)] = true
	}
	for _, d := range wanted {
		if !present[norm(d)] {
			result.Added = append(result.Added, d)
		}
	}
	result.Changed = true
	result.After = strings.Join(after, ";")
	return result
}

func equalEntries(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if norm(a[i]) != norm(b[i]) {
			return false
		}
	}
	return true
}

// ManagedReady reports whether the user PATH already starts with desired and
// contains no stale entries under managedRoot.
func ManagedReady(raw, managedRoot, desired string) bool {
	return !Ensure(raw, managedRoot, desired).Changed
}
