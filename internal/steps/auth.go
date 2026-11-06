package steps

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/celestezj/blog-bootstrap/internal/core"
	"github.com/celestezj/blog-bootstrap/internal/plan"
	"github.com/celestezj/blog-bootstrap/internal/protocol"
	"github.com/celestezj/blog-bootstrap/internal/run"
	"github.com/celestezj/blog-bootstrap/internal/sshauth"
	"github.com/celestezj/blog-bootstrap/internal/state"
	"github.com/celestezj/blog-bootstrap/internal/toolchain"
	"github.com/celestezj/blog-bootstrap/internal/workspace"
)

// AuthStep installs git/OpenSSH, manages the dedicated key and Host alias, and
// proves read/write access to the source and deploy repositories.
type AuthStep struct {
	Ctx       *core.Context
	Installer *toolchain.Installer
	Portable  *toolchain.Portable
}

func (s *AuthStep) ID() string     { return "auth.ssh" }
func (s *AuthStep) Required() bool { return true }

func (s *AuthStep) detect(ctx context.Context) (*toolchain.Tool, *toolchain.Tool, error) {
	snap := s.Ctx.State.Snapshot()
	git, err := toolchain.DetectGit(ctx, s.Ctx.Runner, snap.Paths.Git)
	if err != nil {
		return nil, nil, err
	}
	ssh, err := toolchain.DetectSSH(ctx, s.Ctx.Runner, snap.Paths.SSH)
	if err != nil {
		return git, nil, err
	}
	return git, ssh, nil
}

func (s *AuthStep) gitCandidates() []string {
	switch s.Ctx.GOOS {
	case "windows":
		var out []string
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
			if base := os.Getenv(env); base != "" {
				if env == "LocalAppData" {
					out = append(out, filepath.Join(base, "Programs", "Git", "cmd", "git.exe"))
				} else {
					out = append(out, filepath.Join(base, "Git", "cmd", "git.exe"))
				}
			}
		}
		return out
	case "darwin":
		return []string{"/opt/homebrew/bin/git", "/usr/local/bin/git"}
	default:
		return []string{"/usr/bin/git", "/usr/local/bin/git"}
	}
}

func (s *AuthStep) detectGitWithCandidates(ctx context.Context) (*toolchain.Tool, error) {
	git, err := toolchain.DetectGit(ctx, s.Ctx.Runner, s.Ctx.State.Snapshot().Paths.Git)
	if err != nil || git != nil {
		return git, err
	}
	for _, candidate := range s.gitCandidates() {
		if !fileExists(candidate) {
			continue
		}
		t, err := toolchain.DetectGit(ctx, s.Ctx.Runner, candidate)
		if err != nil {
			return nil, err
		}
		if t != nil {
			return t, nil
		}
	}
	return nil, nil
}

func (s *AuthStep) sshKeygen(ctx context.Context) string {
	if p, err := s.Ctx.Runner.LookPath("ssh-keygen"); err == nil {
		return p
	}
	return "ssh-keygen"
}

func (s *AuthStep) manager(ctx context.Context, git, ssh *toolchain.Tool) *sshauth.Manager {
	m := &sshauth.Manager{
		Runner:       s.Ctx.Runner,
		SSHKeygen:    s.sshKeygen(ctx),
		Owner:        s.Ctx.Config.Owner,
		Alias:        effectiveAlias(s.Ctx),
		KeyPath:      s.Ctx.Config.KeyPath,
		Home:         s.Ctx.Home,
		IsTTY:        s.Ctx.IsTTY,
		Now:          s.Ctx.Now,
		Sleep:        s.Ctx.Sleep,
		PollInterval: 5 * time.Second,
		PollTimeout:  180 * time.Second,
	}
	if git != nil {
		m.GitPath = git.Path
	}
	if ssh != nil {
		m.SSHPath = ssh.Path
	}
	return m
}

func (s *AuthStep) ensureGit(ctx context.Context) (*toolchain.Tool, *toolchain.Tool, error) {
	git, err := s.detectGitWithCandidates(ctx)
	if err != nil {
		return nil, nil, err
	}
	if git == nil || !toolchain.GitSupported(git.Version) {
		var installErr error
		if manager, ok := s.Installer.Manager(ctx); ok {
			s.Ctx.Sayf("未找到可用的 git，正在通过 %s 安装（可能需要管理员权限，耗时几分钟，请勿关闭窗口）...", manager)
			if err := s.Installer.Install(ctx, manager, toolchain.Git); err == nil {
				s.Ctx.Sayf("系统 git 安装完成")
				_ = workspace.RecordChange(s.Ctx, func(c *state.Changes) {
					c.SystemPackages = append(c.SystemPackages, state.PackageChange{Manager: manager, Package: "git", Action: "install"})
				})
			} else {
				installErr = err
			}
		}
		git, err = s.detectGitWithCandidates(ctx)
		if err != nil {
			return nil, nil, err
		}
		if git == nil || !toolchain.GitSupported(git.Version) {
			spec, specErr := toolchain.GitSpec(s.Ctx.GOOS, s.Ctx.GOARCH, s.Ctx.ToolsDir())
			if specErr != nil {
				return nil, nil, humanToolError("git", installErr)
			}
			s.Ctx.Sayf("系统安装不可用，正在下载便携版 git %s...", spec.Version)
			tool, perr := s.Portable.Ensure(ctx, spec)
			if perr != nil {
				return nil, nil, perr
			}
			s.Ctx.Sayf("便携版 git 就绪: %s", tool.Path)
			if recErr := workspace.RecordChange(s.Ctx, func(c *state.Changes) {
				c.PortableTools = append(c.PortableTools, state.ToolChange{Name: "git", Version: tool.Version.String(), Path: tool.Path})
			}); recErr != nil {
				return nil, nil, recErr
			}
			git = tool
		}
	}
	if git != nil {
		s.Ctx.Sayf("git 就绪: %s (%s)", git.Version.String(), git.Path)
	}

	ssh, err := toolchain.DetectSSH(ctx, s.Ctx.Runner, s.Ctx.State.Snapshot().Paths.SSH)
	if err != nil {
		return nil, nil, err
	}
	if ssh == nil && git.Extra != nil {
		if p := git.Extra["ssh"]; p != "" {
			ssh = &toolchain.Tool{Name: "ssh", Path: p, Source: git.Source}
		}
	}
	if ssh == nil {
		return nil, nil, protocol.Human("TOOLS_MISSING",
			"找不到 OpenSSH 客户端；请安装后重试（macOS/Linux 通常自带，Windows 可在“可选功能”中安装 OpenSSH Client）",
			&protocol.NextAction{Type: "human", Code: "TOOLS_MISSING", Message: "安装 OpenSSH 客户端后重新运行"})
	}
	_ = s.Ctx.State.SetPaths(func(p *state.Paths) { p.Git = git.Path; p.SSH = ssh.Path })
	return git, ssh, nil
}

func (s *AuthStep) fingerprint(ctx context.Context, key sshauth.Key) string {
	fp, err := sshauth.Fingerprint(ctx, s.Ctx.Runner, s.sshKeygen(ctx), key)
	if err != nil {
		return ""
	}
	return fp
}

func (s *AuthStep) Inspect(ctx context.Context) (run.Inspection, error) {
	evidence := map[string]string{}
	actions := []plan.Action{}
	git, ssh, err := s.detect(ctx)
	if err != nil {
		return run.Inspection{}, err
	}
	if git != nil {
		evidence["git_version"] = git.Version.String()
	}
	gitReady := git != nil && toolchain.GitSupported(git.Version)
	if !gitReady {
		if manager, ok := s.Installer.Manager(ctx); ok {
			actions = append(actions, plan.Action{Kind: "install_package", Target: manager + " git", Detail: "安装/升级 git"})
		}
		if s.Ctx.GOOS == "windows" {
			actions = append(actions, plan.Action{Kind: "download", Target: "MinGit", Detail: "系统安装失败时使用便携 git"})
		}
	}
	if ssh == nil {
		actions = append(actions, plan.Action{Kind: "install_package", Target: "openssh-client", Detail: "安装 OpenSSH 客户端"})
		return run.Inspection{Actions: actions, Evidence: evidence}, nil
	}
	evidence["ssh_version"] = ssh.Version.String()

	mgr := s.manager(ctx, git, ssh)
	if knownHosts, cleanup, khErr := sshauth.TempKnownHosts(); khErr == nil {
		mgr.KnownHosts = knownHosts
		defer cleanup()
	}
	s.Ctx.Sayf("  正在探测 GitHub SSH 端点...")
	endpoint, err := mgr.ChooseEndpoint(ctx)
	if err != nil {
		return run.Inspection{}, err
	}
	evidence["endpoint"] = endpoint.Label()

	keys, err := sshauth.DiscoverKeys(mgr.SSHDir(), s.Ctx.Config.KeyPath)
	if err != nil {
		return run.Inspection{}, protocol.Fatal("AUTH_KEY_READ_FAILED", "无法读取 SSH 密钥目录: "+err.Error()).WithCause(err)
	}
	s.Ctx.Sayf("  正在检查可复用的 SSH 密钥...")
	key, wrong, err := mgr.FindReusableKey(ctx, keys, endpoint)
	if err != nil {
		return run.Inspection{}, err
	}
	warning := ""
	if len(wrong) > 0 {
		warning = "发现属于其他账号的 SSH key，已忽略: " + strings.Join(wrong, ", ")
	}
	block, err := sshauth.PreviewHostBlock(configPath(s.Ctx), s.Ctx.Config.Alias, endpoint.Host, endpoint.Port, s.Ctx.Config.KeyPath)
	if err != nil {
		return run.Inspection{}, err
	}
	evidence["alias"] = block.Alias

	if block.Changed {
		actions = append(actions, plan.Action{
			Kind:   "write_file",
			Target: configPath(s.Ctx),
			Detail: "写入/更新受管 Host " + block.Alias + " 配置块（写入前备份）",
		})
	}
	if key == nil {
		if fileExists(s.Ctx.Config.KeyPath) {
			k, readErr := sshauth.ReadKey(s.Ctx.Config.KeyPath)
			if readErr == nil {
				actions = append(actions, plan.Action{
					Kind:   "open_browser",
					Target: "https://github.com/settings/ssh/new",
					Detail: "在浏览器登记公钥 " + s.Ctx.Config.KeyPath + ".pub",
				})
				evidence["fingerprint"] = s.fingerprint(ctx, k)
			}
		} else {
			actions = append(actions, plan.Action{
				Kind:   "create_key",
				Target: s.Ctx.Config.KeyPath,
				Detail: "生成无口令 ed25519 key（权限 0600）并打开 GitHub 登记页",
			})
		}
	} else {
		evidence["fingerprint"] = s.fingerprint(ctx, *key)
		evidence["greeting"] = "Hi " + s.Ctx.Config.Owner + "!"
	}
	satisfied := gitReady && key != nil && !block.Changed
	return run.Inspection{Satisfied: satisfied, Actions: actions, Evidence: evidence, Warning: warning}, nil
}

func (s *AuthStep) Apply(ctx context.Context) error {
	git, ssh, err := s.ensureGit(ctx)
	if err != nil {
		return err
	}
	mgr := s.manager(ctx, git, ssh)
	s.Ctx.Sayf("正在探测 GitHub SSH 端点（优先 github.com:22，不可达时回退 ssh.github.com:443）...")
	endpoint, err := mgr.ChooseEndpoint(ctx)
	if err != nil {
		return err
	}
	s.Ctx.Sayf("SSH 端点可用: %s", endpoint.Label())
	block, err := sshauth.EnsureHostBlock(configPath(s.Ctx), s.Ctx.Config.Alias, endpoint.Host, endpoint.Port, s.Ctx.Config.KeyPath, s.Ctx.NowTime())
	if err != nil {
		return err
	}
	mgr.Alias = block.Alias
	if block.Changed {
		s.Ctx.Sayf("正在写入 SSH 配置块 %q 到 %s（写入前会备份原文件）...", block.Alias, block.Path)
		if err := workspace.RecordChange(s.Ctx, func(c *state.Changes) {
			c.SSHBlocks = append(c.SSHBlocks, state.SSHChange{File: block.Path, Alias: block.Alias, Block: block.Block})
			if block.Backup != "" {
				c.Backups = append(c.Backups, block.Backup)
			}
		}); err != nil {
			return err
		}
	}
	_ = s.Ctx.State.SetEndpoint(endpoint.Label())
	_ = s.Ctx.State.SetEffectiveAlias(block.Alias)
	_ = s.Ctx.State.SetPaths(func(p *state.Paths) { p.Key = s.Ctx.Config.KeyPath })

	var key sshauth.Key
	if fileExists(s.Ctx.Config.KeyPath) {
		s.Ctx.Sayf("使用已有 SSH 私钥: %s", s.Ctx.Config.KeyPath)
		key, err = sshauth.ReadKey(s.Ctx.Config.KeyPath)
		if err != nil {
			return protocol.Fatal("AUTH_KEY_READ_FAILED", "无法读取已配置的 key: "+err.Error()).WithCause(err)
		}
	} else {
		s.Ctx.Sayf("生成新的 ed25519 SSH 密钥对: %s", s.Ctx.Config.KeyPath)
		comment := "blog-bootstrap " + s.Ctx.Config.Owner
		k, genErr := sshauth.GenerateKey(ctx, s.Ctx.Runner, s.sshKeygen(ctx), s.Ctx.Config.KeyPath, comment)
		if genErr != nil {
			return genErr
		}
		key = *k
		s.Ctx.Sayf("密钥已生成（私钥权限 0600，无口令）")
		_ = s.Ctx.State.SetPaths(func(p *state.Paths) { p.Key = key.Path })
	}

	s.Ctx.Sayf("正在用该密钥连接 GitHub，确认它属于账号 %s...", s.Ctx.Config.Owner)
	out, err := mgr.Greeting(ctx, key, endpoint)
	if err != nil {
		return err
	}
	switch state, account := sshauth.EvaluateGreeting(out, s.Ctx.Config.Owner); state {
	case sshauth.GreetingMatch:
		s.Ctx.Sayf("SSH 身份验证通过：Hi %s!", s.Ctx.Config.Owner)
		return nil
	case sshauth.GreetingWrong:
		return s.wrongAccount(account)
	}

	publicKey, err := sshauth.PublicKeyFor(ctx, s.Ctx.Runner, s.sshKeygen(ctx), key)
	if err != nil {
		return err
	}
	fp := s.fingerprint(ctx, key)
	url, err := sshauth.OpenEnrollment(s.Ctx.Browser, s.Ctx.Config.Owner, publicKey)
	if err != nil {
		return err
	}

	s.Ctx.Sayf("")
	s.Ctx.Sayf("需要你在浏览器完成一步操作：")
	s.Ctx.Sayf("  1. 确认浏览器中登录的是 GitHub 账号 %s", s.Ctx.Config.Owner)
	s.Ctx.Sayf("  2. 在打开的 “Add new SSH key” 页面点击 “Add SSH key”")
	s.Ctx.Sayf("  3. 若页面没有自动填入公钥，请手动粘贴下面这一整行：")
	s.Ctx.Sayf("")
	s.Ctx.Sayf("     %s", publicKey)
	if fp != "" {
		s.Ctx.Sayf("")
		s.Ctx.Sayf("  公钥指纹（可与页面核对）：%s", fp)
	}
	s.Ctx.Sayf("")
	s.Ctx.Sayf("已尝试自动打开登记页面；若浏览器没有打开，请手动访问：")
	s.Ctx.Sayf("  %s", url)

	humanErr := func(message string) error {
		return protocol.Human("AUTH_KEY_NOT_ADDED", message, &protocol.NextAction{
			Type:     "human",
			Code:     "AUTH_KEY_NOT_ADDED",
			OpenURL:  url,
			Message:  message,
			Expected: "Hi " + s.Ctx.Config.Owner + "!",
			Poll: &protocol.Poll{
				Command:         "blog-bootstrap verify --step auth.ssh --json",
				IntervalSeconds: 5,
				TimeoutSeconds:  180,
			},
		}).WithDetails(map[string]string{
			"fingerprint": fp,
			"add_key_url": url,
		})
	}
	if !s.Ctx.IsTTY {
		msg := "请在浏览器中确认已登录 " + s.Ctx.Config.Owner + " 并点击 Add SSH key，然后重新生成计划并执行"
		s.Ctx.Sayf("")
		s.Ctx.Sayf("当前不是交互式终端，程序先在这里停下。完成上述操作后请依次执行：")
		s.Ctx.Sayf("  1. blog-bootstrap plan（获取新的 consent hash）")
		s.Ctx.Sayf("  2. blog-bootstrap run --consent <新 hash>")
		s.Ctx.Sayf("已完成的步骤会自动跳过。")
		return humanErr(msg)
	}

	mgr.Notify = func(line string) { s.Ctx.Sayf("  %s", line) }
	greeting, account, err := mgr.PollEnrollment(ctx, key, endpoint)
	if err != nil {
		return err
	}
	switch greeting {
	case sshauth.GreetingMatch:
		s.Ctx.Sayf("SSH 身份验证通过：Hi %s!", s.Ctx.Config.Owner)
		return nil
	case sshauth.GreetingWrong:
		return s.wrongAccount(account)
	default:
		return humanErr("公钥尚未在 GitHub 生效，请确认已登录 " + s.Ctx.Config.Owner + " 并点击 Add SSH key 后重新执行本命令")
	}
}

func (s *AuthStep) wrongAccount(account string) error {
	msg := fmt.Sprintf("SSH key 属于账号 %s，不是 %s；请从错误账号删除该 key，或在配置中指定新的 auth.key", account, s.Ctx.Config.Owner)
	return protocol.Human("AUTH_WRONG_ACCOUNT", msg, &protocol.NextAction{
		Type:     "human",
		Code:     "AUTH_WRONG_ACCOUNT",
		OpenURL:  "https://github.com/settings/keys",
		Message:  msg,
		Expected: "Hi " + s.Ctx.Config.Owner + "!",
	})
}

func (s *AuthStep) Verify(ctx context.Context) (map[string]string, error) {
	git, ssh, err := s.detect(ctx)
	if err != nil {
		return nil, asVerify(err)
	}
	if git == nil || !toolchain.GitSupported(git.Version) {
		return nil, protocol.Verify("TOOLS_MISSING", "git 缺失或版本过低")
	}
	if ssh == nil {
		return nil, protocol.Verify("TOOLS_MISSING", "OpenSSH 客户端缺失")
	}
	if !fileExists(s.Ctx.Config.KeyPath) {
		return nil, protocol.Verify("AUTH_KEY_NOT_ADDED", "找不到配置的私钥: "+s.Ctx.Config.KeyPath)
	}
	key, err := sshauth.ReadKey(s.Ctx.Config.KeyPath)
	if err != nil {
		return nil, protocol.Verify("AUTH_KEY_READ_FAILED", "无法读取公钥: "+err.Error())
	}
	mgr := s.manager(ctx, git, ssh)

	host, port := endpointParts(s.Ctx.State.Snapshot().Endpoint)
	endpoint, err := mgr.ChooseEndpointFrom(ctx, sshauth.Endpoint{Host: host, Port: port})
	if err != nil {
		return nil, asVerify(err)
	}
	block, err := sshauth.PreviewHostBlock(configPath(s.Ctx), s.Ctx.Config.Alias, endpoint.Host, endpoint.Port, s.Ctx.Config.KeyPath)
	if err != nil {
		return nil, asVerify(err)
	}
	if block.Changed {
		return nil, protocol.Verify("AUTH_CONFIG_OUTDATED", "SSH 配置块缺失或与当前设置不一致")
	}
	mgr.Alias = block.Alias

	s.Ctx.Sayf("  正在用 SSH 验证 GitHub 身份...")
	out, err := mgr.Greeting(ctx, key, endpoint)
	if err != nil {
		return nil, asVerify(err)
	}
	switch state, account := sshauth.EvaluateGreeting(out, s.Ctx.Config.Owner); state {
	case sshauth.GreetingNone:
		return nil, protocol.Verify("AUTH_KEY_NOT_ADDED", "公钥尚未在 GitHub 生效")
	case sshauth.GreetingWrong:
		return nil, protocol.Verify("AUTH_WRONG_ACCOUNT", "SSH key 属于账号 "+account)
	}

	sourceURL := s.Ctx.Config.Source.WithAlias(block.Alias)
	deployURL := s.Ctx.Config.Deploy.WithAlias(block.Alias)
	s.Ctx.Sayf("  正在验证源仓库读权限...")
	if err := mgr.ProbeRead(ctx, sourceURL); err != nil {
		return nil, asVerify(err)
	}
	s.Ctx.Sayf("  正在验证部署仓库写权限（git push --dry-run）...")
	if err := mgr.ProbeWrite(ctx, deployURL, s.Ctx.Config.Branch, s.Ctx.Config.GitIdentity.Name, s.Ctx.Config.GitIdentity.Email); err != nil {
		return nil, asVerify(err)
	}

	_ = s.Ctx.State.SetPaths(func(p *state.Paths) {
		p.Git = git.Path
		p.SSH = ssh.Path
		p.Key = key.Path
	})
	_ = s.Ctx.State.SetEndpoint(endpoint.Label())
	_ = s.Ctx.State.SetEffectiveAlias(block.Alias)

	evidence := map[string]string{
		"git_version":  git.Version.String(),
		"ssh_version":  ssh.Version.String(),
		"greeting":     "Hi " + s.Ctx.Config.Owner + "!",
		"endpoint":     endpoint.Label(),
		"alias":        block.Alias,
		"source_read":  "ok",
		"deploy_write": "ok",
	}
	if fp := s.fingerprint(ctx, key); fp != "" {
		evidence["fingerprint"] = fp
	}
	return evidence, nil
}
