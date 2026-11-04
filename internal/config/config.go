// Package config loads, validates and resolves blog-setup.yml.
package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/celestezj/blog-bootstrap/internal/protocol"
	"github.com/celestezj/blog-bootstrap/internal/repo"
)

// Config mirrors blog-setup.yml. Only the three repository URLs and the commit
// identity are required; everything else has a deterministic default.
type Config struct {
	Version     int         `yaml:"version"`
	Repos       Repos       `yaml:"repos"`
	GitIdentity GitIdentity `yaml:"git_identity"`
	Auth        Auth        `yaml:"auth"`
	Toolchain   Toolchain   `yaml:"toolchain"`
	Workspace   Workspace   `yaml:"workspace"`
}

type Repos struct {
	Source string `yaml:"source"`
	Theme  string `yaml:"theme"`
	Deploy string `yaml:"deploy"`
}

type GitIdentity struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
}

type Auth struct {
	Alias string `yaml:"alias"`
	Key   string `yaml:"key"`
}

type Toolchain struct {
	Strategy    string `yaml:"strategy"`
	NodeVersion string `yaml:"node_version"`
}

type Workspace struct {
	Dir    string `yaml:"dir"`
	Branch string `yaml:"branch"`
}

// Resolved is a validated configuration with defaults applied.
type Resolved struct {
	Config
	Owner    string
	Source   repo.Repo
	Theme    repo.Repo
	Deploy   repo.Repo
	Alias    string
	KeyPath  string
	Dir      string
	Branch   string
	NodeWant string
	Strategy string
	BaseDir  string
}

var (
	aliasRE = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,62}[A-Za-z0-9])?$`)
	majorRE = regexp.MustCompile(`^[0-9]{1,2}(\.[0-9]+){0,2}$`)
)

func validBranch(b string) bool {
	if b == "" || strings.HasPrefix(b, "-") || strings.Contains(b, "..") ||
		strings.HasPrefix(b, "/") || strings.HasSuffix(b, "/") ||
		strings.HasSuffix(b, ".") || strings.HasSuffix(b, ".lock") ||
		strings.Contains(b, "//") || strings.Contains(b, "@{") || strings.ContainsRune(b, ' ') {
		return false
	}
	for _, r := range b {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-', r == '/':
		default:
			return false
		}
	}
	return true
}

// Load reads and strictly decodes a configuration file. Unknown fields are
// rejected so that typos in field names cannot silently fall back to defaults.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, protocol.Fatal("CONFIG_NOT_FOUND",
				fmt.Sprintf("找不到配置文件 %s；请从 blog-setup.example.yml 复制一份并填写", path))
		}
		return nil, protocol.Fatal("CONFIG_INVALID", fmt.Sprintf("无法读取配置文件 %s: %v", path, err)).WithCause(err)
	}
	defer f.Close()

	cfg := &Config{}
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		return nil, protocol.Fatal("CONFIG_INVALID", fmt.Sprintf("配置文件 %s 解析失败: %v", path, err)).WithCause(err)
	}
	var extra interface{}
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, protocol.Fatal("CONFIG_INVALID", "配置文件只能包含一个 YAML 文档")
	}
	return cfg, nil
}

// Validate checks required fields and enum values without consulting the
// environment.
func (c *Config) Validate() error {
	if c.Version != 1 {
		return protocol.Fatal("CONFIG_INVALID", fmt.Sprintf("不支持的配置版本 %d；当前仅支持 version: 1", c.Version))
	}
	for name, value := range map[string]string{
		"repos.source": c.Repos.Source,
		"repos.theme":  c.Repos.Theme,
		"repos.deploy": c.Repos.Deploy,
	} {
		if strings.TrimSpace(value) == "" {
			return protocol.Fatal("CONFIG_INVALID", fmt.Sprintf("缺少必填字段 %s", name))
		}
	}
	if strings.TrimSpace(c.GitIdentity.Name) == "" {
		return protocol.Fatal("CONFIG_INVALID", "缺少必填字段 git_identity.name")
	}
	if !strings.Contains(c.GitIdentity.Email, "@") {
		return protocol.Fatal("CONFIG_INVALID", "git_identity.email 不是有效邮箱")
	}
	if c.Auth.Alias != "" && !aliasRE.MatchString(c.Auth.Alias) {
		return protocol.Fatal("CONFIG_INVALID", fmt.Sprintf("auth.alias %q 不是合法的 SSH 别名", c.Auth.Alias))
	}
	if c.Toolchain.Strategy != "" && c.Toolchain.Strategy != "system_first" && c.Toolchain.Strategy != "isolated" {
		return protocol.Fatal("CONFIG_INVALID", "toolchain.strategy 只能是 system_first 或 isolated")
	}
	if c.Toolchain.NodeVersion != "" {
		if !majorRE.MatchString(c.Toolchain.NodeVersion) {
			return protocol.Fatal("CONFIG_INVALID", fmt.Sprintf("toolchain.node_version %q 不是合法版本号", c.Toolchain.NodeVersion))
		}
		major, _ := strconv.Atoi(strings.SplitN(c.Toolchain.NodeVersion, ".", 2)[0])
		if major < 18 || major > 99 {
			return protocol.Fatal("CONFIG_INVALID", fmt.Sprintf("toolchain.node_version 主版本 %d 不在支持区间 18-99", major))
		}
	}
	if c.Workspace.Branch != "" && !validBranch(c.Workspace.Branch) {
		return protocol.Fatal("CONFIG_INVALID", fmt.Sprintf("workspace.branch %q 不是合法分支名", c.Workspace.Branch))
	}
	if strings.ContainsRune(c.Workspace.Dir, '\x00') {
		return protocol.Fatal("CONFIG_INVALID", "workspace.dir 包含非法字符")
	}
	return nil
}

// Resolve validates the configuration and applies all documented defaults.
// baseDir is used to anchor relative workspace directories and key paths.
func (c *Config) Resolve(home, baseDir string) (*Resolved, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	source, err := parseRepo("repos.source", c.Repos.Source)
	if err != nil {
		return nil, err
	}
	theme, err := parseRepo("repos.theme", c.Repos.Theme)
	if err != nil {
		return nil, err
	}
	deploy, err := parseRepo("repos.deploy", c.Repos.Deploy)
	if err != nil {
		return nil, err
	}

	owner := source.Owner
	if !strings.EqualFold(theme.Owner, owner) || !strings.EqualFold(deploy.Owner, owner) {
		return nil, protocol.Fatal("CONFIG_OWNER_MISMATCH", fmt.Sprintf(
			"三个仓库必须属于同一账号：source=%s, theme=%s, deploy=%s", source.Owner, theme.Owner, deploy.Owner)).
			WithDetails(map[string]string{"source_owner": source.Owner, "theme_owner": theme.Owner, "deploy_owner": deploy.Owner})
	}
	wantDeploy := owner + ".github.io"
	if !strings.EqualFold(deploy.Name, wantDeploy) {
		return nil, protocol.Fatal("DEPLOY_REPO_NAME_INVALID", fmt.Sprintf(
			"部署仓库必须精确命名为 %s，当前为 %s", wantDeploy, deploy.Name)).
			WithDetails(map[string]string{"expected": wantDeploy, "actual": deploy.Name})
	}

	alias := c.Auth.Alias
	if alias == "" {
		alias = owner
	}

	home = cleanHome(home)
	keyPath := c.Auth.Key
	if keyPath == "" {
		keyPath = filepath.Join("~", ".ssh", "id_ed25519_"+owner)
	}
	keyPath = expandHome(keyPath, home)
	if !filepath.IsAbs(keyPath) {
		keyPath = filepath.Join(baseDir, keyPath)
	}
	keyPath = filepath.Clean(keyPath)

	dir := c.Workspace.Dir
	if dir == "" {
		dir = source.Name
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(baseDir, dir)
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return nil, protocol.Fatal("CONFIG_INVALID", fmt.Sprintf("无法解析 workspace.dir: %v", err)).WithCause(err)
	}

	branch := c.Workspace.Branch
	if branch == "" {
		branch = "master"
	}
	strategy := c.Toolchain.Strategy
	if strategy == "" {
		strategy = "system_first"
	}
	nodeWant := c.Toolchain.NodeVersion
	if nodeWant == "" {
		nodeWant = "22"
	}

	return &Resolved{
		Config:   *c,
		Owner:    owner,
		Source:   source,
		Theme:    theme,
		Deploy:   deploy,
		Alias:    alias,
		KeyPath:  keyPath,
		Dir:      dir,
		Branch:   branch,
		NodeWant: nodeWant,
		Strategy: strategy,
		BaseDir:  baseDir,
	}, nil
}

func parseRepo(field, raw string) (repo.Repo, error) {
	r, err := repo.Parse(raw)
	if err != nil {
		return repo.Repo{}, protocol.Fatal("CONFIG_URL_INVALID",
			fmt.Sprintf("%s 不是受支持的 GitHub 仓库地址: %v", field, err)).WithCause(err)
	}
	return r, nil
}

func cleanHome(home string) string {
	if home != "" {
		return home
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

func expandHome(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		return filepath.Join(home, path[2:])
	}
	return path
}
