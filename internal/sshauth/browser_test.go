package sshauth

import (
	"net/url"
	"testing"
)

func TestPrefillURLEncodesKey(t *testing.T) {
	key := "ssh-ed25519 AAAAC3Nza+/= comment with spaces"
	raw, err := PrefillURL("celestezj", key)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "github.com" || u.Path != "/settings/ssh/new" {
		t.Fatalf("url = %s", raw)
	}
	if got := u.Query().Get("key"); got != key {
		t.Fatalf("key round-trip = %q, want %q", got, key)
	}
	if got := u.Query().Get("title"); got != "blog-bootstrap celestezj" {
		t.Fatalf("title = %q", got)
	}
}

func TestValidateBrowserURL(t *testing.T) {
	bad := []string{
		"http://github.com/settings/ssh/new",
		"https://evil.example.com/x",
		"https://github.com:8443/x",
		"https://user:pass@github.com/x",
	}
	for _, raw := range bad {
		if err := ValidateBrowserURL(raw); err == nil {
			t.Errorf("ValidateBrowserURL(%q) should fail", raw)
		}
	}
	if err := ValidateBrowserURL("https://github.com/settings/ssh/new?key=x"); err != nil {
		t.Fatal(err)
	}
}
