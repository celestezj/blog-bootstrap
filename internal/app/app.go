// Package app implements the blog-bootstrap CLI surface: plan, run, status,
// next, verify, doctor, agent-guide and version.
package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/celestezj/blog-bootstrap/internal/agent"
	"github.com/celestezj/blog-bootstrap/internal/browser"
	"github.com/celestezj/blog-bootstrap/internal/config"
	"github.com/celestezj/blog-bootstrap/internal/core"
	"github.com/celestezj/blog-bootstrap/internal/execx"
	"github.com/celestezj/blog-bootstrap/internal/lock"
	"github.com/celestezj/blog-bootstrap/internal/plan"
	"github.com/celestezj/blog-bootstrap/internal/protocol"
	"github.com/celestezj/blog-bootstrap/internal/run"
	"github.com/celestezj/blog-bootstrap/internal/state"
	"github.com/celestezj/blog-bootstrap/internal/steps"
)

const defaultConfig = "blog-setup.yml"

// App holds injected dependencies so commands can run in tests.
type App struct {
	Version string

	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader

	GOOS   string
	GOARCH string
	Home   string
	IsTTY  bool

	Runner  execx.Runner
	HTTP    *http.Client
	Browser core.Browser

	Now   func() time.Time
	Sleep func(time.Duration)

	StepsFactory func(*core.Context) []run.Step
}

// New returns an app wired to the real environment.
func New(version string) *App {
	httpClient := &http.Client{Timeout: 60 * time.Second}
	return &App{
		Version:      version,
		Stdout:       os.Stdout,
		Stderr:       os.Stderr,
		Stdin:        os.Stdin,
		GOOS:         runtime.GOOS,
		GOARCH:       runtime.GOARCH,
		Home:         homeDir(),
		IsTTY:        stdinIsTTY(),
		Runner:       execx.Exec{},
		HTTP:         httpClient,
		Browser:      core.BrowserFunc(browser.Open),
		Now:          time.Now,
		Sleep:        time.Sleep,
		StepsFactory: steps.NewAll,
	}
}

// Main runs the CLI and returns the process exit code.
func Main(version string) int {
	return New(version).Run(os.Args[1:])
}

// Run dispatches one command and maps failures onto the fixed exit codes.
func (a *App) Run(args []string) int {
	if len(args) == 0 {
		return a.fail("", protocol.Fatal("USAGE", "缺少命令；可用: plan, run, status, next, verify, doctor, agent-guide, version"), false)
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "version", "--version", "-v":
		return a.cmdVersion(rest)
	case "help", "--help", "-h":
		fmt.Fprint(a.Stdout, usage)
		return protocol.ExitOK
	case "plan":
		return a.cmdPlan(rest)
	case "run":
		return a.cmdRun(rest)
	case "status":
		return a.cmdStatus(rest)
	case "next":
		return a.cmdNext(rest)
	case "verify":
		return a.cmdVerify(rest)
	case "doctor":
		return a.cmdDoctor(rest)
	case "agent-guide":
		return a.cmdAgentGuide(rest)
	default:
		return a.fail(cmd, protocol.Fatal("USAGE", "未知命令: "+cmd), false)
	}
}

const usage = `blog-bootstrap <command> [flags]

Commands:
  plan --json                 读取配置并输出改动计划与 consent hash
  run --consent <hash> --json 执行已确认的计划
  status --json               查看缓存状态（不作为成功证据）
  next --json                 返回下一步命令
  verify --strict --json      现场重跑全部探针（唯一成功信号）
  verify --step <id> --json   只重跑一个步骤的探针
  doctor --json               安装后健康检查（仅给警告）
  agent-guide                 输出 AI 安装手册
  version                     输出版本号

Common flags:
  --config <path>   配置文件路径（默认 blog-setup.yml）
  --json            输出 schema_version=1 的机器可读 JSON
`

type common struct {
	configPath string
	json       bool
}

func (a *App) parseCommon(cmd string, args []string, register func(*flag.FlagSet)) (*common, []string, error) {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	c := &common{}
	fs.StringVar(&c.configPath, "config", defaultConfig, "configuration file")
	fs.BoolVar(&c.json, "json", false, "machine-readable output")
	if register != nil {
		register(fs)
	}
	if err := fs.Parse(args); err != nil {
		return c, nil, protocol.Fatal("USAGE", err.Error())
	}
	return c, fs.Args(), nil
}

func (a *App) cmdVersion(args []string) int {
	c, rest, err := a.parseCommon("version", args, nil)
	if err != nil {
		return a.fail("version", err, false)
	}
	if len(rest) > 0 {
		return a.fail("version", protocol.Fatal("USAGE", "version 不接受额外参数"), c.json)
	}
	if c.json {
		return a.success("version", map[string]string{"version": a.Version})
	}
	fmt.Fprintln(a.Stdout, a.Version)
	return protocol.ExitOK
}

func (a *App) cmdPlan(args []string) int {
	c, rest, err := a.parseCommon("plan", args, nil)
	if err != nil {
		return a.fail("plan", err, c != nil && c.json)
	}
	if len(rest) > 0 {
		return a.fail("plan", protocol.Fatal("USAGE", "plan 不接受位置参数"), c.json)
	}
	ctx, steps, err := a.prepare(c.configPath)
	if err != nil {
		return a.fail("plan", err, c.json)
	}
	p, err := a.buildPlan(ctx, steps)
	if err != nil {
		return a.fail("plan", err, c.json)
	}
	if c.json {
		return a.success("plan", p)
	}
	a.printPlan(p)
	return protocol.ExitOK
}

func (a *App) cmdRun(args []string) int {
	consent := ""
	c, rest, err := a.parseCommon("run", args, func(fs *flag.FlagSet) {
		fs.StringVar(&consent, "consent", "", "consent hash from plan")
	})
	if err != nil {
		return a.fail("run", err, c != nil && c.json)
	}
	if len(rest) > 0 {
		return a.fail("run", protocol.Fatal("USAGE", "run 不接受位置参数"), c.json)
	}
	if strings.TrimSpace(consent) == "" {
		return a.fail("run", protocol.Fatal("USAGE", "run 必须携带 --consent <plan hash>"), c.json)
	}
	ctx, steps, err := a.prepare(c.configPath)
	if err != nil {
		return a.fail("run", err, c.json)
	}

	fl, err := lock.Acquire(filepath.Join(ctx.BootstrapDir(), "run.lock"), ctx.State.Snapshot().RunID, a.now)
	if err != nil {
		return a.fail("run", err, c.json)
	}
	defer func() { _ = fl.Release() }()

	fresh, err := a.buildPlan(ctx, steps)
	if err != nil {
		return a.fail("run", err, c.json)
	}
	if !strings.EqualFold(strings.TrimSpace(consent), fresh.Hash) {
		err := protocol.Human("CONSENT_MISMATCH",
			"plan 已变化或同意哈希不匹配；请重新展示计划并取得同意",
			&protocol.NextAction{
				Type:    "human",
				Code:    "CONSENT_MISMATCH",
				Message: "计划已变化，请把新的 plan 展示给用户并重新取得同意",
				Command: "blog-bootstrap plan --json",
			})
		return a.fail("run", err, c.json)
	}
	if err := ctx.State.SetPlanHash(fresh.Hash); err != nil {
		return a.fail("run", protocol.Fatal("STATE_WRITE_FAILED", "无法写入状态文件: "+err.Error()).WithCause(err), c.json)
	}
	ctx.PlanHash = fresh.Hash
	for _, step := range steps {
		if ctx.Logger != nil {
			_ = ctx.Logger.Begin(step.ID())
		}
	}

	engine := run.NewEngine(ctx, steps...)
	rep, err := engine.Run(context.Background())
	if err != nil {
		return a.failWithData("run", err, rep, c.json)
	}
	if c.json {
		return a.success("run", rep)
	}
	a.printReport("安装", rep)
	return protocol.ExitOK
}

func (a *App) cmdStatus(args []string) int {
	c, rest, err := a.parseCommon("status", args, nil)
	if err != nil {
		return a.fail("status", err, c != nil && c.json)
	}
	if len(rest) > 0 {
		return a.fail("status", protocol.Fatal("USAGE", "status 不接受位置参数"), c.json)
	}
	ctx, _, err := a.prepare(c.configPath)
	if err != nil {
		return a.fail("status", err, c.json)
	}
	snap := ctx.State.Snapshot()
	if c.json {
		return a.success("status", snap)
	}
	fmt.Fprintf(a.Stdout, "run_id: %s\n", snap.RunID)
	if snap.Receipt != nil {
		fmt.Fprintf(a.Stdout, "receipt: %s\n", snap.Receipt.VerifiedAt.Format(time.RFC3339))
	} else {
		fmt.Fprintln(a.Stdout, "receipt: 无（尚未通过 strict verify）")
	}
	for _, id := range ctx.State.StepOrder() {
		st := snap.Steps[id]
		fmt.Fprintf(a.Stdout, "  %-10s %s %s\n", id, st.Status, st.ErrorCode)
	}
	return protocol.ExitOK
}

type nextData struct {
	Step    string `json:"step,omitempty"`
	Status  string `json:"status,omitempty"`
	Command string `json:"command"`
}

func (a *App) cmdNext(args []string) int {
	c, rest, err := a.parseCommon("next", args, nil)
	if err != nil {
		return a.fail("next", err, c != nil && c.json)
	}
	if len(rest) > 0 {
		return a.fail("next", protocol.Fatal("USAGE", "next 不接受位置参数"), c.json)
	}
	ctx, steps, err := a.prepare(c.configPath)
	if err != nil {
		return a.fail("next", err, c.json)
	}
	snap := ctx.State.Snapshot()
	data := nextData{}
	if snap.Receipt != nil {
		data.Command = "blog-bootstrap verify --strict --json"
		data.Status = "receipt 已存在；仍需现场重跑 strict verify 才能再次判定成功"
	} else {
		planHash := snap.PlanHash
		if planHash != "" {
			data.Command = "blog-bootstrap run --consent " + planHash + " --json"
		} else {
			data.Command = "blog-bootstrap plan --json"
		}
		for _, step := range steps {
			st := snap.Steps[step.ID()]
			if st.Status != state.StatusVerified {
				data.Step = step.ID()
				data.Status = string(st.Status)
				if st.ErrorCode != "" {
					data.Status += " (" + st.ErrorCode + ")"
				}
				break
			}
		}
	}
	if c.json {
		env := protocol.NewSuccess("next", data)
		env.NextAction = &protocol.NextAction{Type: "run", Command: data.Command, Message: data.Status}
		return a.writeJSON(env)
	}
	fmt.Fprintf(a.Stdout, "下一步: %s\n", data.Command)
	if data.Step != "" {
		fmt.Fprintf(a.Stdout, "步骤: %s (%s)\n", data.Step, data.Status)
	}
	return protocol.ExitOK
}

func (a *App) cmdVerify(args []string) int {
	strict := false
	stepID := ""
	c, rest, err := a.parseCommon("verify", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&strict, "strict", false, "re-run every probe")
		fs.StringVar(&stepID, "step", "", "verify one step")
	})
	if err != nil {
		return a.fail("verify", err, c != nil && c.json)
	}
	if len(rest) > 0 {
		return a.fail("verify", protocol.Fatal("USAGE", "verify 不接受位置参数"), c.json)
	}
	if !strict && stepID == "" {
		return a.fail("verify", protocol.Fatal("USAGE", "verify 需要 --strict，或使用 --step <id> 只验证单个步骤"), c.json)
	}
	ctx, steps, err := a.prepare(c.configPath)
	if err != nil {
		return a.fail("verify", err, c.json)
	}

	if stepID != "" {
		for _, step := range steps {
			if step.ID() != stepID {
				continue
			}
			evidence, err := step.Verify(context.Background())
			if err != nil {
				return a.fail("verify", err, c.json)
			}
			data := map[string]interface{}{"step": stepID, "required": step.Required(), "evidence": evidence}
			if c.json {
				return a.success("verify", data)
			}
			fmt.Fprintf(a.Stdout, "%s: ok\n", stepID)
			for k, v := range evidence {
				fmt.Fprintf(a.Stdout, "  %s: %s\n", k, v)
			}
			return protocol.ExitOK
		}
		return a.fail("verify", protocol.Fatal("UNKNOWN_STEP", "未知步骤: "+stepID), c.json)
	}

	engine := run.NewEngine(ctx, steps...)
	rep, err := engine.VerifyStrict(context.Background())
	if err != nil {
		return a.failWithData("verify", err, rep, c.json)
	}
	if c.json {
		return a.success("verify", rep)
	}
	a.printReport("验证", rep)
	return protocol.ExitOK
}

type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type doctorData struct {
	Checks   []doctorCheck `json:"checks"`
	Warnings []string      `json:"warnings,omitempty"`
}

func (a *App) cmdDoctor(args []string) int {
	c, rest, err := a.parseCommon("doctor", args, nil)
	if err != nil {
		return a.fail("doctor", err, c != nil && c.json)
	}
	if len(rest) > 0 {
		return a.fail("doctor", protocol.Fatal("USAGE", "doctor 不接受位置参数"), c.json)
	}
	ctx, _, err := a.prepare(c.configPath)
	if err != nil {
		return a.fail("doctor", err, c.json)
	}
	data := doctorData{}
	add := func(name, status, detail string) {
		data.Checks = append(data.Checks, doctorCheck{Name: name, Status: status, Detail: detail})
		if status != "ok" {
			data.Warnings = append(data.Warnings, name+": "+detail)
		}
	}
	add("config", "ok", ctx.ConfigPath)

	snap := ctx.State.Snapshot()
	if len(snap.Steps) == 0 {
		add("state", "warn", "尚未运行过 plan/run")
	} else {
		add("state", "ok", "run_id "+snap.RunID)
	}
	if snap.Receipt == nil {
		add("receipt", "warn", "没有收据；请运行 verify --strict")
	} else {
		add("receipt", "ok", snap.Receipt.VerifiedAt.Format(time.RFC3339))
	}

	dir := ctx.Config.Dir
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		add("workspace", "warn", "工作区不存在: "+dir)
	} else {
		add("workspace", "ok", dir)
	}
	for _, name := range []string{"index.html", "search.json"} {
		p := filepath.Join(dir, "public", name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Size() > 0 {
			add("public/"+name, "ok", fmt.Sprintf("%d bytes", st.Size()))
		} else {
			add("public/"+name, "warn", "缺失或为空: "+p)
		}
	}
	if st, err := os.Stat(filepath.Join(dir, "node_modules")); err == nil && st.IsDir() {
		add("node_modules", "ok", "已安装")
	} else {
		add("node_modules", "warn", "未安装依赖")
	}

	for _, name := range []string{"git", "node", "npm"} {
		if p, err := a.Runner.LookPath(name); err == nil && p != "" {
			add(name, "ok", p)
		} else {
			add(name, "warn", "不在当前 PATH；安装器会使用状态文件中记录的绝对路径")
		}
	}
	add("strict-verify", "warn", "doctor 只做健康检查；成功判定必须使用 verify --strict")

	if c.json {
		return a.success("doctor", data)
	}
	for _, chk := range data.Checks {
		fmt.Fprintf(a.Stdout, "%-14s %-5s %s\n", chk.Name, chk.Status, chk.Detail)
	}
	return protocol.ExitOK
}

func (a *App) cmdAgentGuide(args []string) int {
	c, rest, err := a.parseCommon("agent-guide", args, nil)
	if err != nil {
		return a.fail("agent-guide", err, c != nil && c.json)
	}
	if len(rest) > 0 {
		return a.fail("agent-guide", protocol.Fatal("USAGE", "agent-guide 不接受位置参数"), c.json)
	}
	if c.json {
		return a.success("agent-guide", map[string]string{"guide": agent.Guide()})
	}
	fmt.Fprint(a.Stdout, agent.Guide())
	return protocol.ExitOK
}

func (a *App) prepare(configPath string) (*core.Context, []run.Step, error) {
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return nil, nil, protocol.Fatal("CONFIG_INVALID", "无法解析配置路径: "+err.Error()).WithCause(err)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, protocol.Fatal("CONFIG_NOT_FOUND",
				fmt.Sprintf("找不到配置文件 %s；请从 blog-setup.example.yml 复制一份并填写", abs))
		}
		return nil, nil, protocol.Fatal("CONFIG_INVALID", "无法读取配置文件: "+err.Error()).WithCause(err)
	}
	cfg, err := config.Load(abs)
	if err != nil {
		return nil, nil, err
	}
	resolved, err := cfg.Resolve(a.Home, filepath.Dir(abs))
	if err != nil {
		return nil, nil, err
	}
	store, err := state.Open(filepath.Join(resolved.BaseDir, ".bootstrap", "state.json"), a.now)
	if err != nil {
		return nil, nil, protocol.Fatal("STATE_READ_FAILED", "状态文件损坏或不可读: "+err.Error()).WithCause(err)
	}
	ctx := &core.Context{
		Root:         resolved.BaseDir,
		Home:         a.Home,
		ConfigPath:   abs,
		Config:       resolved,
		ConfigDigest: plan.Digest(raw),
		State:        store,
		Runner:       a.Runner,
		HTTP:         a.HTTP,
		Browser:      a.Browser,
		Logger:       core.NewLogger(filepath.Join(resolved.BaseDir, ".bootstrap", "logs")),
		GOOS:         a.GOOS,
		GOARCH:       a.GOARCH,
		IsTTY:        a.IsTTY,
		Now:          a.Now,
		Sleep:        a.Sleep,
		Stdout:       a.Stdout,
		Stderr:       a.Stderr,
	}
	factory := a.StepsFactory
	if factory == nil {
		factory = steps.NewAll
	}
	return ctx, factory(ctx), nil
}

func (a *App) buildPlan(ctx *core.Context, stepList []run.Step) (*plan.Plan, error) {
	stepPlans := make([]plan.StepPlan, 0, len(stepList))
	for _, step := range stepList {
		insp, err := step.Inspect(context.Background())
		if err != nil {
			return nil, err
		}
		stepPlans = append(stepPlans, plan.StepPlan{
			ID:        step.ID(),
			Required:  step.Required(),
			Satisfied: insp.Satisfied,
			Actions:   insp.Actions,
			Evidence:  insp.Evidence,
			Warning:   insp.Warning,
		})
	}
	env := map[string]string{"os": ctx.GOOS, "arch": ctx.GOARCH}
	return plan.Build(ctx.Config, ctx.ConfigDigest, env, stepPlans)
}

func (a *App) printPlan(p *plan.Plan) {
	fmt.Fprintf(a.Stdout, "plan hash: %s\n", p.Hash)
	fmt.Fprintf(a.Stdout, "owner: %s  alias: %s\n", p.Owner, p.Alias)
	fmt.Fprintf(a.Stdout, "workspace: %s  branch: %s\n", p.WorkspaceDir, p.Branch)
	for _, sp := range p.Steps {
		stateLabel := "需执行"
		if sp.Satisfied {
			stateLabel = "已满足"
		}
		fmt.Fprintf(a.Stdout, "\n[%s] %s%s\n", sp.ID, stateLabel, optional(sp.Required))
		if sp.Warning != "" {
			fmt.Fprintf(a.Stdout, "  警告: %s\n", sp.Warning)
		}
		for _, act := range sp.Actions {
			fmt.Fprintf(a.Stdout, "  - %s %s: %s\n", act.Kind, act.Target, act.Detail)
		}
	}
}

func (a *App) printReport(title string, rep *run.Report) {
	fmt.Fprintf(a.Stdout, "%s结果:\n", title)
	for _, sr := range rep.Steps {
		fmt.Fprintf(a.Stdout, "  %-10s %s\n", sr.ID, sr.Status)
		if sr.Message != "" {
			fmt.Fprintf(a.Stdout, "    %s\n", sr.Message)
		}
	}
	for _, w := range rep.Warnings {
		fmt.Fprintf(a.Stdout, "  警告: %s\n", w)
	}
}

func optional(required bool) string {
	if required {
		return ""
	}
	return " (可选)"
}

func (a *App) success(cmd string, data interface{}) int {
	return a.writeJSON(protocol.NewSuccess(cmd, data))
}

func (a *App) fail(cmd string, err error, json bool) int {
	return a.failWithData(cmd, err, nil, json)
}

func (a *App) failWithData(cmd string, err error, data interface{}, json bool) int {
	pe := protocol.As(err)
	if pe == nil {
		pe = protocol.Fatal("UNEXPECTED", err.Error()).WithCause(err)
	}
	if json {
		env := protocol.NewFailure(cmd, pe)
		env.Data = data
		a.writeJSON(env)
		return pe.ExitCode()
	}
	fmt.Fprintf(a.Stderr, "错误: %s\n%s\n", pe.Code, pe.Message)
	if pe.Next != nil {
		if pe.Next.Message != "" && pe.Next.Message != pe.Message {
			fmt.Fprintf(a.Stderr, "下一步: %s\n", pe.Next.Message)
		}
		if pe.Next.OpenURL != "" {
			fmt.Fprintf(a.Stderr, "打开: %s\n", pe.Next.OpenURL)
		}
		if pe.Next.Command != "" {
			fmt.Fprintf(a.Stderr, "命令: %s\n", pe.Next.Command)
		}
	}
	if pe.LogPath != "" {
		fmt.Fprintf(a.Stderr, "日志: %s\n", pe.LogPath)
	}
	return pe.ExitCode()
}

func (a *App) writeJSON(v interface{}) int {
	data, err := protocol.Marshal(v)
	if err != nil {
		fmt.Fprintf(a.Stderr, "无法序列化 JSON: %v\n", err)
		return protocol.ExitFatal
	}
	_, _ = a.Stdout.Write(data)
	return protocol.ExitOK
}

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

func stdinIsTTY() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}
