package steps

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/celestezj/blog-bootstrap/internal/config"
	"github.com/celestezj/blog-bootstrap/internal/core"
	"github.com/celestezj/blog-bootstrap/internal/execx"
	"github.com/celestezj/blog-bootstrap/internal/state"
	"github.com/celestezj/blog-bootstrap/internal/toolchain"
)

// On a machine with ssh but no git, Apply still writes the SSH config block and
// may create a key after installing git, so the plan must list those actions.
func TestAuthInspectListsCredentialActionsWithoutGit(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	clock := func() time.Time { return time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC) }

	fake := execx.NewFake()
	fake.LookPaths["ssh"] = "/usr/bin/ssh"
	fake.LookPaths["winget"] = "C:\\winget.exe"
	fake.Handler = func(c execx.Call) execx.Result {
		if strings.Contains(strings.Join(c.Args, " "), "-V") {
			return execx.Result{Stdout: "OpenSSH_9.5p1"}
		}
		return execx.Result{ExitCode: 255, Stderr: "Permission denied (publickey)."}
	}

	st := state.New(filepath.Join(root, ".bootstrap", "state.json"), clock)
	ctx := &core.Context{
		Root:   root,
		Home:   home,
		Config: &config.Resolved{Owner: "celestezj", Alias: "celestezj", KeyPath: filepath.Join(home, ".ssh", "id_ed25519_celestezj")},
		State:  st,
		Runner: fake,
		GOOS:   "windows",
		Now:    clock,
		Sleep:  func(time.Duration) {},
	}
	installer := toolchain.NewInstaller(fake)
	installer.GOOS = "windows"
	step := &AuthStep{Ctx: ctx, Installer: installer}

	insp, err := step.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if insp.Satisfied {
		t.Fatal("inspection must not be satisfied while git is missing")
	}
	kinds := map[string]bool{}
	for _, action := range insp.Actions {
		kinds[action.Kind] = true
	}
	for _, want := range []string{"install_package", "download", "write_file", "create_key"} {
		if !kinds[want] {
			t.Fatalf("missing %q action in plan: %+v", want, insp.Actions)
		}
	}

	for _, c := range fake.Calls {
		joined := strings.Join(c.Args, " ")
		if c.Name != "/usr/bin/ssh" || !strings.Contains(joined, "-T") {
			continue
		}
		if !strings.Contains(joined, "UserKnownHostsFile=") {
			t.Fatalf("inspection probe must not write real known_hosts: %s", joined)
		}
	}
}
