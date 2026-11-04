package steps

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/celestezj/blog-bootstrap/internal/core"
	"github.com/celestezj/blog-bootstrap/internal/execx"
	"github.com/celestezj/blog-bootstrap/internal/plan"
	"github.com/celestezj/blog-bootstrap/internal/protocol"
	"github.com/celestezj/blog-bootstrap/internal/run"
	"github.com/celestezj/blog-bootstrap/internal/state"
	"github.com/celestezj/blog-bootstrap/internal/toolchain"
	"github.com/celestezj/blog-bootstrap/internal/workspace"
)

// HexoStep provisions the optional global hexo-cli. Build and verification
// always use the project-local hexo instead.
type HexoStep struct {
	Ctx   *core.Context
	Nodes *NodeStep
}

func (s *HexoStep) ID() string     { return "hexo" }
func (s *HexoStep) Required() bool { return false }

func (s *HexoStep) locate(ctx context.Context, rt *nodeRuntime) (string, *toolchain.Version, error) {
	if rt == nil {
		return "", nil, nil
	}
	name, args := toolchain.NpmInvocation(rt.Node.Path, rt.Npm.Path, s.Ctx.GOOS, "root", "-g")
	res := s.Ctx.Runner.Run(ctx, "", rt.Env, name, args...)
	if res.Failed() {
		return "", nil, nil
	}
	root := strings.TrimSpace(res.Stdout)
	for _, candidate := range []string{
		filepath.Join(root, "hexo-cli", "bin", "hexo"),
		filepath.Join(root, "hexo-cli", "bin", "hexo.js"),
		filepath.Join(root, "hexo", "bin", "hexo"),
	} {
		if !fileExists(candidate) {
			continue
		}
		versionRes := s.Ctx.Runner.Run(ctx, "", rt.Env, rt.Node.Path, candidate, "version")
		if versionRes.Failed() {
			continue
		}
		v, err := toolchain.ParseVersion(versionRes.Combined())
		if err != nil {
			continue
		}
		return candidate, &v, nil
	}
	return "", nil, nil
}

func (s *HexoStep) Inspect(ctx context.Context) (run.Inspection, error) {
	rt, ok, err := s.Nodes.current(ctx)
	if err != nil {
		return run.Inspection{}, err
	}
	if !ok {
		return run.Inspection{Actions: []plan.Action{{
			Kind:   "npm_global",
			Target: "hexo-cli",
			Detail: "可选：安装全局 hexo-cli；失败不阻断安装",
		}}}, nil
	}
	path, version, err := s.locate(ctx, rt)
	if err != nil {
		return run.Inspection{}, err
	}
	if path != "" && version.Major == 7 {
		return run.Inspection{Satisfied: true, Evidence: map[string]string{
			"hexo_version": version.String(),
			"hexo_path":    path,
		}}, nil
	}
	actions := []plan.Action{{
		Kind:   "npm_global",
		Target: "hexo-cli",
		Detail: "可选：安装/升级全局 hexo-cli 到 7.x；失败不阻断安装",
	}}
	return run.Inspection{Actions: actions}, nil
}

func (s *HexoStep) Apply(ctx context.Context) error {
	rt, ok, err := s.Nodes.current(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return protocol.Fatal("TOOLS_MISSING", "Node.js 未就绪，无法安装 hexo-cli")
	}
	name, args := toolchain.NpmInvocation(rt.Node.Path, rt.Npm.Path, s.Ctx.GOOS, "install", "-g", "hexo-cli@latest")
	res := s.Ctx.Runner.Run(ctx, "", rt.Env, name, args...)
	logPath := ""
	if s.Ctx.Logger != nil {
		logPath = s.Ctx.Logger.Append("hexo", "npm install -g hexo-cli",
			execx.FormatCommand(name, args)+"\n"+res.Combined())
	}
	if res.Failed() {
		return protocol.Fatal("NPM_INSTALL_FAILED", "安装全局 hexo-cli 失败: "+strings.TrimSpace(res.Combined())).WithLog(logPath)
	}
	return workspace.RecordChange(s.Ctx, func(c *state.Changes) {
		c.SystemPackages = append(c.SystemPackages, state.PackageChange{Manager: "npm", Package: "hexo-cli", Action: "install"})
	})
}

func (s *HexoStep) Verify(ctx context.Context) (map[string]string, error) {
	rt, ok, err := s.Nodes.current(ctx)
	if err != nil {
		return nil, asVerify(err)
	}
	if !ok {
		return nil, protocol.Verify("HEXO_MISSING", "Node.js 未就绪，无法验证 hexo-cli")
	}
	path, version, err := s.locate(ctx, rt)
	if err != nil {
		return nil, asVerify(err)
	}
	if path == "" {
		return nil, protocol.Verify("HEXO_MISSING", "未安装全局 hexo-cli（可选步骤）")
	}
	if version.Major != 7 {
		return nil, protocol.Verify("VERSION_MISMATCH", "hexo 版本为 "+version.String()+"，期望 7.x")
	}
	return map[string]string{
		"hexo_version": version.String(),
		"hexo_path":    path,
		"hexo_major":   strconv.Itoa(version.Major),
	}, nil
}
