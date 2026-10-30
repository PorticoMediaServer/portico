//go:build linux || darwin

package playbackdisc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"strings"
	"testing"
)

// The real original-preparation writer and accepted presentation graph qualify
// SQL custody/erasure here; the fixture does not claim playable-media measurement.
func TestProfileErasureRemovesOriginalPreparationAndKeepsLiveVetoes(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(map[bool]string{false: "sealed", true: "accepted"}[accepted], func(t *testing.T) {
			f := newDiscFixture(t)
			ident, err := identity.New(f.db, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err = f.exec(`INSERT INTO identity_state(singleton,sign_in_kind) VALUES(1,'local')`); err != nil {
				t.Fatal(err)
			}
			if err = f.exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES(?,'erase-owner',X'00','erase-primary',1)`, discAccountID); err != nil {
				t.Fatal(err)
			}
			if err = f.exec(`INSERT INTO direct_profiles(id,account_id,name) VALUES(?,?,'Kids')`, discProfileID, discAccountID); err != nil {
				t.Fatal(err)
			}
			bearer, err := ident.Issue(discAccountID, "erase-primary", "local", "owner", 1)
			if err != nil {
				t.Fatal(err)
			}
			rawKey := "profile-erasure-original-key"
			sum := sha256.Sum256([]byte(rawKey))
			snap := f.snapshot
			snap.Actor = prepActor(snap.Actor)
			snap.Actor.Authority = "local"
			snap.Operation.StartKeyDigest = hex.EncodeToString(sum[:])
			f.snapshot = snap
			if err = f.exec(`UPDATE playback_sessions SET session_hash=? WHERE id=?`, snap.Actor.SessionHash, discSessionID); err != nil {
				t.Fatal(err)
			}
			if err = f.publish(snap, plainCheck()); err != nil {
				t.Fatal(err)
			}
			if _, err = f.activate(snap.key(), snap.ID, plainCheck()); err != nil {
				t.Fatal(err)
			}
			binding := prepBinding(f, snap.Actor, snap.Source, snap.Operation, f.publicPID)
			key := PreparationKey{DeviceID: binding.Actor.DeviceID, StartKeyDigest: binding.Queue.StartKeyDigest}
			pending, err := f.prepAdmit("erase-original", key, binding, prepLimits(), prepPlain())
			if err != nil {
				t.Fatal(err)
			}
			assertLive := func() {
				t.Helper()
				for _, table := range []string{"playback_start_preparation_actor", "playback_start_preparation_lifecycle", "playback_start_preparation_source", "playback_start_preparation_queue"} {
					if _, e := f.db.Exec("DELETE FROM " + table + " WHERE operation_id='erase-original'"); e == nil {
						t.Fatalf("live preparation lost %s", table)
					}
				}
				if _, e := f.db.Exec(`DELETE FROM playback_start_preparations WHERE id='erase-original'`); e == nil {
					t.Fatal("live root deletion allowed")
				}
			}
			assertLive()
			claim, err := f.prepClaim(pending.Claim, "erase-execution", prepPlain())
			if err != nil {
				t.Fatal(err)
			}
			claim, err = f.prepMark(claim, prepPlain())
			if err != nil {
				t.Fatal(err)
			}
			if accepted {
				out := PreparationAccepted{SessionID: "erase-v1", SnapshotID: snap.ID, Legacy: Key{SessionID: snap.SessionID, Generation: snap.Generation}}
				prepSeedLegacySession(t, f, binding, out, rawKey, binding.AssetID, 0)
				if err = f.prepAccept(claim, out, prepPlain(), prepContinuationPlain()); err != nil {
					t.Fatal(err)
				}
				if _, err = f.db.Exec(`DELETE FROM playback_start_preparation_results WHERE operation_id='erase-original'`); err == nil {
					t.Fatal("live result deletion allowed")
				}
			}
			assertLive()
			preview, err := ident.PreviewDirectProfileDeletion(t.Context(), bearer.AccessToken, discProfileID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = ident.RetireDirectProfile(t.Context(), bearer.AccessToken, discProfileID, identity.ProfileRetirementRequest{ExpectedRevision: preview.Revision, Lifetime: preview.Lifetime, DeleteSavedResources: true}); err != nil {
				t.Fatal(err)
			}
			if _, err = f.db.Exec(`UPDATE security_purge_jobs SET profile_incarnation=? WHERE profile_id=?`, strings.Repeat("0", 32), discProfileID); err != nil {
				t.Fatal(err)
			}
			if _, err = f.db.Exec(`DELETE FROM playback_start_preparation_queue WHERE operation_id='erase-original'`); err == nil {
				t.Fatal("another lifetime authorized erase")
			}
			if _, err = f.db.Exec(`UPDATE security_purge_jobs SET profile_incarnation=? WHERE profile_id=?`, preview.Lifetime, discProfileID); err != nil {
				t.Fatal(err)
			}
			// Inject a rollback after the actor delete, before the root delete.
			if _, err = f.db.Exec(`CREATE TRIGGER erase_crash BEFORE DELETE ON playback_start_preparations BEGIN SELECT RAISE(ABORT,'injected erasure crash');END`); err != nil {
				t.Fatal(err)
			}
			failed := false
			for n := 0; n < 4000; n++ {
				worked, e := ident.PurgeProfileStep(context.Background())
				if e != nil {
					failed = true
					break
				}
				if !worked {
					break
				}
			}
			if !failed {
				t.Fatal("root crash boundary was not reached")
			}
			if n := mustInt(t, f.db, `SELECT count(*) FROM playback_start_preparation_actor WHERE operation_id='erase-original'`); n != 1 {
				t.Fatal("rollback lost actor mapper")
			}
			if _, err = f.db.Exec(`DROP TRIGGER erase_crash; UPDATE security_purge_jobs SET state='pending',next_attempt_ms=0 WHERE profile_id=?`, discProfileID); err != nil {
				t.Fatal(err)
			}
			resumed, err := identity.New(f.db, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			for n := 0; n < 4000; n++ {
				worked, e := resumed.PurgeProfileStep(context.Background())
				if e != nil {
					t.Fatal(e)
				}
				if !worked {
					break
				}
				if n == 3999 {
					t.Fatal("preparation erase failed to converge")
				}
			}
			for _, table := range []string{"playback_start_preparations", "playback_start_preparation_actor", "playback_start_preparation_lifecycle", "playback_start_preparation_source", "playback_start_preparation_queue", "playback_start_preparation_results"} {
				var n int
				if e := f.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil || n != 0 {
					t.Fatalf("private rows in %s: %d %v", table, n, e)
				}
			}
			if mustInt(t, f.db, `SELECT count(*) FROM catalog_entities`) == 0 || mustInt(t, f.db, `SELECT count(*) FROM catalog_assets`) == 0 {
				t.Fatal("profile erase removed library media")
			}
		})
	}
}
