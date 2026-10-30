package metadataprovider

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestFingerprintCompatibilityRejectsPrivateAndMalformedEvidence(t *testing.T) {
	if !ValidFingerprint(acousticFingerprint) {
		t.Fatal("valid bounded Chromaprint fixture rejected")
	}
	if got := FingerprintCompatibility("spectral-sign-16x2-11025-2048-v1", acousticFingerprint); got != "unsupported_algorithm" {
		t.Fatal(got)
	}
	for _, v := range []string{strings.Repeat("a", 64), base64.RawURLEncoding.EncodeToString([]byte{1, 255, 255, 255, 0}), base64.RawURLEncoding.EncodeToString([]byte{5, 0, 0, 1, 0}), `{"signatures":[1,2,3]}`, "AQAAEAAAAAAAAAA!"} {
		if ValidFingerprint(v) {
			t.Fatal("non-Chromaprint evidence accepted")
		}
	}
	if FingerprintCompatibility("chromaprint", acousticFingerprint) != "ready" {
		t.Fatal("supported optional path lost")
	}
}
