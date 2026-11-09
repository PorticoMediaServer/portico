package writer

import (
	"errors"
	"math"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// Class is selected only from the compiled command purpose, never a worker
// field. New purposes must acquire an explicit policy here before execution.
func commandClass(id CommandID) (governor.Class, error) {
	switch id {
	case IPPolicySet, IPRefreshBegin, IPRefreshPage, IPEntityClaim, IPEntityRenew, IPEntityFinish,
		IPPersonClaim, IPPersonRenew, IPPersonFinish, IPPersonEvidenceBegin,
		IPPersonEvidenceAppend, IPPersonSeal,
		IPEntityEvidenceBegin, IPEntityEvidenceAppend,
		IPEntitySeal, IPTVSetBegin, IPTVSetPage, IPTVSetSeal, IPEntityAccept, IPEntityDelta,
		IPArtworkDiscoveryClaim, IPArtworkDiscoveryPage, IPArtworkDiscoveryFinish,
		IPFieldEdit, IPFieldLock, IPFieldRestore, IPEditBulkBegin, IPEditBulkPage, IPEditBulkFinish:
		return governor.Background, nil
	case IPArtworkMaterialIntent, IPArtworkMaterialInstalled, IPArtworkMaterialPublish,
		IPArtworkSelect, IPArtworkClear, IPArtworkUploadAccept, IPArtworkReencode,
		IPArtworkGCMark, IPArtworkGCFinish:
		return governor.Background, nil
	case IAConfigureLibrary, IAConfigureSource:
		return governor.Background, nil
	case InventoryAccept, BasicAccept, BasicStage, BasicSeal, InventoryCreate, InventoryClaim, InventoryDirectoryDiscover, InventoryDirectoryEOF, InventoryDirectoryRefuse, InventoryTraversalComplete:
		return governor.ScanMinimum, nil
	case MPQueueRepeat:
		return governor.Foreground, nil
	case ProgressAccept:
		return governor.Foreground, nil
	case ProgressReceiptReclaim:
		return governor.Background, nil
	case SecurityAccept:
		return governor.Recovery, nil
	case EvidenceStage, CreditStage, PeopleStage, EnrichAccept, SearchFragmentStage,
		SearchHeadPublish, SearchFragmentReclaim, SearchImageReclaim, SearchGenerationAdvance,
		SearchGenerationFlip, PeopleHeadPublish, SearchImageStage, SearchSeal, EffectiveStage, EvidenceSeal, CreditSourceSeal:
		return governor.Background, nil
	default:
		return 0, invalid("command_class")
	}
}

// governedTurn is the private frame's custody capability. The closed dispatcher
// owns the minted grant until begin consumes it (ErrBusy leaves it with caller).
// Its request includes complete command/receipt/terminal forecasts. Input and
// output preparation and idle pool/native memory retain separate lifetime custody.
type governedTurn struct {
	g                     governor.Governor
	a                     *governor.Authority
	grant                 *governor.Grant
	expected              governor.Request
	publication           *rbPublication // opt-in phase2 resolved dependency custody
	purpose               CommandID
	limit                 Work
	clockHighWater        *time.Time                // same W composition mark under its existing mutex
	inventoryEngine       sqliteconn.EngineIdentity // exact profile implementation, observed again before BEGIN
	inventoryMemory       inventoryMemoryBudget     // installed once by closed W composition before BEGIN
	searchImageMemory     searchImageMemoryBudget
	scanLifecycleMemory   commandMemoryBudget
	enrichmentMemory      commandMemoryBudget
	basicMemory           commandMemoryBudget // exact Basic variant; zero remains unqualified
	wcProgressMemory      commandMemoryBudget
	wcSecurityMemory      commandMemoryBudget
	wcReclaimMemory       commandMemoryBudget
	mpWorkerMemory        commandMemoryBudget
	directoryMemory       commandMemoryBudget
	publisherMemory       commandMemoryBudget
	effectiveSourceMemory commandMemoryBudget // exact Effective source-proof partition; zero is unqualified
	state                 atomic.Uint32       // 0 active, 1 retirement attempted, 2 retired, 3 failed
	fenced                atomic.Bool
}

func bindGovernedTurn(id CommandID, limit Work, g governor.Governor, a *governor.Authority, wal *governor.WALGate, grant *governor.Grant, expected governor.Request) (*governedTurn, error) {
	if err := a.VerifyComposition(g, wal); err != nil {
		return nil, err
	}
	class, err := commandClass(id)
	if err != nil {
		return nil, err
	}
	if limit.Rows == 0 || limit.Rows > math.MaxUint32 || limit.Pages == 0 || limit.Pages > math.MaxUint32 ||
		limit.VMOps == 0 || limit.WALBytes == 0 || limit.WALBytes > math.MaxInt64 || limit.Hold <= 0 || limit.Hold > MaxCommandHold ||
		!expected.Writer || expected.Key.Domain != uint16(id) || expected.Class != class || expected.MaxHold != limit.Hold ||
		expected.Budget != (governor.UnitBudget{RawRows: uint32(limit.Rows), VMOps: limit.VMOps, Pages: uint32(limit.Pages)}) ||
		expected.Need[governor.WALBytes] != int64(limit.WALBytes) {
		return nil, invalid("command_forecast")
	}
	if err := a.Check(grant, expected); err != nil {
		return nil, err
	}
	// Copies are under the already admitted preparation/metadata reservation.
	// Subsequent caller mutation cannot rebase the exact grant comparison.
	expected.Key.ID = strings.Clone(expected.Key.ID)
	expected.IO = slices.Clone(expected.IO)
	for i := range expected.IO {
		expected.IO[i].DeviceID = strings.Clone(expected.IO[i].DeviceID)
	}
	expected.Footprint = slices.Clone(expected.Footprint)
	return &governedTurn{g: g, a: a, grant: grant, expected: expected, purpose: id, limit: limit}, nil
}

func (c *governedTurn) check(id CommandID, limit Work) error {
	if c == nil || c.state.Load() != 0 || c.fenced.Load() {
		return governor.ErrGrant
	}
	class, err := commandClass(id)
	if err != nil {
		return err
	}
	if id != c.purpose || limit != c.limit || c.expected.Key.Domain != uint16(id) || c.expected.Class != class {
		return invalid("bound_frame_forecast")
	}
	return c.a.Check(c.grant, c.expected)
}

func (c *governedTurn) retired() error {
	if !c.state.CompareAndSwap(0, 1) {
		if c.state.Load() == 2 {
			return nil
		} // exact completed retirement is idempotent
		return governor.ErrRetirement
	}
	// Terminal state precedes the authority call. A panic/incomplete retirement
	// cannot authorize new work or cause a second refund; the frame fences it.
	if err := c.a.Retired(c.grant); err != nil {
		c.state.Store(3)
		return err
	}
	c.state.Store(2)
	return nil
}

func (c *governedTurn) fence() {
	c.fenced.Store(true)
	c.g.Cancel(c.grant) // cancellation alone NEVER returns physical capacity
	c.a.EnterStatic()
}

// refreshWriterWAL is called under exclusive W composition custody at startup
// and AFTER physical return, BEFORE the next Try. It performs no IO inside Try.
// Sampling's own bounded resources belong to composition lifetime custody.
func refreshWriterWAL(path string, wal *governor.WALGate, now func() time.Time) error {
	_, err := observeWriterWAL(path, wal, now)
	return err
}

func observeWriterWAL(path string, wal *governor.WALGate, now func() time.Time) (observation *WALObservation, err error) {
	// Standalone startup/component observation has no preceding writer turn.
	var highWater time.Time
	return observeWriterWALAt(path, wal, now, &highWater)
}

func observeWriterWALAt(path string, wal *governor.WALGate, now func() time.Time, highWater *time.Time, floors ...time.Time) (observation *WALObservation, err error) {
	if wal == nil {
		return nil, invalid("writer_wal_observer")
	}
	token := wal.Observation() // must precede the physical read
	published := false
	defer func() {
		primary := recover()
		if !published {
			// A failed observer cannot leave an earlier sample admissible. The
			// invalidation is pure and uses no second fallible clock callback.
			err = errors.Join(err, wal.Publish(token, governor.WALState{}, time.Time{}))
		}
		if primary != nil {
			panic(primary)
		}
	}()
	if now == nil || highWater == nil {
		return nil, invalid("writer_wal_observer")
	}
	state, err := governor.ReadWAL(path)
	if err != nil {
		return nil, err
	}
	at := now()
	if err := observeTime(highWater, at, floors...); err != nil {
		return nil, err
	}
	if err := wal.Publish(token, state, at); err != nil {
		return nil, err
	}
	published = true
	return &WALObservation{Token: token, ObservedAt: at, State: state}, nil
}
