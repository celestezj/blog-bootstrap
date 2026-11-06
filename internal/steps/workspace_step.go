package steps

import (
	"context"
	"path/filepath"

	"github.com/celestezj/blog-bootstrap/internal/core"
	"github.com/celestezj/blog-bootstrap/internal/plan"
	"github.com/celestezj/blog-bootstrap/internal/protocol"
	"github.com/celestezj/blog-bootstrap/internal/run"
	"github.com/celestezj/blog-bootstrap/internal/workspace"
)

// WorkspaceStep clones the blog repository and pins the theme, either as the
// declared submodule or as an independently cloned fixed commit.
type WorkspaceStep struct {
	Ctx *core.Context
}

func (s *WorkspaceStep) ID() string     { return "workspace" }
func (s *WorkspaceStep) Required() bool { return true }

func (s *WorkspaceStep) git() (*workspace.Git, error) {
	path := s.Ctx.State.Snapshot().Paths.Git
	if path == "" {
		p, err := s.Ctx.Runner.LookPath("git")
		if err != nil {
			return nil, protocol.Verify("TOOLS_MISSING", "找不到 git")
		}
		path = p
	}
	return &workspace.Git{
		Runner: s.Ctx.Runner,
		Path:   path,
		Logger: s.Ctx.Logger,
		StepID: "workspace",
	}, nil
}

func (s *WorkspaceStep) sourceURL() string {
	return s.Ctx.Config.Source.WithAlias(effectiveAlias(s.Ctx))
}

func (s *WorkspaceStep) themeURL() string {
	return s.Ctx.Config.Theme.WithAlias(effectiveAlias(s.Ctx))
}

func (s *WorkspaceStep) Inspect(ctx context.Context) (run.Inspection, error) {
	source := s.sourceURL()
	evidence := map[string]string{"dir": s.Ctx.Config.Dir, "branch": s.Ctx.Config.Branch}
	g, err := s.git()
	if err != nil {
		pe := protocol.As(err)
		if pe == nil || pe.Code != "TOOLS_MISSING" {
			return run.Inspection{}, err
		}
		evidence["git"] = "missing"
		return run.Inspection{
			Evidence: evidence,
			Actions: []plan.Action{
				{Kind: "git_clone", Target: s.Ctx.Config.Dir, Detail: "安装 git 后克隆 " + source + "（分支 " + s.Ctx.Config.Branch + "）"},
				{Kind: "git_theme", Target: workspace.ThemePath, Detail: "按子模块或父仓库 gitlink 固定主题提交"},
			},
		}, nil
	}
	target, err := g.InspectTarget(ctx, s.Ctx.Config.Dir, source)
	if err != nil {
		return run.Inspection{}, err
	}
	if !target.Exists {
		return run.Inspection{
			Evidence: evidence,
			Actions: []plan.Action{
				{Kind: "git_clone", Target: s.Ctx.Config.Dir, Detail: "克隆 " + source + "（分支 " + s.Ctx.Config.Branch + "）"},
				{Kind: "git_theme", Target: workspace.ThemePath, Detail: "按子模块或父仓库 gitlink 固定主题提交"},
			},
		}, nil
	}
	evidence["remote"] = target.Remote
	evidence["head"] = target.Head
	if _, err := g.VerifyCheckout(ctx, s.Ctx.Config.Dir, source, s.Ctx.Config.Branch); err != nil {
		pe := protocol.As(err)
		if pe != nil && pe.Code == "REMOTE_MISMATCH" {
			return run.Inspection{}, protocol.Fatal("REPO_CONFLICT",
				"目标目录的 origin 与配置的源仓库不一致，拒绝覆盖").WithDetails(pe.Details)
		}
		msg := "目标目录当前不在配置的分支 " + s.Ctx.Config.Branch + "，请切换分支或更换 workspace.dir"
		return run.Inspection{}, protocol.Human("BRANCH_MISMATCH", msg, &protocol.NextAction{
			Type: "human", Code: "BRANCH_MISMATCH", Message: msg,
		})
	}
	themeActions, themeOK, err := s.inspectTheme(ctx, g)
	if err != nil {
		return run.Inspection{}, err
	}
	return run.Inspection{Satisfied: themeOK, Actions: themeActions, Evidence: evidence}, nil
}

func (s *WorkspaceStep) inspectTheme(ctx context.Context, g *workspace.Git) ([]plan.Action, bool, error) {
	_, declared, err := g.SubmodulePath(ctx, s.Ctx.Config.Dir, workspace.ThemePath)
	if err != nil {
		return nil, false, err
	}
	if declared {
		if evidence, err := g.VerifySubmodule(ctx, s.Ctx.Config.Dir, workspace.ThemePath); err == nil {
			_ = evidence
			return nil, true, nil
		}
		return []plan.Action{{
			Kind:   "git_submodule_update",
			Target: s.Ctx.Config.Dir,
			Detail: "初始化主题子模块并检出父仓库记录的 gitlink",
		}}, false, nil
	}
	gitlink, err := g.ThemeGitlink(ctx, s.Ctx.Config.Dir, workspace.ThemePath)
	if err != nil {
		return nil, false, err
	}
	themeDir := filepath.Join(s.Ctx.Config.Dir, filepath.FromSlash(workspace.ThemePath))
	if head, ok := g.HeadCommit(ctx, themeDir); ok {
		if head == gitlink {
			return nil, true, nil
		}
		return []plan.Action{{
			Kind:   "git_checkout_theme",
			Target: themeDir,
			Detail: "将独立克隆的主题切换到父仓库记录的提交 " + gitlink,
		}}, false, nil
	}
	return []plan.Action{{
		Kind:   "git_theme_clone",
		Target: themeDir,
		Detail: "克隆主题 " + s.themeURL() + " 并检出提交 " + gitlink,
	}}, false, nil
}

func (s *WorkspaceStep) Apply(ctx context.Context) error {
	g, err := s.git()
	if err != nil {
		return err
	}
	if g.IsGitRepo(ctx, s.Ctx.Config.Dir) {
		s.Ctx.Sayf("博客仓库已存在，正在校验分支 %s 与 origin...", s.Ctx.Config.Branch)
	} else {
		s.Ctx.Sayf("正在克隆博客仓库 %s（分支 %s）到 %s...", s.sourceURL(), s.Ctx.Config.Branch, s.Ctx.Config.Dir)
	}
	if _, err := g.EnsureCheckout(ctx, s.sourceURL(), s.Ctx.Config.Dir, s.Ctx.Config.Branch); err != nil {
		return err
	}
	s.Ctx.Sayf("博客仓库就绪: %s", s.Ctx.Config.Dir)
	_, declared, err := g.SubmodulePath(ctx, s.Ctx.Config.Dir, workspace.ThemePath)
	if err != nil {
		return err
	}
	if declared {
		s.Ctx.Sayf("正在初始化主题子模块 %s...", workspace.ThemePath)
		if err := g.ConfigureSubmoduleURL(ctx, s.Ctx.Config.Dir, workspace.ThemePath, s.themeURL()); err != nil {
			return err
		}
		if err := g.InitSubmodules(ctx, s.Ctx.Config.Dir); err != nil {
			return err
		}
		s.Ctx.Sayf("主题子模块就绪")
		return nil
	}
	gitlink, err := g.ThemeGitlink(ctx, s.Ctx.Config.Dir, workspace.ThemePath)
	if err != nil {
		return err
	}
	themeDir := filepath.Join(s.Ctx.Config.Dir, filepath.FromSlash(workspace.ThemePath))
	if head, ok := g.HeadCommit(ctx, themeDir); ok && head != gitlink {
		s.Ctx.Sayf("正在把主题切换到父仓库记录的提交 %s...", gitlink)
		if err := g.CheckoutCommit(ctx, themeDir, gitlink); err != nil {
			return err
		}
		return g.VerifyTheme(ctx, themeDir, gitlink)
	}
	s.Ctx.Sayf("正在克隆主题 %s 并检出提交 %s...", s.themeURL(), gitlink)
	if err := g.CloneTheme(ctx, s.themeURL(), themeDir, gitlink); err != nil {
		return err
	}
	s.Ctx.Sayf("主题就绪: %s", themeDir)
	return nil
}

func (s *WorkspaceStep) Verify(ctx context.Context) (map[string]string, error) {
	g, err := s.git()
	if err != nil {
		return nil, asVerify(err)
	}
	if !g.IsGitRepo(ctx, s.Ctx.Config.Dir) {
		return nil, protocol.Verify("CLONE_MISSING", "找不到博客仓库: "+s.Ctx.Config.Dir)
	}
	evidence, err := g.VerifyCheckout(ctx, s.Ctx.Config.Dir, s.sourceURL(), s.Ctx.Config.Branch)
	if err != nil {
		return nil, asVerify(err)
	}
	_, declared, err := g.SubmodulePath(ctx, s.Ctx.Config.Dir, workspace.ThemePath)
	if err != nil {
		return nil, asVerify(err)
	}
	if declared {
		themeEvidence, err := g.VerifySubmodule(ctx, s.Ctx.Config.Dir, workspace.ThemePath)
		if err != nil {
			return nil, asVerify(err)
		}
		for k, v := range themeEvidence {
			evidence[k] = v
		}
		evidence["theme_mode"] = "submodule"
		return evidence, nil
	}
	gitlink, err := g.ThemeGitlink(ctx, s.Ctx.Config.Dir, workspace.ThemePath)
	if err != nil {
		return nil, asVerify(err)
	}
	themeDir := filepath.Join(s.Ctx.Config.Dir, filepath.FromSlash(workspace.ThemePath))
	head, ok := g.HeadCommit(ctx, themeDir)
	if !ok {
		return nil, protocol.Verify("THEME_MISSING", "独立主题目录不存在或不是 git 仓库: "+themeDir)
	}
	if head != gitlink {
		return nil, protocol.Verify("THEME_MISMATCH", "主题 HEAD 与父仓库 gitlink 不一致").
			WithDetails(map[string]string{"head": head, "gitlink": gitlink})
	}
	evidence["theme_mode"] = "clone"
	evidence["theme_head"] = head
	evidence["gitlink"] = gitlink
	return evidence, nil
}
