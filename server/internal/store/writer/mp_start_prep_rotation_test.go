package writer

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// SOL-MEDIA binding ruling (MP-07): admit a saved-original replay when the
// CURRENT rotated token in the SAME family proves the turn. The immutable
// command/fence actor stays the original one so the operation/reference and
// request digest are exact; the disclosure actor is the freshly validated
// rotated credential. Rotation alone never denies or conflicts a saved original;
// a retired OLD token, a foreign family, a foreign profile lifetime or an
// altered original still do.

// mpStartPrepRotateCredential refreshes the ordinary token in the SAME family and
// returns a disclosure carrying the freshly validated current credential. The
// command payload keeps the original actor, so its request digest never changes.
func mpStartPrepRotateCredential(t *testing.T, f mpStartPrepFixture, current *mpStartPrepDisclosure, generation int64, tokenHash string) *mpStartPrepDisclosure {
	t.Helper()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := f.e.db.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatal(q, err)
		}
	}
	exec(`INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES(?,?,?,?)`,
		tokenHash, current.actor.family, generation, current.scope.expires)
	exec(`UPDATE authorization_family_tokens SET retired=1 WHERE family_id=? AND generation<?`, current.actor.family, generation)
	exec(`UPDATE authorization_session_families SET current_generation=? WHERE id=?`, generation, current.actor.family)
	a := current.actor
	a.sessionHash, a.tokenGeneration = tokenHash, generation
	return mpStartPrepDisclosureFor(current, a, current.scope)
}

// mpStartPrepDisclosureFor builds an independent disclosure (never a copy of an
// atomic-bearing struct) for the given current actor/scope.
func mpStartPrepDisclosureFor(from *mpStartPrepDisclosure, actor mpPrepActor, scope mpPrepScope) *mpStartPrepDisclosure {
	d := &mpStartPrepDisclosure{writer: from.writer, lifetime: from.lifetime, actor: actor, library: from.library,
		scope: scope, incarnation: from.incarnation}
	d.self = d
	return d
}

func mpStartPrepRunWith(t *testing.T, f mpStartPrepFixture, cap *mpStartPrepDisclosure, kind CommandID, id CommandIdentity, payload any) (mpStartPrepResult, error) {
	t.Helper()
	command, err := newMPStartPrepCommand(kind, id, payload)
	if err != nil {
		t.Fatal(err)
	}
	return f.w.runMPStartPrep(t.Context(), cap, kind, command.reference, payload)
}

func TestMPStartPrepRotatedTokenReplaysOriginal(t *testing.T) {
	t.Run("replay-originals-after-rotation", func(t *testing.T) {
		f := mpStartPrepFixtureFor(t)
		mpStartPrepComposeFixture(t, f)
		begin, err := f.begin(t)
		if err != nil || begin.Outcome.State != Applied {
			t.Fatal(begin, err)
		}
		// Refresh the token between birth and claim.
		current := mpStartPrepRotateCredential(t, f, f.cap, 2, strings.Repeat("8", 64))

		// The lifecycle advances under the rotated credential while every payload
		// remains the ORIGINAL one, so the saved digests are exact.
		claimIn := f.claimInput(1, "mp-execution")
		claimID := CommandIdentity{Incarnation: f.inc, Sequence: 1, OperationID: [16]byte{0x62}}
		claim, err := mpStartPrepRunWith(t, f, current, MPStartPrepClaim, claimID, claimIn)
		if err != nil || claim.Outcome.State != Applied || claim.Outcome.Replayed || claim.Value.State != "preparing" {
			t.Fatal("rotated claim", claim, err)
		}

		// The applied BIRTH replays from the retained original even though the
		// mutable lifecycle row moved to preparing/2. Zero writes.
		before := mpStartPrepReadFacts(t, f)
		replayedBegin, err := mpStartPrepRunWith(t, f, current, MPStartPrepBegin, f.beginIdentity(), f.beginInput())
		if err != nil || replayedBegin.Outcome.State != Applied || !replayedBegin.Outcome.Replayed ||
			replayedBegin.Value != begin.Value || replayedBegin.Digest != begin.Digest {
			t.Fatal("rotated begin replay", replayedBegin, begin, err)
		}
		if after := mpStartPrepReadFacts(t, f); after != before {
			t.Fatalf("begin replay wrote effects: before %+v after %+v", before, after)
		}

		// The claim's saved original replays via the rotated token before its ACK.
		before = mpStartPrepReadFacts(t, f)
		replayedClaim, err := mpStartPrepRunWith(t, f, current, MPStartPrepClaim, claimID, claimIn)
		if err != nil || replayedClaim.Outcome.State != Applied || !replayedClaim.Outcome.Replayed ||
			replayedClaim.Value != claim.Value || replayedClaim.Digest != claim.Digest {
			t.Fatal("rotated claim replay", replayedClaim, claim, err)
		}
		if after := mpStartPrepReadFacts(t, f); after != before {
			t.Fatalf("claim replay wrote effects: before %+v after %+v", before, after)
		}

		f.ack(t, 1)
		// Refresh again between claim and seal.
		current = mpStartPrepRotateCredential(t, f, current, 3, strings.Repeat("9", 64))
		sealIn := f.sealInput(2, f.sealCompletion())
		sealID := CommandIdentity{Incarnation: f.inc, Sequence: 2, OperationID: [16]byte{0x65}}
		seal, err := mpStartPrepRunWith(t, f, current, MPStartPrepSeal, sealID, sealIn)
		if err != nil || seal.Outcome.State != Applied || seal.Outcome.Replayed || seal.Value.State != "prepared" {
			t.Fatal("rotated seal", seal, err)
		}

		// The saved seal original replays identically after the lifecycle advanced.
		before = mpStartPrepReadFacts(t, f)
		replayedSeal, err := mpStartPrepRunWith(t, f, current, MPStartPrepSeal, sealID, sealIn)
		if err != nil || replayedSeal.Outcome.State != Applied || !replayedSeal.Outcome.Replayed ||
			replayedSeal.Value != seal.Value || replayedSeal.Digest != seal.Digest {
			t.Fatal("rotated seal replay", replayedSeal, seal, err)
		}
		if after := mpStartPrepReadFacts(t, f); after != before {
			t.Fatalf("seal replay wrote effects: before %+v after %+v", before, after)
		}

		// The applied birth still replays through the latest current token.
		before = mpStartPrepReadFacts(t, f)
		replayedBegin, err = mpStartPrepRunWith(t, f, current, MPStartPrepBegin, f.beginIdentity(), f.beginInput())
		if err != nil || replayedBegin.Outcome.State != Applied || !replayedBegin.Outcome.Replayed ||
			replayedBegin.Value != begin.Value || replayedBegin.Digest != begin.Digest {
			t.Fatal("rotated begin replay after seal", replayedBegin, begin, err)
		}
		if after := mpStartPrepReadFacts(t, f); after != before {
			t.Fatalf("post-seal begin replay wrote effects: before %+v after %+v", before, after)
		}

		// One preparation, one execution, one worker birth, no second birth.
		var preps, executions int64
		if e := f.e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM playback_start_preparations`).Scan(&preps); e != nil || preps != 1 {
			t.Fatal("replay staged another preparation", preps, e)
		}
		if e := f.e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM mp_start_execution`).Scan(&executions); e != nil || executions != 1 {
			t.Fatal("replay staged another execution", executions, e)
		}
		mpStartPrepAssertReturned(t, f)
	})

	t.Run("old-token-denied", func(t *testing.T) {
		f := mpStartPrepFixtureFor(t)
		mpStartPrepComposeFixture(t, f)
		if _, err := f.begin(t); err != nil {
			t.Fatal(err)
		}
		current := mpStartPrepRotateCredential(t, f, f.cap, 2, strings.Repeat("8", 64))
		claimIn := f.claimInput(1, "mp-execution")
		claimID := CommandIdentity{Incarnation: f.inc, Sequence: 1, OperationID: [16]byte{0x62}}
		if _, err := mpStartPrepRunWith(t, f, current, MPStartPrepClaim, claimID, claimIn); err != nil {
			t.Fatal(err)
		}
		// The retired pre-rotation credential cannot authorize the turn even
		// though the saved originals are unchanged.
		before := mpStartPrepReadFacts(t, f)
		if out, err := mpStartPrepRunWith(t, f, f.cap, MPStartPrepBegin, f.beginIdentity(), f.beginInput()); !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, mpStartPrepResult{}) {
			t.Fatal("old token authorized a begin replay", out, err)
		}
		if out, err := mpStartPrepRunWith(t, f, f.cap, MPStartPrepClaim, claimID, claimIn); !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, mpStartPrepResult{}) {
			t.Fatal("old token authorized a claim replay", out, err)
		}
		if after := mpStartPrepReadFacts(t, f); after != before {
			t.Fatalf("denied old-token replay wrote effects: before %+v after %+v", before, after)
		}
		mpStartPrepAssertReturned(t, f)
	})

	t.Run("foreign-family-denied", func(t *testing.T) {
		f := mpStartPrepFixtureFor(t)
		mpStartPrepComposeFixture(t, f)
		if _, err := f.begin(t); err != nil {
			t.Fatal(err)
		}
		current := mpStartPrepRotateCredential(t, f, f.cap, 2, strings.Repeat("8", 64))
		claimIn := f.claimInput(1, "mp-execution")
		claimID := CommandIdentity{Incarnation: f.inc, Sequence: 1, OperationID: [16]byte{0x62}}
		if _, err := mpStartPrepRunWith(t, f, current, MPStartPrepClaim, claimID, claimIn); err != nil {
			t.Fatal(err)
		}
		// A current token from another family never authorizes this family's
		// applied original, even with the same account/profile/device.
		foreign := current.actor
		foreign.family, foreign.sessionHash = "foreign-family", strings.Repeat("7", 64)
		d := mpStartPrepDisclosureFor(current, foreign, current.scope)
		before := mpStartPrepReadFacts(t, f)
		if out, err := mpStartPrepRunWith(t, f, d, MPStartPrepClaim, claimID, claimIn); !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, mpStartPrepResult{}) {
			t.Fatal("foreign family authorized a replay", out, err)
		}
		if out, err := mpStartPrepRunWith(t, f, d, MPStartPrepBegin, f.beginIdentity(), f.beginInput()); !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, mpStartPrepResult{}) {
			t.Fatal("foreign family authorized a birth replay", out, err)
		}
		if after := mpStartPrepReadFacts(t, f); after != before {
			t.Fatalf("foreign-family replay wrote effects: before %+v after %+v", before, after)
		}
		mpStartPrepAssertReturned(t, f)
	})

	t.Run("foreign-profile-lifetime-denied", func(t *testing.T) {
		f := mpStartPrepFixtureFor(t)
		mpStartPrepComposeFixture(t, f)
		if _, err := f.begin(t); err != nil {
			t.Fatal(err)
		}
		current := mpStartPrepRotateCredential(t, f, f.cap, 2, strings.Repeat("8", 64))
		claimIn := f.claimInput(1, "mp-execution")
		claimID := CommandIdentity{Incarnation: f.inc, Sequence: 1, OperationID: [16]byte{0x62}}
		if _, err := mpStartPrepRunWith(t, f, current, MPStartPrepClaim, claimID, claimIn); err != nil {
			t.Fatal(err)
		}
		// The profile is recreated with a new lifetime in the same family. The
		// current scope proof passes, so only the unchanged durable owner lifetime
		// can deny the replay.
		lifetime := strings.Repeat("d", 32)
		if _, err := f.e.db.ExecContext(t.Context(), `UPDATE direct_profiles SET lifetime=? WHERE id=?`, lifetime, current.actor.profile); err != nil {
			t.Fatal(err)
		}
		if _, err := f.e.db.ExecContext(t.Context(), `UPDATE authorization_session_families SET command_profile_lifetime=? WHERE id=?`, lifetime, current.actor.family); err != nil {
			t.Fatal(err)
		}
		scope := current.scope
		scope.profileBirth = lifetime
		d := mpStartPrepDisclosureFor(current, current.actor, scope)
		before := mpStartPrepReadFacts(t, f)
		if out, err := mpStartPrepRunWith(t, f, d, MPStartPrepClaim, claimID, claimIn); !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, mpStartPrepResult{}) {
			t.Fatal("foreign profile lifetime authorized a replay", out, err)
		}
		if out, err := mpStartPrepRunWith(t, f, d, MPStartPrepBegin, f.beginIdentity(), f.beginInput()); !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, mpStartPrepResult{}) {
			t.Fatal("foreign profile lifetime authorized a birth replay", out, err)
		}
		if after := mpStartPrepReadFacts(t, f); after != before {
			t.Fatalf("foreign-lifetime replay wrote effects: before %+v after %+v", before, after)
		}
		mpStartPrepAssertReturned(t, f)
	})

	t.Run("altered-original-conflicts", func(t *testing.T) {
		f := mpStartPrepFixtureFor(t)
		mpStartPrepComposeFixture(t, f)
		if _, err := f.begin(t); err != nil {
			t.Fatal(err)
		}
		current := mpStartPrepRotateCredential(t, f, f.cap, 2, strings.Repeat("8", 64))
		claimIn := f.claimInput(1, "mp-execution")
		claimID := CommandIdentity{Incarnation: f.inc, Sequence: 1, OperationID: [16]byte{0x62}}
		if _, err := mpStartPrepRunWith(t, f, current, MPStartPrepClaim, claimID, claimIn); err != nil {
			t.Fatal(err)
		}
		before := mpStartPrepReadFacts(t, f)
		// A different claim body under the same identity is a different request
		// digest, never the saved original.
		altered := f.claimInput(1, "mp-other")
		if out, err := mpStartPrepRunWith(t, f, current, MPStartPrepClaim, claimID, altered); !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, mpStartPrepResult{}) {
			t.Fatal("altered claim original was served", out, err)
		}
		// A different start body under the same device/start key is a changed
		// duplicate, never the applied birth.
		q := f.beginInput()
		q.bodyDigest = strings.Repeat("0", 64)
		if out, err := mpStartPrepRunWith(t, f, current, MPStartPrepBegin, f.beginIdentity(), q); !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, mpStartPrepResult{}) {
			t.Fatal("altered birth original was served", out, err)
		}
		if after := mpStartPrepReadFacts(t, f); after != before {
			t.Fatalf("altered-original conflict wrote effects: before %+v after %+v", before, after)
		}
		mpStartPrepAssertReturned(t, f)
	})
}
