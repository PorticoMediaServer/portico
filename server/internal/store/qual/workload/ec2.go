package workload

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// EC2Operation identifies a scheduled cycle, not a physical commit. In
// particular Resolve, ACK, checks and coalescing never add fresh FULL samples.
type EC2Operation string

const (
	EC2Inventory       EC2Operation = "inventory"
	EC2Effective       EC2Operation = "effective"
	EC2Security        EC2Operation = "security"
	EC2ProgressCapture EC2Operation = "progress_capture"
	EC2ProgressDrain   EC2Operation = "progress_drain"
	EC2Reclaim         EC2Operation = "reclaim"
	EC2Browse          EC2Operation = "browse"
	EC2Detail          EC2Operation = "detail"
	EC2Playback        EC2Operation = "playback_lookup"
	EC2Continue        EC2Operation = "continue_watching"
	EC2FullSearch      EC2Operation = "full_search"
	EC2Passive         EC2Operation = "passive"
	EC2Recovery        EC2Operation = "recover_wal"
	EC2WAL             EC2Operation = "wal_observe"
)

var ErrEC2Composition = errors.New("EC2 source-bound native composition unavailable")

// EC2Schedule uses Run's common epoch, original deadlines and bounded serial
// workers. Security/progress belong to the SAME writer stream as ingest. The
// caller declares checkpoint mode; this does not enable concurrent CP custody.
// Recovery is a separate request cadence: only GV may establish hard pressure,
// drain custody and perform recovery. An offer never proves a reset occurred.
type EC2Schedule struct {
	WriterEvery, ReaderEvery, SearchEvery, PassiveEvery, RecoveryEvery, WALEvery time.Duration
	WriterPattern, ReaderPattern                                                 []EC2Operation
	CheckpointMode                                                               string // serialized or concurrent; declaration only
}

// EC2Bindings contains already-owned typed Actions, not constructors, SQL or
// grants. Use Inventory/Effective, the foreground adapters, Passive and the
// forthcoming WC/GV Actions from the SAME trusted database composition.
type EC2Bindings map[EC2Operation]Action

type EC2Manifest struct {
	Schedule             EC2Schedule
	Missing              []EC2Operation
	MixedFULLCadence     bool // intended pattern only, not a native FULL observation
	CompleteCapabilities bool // selected Actions present; not authority/coverage proof
	Scope                string
}

// EC2Streams does not drop unimplemented requested operations. Each missing
// binding emits a null-timed STUB and fails, stopping every stream through Run.
// This lets a partial N1/read/WAL component be exercised without claiming the
// mixed security/checkpoint/reset population was executed. No bound verdict or
// RestartSample is manufactured here; QL joins the actual owner observations.
func EC2Streams(s EC2Schedule, bindings EC2Bindings) ([]Stream, EC2Manifest, error) {
	m := EC2Manifest{Schedule: s, Missing: []EC2Operation{}, Scope: "selected component only; qualification=false; WAL-BOUND/WAL-RESET unclassified"}
	validEvery := func(v time.Duration) bool { return v >= time.Millisecond && v <= 2*time.Hour }
	if !validEvery(s.WriterEvery) || !validEvery(s.WALEvery) || len(s.WriterPattern) == 0 || len(s.WriterPattern) > 32 || len(s.ReaderPattern) > 32 ||
		(s.CheckpointMode != "serialized" && s.CheckpointMode != "concurrent") ||
		(len(s.ReaderPattern) > 0 && !validEvery(s.ReaderEvery)) || (len(s.ReaderPattern) == 0 && s.ReaderEvery != 0) ||
		(s.PassiveEvery != 0 && (s.PassiveEvery < 50*time.Millisecond || s.PassiveEvery > 2*time.Hour)) ||
		(s.RecoveryEvery != 0 && !validEvery(s.RecoveryEvery)) || (s.RecoveryEvery != 0 && s.PassiveEvery == 0) {
		return nil, m, errors.New("invalid EC2 schedule")
	}
	// Copy caller-owned patterns before publishing the immutable declaration.
	m.Schedule.WriterPattern = append([]EC2Operation(nil), s.WriterPattern...)
	m.Schedule.ReaderPattern = append([]EC2Operation(nil), s.ReaderPattern...)
	missing := map[EC2Operation]bool{}
	bind := func(op EC2Operation) Step {
		action := bindings[op]
		if action == nil {
			if !missing[op] {
				m.Missing = append(m.Missing, op)
				missing[op] = true
			}
			action = func(_ context.Context, _ Offer, emit func(Observation) error) error {
				err := fmt.Errorf("%w: %s", ErrEC2Composition, op)
				return errors.Join(err, emit(Observation{Phase: "STUB", Stub: true, Notice: err.Error(), CallError: err.Error(), Result: op}))
			}
		}
		return Step{Name: string(op), Run: action}
	}
	writer := Stream{Name: "writer", Every: s.WriterEvery}
	ingest, security := false, false
	for _, op := range s.WriterPattern {
		switch op {
		case EC2Inventory, EC2Effective:
			ingest = true
		case EC2Security:
			security = true
		case EC2ProgressCapture, EC2ProgressDrain, EC2Reclaim:
		default:
			return nil, m, errors.New("non-writer operation in EC2 writer pattern")
		}
		writer.Pattern = append(writer.Pattern, bind(op))
	}
	if !ingest {
		return nil, m, errors.New("EC2 requires an actual ingest operation")
	}
	streams := []Stream{writer}
	if len(s.ReaderPattern) > 0 {
		// One serial foreground worker (the fixture's Reader:1 profile). The
		// pattern may repeat entries to declare a realistic share, e.g. search
		// well below browse/detail, without a second concurrent reader.
		readers := Stream{Name: "foreground", Every: s.ReaderEvery}
		for _, op := range s.ReaderPattern {
			if op != EC2Browse && op != EC2Detail && op != EC2Playback && op != EC2Continue && op != EC2FullSearch {
				return nil, m, errors.New("invalid EC2 reader pattern")
			}
			readers.Pattern = append(readers.Pattern, bind(op))
		}
		streams = append(streams, readers)
	}
	if s.PassiveEvery != 0 {
		streams = append(streams, Stream{Name: "checkpoint", Every: s.PassiveEvery, Pattern: []Step{bind(EC2Passive)}})
	}
	if s.RecoveryEvery != 0 {
		streams = append(streams, Stream{Name: "recovery", Every: s.RecoveryEvery, Pattern: []Step{bind(EC2Recovery)}})
	}
	streams = append(streams, Stream{Name: "wal", Every: s.WALEvery, Pattern: []Step{bind(EC2WAL)}})
	m.MixedFULLCadence = ingest && security
	m.CompleteCapabilities = len(m.Missing) == 0
	return streams, m, nil
}
