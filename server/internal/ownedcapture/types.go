// Package ownedcapture retains one complete admitted weak finite ISO acquisition.
// It supplies private byte custody, never disc validity or playback authority.
package ownedcapture

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"regexp"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
)

// MaxCaptureLifetime is a hard custody ceiling, never an actor/family extension.
// Producers shorten it to the original admitted actor/source policy lifetime.
const MaxCaptureLifetime = 24 * time.Hour

var (
	ErrInput      = errors.New("invalid owned capture binding")
	ErrCurrent    = errors.New("owned capture authority or receipt retired")
	ErrIncomplete = errors.New("owned capture acquisition incomplete")
)

// Request is private trusted code input. The producer, not these fields or the
// capture store, resolves the physical asset and authorizes the original actor.
type Request struct {
	Principal identity.Principal
	AssetID   string
}

// Binding contains normalized adapter-issued witnesses only. No path, URL,
// credential, source settings blob or inferred provider version belongs here.
// AcquisitionID and ReservationID identify one original admitted acquisition;
// equal bytes never grant another source/actor access to that acquisition.
type Binding struct {
	SourceID, ConfigGeneration        string
	ObjectID, ObjectRevision, AssetID string
	AcquisitionID, ReservationID      string
	PolicyWitness, AuthorityWitness   string
}

var captureID = regexp.MustCompile(`^[a-f0-9]{64}$`)
var opaque = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,256}$`)

func (b Binding) valid() bool {
	for _, v := range []string{b.SourceID, b.ConfigGeneration, b.ObjectID, b.ObjectRevision, b.AssetID, b.AcquisitionID, b.ReservationID, b.PolicyWitness, b.AuthorityWitness} {
		if !opaque.MatchString(v) {
			return false
		}
	}
	return true
}

// SourceLease is issued by the original source producer. Its cancellation and
// checks must retain original config/read policy/actor authority after local
// possession, including in the caller's accepting transaction.
type SourceLease interface {
	Context() context.Context
	Check(context.Context) error
	CheckTx(context.Context, *sql.Tx) error
	Close() error
}

// Acquisition is one source-owned sequential stream, with permission, complete
// reservation, free-space floor and pacing established before any read. Read
// must apply the original source limits/pacing/currentness throughout. Close
// never reopens/resumes the provider and must not uncharge committed custody.
type Acquisition interface {
	io.Reader
	SourceLease
	Binding() Binding
	Size() int64
	ExpiresAt() time.Time
	// Persisted receives cumulative actual successful artifact Writer.Write bytes,
	// never delivered/read estimates; quota stays charged through deletion.
	Persisted(context.Context, int64) error
	// CommitTx transfers the original reservation to committed retained custody
	// in the SAME transaction as publication; it may not enlarge the reservation.
	CommitTx(context.Context, *sql.Tx) error
}

// Destination is constructed only by this store after mediaartifact validates
// its owned directory. It exposes a floor check on THAT volume, no byte/path API.
// Producer is a trusted server adapter, never caller-provided policy booleans.
// Admit binds the generated capture ID to a real original source reservation.
// ReleaseTx runs only after physical owned partial/object-reference cleanup;
// other acquisitions retain their own full charge for deduplicated bytes.
// It must survive retired actors, never substitute a latest principal.
type Producer interface {
	Admit(context.Context, string, Request, Destination) (Acquisition, error)
	Lease(context.Context, Binding) (SourceLease, error)
	CheckTx(context.Context, *sql.Tx, Binding) error
	ReleaseTx(context.Context, *sql.Tx, Binding) error
}

// Receipt is a value checked against immutable normalized rows, not a path or
// bearer capability. Callers must resolve this exact value again for each reader.
type Receipt struct {
	ID, ArtifactID string
	Binding        Binding
	Object         mediaartifact.Object
	Version        mediasource.Version
	ExpiresAt      time.Time
}
