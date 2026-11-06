package steps

import (
	"context"
	"os"
	"path/filepath"

	"github.com/celestezj/blog-bootstrap/internal/core"
	"github.com/celestezj/blog-bootstrap/internal/plan"
	"github.com/celestezj/blog-bootstrap/internal/protocol"
	"github.com/celestezj/blog-bootstrap/internal/run"
	"github.com/celestezj/blog-bootstrap/internal/toolchain"
	"github.com/celestezj/blog-bootstrap/internal/workspace"
)

// PreviewStep installs locked dependencies, builds the site and proves it can
// be served locally. The smoke server runs only as a strict probe; apply never
// marks the build successful.
type PreviewStep struct {
	Ctx   *core.Context
	Nodes *NodeStep
}

func (s *PreviewStep) ID() string     { return "preview" }
func (s *PreviewStep) Required() bool { return true }

func (s *PreviewStep) lockfile() string {
	return toolchain.LockfilePath(s.Ctx.Config.Dir)
}

func (s *PreviewStep) cacheKey(lockfile, nodeVersion string) (string, error) {
	data, err := os.ReadFile(lockfile)
	if err != nil {
		return "", err
	}
	return toolchain.NpmCacheKey(data, nodeVersion), nil
}

func (s *PreviewStep) depsReady() bool {
	if _, ok := workspace.HexoMain(s.Ctx.Config.Dir); !ok {
		return false
	}
	st, err := os.Stat(filepath.Join(s.Ctx.Config.Dir, "node_modules"))
	return err == nil && st.IsDir()
}

func (s *PreviewStep) builder(rt *nodeRuntime) *workspace.Builder {
	return &workspace.Builder{
		Runner: s.Ctx.Runner,
		Env:    rt.Env,
		Logger: s.Ctx.Logger,
		StepID: "preview",
		GOOS:   s.Ctx.GOOS,
	}
}

func (s *PreviewStep) server(rt *nodeRuntime) *workspace.Server {
	return &workspace.Server{
		Env:    rt.Env,
		Logger: s.Ctx.Logger,
		StepID: "preview",
		Wait:   s.Ctx.Wait,
		Notify: func(line string) { s.Ctx.Sayf("  %s", line) },
	}
}

func (s *PreviewStep) Inspect(ctx context.Context) (run.Inspection, error) {
	dir := s.Ctx.Config.Dir
	evidence := map[string]string{"dir": dir}
	rt, rtOK, err := s.Nodes.current(ctx)
	if err != nil {
		return run.Inspection{}, err
	}
	if rtOK {
		evidence["node_version"] = rt.Node.Version.String()
	}

	lockfile := s.lockfile()
	if lockfile == "" {
		evidence["lockfile"] = "missing"
	} else {
		evidence["lockfile"] = lockfile
	}

	artifactsOK := false
	if art, err := workspace.VerifyArtifacts(dir); err == nil {
		artifactsOK = true
		for k, v := range art {
			evidence[k] = v
		}
	}

	hexo, hexoOK := workspace.HexoMain(dir)
	if hexoOK {
		evidence["hexo_local"] = hexo
	}

	key := ""
	if rtOK && lockfile != "" {
		key, err = s.cacheKey(lockfile, rt.Node.Version.String())
		if err != nil {
			return run.Inspection{}, err
		}
		evidence["npm_cache_key"] = key
		if s.Ctx.State.Snapshot().NPMCacheKey == key {
			evidence["npm_cache"] = "hit"
		}
	}

	if rtOK && artifactsOK && hexoOK && s.depsReady() && key != "" &&
		s.Ctx.State.Snapshot().NPMCacheKey == key {
		return run.Inspection{Satisfied: true, Evidence: evidence}, nil
	}

	actions := []plan.Action{{
		Kind:   "npm_ci",
		Target: dir,
		Detail: "按 package-lock.json 执行 npm ci（缓存键 = lockfile SHA256 + Node 版本）",
	}, {
		Kind:   "build",
		Target: dir,
		Detail: "依次执行 hexo clean、hexo generate 与 clean-search.js，并校验 public/index.html、public/search.json",
	}, {
		Kind:   "serve",
		Target: "127.0.0.1",
		Detail: "在空闲端口启动临时 hexo server，轮询 HTTP 200 后回收整个进程树",
	}}
	return run.Inspection{Actions: actions, Evidence: evidence}, nil
}

func (s *PreviewStep) Apply(ctx context.Context) error {
	rt, err := s.Nodes.EnsureRuntime(ctx, s.Nodes.DesiredVersion())
	if err != nil {
		return err
	}
	lockfile := s.lockfile()
	if lockfile == "" {
		return protocol.Fatal("NPM_INSTALL_FAILED", "仓库缺少 package-lock.json，拒绝改用 npm install")
	}
	key, err := s.cacheKey(lockfile, rt.Node.Version.String())
	if err != nil {
		return protocol.Fatal("NPM_INSTALL_FAILED", "无法读取 lockfile: "+err.Error()).WithCause(err)
	}

	builder := s.builder(rt)
	if s.Ctx.State.Snapshot().NPMCacheKey != key || !s.depsReady() {
		s.Ctx.Sayf("正在安装项目依赖（npm ci，文件较多时可能需要几分钟）...")
		if err := builder.InstallDependencies(ctx, s.Ctx.Config.Dir, rt.Node.Path, rt.Npm.Path); err != nil {
			return err
		}
		s.Ctx.Sayf("依赖安装完成")
		if err := s.Ctx.State.SetNPMCacheKey(key); err != nil {
			return protocol.Fatal("STATE_WRITE_FAILED", "无法写入状态文件: "+err.Error()).WithCause(err)
		}
	}
	s.Ctx.Sayf("正在构建站点（hexo clean && hexo generate && clean-search）...")
	if _, err := builder.Build(ctx, s.Ctx.Config.Dir, rt.Node.Path); err != nil {
		return err
	}
	s.Ctx.Sayf("站点构建完成，产物位于 %s", filepath.Join(s.Ctx.Config.Dir, "public"))
	return nil
}

func (s *PreviewStep) Verify(ctx context.Context) (map[string]string, error) {
	dir := s.Ctx.Config.Dir
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, protocol.Verify("BUILD_FAILED", "工作区不存在或不是目录: "+dir)
	}
	rt, ok, err := s.Nodes.current(ctx)
	if err != nil {
		return nil, asVerify(err)
	}
	if !ok {
		node, npm, detectErr := s.Nodes.detect(ctx)
		if detectErr != nil {
			return nil, asVerify(detectErr)
		}
		switch {
		case node == nil:
			return nil, protocol.Verify("TOOLS_MISSING", "找不到 node，请先完成 node 步骤")
		case !toolchain.NodeSupported(node.Version):
			return nil, protocol.Verify("VERSION_MISMATCH", "Node.js 版本过低: "+node.Version.String())
		case !toolchain.MatchWant(s.Nodes.DesiredVersion(), node.Version):
			return nil, protocol.Verify("VERSION_MISMATCH", "Node.js 版本不满足仓库 .node-version")
		case npm == nil:
			return nil, protocol.Verify("NPM_MISSING", "找到 node 但无法执行 npm")
		default:
			return nil, protocol.Verify("VERSION_MISMATCH", "Node.js 运行时未满足要求")
		}
	}
	if err := s.Nodes.storePaths(rt); err != nil {
		return nil, asVerify(err)
	}
	hexo, ok := workspace.HexoMain(dir)
	if !ok {
		return nil, protocol.Verify("BUILD_FAILED", "项目本地 hexo 未安装，请重新执行 npm ci")
	}
	evidence, err := workspace.VerifyArtifacts(dir)
	if err != nil {
		return nil, asVerify(err)
	}
	lockfile := s.lockfile()
	if lockfile == "" {
		return nil, protocol.Verify("NPM_INSTALL_FAILED", "仓库缺少 package-lock.json")
	}
	key, err := s.cacheKey(lockfile, rt.Node.Version.String())
	if err != nil {
		return nil, protocol.Verify("NPM_INSTALL_FAILED", "无法读取 lockfile: "+err.Error())
	}
	s.Ctx.Sayf("  正在启动临时预览服务并确认页面返回 HTTP 200...")
	smoke, err := s.server(rt).Smoke(ctx, dir, rt.Node.Path, hexo)
	if err != nil {
		return nil, asVerify(err)
	}
	evidence["hexo_local"] = hexo
	evidence["node_version"] = rt.Node.Version.String()
	evidence["npm_version"] = rt.Npm.Version.String()
	evidence["lockfile"] = lockfile
	evidence["npm_cache_key"] = key
	for k, v := range smoke {
		evidence[k] = v
	}
	return evidence, nil
}
