// Package plan builds the change plan and its consent hash. The hash binds an
// agent's approval to concrete actions plus the observed environment.
package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/celestezj/blog-bootstrap/internal/config"
)

// Action is one concrete mutation the installer intends to perform.
type Action struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Detail string `json:"detail,omitempty"`
}

// StepPlan is one step's inspection result.
type StepPlan struct {
	ID        string            `json:"id"`
	Required  bool              `json:"required"`
	Satisfied bool              `json:"satisfied"`
	Actions   []Action          `json:"actions,omitempty"`
	Evidence  map[string]string `json:"evidence,omitempty"`
	Warning   string            `json:"warning,omitempty"`
}

// Plan is the user-facing and consent-bound change plan.
type Plan struct {
	SchemaVersion int               `json:"schema_version"`
	Owner         string            `json:"owner"`
	Alias         string            `json:"alias"`
	KeyPath       string            `json:"key_path"`
	SourceURL     string            `json:"source_url"`
	ThemeURL      string            `json:"theme_url"`
	DeployURL     string            `json:"deploy_url"`
	WorkspaceDir  string            `json:"workspace_dir"`
	Branch        string            `json:"branch"`
	NodeVersion   string            `json:"node_version"`
	Strategy      string            `json:"strategy"`
	ConfigDigest  string            `json:"config_digest"`
	Environment   map[string]string `json:"environment,omitempty"`
	Steps         []StepPlan        `json:"steps"`
	Hash          string            `json:"hash"`
}

// Build assembles a plan and computes its consent hash.
func Build(cfg *config.Resolved, configDigest string, env map[string]string, steps []StepPlan) (*Plan, error) {
	p := &Plan{
		SchemaVersion: 1,
		Owner:         cfg.Owner,
		Alias:         cfg.Alias,
		KeyPath:       cfg.KeyPath,
		SourceURL:     cfg.Source.SSH(),
		ThemeURL:      cfg.Theme.SSH(),
		DeployURL:     cfg.Deploy.SSH(),
		WorkspaceDir:  cfg.Dir,
		Branch:        cfg.Branch,
		NodeVersion:   cfg.NodeWant,
		Strategy:      cfg.Strategy,
		ConfigDigest:  configDigest,
		Environment:   env,
		Steps:         steps,
	}
	hash, err := p.computeHash()
	if err != nil {
		return nil, err
	}
	p.Hash = hash
	return p, nil
}

// computeHash covers every field that could change the actions taken: config
// identity, resolved paths, step inspections, and detected environment facts.
func (p *Plan) computeHash() (string, error) {
	fp := struct {
		SchemaVersion int               `json:"schema_version"`
		Owner         string            `json:"owner"`
		Alias         string            `json:"alias"`
		KeyPath       string            `json:"key_path"`
		SourceURL     string            `json:"source_url"`
		ThemeURL      string            `json:"theme_url"`
		DeployURL     string            `json:"deploy_url"`
		WorkspaceDir  string            `json:"workspace_dir"`
		Branch        string            `json:"branch"`
		NodeVersion   string            `json:"node_version"`
		Strategy      string            `json:"strategy"`
		ConfigDigest  string            `json:"config_digest"`
		Environment   map[string]string `json:"environment,omitempty"`
		Steps         []StepPlan        `json:"steps"`
	}{
		SchemaVersion: p.SchemaVersion,
		Owner:         p.Owner,
		Alias:         p.Alias,
		KeyPath:       p.KeyPath,
		SourceURL:     p.SourceURL,
		ThemeURL:      p.ThemeURL,
		DeployURL:     p.DeployURL,
		WorkspaceDir:  p.WorkspaceDir,
		Branch:        p.Branch,
		NodeVersion:   p.NodeVersion,
		Strategy:      p.Strategy,
		ConfigDigest:  p.ConfigDigest,
		Environment:   p.Environment,
		Steps:         p.Steps,
	}
	data, err := json.Marshal(fp)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// Digest returns the SHA256 of raw config bytes, used to invalidate consent
// when the configuration file changes.
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
