// Package progresslineage checks bounded diagnostic ID/range edges only.
// It imports no writer, SQL, governor or reducer and confers no authority.
package progresslineage

import (
	"errors"
	"fmt"
	"sort"
)

// These are bounded diagnostic values, not source issuance, a receipt codec or
// SQL/ACK authority. The producer must emit each raw fact before applying this
// check; rejected/unknown/overflow records may never disappear from the wire.
const MaxPendingCaptures = 128
const FirstDrainQuarterMS int64 = 900_000 // existing FIRST domain piece, UTC epoch milliseconds
const MaxDrainEdges = 128
const MaxObservedIDs = 4096 // diagnostic ID bound, not a product/profile allowance

var ErrProgressLinkage = errors.New("progress capture/drain linkage incomplete")

type ProgressRange struct{ FromMS, UntilMS int64 }
type ProgressOwner struct {
	PlaybackID                                                   string
	CommandIncarnation                                           [16]byte
	ClockIncarnation                                             [16]byte
	OccurrenceGeneration, ActivityGeneration, RememberGeneration int64
}

// Ordinal is the ACTUAL physical order from the one owning writer, including
// interleaved security/other turns. It is not the emission order or original N.
type ProgressPhysical struct {
	UnitID  string
	Ordinal uint64
}

type ProgressCaptureFact struct {
	AcceptedCaptureID                                 string         // actual accepted pending parent; coalescing cannot invent it
	PendingRange                                      *ProgressRange // observed native pending range on the physical capture only
	PendingCursor                                     *int64
	SourceID                                          string
	Owner                                             *ProgressOwner
	ReportSequence, AcceptedSequence, AcceptedOrdinal *int64 // distinct actual owner counters
	Range                                             *ProgressRange
	Disposition                                       string // captured, coalesced, pending, refused, error, unknown
	Physical                                          *ProgressPhysical
	NonPhysicalReason                                 string // required for a genuinely coalesced, nonwriter capture
	MissingReason                                     string
}

type ProgressCaptureEdge struct {
	SourceID                                          string
	ReportSequence, AcceptedSequence, AcceptedOrdinal *int64
	Consumed                                          *ProgressRange
}

type ProgressDrainFact struct {
	AcceptedCaptureID                                 string // actual native pending source, including later split pieces
	DrainID                                           string
	Owner                                             *ProgressOwner
	Physical                                          *ProgressPhysical
	ReportSequence, AcceptedSequence, AcceptedOrdinal *int64
	Consumed                                          *ProgressRange
	PendingAfter                                      *bool
	PendingCursorAfter                                *int64 // null after native final clear, NOT fabricated zero
	CreditedMS                                        *int64 // actual native credit, never inferred from edge lengths
	Edges                                             []ProgressCaptureEdge
	MissingReason                                     string
}

type ProgressEdgeDisposition struct {
	SourceID    string
	Consumed    ProgressRange
	Pending     *ProgressRange
	Disposition string // pending or drained, based on exact accepted edge
}

type ProgressFact struct {
	Capture *ProgressCaptureFact
	Drain   *ProgressDrainFact
	Linkage *ProgressLinkStatus
}

type ProgressLinkStatus struct {
	Complete             bool // THIS linkage check only, never native/whole-run qualification
	MissingReason        string
	PendingSources       int
	TrackedPendingOwners int
	Edges                []ProgressEdgeDisposition
}

type progressBinding struct {
	owner                     ProgressOwner
	report, accepted, ordinal int64
	rangeMS                   ProgressRange
	cursor                    int64
}

type progressPending struct {
	fact   ProgressCaptureFact
	cursor int64
}

// ProgressLinks keeps only bounded source IDs/counters/ranges, no report or
// result payload ledger. Successful edge checks retire fully consumed sources;
// scalar totals never reconcile a gap. One instance binds one actual writer.
type ProgressLinks struct {
	pending                  map[string]progressPending
	ownerReports             map[ProgressOwner]int64
	bindings                 map[string]progressBinding
	seenCaptures, seenDrains map[string]struct{}
	lastPhysical             uint64
	Captures, Drains         uint64
	stopped                  string
}

func NewProgressLinks() *ProgressLinks {
	return &ProgressLinks{pending: map[string]progressPending{}, ownerReports: map[ProgressOwner]int64{}, bindings: map[string]progressBinding{}, seenCaptures: map[string]struct{}{}, seenDrains: map[string]struct{}{}}
}

func progressRange(r *ProgressRange) bool { return r != nil && r.FromMS >= 0 && r.UntilMS > r.FromMS }
func progressOwner(o *ProgressOwner) bool {
	return o != nil && o.PlaybackID != "" && len(o.PlaybackID) <= 1024 && o.CommandIncarnation != ([16]byte{}) && o.ClockIncarnation != ([16]byte{}) && o.OccurrenceGeneration > 0 && o.ActivityGeneration > 0 && o.RememberGeneration > 0
}
func progressCounters(report, accepted, ordinal *int64) bool {
	return report != nil && accepted != nil && ordinal != nil && *report >= 0 && *accepted >= -1 && *ordinal >= 0
}
func progressID(id string) bool { return id != "" && len(id) <= 256 }
func progressPhysical(p *ProgressPhysical) bool {
	return p != nil && progressID(p.UnitID) && p.Ordinal > 0
}
func copyInt(p *int64) *int64 {
	if p == nil {
		return nil
	}
	n := *p
	return &n
}

func (s ProgressLinkStatus) Err() error {
	if s.Complete {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrProgressLinkage, s.MissingReason)
}

func (l *ProgressLinks) fail(reason string) ProgressLinkStatus {
	if l.stopped == "" {
		l.stopped = reason
	}
	return l.status(false, l.stopped, nil)
}

func (l *ProgressLinks) Capture(f ProgressCaptureFact) ProgressLinkStatus {
	l.Captures++
	if l.stopped != "" {
		return l.fail(l.stopped)
	}
	if !progressID(f.SourceID) || !progressOwner(f.Owner) || !progressCounters(f.ReportSequence, f.AcceptedSequence, f.AcceptedOrdinal) || !progressRange(f.Range) {
		return l.fail("capture source/owner/counters/range unavailable: " + f.MissingReason)
	}
	if f.Disposition != "captured" && f.Disposition != "coalesced" && f.Disposition != "pending" {
		return l.fail("capture " + f.Disposition + ": " + f.MissingReason)
	}
	if _, ok := l.seenCaptures[f.SourceID]; ok {
		return l.fail("duplicate capture source ID")
	}
	if len(l.seenCaptures) == MaxObservedIDs {
		return l.fail("capture ID bound exceeded; retain overflowing raw capture")
	}
	if len(l.pending) == MaxPendingCaptures {
		return l.fail("pending capture bound exceeded; retain overflowing raw capture")
	}
	var binding progressBinding
	if f.Physical == nil {
		if f.Disposition != "coalesced" || f.NonPhysicalReason == "" || f.PendingRange != nil || f.PendingCursor != nil {
			return l.fail("raw coalescing is not an accepted pending replacement")
		}
		var found bool
		binding, found = l.bindings[f.AcceptedCaptureID]
		if !found || binding.owner != *f.Owner || *f.AcceptedSequence != binding.accepted || *f.AcceptedOrdinal != binding.ordinal {
			return l.fail("coalesced source lacks actual accepted pending parent")
		}
	} else {
		if f.Disposition == "coalesced" || !progressPhysical(f.Physical) || f.Physical.Ordinal <= l.lastPhysical || f.AcceptedCaptureID != f.SourceID || !progressRange(f.PendingRange) || f.PendingCursor == nil || *f.PendingCursor != f.PendingRange.FromMS {
			return l.fail("capture physical/pending snapshot unavailable or contradictory")
		}
		for _, old := range l.bindings {
			// Pending belongs to the native claim key. A new clock/proof
			// generation cannot replace an undrained claim's obligation.
			if old.owner.PlaybackID == f.Owner.PlaybackID && old.owner.CommandIncarnation == f.Owner.CommandIncarnation {
				return l.fail("capture cannot replace undrained native pending")
			}
		}
		last, known := l.ownerReports[*f.Owner]
		if known && *f.ReportSequence <= last {
			return l.fail("physical capture report sequence did not advance")
		}
		if !known && len(l.ownerReports) == MaxPendingCaptures {
			return l.fail("owner lineage bound exceeded")
		}
		binding = progressBinding{*f.Owner, *f.ReportSequence, *f.AcceptedSequence, *f.AcceptedOrdinal, *f.PendingRange, *f.PendingCursor}
	}
	if f.Range.FromMS < binding.cursor || f.Range.UntilMS > binding.rangeMS.UntilMS {
		return l.fail("capture contribution exceeds actual accepted pending range/cursor")
	}

	// Freeze only the observed small values. Caller buffer mutation cannot rebase
	// a subsequent drain onto new counters or ranges.
	owner, r := *f.Owner, *f.Range
	f.Owner, f.Range = &owner, &r
	if f.PendingRange != nil {
		pending := *f.PendingRange
		f.PendingRange = &pending
	}
	f.PendingCursor = copyInt(f.PendingCursor)
	f.ReportSequence, f.AcceptedSequence, f.AcceptedOrdinal = copyInt(f.ReportSequence), copyInt(f.AcceptedSequence), copyInt(f.AcceptedOrdinal)
	if f.Physical != nil {
		physical := *f.Physical
		f.Physical = &physical
		l.lastPhysical = physical.Ordinal
	}
	l.pending[f.SourceID] = progressPending{f, r.FromMS}
	l.seenCaptures[f.SourceID] = struct{}{}
	if f.Physical != nil {
		l.ownerReports[owner] = *f.ReportSequence
		l.bindings[f.AcceptedCaptureID] = binding
	}
	return l.status(true, "", nil)
}

func (l *ProgressLinks) Drain(d ProgressDrainFact) ProgressLinkStatus {
	l.Drains++
	if l.stopped != "" {
		return l.fail(l.stopped)
	}
	if !progressID(d.DrainID) || !progressOwner(d.Owner) || !progressPhysical(d.Physical) || d.Physical.Ordinal <= l.lastPhysical ||
		!progressCounters(d.ReportSequence, d.AcceptedSequence, d.AcceptedOrdinal) || !progressRange(d.Consumed) || d.PendingAfter == nil || len(d.Edges) == 0 || len(d.Edges) > MaxDrainEdges {
		return l.fail("drain owner/physical/counters/consumed-range/edges unavailable: " + d.MissingReason)
	}
	if _, duplicate := l.seenDrains[d.DrainID]; duplicate {
		return l.fail("duplicate physical drain ID")
	}
	if len(l.seenDrains) == MaxObservedIDs {
		return l.fail("drain ID bound exceeded; retain overflowing raw drain")
	}

	binding, found := l.bindings[d.AcceptedCaptureID]
	if !found || binding.owner != *d.Owner || *d.ReportSequence != binding.report || *d.AcceptedSequence != binding.accepted || *d.AcceptedOrdinal != binding.ordinal || d.Consumed.FromMS != binding.cursor || d.Consumed.UntilMS > binding.rangeMS.UntilMS {
		return l.fail("drain pending counters/range not bound to actual accepted capture")
	}
	if d.CreditedMS == nil || *d.CreditedMS < 0 {
		return l.fail("actual nonnegative native credited milliseconds unavailable")
	}
	if d.Consumed.FromMS/FirstDrainQuarterMS != (d.Consumed.UntilMS-1)/FirstDrainQuarterMS {
		return l.fail("FIRST drain crosses a UTC quarter")
	}
	if *d.PendingAfter != (d.Consumed.UntilMS < binding.rangeMS.UntilMS) {
		return l.fail("native pending disposition contradicts actual accepted range")
	}

	if *d.PendingAfter {
		if d.PendingCursorAfter == nil || *d.PendingCursorAfter != d.Consumed.UntilMS {
			return l.fail("pending cursor does not match actual consumed endpoint")
		}
	} else if d.PendingCursorAfter != nil {
		return l.fail("final native cursor was not NULL")
	}
	next := map[string]int64{}
	intervals := make([]ProgressRange, 0, len(d.Edges))
	dispositions := make([]ProgressEdgeDisposition, 0, len(d.Edges))
	for _, edge := range d.Edges {
		source, ok := l.pending[edge.SourceID]
		if !ok || source.fact.AcceptedCaptureID != d.AcceptedCaptureID || source.fact.Owner == nil || *source.fact.Owner != *d.Owner || !progressCounters(edge.ReportSequence, edge.AcceptedSequence, edge.AcceptedOrdinal) || !progressRange(edge.Consumed) {
			return l.fail("missing or cross-owner capture edge")
		}
		if _, duplicate := next[edge.SourceID]; duplicate {
			return l.fail("duplicate capture edge in drain")
		}
		if *edge.ReportSequence != *source.fact.ReportSequence || *edge.AcceptedSequence != *source.fact.AcceptedSequence || *edge.AcceptedOrdinal != *source.fact.AcceptedOrdinal ||
			edge.Consumed.FromMS != source.cursor || edge.Consumed.UntilMS > source.fact.Range.UntilMS || edge.Consumed.FromMS < d.Consumed.FromMS || edge.Consumed.UntilMS > d.Consumed.UntilMS {
			return l.fail("capture edge counters/range/cursor mismatch")
		}
		next[edge.SourceID] = edge.Consumed.UntilMS
		intervals = append(intervals, *edge.Consumed)
		out := ProgressEdgeDisposition{SourceID: edge.SourceID, Consumed: *edge.Consumed, Disposition: "drained"}
		if edge.Consumed.UntilMS < source.fact.Range.UntilMS {
			remaining := ProgressRange{edge.Consumed.UntilMS, source.fact.Range.UntilMS}
			out.Pending = &remaining
			out.Disposition = "pending"
		}
		dispositions = append(dispositions, out)
	}
	// Coalesced captures may overlap. Check the exact UNION of supplied edges,
	// not their count or summed lengths, against the native consumed interval.
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].FromMS < intervals[j].FromMS })
	cursor := d.Consumed.FromMS
	for _, interval := range intervals {
		if interval.FromMS > cursor {
			return l.fail("unlinked consumed-range gap")
		}
		if interval.UntilMS > cursor {
			cursor = interval.UntilMS
		}
	}
	if cursor != d.Consumed.UntilMS {
		return l.fail("unlinked consumed-range tail")
	}
	if !*d.PendingAfter {
		for id, source := range l.pending {
			if *source.fact.Owner != *d.Owner {
				continue
			}
			cursor := source.cursor
			if until, ok := next[id]; ok {
				cursor = until
			}
			if cursor < source.fact.Range.UntilMS {
				return l.fail("native pending cleared with unresolved capture edges")
			}
		}
	}
	for id, cursor := range next {
		source := l.pending[id]
		if cursor == source.fact.Range.UntilMS {
			delete(l.pending, id)
		} else {
			source.cursor = cursor
			l.pending[id] = source
		}
	}
	l.seenDrains[d.DrainID] = struct{}{}
	binding.cursor = d.Consumed.UntilMS
	if *d.PendingAfter {
		l.bindings[d.AcceptedCaptureID] = binding
	} else {
		delete(l.bindings, d.AcceptedCaptureID)
	}
	l.lastPhysical = d.Physical.Ordinal
	return l.status(true, "", dispositions)
}

func (l *ProgressLinks) status(complete bool, reason string, edges []ProgressEdgeDisposition) ProgressLinkStatus {
	pendingOwners := len(l.bindings)
	return ProgressLinkStatus{Complete: complete, MissingReason: reason, PendingSources: len(l.pending), TrackedPendingOwners: pendingOwners, Edges: edges}
}

func (l *ProgressLinks) Coverage() ProgressLinkStatus {
	if l.stopped != "" {
		return l.fail(l.stopped)
	}
	pendingOwner := len(l.bindings) > 0
	if l.Captures == 0 || l.Drains == 0 || len(l.pending) != 0 || pendingOwner {
		return l.status(false, fmt.Sprintf("unresolved lineage: captures=%d drains=%d tracked pending=%d", l.Captures, l.Drains, len(l.pending)), nil)
	}
	return l.status(true, "", nil)
}
