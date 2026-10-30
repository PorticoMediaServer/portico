package dvr

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

func sharedClaims(t *testing.T) (*Store, *claim, Recording) {
	t.Helper()
	s, source, p, a := sharingStore(t)
	var records []Recording
	for i := 0; i < 2; i++ {
		o := livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: fmt.Sprint(i)}
		r, e := s.Schedule(context.Background(), a, o, ScheduleInput{RequestID: strings.Repeat(fmt.Sprint(i+1), 48), Occurrence: Occurrence{source.ID, p.ChannelID, source.Generation, p.ID}})
		if e != nil {
			t.Fatal(e)
		}
		records = append(records, r)
	}
	leader, found, e := s.claimNext(context.Background())
	if e != nil || !found || leader == nil {
		t.Fatalf("leader: %v %v", leader, e)
	}
	follower := records[0]
	if follower.ID == leader.request.Recording.ID {
		follower = records[1]
	}
	if c, found, e := s.claimNext(context.Background()); e != nil || !found || c != nil {
		t.Fatalf("follower: %v %v", c, e)
	}
	return s, leader, follower
}

func TestSharedFollowerSurvivesEndDuringLeaderFinalization(t *testing.T) {
	for _, leaderState := range []string{"recording", "finalizing", "completed", "incomplete-playable"} {
		t.Run(leaderState, func(t *testing.T) {
			s, c, follower := sharedClaims(t)
			end, _ := time.Parse(time.RFC3339Nano, follower.End)
			s.now = func() time.Time { return end.Add(time.Second) }
			if _, e := s.db.Exec(`UPDATE dvr_recordings SET state=? WHERE id=?`, leaderState, c.request.Recording.ID); e != nil {
				t.Fatal(e)
			}
			if e := s.ReconcileScheduled(context.Background()); e != nil {
				t.Fatal(e)
			}
			if e := s.ExpireMissed(context.Background()); e != nil {
				t.Fatal(e)
			}
			var state string
			if e := s.db.QueryRow(`SELECT state FROM dvr_recordings WHERE id=?`, follower.ID).Scan(&state); e != nil {
				t.Fatal(e)
			}
			if state != "scheduled" {
				t.Fatalf("shared follower lost during %s: %s", leaderState, state)
			}
		})
	}
}

func TestSharedFollowerStillChecksUnclaimedRule(t *testing.T) {
	for _, which := range []string{"disabled", "deleted", "edited"} {
		t.Run(which, func(t *testing.T) {
			s, c, follower := sharedClaims(t)
			// The completed leader is ready to attach; rule maintenance has not run.
			if _, e := s.db.Exec(`UPDATE dvr_recordings SET state='completed' WHERE id=?`, c.request.Recording.ID); e != nil {
				t.Fatal(e)
			}
			var profile string
			if e := s.db.QueryRow(`SELECT profile_id FROM dvr_recordings WHERE id=?`, follower.ID).Scan(&profile); e != nil {
				t.Fatal(e)
			}
			enabled, deleted, revision := 1, 0, 1
			if which == "disabled" {
				enabled = 0
			}
			if which == "deleted" {
				deleted = 1
			}
			if which == "edited" {
				revision = 2
			}
			if _, e := s.db.Exec(`INSERT INTO dvr_rules(id,owner_key,authority,account_id,profile_id,source_id,series_id,revision,config_json,enabled,deleted,updated_ms) VALUES('rule',?,'local','account',?,?,'series',?,'{}',?,?,0)`, livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: profile}.Key(), profile, follower.Occurrence.SourceID, revision, enabled, deleted); e != nil {
				t.Fatal(e)
			}
			if _, e := s.db.Exec(`UPDATE dvr_recordings SET rule_id='rule',rule_revision=1,next_attempt_ms=0 WHERE id=?`, follower.ID); e != nil {
				t.Fatal(e)
			}
			attached, found, e := s.claimNext(context.Background())
			if e != nil || !found {
				t.Fatalf("claim: %v", e)
			}
			if attached != nil {
				t.Fatalf("%s rule attached shared media", which)
			}
		})
	}
}

func TestCancelledCaptureLeaderKeepsAuthorizedFollower(t *testing.T) {
	for _, action := range []string{"cancel", "delete"} {
		t.Run(action, func(t *testing.T) {
			s, c, _ := sharedClaims(t)
			ctx := context.Background()
			var revision int64
			if e := s.db.QueryRow(`SELECT revision FROM dvr_recordings WHERE id=?`, c.request.Recording.ID).Scan(&revision); e != nil {
				t.Fatal(e)
			}
			a := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
				return "owner", func(string, string) bool { return true }, nil
			}
			mutation := Mutation{RequestID: strings.Repeat("f", 48), ExpectedRevision: revision}
			var e error
			if action == "cancel" {
				_, e = s.Cancel(ctx, a, c.request.Owner, c.request.Recording.ID, mutation)
			} else {
				_, e = s.Delete(ctx, a, c.request.Owner, c.request.Recording.ID, mutation)
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = s.renew(ctx, c, false); e != nil {
				t.Fatalf("%s cut authorized follower's capture: %v", action, e)
			}
			cp := CaptureCheckpoint{Object: mediaartifact.Object{Size: 1880}, First: time.Now().Add(-time.Minute), Last: time.Now()}
			if e = s.checkpoint(ctx, c, cp); e != nil {
				t.Fatal(e)
			}
			var state string
			if e = s.db.QueryRow(`SELECT state FROM dvr_recordings WHERE id=?`, c.request.Recording.ID).Scan(&state); e != nil {
				t.Fatal(e)
			}
			expected := "cancelled"
			if action == "delete" {
				expected = "pending-delete"
			}
			if state != expected {
				t.Fatalf("capture resurrected cancelled owner: %s", state)
			}
		})
	}
}

func cancelledLeader(t *testing.T, action string) (*Store, *claim, Recording) {
	t.Helper()
	s, c, follower := sharedClaims(t)
	var revision int64
	if e := s.db.QueryRow(`SELECT revision FROM dvr_recordings WHERE id=?`, c.request.Recording.ID).Scan(&revision); e != nil {
		t.Fatal(e)
	}
	auth := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		return "owner", func(string, string) bool { return true }, nil
	}
	mutation := Mutation{RequestID: strings.Repeat("e", 48), ExpectedRevision: revision}
	var e error
	if action == "delete" {
		_, e = s.Delete(context.Background(), auth, c.request.Owner, c.request.Recording.ID, mutation)
	} else {
		_, e = s.Cancel(context.Background(), auth, c.request.Owner, c.request.Recording.ID, mutation)
	}
	if e != nil {
		t.Fatal(e)
	}
	return s, c, follower
}

func TestWithdrawnLeaderWaitsForSealAndPublishesOnlyFollower(t *testing.T) {
	for _, action := range []string{"cancel", "delete"} {
		t.Run(action, func(t *testing.T) {
			s, c, follower := cancelledLeader(t, action)
			ctx := context.Background()
			if _, e := s.db.Exec(`UPDATE dvr_recordings SET next_attempt_ms=0 WHERE id=?`, follower.ID); e != nil {
				t.Fatal(e)
			}
			if attached, found, e := s.claimNext(ctx); e != nil || !found || attached != nil {
				t.Fatalf("unsealed leader prematurely attached: %v %v", attached, e)
			}
			hash := strings.Repeat("d", 64)
			cp := CaptureCheckpoint{Object: mediaartifact.Object{Digest: hash, Size: 1880}, First: time.Now().Add(-time.Minute), Last: time.Now(), WriterRetired: true, Gaps: 1}
			if e := s.checkpoint(ctx, c, cp); e != nil {
				t.Fatal(e)
			}
			root := filepath.Join(t.TempDir(), c.request.Owner.Key())
			result := CaptureResult{CaptureCheckpoint: cp, Root: root, Path: filepath.Join(root, "captures", c.request.Recording.ID, "objects", hash), Container: "mpegts", VideoCodec: "mpeg2video", Duration: 60}
			if e := s.publish(ctx, c, result); e != nil {
				t.Fatal(e)
			}
			var item, lease int64
			var raw string
			if e := s.db.QueryRow(`SELECT item_id,lease_until_ms,checkpoint_json FROM dvr_recordings WHERE id=?`, c.request.Recording.ID).Scan(&item, &lease, &raw); e != nil {
				t.Fatal(e)
			}
			var saved CaptureCheckpoint
			if e := json.Unmarshal([]byte(raw), &saved); e != nil {
				t.Fatal(e)
			}
			if item != 0 || lease != 0 || !saved.SharedReady {
				t.Fatalf("withdrawn owner got publication or no sealed readiness: %d %d %+v", item, lease, saved)
			}
			if _, e := s.db.Exec(`UPDATE dvr_recordings SET next_attempt_ms=0 WHERE id=?`, follower.ID); e != nil {
				t.Fatal(e)
			}
			attached, found, e := s.claimNext(ctx)
			if e != nil || !found || attached == nil || attached.sharedFrom != c.request.Recording.ID {
				t.Fatalf("sealed leader not shared: %v %v", attached, e)
			}
			attached.checkpoint = saved
			root = filepath.Join(t.TempDir(), attached.request.Owner.Key())
			result.Root, result.Path = root, filepath.Join(root, "captures", attached.request.Recording.ID, "objects", hash)
			if e = s.publish(ctx, attached, result); e != nil {
				t.Fatal(e)
			}
			var count int
			if e = s.db.QueryRow(`SELECT count(*) FROM dvr_catalog_provenance`).Scan(&count); e != nil || count != 1 {
				t.Fatalf("profile publication count %d %v", count, e)
			}
		})
	}
}

func TestWithdrawnLeaderCannotContinueWithoutAuthorizedFollower(t *testing.T) {
	s, c, _ := cancelledLeader(t, "cancel")
	s.durable = func(_ context.Context, _ *sql.Tx, o livechannels.Owner, _, _ string) error {
		if o == c.request.Owner {
			return nil
		}
		return ErrDenied
	}
	if e := s.renew(context.Background(), c, false); e == nil {
		t.Fatal("capture continued without an authorized follower")
	}
}

func TestWithdrawnLeaderUsesRemainingOwnersAuthority(t *testing.T) {
	s, c, _ := cancelledLeader(t, "cancel")
	s.durable = func(_ context.Context, _ *sql.Tx, o livechannels.Owner, _, _ string) error {
		if o == c.request.Owner {
			return ErrDenied
		}
		return nil
	}
	if e := s.renew(context.Background(), c, false); e != nil {
		t.Fatalf("authorized remaining owner's capture cut: %v", e)
	}
}

func TestWithdrawnSharedLeaderCanRecoverCheckpoint(t *testing.T) {
	s, c, follower := cancelledLeader(t, "cancel")
	ctx := context.Background()
	cp := CaptureCheckpoint{Object: mediaartifact.Object{Size: 1880}, First: time.Now().Add(-time.Minute), Last: time.Now()}
	if e := s.checkpoint(ctx, c, cp); e != nil {
		t.Fatal(e)
	}
	now := s.now().Add(time.Minute)
	s.now = func() time.Time { return now }
	if _, e := s.db.Exec(`UPDATE live_allocations SET state='released' WHERE id=?`, c.request.Allocation.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec(`UPDATE dvr_recordings SET next_attempt_ms=? WHERE id=?`, now.Add(time.Hour).UnixMilli(), follower.ID); e != nil {
		t.Fatal(e)
	}
	recovered, found, e := s.claimNext(ctx)
	if e != nil || !found || recovered == nil || !recovered.recovery || !recovered.recoveryContinues || recovered.request.Recording.ID != c.request.Recording.ID {
		t.Fatalf("checkpoint not recoverable for follower: %+v %v", recovered, e)
	}
	var state string
	if e = s.db.QueryRow(`SELECT state FROM dvr_recordings WHERE id=?`, c.request.Recording.ID).Scan(&state); e != nil {
		t.Fatal(e)
	}
	if state != "cancelled" {
		t.Fatalf("recovery resurrected owner: %s", state)
	}
}

func TestSharedFinalizationRetriesTransientFailureWithFiniteBudget(t *testing.T) {
	s, c, follower := sharedClaims(t)
	ctx := context.Background()
	cp := CaptureCheckpoint{Object: mediaartifact.Object{Digest: strings.Repeat("d", 64), Size: 1880}, WriterRetired: true}
	body, e := json.Marshal(cp)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`UPDATE dvr_recordings SET state='completed',checkpoint_json=? WHERE id=?`, string(body), c.request.Recording.ID); e != nil {
		t.Fatal(e)
	}
	clock := s.now()
	s.now = func() time.Time { return clock }
	for attempt := 0; attempt < 3; attempt++ {
		if _, e = s.db.Exec(`UPDATE dvr_recordings SET next_attempt_ms=0 WHERE id=?`, follower.ID); e != nil {
			t.Fatal(e)
		}
		attached, found, e := s.claimNext(ctx)
		if e != nil || !found || attached == nil {
			t.Fatalf("retry %d not claimed: %v", attempt, e)
		}
		s.runSharedClaim(ctx, attached) // The fixture driver returns ErrUnavailable.
		var state string
		if e = s.db.QueryRow(`SELECT state FROM dvr_recordings WHERE id=?`, follower.ID).Scan(&state); e != nil {
			t.Fatal(e)
		}
		if state != "finalizing" {
			t.Fatalf("transient share failure became terminal on attempt %d: %s", attempt, state)
		}
		clock = clock.Add(4 * time.Second)
	}
	attached, found, e := s.claimNext(ctx)
	if e != nil || !found || attached != nil {
		t.Fatalf("exhausted share budget retried: %+v %v", attached, e)
	}
	var state string
	if e = s.db.QueryRow(`SELECT state FROM dvr_recordings WHERE id=?`, follower.ID).Scan(&state); e != nil {
		t.Fatal(e)
	}
	if state != "failed" {
		t.Fatalf("share retry can wait forever: %s", state)
	}
}
