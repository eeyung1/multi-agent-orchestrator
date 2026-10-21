package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	SchemaVersion   = 1
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

type Event struct {
	ID         string `json:"id"`
	Actor      string `json:"actor"`
	Operation  string `json:"operation"`
	Generation uint64 `json:"generation"`
	UTC        string `json:"utc"`
}

type Snapshot struct {
	Authority Authority          `json:"authority"`
	Sessions  map[string]Session `json:"sessions"`
	Claims    map[string]Claim   `json:"claims"`
	Events    []Event            `json:"events"`
}

type Store struct {
	primary string
	dir     string
	path    string
}

func Open(primary, repositoryID string) (*Store, error) {
	primary, err := filepath.Abs(primary)
	if err != nil { return nil, err }
	if strings.TrimSpace(repositoryID) == "" { return nil, errors.New("repository identity is required") }
	dir := filepath.Join(primary, "multi-agent-orchestrator")
	if err := os.MkdirAll(dir, 0o755); err != nil { return nil, err }
	s := &Store{primary: primary, dir: dir, path: filepath.Join(dir, stateFile)}
	if _, err := os.Stat(s.path); errors.Is(err, os.ErrNotExist) {
		legacy, detail, legacyErr := legacyOperationalState(dir)
		if legacyErr != nil { return nil, legacyErr }
		if legacy {
			return nil, fmt.Errorf("legacy operational state requires explicit migration before structured enrollment: %s", detail)
		}
		now := time.Now().UTC().Format(time.RFC3339)
		initial := Snapshot{
			Authority: Authority{RepositoryID: repositoryID, Protocol: ProtocolVersion, Schema: SchemaVersion, Generation: 1, Enrollment: "FOUNDATION", EnrolledUTC: now},
			Sessions: map[string]Session{}, Claims: map[string]Claim{}, Events: []Event{},
		}
		if err := s.withLock(func() error {
			if _, err := os.Stat(s.path); err == nil { return nil }
			return s.write(initial)
		}); err != nil { return nil, err }
	}
	snap, err := s.Read()
	if err != nil { return nil, err }
	if snap.Authority.RepositoryID != repositoryID {
		return nil, fmt.Errorf("state belongs to different repository: %s", snap.Authority.RepositoryID)
	}
	if snap.Authority.Schema != SchemaVersion || snap.Authority.Protocol != ProtocolVersion {
		return nil, fmt.Errorf("unsupported state schema/protocol: %d/%s", snap.Authority.Schema, snap.Authority.Protocol)
	}
	return s, nil
}

func (s *Store) Read() (Snapshot, error) {
	data, err := os.ReadFile(s.path)
	if err != nil { return Snapshot{}, err }
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil { return Snapshot{}, fmt.Errorf("decode orchestrator state: %w", err) }
	if snap.Sessions == nil || snap.Claims == nil { return Snapshot{}, errors.New("malformed orchestrator state: missing maps") }
	return snap, nil
}

func (s *Store) Update(actor, operation string, fn func(*Snapshot) error) error {
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(operation) == "" { return errors.New("actor and operation are required") }
	return s.withLock(func() error {
		snap, err := s.Read()
		if err != nil { return err }
		before := snap.Authority.Generation
		if err := fn(&snap); err != nil { return err }
		snap.Authority.Generation = before + 1
		snap.Events = append(snap.Events, Event{
			ID: fmt.Sprintf("%d-%s", snap.Authority.Generation, operation),
			Actor: actor, Operation: operation, Generation: snap.Authority.Generation,
			UTC: time.Now().UTC().Format(time.RFC3339),
		})
		return s.write(snap)
	})
}

func (s *Store) write(snap Snapshot) error {
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil { return err }
	data = append(data, '\n')
	f, err := os.CreateTemp(s.dir, "state.json.tmp-*")
	if err != nil { return err }
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil { f.Close(); return err }
	if _, err := f.Write(data); err != nil { f.Close(); return err }
	if err := f.Sync(); err != nil { f.Close(); return err }
	if err := f.Close(); err != nil { return err }
	return os.Rename(tmp, s.path)
}

func (s *Store) withLock(fn func() error) error {
	lock := filepath.Join(s.primary, lockDir)
	if err := os.Mkdir(lock, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) { return fmt.Errorf("structured state is locked: %s", lock) }
		return err
	}
	defer os.Remove(lock)
	return fn()
}

func legacyOperationalState(dir string) (bool, string, error) {
	path := filepath.Join(dir, "agentmeeting.md")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) { return false, "", nil }
	if err != nil { return false, "", err }

	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") { continue }
		cells := strings.Split(trimmed, "|")
		if len(cells) < 3 { continue }

		values := make([]string, 0, len(cells)-2)
		for _, cell := range cells[1:len(cells)-1] {
			values = append(values, strings.TrimSpace(cell))
		}
		if len(values) == 0 { continue }

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
	if scope == "" { return "", errors.New("scope is required") }
	if filepath.IsAbs(scope) { return "", errors.New("scope must be repository-relative") }
	clean := filepath.Clean(scope)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("scope escapes repository")
	}
	if clean == "." { return ".", nil }
	joined := filepath.Join(primary, clean)
	rel, err := filepath.Rel(primary, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("scope escapes repository")
	}
	return filepath.ToSlash(rel), nil
}
