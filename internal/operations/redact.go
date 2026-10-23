package operations

import (
	"bytes"
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

var credentialText = regexp.MustCompile(`(?i)\b(api[_-]?key|password|secret|token|credential)\b\s*[:=]\s*(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
var authorizationText = regexp.MustCompile(`(?i)\b(?:authorization\s*[:=]\s*)?(?:bearer|basic)\s+[^\s,;]+`)
var claimText = regexp.MustCompile(`claim-[0-9a-f]{32}`)
var privateKeyText = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)

// Public types are an allowlist, never aliases of state.Task/Result/Config.
// Redact known private claims even if echoed into otherwise public free text.
// Raw validator output, environment/config values, and invitation files are not
// read or projected. This is deliberately not a universal free-text DLP system.
func redact(view Snapshot, s state.Snapshot, extra []string) (Snapshot, error) {
	secrets := map[string]bool{}
	add := func(v string) {
		if v != "" && v != "-" && v != "structured-state" {
			secrets[v] = true
		}
	}
	for _, t := range s.Tasks {
		add(t.Token)
		for _, h := range t.AssignmentHistory {
			add(h.Token)
		}
	}
	for _, c := range s.Claims {
		add(c.Token)
	}
	for _, v := range extra {
		add(v)
	}
	keys := make([]string, 0, len(secrets))
	for k := range secrets {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) == len(keys[j]) {
			return keys[i] < keys[j]
		}
		return len(keys[i]) > len(keys[j])
	})
	pairs := []string{}
	for _, k := range keys {
		pairs = append(pairs, k, "[REDACTED]")
	}
	replace := strings.NewReplacer(pairs...)
	b, err := json.Marshal(view)
	if err != nil {
		return Snapshot{}, ErrAuthority
	}
	var tree any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if d.Decode(&tree) != nil {
		return Snapshot{}, ErrAuthority
	}
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case string:
			x = replace.Replace(x)
			x = privateKeyText.ReplaceAllString(x, "[REDACTED]")
			x = authorizationText.ReplaceAllString(x, "authorization [REDACTED]")
			x = credentialText.ReplaceAllString(x, "credential=[REDACTED]")
			return claimText.ReplaceAllString(x, "[REDACTED]")
		case []any:
			for i := range x {
				x[i] = walk(x[i])
			}
		case map[string]any:
			for k, v := range x {
				x[k] = walk(v)
			}
		}
		return v
	}
	b, err = json.Marshal(walk(tree))
	if err != nil {
		return Snapshot{}, ErrAuthority
	}
	var clean Snapshot
	if json.Unmarshal(b, &clean) != nil {
		return Snapshot{}, ErrAuthority
	}
	return clean, nil
}
