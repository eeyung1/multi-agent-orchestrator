package agentprotocol

import (
	"reflect"
	"strings"
	"testing"
)

func testIdentity() Identity {
	return Identity{
		ProtocolVersion: Version1, AgentID: "agent:developer-1", SessionID: "session:run-1",
		Metadata:     Metadata{Provider: "codex", Runtime: "local-cli", RuntimeVersion: "example-version"},
		Capabilities: []Capability{"coding", "org.example/review:v2"}, SupportedRoles: []Role{RoleWorker},
	}
}

func TestIdentityAcceptsKnownAndFutureProviders(t *testing.T) {
	for _, provider := range []string{"codex", "claude", "gemini", "OpenCode", "Kimi", "Antigravity", "unknown-implementation-2040"} {
		t.Run(provider, func(t *testing.T) {
			id := testIdentity()
			id.Metadata.Provider = provider
			if err := id.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAgentAndSessionAreDistinctIdentities(t *testing.T) {
	id := testIdentity()
	next := id
	next.SessionID = "session:run-2"
	if err := next.Validate(); err != nil {
		t.Fatal(err)
	}
	if next.AgentID != id.AgentID || next.SessionID == id.SessionID {
		t.Fatal("new runtime session lost logical agent identity")
	}
	if reflect.TypeOf(id.AgentID) == reflect.TypeOf(id.SessionID) {
		t.Fatal("agent and session types were conflated")
	}
	// Namespaces are distinct; equality of the text must not imply enrollment,
	// ownership, or that AgentID can substitute for SessionID.
	next.AgentID = AgentID(next.SessionID)
	if err := next.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestExplicitSupportedRoles(t *testing.T) {
	for _, roles := range [][]Role{{RolePlanner}, {RoleWorker}, {RolePlanner, RoleWorker}} {
		id := testIdentity()
		id.SupportedRoles = roles
		if err := id.Validate(); err != nil {
			t.Fatalf("roles %v: %v", roles, err)
		}
	}
	for _, roles := range [][]Role{nil, {""}, {"planner"}, {"ORCHESTRATOR"}, {RoleWorker, RoleWorker}} {
		id := testIdentity()
		id.SupportedRoles = roles
		if err := id.Validate(); err == nil {
			t.Fatalf("accepted invalid/duplicate roles %v", roles)
		}
	}
}

func TestProviderMetadataCannotImplyRole(t *testing.T) {
	id := testIdentity()
	id.Metadata = Metadata{Provider: "PLANNER", Runtime: "ORCHESTRATOR"}
	if err := id.Validate(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(id.SupportedRoles, []Role{RoleWorker}) {
		t.Fatal("metadata changed supported roles")
	}
	id.SupportedRoles = nil
	if err := id.Validate(); err == nil {
		t.Fatal("provider metadata supplied an implicit role")
	}
}

func TestInvalidIdentityFailsClosed(t *testing.T) {
	cases := map[string]func(*Identity){
		"missing version":      func(id *Identity) { id.ProtocolVersion = "" },
		"unsupported version":  func(id *Identity) { id.ProtocolVersion = "orchestrator-agent/v2" },
		"state version":        func(id *Identity) { id.ProtocolVersion = "orchestrator/v1" },
		"missing agent":        func(id *Identity) { id.AgentID = "" },
		"missing session":      func(id *Identity) { id.SessionID = "" },
		"whitespace agent":     func(id *Identity) { id.AgentID = "agent with spaces" },
		"whitespace session":   func(id *Identity) { id.SessionID = " session" },
		"control session":      func(id *Identity) { id.SessionID = "session\n" },
		"oversized identity":   func(id *Identity) { id.AgentID = AgentID(strings.Repeat("a", 257)) },
		"invalid UTF8":         func(id *Identity) { id.AgentID = AgentID(string([]byte{0xff})) },
		"missing provider":     func(id *Identity) { id.Metadata.Provider = "" },
		"metadata controls":    func(id *Identity) { id.Metadata.Runtime = "bad\x00runtime" },
		"metadata whitespace":  func(id *Identity) { id.Metadata.ProviderVersion = " v1" },
		"empty capability set": func(id *Identity) { id.Capabilities = nil },
		"too many capabilities": func(id *Identity) {
			id.Capabilities = make([]Capability, 129)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			id := testIdentity()
			mutate(&id)
			if err := id.Validate(); err == nil {
				t.Fatal("accepted invalid identity")
			}
		})
	}
}

func TestCapabilityValidationAndDuplicatePolicy(t *testing.T) {
	for _, name := range []Capability{"coding", "Acme.Custom/planning:v37", "future-capability_2"} {
		if err := name.Validate(); err != nil {
			t.Fatalf("%q: %v", name, err)
		}
	}
	for _, name := range []Capability{"", " coding", "two words", "/coding", "code*", "coding\n", "能力", Capability(strings.Repeat("a", 129))} {
		if err := name.Validate(); err == nil {
			t.Fatalf("accepted malformed capability %q", name)
		}
	}
	id := testIdentity()
	id.Capabilities = []Capability{"coding", "coding"}
	before := append([]Capability(nil), id.Capabilities...)
	for i := 0; i < 2; i++ {
		if err := id.Validate(); err == nil || err.Error() != "duplicate capability" {
			t.Fatalf("duplicate policy is not deterministic: %v", err)
		}
	}
	if !reflect.DeepEqual(id.Capabilities, before) {
		t.Fatal("validation mutated caller data")
	}
	id.Capabilities = []Capability{"coding", "Coding"}
	if err := id.Validate(); err != nil {
		t.Fatalf("capability names are case sensitive: %v", err)
	}
}

func TestPhaseOneEnrollmentVocabulary(t *testing.T) {
	for _, s := range []EnrollmentState{EnrollmentRequested, EnrollmentAdmitted, EnrollmentRejected} {
		if err := s.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []EnrollmentState{"", "READY", "BUSY", "DISCONNECTED", "unknown"} {
		if err := s.Validate(); err == nil {
			t.Fatalf("accepted state outside Phase 1 admission vocabulary: %s", s)
		}
	}
}
