package main

import (
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentattachment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"time"
)

func inviteRequest(agent string, roles, capabilities []string, ttl time.Duration, replace bool) agentattachment.InviteRequest {
	q := agentattachment.InviteRequest{AgentID: agentprotocol.AgentID(agent), TTL: ttl, Replace: replace}
	for _, r := range roles {
		q.Roles = append(q.Roles, agentprotocol.Role(r))
	}
	for _, c := range capabilities {
		q.Capabilities = append(q.Capabilities, agentprotocol.Capability(c))
	}
	return q
}
