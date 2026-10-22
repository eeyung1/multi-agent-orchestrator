package state

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
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
	if after.Authority.Schema != 2 || after.Authority.Protocol != s.Authority.Protocol || after.Authority.Generation != s.Authority.Generation+1 || after.AgentRegistry != nil {
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
