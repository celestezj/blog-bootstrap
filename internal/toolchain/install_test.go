package toolchain

import (
	"context"
	"strings"
	"testing"

	"github.com/celestezj/blog-bootstrap/internal/execx"
)

func TestInstallSuccess(t *testing.T) {
	fake := execx.NewFake()
	fake.LookPaths["winget"] = "winget"
	inst := &Installer{Runner: fake, GOOS: "windows", IsRoot: func() bool { return false }}
	if err := inst.Install(context.Background(), "winget", Git); err != nil {
		t.Fatal(err)
	}
	calls := fake.CallsMatching("winget")
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	joined := strings.Join(calls[0].Args, " ")
	if !strings.Contains(joined, "Git.Git") {
		t.Fatalf("args = %v", calls[0].Args)
	}
}

func TestInstallNoPermission(t *testing.T) {
	fake := execx.NewFake()
	fake.LookPaths["winget"] = "winget"
	fake.Handler = func(c execx.Call) execx.Result {
		return execx.Result{ExitCode: 1, Stderr: "Access is denied: requires elevation"}
	}
	inst := &Installer{Runner: fake, GOOS: "windows", IsRoot: func() bool { return false }}
	err := inst.Install(context.Background(), "winget", Node)
	if err == nil {
		t.Fatal("expected failure")
	}
	se, ok := err.(*SystemInstallError)
	if !ok {
		t.Fatalf("error type %T", err)
	}
	if se.Kind != KindPrivilege {
		t.Fatalf("kind = %s, want privilege", se.Kind)
	}
}

func TestManagerMissing(t *testing.T) {
	fake := execx.NewFake()
	inst := &Installer{Runner: fake, GOOS: "linux", IsRoot: func() bool { return false }}
	if _, ok := inst.Manager(context.Background()); ok {
		t.Fatal("no manager should be available")
	}
}

func TestInstallSudoWithoutPasswordIsHuman(t *testing.T) {
	fake := execx.NewFake()
	fake.LookPaths["apt-get"] = "apt-get"
	fake.LookPaths["sudo"] = "/usr/bin/sudo"
	fake.Handler = func(c execx.Call) execx.Result {
		return execx.Result{ExitCode: 1, Stderr: "sudo: a password is required"}
	}
	inst := &Installer{Runner: fake, GOOS: "linux", IsRoot: func() bool { return false }}
	err := inst.Install(context.Background(), "apt-get", Git)
	se, ok := err.(*SystemInstallError)
	if !ok || se.Kind != KindPrivilege {
		t.Fatalf("err = %#v", err)
	}
	if !strings.Contains(se.Command, "sudo apt-get") {
		t.Fatalf("human command = %q", se.Command)
	}
}

func TestInstallAsRoot(t *testing.T) {
	fake := execx.NewFake()
	inst := &Installer{Runner: fake, GOOS: "linux", IsRoot: func() bool { return true }}
	if err := inst.Install(context.Background(), "apt-get", Node); err != nil {
		t.Fatal(err)
	}
	calls := fake.CallsMatching("apt-get")
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	if !strings.Contains(strings.Join(calls[0].Args, " "), "nodejs npm") {
		t.Fatalf("args = %v", calls[0].Args)
	}
}
