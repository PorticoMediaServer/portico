// Package playbackdisc stores private immutable disc inputs and facts for one
// accepted legacy presentation. Rows, catalogue metadata and hashes grant no
// authority or byte custody on their own.
package playbackdisc

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/ownedcapture"
)

var (
	ErrInput    = errors.New("invalid private disc snapshot")
	ErrCurrent  = errors.New("private disc presentation retired or changed")
	ErrConflict = errors.New("private disc presentation already bound")
	ErrCapacity = errors.New("private disc reference capacity exceeded")
)

type Key struct {
	SessionID  string
	Generation int
}
type Actor struct {
	ServerID, Authority, AccountID, ProfileID, DeviceID, FamilyID, Grant, SessionHash string
	Epoch, TokenGeneration, OriginalHorizonMs                                         int64
}

// Operation retains the original queue tuple. QueueCustodyGeneration zero is
// the legitimate initial value supplied by queues_v1. A queued operation still
// requires its complete original identity, owner/entry/revision and the
// mandatory accepting checker; custody is compared
// exactly, never inferred, incremented or replaced by this store.
type Operation struct {
	LegacyRequestID, StartKeyDigest, StartBodyDigest, QueueID, QueueEntryID, QueueOwnerKind, QueueOwnerID string
	QueueRevision, QueueCustodyGeneration                                                                 int64
}
type Source struct {
	LibraryID, LibrarySourceID, AssetID, InventoryObjectID, InventoryRevision, RootIdentity, Incarnation, RemoteSourceID, EffectiveConfiguration, ReadPolicyWitness, ReadAuthorityWitness string
	LibraryGeneration, RawConfigurationGeneration, ProfileRevision, Size, ModifiedNS                                                                                                      int64
}
type Conditional struct{ Version mediasource.Version }
type Captured struct{ Receipt ownedcapture.Receipt }

// Input has exactly one branch. ContentSHA256 belongs only to an exact owned
// receipt; it cannot upgrade an origin to a conditional source.
type Input struct {
	Conditional *Conditional
	Captured    *Captured
}
type DiscFacts struct {
	Structure                                                                           disc.Probe
	SelectedTitleKey, StructureRevision, StreamRevision, ChapterRevision, FactsRevision string
}
type MediaFacts struct {
	Container, VideoCodec, AudioCodec string
	Width, Height                     int
	DurationMs, OriginUS              int64
	TimingKnown                       bool
	Streams                           []assets.Stream
	Chapters                          []assets.Chapter
}
type ToolPins struct{ DiscHelperDigest, FFmpegDigest, FFprobeDigest, RuntimeIdentity string }
type Snapshot struct {
	ID, SessionID string
	Generation    int
	Actor         Actor
	Operation     Operation
	Source        Source
	Input         Input
	Disc          DiscFacts
	Media         MediaFacts
	Tools         ToolPins
}

// Check must reprove the sealed original Play measurement, actor/source/content,
// physical object, queue/custody/occurrence and idempotent operation inside THIS
// transaction. Activation requires the original process proof, never facts read
// back after a crash. The store additionally checks its own exact rows/receipt.
type Check func(context.Context, *sql.Tx, Snapshot) error

type Pin struct {
	ID, SnapshotID, SessionID string
	Generation                int
	ExpiresAt                 time.Time
}
type Store struct {
	serverID string
	captures *ownedcapture.Service
}

func New(serverID string, captures *ownedcapture.Service) (*Store, error) {
	if !text(serverID, 256, false) {
		return nil, ErrInput
	}
	return &Store{serverID: serverID, captures: captures}, nil
}
