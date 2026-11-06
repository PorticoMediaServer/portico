package scanclock

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/read"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// All records/times below are FABRICATED correctness controls, not measured
// scan, writer, browse, helper or hardware evidence.
func control(t *testing.T) (*Clock, time.Time, writer.ScanReceiptRequest, writer.InventoryResult, read.BrowseInput) {
	t.Helper()
	start := time.Now()
	c, err := New(start, Manifest{Run: "run", Database: "database", ClockDomain: "test-process", FixtureSHA256: strings.Repeat("a", 64), SourceSHA: strings.Repeat("b", 40), BinarySHA256: strings.Repeat("c", 64), HostEpoch: "fabricated", MediaProfile: "control"})
	if err != nil {
		t.Fatal(err)
	}
	q := writer.ScanReceiptRequest{RunID: "run", LibraryID: 7, LibraryKey: "library", Receipt: writer.ReceiptReference{Identity: writer.CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, RequestDigest: writer.RequestDigest{3}}}
	result := writer.InventoryResult{RunID: "run", Outcome: writer.ReceiptOutcome{State: writer.Applied, Reference: q.Receipt}, AcceptedRunRevision: 2, SourceID: "source", SourceIncarnation: "incarnation", SourceRevision: "opaque", NextCursor: "123", ResultCount: 1, Digest: writer.ResultDigest{4}, Members: []writer.InventoryMember{{ObjectID: "object", LibraryID: 7, SubjectID: 31, AssetID: 41, AcceptedAssetRevision: 1}}}
	input := read.BrowseInput{Library: 7, Database: "database", TokenHash: "actual-trusted-input"}
	return c, start, q, result, input
}
func TestScanClockKeepsVisibilityNullWithoutCurrentWitness(t *testing.T) {
	c, start, q, result, input := control(t)
	page := read.BrowsePage{Items: []read.BrowseItem{{ID: 31, Title: "Filename", Revision: 1}}}
	if err := c.Browse(start.Add(time.Millisecond), input, page, nil); !errors.Is(err, ErrObservation) {
		t.Fatal("discovery alone became visibility", err)
	}
	if err := c.Applied(start.Add(5*time.Millisecond), q, result, nil); err != nil {
		t.Fatal(err)
	}
	if r := c.Report(); r.FirstFilenameVisibleNS != nil || r.AppliedObservedNS == nil || *r.AppliedObservedNS != 5e6 {
		t.Fatal("commit became visibility", r)
	}
	// None has a current original-receipt/object/asset/info_epoch witness.
	// Even the unchanged row must stay null if the original stops resolving.
	for _, item := range []read.BrowseItem{
		{ID: 32, PublicID: [16]byte{1}, Title: "Other", Revision: 1},
		{ID: 31, PublicID: [16]byte{1}, Title: "Filename", Revision: 1},
		{ID: 31, PublicID: [16]byte{2}, Title: "Replacement title", Revision: 999},
	} {
		if err := c.Browse(start.Add(8*time.Millisecond), input, read.BrowsePage{Items: []read.BrowseItem{item}}, nil); !errors.Is(err, ErrObservation) {
			t.Fatal("browse row substituted for current witness", item, err)
		}
		if r := c.Report(); r.FilenameObserved || r.FirstFilenameVisibleNS != nil || !strings.Contains(strings.Join(r.Missing, "\n"), "current public-facade witness") {
			t.Fatal("unjoined row became visibility", item, r)
		}
	}
	r := c.Report()
	if r.OriginalReference == nil || *r.OriginalReference != q.Receipt || r.TargetSubjectID != result.Members[0].SubjectID || r.AllFilenameVisibleNS != nil || r.FirstBasicNS != nil || r.AllBasicNS != nil || r.Qualification || r.Population != "one_fresh_n1_target" {
		t.Fatal(r)
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"first_filename_visible_ns":null`) || !strings.Contains(string(raw), `"all_filename_visible_ns":null`) || !strings.Contains(string(raw), `"first_basic_ns":null`) {
		t.Fatal("missing clock became zero", string(raw))
	}
}

func TestScanClockRejectsNonPositiveIntervalsWithReason(t *testing.T) {
	for _, tc := range []struct {
		name            string
		applied, browse time.Duration
		appliedInvalid  bool
		reason          string
	}{
		{"Applied_at_origin", 0, 0, true, "non-positive traversal-offer-to-observation"},
		{"Applied_before_origin", -time.Nanosecond, 0, true, "non-positive traversal-offer-to-observation"},
		{"browse_at_origin", time.Millisecond, 0, false, "non-positive traversal-offer-to-observation"},
		{"browse_at_Applied", time.Millisecond, time.Millisecond, false, "non-positive Applied-to-browse"},
		{"browse_before_Applied", 2 * time.Millisecond, time.Millisecond, false, "non-positive Applied-to-browse"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, start, q, result, input := control(t)
			err := c.Applied(start.Add(tc.applied), q, result, nil)
			if tc.appliedInvalid {
				if !errors.Is(err, ErrObservation) || c.Report().AppliedObservedNS != nil {
					t.Fatal("non-positive Applied interval recorded", err, c.Report())
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				page := read.BrowsePage{Items: []read.BrowseItem{{ID: 31, Title: "Filename", Revision: 1}}}
				if err := c.Browse(start.Add(tc.browse), input, page, nil); !errors.Is(err, ErrObservation) {
					t.Fatal("non-positive browse interval accepted", err)
				}
			}
			r := c.Report()
			if r.FirstFilenameVisibleNS != nil || r.FilenameObserved || !strings.Contains(strings.Join(r.Missing, "\n"), tc.reason) {
				t.Fatal("non-positive interval lacks null/reason", r)
			}
		})
	}
}
func TestScanClockRejectsErrorReplayMissingAndWrongOriginal(t *testing.T) {
	for _, name := range []string{"error", "zero", "replay", "reference", "cohort", "scope", "cursor", "clock"} {
		t.Run(name, func(t *testing.T) {
			c, start, q, r, _ := control(t)
			at := start.Add(time.Millisecond)
			var callErr error
			switch name {
			case "error":
				callErr = errors.New("uncertain call")
			case "zero":
				r = writer.InventoryResult{}
			case "replay":
				r.Outcome.Replayed = true
			case "reference":
				r.Outcome.Reference.Identity.Sequence++
			case "cohort":
				r.ResultCount = 0
			case "scope":
				r.Members[0].LibraryID++
			case "cursor":
				r.NextCursor = ""
			case "clock":
				at = at.Round(0)
			}
			if err := c.Applied(at, q, r, callErr); !errors.Is(err, ErrObservation) {
				t.Fatal("unproved Applied accepted", err)
			}
			if c.Report().AppliedObserved || c.Report().FirstFilenameVisibleNS != nil {
				t.Fatal("invalid record created milestone")
			}
		})
	}
}
func TestScanClockRejectsWrongBrowseAndMissingClockOrigin(t *testing.T) {
	c, start, q, r, input := control(t)
	if _, err := New(start.Round(0), c.manifest); err == nil {
		t.Fatal("wall clock accepted as process clock")
	}
	if err := c.Applied(start.Add(time.Millisecond), q, r, nil); err != nil {
		t.Fatal(err)
	}
	input.Library++
	page := read.BrowsePage{Items: []read.BrowseItem{{ID: 31, Title: "Filename", Revision: 1}}}
	if err := c.Browse(start.Add(2*time.Millisecond), input, page, nil); err == nil {
		t.Fatal("cross-library row accepted")
	}
	input.Library--
	input.Database = "other-database"
	if err := c.Browse(start.Add(2*time.Millisecond), input, page, nil); err == nil {
		t.Fatal("cross-database row accepted")
	}
	input.Database = "database"
	if err := c.Browse(start.Add(2*time.Millisecond), input, page, errors.New("read failed")); err == nil {
		t.Fatal("error plus rows accepted")
	}
	if c.Report().FirstFilenameVisibleNS != nil {
		t.Fatal("invalid browse set clock")
	}
}
