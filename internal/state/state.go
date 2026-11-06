// Package state persists the resumable installation cache and the receipt.
// The state file is a cache only; it is never evidence of success.
package state

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/celestezj/blog-bootstrap/internal/atomicfile"
)

// Status is the lifecycle state of one step.
type Status string

const (
	StatusPending  Status = "pending"
	StatusApplied  Status = "applied"
	StatusVerified Status = "verified"
	StatusFailed   Status = "failed"
	StatusInvalid  Status = "invalid"
	StatusSkipped  Status = "skipped"
)

// StepState is the cached state of one step.
type StepState struct {
	Status    Status            `json:"status"`
	UpdatedAt time.Time         `json:"updated_at"`
	Evidence  map[string]string `json:"evidence,omitempty"`
	ErrorCode string            `json:"error_code,omitempty"`
	Message   string            `json:"message,omitempty"`
}

// Paths records resolved absolute tool paths for subprocess use.
type Paths struct {
	Git   string `json:"git,omitempty"`
	SSH   string `json:"ssh,omitempty"`
	Key   string `json:"key,omitempty"`
	Node  string `json:"node,omitempty"`
	Npm   string `json:"npm,omitempty"`
	Hexo  string `json:"hexo,omitempty"`
	Theme string `json:"theme,omitempty"`
}

// Receipt is written only after every required strict probe passes.
type Receipt struct {
	SchemaVersion        int               `json:"schema_version"`
	RunID                string            `json:"run_id"`
	PlanHash             string            `json:"plan_hash"`
	Owner                string            `json:"owner"`
	Alias                string            `json:"alias"`
	Endpoint             string            `json:"endpoint,omitempty"`
	PublicKeyFingerprint string            `json:"public_key_fingerprint,omitempty"`
	WorkspaceDir         string            `json:"workspace_dir"`
	VerifiedAt           time.Time         `json:"verified_at"`
	Tools                map[string]string `json:"tools,omitempty"`
	Steps                []ReceiptStep     `json:"steps"`
}
type ReceiptStep struct {
	ID       string            `json:"id"`
	Evidence map[string]string `json:"evidence,omitempty"`
}

// State is the on-disk document.
type State struct {
	SchemaVersion  int                  `json:"schema_version"`
	RunID          string               `json:"run_id"`
	CreatedAt      time.Time            `json:"created_at"`
	UpdatedAt      time.Time            `json:"updated_at"`
	PlanHash       string               `json:"plan_hash,omitempty"`
	Endpoint       string               `json:"endpoint,omitempty"`
	EffectiveAlias string               `json:"effective_alias,omitempty"`
	NPMCacheKey    string               `json:"npm_cache_key,omitempty"`
	Paths          Paths                `json:"paths"`
	Steps          map[string]StepState `json:"steps"`
	Receipt        *Receipt             `json:"receipt,omitempty"`
	Changes        Changes              `json:"changes"`
}

// Changes is the human-rollback record surfaced as changes.json.
type Changes struct {
	SystemPackages []PackageChange `json:"system_packages,omitempty"`
	PortableTools  []ToolChange    `json:"portable_tools,omitempty"`
	SSHBlocks      []SSHChange     `json:"ssh_blocks,omitempty"`
	Backups        []string        `json:"backups,omitempty"`
	UserPath       []PathChange    `json:"user_path,omitempty"`
}
type PackageChange struct {
	Manager string `json:"manager"`
	Package string `json:"package"`
	Action  string `json:"action"`
}
type ToolChange struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Path    string `json:"path"`
}
type SSHChange struct {
	File  string `json:"file"`
	Alias string `json:"alias"`
	Block string `json:"block"`
}
type PathChange struct {
	Scope   string   `json:"scope"`
	Key     string   `json:"key"`
	Before  string   `json:"before"`
	After   string   `json:"after"`
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
}

// Store serializes access to the state file and writes it atomically.
type Store struct {
	path string
	mu   sync.Mutex
	st   State
	now  func() time.Time
}

const SchemaVersion = 1

// New creates an in-memory store that writes to path on Save.
func New(path string, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	t := now()
	return &Store{
		path: path,
		now:  now,
		st: State{
			SchemaVersion: SchemaVersion,
			RunID:         newRunID(),
			CreatedAt:     t,
			UpdatedAt:     t,
			Steps:         map[string]StepState{},
		},
	}
}

// Open loads an existing state file or creates a new one. A corrupt file is an
// error: silently discarding it would hide damaged evidence.
func Open(path string, now func() time.Time) (*Store, error) {
	s := New(path, now)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, err
	}
	st := State{}
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	if st.SchemaVersion != SchemaVersion {
		return nil, errors.New("unsupported state schema version")
	}
	if st.Steps == nil {
		st.Steps = map[string]StepState{}
	}
	s.st = st
	return s, nil
}

// Path returns the file backing the store.
func (s *Store) Path() string { return s.path }

// Snapshot returns a deep copy of the current state.
func (s *Store) Snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return clone(s.st)
}

// Update mutates the state under lock and persists it atomically.
func (s *Store) Update(fn func(*State)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.st)
	s.st.UpdatedAt = s.now()
	return s.saveLocked()
}

// SetStep records one step state and clears the receipt, because any mutation
// invalidates earlier success evidence until strict verification runs again.
func (s *Store) SetStep(id string, st StepState) error {
	return s.Update(func(stt *State) {
		stt.Steps[id] = st
		stt.Receipt = nil
	})
}

// InvalidateFrom marks id and every step in order at or after id as invalid.
func (s *Store) InvalidateFrom(order []string, id string) error {
	return s.Update(func(stt *State) {
		hit := false
		for _, stepID := range order {
			if stepID == id {
				hit = true
			}
			if hit {
				stt.Steps[stepID] = StepState{Status: StatusInvalid, UpdatedAt: s.now()}
			}
		}
		stt.Receipt = nil
	})
}

// FailStep records a failed step and invalidates every downstream step.
func (s *Store) FailStep(order []string, id string, st StepState) error {
	return s.Update(func(stt *State) {
		stt.Steps[id] = st
		after := false
		for _, stepID := range order {
			if stepID == id {
				after = true
				continue
			}
			if after {
				stt.Steps[stepID] = StepState{Status: StatusInvalid, UpdatedAt: s.now()}
			}
		}
		stt.Receipt = nil
	})
}

// SetPlanHash stores the hash of the plan the user consented to.
func (s *Store) SetPlanHash(hash string) error {
	return s.Update(func(stt *State) { stt.PlanHash = hash })
}

// SetPaths merges resolved tool paths.
func (s *Store) SetPaths(update func(*Paths)) error {
	return s.Update(func(stt *State) { update(&stt.Paths) })
}

// SetEndpoint records the SSH endpoint that succeeded.
func (s *Store) SetEndpoint(endpoint string) error {
	return s.Update(func(stt *State) { stt.Endpoint = endpoint })
}

// SetEffectiveAlias records the alias actually used in SSH config, which may
// differ from the configured alias after conflict resolution.
func (s *Store) SetEffectiveAlias(alias string) error {
	return s.Update(func(stt *State) { stt.EffectiveAlias = alias })
}

// SetNPMCacheKey records the lockfile+node cache key.
func (s *Store) SetNPMCacheKey(key string) error {
	return s.Update(func(stt *State) { stt.NPMCacheKey = key })
}

// AddChange appends a rollback record.
func (s *Store) AddChange(fn func(*Changes)) error {
	return s.Update(func(stt *State) { fn(&stt.Changes) })
}

// WriteReceipt stores the receipt and persists it.
func (s *Store) WriteReceipt(r *Receipt) error {
	return s.Update(func(stt *State) {
		r.SchemaVersion = SchemaVersion
		r.RunID = stt.RunID
		stt.Receipt = r
	})
}

// StepOrder returns step ids sorted for deterministic reporting.
func (s *Store) StepOrder() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.st.Steps))
	for id := range s.st.Steps {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.st, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(s.path, append(data, '\n'), 0o600)
}

func clone(st State) State {
	data, _ := json.Marshal(st)
	out := State{}
	_ = json.Unmarshal(data, &out)
	return out
}

func newRunID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().String()))
	}
	return hex.EncodeToString(b)
}
