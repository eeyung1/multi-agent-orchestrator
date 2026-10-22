package agentprotocol

import (
	"errors"
	"testing"
)

func TestUnsupportedProtocolErrorsRemainClassifiable(t *testing.T) {
	e, _ := testEnvelope(Heartbeat)
	e.ProtocolVersion = "future/v99"
	if err := e.Validate(); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatal(err)
	}
	e.ProtocolVersion, e.Type = Version1, "unknown-future-type"
	if err := e.Validate(); !errors.Is(err, ErrUnsupportedMessageType) {
		t.Fatal(err)
	}
}
