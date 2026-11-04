// Package repo parses and normalizes GitHub repository URLs.
package repo

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const GitHubHost = "github.com"

var (
	ownerRE = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)
	nameRE  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
)

// ParseError describes why a URL was rejected.
type ParseError struct {
	Code    string
	Message string
}

func (e *ParseError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

func newErr(code, format string, args ...interface{}) *ParseError {
	return &ParseError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Repo is a normalized GitHub repository.
type Repo struct {
	Owner  string
	Name   string
	Scheme string // ssh | https
	Raw    string
}

func (r Repo) String() string { return r.Owner + "/" + r.Name }

// SSH returns the canonical github.com SSH URL (with .git suffix).
func (r Repo) SSH() string {
	return "git@" + GitHubHost + ":" + r.Owner + "/" + r.Name + ".git"
}

// HTTPS returns the canonical HTTPS URL (with .git suffix).
func (r Repo) HTTPS() string {
	return "https://" + GitHubHost + "/" + r.Owner + "/" + r.Name + ".git"
}

// WithAlias rewrites github.com to the SSH host alias.
func (r Repo) WithAlias(alias string) string {
	return "git@" + alias + ":" + r.Owner + "/" + r.Name + ".git"
}

// Parse accepts only https://github.com/owner/repo.git and
// git@github.com:owner/repo.git. Custom hosts are rejected so that the owner
// can always be derived reliably.
func Parse(raw string) (Repo, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Repo{}, newErr("URL_EMPTY", "repository URL is empty")
	}
	if strings.HasPrefix(strings.ToLower(s), "git@") {
		return parseSCP(s)
	}
	if strings.HasPrefix(strings.ToLower(s), "https://") {
		return parseHTTPS(s)
	}
	return Repo{}, newErr("URL_INVALID", "unsupported repository URL %q: use git@github.com:owner/repo.git or https://github.com/owner/repo.git", raw)
}

func parseSCP(raw string) (Repo, error) {
	rest := raw[len("git@"):]
	host, path, ok := strings.Cut(rest, ":")
	if !ok {
		return Repo{}, newErr("URL_INVALID", "invalid SSH repository URL %q", raw)
	}
	if !strings.EqualFold(host, GitHubHost) {
		return Repo{}, newErr("URL_HOST_UNSUPPORTED", "repository host %q is not supported: use %s explicitly", host, GitHubHost)
	}
	r, err := parsePath(path, "ssh", raw)
	if err != nil {
		return Repo{}, err
	}
	return r, nil
}

func parseHTTPS(raw string) (Repo, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Repo{}, newErr("URL_INVALID", "invalid HTTPS repository URL %q", raw)
	}
	if u.User != nil {
		return Repo{}, newErr("URL_INVALID", "repository URL must not embed credentials")
	}
	if u.Port() != "" {
		return Repo{}, newErr("URL_HOST_UNSUPPORTED", "repository URL must not specify a port")
	}
	if !strings.EqualFold(u.Host, GitHubHost) {
		return Repo{}, newErr("URL_HOST_UNSUPPORTED", "repository host %q is not supported: use %s", u.Host, GitHubHost)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return Repo{}, newErr("URL_INVALID", "repository URL must not have a query or fragment")
	}
	return parsePath(strings.TrimPrefix(u.Path, "/"), "https", raw)
}

func parsePath(path, scheme, raw string) (Repo, error) {
	path = strings.TrimSuffix(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return Repo{}, newErr("URL_INVALID", "repository URL %q must be of the form owner/repo", raw)
	}
	owner, name := parts[0], parts[1]
	name = strings.TrimSuffix(name, ".git")
	if name == "" || name == "." || name == ".." || !nameRE.MatchString(name) {
		return Repo{}, newErr("URL_INVALID", "repository name %q is not valid", name)
	}
	if !ownerRE.MatchString(owner) {
		return Repo{}, newErr("URL_INVALID", "repository owner %q is not a valid GitHub account", owner)
	}
	return Repo{Owner: owner, Name: name, Scheme: scheme, Raw: raw}, nil
}

// SameRepo reports whether two URLs point at the same owner/repo. It accepts
// alias-rewritten remotes such as git@celestezj:owner/repo.git, which Parse
// deliberately rejects for configuration input.
func SameRepo(a, b string) bool {
	oa, na, okA := looseIdentity(a)
	ob, nb, okB := looseIdentity(b)
	if okA && okB {
		return strings.EqualFold(oa, ob) && strings.EqualFold(na, nb)
	}
	return normalizeLoose(a) == normalizeLoose(b)
}

func looseIdentity(raw string) (owner, name string, ok bool) {
	s := strings.TrimSpace(raw)
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	var path string
	lower := strings.ToLower(s)
	switch {
	case strings.HasPrefix(lower, "https://"), strings.HasPrefix(lower, "http://"):
		u, err := url.Parse(s)
		if err != nil {
			return "", "", false
		}
		path = strings.Trim(u.Path, "/")
	case strings.Contains(s, ":"):
		path = s[strings.Index(s, ":")+1:]
	default:
		return "", "", false
	}
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return "", "", false
	}
	owner, name = parts[len(parts)-2], parts[len(parts)-1]
	if owner == "" || name == "" {
		return "", "", false
	}
	return owner, name, true
}

func normalizeLoose(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimSuffix(s, "/")
	return strings.ToLower(s)
}
