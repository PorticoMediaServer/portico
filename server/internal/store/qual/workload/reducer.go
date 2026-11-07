package workload

import (
	"errors"
	"fmt"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/qual"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// Classification is QL's external join of actual WAL observations and owning
// composition/physical-return evidence. QS does NOT infer reset/no-frames from
// PASSIVE, file allocation, a phase name or a missing field. Ordinal counts
// complete physically returned attempts in this cell, independently of offers.
// Preserve incomplete raw records separately: they cannot disappear into a
// shorter passing population. EvidenceRef points to the exact raw phase record.
type Classification struct {
	Ordinal                                    uint64
	UnitID, Recipe, IntendedClass, EvidenceRef string
	Position                                   qual.WALCommitPosition
	UnknownReason, OutcomeReason               string
	Fenced                                     *bool // nil is unobserved, never false
	BeforeCustodyRefusal                       bool  // W-owned stage proof at EvidenceRef
}

// RestartSample projects owner durations to QL's existing reducer vocabulary.
// It refuses missing physical/policy/WAL evidence. A zero facade result on an
// error is not rollback; if it lacks evidence, retain it as an unreduced failure.
// Historic RESOLVE/ACK keep their own recipe and THIS-call timings, not the
// original mutation's timings or accepted-unit count.
func RestartSample(cell qual.RestartCell, o Observation, join Classification) (qual.RestartSample, error) {
	if o.Progress != nil {
		return qual.RestartSample{}, ErrWCComposition
	}
	e := o.Evidence
	if o.CallReturned == nil || join.BeforeCustodyRefusal || join.Fenced == nil || o.Stub || e == nil || o.HoldNS == nil || *o.HoldNS <= 0 || len(o.EvidenceProblems) != 0 ||
		e.Policy == nil || e.Policy.Engine != "3.53.4" || e.Policy.TempStore != 1 ||
		e.WALBefore == nil || e.WALAfter == nil || e.WALBefore.ObservedAt.IsZero() || e.WALAfter.ObservedAt.IsZero() ||
		e.PhysicalReturnedAt == nil || e.ReturnedAt.IsZero() || e.ReturnedAt.Before(*e.PhysicalReturnedAt) || e.ReturnedAt.After(*o.CallReturned) {
		return qual.RestartSample{}, errors.New("raw observation lacks complete owner evidence; retain unreduced")
	}
	if join.Ordinal == 0 || join.UnitID == "" || join.Recipe == "" || join.EvidenceRef == "" {
		return qual.RestartSample{}, errors.New("missing QL evidence join")
	}
	p := e.Policy
	if p.JournalMode != "wal" || p.ForeignKeys != 1 || p.Synchronous != int(e.Durability) || p.AutoCheckpoint != 0 ||
		p.CacheSize >= 0 || p.BusyTimeout != 0 || p.MmapSize != 0 || p.QueryOnly != 0 || p.DQSDDL != 0 || p.DQSDML != 0 ||
		e.WALBefore.ObservedAt.After(*e.PhysicalReturnedAt) || e.WALAfter.ObservedAt.After(*o.CallReturned) {
		return qual.RestartSample{}, errors.New("contradictory actual policy/WAL observation")
	}
	if e.Commit == writer.CommitNotAttempted && (o.CommitNS != nil || e.CommitAttemptedAt != nil || e.CommitReturnedAt != nil) {
		return qual.RestartSample{}, errors.New("COMMIT interval contradicts not-attempted observation")
	}
	s := qual.RestartSample{CellID: cell.ID, UnitID: join.UnitID, Ordinal: join.Ordinal, Recipe: join.Recipe,
		IntendedClass: join.IntendedClass, EvidenceRef: join.EvidenceRef, Position: join.Position,
		UnknownReason: join.UnknownReason, OutcomeReason: join.OutcomeReason, Fenced: *join.Fenced, Hold: *o.HoldNS}
	if o.CommitNS != nil {
		s.Commit = *o.CommitNS
	} else if e.Commit != writer.CommitNotAttempted || e.CommitAttemptedAt != nil || e.CommitReturnedAt != nil {
		return qual.RestartSample{}, errors.New("missing COMMIT interval is not zero")
	}
	switch e.Class {
	case governor.Background:
		s.Class = "Background"
	case governor.ScanMinimum:
		s.Class = "ScanMinimum"
	case governor.Foreground:
		s.Class = "Foreground"
	case governor.Recovery:
		s.Class = "Recovery"
	default:
		return qual.RestartSample{}, errors.New("unknown actual grant class")
	}
	switch e.Durability {
	case writer.Normal:
		s.Durability = "NORMAL"
	case writer.Full:
		s.Durability = "FULL"
	default:
		return qual.RestartSample{}, errors.New("unknown actual durability")
	}
	switch e.Commit {
	case writer.CommitSucceeded:
		s.Outcome = "committed"
	case writer.CommitNotAttempted, writer.CommitRolledBack:
		s.Outcome = "rolled_back"
	case writer.CommitUnknown:
		s.Outcome = "unknown"
	default:
		return qual.RestartSample{}, errors.New("unknown owner COMMIT observation")
	}
	// Validate with the real reducer (ordinal temporarily one for this isolated
	// shape check); it still validates global sequence/identity in the full cell.
	check := s
	check.Ordinal = 1
	if _, err := qual.SummarizeRestarts(cell, []qual.RestartSample{check}, 0); err != nil {
		return qual.RestartSample{}, fmt.Errorf("invalid reducer projection: %w", err)
	}
	return s, nil
}

// Coverage withholds the whole-run report on any gap. ClassificationComplete
// in the shared reducer covers only its inputs; it does not prove run coverage.
type Coverage struct {
	WriterCalls, PhysicalAttempts, BeforeCustodyRefusals, Unprojectable int
	Problems                                                            []string
	Report                                                              *qual.RestartReport
}

// Reduce consumes a COMPLETE raw run and independent W/QL joins keyed by raw
// UnitID. It never drops null attempts and renumbers the remaining successes.
// Source/provenance authentication and physical ordinal issuance remain QL's.
func Reduce(cell qual.RestartCell, raw []Record, joins map[string]Classification) Coverage {
	c := Coverage{}
	problem := func(reason string) { c.Problems = append(c.Problems, reason) }
	if len(raw) < 2 || raw[0].Kind != "start" || raw[len(raw)-1].Kind != "final" || !raw[len(raw)-1].DriversReturned || !raw[len(raw)-1].ScheduleComplete {
		problem("missing/incomplete raw run envelope")
	}
	var samples []qual.RestartSample
	var ordinal uint64
	seen := map[string]bool{}
	type counts struct {
		scheduled, offers, calls, missed uint64
		lastSlot                         uint64
		ended                            bool
	}
	streams := map[string]*counts{}
	if len(raw) > 0 {
		for _, s := range raw[0].Streams {
			if s.Name == "" || streams[s.Name] != nil || s.Every < time.Millisecond || s.Offset < 0 || s.Offset >= raw[0].Duration || len(s.Pattern) == 0 {
				problem("invalid raw schedule")
				continue
			}
			streams[s.Name] = &counts{scheduled: uint64((raw[0].Duration-s.Offset-1)/s.Every) + 1}
		}
	}
	if len(streams) == 0 {
		problem("missing declared schedules")
	}
	for i, r := range raw {
		if r.Observation != nil && r.Observation.Progress != nil {
			problem("progress metadata has no executable owner composition yet")
		}
		if r.Version != Version || r.Label != "DIRECTIONAL" || r.Qualification || r.Cell != cell || r.Sequence != uint64(i+1) || r.RunID == "" || r.UnitID != fmt.Sprintf("%s:%d", raw[0].RunID, r.Sequence) || r.RunID != raw[0].RunID {
			problem("raw run binding/order mismatch")
		}
		if r.Missed != 0 || r.Error != "" || r.Observation != nil && (r.Observation.Stub || r.Observation.RefusalError) {
			problem("raw missed/stub/refusal/error coverage gap")
		}
		switch r.Kind {
		case "start":
			if i != 0 {
				problem("duplicate start")
			}
		case "final":
			if i != len(raw)-1 {
				problem("record after final")
			}
		case "operation", "missed":
			if r.Offer == nil || streams[r.Offer.Stream] == nil {
				problem("missing/undeclared offer")
				break
			}
			s := streams[r.Offer.Stream]
			if s.ended {
				problem("offer after stream end")
			}
			if r.Kind == "missed" {
				s.missed += r.Missed
				break
			}
			if r.Offer.Slot > s.lastSlot {
				s.offers++
				s.lastSlot = r.Offer.Slot
			} else if r.Offer.Slot != s.lastSlot {
				problem("out-of-order offer slot")
			}
			if r.Observation != nil {
				s.calls++
			}
		case "stream_end":
			if r.Progress == nil || streams[r.Progress.Name] == nil {
				problem("missing/undeclared stream end")
				break
			}
			s, p := streams[r.Progress.Name], r.Progress
			if s.ended || p.Scheduled != s.scheduled || p.Offered != s.offers || p.Calls != s.calls || p.Missed != s.missed || p.Unserved != 0 || p.Offered+p.Missed != p.Scheduled {
				problem("stream coverage counts mismatch")
			}
			s.ended = true
		default:
			problem("unknown raw record kind")
		}
		o := r.Observation
		if o == nil || o.Phase != "ACCEPT" && o.Phase != "BEGIN" && o.Phase != "RESOLVE" && o.Phase != "ACK" && o.Phase != "SNAPSHOT" {
			continue
		}
		c.WriterCalls++
		join, ok := joins[r.UnitID]
		if !ok || join.UnitID != r.UnitID || join.EvidenceRef == "" {
			c.Unprojectable++
			problem("missing per-call physical join")
			continue
		}
		seen[r.UnitID] = true
		if join.BeforeCustodyRefusal {
			if join.Ordinal != 0 || o.Evidence != nil || o.HoldNS != nil || o.CallError == "" {
				c.Unprojectable++
				problem("contradictory before-custody proof")
				continue
			}
			c.BeforeCustodyRefusals++
			continue
		}
		ordinal++
		if join.Ordinal != ordinal {
			c.Unprojectable++
			problem("physical-attempt ordinal gap")
			continue
		}
		s, err := RestartSample(cell, *o, join)
		if err != nil {
			c.Unprojectable++
			problem(err.Error())
			continue
		}
		c.PhysicalAttempts++
		samples = append(samples, s)
	}
	for _, s := range streams {
		if !s.ended {
			problem("missing stream end")
		}
	}
	if len(seen) != len(joins) {
		problem("join does not match a raw writer call")
	}
	if len(samples) == 0 {
		problem("no complete physical population")
	}
	if len(c.Problems) == 0 {
		r, err := qual.SummarizeRestarts(cell, samples, c.BeforeCustodyRefusals)
		if err != nil {
			problem(err.Error())
		} else {
			c.Report = &r
		}
	}
	return c
}
