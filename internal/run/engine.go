// Package run implements the detect/apply/verify state machine. Apply never
// marks a step complete; only an independent strict probe does.
package run

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/celestezj/blog-bootstrap/internal/core"
	"github.com/celestezj/blog-bootstrap/internal/plan"
	"github.com/celestezj/blog-bootstrap/internal/protocol"
	"github.com/celestezj/blog-bootstrap/internal/state"
)

// Inspection is a step's read-only assessment.
type Inspection struct {
	Satisfied bool
	Evidence  map[string]string
	Actions   []plan.Action
	Warning   string
}

// Step is one independently detectable, applicable and verifiable unit.
type Step interface {
	ID() string
	Required() bool
	Inspect(ctx context.Context) (Inspection, error)
	Apply(ctx context.Context) error
	Verify(ctx context.Context) (map[string]string, error)
}

// Finalizer runs once after a fully successful Run. Verify never calls it.
type Finalizer interface {
	Finalize(ctx context.Context) (warnings []string, err error)
}

// StepReport is the per-step result exposed in JSON.
type StepReport struct {
	ID       string            `json:"id"`
	Required bool              `json:"required"`
	Status   string            `json:"status"`
	Message  string            `json:"message,omitempty"`
	Evidence map[string]string `json:"evidence,omitempty"`
	Actions  []plan.Action     `json:"actions,omitempty"`
}

// Report is the overall run/verify summary.
type Report struct {
	Applied  bool           `json:"applied"`
	Steps    []StepReport   `json:"steps"`
	Warnings []string       `json:"warnings,omitempty"`
	Receipt  *state.Receipt `json:"receipt,omitempty"`
	PlanHash string         `json:"plan_hash,omitempty"`
}

// Engine executes steps with retry and downstream invalidation.
type Engine struct {
	Ctx        *core.Context
	Steps      []Step
	MaxRetries int
	Backoff    func(attempt int) time.Duration
}

// NewEngine builds an engine with three bounded retries and exponential wait.
func NewEngine(ctx *core.Context, steps ...Step) *Engine {
	return &Engine{
		Ctx:        ctx,
		Steps:      steps,
		MaxRetries: 3,
		Backoff: func(attempt int) time.Duration {
			d := time.Second << uint(attempt)
			if d > 10*time.Second {
				d = 10 * time.Second
			}
			return d
		},
	}
}

func (e *Engine) order() []string {
	ids := make([]string, 0, len(e.Steps))
	for _, s := range e.Steps {
		ids = append(ids, s.ID())
	}
	return ids
}

// Run inspects, applies when needed, then verifies every step.
func (e *Engine) Run(ctx context.Context) (*Report, error) {
	rep := &Report{Applied: true, PlanHash: e.Ctx.PlanHash}
	total := len(e.Steps)
	e.Ctx.Sayf("开始执行安装计划：共 %d 步", total)
	for i, step := range e.Steps {
		sr := StepReport{ID: step.ID(), Required: step.Required()}
		e.Ctx.Sayf("\n[%d/%d] %s", i+1, total, step.ID())

		insp, err := step.Inspect(ctx)
		if err != nil {
			if !step.Required() {
				sr.Status = "skipped"
				sr.Message = "可选步骤检查失败，已跳过: " + err.Error()
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s 可选步骤检查失败，已跳过: %v", step.ID(), err))
				_ = e.markSkipped(step.ID(), err)
				rep.Steps = append(rep.Steps, sr)
				e.Ctx.Sayf("  已跳过（可选步骤）: %s", errorSummary(err))
				continue
			}
			sr.Status = "failed"
			sr.Message = err.Error()
			rep.Steps = append(rep.Steps, sr)
			e.Ctx.Sayf("  失败: %s", errorSummary(err))
			return e.fail(rep, step.ID(), err)
		}
		sr.Evidence = insp.Evidence
		if insp.Warning != "" {
			rep.Warnings = append(rep.Warnings, insp.Warning)
			e.Ctx.Sayf("  警告: %s", insp.Warning)
		}

		switch {
		case insp.Satisfied:
			sr.Status = "satisfied"
			e.Ctx.Sayf("  已满足，正在验证...")
		default:
			sr.Actions = insp.Actions
			for _, act := range insp.Actions {
				e.Ctx.Sayf("  - %s", describeAction(act))
			}
			e.Ctx.Sayf("  执行中...")
			if err := e.retry(ctx, func() error { return step.Apply(ctx) }); err != nil {
				if !step.Required() {
					sr.Status = "skipped"
					sr.Message = "可选步骤失败，已跳过: " + err.Error()
					rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s 可选步骤失败，已跳过: %v", step.ID(), err))
					_ = e.markSkipped(step.ID(), err)
					rep.Steps = append(rep.Steps, sr)
					e.Ctx.Sayf("  已跳过（可选步骤）: %s", errorSummary(err))
					continue
				}
				sr.Status = "failed"
				sr.Message = err.Error()
				rep.Steps = append(rep.Steps, sr)
				e.Ctx.Sayf("  失败: %s", errorSummary(err))
				return e.fail(rep, step.ID(), err)
			}
			sr.Status = "applied"
			e.Ctx.Sayf("  执行完成，正在验证...")
		}

		evidence, err := e.verifyWithRetry(ctx, step)
		if err != nil {
			if !step.Required() {
				sr.Status = "skipped"
				sr.Message = "可选步骤验证失败，已跳过: " + err.Error()
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s 可选步骤验证失败，已跳过: %v", step.ID(), err))
				_ = e.markSkipped(step.ID(), err)
				rep.Steps = append(rep.Steps, sr)
				e.Ctx.Sayf("  已跳过（可选步骤）: %s", errorSummary(err))
				continue
			}
			sr.Status = "failed"
			sr.Message = err.Error()
			rep.Steps = append(rep.Steps, sr)
			e.Ctx.Sayf("  验证失败: %s", errorSummary(err))
			return e.fail(rep, step.ID(), err)
		}
		e.Ctx.Sayf("  验证通过")
		for k, v := range evidence {
			if sr.Evidence == nil {
				sr.Evidence = map[string]string{}
			}
			sr.Evidence[k] = v
		}
		sr.Status = "verified"
		rep.Steps = append(rep.Steps, sr)
		if err := e.Ctx.State.SetStep(step.ID(), state.StepState{
			Status:    state.StatusVerified,
			UpdatedAt: e.Ctx.NowTime(),
			Evidence:  evidence,
		}); err != nil {
			return rep, protocol.Fatal("STATE_WRITE_FAILED", "无法写入状态文件: "+err.Error()).WithCause(err)
		}
	}
	e.Ctx.Sayf("\n全部必需步骤验证通过，正在写入收据...")
	if err := e.writeReceipt(rep); err != nil {
		return rep, err
	}
	return rep, nil
}

// VerifyStrict re-probes every step without applying anything. This is the
// only success signal in the protocol.
func (e *Engine) VerifyStrict(ctx context.Context) (*Report, error) {
	rep := &Report{Applied: false, PlanHash: e.Ctx.PlanHash}
	total := len(e.Steps)
	e.Ctx.Sayf("验证 %d 个步骤（只探测，不修改系统）", total)
	for i, step := range e.Steps {
		sr := StepReport{ID: step.ID(), Required: step.Required()}
		e.Ctx.Sayf("\n[%d/%d] %s", i+1, total, step.ID())
		evidence, err := e.verifyWithRetry(ctx, step)
		if err != nil {
			if !step.Required() {
				sr.Status = "skipped"
				sr.Message = err.Error()
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s 可选步骤未通过: %v", step.ID(), err))
				_ = e.markSkipped(step.ID(), err)
				rep.Steps = append(rep.Steps, sr)
				e.Ctx.Sayf("  已跳过（可选步骤）: %s", errorSummary(err))
				continue
			}
			sr.Status = "failed"
			sr.Message = err.Error()
			rep.Steps = append(rep.Steps, sr)
			e.Ctx.Sayf("  验证失败: %s", errorSummary(err))
			return e.fail(rep, step.ID(), err)
		}
		sr.Status = "verified"
		e.Ctx.Sayf("  验证通过")
		sr.Evidence = evidence
		rep.Steps = append(rep.Steps, sr)
		if err := e.Ctx.State.SetStep(step.ID(), state.StepState{
			Status:    state.StatusVerified,
			UpdatedAt: e.Ctx.NowTime(),
			Evidence:  evidence,
		}); err != nil {
			return rep, protocol.Fatal("STATE_WRITE_FAILED", "无法写入状态文件: "+err.Error()).WithCause(err)
		}
	}
	e.Ctx.Sayf("\n全部必需步骤验证通过，正在写入收据...")
	if err := e.writeReceipt(rep); err != nil {
		return rep, err
	}
	return rep, nil
}

func describeAction(act plan.Action) string {
	detail := strings.TrimSpace(act.Detail)
	if detail != "" {
		return detail
	}
	target := strings.TrimSpace(act.Target)
	if target != "" {
		return act.Kind + " " + target
	}
	return act.Kind
}

func errorSummary(err error) string {
	if pe := protocol.As(err); pe != nil {
		return pe.Code + ": " + pe.Message
	}
	return err.Error()
}

func (e *Engine) verifyWithRetry(ctx context.Context, step Step) (map[string]string, error) {
	var evidence map[string]string
	err := e.retry(ctx, func() error {
		var verr error
		evidence, verr = step.Verify(ctx)
		return verr
	})
	return evidence, err
}

func (e *Engine) retry(ctx context.Context, fn func() error) error {
	var last *protocol.Error
	for attempt := 0; attempt <= e.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := e.Backoff
			if backoff == nil {
				backoff = func(int) time.Duration { return time.Second }
			}
			e.Ctx.Wait(backoff(attempt - 1))
		}
		err := fn()
		if err == nil {
			return nil
		}
		last = classify(err)
		if last.Class != protocol.ClassRetry {
			return last
		}
		if ctx.Err() != nil {
			return protocol.Fatal("CANCELLED", "操作被取消").WithCause(ctx.Err())
		}
	}
	return humanizeRetry(last, e.MaxRetries)
}

func (e *Engine) markSkipped(id string, err error) error {
	pe := classify(err)
	return e.Ctx.State.SetStep(id, state.StepState{
		Status:    state.StatusSkipped,
		UpdatedAt: e.Ctx.NowTime(),
		Message:   pe.Message,
		ErrorCode: pe.Code,
	})
}

func (e *Engine) fail(rep *Report, id string, err error) (*Report, error) {
	pe := classify(err)
	if pe.Class == protocol.ClassRetry {
		pe = humanizeRetry(pe, e.MaxRetries)
	}
	_ = e.Ctx.State.FailStep(e.order(), id, state.StepState{
		Status:    state.StatusFailed,
		UpdatedAt: e.Ctx.NowTime(),
		ErrorCode: pe.Code,
		Message:   pe.Message,
		Evidence:  pe.Details,
	})
	return rep, pe
}

func (e *Engine) writeReceipt(rep *Report) error {
	if e.Ctx.Config == nil {
		return nil
	}
	snap := e.Ctx.State.Snapshot()
	alias := e.Ctx.Config.Alias
	if snap.EffectiveAlias != "" {
		alias = snap.EffectiveAlias
	}
	receipt := &state.Receipt{
		PlanHash:     e.Ctx.PlanHash,
		Owner:        e.Ctx.Config.Owner,
		Alias:        alias,
		Endpoint:     snap.Endpoint,
		WorkspaceDir: e.Ctx.Config.Dir,
		VerifiedAt:   e.Ctx.NowTime(),
		Tools: map[string]string{
			"git":  snap.Paths.Git,
			"node": snap.Paths.Node,
			"npm":  snap.Paths.Npm,
		},
	}
	for _, sr := range rep.Steps {
		if sr.Status != "verified" {
			continue
		}
		receipt.Steps = append(receipt.Steps, state.ReceiptStep{ID: sr.ID, Evidence: sr.Evidence})
		if fp, ok := sr.Evidence["fingerprint"]; ok {
			receipt.PublicKeyFingerprint = fp
		}
	}
	if err := e.Ctx.State.WriteReceipt(receipt); err != nil {
		return protocol.Fatal("STATE_WRITE_FAILED", "无法写入收据: "+err.Error()).WithCause(err)
	}
	rep.Receipt = receipt
	return nil
}

func classify(err error) *protocol.Error {
	if pe := protocol.As(err); pe != nil {
		return pe
	}
	return protocol.Fatal("UNEXPECTED", err.Error()).WithCause(err)
}

func humanizeRetry(pe *protocol.Error, attempts int) *protocol.Error {
	if pe == nil {
		return nil
	}
	msg := fmt.Sprintf("%s（已重试 %d 次，请检查网络或权限后重试）", pe.Message, attempts)
	return &protocol.Error{
		Code:    pe.Code,
		Message: msg,
		Class:   protocol.ClassHuman,
		Cause:   pe,
		LogPath: pe.LogPath,
		Details: pe.Details,
		Next: &protocol.NextAction{
			Type:    "human",
			Code:    pe.Code,
			Message: msg,
		},
	}
}
