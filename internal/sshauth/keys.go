// Package sshauth manages single-account SSH credentials: key discovery,
// ed25519 generation, managed Host aliases, enrollment and permission probes.
package sshauth

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/celestezj/blog-bootstrap/internal/execx"
	"github.com/celestezj/blog-bootstrap/internal/protocol"
)

// Key is one SSH key pair on disk.
type Key struct {
	Path        string
	PublicPath  string
	PublicKey   string
	Fingerprint string
}

var fingerprintRE = regexp.MustCompile(`SHA256:[A-Za-z0-9+/=]+`)

// DiscoverKeys lists plausible private keys in dir, always including preferred
// when it exists. Public key text is loaded when available.
func DiscoverKeys(dir, preferred string) ([]Key, error) {
	seen := map[string]bool{}
	var keys []Key

	add := func(path string) {
		if path == "" || seen[path] {
			return
		}
		st, err := os.Stat(path)
		if err != nil || st.IsDir() {
			return
		}
		seen[path] = true
		k := Key{Path: path, PublicPath: path + ".pub"}
		if data, err := os.ReadFile(k.PublicPath); err == nil {
			k.PublicKey = strings.TrimSpace(string(data))
		}
		keys = append(keys, k)
	}

	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasSuffix(name, ".pub") || !strings.HasPrefix(name, "id_") {
				continue
			}
			add(filepath.Join(dir, name))
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	add(preferred)
	sort.Slice(keys, func(i, j int) bool { return keys[i].Path < keys[j].Path })
	return keys, nil
}

// ReadKey loads an existing key pair.
func ReadKey(path string) (Key, error) {
	k := Key{Path: path, PublicPath: path + ".pub"}
	data, err := os.ReadFile(k.PublicPath)
	if err != nil {
		return k, err
	}
	k.PublicKey = strings.TrimSpace(string(data))
	return k, nil
}

// Fingerprint returns the SHA256 fingerprint of a key via ssh-keygen -lf.
func Fingerprint(ctx context.Context, r execx.Runner, sshKeygen string, k Key) (string, error) {
	target := k.PublicPath
	if _, err := os.Stat(target); err != nil {
		target = k.Path
	}
	res := r.Run(ctx, "", nil, sshKeygen, "-lf", target)
	if res.Err != nil {
		return "", res.Err
	}
	return ParseFingerprint(res.Combined()), nil
}

// ParseFingerprint extracts SHA256:... from ssh-keygen output.
func ParseFingerprint(out string) string {
	return fingerprintRE.FindString(out)
}

// GenerateKey creates an unencrypted ed25519 key at path with owner-only
// permissions. It never overwrites an existing key.
func GenerateKey(ctx context.Context, r execx.Runner, sshKeygen, path, comment string) (*Key, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, protocol.Fatal("AUTH_KEY_EXISTS", "目标私钥已存在，拒绝覆盖: "+path)
	}
	if _, err := os.Stat(path + ".pub"); err == nil {
		return nil, protocol.Fatal("AUTH_KEY_EXISTS", "目标公钥已存在，拒绝覆盖: "+path+".pub")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, protocol.Fatal("AUTH_KEY_GENERATION_FAILED", "无法创建密钥目录: "+err.Error()).WithCause(err)
	}
	res := r.Run(ctx, "", nil, sshKeygen, "-t", "ed25519", "-N", "", "-C", comment, "-f", path)
	if res.Err != nil || res.Failed() {
		return nil, protocol.Fatal("AUTH_KEY_GENERATION_FAILED", "ssh-keygen 失败: "+strings.TrimSpace(res.Combined())).
			WithCause(res.Err).WithLog("")
	}
	if err := os.Chmod(path, 0o600); err != nil && !os.IsPermission(err) {
		return nil, protocol.Fatal("AUTH_KEY_PERMISSION_FAILED", "无法收紧私钥权限: "+err.Error()).WithCause(err)
	}
	k, err := ReadKey(path)
	if err != nil {
		return nil, protocol.Fatal("AUTH_KEY_READ_FAILED", "无法读取新公钥: "+err.Error()).WithCause(err)
	}
	return &k, nil
}

// PublicKeyFor returns the public key text, deriving it from the private key
// only when the .pub sidecar is missing.
func PublicKeyFor(ctx context.Context, r execx.Runner, sshKeygen string, k Key) (string, error) {
	if k.PublicKey != "" {
		return k.PublicKey, nil
	}
	res := r.Run(ctx, "", nil, sshKeygen, "-y", "-f", k.Path)
	if res.Err != nil || res.Failed() {
		return "", protocol.Fatal("AUTH_KEY_READ_FAILED", "无法导出公钥: "+strings.TrimSpace(res.Combined()))
	}
	return strings.TrimSpace(res.Stdout), nil
}
