package operations

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/segmentmarkers"
	"testing"
)

func TestSegmentConfidenceSavedPolicyAndOlderDocumentPreservation(t *testing.T) {
	s, owner, auth := consoleFixture(t)
	ctx := context.Background()
	doc, err := s.Settings(ctx, auth)
	if err != nil {
		t.Fatal(err)
	}
	values := doc.Requested
	values.SegmentViewerConfidencePercent = 70
	values.SegmentAutomaticConfidencePercent = 95
	saved, err := s.ApplySettings(ctx, owner, auth, SettingsChange{doc.Revision, "segment-confidence", values})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := segmentmarkers.ReadConfidencePolicy(ctx, snapshot.Tx())
	snapshot.Rollback()
	if err != nil || policy.ViewerPercent != 70 || policy.AutomaticPercent != 95 || policy.AutomaticSafe(false, .9, "season_audio_match:intro:v1") {
		t.Fatal(policy, err)
	}
	// A client from before these optional fields existed cannot reset their values.
	values = saved.Requested
	values.SegmentViewerConfidencePercent = 0
	values.SegmentAutomaticConfidencePercent = 0
	saved, err = s.ApplySettings(ctx, owner, auth, SettingsChange{saved.Revision, "older-settings-client", values})
	if err != nil || saved.Requested.SegmentViewerConfidencePercent != 70 || saved.Requested.SegmentAutomaticConfidencePercent != 95 {
		t.Fatal(saved, err)
	}
	for _, invalid := range []int{1, 49, 71, 100} {
		values = saved.Requested
		values.SegmentViewerConfidencePercent = invalid
		if _, err = s.ApplySettings(ctx, owner, auth, SettingsChange{saved.Revision, "invalid-confidence", values}); err == nil {
			t.Fatal("invalid confidence accepted", invalid)
		}
	}
}

func TestSegmentConfidenceReceiptDistinguishesOmissionFromExplicitDefaults(t *testing.T) {
	s, owner, auth := consoleFixture(t)
	ctx := context.Background()
	doc, err := s.Settings(ctx, auth)
	if err != nil {
		t.Fatal(err)
	}
	values := doc.Requested
	values.SegmentViewerConfidencePercent, values.SegmentAutomaticConfidencePercent = 70, 95
	saved, err := s.ApplySettings(ctx, owner, auth, SettingsChange{doc.Revision, "confidence-initial", values})
	if err != nil {
		t.Fatal(err)
	}
	values = saved.Requested
	values.SegmentViewerConfidencePercent, values.SegmentAutomaticConfidencePercent = 0, 0
	omitted := SettingsChange{saved.Revision, "confidence-omitted", values}
	result, err := s.ApplySettings(ctx, owner, auth, omitted)
	if err != nil || result.Requested.SegmentViewerConfidencePercent != 70 || result.Requested.SegmentAutomaticConfidencePercent != 95 {
		t.Fatal(result, err)
	}
	replay, err := s.ApplySettings(ctx, owner, auth, omitted)
	if err != nil || replay.Revision != result.Revision {
		t.Fatal(replay, err)
	}
	explicit := omitted
	explicit.Values.SegmentViewerConfidencePercent, explicit.Values.SegmentAutomaticConfidencePercent = 50, 85
	if _, err = s.ApplySettings(ctx, owner, auth, explicit); err == nil {
		t.Fatal("explicit defaults reused omission receipt")
	}
}

func TestChapterSettingDefaultsFalseOmissionAndReceipt(t *testing.T) {
	s, owner, auth := consoleFixture(t)
	ctx := context.Background()
	doc, err := s.Settings(ctx, auth)
	if err != nil || doc.Requested.SegmentUseChapterNames == nil || !*doc.Requested.SegmentUseChapterNames {
		t.Fatal(doc, err)
	}
	disabled := false
	values := doc.Requested
	values.SegmentUseChapterNames = &disabled
	saved, err := s.ApplySettings(ctx, owner, auth, SettingsChange{doc.Revision, "chapters-disable", values})
	if err != nil || saved.Requested.SegmentUseChapterNames == nil || *saved.Requested.SegmentUseChapterNames {
		t.Fatal(saved, err)
	}
	snapshot, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := segmentmarkers.ReadConfidencePolicy(ctx, snapshot.Tx())
	snapshot.Rollback()
	if err != nil || policy.UseChapterNames {
		t.Fatal(policy, err)
	}
	values = saved.Requested
	values.SegmentUseChapterNames = nil
	change := SettingsChange{saved.Revision, "chapters-omitted", values}
	preserved, err := s.ApplySettings(ctx, owner, auth, change)
	if err != nil || preserved.Requested.SegmentUseChapterNames == nil || *preserved.Requested.SegmentUseChapterNames {
		t.Fatal(preserved, err)
	}
	replay, err := s.ApplySettings(ctx, owner, auth, change)
	if err != nil || replay.Revision != preserved.Revision {
		t.Fatal(replay, err)
	}
	enabled := true
	change.Values.SegmentUseChapterNames = &enabled
	if _, err = s.ApplySettings(ctx, owner, auth, change); err == nil {
		t.Fatal("explicit true reused omitted receipt")
	}
	binding := ConfigurationBinding{Field: "segments.useChapterNames", Scale: 1}
	if err = assignConfiguration(&values, binding, false); err != nil || values.SegmentUseChapterNames == nil || *values.SegmentUseChapterNames || configurationValue(binding, values.SegmentUseChapterNames) != false {
		t.Fatal(values, err)
	}
}
