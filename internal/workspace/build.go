package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/celestezj/blog-bootstrap/internal/core"
	"github.com/celestezj/blog-bootstrap/internal/execx"
	"github.com/celestezj/blog-bootstrap/internal/protocol"
	"github.com/celestezj/blog-bootstrap/internal/toolchain"
)

// HexoMain locates the project-local hexo CLI entry script. It is executed
// with node, never through an npm/.cmd shim.
func HexoMain(dir string) (string, bool) {
	candidates := []string{
		filepath.Join(dir, "node_modules", "hexo", "bin", "hexo"),
		filepath.Join(dir, "node_modules", "hexo-cli", "bin", "hexo"),
		filepath.Join(dir, "node_modules", "hexo-cli", "bin", "hexo.js"),
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, true
		}
	}
	return "", false
}

// Builder runs npm ci, hexo clean/generate and clean-search.js in the cloned
// repository, then verifies the generated artifacts.
type Builder struct {
	Runner execx.Runner
	Env    []string
	Logger *core.Logger
	StepID string
	GOOS   string
}

func (b *Builder) run(ctx context.Context, dir, label string, name string, args ...string) (execx.Result, string) {
	res := b.Runner.Run(ctx, dir, b.Env, name, args...)
	logPath := ""
	if b.Logger != nil {
		logPath = b.Logger.Append(b.StepID, label+"\n"+execx.FormatCommand(name, args), res.Combined())
	}
	return res, logPath
}

func (b *Builder) tail() string {
	if b.Logger == nil {
		return ""
	}
	return b.Logger.Tail(b.StepID)
}

func (b *Builder) failure(code, message, logPath string, res execx.Result) error {
	details := map[string]string{}
	if out := strings.TrimSpace(res.Combined()); out != "" {
		details["output"] = out
	}
	if tail := b.tail(); tail != "" {
		details["tail"] = tail
	}
	return protocol.Fatal(code, message).WithLog(logPath).WithDetails(details)
}

// InstallDependencies runs npm ci. It returns a classified failure with the
// log path and output tail so the caller can diagnose without rerunning.
func (b *Builder) InstallDependencies(ctx context.Context, dir, nodePath, npmMain string) error {
	name, args := toolchain.NpmInvocation(nodePath, npmMain, b.GOOS, "ci")
	res, logPath := b.run(ctx, dir, "npm ci", name, args...)
	if res.Failed() {
		return b.failure("NPM_INSTALL_FAILED", "npm ci 失败: "+strings.TrimSpace(res.Combined()), logPath, res)
	}
	return nil
}

// Build runs the documented build sequence and verifies both required
// artifacts exist and are non-empty.
func (b *Builder) Build(ctx context.Context, dir, nodePath string) (map[string]string, error) {
	hexo, ok := HexoMain(dir)
	if !ok {
		return nil, protocol.Fatal("BUILD_FAILED", "无法定位项目本地 hexo（请确认依赖已安装）")
	}
	for _, command := range []string{"clean", "generate"} {
		res, logPath := b.run(ctx, dir, "hexo "+command, nodePath, hexo, command)
		if res.Failed() {
			return nil, b.failure("BUILD_FAILED", "hexo "+command+" 失败: "+strings.TrimSpace(res.Combined()), logPath, res)
		}
	}
	cleanScript := filepath.Join(dir, "clean-search.js")
	if st, err := os.Stat(cleanScript); err != nil || st.IsDir() {
		return nil, protocol.Fatal("BUILD_FAILED", "仓库缺少 clean-search.js，无法生成 search.json")
	}
	res, logPath := b.run(ctx, dir, "clean-search.js", nodePath, cleanScript)
	if res.Failed() {
		return nil, b.failure("BUILD_FAILED", "clean-search.js 失败: "+strings.TrimSpace(res.Combined()), logPath, res)
	}
	return VerifyArtifacts(dir)
}

// VerifyArtifacts proves both generated files exist and are non-empty.
func VerifyArtifacts(dir string) (map[string]string, error) {
	evidence := map[string]string{}
	for _, name := range []string{"index.html", "search.json"} {
		path := filepath.Join(dir, "public", name)
		st, err := os.Stat(path)
		if err != nil || st.IsDir() {
			return nil, protocol.Fatal("BUILD_FAILED", "构建产物缺失: public/"+name).
				WithDetails(map[string]string{"path": path})
		}
		if st.Size() == 0 {
			return nil, protocol.Fatal("BUILD_FAILED", "构建产物为空: public/"+name).
				WithDetails(map[string]string{"path": path})
		}
		evidence["public/"+name] = strconv.FormatInt(st.Size(), 10)
	}
	return evidence, nil
}
