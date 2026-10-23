package state

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
)

func writeFixture(t *testing.T, path string, snapshot Snapshot) []byte {
	t.Helper()
	b, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestV1MigrationPreservesOwnershipAndDoesNotEnrollAgents(t *testing.T) {
	d := t.TempDir()
	store, err := Open(d, "repo:test")
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	s.Authority.Schema = 1
	s.Sessions["legacy-session"] = Session{ID: "legacy-session", Role: "coding", State: "ACTIVE", Generation: 7}
	s.Claims["claim"] = Claim{Token: "claim", SessionID: "legacy-session", Feature: "feature", Scope: "feature", Generation: 8}
	s.Tasks["objective::task"] = Task{ID: "task", ObjectiveID: "objective", Status: "ASSIGNED", AssignmentGeneration: 9, SessionID: "legacy-session", Token: "claim", Branch: "feature", Worktree: "preserved"}
	writeFixture(t, store.path, s)
	if _, err := store.Read(); err == nil {
		t.Fatal("already-open handle accepted a downgraded schema")
	}
	reopened, err := Open(d, "repo:test")
	if err != nil {
		t.Fatal(err)
	}
	after, err := reopened.Read()
	if err != nil {
		t.Fatal(err)
	}
	if after.Authority.Schema != SchemaVersion || after.Authority.Protocol != s.Authority.Protocol || after.Authority.Generation != s.Authority.Generation+1 || after.AgentRegistry != nil {
		t.Fatal("incorrect migration/enrollment")
	}
	if !reflect.DeepEqual(s.Sessions, after.Sessions) || !reflect.DeepEqual(s.Claims, after.Claims) || !reflect.DeepEqual(s.Tasks, after.Tasks) {
		t.Fatal("migration changed ownership")
	}
	if len(after.Events) != len(s.Events)+1 || after.Events[len(after.Events)-1].Operation != "state-schema-migration" {
		t.Fatal("migration was not audited")
	}
	if _, err := Open(d, "repo:test"); err != nil {
		t.Fatal(err)
	}
	again, err := reopened.Read()
	if err != nil || !reflect.DeepEqual(after, again) {
		t.Fatal("migration repeated on reopen")
	}
	info, err := os.Stat(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("state permissions are not private")
	}
}

func TestConcurrentV1MigrationRunsOnce(t *testing.T) {
	d := t.TempDir()
	store, err := Open(d, "repo:test")
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	s.Authority.Schema = 1
	writeFixture(t, store.path, s)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; _, err := Open(d, "repo:test"); errs <- err }()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	after, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	if after.Authority.Generation != s.Authority.Generation+1 || len(after.Events) != len(s.Events)+1 {
		t.Fatal("concurrent migration advanced authority more than once")
	}
}

func TestV2MigrationPreservesRegistryAndAddsNoPlanner(t *testing.T) {
	store, err := Open(t.TempDir(), "repo:test")
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	s.Authority.Schema = 2
	// Empty, valid Phase 2 registry is preserved, never silently reconstructed.
	s.AgentRegistry = &AgentRegistryState{StaleAfterNS: time.Minute, ObservedAt: time.Now().UTC(), Sessions: map[agentprotocol.SessionID]AgentSession{}}
	writeFixture(t, store.path, s)
	reopened, err := Open(store.primary, "repo:test")
	if err != nil {
		t.Fatal(err)
	}
	after, err := reopened.Read()
	if err != nil {
		t.Fatal(err)
	}
	if after.Authority.Schema != SchemaVersion || !reflect.DeepEqual(after.AgentRegistry, s.AgentRegistry) || len(after.AgentRegistry.Planners) != 0 {
		t.Fatal("migration changed enrollment or selected Planner")
	}
	if after.Authority.Generation != s.Authority.Generation+1 {
		t.Fatal("migration not audited once")
	}
}

func TestMigrationRejectsUnknownOrAmbiguousDataWithoutOverwrite(t *testing.T) {
	for _, change := range []func(*Snapshot){
		func(s *Snapshot) { s.Authority.Schema = 99 },
		func(s *Snapshot) { s.Authority.Schema = 1; s.Authority.Protocol = "unknown" },
		func(s *Snapshot) { s.Authority.Schema = 1; s.Authority.RepositoryID = "different" },
		func(s *Snapshot) { s.Authority.Schema = 1; s.AgentRegistry = &AgentRegistryState{} },
	} {
		d := t.TempDir()
		store, err := Open(d, "repo:test")
		if err != nil {
			t.Fatal(err)
		}
		s, err := store.Read()
		if err != nil {
			t.Fatal(err)
		}
		change(&s)
		before := writeFixture(t, store.path, s)
		if _, err := Open(d, "repo:test"); err == nil {
			t.Fatal("invalid schema/registry migrated")
		}
		after, err := os.ReadFile(store.path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("invalid state overwritten")
		}
	}
	d := t.TempDir()
	store, err := Open(d, "repo:test")
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	s.Authority.Schema = 1
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data[:len(data)-1], []byte(`,"future_authority":true}`)...)
	if err := os.WriteFile(store.path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(d, "repo:test"); err == nil {
		t.Fatal("migration discarded unknown fields")
	}
}

func TestConditionalUpdateDoesNotWriteOrAdvanceAuthority(t *testing.T) {
	store, err := Open(t.TempDir(), "repo:test")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateIf("test", "read-only", func(s *Snapshot) (bool, error) { s.Sessions["discarded"] = Session{ID: "discarded"}; return false, nil }); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(store.path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("conditional read changed durable authority")
	}
}

func TestPublicationFailureRetainsPriorState(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires directory write permission enforcement")
	}
	d := t.TempDir()
	store, err := Open(d, "repo:test")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(store.path), 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Dir(store.path), 0755)
	err = store.Update("test", "write-fails", func(s *Snapshot) error { s.Sessions["unpublished"] = Session{ID: "unpublished"}; return nil })
	if err == nil {
		t.Fatal("publication failure reported success")
	}
	after, err := os.ReadFile(store.path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("publication failure changed state/generation")
	}
}

func TestV3MigrationPreservesPlannerAndTaskIdentity(t *testing.T) {
	store, err := Open(t.TempDir(), "repo:test")
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	before.Authority.Schema = 3
	now := time.Now().UTC()
	digest := strings.Repeat("a", 64)
	identity := agentprotocol.Identity{ProtocolVersion: agentprotocol.Version3, AgentID: "planning-agent", SessionID: "planner-enrolled", Metadata: agentprotocol.Metadata{Provider: "unknown-runtime"}, Capabilities: []agentprotocol.Capability{"planning"}, SupportedRoles: []agentprotocol.Role{agentprotocol.RolePlanner}}
	binding := PlannerBinding{RunID: "run", BindingID: "binding", AgentID: identity.AgentID, SessionID: identity.SessionID, Generation: 1, Decision: "human selection", Reason: "selected", SelectedAt: now, RequestID: "request", RequestStatus: "COMPLETE", ProposalDigest: digest, AdoptedObjective: "o"}
	before.AgentRegistry = &AgentRegistryState{StaleAfterNS: time.Minute, ObservedAt: now, LastGeneration: 1, Sessions: map[agentprotocol.SessionID]AgentSession{identity.SessionID: {Identity: identity, Generation: 1, AssignedRole: agentprotocol.RolePlanner, RoleDecision: "human selection", Status: AgentReady, EnrolledAt: now, LastSeenAt: now, StateChangedAt: now, AdmissionDecision: "trusted admission", StateReason: "selected"}}, Planners: map[string]PlannerBinding{"run": binding}}
	before.Objectives["o"] = Objective{ID: "o", PlannerEvidence: &PlannerEvidence{Correlation: agentprotocol.PlannerCorrelation{RunID: "run", BindingID: "binding", RequestID: "request"}, ProposalDigest: digest}}
	before.Tasks["o::t"] = Task{ID: "t", ObjectiveID: "o", Status: "ASSIGNED", Attempt: 2, AssignmentGeneration: 17, Token: "private-claim", SessionID: "operational-session", Branch: "agent/t", Worktree: "preserved"}
	writeFixture(t, store.path, before)
	if _, err := store.Read(); err == nil {
		t.Fatal("old schema accepted without explicit Open migration")
	}
	reopened, err := Open(store.primary, "repo:test")
	if err != nil {
		t.Fatal(err)
	}
	after, err := reopened.Read()
	if err != nil {
		t.Fatal(err)
	}
	if after.Authority.Schema != 4 || !reflect.DeepEqual(before.Tasks, after.Tasks) || !reflect.DeepEqual(before.AgentRegistry, after.AgentRegistry) || !reflect.DeepEqual(before.Objectives, after.Objectives) || after.Tasks["o::t"].WorkerDispatch != nil {
		t.Fatal("migration changed ownership or invented dispatch")
	}
}
