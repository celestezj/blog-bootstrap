// Package steps implements the five install steps: auth.ssh, node, hexo,
// workspace and preview.
package steps

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/celestezj/blog-bootstrap/internal/core"
	"github.com/celestezj/blog-bootstrap/internal/protocol"
	"github.com/celestezj/blog-bootstrap/internal/run"
	"github.com/celestezj/blog-bootstrap/internal/sshauth"
	"github.com/celestezj/blog-bootstrap/internal/toolchain"
)

// NewAll wires the five steps in install order.
func NewAll(ctx *core.Context) []run.Step {
	installer := toolchain.NewInstaller(ctx.Runner)
	installer.GOOS = ctx.GOOS
	portable := &toolchain.Portable{
		HTTP:     ctx.HTTP,
		ToolsDir: ctx.ToolsDir(),
		GOOS:     ctx.GOOS,
		GOARCH:   ctx.GOARCH,
		Logf: func(format string, args ...interface{}) {
			line := strings.TrimSpace(fmt.Sprintf(format, args...))
			if ctx.Logger != nil {
				ctx.Logger.Append("node", "portable", line)
			}
			ctx.Sayf("  %s", line)
		},
		Progress: func(downloaded, total int64) {
			if total > 0 {
				ctx.Sayf("  已下载 %s / %s（%.0f%%）", humanBytes(downloaded), humanBytes(total),
					float64(downloaded)*100/float64(total))
				return
			}
			ctx.Sayf("  已下载 %s", humanBytes(downloaded))
		},
	}
	nodes := &NodeStep{Ctx: ctx, Installer: installer, Portable: portable}
	return []run.Step{
		&AuthStep{Ctx: ctx, Installer: installer, Portable: portable},
		nodes,
		&HexoStep{Ctx: ctx, Nodes: nodes},
		&WorkspaceStep{Ctx: ctx},
		&PreviewStep{Ctx: ctx, Nodes: nodes},
	}
}

// configPath is ~/.ssh/config for this user.
func configPath(ctx *core.Context) string {
	return filepath.Join(ctx.Home, ".ssh", "config")
}

// effectiveAlias returns the alias actually present in SSH config, falling back
// to the configured alias when the block has not been written yet.
func effectiveAlias(ctx *core.Context) string {
	if ctx.Config == nil {
		return ""
	}
	if snap := ctx.State.Snapshot(); snap.EffectiveAlias != "" {
		return snap.EffectiveAlias
	}
	host, port := endpointParts(ctx.State.Snapshot().Endpoint)
	res, err := sshauth.PreviewHostBlock(configPath(ctx), ctx.Config.Alias, host, port, ctx.Config.KeyPath)
	if err == nil && res.Alias != "" {
		return res.Alias
	}
	return ctx.Config.Alias
}

func endpointParts(label string) (string, int) {
	if label == "" {
		return "github.com", 22
	}
	idx := strings.LastIndex(label, ":")
	if idx < 0 {
		return label, 22
	}
	port, err := strconv.Atoi(label[idx+1:])
	if err != nil {
		return label, 22
	}
	return label[:idx], port
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

// asVerify converts deterministic apply/probe failures into verification
// failures while preserving retryable network errors.
func asVerify(err error) error {
	pe := protocol.As(err)
	if pe == nil {
		return protocol.Verify("VERIFY_FAILED", err.Error()).WithCause(err)
	}
	if pe.Class == protocol.ClassRetry {
		return pe
	}
	return &protocol.Error{
		Code:    pe.Code,
		Message: pe.Message,
		Class:   protocol.ClassVerify,
		LogPath: pe.LogPath,
		Details: pe.Details,
		Cause:   pe,
	}
}

func humanToolError(tool string, cause error) error {
	command := ""
	if se, ok := cause.(*toolchain.SystemInstallError); ok {
		command = se.Command
	}
	msg := "无法自动安装 " + tool + "，请手动安装后重试"
	if command != "" {
		msg += "，例如: " + command
	}
	return protocol.Human("TOOLS_MISSING", msg, &protocol.NextAction{
		Type:    "human",
		Code:    "TOOLS_MISSING",
		Message: msg,
		Command: command,
	})
}
