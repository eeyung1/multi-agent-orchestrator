package providers

import (
	"encoding/json"
	"fmt"
)

// Argument construction is code-owned. No prompt or project file can supply
// options here. All three adapters consume request/assignment JSON on stdin.
func PlannerArgs(id, answerPath string) ([]string, error) {
	switch id {
	case "codex":
		return []string{"exec", "--sandbox", "read-only", "-c", `approval_policy="never"`, "--skip-git-repo-check", "--ephemeral", "--color", "never", "--output-last-message", answerPath, "-"}, nil
	case "claude":
		return []string{"--print", "--output-format", "json", "--tools", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--setting-sources", "user", "--disable-slash-commands", "--no-session-persistence"}, nil
	case "gemini":
		return []string{"--prompt", "Return only the proposal JSON requested on stdin. Do not execute tools or change files.", "--approval-mode", "plan", "--output-format", "json", "--extensions", "none"}, nil
	default:
		return nil, fmt.Errorf("no built-in Planner adapter for %q", id)
	}
}

func WorkerArgs(id string) ([]string, error) {
	switch id {
	case "codex":
		return []string{"exec", "--sandbox", "workspace-write", "-c", `approval_policy="never"`, "--ephemeral", "--color", "never", "-"}, nil
	case "claude":
		return []string{"--print", "--output-format", "text", "--permission-mode", "acceptEdits", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--setting-sources", "user", "--disable-slash-commands", "--no-session-persistence"}, nil
	case "gemini":
		return []string{"--prompt", "Implement only the assigned task on stdin. Leave changes uncommitted for the Go controller.", "--approval-mode", "auto_edit", "--sandbox", "--output-format", "text", "--extensions", "none"}, nil
	default:
		return nil, fmt.Errorf("no built-in Worker adapter for %q", id)
	}
}

func ProposalText(id string, output []byte) ([]byte, error) {
	if id == "codex" {
		return output, nil
	}
	var envelope struct {
		Result   string          `json:"result"`
		Response string          `json:"response"`
		IsError  bool            `json:"is_error"`
		Error    json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(output, &envelope); err != nil {
		return nil, fmt.Errorf("provider %s returned an invalid response envelope", id)
	}
	if envelope.IsError || (len(envelope.Error) > 0 && string(envelope.Error) != "null") {
		return nil, fmt.Errorf("provider %s reported an error; check its authentication/permissions outside ORCHESTRATOR", id)
	}
	switch id {
	case "claude":
		if envelope.Result != "" {
			return []byte(envelope.Result), nil
		}
	case "gemini":
		if envelope.Response != "" {
			return []byte(envelope.Response), nil
		}
	}
	return nil, fmt.Errorf("provider %s returned no final proposal", id)
}
