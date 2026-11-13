package db_test

import (
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"testing"
)

func TestSegmentReviewKeepsOwnerDecisionsThroughDetection(t *testing.T) {
	l := newLibrary(t, 60)
	entries, err := l.d.SegmentEntries(ctx, l.libs[0].PublicID, "", 0, 25)
	if err != nil || len(entries) == 0 {
		t.Fatal(entries, err)
	}
	e := entries[0]
	found := []db.SegmentMarker{{Kind: db.MarkerIntro, Source: db.MarkerConfirmed, StartMS: 1000, EndMS: 2000, Confidence: .99, Provenance: "season_audio_match:intro:v1"}}
	save := func() {
		t.Helper()
		if err := l.d.SaveSegmentAnalysis(ctx, e, "standard", "complete", "", []byte(`{}`), found); err != nil {
			t.Fatal(err)
		}
	}
	save()
	fresh := func() db.SegmentEntry {
		t.Helper()
		rows, err := l.d.SegmentEntries(ctx, e.LibraryID, e.TopID, e.MediaID-1, 1)
		if err != nil || len(rows) != 1 {
			t.Fatal(rows, err)
		}
		return rows[0]
	}
	e = fresh()
	if e.SourceKey != e.Key() {
		t.Fatal("source snapshot differs", e)
	}
	markers, err := l.d.SegmentMarkers(ctx, e.MediaID)
	if err != nil || len(markers) != 1 {
		t.Fatal(markers, err)
	}
	change := db.SegmentChange{Entry: e, SourceKey: e.Key(), Revision: e.Revision, Action: "dismiss", Marker: markers[0]}
	if err := l.d.ChangeSegmentMarker(ctx, change); err != nil {
		t.Fatal(err)
	}
	if err := l.d.ChangeSegmentMarker(ctx, change); err != db.ErrConflict {
		t.Fatal("an old review overwrote a later decision", err)
	}
	if _, err := l.d.RedetectSegments(ctx, e.LibraryID, e.TopID); err != nil {
		t.Fatal(err)
	}
	if err := l.d.SaveSegmentComparison(ctx, e, "standard", []byte(`{}`), found); err != nil {
		t.Fatal(err)
	}
	if got := fresh(); got.SourceKey != "" {
		t.Fatal("a stale comparison cancelled Detect again", got)
	}
	save()
	markers, err = l.d.SegmentMarkers(ctx, e.MediaID)
	if err != nil || len(markers) != 1 || !markers[0].Dismissed || markers[0].Source != db.MarkerFromPerson {
		t.Fatal("detection lost the owner's dismissal", markers, err)
	}
	detail, err := l.d.Item(ctx, l.all, e.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range detail.Versions {
		for _, m := range v.Markers {
			if m.Kind == db.MarkerIntro {
				t.Fatal("a dismissed marker reached a viewer", m)
			}
		}
	}
	e = fresh()
	change = db.SegmentChange{Entry: e, SourceKey: e.Key(), Revision: e.Revision, Action: "edit", Marker: markers[0]}
	change.Marker.StartMS, change.Marker.EndMS = 3000, 4000
	if err := l.d.ChangeSegmentMarker(ctx, change); err != nil {
		t.Fatal(err)
	}
	save()
	markers, _ = l.d.SegmentMarkers(ctx, e.MediaID)
	if len(markers) != 1 || markers[0].Dismissed || markers[0].StartMS != 3000 || markers[0].EndMS != 4000 {
		t.Fatal("detection replaced an adjusted marker", markers)
	}
	c, m, err := l.d.SegmentCounts(ctx, e.LibraryID, e.TopID, "standard")
	if err != nil || c.Complete == 0 || m.Approved != 1 || m.Automatic != 0 {
		t.Fatal(c, m, err)
	}
	e = fresh()
	e.Size++
	if err := l.d.ChangeSegmentMarker(ctx, db.SegmentChange{Entry: e, SourceKey: e.Key(), Revision: e.Revision, Action: "approve", Marker: markers[0]}); err != db.ErrConflict {
		t.Fatal("a stale source could change markers", err)
	}
}
