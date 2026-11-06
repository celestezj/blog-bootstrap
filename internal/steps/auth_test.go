package steps

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/celestezj/blog-bootstrap/internal/config"
	"github.com/celestezj/blog-bootstrap/internal/core"
	"github.com/celestezj/blog-bootstrap/internal/execx"
	"github.com/celestezj/blog-bootstrap/internal/protocol"
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

// In a non-interactive session Apply must print actionable enrollment
// instructions and hand control back instead of blocking on a 180s poll.
func TestAuthApplyNonTTYHandsOffEnrollment(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	keyPath := filepath.Join(home, ".ssh", "id_ed25519_celestezj")
	clock := func() time.Time { return time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC) }

	fake := execx.NewFake()
	fake.LookPaths["git"] = "/usr/bin/git"
	fake.LookPaths["ssh"] = "/usr/bin/ssh"
	fake.LookPaths["ssh-keygen"] = "/usr/bin/ssh-keygen"
	fake.Handler = func(c execx.Call) execx.Result {
		joined := strings.Join(c.Args, " ")
		switch {
		case strings.Contains(joined, "--version"):
			return execx.Result{Stdout: "git version 2.43.0"}
		case c.Name == "/usr/bin/ssh" && strings.Contains(joined, "-V"):
			return execx.Result{ExitCode: 0, Stderr: "OpenSSH_9.5p1, OpenSSL 3.0"}
		case c.Name == "/usr/bin/ssh":
			return execx.Result{ExitCode: 255, Stderr: "git@github.com: Permission denied (publickey)."}
		case c.Name == "/usr/bin/ssh-keygen" && strings.Contains(joined, "-lf"):
			return execx.Result{Stdout: "256 SHA256:abc123def456 blog-bootstrap celestezj (ED25519)"}
		case c.Name == "/usr/bin/ssh-keygen":
			for i, a := range c.Args {
				if a == "-f" && i+1 < len(c.Args) {
					path := c.Args[i+1]
					_ = os.WriteFile(path, []byte("fake-private"), 0o600)
					_ = os.WriteFile(path+".pub", []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFIXTURE blog-bootstrap celestezj\n"), 0o644)
				}
			}
			return execx.Result{}
		}
		return execx.Result{}
	}

	st := state.New(filepath.Join(root, ".bootstrap", "state.json"), clock)
	var progress []string
	var opened string
	ctx := &core.Context{
		Root: root,
		Home: home,
		Config: &config.Resolved{
			Owner:   "celestezj",
			Alias:   "celestezj",
			KeyPath: keyPath,
		},
		State:    st,
		Runner:   fake,
		GOOS:     "linux",
		GOARCH:   "amd64",
		IsTTY:    false,
		Now:      clock,
		Sleep:    func(time.Duration) { t.Fatal("non-TTY apply must not poll") },
		Progress: func(line string) { progress = append(progress, line) },
		Browser:  core.BrowserFunc(func(url string) error { opened = url; return nil }),
	}
	installer := toolchain.NewInstaller(fake)
	installer.GOOS = "linux"
	step := &AuthStep{Ctx: ctx, Installer: installer}

	err := step.Apply(context.Background())
	pe := protocol.As(err)
	if pe == nil || pe.Code != "AUTH_KEY_NOT_ADDED" {
		t.Fatalf("err = %v, want AUTH_KEY_NOT_ADDED", err)
	}
	if pe.ExitCode() != protocol.ExitHuman {
		t.Fatalf("exit = %d, want human handoff", pe.ExitCode())
	}
	if pe.Next == nil || pe.Next.OpenURL == "" || pe.Next.Poll == nil {
		t.Fatalf("missing actionable next action: %+v", pe.Next)
	}
	if _, ok := pe.Details["fingerprint"]; !ok {
		t.Fatalf("fingerprint must be exposed, details = %v", pe.Details)
	}
	if opened == "" || !strings.HasPrefix(opened, "https://github.com/settings/ssh/new?") {
		t.Fatalf("browser URL = %q", opened)
	}
	joined := strings.Join(progress, "\n")
	for _, want := range []string{
		"需要你在浏览器完成一步操作",
		"Add SSH key",
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFIXTURE",
		"SHA256:abc123def456",
		"当前不是交互式终端",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("progress missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "等待公钥生效") {
		t.Fatalf("non-TTY run must not enter the polling loop:\n%s", joined)
	}
}
