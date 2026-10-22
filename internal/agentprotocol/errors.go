package agentprotocol

import "errors"

// These shape errors allow callers to distinguish protocol incompatibility
// without parsing error text or exposing message contents.
var (
	ErrUnsupportedVersion     = errors.New("unsupported agent protocol version")
	ErrUnsupportedMessageType = errors.New("unsupported agent message type")
)
