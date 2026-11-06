package sshauth

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/celestezj/blog-bootstrap/internal/execx"
	"github.com/celestezj/blog-bootstrap/internal/netx"
	"github.com/celestezj/blog-bootstrap/internal/protocol"
)

// GreetingState classifies the GitHub SSH greeting.
type GreetingState string

const (
	GreetingNone  GreetingState = "none"
	GreetingMatch GreetingState = "match"
	GreetingWrong GreetingState = "wrong_account"
)

var greetingRE = regexp.MustCompile(`(?i)\bHi\s+([A-Za-z0-9-]+)\s*!`)

// EvaluateGreeting compares "Hi <account>!" with the target owner. The SSH exit
// code is deliberately ignored: GitHub exits 1 even on successful auth.
func EvaluateGreeting(output, owner string) (GreetingState, string) {
	m := greetingRE.FindStringSubmatch(output)
	if m == nil {
		return GreetingNone, ""
	}
	account := m[1]
	if strings.EqualFold(account, owner) {
		return GreetingMatch, account
	}
	return GreetingWrong, account
}

// Endpoint is one SSH network endpoint.
type Endpoint struct {
	Host string
	Port int
}

// Label renders the endpoint for logs and state.
func (e Endpoint) Label() string {
	return e.Host + ":" + strconv.Itoa(e.Port)
}

// Endpoints are tried in order.
var Endpoints = []Endpoint{
	{Host: "github.com", Port: 22},
	{Host: "ssh.github.com", Port: 443},
}

// Manager performs SSH probes with an injectable runner and clock.
type Manager struct {
	Runner       execx.Runner
	SSHPath      string
	SSHKeygen    string
	GitPath      string
	Owner        string
	Alias        string
	KeyPath      string
	Home         string
	IsTTY        bool
	Now          func() time.Time
	Sleep        func(time.Duration)
	PollInterval time.Duration
	PollTimeout  time.Duration

	// KnownHosts overrides UserKnownHostsFile for read-only probes so that
	// inspection never mutates the user's real known_hosts before consent.
	KnownHosts string

	// Notify receives human-facing progress while waiting for enrollment.
	Notify func(string)
}

func (m *Manager) notify(format string, args ...interface{}) {
	if m.Notify != nil {
		m.Notify(fmt.Sprintf(format, args...))
	}
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) wait(d time.Duration) {
	if m.Sleep != nil {
		m.Sleep(d)
		return
	}
	time.Sleep(d)
}

func (m *Manager) ssh() (string, error) {
	if m.SSHPath != "" {
		return m.SSHPath, nil
	}
	p, err := m.Runner.LookPath("ssh")
	if err != nil {
		return "", protocol.Fatal("TOOLS_MISSING", "找不到 ssh，请先安装 OpenSSH").WithCause(err)
	}
	return p, nil
}

// SSHConfigPath returns ~/.ssh/config.
func (m *Manager) SSHConfigPath() string {
	return filepath.Join(m.Home, ".ssh", "config")
}

// SSHDir returns ~/.ssh.
func (m *Manager) SSHDir() string { return filepath.Join(m.Home, ".ssh") }

// Greeting runs ssh -T with one key and returns combined output.
func (m *Manager) Greeting(ctx context.Context, key Key, ep Endpoint) (string, error) {
	sshPath, err := m.ssh()
	if err != nil {
		return "", err
	}
	args := SSHArgsWithKnownHosts(ep, key.Path, m.KnownHosts)
	res := m.Runner.Run(ctx, "", nil, sshPath, args...)
	if res.Err != nil {
		return "", protocol.Fatal("AUTH_PROBE_FAILED", "ssh 探针执行失败: "+res.Err.Error()).WithCause(res.Err)
	}
	return res.Combined(), nil
}

// SSHArgs builds the deterministic ssh argument list. It never uses a shell.
func SSHArgs(ep Endpoint, keyPath string) []string {
	return SSHArgsWithKnownHosts(ep, keyPath, "")
}

// SSHArgsWithKnownHosts adds an optional throwaway known_hosts file so probes
// can run without touching the user's real file.
func SSHArgsWithKnownHosts(ep Endpoint, keyPath, knownHosts string) []string {
	args := []string{
		"-T",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"-o", "StrictHostKeyChecking=accept-new",
	}
	if knownHosts != "" {
		args = append(args, "-o", "UserKnownHostsFile="+knownHosts)
	}
	if keyPath != "" {
		args = append(args, "-i", keyPath, "-o", "IdentitiesOnly=yes")
	}
	if ep.Port != 22 {
		args = append(args, "-p", strconv.Itoa(ep.Port))
	}
	args = append(args, "git@"+ep.Host)
	return args
}

// TempKnownHosts returns an empty known_hosts file for read-only probes.
func TempKnownHosts() (string, func(), error) {
	f, err := os.CreateTemp("", "blog-bootstrap-known_hosts-*")
	if err != nil {
		return "", func() {}, err
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", func() {}, err
	}
	return name, func() { _ = os.Remove(name) }, nil
}

// ChooseEndpoint probes github.com:22 then ssh.github.com:443. Reachability is
// proven by any non-connection SSH response (including auth failure).
func (m *Manager) ChooseEndpoint(ctx context.Context) (Endpoint, error) {
	return m.ChooseEndpointFrom(ctx, Endpoint{})
}

// ChooseEndpointFrom tries preferred first when set, then the standard order.
// It lets verification keep a previously working endpoint while still falling
// back when that endpoint becomes unreachable.
func (m *Manager) ChooseEndpointFrom(ctx context.Context, preferred Endpoint) (Endpoint, error) {
	sshPath, err := m.ssh()
	if err != nil {
		return Endpoint{}, err
	}
	candidates := make([]Endpoint, 0, len(Endpoints)+1)
	if preferred.Host != "" {
		candidates = append(candidates, preferred)
	}
	for _, ep := range Endpoints {
		if ep == preferred {
			continue
		}
		candidates = append(candidates, ep)
	}
	var last string
	for _, ep := range candidates {
		res := m.Runner.Run(ctx, "", nil, sshPath, SSHArgsWithKnownHosts(ep, "", m.KnownHosts)...)
		out := res.Combined()
		if !netx.IsConnectFailure(out) {
			return ep, nil
		}
		last = ep.Label() + ": " + strings.TrimSpace(out)
	}
	return Endpoint{}, protocol.Retry("NETWORK_UNREACHABLE", "无法连接 GitHub SSH 端点: "+last)
}

// FindReusableKey returns a key already authenticated as the target owner.
// Keys bound to another account are reported so the caller can explain why a
// new key is required.
func (m *Manager) FindReusableKey(ctx context.Context, keys []Key, ep Endpoint) (*Key, []string, error) {
	var wrong []string
	for _, k := range keys {
		out, err := m.Greeting(ctx, k, ep)
		if err != nil {
			return nil, wrong, err
		}
		state, account := EvaluateGreeting(out, m.Owner)
		switch state {
		case GreetingMatch:
			return &k, wrong, nil
		case GreetingWrong:
			wrong = append(wrong, account)
		}
	}
	return nil, wrong, nil
}

// PollEnrollment waits until the key authenticates as owner, a wrong account
// answers, or the timeout elapses.
func (m *Manager) PollEnrollment(ctx context.Context, key Key, ep Endpoint) (GreetingState, string, error) {
	interval := m.PollInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	timeout := m.PollTimeout
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	deadline := m.now().Add(timeout)
	start := m.now()
	attempt := 0
	m.notify("等待公钥生效：每 %d 秒检查一次，最多 %d 秒",
		int(interval.Seconds()), int(timeout.Seconds()))
	for {
		attempt++
		out, err := m.Greeting(ctx, key, ep)
		if err != nil {
			return GreetingNone, "", err
		}
		state, account := EvaluateGreeting(out, m.Owner)
		if state != GreetingNone {
			m.notify("公钥已生效（第 %d 次检查）", attempt)
			return state, account, nil
		}
		if !m.now().Before(deadline) {
			m.notify("等待超时：%d 秒内未检测到公钥", int(timeout.Seconds()))
			return GreetingNone, "", nil
		}
		elapsed := int(m.now().Sub(start).Seconds())
		m.notify("第 %d 次检查：尚未生效（已等待 %d/%d 秒）",
			attempt, elapsed, int(timeout.Seconds()))
		m.wait(interval)
	}
}

// ProbeRead proves the SSH identity can read a repository.
func (m *Manager) ProbeRead(ctx context.Context, url string) error {
	gitPath := m.GitPath
	if gitPath == "" {
		p, err := m.Runner.LookPath("git")
		if err != nil {
			return protocol.Fatal("TOOLS_MISSING", "找不到 git").WithCause(err)
		}
		gitPath = p
	}
	res := m.Runner.Run(ctx, "", nil, gitPath, "ls-remote", url)
	if res.Failed() || res.Err != nil {
		out := strings.TrimSpace(res.Combined())
		if netx.IsConnectFailure(out) {
			return protocol.Retry("NETWORK_UNREACHABLE", "读取仓库失败: "+out)
		}
		return protocol.Fatal("AUTH_READ_FAILED", "无法读取仓库 "+url+": "+out)
	}
	return nil
}

// ProbeWrite proves write access with a dry-run push from a throwaway local
// repository. Nothing is written to the remote.
func (m *Manager) ProbeWrite(ctx context.Context, url, branch, name, email string) error {
	gitPath := m.GitPath
	if gitPath == "" {
		p, err := m.Runner.LookPath("git")
		if err != nil {
			return protocol.Fatal("TOOLS_MISSING", "找不到 git").WithCause(err)
		}
		gitPath = p
	}
	dir, err := os.MkdirTemp("", "blog-bootstrap-probe-*")
	if err != nil {
		return protocol.Fatal("AUTH_PROBE_FAILED", "无法创建临时仓库: "+err.Error()).WithCause(err)
	}
	defer os.RemoveAll(dir)

	if res := m.Runner.Run(ctx, dir, nil, gitPath, "init", "-q"); res.Failed() {
		return protocol.Fatal("AUTH_PROBE_FAILED", "git init 失败: "+strings.TrimSpace(res.Combined()))
	}
	commit := m.Runner.Run(ctx, dir, nil, gitPath,
		"-c", "user.name="+name, "-c", "user.email="+email, "-c", "commit.gpgsign=false",
		"commit", "--allow-empty", "-q", "-m", "blog-bootstrap write probe")
	if commit.Failed() {
		return protocol.Fatal("AUTH_PROBE_FAILED", "创建探针提交失败: "+strings.TrimSpace(commit.Combined()))
	}
	push := m.Runner.Run(ctx, dir, nil, gitPath, "push", "--dry-run", url, "HEAD:refs/heads/"+branch)
	if push.Failed() && isNonFastForward(push.Combined()) {
		// The remote branch already has history that the throwaway probe commit
		// does not descend from. Try a different ref name so the probe does not
		// depend on the deploy branch's history; --dry-run still sends no
		// update, so nothing is created or moved.
		push = m.Runner.Run(ctx, dir, nil, gitPath, "push", "--dry-run", url, "HEAD:refs/heads/blog-bootstrap-write-probe")
	}
	if push.Failed() {
		out := strings.TrimSpace(push.Combined())
		if netx.IsConnectFailure(out) {
			return protocol.Retry("NETWORK_UNREACHABLE", "部署仓库写探针失败: "+out)
		}
		return protocol.Fatal("AUTH_PERMISSION_INSUFFICIENT",
			"部署仓库写权限验证失败（git push --dry-run）: "+out).
			WithDetails(map[string]string{"url": url})
	}
	return nil
}

// isNonFastForward reports a rejected push caused by remote history, not by
// missing permissions.
func isNonFastForward(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "non-fast-forward") ||
		strings.Contains(lower, "fetch first") ||
		strings.Contains(lower, "updates were rejected")
}
