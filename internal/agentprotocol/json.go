package agentprotocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	// V2 fields must remain unknown on the V1 wire, even when null/zero values
	// would disappear into Go zero values. The strict decode above already
	// rejects duplicates/unknown fields for the shared envelope model.
	if e.ProtocolVersion == Version1 {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return Envelope{}, errors.New("invalid v1 envelope")
		}
		for name, value := range fields {
			if strings.EqualFold(name, "invitation") || strings.EqualFold(name, "acknowledgment") {
				return Envelope{}, errors.New("v2 fields are not permitted on v1")
			}
			if strings.EqualFold(name, "enrollment") {
				var decision map[string]json.RawMessage
				if err := json.Unmarshal(value, &decision); err != nil {
					return Envelope{}, errors.New("invalid v1 enrollment decision")
				}
				for key := range decision {
					if strings.EqualFold(key, "session_generation") {
						return Envelope{}, errors.New("v2 fields are not permitted on v1")
					}
				}
			}
		}
	}
	if err := e.ValidateFrom(origin); err != nil {
		return Envelope{}, err
	}
	return e, nil
}
