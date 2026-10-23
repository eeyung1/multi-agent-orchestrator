package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/locking"
	"github.com/eeyung1/multi-agent-orchestrator/internal/strictjson"
)

const (
	SchemaVersion   = 4
	ProtocolVersion = "orchestrator/v1"
	stateFile       = "state.json"
	lockDir         = ".orchestrator-state-lock"
)

type Authority struct {
	RepositoryID string `json:"repository_id"`
	Protocol     string `json:"protocol"`
	Schema       int    `json:"schema"`
	Generation   uint64 `json:"generation"`
	Enrollment   string `json:"enrollment"`
	Provenance   string `json:"provenance,omitempty"`
	EnrolledUTC  string `json:"enrolled_utc"`
}

type Session struct {
	ID         string `json:"id"`
	Role       string `json:"role"`
	State      string `json:"state"`
	Generation uint64 `json:"generation"`
}

type Claim struct {
	Token      string `json:"token"`
	SessionID  string `json:"session_id"`
	Feature    string `json:"feature"`
	Scope      string `json:"scope"`
	Generation uint64 `json:"generation"`
}

type Objective struct {
	ID              string           `json:"id"`
	Description     string           `json:"description"`
	Status          string           `json:"status"`
	PlanDigest      string           `json:"plan_digest,omitempty"`
	PolicyDigest    string           `json:"policy_digest,omitempty"`
	ReconDigest     string           `json:"recon_digest,omitempty"`
	AuthorizedBy    string           `json:"authorized_by,omitempty"`
	BaseSHA         string           `json:"base_sha,omitempty"`
	PlannerEvidence *PlannerEvidence `json:"planner_evidence,omitempty"`
	PlannerProvider string           `json:"planner_provider,omitempty"`
	WorkerProviders []string         `json:"worker_providers,omitempty"`
}

type AssignmentHistory struct {
	WorkerDispatch       *WorkerDispatch `json:"worker_dispatch,omitempty"`
	Attempt              uint64          `json:"attempt"`
	AssignmentGeneration uint64          `json:"assignment_generation"`
	Token                string          `json:"token,omitempty"`
	SessionID            string          `json:"session_id,omitempty"`
	Branch               string          `json:"branch,omitempty"`
	Worktree             string          `json:"worktree,omitempty"`
	BaseSHA              string          `json:"base_sha,omitempty"`
	Blocker              string          `json:"blocker,omitempty"`
}

type Task struct {
	WorkerDispatch       *WorkerDispatch     `json:"worker_dispatch,omitempty"`
	ID                   string              `json:"id"`
	ObjectiveID          string              `json:"objective_id"`
	Feature              string              `json:"feature"`
	Scope                string              `json:"scope"`
	DependsOn            []string            `json:"depends_on,omitempty"`
	Priority             int                 `json:"priority,omitempty"`
	Status               string              `json:"status"`
	Attempt              uint64              `json:"attempt"`
	AssignmentGeneration uint64              `json:"assignment_generation"`
	Token                string              `json:"token,omitempty"`
	SessionID            string              `json:"session_id,omitempty"`
	ResultID             string              `json:"result_id,omitempty"`
	Description          string              `json:"description,omitempty"`
	AcceptanceCriteria   []string            `json:"acceptance_criteria,omitempty"`
	Validation           []string            `json:"validation,omitempty"`
	Capability           string              `json:"capability,omitempty"`
	Branch               string              `json:"branch,omitempty"`
	Worktree             string              `json:"worktree,omitempty"`
	BaseSHA              string              `json:"base_sha,omitempty"`
	Blocker              string              `json:"blocker,omitempty"`
	ValidatedHead        string              `json:"validated_head,omitempty"`
	ValidationEvidence   string              `json:"validation_evidence,omitempty"`
	AssignmentHistory    []AssignmentHistory `json:"assignment_history,omitempty"`
}

type ResultRecord struct {
	ID                   string   `json:"id"`
	ObjectiveID          string   `json:"objective_id"`
	TaskID               string   `json:"task_id"`
	SessionID            string   `json:"session_id"`
	AssignmentGeneration uint64   `json:"assignment_generation"`
	Branch               string   `json:"branch"`
	HeadSHA              string   `json:"head_sha"`
	Validation           string   `json:"validation"`
	Artifacts            []string `json:"artifacts,omitempty"`
	Blockers             []string `json:"blockers,omitempty"`
	Limitations          []string `json:"limitations,omitempty"`
	RecordedUTC          string   `json:"recorded_utc"`
}

type Review struct {
	ObjectiveID string   `json:"objective_id"`
	Status      string   `json:"status"`
	ResultIDs   []string `json:"result_ids"`
	Decision    string   `json:"decision,omitempty"`
	DecidedBy   string   `json:"decided_by,omitempty"`
	DecidedUTC  string   `json:"decided_utc,omitempty"`
}

type Event struct {
	ID         string `json:"id"`
	Actor      string `json:"actor"`
	Operation  string `json:"operation"`
	Generation uint64 `json:"generation"`
	UTC        string `json:"utc"`
}

type Snapshot struct {
	Authority     Authority               `json:"authority"`
	Sessions      map[string]Session      `json:"sessions"`
	Claims        map[string]Claim        `json:"claims"`
	Objectives    map[string]Objective    `json:"objectives"`
	Tasks         map[string]Task         `json:"tasks"`
	Results       map[string]ResultRecord `json:"results"`
	Reviews       map[string]Review       `json:"reviews"`
	Events        []Event                 `json:"events"`
	AgentRegistry *AgentRegistryState     `json:"agent_registry,omitempty"`
}

type Store struct {
	primary      string
	repositoryID string
	dir          string
	path         string
}

func Open(primary, repositoryID string) (*Store, error) {
	primary, err := filepath.Abs(primary)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(repositoryID) == "" {
		return nil, errors.New("repository identity is required")
	}
	dir := filepath.Join(primary, "multi-agent-orchestrator")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{primary: primary, repositoryID: repositoryID, dir: dir, path: filepath.Join(dir, stateFile)}
	if _, err := os.Stat(s.path); errors.Is(err, os.ErrNotExist) {
		legacy, detail, legacyErr := legacyOperationalState(dir)
		if legacyErr != nil {
			return nil, legacyErr
		}
		if legacy {
			return nil, fmt.Errorf("legacy operational state requires explicit migration before structured enrollment: %s", detail)
		}
		now := time.Now().UTC().Format(time.RFC3339)
		initial := Snapshot{
			Authority: Authority{RepositoryID: repositoryID, Protocol: ProtocolVersion, Schema: SchemaVersion, Generation: 1, Enrollment: "FOUNDATION", EnrolledUTC: now},
			Sessions:  map[string]Session{}, Claims: map[string]Claim{}, Objectives: map[string]Objective{}, Tasks: map[string]Task{}, Results: map[string]ResultRecord{}, Reviews: map[string]Review{}, Events: []Event{},
		}
		if err := s.withLock(func() error {
			if _, err := os.Stat(s.path); err == nil {
				return nil
			}
			return s.write(initial)
		}); err != nil {
			return nil, err
		}
	}
	snap, err := s.read(true)
	if err != nil {
		return nil, err
	}
	if snap.Authority.RepositoryID != repositoryID {
		return nil, fmt.Errorf("state belongs to different repository: %s", snap.Authority.RepositoryID)
	}
	if snap.Authority.Schema < SchemaVersion {
		if err := s.migrateLegacy(repositoryID); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) Read() (Snapshot, error) {
	return s.read(false)
}

func (s *Store) read(allowLegacy bool) (Snapshot, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return Snapshot{}, err
	}
	return s.decode(data, allowLegacy)
}

func (s *Store) decode(data []byte, allowLegacy bool) (Snapshot, error) {
	var snap Snapshot
	if err := strictjson.Decode(data, &snap); err != nil {
		return Snapshot{}, fmt.Errorf("decode orchestrator state: %w", err)
	}
	if snap.Sessions == nil || snap.Claims == nil {
		return Snapshot{}, errors.New("malformed orchestrator state: missing maps")
	}
	if s.repositoryID != "" && snap.Authority.RepositoryID != s.repositoryID {
		return Snapshot{}, errors.New("state belongs to different repository")
	}
	if snap.Authority.Protocol != ProtocolVersion || (snap.Authority.Schema != SchemaVersion && !(allowLegacy && (snap.Authority.Schema == 1 || snap.Authority.Schema == 2 || snap.Authority.Schema == 3))) {
		return Snapshot{}, errors.New("unsupported state schema/protocol")
	}
	if err := snap.validateAgentRegistry(); err != nil {
		return Snapshot{}, err
	}
	if snap.Objectives == nil {
		snap.Objectives = map[string]Objective{}
	}
	if snap.Tasks == nil {
		snap.Tasks = map[string]Task{}
	}
	if snap.Results == nil {
		snap.Results = map[string]ResultRecord{}
	}
	if snap.Reviews == nil {
		snap.Reviews = map[string]Review{}
	}
	return snap, nil
}

func (s *Store) Update(actor, operation string, fn func(*Snapshot) error) error {
	return s.UpdateIf(actor, operation, func(snap *Snapshot) (bool, error) {
		return true, fn(snap)
	})
}

// UpdateIf reads and optionally publishes under the existing state lock. False
// discards the callback's private snapshot without an event/revision/write.
func (s *Store) UpdateIf(actor, operation string, fn func(*Snapshot) (bool, error)) error {
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(operation) == "" {
		return errors.New("actor and operation are required")
	}
	return s.withLock(func() error {
		snap, err := s.Read()
		if err != nil {
			return err
		}
		before := snap.Authority.Generation
		changed, err := fn(&snap)
		if err != nil {
			return err
		}
		if !changed {
			return nil
		}
		if before == ^uint64(0) {
			return errors.New("authority generation exhausted")
		}
		snap.Authority.Generation = before + 1
		snap.Events = append(snap.Events, Event{
			ID:    fmt.Sprintf("%d-%s", snap.Authority.Generation, operation),
			Actor: actor, Operation: operation, Generation: snap.Authority.Generation,
			UTC: time.Now().UTC().Format(time.RFC3339),
		})
		return s.write(snap)
	})
}

func (s *Store) write(snap Snapshot) error {
	if s.repositoryID != "" && snap.Authority.RepositoryID != s.repositoryID {
		return errors.New("state belongs to different repository")
	}
	if snap.Authority.Schema != SchemaVersion || snap.Authority.Protocol != ProtocolVersion {
		return errors.New("unsupported state schema/protocol")
	}
	if err := snap.validateAgentRegistry(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.CreateTemp(s.dir, "state.json.tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	// A post-rename failure means publication may have occurred. Surface it;
	// callers must reread instead of assuming that a reported error rolled back.
	dir, err := os.Open(s.dir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (s *Store) migrateLegacy(repositoryID string) error {
	return s.withLock(func() error {
		snap, err := s.read(true)
		if err != nil {
			return err
		}
		if snap.Authority.RepositoryID != repositoryID {
			return errors.New("state belongs to different repository")
		}
		if snap.Authority.Schema == SchemaVersion {
			return nil
		}
		if snap.Authority.Generation == ^uint64(0) {
			return errors.New("authority generation exhausted")
		}
		snap.Authority.Schema = SchemaVersion
		snap.Authority.Generation++
		snap.Events = append(snap.Events, Event{ID: fmt.Sprintf("%d-state-schema-migration", snap.Authority.Generation),
			Actor: "ORCHESTRATOR", Operation: "state-schema-migration", Generation: snap.Authority.Generation,
			UTC: time.Now().UTC().Format(time.RFC3339)})
		return s.write(snap)
	})
}

func (s *Store) withLock(fn func() error) error {
	lock := filepath.Join(s.primary, lockDir)
	release, err := locking.Acquire(lock)
	if err != nil {
		return err
	}
	defer release()
	return fn()
}

func legacyOperationalState(dir string) (bool, string, error) {
	path := filepath.Join(dir, "agentmeeting.md")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}

	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		cells := strings.Split(trimmed, "|")
		if len(cells) < 3 {
			continue
		}

		values := make([]string, 0, len(cells)-2)
		for _, cell := range cells[1 : len(cells)-1] {
			values = append(values, strings.TrimSpace(cell))
		}
		if len(values) == 0 {
			continue
		}

		first := strings.ToLower(values[0])
		if first == "agent/session id" || first == "token" || strings.Trim(first, "-: ") == "" {
			continue
		}

		// Session rows have seven cells; claim rows have five cells. Fail closed
		// for either because both represent pre-structured operational ownership.
		if len(values) == 7 || len(values) == 5 {
			return true, "agentmeeting.md contains session/claim rows", nil
		}
	}
	return false, "", nil
}

func CanonicalScope(primary, scope string) (string, error) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return "", errors.New("scope is required")
	}
	if strings.ContainsAny(scope, "|\n\r\x00\\") {
		return "", errors.New("scope contains unsupported characters")
	}
	if filepath.IsAbs(scope) {
		return "", errors.New("scope must be repository-relative")
	}
	clean := filepath.Clean(scope)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("scope escapes repository")
	}
	if clean == "." {
		return ".", nil
	}
	joined := filepath.Join(primary, clean)
	// Resolve every existing ancestor, including symlinks to paths that do not
	// exist yet. Two lexical names must not describe different ownership scopes.
	for current := joined; current != primary && current != filepath.Dir(current); current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("scope traverses a symlink")
		}
	}
	rel, err := filepath.Rel(primary, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("scope escapes repository")
	}
	return filepath.ToSlash(rel), nil
}
