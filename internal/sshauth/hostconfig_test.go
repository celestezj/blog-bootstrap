package sshauth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var fixed = time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)

func TestEnsureHostBlockAppendsAndBacksUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".ssh", "config")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	original := "Host github.com\n    HostName github.com\n    User git\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := EnsureHostBlock(path, "celestezj", "github.com", 22, "/home/u/.ssh/id", fixed)
	if err != nil {
		t.Fatal(err)
	}
	if res.Alias != "celestezj" || !res.Changed {
		t.Fatalf("result = %+v", res)
	}
	if res.Backup == "" {
		t.Fatal("backup expected")
	}
	if _, err := os.Stat(res.Backup); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.HasPrefix(content, original) {
		t.Fatal("existing config must be preserved")
	}
	if !strings.Contains(content, managedBegin+"celestezj") {
		t.Fatal("managed block missing")
	}
	if !strings.Contains(content, "IdentitiesOnly yes") {
		t.Fatal("IdentitiesOnly missing")
	}
}

func TestEnsureHostBlockConflictUsesAlternateAndIsStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	original := "Host celestezj\n    HostName example.com\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := EnsureHostBlock(path, "celestezj", "github.com", 22, "/key", fixed)
	if err != nil {
		t.Fatal(err)
	}
	if res.Alias != "celestezj-blog" {
		t.Fatalf("alias = %s, want celestezj-blog", res.Alias)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "HostName example.com") {
		t.Fatal("unmanaged block must not be modified")
	}

	res2, err := EnsureHostBlock(path, "celestezj", "github.com", 22, "/key", fixed.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if res2.Alias != "celestezj-blog" {
		t.Fatalf("second run alias = %s, want stable alternate", res2.Alias)
	}
	if res2.Changed {
		t.Fatal("second run should be a no-op")
	}
}

func TestEnsureHostBlockUpdatesManagedInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if _, err := EnsureHostBlock(path, "celestezj", "github.com", 22, "/old/key", fixed); err != nil {
		t.Fatal(err)
	}
	res, err := EnsureHostBlock(path, "celestezj", "ssh.github.com", 443, "/new/key", fixed.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed {
		t.Fatal("expected change")
	}
	data, _ := os.ReadFile(path)
	content := string(data)
	if strings.Count(content, managedBegin+"celestezj") != 1 {
		t.Fatalf("duplicate managed blocks:\n%s", content)
	}
	if strings.Contains(content, "/old/key") {
		t.Fatal("old key not replaced")
	}
	if !strings.Contains(content, "Port 443") || !strings.Contains(content, "HostName ssh.github.com") {
		t.Fatalf("endpoint not updated:\n%s", content)
	}
}

func TestEnsureHostBlockPreservesCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte("Host x\r\n  HostName y\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureHostBlock(path, "celestezj", "github.com", 22, "/key", fixed); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "Host x\r\n") {
		t.Fatal("CRLF line endings not preserved")
	}
}

func TestParseConfigIgnoresWildcards(t *testing.T) {
	scan := parseConfig([]string{"Host * github.com*", "  User git"})
	if scan.unmanaged["github.com*"] || scan.unmanaged["*"] {
		t.Fatal("wildcards must not be treated as concrete aliases")
	}
}
