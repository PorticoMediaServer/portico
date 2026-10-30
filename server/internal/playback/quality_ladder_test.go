package playback

import (
	"strings"
	"testing"
)

func rungIDs(rungs []QualityRung) []string {
	out := make([]string, 0, len(rungs))
	for _, r := range rungs {
		out = append(out, r.ID)
	}
	return out
}

func TestQualityLadderKeepsEverySourceStep(t *testing.T) {
	want := "original automatic 2160p 1080p-high 1080p-low 720p-high 720p-low 480p 240p"
	for _, height := range []int{0, 144, 240, 480, 720, 1080, 2160} {
		rungs := QualityOffers(height, true, true, openPolicy(), 6_000_000)
		if got := strings.Join(rungIDs(rungs), " "); got != want {
			t.Fatal(got)
		}
		if rungs[2].SameAsOriginal != (height > 0) {
			t.Fatal("source facts", height, rungs[2])
		}
	}
	unknown := QualityOffers(1080, true, true, openPolicy())
	if unknown[2].SameAsOriginal {
		t.Fatal("guessed unknown bitrate")
	}
}

func TestQualityLadderRespectsOwnerAndPolicy(t *testing.T) {
	policy := openPolicy()
	// Owner-disabled transcoding leaves only what the source satisfies directly.
	off := QualityOffers(1080, true, false, policy)
	for _, r := range off {
		switch r.Kind {
		case QualityAutomatic, QualityOriginal:
			if !r.Enabled {
				t.Fatal("direct rung disabled with transcoding off", r)
			}
		default:
			if r.Enabled || r.Reason != RungReasonTranscodeOff {
				t.Fatal("fixed rung selectable with transcoding off", r)
			}
		}
	}

	// A lane ceiling disables the rungs above it but still publishes them, so a
	// client can say why the menu is short instead of inventing an explanation.
	limited := DefaultDeliveryConfiguration()
	limited.MaxVideoHeight, limited.MaxVideoBitrateBPS = 720, 8_000_000
	constrained := ResolveDeliveryPolicy(nil, NetworkRemote, LocalityRemote, "cellular", limited)
	rungs := QualityOffers(2160, true, true, constrained)
	for _, r := range rungs {
		if r.Kind != QualityFixed {
			continue
		}
		wantEnabled := r.TargetDisplayHeight <= 720 && r.MaxVideoBitrateBPS <= 8_000_000
		if r.Enabled != wantEnabled {
			t.Fatal("rung enablement disagrees with the lane ceiling", r)
		}
		if !r.Enabled && r.Reason != RungReasonExceedsPolicy {
			t.Fatal("disabled rung published no reason", r)
		}
	}

	// An unavailable source offers nothing selectable at all.
	for _, r := range QualityOffers(1080, false, true, policy) {
		if r.Enabled || r.Reason != RungReasonSourceUnusable {
			t.Fatal("unavailable source published a selectable rung", r)
		}
	}
}

func TestQualityOffersRevisionFencesSelection(t *testing.T) {
	policy := openPolicy()
	rungs := QualityOffers(1080, true, true, policy)
	revision := OffersRevision("asset", rungs, policy)
	if len(revision) != 64 {
		t.Fatal("revision is not a full digest", revision)
	}
	if OffersRevision("asset", rungs, policy) != revision {
		t.Fatal("revision is not stable for an unchanged ladder")
	}
	// The same ladder under a different lane is a different ladder.
	capped := DefaultDeliveryConfiguration()
	capped.MaxVideoHeight = 720
	other := ResolveDeliveryPolicy(nil, NetworkRemote, LocalityRemote, "cellular", capped)
	if OffersRevision("asset", QualityOffers(1080, true, true, other), other) == revision {
		t.Fatal("revision ignored the resolved policy")
	}
	if OffersRevision("other-asset", rungs, policy) == revision {
		t.Fatal("revision ignored the source")
	}

	if _, ok := SelectedRung(rungs, "nonsense"); ok {
		t.Fatal("unknown rung accepted")
	}
	if _, ok := SelectedRung(QualityOffers(1080, true, false, policy), "720p-high"); ok {
		t.Fatal("disabled rung accepted")
	}
	rung, ok := SelectedRung(rungs, "")
	if !ok || rung.ID != "original" {
		t.Fatal("empty selection did not default to Original", rung)
	}
}

func TestQualityTargetNarrowsToTheStricterCeiling(t *testing.T) {
	owner := DefaultDeliveryConfiguration()
	owner.MaxVideoHeight, owner.MaxVideoBitrateBPS, owner.MaxAudioBitrateBPS = 720, 3_000_000, 128_000
	lane := ResolveDeliveryPolicy(nil, NetworkRemote, LocalityRemote, "cellular", owner)
	// A rung asking for more than the lane allows is narrowed to the lane.
	wide := QualityRung{ID: "1080p-low", Kind: QualityFixed, TargetDisplayHeight: 1080, MaxVideoBitrateBPS: 8_000_000, MaxAudioBitrateBPS: 192_000}.Target(lane, 2160)
	if wide.MaxVideoHeight != 720 || wide.MaxVideoBitrateBPS != 3_000_000 || wide.MaxAudioBitrateBPS != 128_000 {
		t.Fatal("rung was not narrowed by the lane", wide)
	}
	// A rung asking for less than the lane allows keeps its own smaller ceiling.
	narrow := QualityRung{ID: "480p", Kind: QualityFixed, TargetDisplayHeight: 480, MaxVideoBitrateBPS: 2_000_000, MaxAudioBitrateBPS: 96_000}.Target(lane, 2160)
	if narrow.MaxVideoHeight != 480 || narrow.MaxVideoBitrateBPS != 2_000_000 || narrow.MaxAudioBitrateBPS != 96_000 {
		t.Fatal("lane widened a narrower rung", narrow)
	}
	// Original carries no ceilings, and automatic carries the lane's.
	if original := (QualityRung{ID: "original", Kind: QualityOriginal}).Target(lane, 2160); original.MaxVideoBitrateBPS != 3_000_000 || original.MaxVideoHeight != 720 {
		t.Fatal("original ignored the mandatory lane ceiling", original)
	}
	if auto := autoTarget(lane, 2160); auto.MaxVideoHeight != 720 || auto.MaxVideoBitrateBPS != 3_000_000 {
		t.Fatal("automatic rung did not take the lane ceiling", auto)
	}
	// No target ever exceeds the source it is cut from.
	if capped := autoTarget(openPolicy(), 480); capped.MaxVideoHeight != 0 {
		t.Fatal("unclamped lane invented a height", capped)
	}
	if capped := (QualityRung{ID: "1080p-low", Kind: QualityFixed, TargetDisplayHeight: 1080}).Target(openPolicy(), 480); capped.MaxVideoHeight != 480 {
		t.Fatal("target exceeded the source height", capped)
	}
}

func TestQualitySelectionDomain(t *testing.T) {
	if !validQualitySelection("automatic") || !validQualitySelection("original") || !validQualitySelection("1080p-low") {
		t.Fatal("published rung rejected")
	}
	if validQualitySelection("") || validQualitySelection("4320p") || validQualitySelection("../auto") {
		t.Fatal("unpublished rung accepted")
	}
}
