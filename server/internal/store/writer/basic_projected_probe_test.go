package writer

import (
	"errors"
	"strings"
	"testing"
)

func TestBasicProjectedProbeKeepsNativeCounterMeanings(t *testing.T) {
	s := basicStreamFactStage{changeID: 51, inputRevision: 7, libraryID: 7, assetID: 41, expectedEpoch: 17, assetToken: "asset", runID: "run", sourceID: "source", incarnation: "inc", objectID: "object", objectRevision: "rev", sourceGeneration: 23, policyRevision: 29, witnessFingerprint: [32]byte{1}, factRevision: 31, size: 42, modifiedNS: -11, detailVersion: 1, fingerprint: "probe"}
	c := basicAssetProjection{observation: basicAssetObservation{41, 7, 42, -11, "/media/movie.mkv", "object", "source", "inc", "rev", "run"}, assetToken: "asset", before: basicProbeScalars{"old", "vp9", "aac", 1920, 1080, 90}, playbackIncarnation: [16]byte{1}, playbackRevision: 101, preparedRevision: 103, fileSizeRevision: 113, invalidationRevision: 109, expectedEpoch: 17}
	after := basicProbeScalars{"mkv", "h264", "", 0, 0, 100}
	got, err := prepareBasicProjectedProbe(s, 13, c, after)
	if err != nil || got.source != s || got.changeRevision != 13 || got.before != c.before || got.after != after || got.playbackIncarnation != c.playbackIncarnation || got.playbackRevision != 101 || got.preparedRevision != 103 || got.fileSizeRevision != 113 || got.invalidationRevision != 109 || got.assetPath != "/media/movie.mkv" {
		t.Fatal(got, err)
	}
	for _, edit := range []func(*basicStreamFactStage){
		func(s *basicStreamFactStage) { s.expectedEpoch++ }, func(s *basicStreamFactStage) { s.assetID++ }, func(s *basicStreamFactStage) { s.objectRevision = "new" }, func(s *basicStreamFactStage) { s.libraryID++ },
	} {
		bad := s
		edit(&bad)
		if got, err := prepareBasicProjectedProbe(bad, 13, c, after); got != (basicProbeEffects{}) || !errors.Is(err, ErrConflict) {
			t.Fatal(got, err)
		}
	}
	s.fingerprint = strings.Repeat("x", maxCanonicalRequestBytes+1)
	if got, err := prepareBasicProjectedProbe(s, 13, c, after); got != (basicProbeEffects{}) || !errors.Is(err, ErrCap) {
		t.Fatal(got, err)
	}
}
