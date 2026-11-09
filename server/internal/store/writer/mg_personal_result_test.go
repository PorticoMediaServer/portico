package writer

import (
	"bytes"
	"errors"
	"testing"
)

func TestMGPersonalResultCodecRoundTripAndBounds(t *testing.T) {
	id := mgPersonalIdentity(3, 9)
	var request RequestDigest
	request[0] = 7
	preference := mgPersonalPreferenceResult{source: "library:channel", channel: "channel", revision: 4, favorite: true}
	payload, digest, err := mgPersonalPreferencePayload(id, request, preference)
	if err != nil || len(payload) == 0 {
		t.Fatal(payload, digest, err)
	}
	if got, err := decodeMGPersonalPreference(id, request, payload, digest); err != nil || got != preference {
		t.Fatal(got, err)
	}
	// A tampered payload, digest, identity or request never decodes as the
	// original result.
	bad := bytes.Clone(payload)
	bad[len(bad)-1] ^= 1
	if _, err := decodeMGPersonalPreference(id, request, bad, digest); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("tampered payload decoded", err)
	}
	wrongDigest := digest
	wrongDigest[0] ^= 1
	if _, err := decodeMGPersonalPreference(id, request, payload, wrongDigest); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("wrong digest decoded", err)
	}
	other := id
	other.Sequence++
	if _, err := decodeMGPersonalPreference(other, request, payload, digest); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("foreign identity decoded", err)
	}

	reminder := mgPersonalReminderResult{
		id: "a" + string(bytes.Repeat([]byte("b"), 63)), owner: "c" + string(bytes.Repeat([]byte("d"), 63)),
		kind: "live-source", source: "source", channel: "channel", generation: "generation", programme: "programme",
		start: "2026-11-10T20:00:00Z", externalGeneration: "generation", revision: 2,
	}
	payload, digest, err = mgPersonalReminderPayload(id, request, reminder)
	if err != nil || len(payload) == 0 {
		t.Fatal(payload, digest, err)
	}
	if got, err := decodeMGPersonalReminder(id, request, payload, digest); err != nil || got != reminder {
		t.Fatal(got, err)
	}
	// A reminder whose generated kind/reference pair is inconsistent is refused
	// before any payload or receipt is written.
	broken := reminder
	broken.libraryGeneration = broken.generation
	if err := preflightMGPersonalReminder(broken); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("inconsistent generated ref accepted", err)
	}
	broken = reminder
	broken.revision = 0
	if err := preflightMGPersonalReminder(broken); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("zero revision accepted", err)
	}
	if err := preflightMGPersonalPreference(mgPersonalPreferenceResult{source: "", channel: "channel", revision: 1}); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("empty preference source accepted", err)
	}
	if err := preflightMGPersonalPreference(mgPersonalPreferenceResult{source: "source", channel: "channel", revision: 0}); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("zero preference revision accepted", err)
	}
}
