package writer

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// P1-1: the frozen owner tuple names a sparse namespace, never authority. A
// session for profile A must not read or write profile B's preference, reminder
// or reminder delete, and must not take B's receipt under A's root.
func TestMGPersonalOwnerBoundToSession(t *testing.T) {
	t.Run("channel-preference", func(t *testing.T) {
		w, _, v := mgPersonalFixture(t)
		fake := mgPersonalFake(t, v)
		other := mgPersonalOwner{authority: "local", account: "other-account", profile: "other-profile"}
		request := mgPersonalPreferenceRequest{owner: other, target: v.live, favorite: true}
		reference := mgPersonalPreferenceReference(t, mgPersonalIdentity(1, 1), request)

		turn := mgPersonalTurn(t, w, MGSetChannelPreference)
		got, err := acceptMGPersonalPreference(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
		if !errors.Is(err, ErrDisclosureDenied) || got != (mgPersonalAcceptance{}) {
			t.Fatal("foreign owner wrote a preference", got, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		var preferences, receipts int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM live_channel_preferences),(SELECT count(*) FROM user_mutation_command_receipts)`).Scan(&preferences, &receipts); err != nil {
			t.Fatal(err)
		}
		if preferences != 0 || receipts != 0 {
			t.Fatal(preferences, receipts)
		}
	})

	t.Run("set-reminder", func(t *testing.T) {
		w, _, v := mgPersonalFixture(t)
		fake := mgPersonalFake(t, v)
		other := mgPersonalOwner{authority: "local", account: "other-account", profile: "other-profile"}
		request := v.reminder(false)
		request.owner = other
		reference := mgPersonalReminderReference(t, mgPersonalIdentity(1, 1), request)

		turn := mgPersonalTurn(t, w, MGSetReminder)
		got, err := acceptMGPersonalReminder(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
		if !errors.Is(err, ErrDisclosureDenied) || got != (mgPersonalAcceptance{}) {
			t.Fatal("foreign owner wrote a reminder", got, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		var reminders, receipts int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM live_reminders),(SELECT count(*) FROM user_mutation_command_receipts)`).Scan(&reminders, &receipts); err != nil {
			t.Fatal(err)
		}
		if reminders != 0 || receipts != 0 {
			t.Fatal(reminders, receipts)
		}
	})

	t.Run("delete-reminder", func(t *testing.T) {
		w, _, v := mgPersonalFixture(t)
		fake := mgPersonalFake(t, v)
		other := mgPersonalOwner{authority: "local", account: "other-account", profile: "other-profile"}
		seed := v.reminder(false)
		seed.owner = other
		if _, err := w.db.ExecContext(t.Context(), `INSERT INTO live_reminders(id,owner_key,kind,source_id,channel_id,generation_id,programme_id,revision,start_utc) VALUES(?,?,?,?,?,?,?,?,?)`,
			seed.id(), seed.owner.key(), string(seed.target.kind), seed.target.source, seed.target.channel, seed.target.generation, seed.programme, int64(1), v.start.Format(time.RFC3339)); err != nil {
			t.Fatal(err)
		}
		request := mgPersonalDeleteRequest{owner: other, id: seed.id(), expected: 1}
		reference := mgPersonalDeleteReference(t, mgPersonalIdentity(1, 1), request)

		turn := mgPersonalTurn(t, w, MGDeleteReminder)
		got, err := acceptMGPersonalReminderDelete(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
		if !errors.Is(err, ErrDisclosureDenied) || got != (mgPersonalAcceptance{}) {
			t.Fatal("foreign owner deleted a reminder", got, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		var reminders, receipts int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM live_reminders WHERE owner_key=?),(SELECT count(*) FROM user_mutation_command_receipts)`, other.key()).Scan(&reminders, &receipts); err != nil {
			t.Fatal(err)
		}
		if reminders != 1 || receipts != 0 {
			t.Fatal(reminders, receipts)
		}
	})
}

// P1-2: 811 and 812 admit the current native target only. A forbidden channel,
// rating or item denies before the effect and leaves no row or receipt behind.
func TestMGPersonalPreferenceTargetAdmission(t *testing.T) {
	w, _, v := mgPersonalFixture(t)
	fake := mgPersonalFake(t, v)
	fake.viewer = mgPersonalFakeViewer{channels: map[string]bool{}}
	request := mgPersonalPreferenceRequest{owner: v.owner, target: v.live, favorite: true}
	reference := mgPersonalPreferenceReference(t, mgPersonalIdentity(1, 1), request)

	turn := mgPersonalTurn(t, w, MGSetChannelPreference)
	got, err := acceptMGPersonalPreference(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	if !errors.Is(err, ErrDisclosureDenied) || got != (mgPersonalAcceptance{}) {
		t.Fatal("forbidden channel admitted a preference", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var preferences, receipts int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM live_channel_preferences),(SELECT count(*) FROM user_mutation_command_receipts)`).Scan(&preferences, &receipts); err != nil {
		t.Fatal(err)
	}
	if preferences != 0 || receipts != 0 {
		t.Fatal(preferences, receipts)
	}
}

func TestMGPersonalReminderTargetAdmission(t *testing.T) {
	t.Run("forbidden-rating", func(t *testing.T) {
		w, _, v := mgPersonalFixture(t)
		if _, err := w.db.ExecContext(t.Context(), `UPDATE live_programmes SET has_live_programme_metadata=1,metadata_series_id='',metadata_episode_id='',metadata_new_evidence='unknown',metadata_description='',metadata_facts='{"rating":{"system":"VCHIP","value":"TV-MA"}}' WHERE generation_id='live-generation' AND id='live-programme'`); err != nil {
			t.Fatal(err)
		}
		fake := mgPersonalFake(t, v)
		fake.viewer = mgPersonalFakeViewer{ratings: []string{"TV-PG"}}
		request := v.reminder(false)
		reference := mgPersonalReminderReference(t, mgPersonalIdentity(1, 1), request)

		turn := mgPersonalTurn(t, w, MGSetReminder)
		got, err := acceptMGPersonalReminder(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
		if !errors.Is(err, ErrDisclosureDenied) || got != (mgPersonalAcceptance{}) {
			t.Fatal("forbidden rating admitted a reminder", got, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		var reminders, receipts int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM live_reminders),(SELECT count(*) FROM user_mutation_command_receipts)`).Scan(&reminders, &receipts); err != nil {
			t.Fatal(err)
		}
		if reminders != 0 || receipts != 0 {
			t.Fatal(reminders, receipts)
		}
	})

	t.Run("allowed-rating", func(t *testing.T) {
		w, _, v := mgPersonalFixture(t)
		if _, err := w.db.ExecContext(t.Context(), `UPDATE live_programmes SET has_live_programme_metadata=1,metadata_series_id='',metadata_episode_id='',metadata_new_evidence='unknown',metadata_description='',metadata_facts='{"rating":{"system":"VCHIP","value":"TV-MA"}}' WHERE generation_id='live-generation' AND id='live-programme'`); err != nil {
			t.Fatal(err)
		}
		fake := mgPersonalFake(t, v)
		fake.viewer = mgPersonalFakeViewer{ratings: []string{"TV-MA"}}
		request := v.reminder(false)
		reference := mgPersonalReminderReference(t, mgPersonalIdentity(1, 1), request)

		turn := mgPersonalTurn(t, w, MGSetReminder)
		got, err := acceptMGPersonalReminder(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
		if err != nil || got.decision != receiptFresh {
			t.Fatal("allowed rating was not admitted", got, err)
		}
	})

	t.Run("forbidden-item", func(t *testing.T) {
		w, _, v := mgPersonalFixture(t)
		fake := mgPersonalFake(t, v)
		fake.viewer = mgPersonalFakeViewer{items: map[int64]bool{}}
		request := v.reminder(true)
		reference := mgPersonalReminderReference(t, mgPersonalIdentity(1, 1), request)

		turn := mgPersonalTurn(t, w, MGSetReminder)
		got, err := acceptMGPersonalReminder(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
		if !errors.Is(err, ErrDisclosureDenied) || got != (mgPersonalAcceptance{}) {
			t.Fatal("forbidden item admitted a reminder", got, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		var reminders, receipts int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM live_reminders),(SELECT count(*) FROM user_mutation_command_receipts)`).Scan(&reminders, &receipts); err != nil {
			t.Fatal(err)
		}
		if reminders != 0 || receipts != 0 {
			t.Fatal(reminders, receipts)
		}
	})

	t.Run("allowed-item", func(t *testing.T) {
		w, _, v := mgPersonalFixture(t)
		fake := mgPersonalFake(t, v)
		fake.viewer = mgPersonalFakeViewer{items: map[int64]bool{v.item.item: true}}
		request := v.reminder(true)
		reference := mgPersonalReminderReference(t, mgPersonalIdentity(1, 1), request)

		turn := mgPersonalTurn(t, w, MGSetReminder)
		got, err := acceptMGPersonalReminder(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
		if err != nil || got.decision != receiptFresh {
			t.Fatal("allowed item was not admitted", got, err)
		}
	})
}

// The real caller lifetime guard (mg_personal_commands.go) is distinct from the
// turn ctx. A cancelled LIFETIME must refuse with context.Canceled before any
// custody or effect even when the turn ctx is alive.
func TestMGPersonalAuthorizeLifetimeGuard(t *testing.T) {
	w, _, v := mgPersonalFixture(t)
	fake := mgPersonalFake(t, v)
	lifetime, cancel := context.WithCancel(t.Context())
	cancel()
	turn := mgPersonalTurn(t, w, MGSetChannelPreference)
	got, err := mgPersonalAuthorize(t.Context(), turn, MGSetChannelPreference, fake, v.proof, lifetime)
	// The guard must refuse BEFORE the seam: a later finishCommand join would
	// also report context.Canceled, so the seam's own denial must be absent.
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrDisclosureDenied) || got != nil {
		t.Fatal("cancelled caller lifetime reached the root seam", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

type mgPersonalStubRoots struct {
	session mgPersonalRootAuthorization
}

func (s mgPersonalStubRoots) authorizeUSCurrentSession(context.Context, *commandTurn, *usOwnerAuthorityProof, context.Context) (mgPersonalRootAuthorization, error) {
	return s.session, nil
}

type mgPersonalStubSession struct {
	root     string
	identity uoRootIdentity
}

func (s mgPersonalStubSession) mgPersonalRootID() string { return s.root }
func (s mgPersonalStubSession) mgPersonalRootIdentity() uoRootIdentity {
	return s.identity
}
func (s mgPersonalStubSession) state(context.Context, *commandTurn) (string, error) {
	return "active", nil
}
func (s mgPersonalStubSession) recheck(context.Context, *commandTurn) error { return nil }
func (s mgPersonalStubSession) admitTarget(context.Context, *commandTurn, mgPersonalAdmissionTarget) error {
	return nil
}

// The session shape check (mg_personal_commands.go) is what refuses a root ID
// that is not the exact 22-byte opaque root or an incomplete frozen identity.
func TestMGPersonalAuthorizeRejectsMalformedSession(t *testing.T) {
	w, _, v := mgPersonalFixture(t)
	for name, session := range map[string]mgPersonalRootAuthorization{
		"short-root":          mgPersonalStubSession{root: "short", identity: mgPersonalFakeIdentity(v.proof)},
		"incomplete-identity": mgPersonalStubSession{root: v.root},
	} {
		t.Run(name, func(t *testing.T) {
			turn := mgPersonalTurn(t, w, MGSetChannelPreference)
			got, err := mgPersonalAuthorize(t.Context(), turn, MGSetChannelPreference, mgPersonalStubRoots{session: session}, v.proof, t.Context())
			if !errors.Is(err, ErrDisclosureDenied) || got != nil {
				t.Fatal("malformed session was admitted", got, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// The saved result type is bound to the command kind, so one receipt row can
// never carry another command family's result shape.
func TestMGPersonalResultPayloadKindBound(t *testing.T) {
	id := mgPersonalIdentity(1, 1)
	var q RequestDigest
	preference := mgPersonalPreferenceResult{source: "personal-live", channel: "live-channel", revision: 1}
	if _, _, err := mgPersonalResultPayload(id, q, MGSetChannelPreference, preference); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgPersonalResultPayload(id, q, MGSetReminder, preference); !errors.Is(err, ErrInvalid) {
		t.Fatal("preference result accepted under a reminder kind", err)
	}

	reminder := mgPersonalReminderResult{
		id: strings.Repeat("a", 64), owner: strings.Repeat("b", 64), kind: "live-source",
		source: "source", channel: "channel", generation: "generation", programme: "programme",
		start: "2026-11-10T20:00:00Z", externalGeneration: "generation", revision: 1,
	}
	for _, kind := range []CommandID{MGSetReminder, MGDeleteReminder} {
		if _, _, err := mgPersonalResultPayload(id, q, kind, reminder); err != nil {
			t.Fatal(kind, err)
		}
	}
	if _, _, err := mgPersonalResultPayload(id, q, MGSetChannelPreference, reminder); !errors.Is(err, ErrInvalid) {
		t.Fatal("reminder result accepted under a preference kind", err)
	}

	source := mgSourceCommandResult{kind: MGSetSourceEnabled, appliedMS: 1}
	if _, _, err := mgPersonalResultPayload(id, q, MGRetireSource, source); !errors.Is(err, ErrInvalid) {
		t.Fatal("source result accepted under a different source kind", err)
	}
	dvr := mgDVRPolicyResult{kind: mgDVRPolicyDefaultsKind, revision: 1}
	if _, _, err := mgPersonalResultPayload(id, q, MGSetDVRStoragePolicy, dvr); !errors.Is(err, ErrInvalid) {
		t.Fatal("defaults result accepted under the storage kind", err)
	}
}

// A legal 256-byte library channel derives a 264-byte "library:"+channel saved
// source. The result cap follows that derivation instead of rejecting an input
// the request validator already accepted.
func TestMGPersonalLibraryPreferenceSourceCap(t *testing.T) {
	id := mgPersonalIdentity(2, 3)
	var q RequestDigest
	channel := strings.Repeat("c", mgPersonalResultTextMax)
	preference := mgPersonalPreferenceResult{source: mgPersonalLibraryPrefix + channel, channel: channel, revision: 1}
	payload, digest, err := mgPersonalPreferencePayload(id, q, preference)
	if err != nil {
		t.Fatal("legal library channel rejected", err)
	}
	if got, err := decodeMGPersonalPreference(id, q, payload, digest); err != nil || got != preference {
		t.Fatal(got, err)
	}

	// The derived source must be exactly "library:"+channel, and a live source
	// keeps the common 256-byte cap.
	bad := preference
	bad.source = mgPersonalLibraryPrefix + strings.Repeat("d", len(channel))
	if err := preflightMGPersonalPreference(bad); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("foreign library derivation accepted", err)
	}
	if err := preflightMGPersonalPreference(mgPersonalPreferenceResult{source: strings.Repeat("d", mgPersonalPreferenceSourceMax), channel: channel, revision: 1}); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("over-long live source accepted", err)
	}
	long := strings.Repeat("c", mgPersonalResultTextMax+1)
	if (mgPersonalChannel{kind: mgPersonalLibrary, source: long, channel: long, generation: "generation", revision: 1}).valid() {
		t.Fatal("over-long library channel passed the request validator", long)
	}
}
