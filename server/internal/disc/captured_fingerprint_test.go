package disc

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
)

func capturedFixtureVersion(t *testing.T, scope string, data string) mediasource.Version {
	t.Helper()
	digest := sha256.Sum256([]byte(data))
	version, err := mediasource.NewVersion(mediasource.Evidence{Kind: mediasource.ContentSHA256,
		Scope: scope, Object: "original-owned-acquisition", Revision: hex.EncodeToString(digest[:]), Size: int64(len(data))})
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func capturedProbeFixture(t *testing.T) Probe {
	t.Helper()
	_, fixtures := discFixtures(t)
	for _, fixture := range fixtures {
		probe, err := DecodeProbe(fixture.Expected)
		if err != nil {
			t.Fatal(err)
		}
		if probe.Packaging == "iso" {
			return probe
		}
	}
	t.Fatal("missing authored ISO fixture")
	return Probe{}
}

func TestCapturedStructureDoesNotCarryAcrossCaptureSourceOrBytes(t *testing.T) {
	probe := capturedProbeFixture(t)
	version := capturedFixtureVersion(t, "original-source", "owned bytes")
	original, err := CapturedStructureFingerprint("capture-one", version, probe)
	if err != nil {
		t.Fatal(err)
	}
	for _, changed := range []struct {
		capture string
		version mediasource.Version
	}{
		{"capture-two", version},
		{"capture-one", capturedFixtureVersion(t, "another-source", "owned bytes")},
		{"capture-one", capturedFixtureVersion(t, "original-source", "other bytes")},
	} {
		got, err := CapturedStructureFingerprint(changed.capture, changed.version, probe)
		if err != nil || got == original {
			t.Fatal("identical-looking structure reused another capture's facts")
		}
	}
	again, err := CapturedStructureFingerprint("capture-one", version, probe)
	if err != nil || again != original {
		t.Fatal("the exact original capture was not stable")
	}
}

func TestCapturedStructureRejectsNonCaptureOrMalformedEvidence(t *testing.T) {
	probe := capturedProbeFixture(t)
	provider, err := mediasource.NewVersion(mediasource.Evidence{Kind: mediasource.StrongETag,
		Scope: "source", Object: "provider-object", Revision: `"provider-version"`, Size: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = CapturedStructureFingerprint("capture", provider, probe); err == nil {
		t.Fatal("provider evidence was relabeled as an owned capture")
	}
	version := capturedFixtureVersion(t, "source", "owned bytes")
	for _, capture := range []string{"", "capture\x00other"} {
		if _, err = CapturedStructureFingerprint(capture, version, probe); err == nil {
			t.Fatal("missing or ambiguous capture identity was accepted")
		}
	}
	probe.Titles = nil
	if _, err = CapturedStructureFingerprint("capture", version, probe); err == nil {
		t.Fatal("malformed disc structure was accepted")
	}
}

func TestOneCaptureCannotVersionADiscFolder(t *testing.T) {
	probe := capturedProbeFixture(t)
	probe.Packaging = "folder"
	if _, err := CapturedStructureFingerprint("capture", capturedFixtureVersion(t, "source", "one member"), probe); err == nil {
		t.Fatal("one member capture was promoted into a directory version")
	}
}
