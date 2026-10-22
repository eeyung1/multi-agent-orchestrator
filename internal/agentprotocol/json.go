package agentprotocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
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
	// encoding/json otherwise accepts duplicate fields with last-value-wins
	// semantics. Reject ambiguity, including case variants recognized by Go.
	check := json.NewDecoder(bytes.NewReader(data))
	if err := uniqueValue(check, 0); err != nil {
		return Envelope{}, fmt.Errorf("decode agent envelope: %w", err)
	}
	if _, err := check.Token(); err != io.EOF {
		return Envelope{}, errors.New("agent envelope contains trailing data")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var e Envelope
	if err := d.Decode(&e); err != nil {
		return Envelope{}, fmt.Errorf("decode agent envelope: %w", err)
	}
	if err := e.ValidateFrom(origin); err != nil {
		return Envelope{}, err
	}
	return e, nil
}

func uniqueValue(d *json.Decoder, depth int) error {
	if depth > 32 {
		return errors.New("agent envelope nesting exceeds limit")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	keys := map[string]bool{}
	for d.More() {
		if delim == '{' {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return errors.New("invalid JSON object key")
			}
			name = strings.ToLower(name)
			if keys[name] {
				return errors.New("duplicate JSON object key")
			}
			keys[name] = true
		}
		if err := uniqueValue(d, depth+1); err != nil {
			return err
		}
	}
	_, err = d.Token()
	return err
}
