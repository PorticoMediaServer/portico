package progresslineage

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func number(n int64) *int64 { return &n }
func yes(v bool) *bool      { return &v }
func owner() *ProgressOwner {
	return &ProgressOwner{PlaybackID: "SYNTHETIC-playback", CommandIncarnation: [16]byte{1}, ClockIncarnation: [16]byte{2}, OccurrenceGeneration: 5, ActivityGeneration: 7, RememberGeneration: 11}
}
func capture(id string, seq, from, until int64, physical uint64) ProgressCaptureFact {
	f := ProgressCaptureFact{SourceID: id, Owner: owner(), ReportSequence: number(seq), AcceptedSequence: number(17), AcceptedOrdinal: number(71), Range: &ProgressRange{from, until}, Disposition: "captured"}
	if physical > 0 {
		f.Physical = &ProgressPhysical{UnitID: "writer/" + id, Ordinal: physical}
		f.AcceptedCaptureID = id
		f.PendingRange = &ProgressRange{from, until}
		f.PendingCursor = number(from)
	} else {
		f.Disposition = "coalesced"
		f.AcceptedCaptureID = "a"
		f.NonPhysicalReason = "fabricated logical coalescing control; no physical writer call"
	}
	return f
}
func edge(f ProgressCaptureFact, from, until int64) ProgressCaptureEdge {
	return ProgressCaptureEdge{SourceID: f.SourceID, ReportSequence: copyInt(f.ReportSequence), AcceptedSequence: copyInt(f.AcceptedSequence), AcceptedOrdinal: copyInt(f.AcceptedOrdinal), Consumed: &ProgressRange{from, until}}
}
func drain(id string, physical uint64, from, until int64, pending bool, edges ...ProgressCaptureEdge) ProgressDrainFact {
	d := ProgressDrainFact{AcceptedCaptureID: "a", DrainID: id, Owner: owner(), Physical: &ProgressPhysical{id, physical}, ReportSequence: number(10), AcceptedSequence: number(17), AcceptedOrdinal: number(71), Consumed: &ProgressRange{from, until}, PendingAfter: yes(pending), CreditedMS: number(42), Edges: edges}
	if pending {
		d.PendingCursorAfter = number(until)
	}
	return d
}

func TestProgressLineageCoalescedUnionAndSplitDrain(t *testing.T) {
	l := NewProgressLinks()
	a, b, c := capture("a", 10, 0, 2000, 1), capture("b", 11, 0, 1000, 0), capture("c", 12, 2000, 3000, 0)
	a.PendingRange = &ProgressRange{0, 3000}
	for _, f := range []ProgressCaptureFact{a, b, c} {
		if s := l.Capture(f); !s.Complete {
			t.Fatal(s)
		}
	}
	first := l.Drain(drain("drain/4", 4, 0, 1500, true, edge(a, 0, 1500), edge(b, 0, 1000)))
	if !first.Complete || first.PendingSources != 2 || len(first.Edges) != 2 || first.Edges[0].Disposition != "pending" || *first.Edges[0].Pending != (ProgressRange{1500, 2000}) || first.Edges[1].Disposition != "drained" || first.Edges[1].Pending != nil {
		t.Fatal(first)
	}
	if l.Coverage().Complete {
		t.Fatal("partial drain turned into complete lineage")
	}
	second := drain("drain/7", 7, 1500, 3000, false, edge(a, 1500, 2000), edge(c, 2000, 3000))
	if s := l.Drain(second); !s.Complete || s.PendingSources != 0 {
		t.Fatal(s)
	}
	if !l.Coverage().Complete || l.Captures != 3 || l.Drains != 2 || *second.CreditedMS != 42 {
		t.Fatal("counts or edge length replaced actual native credit")
	}
	raw, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"PendingCursorAfter":null`) {
		t.Fatal("final NULL cursor fabricated as zero")
	}
}

func TestProgressLineageMissingOrCrossOwnerEdgesAreSticky(t *testing.T) {
	for _, failure := range []string{"missing_tail", "source_clock", "counter", "unknown"} {
		t.Run(failure, func(t *testing.T) {
			l := NewProgressLinks()
			a := capture("a", 10, 0, 2000, 1)
			if failure == "unknown" {
				a.Range = nil
				a.ReportSequence = nil
				a.Disposition = "unknown"
				a.MissingReason = "actual owner view unavailable"
				status := l.Capture(a)
				raw, err := json.Marshal(ProgressFact{Capture: &a, Linkage: &status})
				if err != nil {
					t.Fatal(err)
				}
				if status.Complete || !errors.Is(status.Err(), ErrProgressLinkage) || !strings.Contains(string(raw), `"Range":null`) {
					t.Fatal("unknown source repaired", status)
				}
				if l.Capture(capture("later", 11, 0, 1000, 2)).Complete {
					t.Fatal("unknown source did not stop lineage")
				}
				return
			}
			if !l.Capture(a).Complete {
				t.Fatal("seed capture")
			}
			d := drain("d", 2, 0, 2000, false, edge(a, 0, 2000))
			switch failure {
			case "missing_tail":
				d.Edges[0].Consumed.UntilMS = 1000
			case "source_clock":
				d.Owner.ClockIncarnation[0]++
			case "counter":
				d.Edges[0].AcceptedSequence = number(99)
			}
			failed := l.Drain(d)
			if failed.Complete || failed.MissingReason == "" || failed.PendingSources != 1 || !errors.Is(failed.Err(), ErrProgressLinkage) {
				t.Fatal("invalid edge accepted", failed)
			}
			if l.Drain(drain("fixed", 3, 0, 2000, false, edge(a, 0, 2000))).Complete {
				t.Fatal("later repaired record erased original failure")
			}
		})
	}
}

func TestProgressLineageFreezesInputsAndRejectsRetiredIDReuse(t *testing.T) {
	l := NewProgressLinks()
	a := capture("a", 10, 0, 1000, 1)
	original := edge(a, 0, 1000)
	if !l.Capture(a).Complete {
		t.Fatal("seed")
	}
	*a.ReportSequence = 999
	a.Range.UntilMS = 9000
	a.Owner.ClockIncarnation[0]++
	if s := l.Drain(drain("d", 2, 0, 1000, false, original)); !s.Complete {
		t.Fatal("caller rewrote retained metadata", s)
	}
	if !l.Coverage().Complete {
		t.Fatal("known completion unavailable")
	}
	if l.Capture(capture("a", 11, 1000, 2000, 3)).Complete {
		t.Fatal("retired source ID reused")
	}
}

func TestProgressLineageOverflowKeepsCoverageIncomplete(t *testing.T) {
	l := NewProgressLinks()
	for i := 0; i < MaxPendingCaptures; i++ {
		physical := uint64(0)
		if i == 0 {
			physical = 1
		}
		f := capture(fmt.Sprint(i), int64(i), 0, 1000, physical)
		f.AcceptedCaptureID = "0"
		if s := l.Capture(f); !s.Complete {
			t.Fatal(s)
		}
	}
	extra := capture("overflow", MaxPendingCaptures, 0, 1000, MaxPendingCaptures+1)
	s := l.Capture(extra)
	if s.Complete || s.PendingSources != MaxPendingCaptures || l.Captures != MaxPendingCaptures+1 || !strings.Contains(s.MissingReason, "bound exceeded") {
		t.Fatal("overflow silently dropped", s)
	}
	if l.Coverage().Complete {
		t.Fatal("overflow is passing subset")
	}
}

func TestProgressLineageRequiresNativeCreditBindingAndQuarter(t *testing.T) {
	for _, fault := range []string{"nil_credit", "negative_credit", "pending_counter", "quarter", "pending_replacement", "raw_coalescing"} {
		t.Run(fault, func(t *testing.T) {
			l := NewProgressLinks()
			a := capture("a", 10, 0, 1000, 1)
			if fault == "quarter" {
				a.Range = &ProgressRange{FirstDrainQuarterMS - 1, FirstDrainQuarterMS + 1}
				a.PendingRange = &ProgressRange{a.Range.FromMS, a.Range.UntilMS}
				a.PendingCursor = number(a.Range.FromMS)
			}
			if !l.Capture(a).Complete {
				t.Fatal("setup")
			}
			d := drain("d", 2, a.Range.FromMS, a.Range.UntilMS, false, edge(a, a.Range.FromMS, a.Range.UntilMS))
			switch fault {
			case "nil_credit":
				d.CreditedMS = nil
			case "negative_credit":
				d.CreditedMS = number(-1)
			case "pending_counter":
				d.ReportSequence = number(999)
			case "pending_replacement":
				replacement := capture("new", 11, 1000, 2000, 2)
				replacement.Owner.ClockIncarnation[0]++
				replacement.Owner.OccurrenceGeneration++
				replacement.Owner.ActivityGeneration++
				replacement.Owner.RememberGeneration++
				if l.Capture(replacement).Complete {
					t.Fatal("raw new capture replaced accepted pending")
				}
				return
			case "raw_coalescing":
				if l.Capture(capture("alias", 11, 0, 2000, 0)).Complete {
					t.Fatal("coalescing extended actual accepted range")
				}
				return
			}
			status := l.Drain(d)
			if status.Complete || status.PendingSources != 1 {
				t.Fatal("native domain gap passed", status)
			}
			if fault == "quarter" && !strings.Contains(status.MissingReason, "quarter") {
				t.Fatal("quarter control did not reach quarter check", status)
			}
		})
	}
}
