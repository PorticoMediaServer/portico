package playbackv1

import "errors"

// FieldError names the request field that was refused (the API error's path).
type FieldError struct{ Path string }

func (e *FieldError) Error() string { return "invalid field " + e.Path }

// Domain errors. The HTTP layer maps each to a registered API error code.
var (
	ErrNotFound            = errors.New("not found")
	ErrEnded               = errors.New("session ended")
	ErrRevision            = errors.New("revision mismatch")
	ErrRevisionRequired    = errors.New("revision required")
	ErrIdempotencyMismatch = errors.New("idempotency key reused with a different request")
	ErrKeyRequired         = errors.New("idempotency key required")
	ErrQueueEnded          = errors.New("queue ended")
	ErrUnsupportedSelector = errors.New("unsupported selector")
	// ErrQueueTooLarge is a selection past a segment's or a queue's key bound.
	ErrQueueTooLarge   = errors.New("queue too large")
	ErrNotControllable = errors.New("device not controllable")
	ErrStreamExists    = errors.New("stream exists")
	// ErrClientUpdateRequired refuses a playback from a client that does not
	// speak the observation protocol, before anything is created.
	ErrClientUpdateRequired = errors.New("client update required")
)

// PlaybackProtocol is the observation protocol a client must declare (the
// Portico-Playback header) to start or continue playback.
const PlaybackProtocol = 2

// StreamLimitDetail is observed account capacity, never a list of hidden titles.
type StreamLimitDetail struct {
	Scope   string `json:"scope"`
	Limit   int64  `json:"limit"`
	Playing int64  `json:"playing"`
}
