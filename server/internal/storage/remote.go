package storage

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
)

// RemoteSources is the adapter-specific seam used by the existing path-based
// catalog, durable inventory and acquisition. Paths are assigned virtual roots, never URLs.
// Local and externally mounted paths continue through the physical helper.
type RemoteSources interface {
	Handles(string) bool
	InspectRoot(context.Context, string) (Snapshot, error)
	Stat(context.Context, string) (Snapshot, error)
	ListPage(context.Context, string, string, string) (RemotePage, error)
	ListInventoryPage(context.Context, string, string, string, int) (RemotePage, error)
	CommitPage(context.Context, *sql.Tx, RemotePage) error
	CheckScan(context.Context, string, string) error
	ValidateScan(context.Context, *sql.Tx, string, string) error
	RecordReference(context.Context, *sql.Tx, string, string) error
	Acquire(context.Context, string, string) (RemoteObject, error)
}

// RemoteInventoryVerifier is additive: peer acquisition consumers continue to
// use RemoteSources. An inventory consumer requires fresh complete-child proof.
type RemoteInventoryVerifier interface {
	VerifyInventoryDirectory(context.Context, string, string, string) error
}

// RemoteInventoryAbsence is the narrow remote per-file proof. Complete, fresh
// directory evidence produced for the caller's key can establish that one exact
// child is absent without materialising the directory again. Both methods belong
// to the same adapter because a proof that cannot be revalidated in the
// accepting writer must not be taken at all.
type RemoteInventoryAbsence interface {
	// InventoryAbsence reports whether the exact recorded file is absent from
	// fresh evidence produced for this key. A present file is (false, nil); an
	// absent file is (true, nil) only with such evidence. Pending evidence is
	// ErrBusy, and an inconclusive or unavailable source is an error, never an
	// absence.
	InventoryAbsence(context.Context, string, InventoryRequest) (bool, error)
	// ValidateInventoryAbsenceTx fences the exact listing for the same key and
	// the captured source generation in the writer that accepts the missing
	// mark.
	ValidateInventoryAbsenceTx(context.Context, *sql.Tx, string, InventoryRequest) error
}

func RemoteDirectoryIdentity(source, path string) string {
	return "remote-dir:" + source + ":" + identity.Digest(path)
}

type RemotePage struct {
	// Limit is part of this immutable page's identity alongside Cursor. It is
	// rechecked against private source rows before accepting its observations.
	Limit                                             int
	ID, SourceID, Directory, Generation, Cursor, Next string
	Entries                                           []Snapshot
	Complete, Pending                                 bool
}

// RemoteObject owns a captured configuration and either a conditional HTTP
// representation or one retained sequential acquisition. A zero Version is
// deliberately not promoted to an immutable version from size/mtime alone.
type RemoteObject interface {
	Snapshot() Snapshot
	Version() mediasource.Version
	Access() string
	Observe(context.Context) (Snapshot, error)
	OpenRange(context.Context, int64, int64) (io.ReadCloser, error)
	Close() error
}

var (
	ErrRemoteAnalysisDisabled = errors.New("STRM target analysis is not enabled")
	ErrRemoteConfig           = errors.New("remote source configuration is invalid")
	ErrRemoteChanged          = errors.New("remote source configuration or object changed")
	ErrRemoteOffline          = errors.New("remote source is unavailable")
	ErrRemoteCredentials      = errors.New("remote source credentials require attention")
	ErrRemoteRange            = errors.New("remote source does not support safe byte ranges")
	ErrRemoteCursor           = errors.New("remote inventory continuation expired; start a new scan")
	ErrRemoteLimit            = errors.New("remote source operation exceeded its safety budget")
	ErrRemoteBinary           = errors.New("approved rclone executable changed or is unavailable")
)

func (c *Client) IsRemote(path string) bool {
	return c != nil && c.Remote != nil && c.Remote.Handles(path)
}
