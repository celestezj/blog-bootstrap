package sshauth

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/celestezj/blog-bootstrap/internal/execx"
)

func TestDiscoverKeys(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("id_ed25519", "priv")
	write("id_ed25519.pub", "ssh-ed25519 AAAA a@b")
	write("id_rsa", "priv")
	write("config", "Host x")
	write("known_hosts", "x")
	preferred := filepath.Join(dir, "custom_key")
	write("custom_key", "priv")

	keys, err := DiscoverKeys(dir, preferred)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 3 {
		t.Fatalf("keys = %d: %+v", len(keys), keys)
	}
	if keys[0].Path != preferred && keys[len(keys)-1].Path != preferred {
		t.Fatal("preferred key missing")
	}
	for _, k := range keys {
		if strings.HasSuffix(k.Path, ".pub") || strings.HasSuffix(k.Path, "config") {
			t.Fatalf("unexpected key %s", k.Path)
		}
	}
}

func TestGenerateKey(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".ssh", "id_ed25519_celestezj")
	fake := execx.NewFake()
	fake.Handler = func(c execx.Call) execx.Result {
		if c.Name == "ssh-keygen" {
			var path string
			for i, a := range c.Args {
				if a == "-f" && i+1 < len(c.Args) {
					path = c.Args[i+1]
				}
			}
			if path != "" {
				_ = os.WriteFile(path, []byte("PRIVATE"), 0o644)
				_ = os.WriteFile(path+".pub", []byte("ssh-ed25519 AAAA comment"), 0o644)
			}
		}
		return execx.Result{}
	}
	key, err := GenerateKey(context.Background(), fake, "ssh-keygen", target, "blog-bootstrap:celestezj")
	if err != nil {
		t.Fatal(err)
	}
	if key.PublicKey == "" {
		t.Fatal("public key not loaded")
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %o, want 600", st.Mode().Perm())
		}
	}
	calls := fake.CallsMatching("ssh-keygen")
	joined := strings.Join(calls[len(calls)-1].Args, " ")
	if !strings.Contains(joined, "-t ed25519") || !strings.Contains(joined, `-N `) {
		t.Fatalf("args = %s", joined)
	}
}

func TestGenerateKeyRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "id")
	if err := os.WriteFile(target, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := GenerateKey(context.Background(), execx.NewFake(), "ssh-keygen", target, "c")
	if err == nil {
		t.Fatal("must not overwrite existing key")
	}
	data, _ := os.ReadFile(target)
	if string(data) != "existing" {
		t.Fatal("existing key modified")
	}
}

func TestParseFingerprint(t *testing.T) {
	out := "256 SHA256:abcDEF123+/= celestezj@host (ED25519)"
	if got := ParseFingerprint(out); got != "SHA256:abcDEF123+/=" {
		t.Fatalf("fingerprint = %q", got)
	}
}

func TestPublicKeyForDerivesWhenMissing(t *testing.T) {
	fake := execx.NewFake()
	fake.Handler = func(c execx.Call) execx.Result {
		return execx.Result{Stdout: "ssh-ed25519 AAAA derived\n"}
	}
	got, err := PublicKeyFor(context.Background(), fake, "ssh-keygen", Key{Path: "id"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "ssh-ed25519 AAAA derived" {
		t.Fatalf("got %q", got)
	}
}
