package sshauth

import (
	"net/url"
	"strings"

	"github.com/celestezj/blog-bootstrap/internal/protocol"
)

// Browser opens URLs without a shell.
type Browser interface {
	Open(url string) error
}

// PrefillURL builds the GitHub "new SSH key" page with the public key encoded
// as a query parameter. Only https://github.com URLs are ever produced.
func PrefillURL(owner, publicKey string) (string, error) {
	key := strings.TrimSpace(publicKey)
	if key == "" {
		return "", protocol.Fatal("AUTH_KEY_EMPTY", "公钥为空，无法生成登记页面")
	}
	q := url.Values{}
	q.Set("title", "blog-bootstrap "+owner)
	q.Set("key", key)
	u := "https://github.com/settings/ssh/new?" + q.Encode()
	if err := ValidateBrowserURL(u); err != nil {
		return "", err
	}
	return u, nil
}

// ValidateBrowserURL enforces the GitHub-only browser boundary.
func ValidateBrowserURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return protocol.Fatal("AUTH_BROWSER_URL_INVALID", "无法解析浏览器地址: "+err.Error()).WithCause(err)
	}
	if u.Scheme != "https" {
		return protocol.Fatal("AUTH_BROWSER_URL_INVALID", "只允许通过 https 打开 GitHub 页面")
	}
	if !strings.EqualFold(u.Host, "github.com") || u.Port() != "" {
		return protocol.Fatal("AUTH_BROWSER_URL_INVALID", "只允许打开 github.com 页面，实际为 "+u.Host)
	}
	if u.User != nil {
		return protocol.Fatal("AUTH_BROWSER_URL_INVALID", "浏览器地址不允许包含凭据")
	}
	return nil
}

// OpenEnrollment opens the prefill page and returns its URL even when opening
// fails, so the caller can hand the link to the user.
func OpenEnrollment(b Browser, owner, publicKey string) (string, error) {
	u, err := PrefillURL(owner, publicKey)
	if err != nil {
		return "", err
	}
	if b != nil {
		_ = b.Open(u)
	}
	return u, nil
}
