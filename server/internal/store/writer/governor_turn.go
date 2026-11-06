package writer

import (
	"errors"
	"math"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Class is selected only from the compiled command purpose, never a worker
// field. New purposes must acquire an explicit policy here before execution.
func commandClass(id CommandID) (governor.Class, error) {
	switch id {
	case InventoryAccept, BasicAccept:
		return governor.ScanMinimum, nil
	case ProgressAccept:
		return governor.Foreground, nil
	case SecurityAccept:
		return governor.Recovery, nil
	case EvidenceStage, CreditStage, PeopleStage, EnrichAccept, SearchFragmentStage,
		SearchHeadPublish, SearchFragmentReclaim, SearchImageReclaim, SearchGenerationAdvance,
		SearchGenerationFlip, PeopleHeadPublish, SearchImageStage, SearchSeal, EffectiveStage:
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
	g        governor.Governor
	a        *governor.Authority
	grant    *governor.Grant
	expected governor.Request
	state    atomic.Uint32 // 0 active, 1 retirement attempted, 2 retired, 3 failed
	fenced   atomic.Bool
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
	return &governedTurn{g: g, a: a, grant: grant, expected: expected}, nil
}

func (c *governedTurn) check() error {
	if c == nil || c.state.Load() != 0 || c.fenced.Load() {
		return governor.ErrGrant
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

func observeWriterWAL(path string, wal *governor.WALGate, now func() time.Time) (*WALObservation, error) {
	if wal == nil || now == nil {
		return nil, invalid("writer_wal_observer")
	}
	token := wal.Observation() // must precede the physical read
	state, err := governor.ReadWAL(path)
	at := now()
	if err != nil {
		// Refuse against an invalid sample, never leave old headroom reusable.
		return nil, errors.Join(err, wal.Publish(token, governor.WALState{}, at))
	}
	if err := wal.Publish(token, state, at); err != nil {
		return nil, err
	}
	return &WALObservation{Token: token, ObservedAt: at, State: state}, nil
}
