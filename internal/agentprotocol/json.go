package agentprotocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/eeyung1/multi-agent-orchestrator/internal/strictjson"
)

const MaxEnvelopeBytes = 1 << 20

// Encode validates and serializes in struct field/list order. Validation never
// mutates or sorts the caller's identity/role/capability data.
func Encode(e Envelope) ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	if len(b) > MaxEnvelopeBytes {
		return nil, errors.New("agent envelope exceeds 1 MiB")
	}
	return b, nil
}

// Decode accepts one strict versioned message and checks its trusted direction.
// Future transports must establish origin outside the wire data. This function
// does not authenticate, enroll, dispatch, or accept a result authoritatively.
func Decode(data []byte, origin Origin) (Envelope, error) {
	if len(data) > MaxEnvelopeBytes || !utf8.Valid(data) {
		return Envelope{}, errors.New("oversized or invalid UTF-8 agent envelope")
	}
	var e Envelope
	if err := strictjson.Decode(data, &e); err != nil {
		return Envelope{}, fmt.Errorf("decode agent envelope: %w", err)
	}
	if err := e.ValidateFrom(origin); err != nil {
		return Envelope{}, err
	}
	return e, nil
}
