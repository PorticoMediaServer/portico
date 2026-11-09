package writer

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// One real full-F fixture per command exercises each refusal before Fresh,
// saved-original replay and ACKed-retired replay. Plain native reads independently
// pin custody, lifecycle, receipt/ACK/debt and worker counts after every refusal.
func TestMPStartAcceptScopeAuthority(t *testing.T) {
	for _, kind := range []CommandID{MPStartPrepAccept, MPStartPrepRetire, MPStartPrepTimeout} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			f := mpStartAcceptFixtureFor(t)
			mpStartAcceptSealed(t, f)
			if kind == MPStartPrepTimeout {
				mpStartExpire(t, f)
			}
			old := f.cap
			// Rotate BEFORE the fresh terminal command. Its immutable payload
			// still carries the original actor; scope uses only this live token.
			f.cap = mpStartPrepRotateCredential(t, f, old, 2, strings.Repeat("8", 64))
			originalFence := f.acceptFence("mp-execution", 3)
			identity := CommandIdentity{Incarnation: f.inc, Sequence: 3, OperationID: [16]byte{0x75}}
			run := func(d *mpStartPrepDisclosure, fence mpStartAcceptFence) (mpStartAcceptResult, error) {
				var input any
				switch kind {
				case MPStartPrepAccept:
					input = mpStartAcceptInput{fence}
				case MPStartPrepRetire:
					input = mpStartRetireInput{fence}
				case MPStartPrepTimeout:
					input = mpStartTimeoutInput{fence}
				}
				c, err := newMPStartAcceptCommand(kind, identity, input)
				if err != nil {
					t.Fatal(err)
				}
				return f.w.runMPStartAccept(t.Context(), d, kind, c.reference, input)
			}
			exec := func(q string, args ...any) {
				t.Helper()
				if _, err := f.e.db.ExecContext(t.Context(), q, args...); err != nil {
					t.Fatal(err)
				}
			}
			var original mpStartAcceptResult
			for _, phase := range []string{"fresh", "replay", "acked-retired"} {
				t.Run(phase, func(t *testing.T) {
					if phase == "acked-retired" {
						f.ack(t, 3)
					}
					before := mpStartAcceptReadFacts(t, f)
					denied := func(d *mpStartPrepDisclosure, fence mpStartAcceptFence) {
						t.Helper()
						out, err := run(d, fence)
						if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, mpStartAcceptResult{}) {
							t.Fatal("scope refusal leaked a result or wrong classification", out, err)
						}
						if after := mpStartAcceptReadFacts(t, f); after != before {
							t.Fatalf("denial wrote effects: before %+v after %+v", before, after)
						}
						mpStartPrepAssertReturned(t, f)
					}
					scope := f.cap.scope
					t.Run("retired-token", func(t *testing.T) { denied(old, originalFence) })
					t.Run("foreign-family", func(t *testing.T) {
						a := f.cap.actor
						a.family = "foreign-family"
						denied(mpStartPrepDisclosureFor(f.cap, a, scope), originalFence)
					})
					t.Run("member-library-removed", func(t *testing.T) {
						exec(`UPDATE direct_memberships SET allowed_libraries=? WHERE account_id=?`, `["other-library"]`, f.actor.account)
						// A stale in-memory allow list must fail native reproof.
						denied(f.cap, originalFence)
						f.cap.scope.memberLibraries = `["other-library"]`
						denied(f.cap, originalFence)
						f.cap.scope = scope
						exec(`UPDATE direct_memberships SET allowed_libraries=? WHERE account_id=?`, scope.memberLibraries, f.actor.account)
					})
					t.Run("profile-tightened", func(t *testing.T) {
						exec(`UPDATE direct_profiles SET allowed_libraries=? WHERE id=?`, `["other-library"]`, f.actor.profile)
						denied(f.cap, originalFence)
						f.cap.scope.profileLibraries = sql.NullString{String: `["other-library"]`, Valid: true}
						denied(f.cap, originalFence)
						f.cap.scope = scope
						exec(`UPDATE direct_profiles SET allowed_libraries=? WHERE id=?`, scope.profileLibraries, f.actor.profile)
					})
					t.Run("capability-library-mismatch", func(t *testing.T) {
						f.cap.library = "library-two"
						denied(f.cap, originalFence)
						f.cap.library = f.source.library
					})
					t.Run("request-library-mismatch", func(t *testing.T) {
						fence := originalFence
						fence.source.library = "library-two"
						denied(f.cap, fence)
					})
					t.Run("stored-owner-library-mismatch", func(t *testing.T) {
						// Both libraries are currently permitted, so only the
						// native owner's exact library can reject this swap.
						f.cap.scope.memberLibraries = `["library-one","library-two"]`
						exec(`UPDATE direct_memberships SET allowed_libraries=? WHERE account_id=?`, f.cap.scope.memberLibraries, f.actor.account)
						f.cap.library = "library-two"
						fence := originalFence
						fence.source.library = "library-two"
						denied(f.cap, fence)
						f.cap.library = f.source.library
						f.cap.scope = scope
						exec(`UPDATE direct_memberships SET allowed_libraries=? WHERE account_id=?`, scope.memberLibraries, f.actor.account)
					})
					t.Run("foreign-profile-lifetime", func(t *testing.T) {
						lifetime := strings.Repeat("d", 32)
						exec(`UPDATE direct_profiles SET lifetime=? WHERE id=?`, lifetime, f.actor.profile)
						exec(`UPDATE authorization_session_families SET command_profile_lifetime=? WHERE id=?`, lifetime, f.actor.family)
						f.cap.scope.profileBirth = lifetime
						denied(f.cap, originalFence)
						f.cap.scope = scope
						exec(`UPDATE direct_profiles SET lifetime=? WHERE id=?`, scope.profileBirth, f.actor.profile)
						exec(`UPDATE authorization_session_families SET command_profile_lifetime=? WHERE id=?`, scope.profileBirth, f.actor.family)
					})
					out, err := run(f.cap, originalFence)
					if err != nil {
						t.Fatal("unchanged scope refused", out, err)
					}
					switch phase {
					case "fresh":
						if out.Outcome.State != Applied || out.Outcome.Replayed || out.Digest == (ResultDigest{}) {
							t.Fatal(out)
						}
						original = out
					case "replay":
						if out.Outcome.State != Applied || !out.Outcome.Replayed || out.Value != original.Value || out.Digest != original.Digest {
							t.Fatal("original changed", out, original)
						}
					case "acked-retired":
						if out.Outcome.State != ResultRetired || out.Value != (mpStartAcceptValue{}) || out.Digest != (ResultDigest{}) {
							t.Fatal("retired result disclosed payload", out)
						}
					}
					if phase != "fresh" {
						if after := mpStartAcceptReadFacts(t, f); after != before {
							t.Fatalf("replay wrote effects: before %+v after %+v", before, after)
						}
					}
					// A current explicitly restricted profile allowing this
					// library also passes the alternate native statement.
					if phase == "replay" {
						exec(`UPDATE direct_profiles SET allowed_libraries=? WHERE id=?`, `["library-one"]`, f.actor.profile)
						f.cap.scope.profileLibraries = sql.NullString{String: `["library-one"]`, Valid: true}
						restricted, err := run(f.cap, originalFence)
						if err != nil || restricted.Value != original.Value || restricted.Digest != original.Digest || !restricted.Outcome.Replayed {
							t.Fatal(restricted, err)
						}
						if after := mpStartAcceptReadFacts(t, f); after != before {
							t.Fatal("restricted replay wrote effects", after, before)
						}
						f.cap.scope = scope
						exec(`UPDATE direct_profiles SET allowed_libraries=? WHERE id=?`, scope.profileLibraries, f.actor.profile)
					}
					mpStartPrepAssertReturned(t, f)
				})
			}
		})
	}
}
