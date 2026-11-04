package sshauth

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/celestezj/blog-bootstrap/internal/atomicfile"
	"github.com/celestezj/blog-bootstrap/internal/protocol"
)

const (
	managedBegin = "# BEGIN blog-bootstrap "
	managedEnd   = "# END blog-bootstrap "
)

// BlockResult describes what happened to ~/.ssh/config.
type BlockResult struct {
	Alias   string
	Path    string
	Backup  string
	Block   string
	Changed bool
}

// RenderBlock renders the managed Host block.
func RenderBlock(alias, hostName string, port int, identityFile string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s%s\n", managedBegin, alias)
	fmt.Fprintf(&b, "Host %s\n", alias)
	fmt.Fprintf(&b, "    HostName %s\n", hostName)
	fmt.Fprintf(&b, "    User git\n")
	if port != 22 {
		fmt.Fprintf(&b, "    Port %d\n", port)
	}
	fmt.Fprintf(&b, "    IdentityFile \"%s\"\n", identityFile)
	fmt.Fprintf(&b, "    IdentitiesOnly yes\n")
	fmt.Fprintf(&b, "    StrictHostKeyChecking accept-new\n")
	fmt.Fprintf(&b, "%s%s", managedEnd, alias)
	return b.String()
}

// PreviewHostBlock reports the effective alias and whether EnsureHostBlock
// would change the file, without writing anything.
func PreviewHostBlock(path, alias, hostName string, port int, identityFile string) (BlockResult, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return BlockResult{}, protocol.Fatal("AUTH_CONFIG_READ_FAILED", "无法读取 SSH 配置: "+err.Error()).WithCause(err)
	}
	newContent, res := prepareHostBlock(string(data), alias, hostName, port, identityFile)
	res.Path = path
	res.Changed = newContent != string(data)
	return res, nil
}

// EnsureHostBlock adds or updates the managed block for alias. Existing
// unmanaged Host blocks are never modified; a conflicting alias falls back to
// <alias>-blog. The file is backed up before any change.
func EnsureHostBlock(path, alias, hostName string, port int, identityFile string, now time.Time) (BlockResult, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return BlockResult{}, protocol.Fatal("AUTH_CONFIG_READ_FAILED", "无法读取 SSH 配置: "+err.Error()).WithCause(err)
	}
	content := string(data)
	newContent, res := prepareHostBlock(content, alias, hostName, port, identityFile)
	res.Path = path
	if newContent == content {
		return res, nil
	}
	res.Changed = true

	perm := os.FileMode(0o600)
	if st, statErr := os.Stat(path); statErr == nil {
		perm = st.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return res, protocol.Fatal("AUTH_CONFIG_WRITE_FAILED", err.Error()).WithCause(err)
	}
	if len(data) > 0 {
		backup := fmt.Sprintf("%s.blog-bootstrap.bak.%s", path, now.UTC().Format("20060102T150405.000000000Z"))
		if err := atomicfile.Write(backup, data, perm); err != nil {
			return res, protocol.Fatal("AUTH_CONFIG_BACKUP_FAILED", "无法备份 SSH 配置: "+err.Error()).WithCause(err)
		}
		res.Backup = backup
	}
	if err := atomicfile.Write(path, []byte(newContent), perm); err != nil {
		return res, protocol.Fatal("AUTH_CONFIG_WRITE_FAILED", "无法写入 SSH 配置: "+err.Error()).WithCause(err)
	}
	return res, nil
}

func prepareHostBlock(content, alias, hostName string, port int, identityFile string) (string, BlockResult) {
	newline := "\n"
	if strings.Contains(content, "\r\n") {
		newline = "\r\n"
	}
	lines := splitLines(content)
	parsed := parseConfig(lines)

	target := alias
	if _, managed := parsed.blocks[target]; !managed && parsed.unmanaged[strings.ToLower(target)] {
		if alt := existingManagedAlternate(alias, parsed); alt != "" {
			target = alt
		} else {
			target = chooseAlias(alias, parsed.unmanaged, parsed.blocks)
		}
	}

	block := RenderBlock(target, hostName, port, identityFile)
	blockLines := strings.Split(block, "\n")

	var newLines []string
	if span, ok := parsed.blocks[target]; ok {
		newLines = append(newLines, lines[:span[0]]...)
		newLines = append(newLines, blockLines...)
		newLines = append(newLines, lines[span[1]+1:]...)
	} else {
		trimmed := trimTrailingEmpty(lines)
		newLines = append(newLines, trimmed...)
		if len(trimmed) > 0 {
			newLines = append(newLines, "")
		}
		newLines = append(newLines, blockLines...)
	}
	newContent := strings.Join(newLines, newline)
	if !strings.HasSuffix(newContent, newline) {
		newContent += newline
	}
	return newContent, BlockResult{Alias: target, Block: block}
}

type configScan struct {
	blocks    map[string][2]int
	unmanaged map[string]bool
}

func parseConfig(lines []string) configScan {
	scan := configScan{blocks: map[string][2]int{}, unmanaged: map[string]bool{}}
	inManaged := false
	current := ""
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, managedBegin):
			inManaged = true
			current = strings.TrimSpace(strings.TrimPrefix(line, managedBegin))
		case strings.HasPrefix(line, managedEnd):
			if inManaged {
				scan.blocks[current] = [2]int{findBlockStart(lines, i, current), i}
			}
			inManaged = false
			current = ""
		case !inManaged:
			if host, ok := hostLine(line); ok {
				for _, pattern := range hosts(host) {
					scan.unmanaged[pattern] = true
				}
			}
		}
	}
	return scan
}

func findBlockStart(lines []string, end int, alias string) int {
	marker := managedBegin + alias
	for i := end; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == marker {
			return i
		}
	}
	return end
}

func hostLine(line string) (string, bool) {
	lower := strings.ToLower(line)
	if !strings.HasPrefix(lower, "host ") && lower != "host" {
		return "", false
	}
	return strings.TrimSpace(line[len("host"):]), true
}

func hosts(spec string) []string {
	var out []string
	for _, f := range strings.Fields(spec) {
		if strings.ContainsAny(f, "*?") {
			continue
		}
		out = append(out, strings.ToLower(strings.TrimSpace(f)))
	}
	return out
}

// existingManagedAlternate finds an alternate alias this tool already owns, so
// repeated runs keep updating one block instead of allocating a new alias.
func existingManagedAlternate(base string, parsed configScan) string {
	for i := 0; i < 100; i++ {
		candidate := base + "-blog"
		if i > 0 {
			candidate = fmt.Sprintf("%s-blog-%d", base, i+1)
		}
		if _, ok := parsed.blocks[candidate]; ok && !parsed.unmanaged[strings.ToLower(candidate)] {
			return candidate
		}
	}
	return ""
}

func chooseAlias(base string, unmanaged map[string]bool, managed map[string][2]int) string {
	taken := func(a string) bool {
		if unmanaged[strings.ToLower(a)] {
			return true
		}
		_, ok := managed[a]
		return ok
	}
	for i := 0; i < 100; i++ {
		candidate := base + "-blog"
		if i > 0 {
			candidate = fmt.Sprintf("%s-blog-%d", base, i+1)
		}
		if !taken(candidate) {
			return candidate
		}
	}
	// Unreachable in practice; a numeric suffix always eventually frees up.
	return base + "-blog"
}

func splitLines(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func trimTrailingEmpty(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
