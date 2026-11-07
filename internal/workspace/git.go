// Package workspace clones the blog repository, pins the theme and verifies a
// reproducible build and local preview.
package workspace

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/celestezj/blog-bootstrap/internal/core"
	"github.com/celestezj/blog-bootstrap/internal/execx"
	"github.com/celestezj/blog-bootstrap/internal/netx"
	"github.com/celestezj/blog-bootstrap/internal/protocol"
	"github.com/celestezj/blog-bootstrap/internal/repo"
)

// ThemePath is the gitlink location of the A4 theme.
const ThemePath = "themes/A4"

// Git executes git with a resolved absolute path and optional logging.
type Git struct {
	Runner execx.Runner
	Path   string
	Env    []string
	Logger *core.Logger
	StepID string
}

func (g *Git) run(ctx context.Context, dir, label string, args ...string) (execx.Result, string) {
	res := g.Runner.Run(ctx, dir, g.Env, g.Path, args...)
	logPath := ""
	if g.Logger != nil {
		logPath = g.Logger.Append(g.StepID, label+"\n"+execx.FormatCommand(g.Path, args), res.Combined())
	}
	return res, logPath
}

// TargetState describes an existing target directory.
type TargetState struct {
	Exists bool
	Remote string
	Head   string
}

// InspectTarget checks the target directory without modifying it. A directory
// belonging to a different repository is a deterministic REPO_CONFLICT.
func (g *Git) InspectTarget(ctx context.Context, dir, sourceURL string) (TargetState, error) {
	st, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return TargetState{}, nil
		}
		return TargetState{}, protocol.Fatal("WORKSPACE_ACCESS_FAILED", "无法检查目标目录: "+err.Error()).WithCause(err)
	}
	if !st.IsDir() {
		return TargetState{}, protocol.Fatal("REPO_CONFLICT", "目标路径已存在且不是目录: "+dir)
	}
	if !g.IsGitRepo(ctx, dir) {
		return TargetState{}, protocol.Fatal("REPO_CONFLICT", "目标目录已存在但不是 git 仓库: "+dir)
	}
	remoteRes := g.Runner.Run(ctx, dir, g.Env, g.Path, "remote", "get-url", "origin")
	if remoteRes.Failed() {
		return TargetState{}, protocol.Fatal("REPO_CONFLICT", "目标目录没有 origin remote: "+dir)
	}
	remote := strings.TrimSpace(remoteRes.Stdout)
	if !repo.SameRepo(remote, sourceURL) {
		return TargetState{}, protocol.Fatal("REPO_CONFLICT",
			"目标目录属于其他仓库，拒绝覆盖").
			WithDetails(map[string]string{"expected": sourceURL, "actual": remote, "dir": dir})
	}
	head := ""
	if headRes := g.Runner.Run(ctx, dir, g.Env, g.Path, "rev-parse", "HEAD"); !headRes.Failed() {
		head = strings.TrimSpace(headRes.Stdout)
	}
	return TargetState{Exists: true, Remote: remote, Head: head}, nil
}

// HeadCommit returns the commit of dir when dir itself is a git work tree.
func (g *Git) HeadCommit(ctx context.Context, dir string) (string, bool) {
	if !g.IsGitRepo(ctx, dir) {
		return "", false
	}
	head, err := g.head(ctx, dir)
	if err != nil {
		return "", false
	}
	return head, true
}

// CheckoutCommit detaches the theme work tree at the recorded gitlink.
func (g *Git) CheckoutCommit(ctx context.Context, dir, commit string) error {
	res, logPath := g.run(ctx, dir, "git checkout theme", "checkout", "--detach", commit)
	if res.Failed() {
		return protocol.Fatal("THEME_MISMATCH", "无法检出主题提交 "+commit+": "+strings.TrimSpace(res.Combined())).WithLog(logPath)
	}
	return nil
}

// EnsureCheckout reuses a matching existing checkout instead of cloning, then
// validates the remote and branch. The target directory is never deleted.
func (g *Git) EnsureCheckout(ctx context.Context, sourceURL, dir, branch string) (map[string]string, error) {
	target, err := g.InspectTarget(ctx, dir, sourceURL)
	if err != nil {
		return nil, err
	}
	if !target.Exists {
		if err := g.Clone(ctx, sourceURL, dir, branch); err != nil {
			return nil, err
		}
	}
	return g.VerifyCheckout(ctx, dir, sourceURL, branch)
}

// Clone clones the configured branch into dir.
func (g *Git) Clone(ctx context.Context, sourceURL, dir, branch string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return protocol.Fatal("CLONE_FAILED", "无法创建父目录: "+err.Error()).WithCause(err)
	}
	res, logPath := g.run(ctx, filepath.Dir(dir), "git clone", "clone", "--branch", branch, sourceURL, dir)
	if res.Failed() {
		out := strings.TrimSpace(res.Combined())
		if netx.IsConnectFailure(out) {
			return protocol.Retry("NETWORK_UNREACHABLE", "克隆博客仓库失败: "+out).WithLog(logPath)
		}
		if strings.Contains(out, "Remote branch") && strings.Contains(out, "not found") {
			return protocol.Fatal("BRANCH_NOT_FOUND", "源仓库不存在分支 "+branch).WithLog(logPath)
		}
		return protocol.Fatal("CLONE_FAILED", "克隆博客仓库失败: "+out).WithLog(logPath)
	}
	return nil
}

// VerifyCheckout verifies remote and branch without writing anything.
func (g *Git) VerifyCheckout(ctx context.Context, dir, sourceURL, branch string) (map[string]string, error) {
	res := g.Runner.Run(ctx, dir, g.Env, g.Path, "remote", "get-url", "origin")
	if res.Failed() {
		return nil, protocol.Verify("REMOTE_MISMATCH", "无法读取 origin remote")
	}
	remote := strings.TrimSpace(res.Stdout)
	if !repo.SameRepo(remote, sourceURL) {
		return nil, protocol.Verify("REMOTE_MISMATCH", "origin remote 与配置不一致: "+remote)
	}
	branchRes := g.Runner.Run(ctx, dir, g.Env, g.Path, "rev-parse", "--abbrev-ref", "HEAD")
	if branchRes.Failed() {
		return nil, protocol.Verify("BRANCH_MISMATCH", "无法读取当前分支")
	}
	gotBranch := strings.TrimSpace(branchRes.Stdout)
	if gotBranch != branch {
		return nil, protocol.Verify("BRANCH_MISMATCH", "当前分支为 "+gotBranch+"，期望 "+branch)
	}
	headRes := g.Runner.Run(ctx, dir, g.Env, g.Path, "rev-parse", "HEAD")
	evidence := map[string]string{"remote": remote, "branch": gotBranch}
	if !headRes.Failed() {
		evidence["head"] = strings.TrimSpace(headRes.Stdout)
	}
	return evidence, nil
}

// SubmodulePath reports the declared path of a named submodule.
func (g *Git) SubmodulePath(ctx context.Context, dir, name string) (string, bool, error) {
	res := g.Runner.Run(ctx, dir, g.Env, g.Path, "config", "-f", ".gitmodules", "--get", "submodule."+name+".path")
	if res.Err != nil {
		return "", false, protocol.Fatal("SUBMODULE_FAILED", "无法读取 .gitmodules: "+res.Err.Error()).WithCause(res.Err)
	}
	if res.ExitCode != 0 {
		return "", false, nil
	}
	return strings.TrimSpace(res.Stdout), true, nil
}

// ConfigureSubmoduleURL overrides the submodule URL in the local repository
// config only; .gitmodules is never modified.
func (g *Git) ConfigureSubmoduleURL(ctx context.Context, dir, name, url string) error {
	res, logPath := g.run(ctx, dir, "configure submodule",
		"config", "submodule."+name+".url", url)
	if res.Failed() {
		return protocol.Fatal("SUBMODULE_FAILED", "无法写入子模块 URL 覆盖: "+strings.TrimSpace(res.Combined())).WithLog(logPath)
	}
	return nil
}

// InitSubmodules initializes recursively, retrying once as specified.
func (g *Git) InitSubmodules(ctx context.Context, dir string) error {
	res, logPath := g.run(ctx, dir, "submodule update", "submodule", "update", "--init", "--recursive")
	if !res.Failed() {
		return nil
	}
	res, logPath = g.run(ctx, dir, "submodule update (retry)", "submodule", "update", "--init", "--recursive")
	if res.Failed() {
		return protocol.Fatal("SUBMODULE_FAILED", "子模块初始化失败: "+strings.TrimSpace(res.Combined())).WithLog(logPath)
	}
	return nil
}

// VerifySubmodule proves the submodule is initialized and its HEAD equals the
// parent commit's gitlink.
func (g *Git) VerifySubmodule(ctx context.Context, dir, name string) (map[string]string, error) {
	status := g.Runner.Run(ctx, dir, g.Env, g.Path, "submodule", "status", name)
	if status.Err != nil {
		return nil, protocol.Verify("SUBMODULE_FAILED", "git submodule status 执行失败")
	}
	for _, line := range strings.Split(strings.TrimSpace(status.Stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "-") {
			return nil, protocol.Verify("SUBMODULE_FAILED", "子模块未初始化: "+line)
		}
	}
	gitlink, err := g.ThemeGitlink(ctx, dir, name)
	if err != nil {
		return nil, protocol.Verify("SUBMODULE_FAILED", "无法读取父仓库 gitlink: "+err.Error())
	}
	themeDir := filepath.Join(dir, filepath.FromSlash(name))
	headRes := g.Runner.Run(ctx, themeDir, g.Env, g.Path, "rev-parse", "HEAD")
	if headRes.Failed() {
		return nil, protocol.Verify("SUBMODULE_FAILED", "无法读取主题 HEAD")
	}
	head := strings.TrimSpace(headRes.Stdout)
	if head != gitlink {
		return nil, protocol.Verify("SUBMODULE_FAILED",
			"主题 HEAD 与父仓库 gitlink 不一致").WithDetails(map[string]string{"head": head, "gitlink": gitlink})
	}
	return map[string]string{"theme_head": head, "gitlink": gitlink}, nil
}

// ThemeGitlink returns the commit recorded for the theme path in HEAD.
func (g *Git) ThemeGitlink(ctx context.Context, dir, themePath string) (string, error) {
	res := g.Runner.Run(ctx, dir, g.Env, g.Path, "rev-parse", "HEAD:"+themePath)
	if res.Failed() {
		return "", protocol.Fatal("THEME_TRACKING_MISSING", "仓库未记录主题路径 "+themePath)
	}
	return strings.TrimSpace(res.Stdout), nil
}

// IsGitRepo reports whether dir itself is a git work tree root, not merely a
// directory nested inside another repository.
func (g *Git) IsGitRepo(ctx context.Context, dir string) bool {
	res := g.Runner.Run(ctx, dir, g.Env, g.Path, "rev-parse", "--show-toplevel")
	if res.Failed() {
		return false
	}
	top := strings.TrimSpace(res.Stdout)
	if top == "" {
		return false
	}
	absDir, errDir := filepath.Abs(dir)
	absTop, errTop := filepath.Abs(top)
	if errDir != nil || errTop != nil {
		return false
	}
	a, b := filepath.Clean(absDir), filepath.Clean(absTop)
	// Compare by file identity so symlinked paths still match: on macOS
	// t.TempDir() yields /var/..., while git reports /private/var/....
	if ai, aErr := os.Stat(a); aErr == nil {
		if bi, bErr := os.Stat(b); bErr == nil {
			return os.SameFile(ai, bi)
		}
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// CloneTheme clones the theme separately when no submodule is declared, then
// pins it to the parent's recorded commit.
func (g *Git) CloneTheme(ctx context.Context, themeURL, themeDir, gitlink string) error {
	if g.IsGitRepo(ctx, themeDir) {
		head, err := g.head(ctx, themeDir)
		if err != nil {
			return err
		}
		if head != gitlink {
			return protocol.Verify("THEME_MISMATCH", "主题 HEAD 与父仓库 gitlink 不一致").
				WithDetails(map[string]string{"head": head, "gitlink": gitlink})
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(themeDir), 0o755); err != nil {
		return protocol.Fatal("THEME_CLONE_FAILED", err.Error()).WithCause(err)
	}
	res, logPath := g.run(ctx, filepath.Dir(themeDir), "git clone theme", "clone", themeURL, themeDir)
	if res.Failed() {
		out := strings.TrimSpace(res.Combined())
		if netx.IsConnectFailure(out) {
			return protocol.Retry("NETWORK_UNREACHABLE", "克隆主题仓库失败: "+out).WithLog(logPath)
		}
		return protocol.Fatal("THEME_CLONE_FAILED", "克隆主题仓库失败: "+out).WithLog(logPath)
	}
	checkout, logPath := g.run(ctx, themeDir, "theme checkout", "checkout", gitlink)
	if checkout.Failed() {
		return protocol.Fatal("THEME_MISMATCH", "无法检出主题提交 "+gitlink+": "+strings.TrimSpace(checkout.Combined())).WithLog(logPath)
	}
	return g.VerifyTheme(ctx, themeDir, gitlink)
}

// VerifyTheme proves the theme work tree is at the parent-recorded commit.
func (g *Git) VerifyTheme(ctx context.Context, themeDir, gitlink string) error {
	head, err := g.head(ctx, themeDir)
	if err != nil {
		return err
	}
	if head != gitlink {
		return protocol.Verify("THEME_MISMATCH", "主题 HEAD 与父仓库 gitlink 不一致").
			WithDetails(map[string]string{"head": head, "gitlink": gitlink})
	}
	return nil
}

func (g *Git) head(ctx context.Context, dir string) (string, error) {
	res := g.Runner.Run(ctx, dir, g.Env, g.Path, "rev-parse", "HEAD")
	if res.Failed() {
		return "", protocol.Verify("THEME_MISMATCH", "无法读取主题 HEAD: "+strings.TrimSpace(res.Combined()))
	}
	return strings.TrimSpace(res.Stdout), nil
}
