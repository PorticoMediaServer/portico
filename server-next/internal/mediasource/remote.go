package mediasource

import (
	"context"
	"errors"
	"io"
)

// Snapshot is one file or folder as a source reported it.
type Snapshot struct {
	ErrorCode string `json:"errorCode,omitempty"`
	// Opaque adapter evidence; never a provider locator.
	Revision       string `json:"revision,omitempty"`
	ObjectID       string `json:"objectId,omitempty"`
	ObjectIdentity string `json:"objectIdentity,omitempty"`
	ChangeToken    string `json:"changeToken,omitempty"`
	Path           string `json:"path"`
	Name           string `json:"name"`
	Size           int64  `json:"size"`
	ModifiedNS     int64  `json:"modifiedNs"`
	Directory      bool   `json:"directory"`
	Data           string `json:"data,omitempty"`
}

// RemoteObject owns a captured configuration and either a conditional HTTP
// representation or one retained sequential acquisition. A zero Version is
// deliberately not promoted to an immutable version from size/mtime alone.
type RemoteObject interface {
	Snapshot() Snapshot
	Version() Version
	Access() string
	Observe(context.Context) (Snapshot, error)
	OpenRange(context.Context, int64, int64) (io.ReadCloser, error)
	Close() error
}

var (
	ErrRemoteAnalysisDisabled = errors.New("STRM target analysis is not enabled")
	ErrRemoteChanged          = errors.New("remote source configuration or object changed")
	ErrRemoteRange            = errors.New("remote source does not support safe byte ranges")
)
