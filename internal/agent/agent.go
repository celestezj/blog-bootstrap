// Package agent exposes the embedded AI installation guide. The guide must
// stay byte-identical to docs/agent-install.md; a test enforces it.
package agent

import _ "embed"

//go:embed guide.md
var guide string

// Guide returns the AI installation guide.
func Guide() string { return guide }
