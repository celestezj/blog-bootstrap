package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/celestezj/blog-bootstrap/internal/protocol"
)

func baseConfig() *Config {
	return &Config{
		Version: 1,
		Repos: Repos{
			Source: "git@github.com:celestezj/blog-delta.git",
			Theme:  "git@github.com:celestezj/hexo-theme-A4.git",
			Deploy: "git@github.com:celestezj/celestezj.github.io.git",
		},
		GitIdentity: GitIdentity{Name: "Celeste", Email: "celeste@example.com"},
	}
}

func TestResolveDefaults(t *testing.T) {
	cfg := baseConfig()
	home := t.TempDir()
	base := t.TempDir()

	got, err := cfg.Resolve(home, base)
	if err != nil {
		t.Fatal(err)
	}
	if got.Owner != "celestezj" {
		t.Errorf("Owner = %q", got.Owner)
	}
	if got.Alias != "celestezj" {
		t.Errorf("Alias = %q, want owner default", got.Alias)
	}
	wantKey := filepath.Join(home, ".ssh", "id_ed25519_celestezj")
	if got.KeyPath != wantKey {
		t.Errorf("KeyPath = %q, want %q", got.KeyPath, wantKey)
	}
	wantDir := filepath.Join(base, "blog-delta")
	if got.Dir != wantDir {
		t.Errorf("Dir = %q, want %q", got.Dir, wantDir)
	}
	if got.Branch != "master" {
		t.Errorf("Branch = %q, want master", got.Branch)
	}
	if got.NodeWant != "22" {
		t.Errorf("NodeWant = %q, want 22", got.NodeWant)
	}
	if got.Strategy != "system_first" {
		t.Errorf("Strategy = %q, want system_first", got.Strategy)
	}
}

func TestResolveExplicitOverrides(t *testing.T) {
	cfg := baseConfig()
	cfg.Auth.Alias = "celestezj-blog"
	cfg.Auth.Key = "~/keys/blog"
	cfg.Workspace.Dir = "custom-dir"
	cfg.Workspace.Branch = "main"
	cfg.Toolchain.NodeVersion = "20.11.1"
	cfg.Toolchain.Strategy = "isolated"

	home := t.TempDir()
	base := t.TempDir()
	got, err := cfg.Resolve(home, base)
	if err != nil {
		t.Fatal(err)
	}
	if got.Alias != "celestezj-blog" {
		t.Errorf("Alias = %q", got.Alias)
	}
	if got.KeyPath != filepath.Join(home, "keys", "blog") {
		t.Errorf("KeyPath = %q", got.KeyPath)
	}
	if got.Dir != filepath.Join(base, "custom-dir") {
		t.Errorf("Dir = %q", got.Dir)
	}
	if got.Branch != "main" || got.NodeWant != "20.11.1" || got.Strategy != "isolated" {
		t.Errorf("overrides not applied: %+v", got)
	}
}

func TestResolveURLAliasRejected(t *testing.T) {
	cfg := baseConfig()
	cfg.Repos.Source = "git@celestezj:celestezj/blog-delta.git"
	_, err := cfg.Resolve(t.TempDir(), t.TempDir())
	assertCode(t, err, "CONFIG_URL_INVALID")
}

func TestResolveOwnerMismatch(t *testing.T) {
	cfg := baseConfig()
	cfg.Repos.Deploy = "git@github.com:bob/bob.github.io.git"
	_, err := cfg.Resolve(t.TempDir(), t.TempDir())
	assertCode(t, err, "CONFIG_OWNER_MISMATCH")
}

func TestResolveDeployNameInvalid(t *testing.T) {
	cfg := baseConfig()
	cfg.Repos.Deploy = "git@github.com:celestezj/blog-pages.git"
	_, err := cfg.Resolve(t.TempDir(), t.TempDir())
	assertCode(t, err, "DEPLOY_REPO_NAME_INVALID")
}

func TestValidateRequired(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"version", func(c *Config) { c.Version = 2 }},
		{"source", func(c *Config) { c.Repos.Source = "" }},
		{"identity name", func(c *Config) { c.GitIdentity.Name = " " }},
		{"identity email", func(c *Config) { c.GitIdentity.Email = "nope" }},
		{"strategy", func(c *Config) { c.Toolchain.Strategy = "magic" }},
		{"node", func(c *Config) { c.Toolchain.NodeVersion = "v22" }},
		{"branch", func(c *Config) { c.Workspace.Branch = "-bad" }},
		{"dotdot branch", func(c *Config) { c.Workspace.Branch = "a..b" }},
		{"alias", func(c *Config) { c.Auth.Alias = "bad alias" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseConfig()
			tt.mutate(cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate succeeded, want error")
			}
		})
	}
}

func TestExampleConfigParses(t *testing.T) {
	path := filepath.Join("..", "..", "blog-setup.example.yml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%s) error = %v", path, err)
	}
	if cfg.Version != 1 {
		t.Errorf("example version = %d", cfg.Version)
	}
	if cfg.Repos.Source == "" || cfg.Repos.Theme == "" || cfg.Repos.Deploy == "" {
		t.Error("example repos incomplete")
	}
}

func TestLoadStrictUnknownField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blog-setup.yml")
	data := `version: 1
repos:
  source: "git@github.com:a/b.git"
  theme: "git@github.com:a/c.git"
  deploy: "git@github.com:a/a.github.io.git"
git_identity:
  name: "A"
  email: "a@example.com"
unexpected: true
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	assertCode(t, err, "CONFIG_INVALID")
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yml"))
	assertCode(t, err, "CONFIG_NOT_FOUND")
}

func TestKeyPathStaysAbsolute(t *testing.T) {
	cfg := baseConfig()
	abs := filepath.Join(t.TempDir(), "id")
	if runtime.GOOS == "windows" {
		abs = strings.ToUpper(abs)
	}
	cfg.Auth.Key = abs
	got, err := cfg.Resolve(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got.KeyPath) {
		t.Fatalf("KeyPath %q is not absolute", got.KeyPath)
	}
}

func assertCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %s, got nil", want)
	}
	pe := protocol.As(err)
	if pe == nil {
		t.Fatalf("expected protocol error, got %T: %v", err, err)
	}
	if pe.Code != want {
		t.Fatalf("error code = %s, want %s (%v)", pe.Code, want, err)
	}
}
