package toolchain

import (
	"strings"
	"testing"
)

func TestParseVersion(t *testing.T) {
	tests := map[string]string{
		"git version 2.43.0.windows.1": "2.43.0",
		"v22.14.0":                     "22.14.0",
		"10.9.2":                       "10.9.2",
		"OpenSSH_for_Windows_9.5p1":    "9.5.0",
		"hexo: 7.3.0":                  "7.3.0",
	}
	for in, want := range tests {
		v, err := ParseVersion(in)
		if err != nil {
			t.Fatalf("ParseVersion(%q) error = %v", in, err)
		}
		if v.String() != want {
			t.Errorf("ParseVersion(%q) = %s, want %s", in, v, want)
		}
	}
}

func TestVersionRanges(t *testing.T) {
	low, _ := ParseVersion("2.10.0")
	ok, _ := ParseVersion("2.43.0")
	if GitSupported(low) {
		t.Error("git 2.10 should not be reused")
	}
	if !GitSupported(ok) {
		t.Error("git 2.43 should be reused")
	}
	n17, _ := ParseVersion("17.9.0")
	n22, _ := ParseVersion("22.14.0")
	if NodeSupported(n17) {
		t.Error("node 17 should be below the supported range")
	}
	if !NodeSupported(n22) {
		t.Error("node 22 should be supported")
	}
}

func TestMatchWant(t *testing.T) {
	v, _ := ParseVersion("22.14.0")
	if !MatchWant("22", v) {
		t.Error("22 should match 22.14.0")
	}
	if !MatchWant("22.14.0", v) {
		t.Error("full version should match")
	}
	if MatchWant("20", v) {
		t.Error("20 must not match 22.14.0")
	}
	if MatchWant("22.13", v) {
		t.Error("22.13 must not match 22.14.0")
	}
}

func TestNeedsPortableNode(t *testing.T) {
	system := &Tool{Version: Version{Major: 22, Minor: 14}}
	if NeedsPortableNode(system, "22") {
		t.Error("matching system node should be reused")
	}
	if !NeedsPortableNode(system, "20") {
		t.Error("mismatched system node should use portable node")
	}
	if !NeedsPortableNode(nil, "22") {
		t.Error("missing node needs portable")
	}
}

func TestResolveNodeDistVersion(t *testing.T) {
	if got, err := ResolveNodeDistVersion("22"); err != nil || got != "22.14.0" {
		t.Fatalf("22 -> %q, %v", got, err)
	}
	if got, err := ResolveNodeDistVersion("v20.11.1"); err != nil || got != "20.11.1" {
		t.Fatalf("exact -> %q, %v", got, err)
	}
	if _, err := ResolveNodeDistVersion("99"); err == nil {
		t.Fatal("unsupported major should error")
	}
}

func TestNodeSpec(t *testing.T) {
	spec, err := NodeSpec("windows", "amd64", "22", `C:\tools`)
	if err != nil {
		t.Fatal(err)
	}
	if want := "node-v22.14.0-win-x64.zip"; !strings.Contains(spec.URL, want) {
		t.Fatalf("URL = %s, want containing %s", spec.URL, want)
	}
	if spec.Archive != "zip" {
		t.Fatalf("archive = %s", spec.Archive)
	}
	if _, err := NodeSpec("plan9", "amd64", "22", "x"); err == nil {
		t.Fatal("unsupported OS should error")
	}
}

func TestNpmCacheKeyChanges(t *testing.T) {
	a := NpmCacheKey([]byte(`{"lockfileVersion":3}`), "22.14.0")
	b := NpmCacheKey([]byte(`{"lockfileVersion":3}`), "22.14.0")
	if a != b {
		t.Fatal("same input must give same key")
	}
	if a == NpmCacheKey([]byte(`{"lockfileVersion":3,"x":1}`), "22.14.0") {
		t.Fatal("lockfile change must change key")
	}
	if a == NpmCacheKey([]byte(`{"lockfileVersion":3}`), "20.18.1") {
		t.Fatal("node change must change key")
	}
}

func TestGitSpecWindowsOnly(t *testing.T) {
	if _, err := GitSpec("linux", "amd64", "/tmp"); err == nil {
		t.Fatal("portable git should be unavailable on linux")
	}
	spec, err := GitSpec("windows", "amd64", `C:\tools`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(spec.URL, "MinGit-2.47.1-64-bit.zip") {
		t.Fatalf("URL = %s", spec.URL)
	}
}
