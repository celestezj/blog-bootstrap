package repo

import "testing"

func TestParseValid(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		owner string
		repo  string
	}{
		{"ssh", "git@github.com:celestezj/blog-delta.git", "celestezj", "blog-delta"},
		{"ssh without suffix", "git@github.com:celestezj/blog-delta", "celestezj", "blog-delta"},
		{"https", "https://github.com/celestezj/hexo-theme-A4.git", "celestezj", "hexo-theme-A4"},
		{"https without suffix", "https://github.com/celestezj/celestezj.github.io", "celestezj", "celestezj.github.io"},
		{"single letter owner", "git@github.com:a/b.git", "a", "b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := Parse(tt.raw)
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", tt.raw, err)
			}
			if r.Owner != tt.owner || r.Name != tt.repo {
				t.Fatalf("Parse(%q) = %s/%s, want %s/%s", tt.raw, r.Owner, r.Name, tt.owner, tt.repo)
			}
		})
	}
}

func TestParseRejects(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		code string
	}{
		{"alias host", "git@celestezj:celestezj/blog-delta.git", "URL_HOST_UNSUPPORTED"},
		{"other host", "https://gitlab.com/celestezj/blog-delta.git", "URL_HOST_UNSUPPORTED"},
		{"http", "http://github.com/celestezj/blog-delta.git", "URL_INVALID"},
		{"credentials", "https://user:pass@github.com/celestezj/blog-delta.git", "URL_INVALID"},
		{"query", "https://github.com/celestezj/blog-delta.git?x=1", "URL_INVALID"},
		{"port", "https://github.com:8443/celestezj/blog-delta.git", "URL_HOST_UNSUPPORTED"},
		{"extra path", "https://github.com/celestezj/blog-delta/tree/main", "URL_INVALID"},
		{"missing repo", "git@github.com:celestezj.git", "URL_INVALID"},
		{"bad owner", "git@github.com:-bad/repo.git", "URL_INVALID"},
		{"empty", "", "URL_EMPTY"},
		{"file path", "/tmp/repo", "URL_INVALID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.raw)
			if err == nil {
				t.Fatalf("Parse(%q) succeeded, want error", tt.raw)
			}
			pe, ok := err.(*ParseError)
			if !ok {
				t.Fatalf("error type = %T, want *ParseError", err)
			}
			if pe.Code != tt.code {
				t.Fatalf("error code = %s, want %s", pe.Code, tt.code)
			}
		})
	}
}

func TestWithAlias(t *testing.T) {
	r, err := Parse("git@github.com:celestezj/blog-delta.git")
	if err != nil {
		t.Fatal(err)
	}
	got := r.WithAlias("celestezj")
	want := "git@celestezj:celestezj/blog-delta.git"
	if got != want {
		t.Fatalf("WithAlias = %q, want %q", got, want)
	}
}

func TestSameRepo(t *testing.T) {
	if !SameRepo("git@github.com:a/b.git", "https://github.com/a/b") {
		t.Fatal("expected same repo")
	}
	if SameRepo("git@github.com:a/b.git", "git@github.com:a/c.git") {
		t.Fatal("expected different repos")
	}
	if !SameRepo("git@celestezj:a/b.git", "git@github.com:a/b.git") {
		t.Fatal("alias-rewritten remote should match canonical URL")
	}
	if !SameRepo("git@celestezj:a/b.git", "https://github.com/a/b") {
		t.Fatal("alias SSH remote should match HTTPS URL")
	}
}
