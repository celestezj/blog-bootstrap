package plan_test

import (
	"testing"

	"github.com/celestezj/blog-bootstrap/internal/config"
	"github.com/celestezj/blog-bootstrap/internal/plan"
)

func resolved(t *testing.T) *config.Resolved {
	t.Helper()
	cfg := &config.Config{
		Version: 1,
		Repos: config.Repos{
			Source: "git@github.com:celestezj/blog-delta.git",
			Theme:  "git@github.com:celestezj/hexo-theme-A4.git",
			Deploy: "git@github.com:celestezj/celestezj.github.io.git",
		},
		GitIdentity: config.GitIdentity{Name: "Celeste", Email: "celeste@example.com"},
	}
	r, err := cfg.Resolve(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func steps(gitVersion string) []plan.StepPlan {
	return []plan.StepPlan{{
		ID:        "auth.ssh",
		Required:  true,
		Satisfied: false,
		Actions:   []plan.Action{{Kind: "generate_key", Target: "~/.ssh/id"}},
		Evidence:  map[string]string{"git_version": gitVersion},
	}}
}

func build(t *testing.T, r *config.Resolved, digest string, env map[string]string, st []plan.StepPlan) *plan.Plan {
	t.Helper()
	p, err := plan.Build(r, digest, env, st)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHashStable(t *testing.T) {
	r := resolved(t)
	a := build(t, r, "cfg1", map[string]string{"os": "windows"}, steps("2.43"))
	b := build(t, r, "cfg1", map[string]string{"os": "windows"}, steps("2.43"))
	if a.Hash != b.Hash {
		t.Fatalf("hash changed for identical input: %s != %s", a.Hash, b.Hash)
	}
}

func TestHashInvalidated(t *testing.T) {
	r := resolved(t)
	base := build(t, r, "cfg1", map[string]string{"os": "windows"}, steps("2.43"))

	cases := map[string]*plan.Plan{
		"config digest": build(t, r, "cfg2", map[string]string{"os": "windows"}, steps("2.43")),
		"environment":   build(t, r, "cfg1", map[string]string{"os": "windows", "node": "20"}, steps("2.43")),
		"step evidence": build(t, r, "cfg1", map[string]string{"os": "windows"}, steps("2.44")),
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			if p.Hash == base.Hash {
				t.Fatalf("%s change did not alter the consent hash", name)
			}
		})
	}
}

func TestHashChangesWithIdentity(t *testing.T) {
	r := resolved(t)
	base := build(t, r, "cfg", map[string]string{}, steps("2.43"))
	r.Alias = "celestezj-blog"
	changed := build(t, r, "cfg", map[string]string{}, steps("2.43"))
	if changed.Hash == base.Hash {
		t.Fatal("alias change did not alter the consent hash")
	}
}
