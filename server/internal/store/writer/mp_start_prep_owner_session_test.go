package writer

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// This is a fully valid native family/token, not just a forged disclosure whose
// actor disagrees with the request. Both actor/request name the foreign family;
// only binding the stored preparation owner can reject it.
func mpStartPrepOtherSession(t *testing.T, f mpStartPrepFixture, otherAccount bool) *mpStartPrepDisclosure {
	t.Helper()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := f.e.db.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatal(err)
		}
	}
	a, s := f.actor, f.cap.scope
	a.family = "mp-other-family"
	a.sessionHash = strings.Repeat("7", 64)
	if otherAccount {
		a.account, a.profile, a.family = "mp-other-account", "mp-other-profile", "mp-other-account-family"
		a.sessionHash = strings.Repeat("6", 64)
		exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) SELECT ?,?,password_hash,?,epoch FROM accounts WHERE id=?`, a.account, a.account, a.profile, f.actor.account)
		exec(`INSERT INTO direct_memberships(account_id,incarnation,role,allowed_libraries,revision) SELECT ?,incarnation,role,allowed_libraries,revision FROM direct_memberships WHERE account_id=?`, a.account, f.actor.account)
		exec(`INSERT INTO direct_profiles(id,account_id,lifetime,name,is_primary,revision,allowed_libraries) SELECT ?,?,lifetime,'Other',is_primary,revision,allowed_libraries FROM direct_profiles WHERE id=?`, a.profile, a.account, f.actor.profile)
		s.primaryProfile = a.profile
	}
	exec(`INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,device_id,command_member_incarnation,command_profile_lifetime,command_incarnation,security_kind) SELECT ?,server_id,?,?,authority,role,epoch,authorization_horizon,1,device_id,command_member_incarnation,command_profile_lifetime,command_incarnation,security_kind FROM authorization_session_families WHERE id=?`, a.family, a.account, a.profile, f.actor.family)
	exec(`INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES(?,?,1,?)`, a.sessionHash, a.family, s.expires)
	return mpStartPrepDisclosureFor(f.cap, a, s)
}

func TestMPStartPrepOwnerSessionBinding(t *testing.T) {
	f := mpStartPrepFixtureFor(t)
	mpStartPrepComposeFixture(t, f)
	birth, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	current := mpStartPrepRotateCredential(t, f, f.cap, 2, strings.Repeat("8", 64))
	foreign := []*mpStartPrepDisclosure{mpStartPrepOtherSession(t, f, false), mpStartPrepOtherSession(t, f, true)}
	// Positive controls prove each foreign family/token really passes native
	// current scope/actor/source validation for its own independent birth.
	for i, d := range foreign {
		q := f.beginInput()
		q.actor = d.actor
		q.queue = mpPrepQueue{}
		q.id = d.actor.family
		q.keyDigest = strings.Repeat(string(rune('4'+i)), 64)
		q.results = mpStartPrepStableIDs{session: q.id + "-session", legacySession: q.id + "-legacy", snapshot: q.id + "-snapshot", legacyGeneration: 1}
		if out, e := mpStartPrepRunWith(t, f, d, MPStartPrepBegin, f.beginIdentity(), q); e != nil || out.Outcome.State != Applied || out.Outcome.Replayed {
			t.Fatal("invalid foreign-session fixture", out, e)
		}
	}
	refuse := func(d *mpStartPrepDisclosure, kind CommandID, id CommandIdentity, input any, want error) {
		t.Helper()
		before := mpStartPrepReadFacts(t, f)
		out, e := mpStartPrepRunWith(t, f, d, kind, id, input)
		if !errors.Is(e, want) || !reflect.DeepEqual(out, mpStartPrepResult{}) {
			t.Fatal("wrong owner refusal/result", kind, out, e, want)
		}
		if after := mpStartPrepReadFacts(t, f); after != before {
			t.Fatal("refusal changed owner/worker/receipt/ACK/debt", before, after)
		}
		mpStartPrepAssertReturned(t, f)
	}
	t.Run("700-foreign-session-and-altered-original", func(t *testing.T) {
		for _, d := range foreign {
			q := f.beginInput()
			q.actor = d.actor
			refuse(d, MPStartPrepBegin, f.beginIdentity(), q, ErrDisclosureDenied)
		}
		for _, c := range []struct {
			name   string
			change func(*mpStartPrepBeginInput)
			want   error
		}{
			{"grant", func(q *mpStartPrepBeginInput) { q.actor.grant = "changed-grant" }, ErrConflict},
			{"hash", func(q *mpStartPrepBeginInput) { q.actor.sessionHash = strings.Repeat("9", 64) }, ErrConflict},
			{"generation", func(q *mpStartPrepBeginInput) { q.actor.tokenGeneration++ }, ErrConflict},
			{"horizon", func(q *mpStartPrepBeginInput) { q.actor.horizonMS++ }, ErrConflict},
			{"epoch", func(q *mpStartPrepBeginInput) { q.actor.epoch++ }, ErrConflict},
			{"birth-member", func(q *mpStartPrepBeginInput) { q.birth.birth.member = strings.Repeat("d", 32) }, ErrDisclosureDenied},
			{"birth-profile-lifetime", func(q *mpStartPrepBeginInput) { q.birth.birth.profileLifetime = strings.Repeat("d", 32) }, ErrDisclosureDenied},
		} {
			t.Run(c.name, func(t *testing.T) {
				q := f.beginInput()
				c.change(&q)
				refuse(current, MPStartPrepBegin, f.beginIdentity(), q, c.want)
			})
		}
		before := mpStartPrepReadFacts(t, f)
		out, e := mpStartPrepRunWith(t, f, current, MPStartPrepBegin, f.beginIdentity(), f.beginInput())
		if e != nil || !out.Outcome.Replayed || out.Value != birth.Value || out.Digest != birth.Digest || mpStartPrepReadFacts(t, f) != before {
			t.Fatal("rotated original birth failed", out, e)
		}
	})
	for _, kind := range []CommandID{MPStartPrepClaim, MPStartPrepSeal} {
		sequence := int64(kind - MPStartPrepClaim + 1)
		identity := CommandIdentity{Incarnation: f.inc, Sequence: sequence, OperationID: [16]byte{byte(kind)}}
		var input any
		switch kind {
		case MPStartPrepClaim:
			input = f.claimInput(1, "mp-execution")
		case MPStartPrepSeal:
			input = f.sealInput(2, f.sealCompletion())
		}
		var original mpStartPrepResult
		for _, phase := range []string{"fresh", "replay", "retired"} {
			t.Run(string(rune('0'+sequence))+"/"+phase, func(t *testing.T) {
				// Current disclosure AND immutable request agree on the foreign
				// session. This must reach, and be stopped by, the owner check.
				for _, d := range foreign {
					var q any
					if kind == MPStartPrepClaim {
						v := input.(mpStartPrepClaimInput)
						v.fence.actor = d.actor
						q = v
					} else {
						v := input.(mpStartPrepSealInput)
						v.fence.actor = d.actor
						q = v
					}
					refuse(d, kind, identity, q, ErrDisclosureDenied)
				}
				// Recreate each native lifetime in the same family. Native
				// current proof passes; even ACKed-retired classification must
				// remain hidden from this new member/profile lifetime.
				for _, member := range []bool{true, false} {
					s := current.scope
					changed := s
					value := strings.Repeat("d", 32)
					var nativeSQL, familySQL, old, key string
					if member {
						changed.memberBirth = value
						old = s.memberBirth
						key = current.actor.account
						nativeSQL = `UPDATE direct_memberships SET incarnation=? WHERE account_id=?`
						familySQL = `UPDATE authorization_session_families SET command_member_incarnation=? WHERE id=?`
					} else {
						changed.profileBirth = value
						old = s.profileBirth
						key = current.actor.profile
						nativeSQL = `UPDATE direct_profiles SET lifetime=? WHERE id=?`
						familySQL = `UPDATE authorization_session_families SET command_profile_lifetime=? WHERE id=?`
					}
					exec := func(q string, args ...any) {
						t.Helper()
						if _, e := f.e.db.ExecContext(t.Context(), q, args...); e != nil {
							t.Fatal(e)
						}
					}
					exec(nativeSQL, value, key)
					exec(familySQL, value, current.actor.family)
					refuse(mpStartPrepDisclosureFor(current, current.actor, changed), kind, identity, input, ErrDisclosureDenied)
					exec(nativeSQL, old, key)
					exec(familySQL, old, current.actor.family)
				}
				before := mpStartPrepReadFacts(t, f)
				out, e := mpStartPrepRunWith(t, f, current, kind, identity, input)
				if e != nil {
					t.Fatal(out, e)
				}
				switch phase {
				case "fresh":
					if out.Outcome.State != Applied || out.Outcome.Replayed {
						t.Fatal(out)
					}
					original = out
				case "replay":
					if !out.Outcome.Replayed || out.Value != original.Value || out.Digest != original.Digest {
						t.Fatal("rotated original changed", out, original)
					}
				case "retired":
					if out.Outcome.State != ResultRetired || out.Value != (mpStartPrepValue{}) || out.Digest != (ResultDigest{}) {
						t.Fatal("retired leaked original", out)
					}
				}
				if phase != "fresh" && mpStartPrepReadFacts(t, f) != before {
					t.Fatal("replay changed owner/worker/receipt/ACK/debt")
				}
				if phase == "replay" {
					f.ack(t, sequence)
				}
			})
		}
	}
}
