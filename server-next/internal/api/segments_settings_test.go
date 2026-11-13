package api

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/segmentdetect"
)

func TestSegmentConfidenceAndChapterSettingsReachPlayback(t *testing.T) {
	sh := newShelf(t, "Film.2020.mkv")
	engine := &fakeEngine{}
	sh.s.playback.SetEngine(engine, engine)
	sh.s.settings.PauseBackground(sh.s.now(), true)
	ctx := context.Background()
	entries, err := sh.s.db.SegmentEntries(ctx, sh.library, "", 0, 1)
	if err != nil || len(entries) != 1 {
		t.Fatal(entries, err)
	}
	e := entries[0]
	found := []db.SegmentMarker{
		{Kind: db.MarkerIntro, Source: db.MarkerConfirmed, StartMS: 1000, EndMS: 2000, Confidence: .8, Provenance: segmentdetect.IntroProvenance},
		{Kind: db.MarkerCredits, Source: db.MarkerFromAnalysis, StartMS: 3000, EndMS: 4000, Confidence: .65, Provenance: segmentdetect.ChapterProvenance},
	}
	if err := sh.s.db.SaveSegmentAnalysis(ctx, e, "standard", "complete", "", []byte(`{}`), found); err != nil {
		t.Fatal(err)
	}
	read := func() PlaybackOptions {
		t.Helper()
		return get[PlaybackOptions](t, sh.s, sh.owner, "/v1/items/"+e.ItemID+"/playback-options")
	}
	set := func(change map[string]any) {
		t.Helper()
		if w := call(t, sh.s, "PUT", "/v1/admin/settings/segments", sh.owner, change, nil); w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
	}
	if got := read(); len(got.Markers) != 2 || got.Markers[0].AutomaticSafe || got.Markers[1].AutomaticSafe {
		t.Fatal("default confidence did not distinguish suggestions from safe skips", got.Markers)
	}
	set(map[string]any{"automaticConfidencePercent": 70, "viewerConfidencePercent": 70})
	if got := read(); len(got.Markers) != 1 || !got.Markers[0].AutomaticSafe {
		t.Fatal("confidence floors did not apply without remeasurement", got.Markers)
	}
	set(map[string]any{"viewerConfidencePercent": 30, "useChapterNames": false})
	if got := read(); len(got.Markers) != 1 || got.Markers[0].Type != "intro" {
		t.Fatal("chapter suggestions still reach viewers", got.Markers)
	}
	set(map[string]any{"mode": "off"})
	if got := read(); len(got.Markers) != 0 {
		t.Fatal("off still offers skip buttons", got.Markers)
	}
	if w := call(t, sh.s, "PUT", "/v1/admin/settings/segments", sh.owner, map[string]any{"keepDays": 7}, nil); w.Code != 422 {
		t.Fatal("an inert history setting was accepted", w.Code, w.Body)
	}
}

func TestSegmentRetryOnlyQueuesUnreadableFiles(t *testing.T) {
	s, owner, _ := seededWith(t, 60, 0)
	s.settings.PauseBackground(s.now(), true)
	libs, err := s.db.AllLibraries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	entries, err := s.db.SegmentEntries(context.Background(), libs[0].PublicID, "", 0, 3)
	if err != nil || len(entries) != 3 {
		t.Fatal(entries, err)
	}
	for i, e := range entries {
		state := []string{"failed", "complete", "unsupported"}[i]
		if err := s.db.SaveSegmentAnalysis(context.Background(), e, "standard", state, "", []byte(`{}`), nil); err != nil {
			t.Fatal(err)
		}
	}
	var changed SegmentChanged
	if w := call(t, s, "POST", "/v1/admin/segments/retry", owner, map[string]any{}, &changed); w.Code != 200 || changed.Changed != 1 {
		t.Fatal(w.Code, w.Body)
	}
	fresh, err := s.db.SegmentEntries(context.Background(), libs[0].PublicID, "", 0, 3)
	if err != nil || fresh[0].SourceKey != "" || fresh[1].SourceKey != fresh[1].Key() || fresh[2].SourceKey != fresh[2].Key() {
		t.Fatal("retry changed a readable or unsupported measurement", fresh, err)
	}
	if w := call(t, s, "POST", "/v1/admin/segments/redetect", owner, map[string]any{}, &changed); w.Code != 200 || changed.Changed < 3 {
		t.Fatal(w.Code, w.Body)
	}
	fresh, err = s.db.SegmentEntries(context.Background(), libs[0].PublicID, "", 0, 3)
	if err != nil || fresh[1].SourceKey != "" || fresh[2].SourceKey != "" {
		t.Fatal("measure everything left measurements current", fresh, err)
	}
}
