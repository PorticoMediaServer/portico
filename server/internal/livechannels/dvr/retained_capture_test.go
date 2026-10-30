package dvr

import (
	"context"
	"database/sql"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/recordingaccess"
	"reflect"
	"strings"
	"testing"
	"time"
)

func retainedRow(t *testing.T, db *sql.DB, table, id string) []any {
	t.Helper()
	rows, err := db.Query("SELECT * FROM "+table+" WHERE id=?", id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatalf("missing %s", table)
	}
	out := make([]any, len(cols))
	ptr := make([]any, len(cols))
	for i := range out {
		ptr[i] = &out[i]
	}
	if err = rows.Scan(ptr...); err != nil {
		t.Fatal(err)
	}
	return out
}
func TestInProgressRecordingSurvivesProfileEraseAndRetainsSourceFences(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, mode := range []string{"finish", "source-revoked", "preparing", "recovery", "account-disabled", "stale-token"} {
		t.Run(mode, func(t *testing.T) {
			s, src, programme, auth := sharingStore(t)
			ctx := context.Background()
			owner := livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "0"}
			if _, err := s.db.Exec(`INSERT INTO live_source_settings(source_id,viewer_access) VALUES(?,'server-members') ON CONFLICT(source_id) DO UPDATE SET viewer_access='server-members'`, src.ID); err != nil {
				t.Fatal(err)
			}
			policy := recordingaccess.Policy{Schema: recordingaccess.NewSchema()}
			s.durable = policy.Durable
			if err := s.ConfigureRetainedCaptures(policy.RetainedCapture); err != nil {
				t.Fatal(err)
			}
			first, err := s.Schedule(ctx, auth, owner, ScheduleInput{RequestID: strings.Repeat("e", 48), Occurrence: Occurrence{src.ID, programme.ChannelID, src.Generation, programme.ID}})
			if err != nil {
				t.Fatal(err)
			}
			c, found, err := s.claimNext(ctx)
			if err != nil || !found || c == nil {
				t.Fatalf("claim: %v %v", c, err)
			}
			futureID := strings.Repeat("9", 64)
			futureStart := time.Now().UTC().Add(2 * time.Hour)
			if _, err = s.db.Exec(`INSERT INTO live_programmes(generation_id,id,channel_id,provider_key,title,start_utc,end_utc,lineage) VALUES(?,?,?,'next','Next',?,?,'next')`, src.Generation, futureID, programme.ChannelID, futureStart.Format(time.RFC3339), futureStart.Add(time.Hour).Format(time.RFC3339)); err != nil {
				t.Fatal(err)
			}
			next, err := s.Schedule(ctx, auth, owner, ScheduleInput{RequestID: strings.Repeat("f", 48), Occurrence: Occurrence{src.ID, programme.ChannelID, src.Generation, futureID}})
			if err != nil {
				t.Fatal(err)
			}
			hash := strings.Repeat("d", 64)
			cp := CaptureCheckpoint{Object: mediaartifact.Object{Digest: hash, Size: 1880}, First: time.Now().Add(-time.Minute), Last: time.Now()}
			if mode != "preparing" {
				if err = s.checkpoint(ctx, c, cp); err != nil {
					t.Fatal(err)
				}
			}
			ident, err := identity.New(s.db, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(`INSERT INTO identity_state(singleton,sign_in_kind) VALUES(1,'local')`); err != nil {
				t.Fatal(err)
			}
			bearer, err := ident.Issue("account", "profile", "local", "owner", 1)
			if err != nil {
				t.Fatal(err)
			}
			preview, err := ident.PreviewDirectProfileDeletion(ctx, bearer.AccessToken, owner.ProfileID)
			if err != nil {
				t.Fatal(err)
			}
			var revisionBefore int64
			if err = s.db.QueryRow(`SELECT revision FROM dvr_owner_revisions WHERE owner_key=?`, owner.Key()).Scan(&revisionBefore); err != nil {
				t.Fatal(err)
			}
			allocationBefore := retainedRow(t, s.db, "live_allocations", c.request.Allocation.ID)
			recordingBefore := retainedRow(t, s.db, "dvr_recordings", first.ID)
			if _, err = ident.RetireDirectProfile(ctx, bearer.AccessToken, owner.ProfileID, identity.ProfileRetirementRequest{ExpectedRevision: preview.Revision, Lifetime: preview.Lifetime, DeleteSavedResources: true}); err != nil {
				t.Fatal(err)
			}
			for n := 0; n < 4000; n++ {
				worked, e := ident.PurgeProfileStep(ctx)
				if e != nil {
					t.Fatal(e)
				}
				if !worked {
					break
				}
				if n == 3999 {
					t.Fatal("profile erase did not converge")
				}
			}
			if !reflect.DeepEqual(allocationBefore, retainedRow(t, s.db, "live_allocations", c.request.Allocation.ID)) {
				t.Fatal("erase changed a recording allocation")
			}
			if !reflect.DeepEqual(recordingBefore, retainedRow(t, s.db, "dvr_recordings", first.ID)) {
				t.Fatal("erase changed a recording")
			}
			var retainedRevision int64
			if err = s.db.QueryRow(`SELECT revision FROM dvr_owner_revisions WHERE owner_key=?`, owner.Key()).Scan(&retainedRevision); err != nil || retainedRevision != revisionBefore {
				t.Fatalf("retained media revision changed during erase: %d/%d %v", retainedRevision, revisionBefore, err)
			}
			if _, err = s.Schedule(ctx, auth, owner, ScheduleInput{RequestID: strings.Repeat("a", 48), Occurrence: Occurrence{src.ID, programme.ChannelID, src.Generation, futureID}}); err == nil {
				t.Fatal("erased creator admitted a new schedule")
			}
			err = nil
			switch mode {
			case "source-revoked":
				_, err = s.db.Exec(`UPDATE live_source_settings SET viewer_access='owner-only' WHERE source_id=?`, src.ID)
			case "account-disabled":
				_, err = s.db.Exec(`UPDATE direct_memberships SET disabled=1 WHERE account_id='account'`)
			case "recovery":
				c.recovery = true
			case "stale-token":
				c.token = "another-token"
			}
			if err != nil {
				t.Fatal(err)
			}
			err = s.renew(ctx, c, false)
			if mode != "finish" {
				if err == nil {
					t.Fatalf("%s granted retained continuation", mode)
				}
				return
			}
			if err != nil {
				t.Fatalf("in-progress capture cancelled: %v", err)
			}
			cp.WriterRetired = true
			if err = s.checkpoint(ctx, c, cp); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(t.TempDir(), owner.Key())
			result := CaptureResult{CaptureCheckpoint: cp, Root: root, Path: filepath.Join(root, "captures", first.ID, "objects", hash), Container: "mpegts", VideoCodec: "mpeg2video", Duration: 60, Width: 160, Height: 90}
			if err = s.publish(ctx, c, result); err != nil {
				t.Fatalf("retained capture did not publish: %v", err)
			}
			if err = s.db.QueryRow(`SELECT revision FROM dvr_owner_revisions WHERE owner_key=?`, owner.Key()).Scan(&retainedRevision); err != nil || retainedRevision <= revisionBefore {
				t.Fatalf("continuation recreated instead of advancing retained revision: %d/%d %v", retainedRevision, revisionBefore, err)
			}
			var state string
			var item int64
			if err = s.db.QueryRow(`SELECT state,item_id FROM dvr_recordings WHERE id=?`, first.ID).Scan(&state, &item); err != nil || item == 0 || (state != "completed" && state != "incomplete-playable") {
				t.Fatalf("capture not kept as media: %s %d %v", state, item, err)
			}
			s.now = func() time.Time { return futureStart.Add(time.Second) }
			claimed, _, err := s.claimNext(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if claimed != nil {
				t.Fatal("retired creator's next programme started capture")
			}
			if err = s.db.QueryRow(`SELECT state FROM dvr_recordings WHERE id=?`, next.ID).Scan(&state); err != nil || state == "recording" || state == "preparing" {
				t.Fatalf("future programme recorded: %s %v", state, err)
			}
		})
	}
}
