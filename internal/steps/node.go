package steps

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/celestezj/blog-bootstrap/internal/core"
	"github.com/celestezj/blog-bootstrap/internal/plan"
	"github.com/celestezj/blog-bootstrap/internal/protocol"
	"github.com/celestezj/blog-bootstrap/internal/run"
	"github.com/celestezj/blog-bootstrap/internal/state"
	"github.com/celestezj/blog-bootstrap/internal/toolchain"
	"github.com/celestezj/blog-bootstrap/internal/workspace"
)

// nodeRuntime is a resolved node + npm pair with a constructed PATH.
type nodeRuntime struct {
	Node *toolchain.Tool
	Npm  *toolchain.Tool
	Env  []string
}

// NodeStep provisions Node.js with system-first installation and a portable
// fallback, reconciling the repository's .node-version after clone.
type NodeStep struct {
	Ctx       *core.Context
	Installer *toolchain.Installer
	Portable  *toolchain.Portable
}

func (s *NodeStep) ID() string     { return "node" }
func (s *NodeStep) Required() bool { return true }

// DesiredVersion prefers the cloned repository's .node-version, then config.
func (s *NodeStep) DesiredVersion() string {
	if s.Ctx.Config == nil {
		return ""
	}
	p := filepath.Join(s.Ctx.Config.Dir, ".node-version")
	if v, err := toolchain.ReadNodeVersion(p); err == nil && strings.TrimSpace(v) != "" {
		return v
	}
	return s.Ctx.Config.NodeWant
}

func (s *NodeStep) spec() (toolchain.PortableSpec, error) {
	return toolchain.NodeSpec(s.Ctx.GOOS, s.Ctx.GOARCH, s.DesiredVersion(), s.Ctx.ToolsDir())
}

func (s *NodeStep) nodeCandidates() []string {
	switch s.Ctx.GOOS {
	case "windows":
		var out []string
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
			if base := os.Getenv(env); base != "" {
				candidates := []string{filepath.Join(base, "nodejs", "node.exe")}
				if env == "LocalAppData" {
					candidates = []string{filepath.Join(base, "Programs", "nodejs", "node.exe")}
				}
				out = append(out, candidates...)
			}
		}
		return out
	case "darwin":
		return []string{"/opt/homebrew/bin/node", "/usr/local/bin/node"}
	default:
		return []string{"/usr/bin/node", "/usr/local/bin/node"}
	}
}

func (s *NodeStep) detect(ctx context.Context) (*toolchain.Tool, *toolchain.Tool, error) {
	snap := s.Ctx.State.Snapshot()
	node, err := toolchain.DetectNode(ctx, s.Ctx.Runner, snap.Paths.Node)
	if err != nil {
		return nil, nil, err
	}
	if node == nil {
		for _, candidate := range s.nodeCandidates() {
			if !fileExists(candidate) {
				continue
			}
			t, err := toolchain.DetectNode(ctx, s.Ctx.Runner, candidate)
			if err != nil {
				return nil, nil, err
			}
			if t != nil {
				node = t
				break
			}
		}
	}
	npm, err := toolchain.DetectNpm(ctx, s.Ctx.Runner, node, s.Ctx.GOOS)
	if err != nil {
		return node, nil, err
	}
	return node, npm, nil
}

func (s *NodeStep) runtimeFor(node, npm *toolchain.Tool) *nodeRuntime {
	if node == nil || npm == nil {
		return nil
	}
	dirs := []string{filepath.Dir(node.Path), filepath.Dir(npm.Path)}
	snap := s.Ctx.State.Snapshot()
	if snap.Paths.Git != "" {
		dirs = append(dirs, filepath.Dir(snap.Paths.Git))
	} else if gitPath, err := s.Ctx.Runner.LookPath("git"); err == nil && gitPath != "" {
		dirs = append(dirs, filepath.Dir(gitPath))
	}
	return &nodeRuntime{Node: node, Npm: npm, Env: toolchain.EnvironWith(dirs...)}
}

// current returns the usable runtime only when it satisfies the desired
// version; otherwise (nil, false, nil).
func (s *NodeStep) current(ctx context.Context) (*nodeRuntime, bool, error) {
	want := s.DesiredVersion()
	node, npm, err := s.detect(ctx)
	if err != nil {
		return nil, false, err
	}
	if node != nil && npm != nil && toolchain.NodeSupported(node.Version) && toolchain.MatchWant(want, node.Version) {
		return s.runtimeFor(node, npm), true, nil
	}
	if spec, err := s.spec(); err == nil && s.Portable != nil {
		if tool := s.Portable.Lookup(spec); tool != nil {
			npm, npmErr := toolchain.DetectNpm(ctx, s.Ctx.Runner, tool, s.Ctx.GOOS)
			if npmErr == nil && npm != nil && toolchain.MatchWant(want, tool.Version) {
				return s.runtimeFor(tool, npm), true, nil
			}
		}
	}
	return nil, false, nil
}

func (s *NodeStep) Inspect(ctx context.Context) (run.Inspection, error) {
	want := s.DesiredVersion()
	evidence := map[string]string{"node_want": want}
	node, npm, err := s.detect(ctx)
	if err != nil {
		return run.Inspection{}, err
	}
	if node != nil {
		evidence["node_version"] = node.Version.String()
		evidence["node_path"] = node.Path
	}
	if npm != nil {
		evidence["npm_version"] = npm.Version.String()
	}
	if _, ok, err := s.current(ctx); err != nil {
		return run.Inspection{}, err
	} else if ok {
		return run.Inspection{Satisfied: true, Evidence: evidence}, nil
	}

	actions := []plan.Action{}
	warning := ""
	mismatch := node != nil && (!toolchain.NodeSupported(node.Version) || !toolchain.MatchWant(want, node.Version))
	if mismatch {
		warning = fmt.Sprintf("系统 Node %s 不满足 %s，将使用项目内便携版本", node.Version.String(), want)
	}
	if s.Ctx.Config.Strategy == "isolated" {
		actions = append(actions, s.portableAction("isolated 策略"))
	} else if node == nil {
		if manager, ok := s.Installer.Manager(ctx); ok {
			actions = append(actions, plan.Action{
				Kind:   "install_package",
				Target: manager + " nodejs",
				Detail: "通过系统包管理器安装 Node.js；无权限时回退便携版本",
			})
		}
		actions = append(actions, s.portableAction("系统包管理器不可用或安装失败"))
	} else if mismatch || npm == nil {
		actions = append(actions, s.portableAction("系统 Node 版本不匹配"))
	}
	if _, err := os.Stat(filepath.Join(s.Ctx.Config.Dir, ".node-version")); err != nil {
		actions = append(actions, plan.Action{
			Kind:   "reconcile",
			Target: filepath.Join(s.Ctx.Config.Dir, ".node-version"),
			Detail: "clone 后按仓库 .node-version 对账，必要时切换项目内便携 Node",
		})
	}
	return run.Inspection{Actions: actions, Evidence: evidence, Warning: warning}, nil
}

func (s *NodeStep) portableAction(reason string) plan.Action {
	spec, err := s.spec()
	if err != nil {
		return plan.Action{Kind: "download", Target: "node", Detail: reason}
	}
	return plan.Action{
		Kind:   "download",
		Target: spec.URL,
		Detail: fmt.Sprintf("便携 Node.js %s → %s（%s）", spec.Version, spec.InstallDir, reason),
	}
}

func (s *NodeStep) Apply(ctx context.Context) error {
	if rt, ok, err := s.current(ctx); err != nil {
		return err
	} else if ok {
		s.Ctx.Sayf("Node.js 已就绪: %s (%s)", rt.Node.Version.String(), rt.Node.Path)
		return s.storePaths(rt)
	}
	want := s.DesiredVersion()
	systemMismatch := false
	var installErr error
	if s.Ctx.Config.Strategy != "isolated" {
		node, npm, err := s.detect(ctx)
		if err != nil {
			return err
		}
		systemMismatch = node != nil && (!toolchain.NodeSupported(node.Version) || !toolchain.MatchWant(want, node.Version))
		if node == nil || systemMismatch || npm == nil {
			if node == nil {
				if manager, ok := s.Installer.Manager(ctx); ok {
					s.Ctx.Sayf("未找到可用的 Node.js，正在通过 %s 安装（可能需要管理员权限，耗时几分钟，请勿关闭窗口）...", manager)
					if err := s.Installer.Install(ctx, manager, toolchain.Node); err == nil {
						s.Ctx.Sayf("系统 Node.js 安装完成")
						_ = workspace.RecordChange(s.Ctx, func(c *state.Changes) {
							c.SystemPackages = append(c.SystemPackages, state.PackageChange{Manager: manager, Package: "nodejs", Action: "install"})
						})
					} else {
						installErr = err
					}
				}
			}
			node, npm, err = s.detect(ctx)
			if err != nil {
				return err
			}
			if node != nil && npm != nil && toolchain.NodeSupported(node.Version) && toolchain.MatchWant(want, node.Version) {
				s.Ctx.Sayf("Node.js 就绪: %s (%s)", node.Version.String(), node.Path)
				return s.storePaths(s.runtimeFor(node, npm))
			}
		} else {
			s.Ctx.Sayf("Node.js 就绪: %s (%s)", node.Version.String(), node.Path)
			return s.storePaths(s.runtimeFor(node, npm))
		}
	}
	if err := s.installPortable(ctx); err != nil {
		if installErr != nil && protocol.As(err) == nil {
			return humanToolError("Node.js", installErr)
		}
		return err
	}
	return nil
}

func (s *NodeStep) installPortable(ctx context.Context) error {
	spec, err := s.spec()
	if err != nil {
		return protocol.Fatal("NODE_VERSION_UNSUPPORTED", err.Error()).WithCause(err)
	}
	s.Ctx.Sayf("正在下载便携版 Node.js %s...", spec.Version)
	tool, err := s.Portable.Ensure(ctx, spec)
	if err != nil {
		return err
	}
	npm, err := toolchain.DetectNpm(ctx, s.Ctx.Runner, tool, s.Ctx.GOOS)
	if err != nil {
		return err
	}
	rt := s.runtimeFor(tool, npm)
	if rt == nil {
		return protocol.Fatal("NPM_MISSING", "便携 Node.js 未包含可用的 npm")
	}
	if err := s.storePaths(rt); err != nil {
		return err
	}
	s.Ctx.Sayf("便携版 Node.js 就绪: %s (%s)", tool.Version.String(), tool.Path)
	return workspace.RecordChange(s.Ctx, func(c *state.Changes) {
		for _, existing := range c.PortableTools {
			if existing.Name == "node" && existing.Path == tool.Path {
				return
			}
		}
		c.PortableTools = append(c.PortableTools, state.ToolChange{Name: "node", Version: tool.Version.String(), Path: tool.Path})
	})
}

func (s *NodeStep) storePaths(rt *nodeRuntime) error {
	if rt == nil {
		return protocol.Fatal("TOOLS_MISSING", "无法解析 Node.js 运行时")
	}
	return s.Ctx.State.SetPaths(func(p *state.Paths) {
		p.Node = rt.Node.Path
		p.Npm = rt.Npm.Path
	})
}

// EnsureRuntime installs or selects a runtime matching want. Used by later
// steps after clone discovers .node-version.
func (s *NodeStep) EnsureRuntime(ctx context.Context, want string) (*nodeRuntime, error) {
	if strings.TrimSpace(want) != "" {
		if want != s.DesiredVersion() {
			// A different version than the workspace's file: prefer explicit want.
			node, npm, err := s.detect(ctx)
			if err != nil {
				return nil, err
			}
			if node != nil && npm != nil && toolchain.MatchWant(want, node.Version) && toolchain.NodeSupported(node.Version) {
				rt := s.runtimeFor(node, npm)
				if err := s.storePaths(rt); err != nil {
					return nil, err
				}
				return rt, nil
			}
			return s.installPortableVersion(ctx, want)
		}
	}
	if rt, ok, err := s.current(ctx); err != nil {
		return nil, err
	} else if ok {
		if err := s.storePaths(rt); err != nil {
			return nil, err
		}
		return rt, nil
	}
	return s.installPortableVersion(ctx, s.DesiredVersion())
}

func (s *NodeStep) installPortableVersion(ctx context.Context, want string) (*nodeRuntime, error) {
	spec, err := toolchain.NodeSpec(s.Ctx.GOOS, s.Ctx.GOARCH, want, s.Ctx.ToolsDir())
	if err != nil {
		return nil, protocol.Fatal("NODE_VERSION_UNSUPPORTED", err.Error()).WithCause(err)
	}
	s.Ctx.Sayf("仓库要求 Node.js %s，正在准备便携版本...", spec.Version)
	tool, err := s.Portable.Ensure(ctx, spec)
	if err != nil {
		return nil, err
	}
	npm, err := toolchain.DetectNpm(ctx, s.Ctx.Runner, tool, s.Ctx.GOOS)
	if err != nil {
		return nil, err
	}
	rt := s.runtimeFor(tool, npm)
	if rt == nil {
		return nil, protocol.Fatal("NPM_MISSING", "便携 Node.js 未包含可用的 npm")
	}
	if err := s.storePaths(rt); err != nil {
		return nil, err
	}
	s.Ctx.Sayf("便携版 Node.js 就绪: %s (%s)", tool.Version.String(), tool.Path)
	if err := workspace.RecordChange(s.Ctx, func(c *state.Changes) {
		for _, existing := range c.PortableTools {
			if existing.Name == "node" && existing.Path == tool.Path {
				return
			}
		}
		c.PortableTools = append(c.PortableTools, state.ToolChange{Name: "node", Version: tool.Version.String(), Path: tool.Path})
	}); err != nil {
		return nil, err
	}
	return rt, nil
}

func (s *NodeStep) Verify(ctx context.Context) (map[string]string, error) {
	want := s.DesiredVersion()
	rt, ok, err := s.current(ctx)
	if err != nil {
		return nil, asVerify(err)
	}
	if !ok {
		node, npm, detectErr := s.detect(ctx)
		if detectErr != nil {
			return nil, asVerify(detectErr)
		}
		switch {
		case node == nil:
			return nil, protocol.Verify("TOOLS_MISSING", "找不到 node，请先完成 node 步骤")
		case !toolchain.NodeSupported(node.Version):
			return nil, protocol.Verify("VERSION_MISMATCH", "Node.js 版本过低: "+node.Version.String())
		case !toolchain.MatchWant(want, node.Version):
			return nil, protocol.Verify("VERSION_MISMATCH",
				fmt.Sprintf("当前 Node.js %s 不满足仓库要求的 %s", node.Version.String(), want))
		case npm == nil:
			return nil, protocol.Verify("NPM_MISSING", "找到 node 但无法执行 npm")
		default:
			return nil, protocol.Verify("VERSION_MISMATCH", "Node.js 运行时未满足要求")
		}
	}
	if err := s.storePaths(rt); err != nil {
		return nil, err
	}
	return map[string]string{
		"node_version": rt.Node.Version.String(),
		"node_path":    rt.Node.Path,
		"npm_version":  rt.Npm.Version.String(),
		"node_source":  rt.Node.Source,
		"node_want":    want,
	}, nil
}
