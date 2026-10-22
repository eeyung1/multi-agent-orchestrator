package strictjson

import "testing"

func TestStructAmbiguityAndCaseSensitiveMapKeys(t *testing.T) {
	type record struct {
		Generation uint64 `json:"session_generation"`
	}
	type snapshot struct {
		Sessions map[string]record `json:"sessions"`
	}
	var got snapshot
	if err := Decode([]byte(`{"sessions":{"Run":{"session_generation":1},"run":{"session_generation":2}}}`), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Sessions) != 2 || got.Sessions["Run"].Generation != 1 || got.Sessions["run"].Generation != 2 {
		t.Fatal("map identities were folded")
	}
	for _, input := range []string{
		`{"sessions":{},"SESSIONS":{}}`,
		`{"sessions":{"run":{"session_generation":1,"SESSION_GENERATION":2}}}`,
		`{"sessions":{"run":{"session_generation":1},"run":{"session_generation":2}}}`,
		`{"sessions":{"run":{"session_generation":1,"authority":true}}}`,
	} {
		if err := Decode([]byte(input), &got); err == nil {
			t.Fatal("accepted ambiguous/unknown state fields")
		}
	}
}
