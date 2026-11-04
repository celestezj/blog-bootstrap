package toolchain

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/celestezj/blog-bootstrap/internal/protocol"
)

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func serveArchive(t *testing.T, archive []byte, sum string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/archive.zip", func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	})
	mux.HandleFunc("/SHASUMS256.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  archive.zip\n", sum)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestPortableEnsureSuccess(t *testing.T) {
	archive := makeZip(t, map[string]string{
		"tool/bin/node.exe":                    "binary",
		"tool/node_modules/npm/bin/npm-cli.js": "//npm",
	})
	sum := fmt.Sprintf("%x", sha256.Sum256(archive))
	srv := serveArchive(t, archive, sum)

	toolsDir := t.TempDir()
	p := &Portable{HTTP: srv.Client(), ToolsDir: toolsDir}
	spec := PortableSpec{
		Kind:        Node,
		Version:     "22.14.0",
		URL:         srv.URL + "/archive.zip",
		ChecksumURL: srv.URL + "/SHASUMS256.txt",
		Archive:     "zip",
		InstallDir:  filepath.Join(toolsDir, "node", "22.14.0"),
		Binaries:    []string{"node.exe"},
	}
	tool, err := p.Ensure(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(tool.Path, "node.exe") {
		t.Fatalf("path = %s", tool.Path)
	}
	if _, err := os.Stat(tool.Path); err != nil {
		t.Fatalf("binary missing: %v", err)
	}
	if tool.Source != "portable" {
		t.Fatalf("source = %s", tool.Source)
	}
	assertNoDownloads(t, toolsDir)
}

func TestPortableChecksumMismatchDeletesFile(t *testing.T) {
	archive := makeZip(t, map[string]string{"tool/bin/node.exe": "binary"})
	wrong := strings.Repeat("0", 64)
	srv := serveArchive(t, archive, wrong)

	toolsDir := t.TempDir()
	p := &Portable{HTTP: srv.Client(), ToolsDir: toolsDir}
	spec := PortableSpec{
		Kind:        Node,
		Version:     "22.14.0",
		URL:         srv.URL + "/archive.zip",
		ChecksumURL: srv.URL + "/SHASUMS256.txt",
		Archive:     "zip",
		InstallDir:  filepath.Join(toolsDir, "node", "22.14.0"),
		Binaries:    []string{"node.exe"},
	}
	_, err := p.Ensure(context.Background(), spec)
	if err == nil {
		t.Fatal("expected checksum failure")
	}
	pe := protocol.As(err)
	if pe == nil || pe.Code != "CHECKSUM_MISMATCH" {
		t.Fatalf("err = %v", err)
	}
	assertNoDownloads(t, toolsDir)
	if _, err := os.Stat(spec.InstallDir); !os.IsNotExist(err) {
		t.Fatal("install dir must not be created on checksum failure")
	}
}

func TestPortablePinnedChecksum(t *testing.T) {
	archive := makeZip(t, map[string]string{"bin/node": "binary"})
	sum := fmt.Sprintf("%x", sha256.Sum256(archive))
	srv := serveArchive(t, archive, sum)

	toolsDir := t.TempDir()
	p := &Portable{HTTP: srv.Client(), ToolsDir: toolsDir}
	spec := PortableSpec{
		Kind:       Node,
		Version:    "22.14.0",
		URL:        srv.URL + "/archive.zip",
		Checksum:   sum,
		Archive:    "zip",
		InstallDir: filepath.Join(toolsDir, "node", "22.14.0"),
		Binaries:   []string{"node"},
	}
	tool, err := p.Ensure(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(tool.Path, "node") {
		t.Fatalf("path = %s", tool.Path)
	}
}

func TestPortableEnsureReusesExisting(t *testing.T) {
	toolsDir := t.TempDir()
	spec := PortableSpec{
		Kind:       Node,
		Version:    "22.14.0",
		Archive:    "zip",
		InstallDir: filepath.Join(toolsDir, "node", "22.14.0"),
		Binaries:   []string{"node.exe"},
	}
	bin := filepath.Join(spec.InstallDir, "bin", "node.exe")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := &Portable{ToolsDir: toolsDir}
	tool, err := p.Ensure(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if tool.Path != bin {
		t.Fatalf("path = %s, want %s", tool.Path, bin)
	}
}

func assertNoDownloads(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "download-") {
			t.Fatalf("download file left behind: %s", e.Name())
		}
	}
}
