// Package selectionactions executes explicitly confirmed immutable mutation commands.
// Query preparation and traversal live only in bounded process memory.
package selectionactions

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

var (
	ErrCancelled = errors.New("selection action cancelled")
	ErrInput     = errors.New("selection action input invalid")
	ErrStale     = errors.New("selection action preparation stale")
	ErrCapacity  = errors.New("selection action capacity exhausted")
	ErrConflict  = errors.New("selection action operation id reused")
	ErrNotFound  = errors.New("selection action not found")
)

const MaxRecords = 20
const MaxSourceBytes = 4 << 20
const RetryAfterMs = 1000

type Options struct {
	DeleteFiles *bool  `json:"deleteFiles,omitempty"`
	DeviceID    string `json:"deviceId,omitempty"`
	Quality     string `json:"quality,omitempty"`
}

type Sample struct {
	ItemID string `json:"itemId"`
	Title  string `json:"title"`
	Kind   string `json:"kind"`
}
type Preview struct {
	Kind             string   `json:"kind"`
	PreparationID    string   `json:"preparationId"`
	Action           string   `json:"action"`
	Basis            string   `json:"basis"`
	Total            int64    `json:"total"`
	Files            int64    `json:"files"`
	Bytes            int64    `json:"bytes"`
	BytesKnown       bool     `json:"bytesKnown"`
	Confirmation     string   `json:"confirmation"`
	ConfirmationKind string   `json:"confirmationKind"`
	ExpiresAt        string   `json:"expiresAt"`
	Sample           []Sample `json:"sample"`
}
type Pending struct {
	Kind          string `json:"kind"`
	PreparationID string `json:"preparationId"`
	RetryAfterMs  int    `json:"retryAfterMs"`
}
type Confirm struct {
	OperationID   string `json:"operationId"`
	PreparationID string `json:"preparationId"`
	Confirmation  string `json:"confirmation"`
}
type Job struct {
	OperationID  string `json:"operationId"`
	Action       string `json:"action"`
	State        string `json:"state"`
	Total        int64  `json:"total"`
	Admitted     int64  `json:"admitted"`
	Done         int64  `json:"done"`
	Failed       int64  `json:"failed"`
	BytesDeleted int64  `json:"bytesDeleted"`
	FilesDeleted int64  `json:"filesDeleted"`
	ErrorCode    string `json:"errorCode,omitempty"`
	RetryAfterMs int    `json:"retryAfterMs"`
}

// Target is a concrete immutable user command. It contains no query expression.
type Target struct {
	EntityID                                 int64
	ItemID, LibraryID, Kind, Title, Identity string
	VersionID                                string
	DownloadIdentity                         string
	Lifetime                                 string
	OperationID                              string `json:"-"` // execution correlation; assigned after confirmation, never source digest
}

// File is a concrete approved file/link command, never query scratch.
type File struct {
	TargetID, AssetID               int64
	Token, Path, Root, RootIdentity string
	SourceID, SourceIncarnation     string
	SourceGeneration                int64
	Size, ModifiedNS                int64
	Shared, Present                 bool
	Part                            int
	Start                           float64
	End                             *float64
	Edition                         string
	Started                         bool // set only by the durable executor, excluded from capture digest
}

// Record begins a target, supplies one file, or finishes a target. At most one
// branch is set; all branches participate in the ordered preparation digest.
type Record struct {
	Target *Target
	File   *File
	Finish int64
}
type Batch struct {
	Records []Record
	Done    bool
}

// Source performs bounded read-only work. Step retains no snapshots across calls.
// Every source/callback capture and backing allocation must fit Bytes and Reserve.
type Source interface {
	// A failed step must not advance its volatile continuation. A successful
	// batch is retained by the journal until admission commits.
	Step(context.Context) (Batch, error)
	Bytes() int64
}
type SourceFactory func(context.Context) (Source, error)
type Authorize func(context.Context, *sql.Tx, identity.Principal, string, string, *Target) error

// Effect returns only observed effects. Complete and Failed apply to target
// finalization; file commands never advance target counts.
type Effect struct {
	Bytes, Files int64
	Code         string
}
type Apply func(context.Context, identity.Principal, string, Options, Target, *File, func(*sql.Tx, Effect) error) error

type Acceptance struct {
	Principal                                      identity.Principal
	Binding, Account, Library, Action, RequestHash string
	OperationID                                    string
	Options                                        Options
	Total                                          int64
	Digest                                         string
	Factory                                        SourceFactory
	Check                                          func(context.Context, *sql.Tx) error
	Reserve                                        func(int64) error
	Release                                        func()
}

// Config's small active limits bound command admission, not library size.
type Config struct {
	PerAccount, Active int
	HistoryTTL         time.Duration
	Now                func() time.Time
}

func DefaultConfig() Config {
	return Config{PerAccount: 2, Active: 4, HistoryTTL: time.Hour, Now: time.Now}
}
func Terminal(state string) bool {
	switch state {
	case "complete", "partial", "failed", "cancelled":
		return true
	}
	return false
}

// Lifecycle records only actual domain ownership in the SAME accepting/terminal
// transaction. It cannot perform I/O or retain query/capture state.
type Lifecycle func(context.Context, *sql.Tx, identity.Principal, Job, Options) error
