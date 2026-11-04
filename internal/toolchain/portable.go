package toolchain

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/celestezj/blog-bootstrap/internal/protocol"
)

// PortableSpec describes one downloadable toolchain archive.
type PortableSpec struct {
	Kind        Kind
	Version     string
	URL         string
	ChecksumURL string
	Checksum    string // optional pinned hex; skips ChecksumURL
	Archive     string // zip | tar.gz
	InstallDir  string
	Binaries    []string
}

// Portable downloads and verifies self-contained toolchains.
type Portable struct {
	HTTP     *http.Client
	ToolsDir string
	GOOS     string
	GOARCH   string
	Logf     func(format string, args ...interface{})
}

const maxDownload = 512 << 20

// Ensure returns the portable tool, downloading and verifying it if absent.
func (p *Portable) Ensure(ctx context.Context, spec PortableSpec) (*Tool, error) {
	if tool := p.existing(spec); tool != nil {
		return tool, nil
	}
	if spec.ChecksumURL == "" && spec.Checksum == "" {
		return nil, protocol.Fatal("CHECKSUM_UNAVAILABLE", "便携工具缺少官方校验和，拒绝下载执行")
	}

	if err := os.MkdirAll(p.ToolsDir, 0o755); err != nil {
		return nil, protocol.Fatal("TOOL_INSTALL_FAILED", "无法创建工具目录: "+err.Error()).WithCause(err)
	}
	archive := filepath.Join(p.ToolsDir, "download-"+string(spec.Kind)+"-"+spec.Version+"."+strings.ReplaceAll(spec.Archive, ".", "_"))
	if err := p.download(ctx, spec.URL, archive); err != nil {
		os.Remove(archive)
		return nil, err
	}

	want := spec.Checksum
	if want == "" {
		var err error
		want, err = p.fetchChecksum(ctx, spec)
		if err != nil {
			os.Remove(archive)
			return nil, err
		}
	}
	got, err := fileSHA256(archive)
	if err != nil {
		os.Remove(archive)
		return nil, protocol.Fatal("DOWNLOAD_FAILED", "无法读取下载文件: "+err.Error()).WithCause(err)
	}
	if !strings.EqualFold(got, want) {
		os.Remove(archive)
		return nil, protocol.Retry("CHECKSUM_MISMATCH",
			fmt.Sprintf("%s 下载校验失败（期望 %s，实际 %s），已删除文件", spec.Kind, want, got)).
			WithDetails(map[string]string{"expected": want, "actual": got})
	}

	tmp := spec.InstallDir + ".tmp"
	os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		os.Remove(archive)
		return nil, protocol.Fatal("EXTRACT_FAILED", "无法创建解压目录: "+err.Error()).WithCause(err)
	}
	if err := extract(archive, tmp, spec.Archive); err != nil {
		os.RemoveAll(tmp)
		os.Remove(archive)
		return nil, protocol.Fatal("EXTRACT_FAILED", "解压失败: "+err.Error()).WithCause(err)
	}
	os.Remove(archive)

	os.RemoveAll(spec.InstallDir)
	if err := os.MkdirAll(filepath.Dir(spec.InstallDir), 0o755); err != nil {
		return nil, protocol.Fatal("TOOL_INSTALL_FAILED", err.Error()).WithCause(err)
	}
	if err := os.Rename(tmp, spec.InstallDir); err != nil {
		return nil, protocol.Fatal("TOOL_INSTALL_FAILED", "无法移动工具目录: "+err.Error()).WithCause(err)
	}

	tool := p.existing(spec)
	if tool == nil {
		return nil, protocol.Fatal("TOOL_BINARY_MISSING",
			fmt.Sprintf("解压后找不到 %s 可执行文件", strings.Join(spec.Binaries, "/")))
	}
	if p.Logf != nil {
		p.Logf("便携 %s %s 已安装到 %s", spec.Kind, spec.Version, spec.InstallDir)
	}
	return tool, nil
}

// Lookup returns an already-extracted portable tool without downloading.
func (p *Portable) Lookup(spec PortableSpec) *Tool { return p.existing(spec) }

func (p *Portable) existing(spec PortableSpec) *Tool {
	bin, err := findBinary(spec.InstallDir, spec.Binaries)
	if err != nil || bin == "" {
		return nil
	}
	v, _ := ParseVersion(spec.Version)
	extra := map[string]string{}
	if ssh, _ := findBinary(spec.InstallDir, []string{"ssh.exe", "ssh"}); ssh != "" {
		extra["ssh"] = ssh
	}
	return &Tool{Name: string(spec.Kind), Path: bin, Version: v, Source: "portable", Extra: extra}
}

func (p *Portable) download(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return protocol.Fatal("DOWNLOAD_FAILED", "无法创建下载请求: "+err.Error()).WithCause(err)
	}
	client := p.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return protocol.Retry("NETWORK_UNREACHABLE", "下载失败: "+err.Error()).WithCause(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return protocol.Retry("NETWORK_UNREACHABLE", fmt.Sprintf("下载 %s 返回 HTTP %d", url, resp.StatusCode))
	}
	f, err := os.Create(dest)
	if err != nil {
		return protocol.Fatal("DOWNLOAD_FAILED", err.Error()).WithCause(err)
	}
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return protocol.Retry("NETWORK_UNREACHABLE", "下载中断: "+err.Error()).WithCause(err)
	}
	if n > maxDownload {
		return protocol.Fatal("DOWNLOAD_TOO_LARGE", "下载文件超过上限")
	}
	return nil
}

func (p *Portable) fetchChecksum(ctx context.Context, spec PortableSpec) (string, error) {
	client := p.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, spec.ChecksumURL, nil)
	if err != nil {
		return "", protocol.Fatal("CHECKSUM_UNAVAILABLE", err.Error()).WithCause(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", protocol.Retry("NETWORK_UNREACHABLE", "获取校验和失败: "+err.Error()).WithCause(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", protocol.Retry("NETWORK_UNREACHABLE", fmt.Sprintf("获取校验和返回 HTTP %d", resp.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", protocol.Retry("NETWORK_UNREACHABLE", err.Error()).WithCause(err)
	}
	name := filepath.Base(spec.URL)
	if sum, ok := checksumFromSHASUMS(data, name); ok {
		return sum, nil
	}
	if sum, ok := checksumFromGitHubRelease(data, name); ok {
		return sum, nil
	}
	return "", protocol.Fatal("CHECKSUM_UNAVAILABLE", "官方校验文件中找不到 "+name)
}

func checksumFromSHASUMS(data []byte, filename string) (string, bool) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		candidate := strings.TrimPrefix(fields[1], "*")
		if candidate == filename && isHex64(fields[0]) {
			return fields[0], true
		}
	}
	return "", false
}

func checksumFromGitHubRelease(data []byte, filename string) (string, bool) {
	var rel struct {
		Assets []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(data, &rel); err != nil {
		return "", false
	}
	for _, a := range rel.Assets {
		if a.Name == filename {
			sum := strings.TrimPrefix(a.Digest, "sha256:")
			if isHex64(sum) {
				return sum, true
			}
		}
	}
	return "", false
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	if _, err := hex.DecodeString(s); err != nil {
		return false
	}
	return true
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func findBinary(root string, names []string) (string, error) {
	var found []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		for _, n := range names {
			if strings.EqualFold(d.Name(), n) {
				found = append(found, path)
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(found) == 0 {
		return "", os.ErrNotExist
	}
	sort.Slice(found, func(i, j int) bool { return len(found[i]) < len(found[j]) })
	return found[0], nil
}

func extract(archivePath, dest, kind string) error {
	switch kind {
	case "zip":
		return extractZip(archivePath, dest)
	case "tar.gz":
		return extractTarGz(archivePath, dest)
	default:
		return fmt.Errorf("unsupported archive type %q", kind)
	}
}

func safeJoin(root, name string) (string, error) {
	clean := filepath.Clean(name)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry escapes destination: %s", name)
	}
	target := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry escapes destination: %s", name)
	}
	return target, nil
}

func extractZip(path, dest string) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		target, err := safeJoin(dest, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		mode := f.Mode()
		if mode == 0 {
			mode = 0o644
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		rc.Close()
		out.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}

func extractTarGz(path, dest string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dest, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if runtime.GOOS != "windows" {
				if err := os.Symlink(hdr.Linkname, target); err != nil {
					return err
				}
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(hdr.Mode)
			if mode == 0 {
				mode = 0o644
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, tr)
			out.Close()
			if copyErr != nil {
				return copyErr
			}
		}
	}
}
