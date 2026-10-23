package state

import (
	"errors"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
)

type AgentRef struct {
	AgentID    agentprotocol.AgentID   `json:"agent_id"`
	SessionID  agentprotocol.SessionID `json:"session_id"`
	Generation AgentSessionGeneration  `json:"session_generation"`
}

func (ref AgentRef) Validate() error {
	if err := ref.AgentID.Validate(); err != nil {
		return err
	}
	if err := ref.SessionID.Validate(); err != nil {
		return err
	}
	if ref.Generation == 0 {
		return errors.New("registry session generation is required")
	}
	return nil
}
