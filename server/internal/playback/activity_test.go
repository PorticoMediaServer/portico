package playback

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"strings"
	"testing"
	"time"
)

func activityFixture(t *testing.T) (*sql.DB, *Service, identity.Principal, ActivityScope, string, int64) {
	t.Helper()
	root := t.TempDir()
	db, e := persistence.Open(filepath.Join(root, "db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	ident, e := identity.New(db, root)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','profile',1)`)
	if e != nil {
		t.Fatal(e)
	}
	id, item, _ := catalogFixture(t, db, "lib", "/secret/source", compactcatalog.Movie, "item", "Film", compactcatalog.Asset{Path: "/secret/source/file.mp4", Size: 1, ModifiedNS: 1, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 640, Height: 360, Duration: 100})
	settlePlaybackCatalog(t, db)
	session, e := ident.Issue("owner", "profile", "local", "owner", 1)
	if e != nil {
		t.Fatal(e)
	}
	p, e := ident.Authenticate(session.AccessToken)
	if e != nil {
		t.Fatal(e)
	}
	return db, New(db), p, ActivityScope{"server", "viewer"}, item, id
}
func operation(n int) string {
	return fmt.Sprintf("%013d-00000000-0000-0000-0000-%012d", time.Now().UnixMilli(), n)
}
func TestActivityPaginationReportsAndScope(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, s, p, scope, item, _ := activityFixture(t)
	ctx := context.Background()
	var last Session
	for i := 0; i < 85; i++ {
		var e error
		last, e = s.Create(p, item, "automatic", fmt.Sprint(i))
		if e != nil {
			t.Fatal(e)
		}
	}
	first, e := s.Activity(ctx, p, scope, "recent", 40, "")
	if e != nil || len(first.Items) != 40 || first.NextCursor == "" {
		t.Fatal(first, e)
	}
	if first.Items[0].ReportedAt != nil {
		t.Fatal("fabricated report")
	}
	if e = s.Progress(p, last.ID, last.Generation, 1, 17, "playing"); e != nil {
		t.Fatal(e)
	}
	if e = s.Progress(p, last.ID, last.Generation, 0, 90, "playing"); e != nil {
		t.Fatal(e)
	}
	now, e := s.Activity(ctx, p, scope, "open", 40, "")
	if e != nil || len(now.Items) != 40 || now.Items[0].SessionID != last.ID || now.Items[0].ReportedAt == nil || *now.Items[0].PositionMs != 17000 {
		t.Fatal(now, e)
	}
	if _, e = s.Create(p, item, "automatic", "new-after-first-page"); e != nil {
		t.Fatal(e)
	}
	second, e := s.Activity(ctx, p, scope, "recent", 40, first.NextCursor)
	if e != nil || len(second.Items) != 40 {
		t.Fatal(second, e)
	}
	third, e := s.Activity(ctx, p, scope, "recent", 40, second.NextCursor)
	if e != nil || len(third.Items) != 5 || third.NextCursor != "" {
		t.Fatal(third, e)
	}
	seen := map[string]bool{}
	for _, page := range []ActivityPage{first, second, third} {
		for _, r := range page.Items {
			if seen[r.SessionID] {
				t.Fatal("duplicate")
			}
			seen[r.SessionID] = true
		}
		raw, _ := json.Marshal(page)
		if strings.Contains(string(raw), "grant") || strings.Contains(string(raw), "secret/source") || strings.Contains(string(raw), p.Hash) {
			t.Fatal("leak")
		}
	}
	if _, e = s.Activity(ctx, p, ActivityScope{"server", "other"}, "recent", 40, first.NextCursor); !errors.Is(e, ErrActivityQuery) {
		t.Fatal(e)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, e = s.Activity(cancelCtx, p, scope, "recent", 40, ""); e == nil {
		t.Fatal("cancel ignored")
	}
	db.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id=(SELECT family_id FROM authorization_family_tokens WHERE token_hash=?)`, p.Hash)
	if _, e = s.Activity(ctx, p, scope, "recent", 40, ""); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal(e)
	}
}
func TestActivityStopReplayConflictExpiryAndGrant(t *testing.T) {
	db, s, p, scope, item, _ := activityFixture(t)
	ctx := context.Background()
	a, e := s.Create(p, item, "automatic", "a")
	if e != nil {
		t.Fatal(e)
	}
	cmd := ActivityStop{operation(1), a.Generation}
	if _, _, _, e = s.ResolveGrant(strings.TrimPrefix(a.StreamURL, "/v1/media/")); e != nil {
		t.Fatal(e)
	}
	receipt, e := s.ActivityStop(ctx, p, scope, a.ID, cmd, false)
	if e != nil || !receipt.AuthorizationStopped || receipt.State != "stopped" {
		t.Fatal(receipt, e)
	}
	if _, _, _, e = s.ResolveGrant(strings.TrimPrefix(a.StreamURL, "/v1/media/")); e == nil {
		t.Fatal("grant survived stop")
	}
	b, e := s.Create(p, item, "automatic", "b")
	if e != nil {
		t.Fatal(e)
	}
	restarted := New(db)
	again, e := restarted.ActivityStop(ctx, p, scope, a.ID, cmd, false)
	if e != nil || again != receipt {
		t.Fatal(again, e)
	}
	got, e := s.Get(p, b.ID)
	if e != nil || got.State == "stopped" {
		t.Fatal("replacement stopped")
	}
	if _, e = s.ActivityStop(ctx, p, scope, b.ID, cmd, false); !errors.Is(e, ErrActivityConflict) {
		t.Fatal(e)
	}
	if _, e = s.ActivityStop(ctx, p, scope, b.ID, ActivityStop{operation(2), a.Generation}, false); !errors.Is(e, ErrActivityConflict) {
		t.Fatal(e)
	}
	recovered, e := s.ActivityStop(ctx, p, scope, "", ActivityStop{OperationID: cmd.OperationID}, true)
	if e != nil || recovered != receipt {
		t.Fatal(e)
	}
	db.Exec(`UPDATE playback_admin_operations SET created_at=?`, time.Now().Add(-31*24*time.Hour).Unix())
	if _, e = s.ActivityStop(ctx, p, scope, a.ID, cmd, false); !errors.Is(e, ErrActivityExpired) {
		t.Fatal(e)
	}
	old := fmt.Sprintf("%013d-00000000-0000-0000-0000-000000000004", time.Now().Add(-time.Hour).UnixMilli())
	if _, e = s.ActivityStop(ctx, p, scope, b.ID, ActivityStop{old, b.Generation}, false); !errors.Is(e, ErrActivityExpired) {
		t.Fatal(e)
	}
	db.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id=(SELECT family_id FROM authorization_family_tokens WHERE token_hash=?)`, p.Hash)
	if _, e = s.ActivityStop(ctx, p, scope, "", ActivityStop{OperationID: cmd.OperationID}, true); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal(e)
	}
}
func TestActivityCleanupAndOwnerBoundary(t *testing.T) {
	db, s, p, scope, item, _ := activityFixture(t)
	a, e := s.Create(p, item, "automatic", "a")
	if e != nil {
		t.Fatal(e)
	}
	db.Exec(`UPDATE playback_sessions SET mode='hls' WHERE id=?`, a.ID)
	db.Exec(`INSERT INTO playback_artifacts(session_id,directory,status) VALUES(?,'/secret/artifact','building')`, a.ID)
	receipt, e := s.ActivityStop(context.Background(), p, scope, a.ID, ActivityStop{operation(5), a.Generation}, false)
	if e != nil || receipt.CleanupStatus != "pending" {
		t.Fatal(receipt, e)
	}
	page, e := s.Activity(context.Background(), p, scope, "recent", 40, "")
	if e != nil || page.Items[0].CleanupStatus != "pending" {
		t.Fatal(page, e)
	}
	db.Exec(`DELETE FROM playback_artifacts WHERE session_id=?`, a.ID)
	page, e = s.Activity(context.Background(), p, scope, "recent", 40, "")
	if e != nil || page.Items[0].CleanupStatus != "complete" {
		t.Fatal(page, e)
	}
	for _, role := range []string{"member", "owner"} {
		q := p
		q.Role = role
		q.Authority = "hosted"
		if _, e = s.Activity(context.Background(), q, scope, "recent", 40, ""); !errors.Is(e, identity.ErrUnauthorized) {
			t.Fatal(e)
		}
	}
}

func TestActivityReceiptCapacityAndFreshRecovery(t *testing.T) {
	db, s, p, scope, item, _ := activityFixture(t)
	ctx := context.Background()
	a, e := s.Create(p, item, "automatic", "a")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ActivityStop(ctx, p, scope, "", ActivityStop{OperationID: operation(99)}, true); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal(e)
	}
	_, e = db.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<65536) INSERT INTO playback_admin_operations SELECT 'capacity-'||x,'a','f','{}',? FROM n`, time.Now().Unix())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ActivityStop(ctx, p, scope, a.ID, ActivityStop{operation(100), a.Generation}, false); !errors.Is(e, ErrActivityCapacity) {
		t.Fatal(e)
	}
	got, e := s.Get(p, a.ID)
	if e != nil || got.State == "stopped" {
		t.Fatal("capacity mutated session")
	}
	db.Exec(`UPDATE playback_admin_operations SET created_at=?`, time.Now().Add(-31*24*time.Hour).Unix())
	if _, e = s.ActivityStop(ctx, p, scope, a.ID, ActivityStop{operation(101), a.Generation}, false); e != nil {
		t.Fatal(e)
	}
}

func TestActivityStopOwnedSupervisorCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, s, p, scope, item, _ := activityFixture(t)
	a, e := s.Create(p, item, "automatic", "a")
	if e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	dir := filepath.Join(root, a.ID)
	if e = os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(dir, "master.m3u8"), []byte("fixture"), 0600)
	db.Exec(`UPDATE playback_sessions SET mode='hls' WHERE id=?`, a.ID)
	db.Exec(`INSERT INTO playback_artifacts(session_id,directory,status) VALUES(?,?,'building')`, a.ID, dir)
	cancelled := make(chan struct{}, 1)
	h := &HLS{db: db, root: root, active: map[string]context.CancelFunc{a.ID: func() {
		select {
		case cancelled <- struct{}{}:
		default:
		}
	}}}
	s.ConfigureHLS(h)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { h.Run(ctx); close(done) }()
	if _, e = s.ActivityStop(ctx, p, scope, a.ID, ActivityStop{operation(200), a.Generation}, false); e != nil {
		t.Fatal(e)
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("owned worker not cancelled")
	}
	h.mu.Lock()
	delete(h.active, a.ID)
	h.mu.Unlock()
	deadline := time.Now().Add(3 * time.Second)
	var page ActivityPage
	var activityErr error
	for {
		_, e = os.Stat(dir)
		page, activityErr = s.Activity(ctx, p, scope, "recent", 40, "")
		// The supervisor records completed cleanup only after it removes the
		// directory, so disappearance alone is not completion: wait for both.
		if activityErr == nil && os.IsNotExist(e) && len(page.Items) > 0 && page.Items[0].CleanupStatus == "complete" {
			break
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, e = os.Stat(dir); !os.IsNotExist(e) {
		t.Fatal("owned artifacts not removed", e)
	}
	if activityErr != nil || len(page.Items) == 0 || page.Items[0].CleanupStatus != "complete" {
		t.Fatal(page, activityErr)
	}
	cancel()
	<-done
}

func TestActivityCleanupFailureRetainsEvidence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, s, p, scope, item, _ := activityFixture(t)
	a, e := s.Create(p, item, "automatic", "a")
	if e != nil {
		t.Fatal(e)
	}
	blocked := filepath.Join(t.TempDir(), "blocked-root")
	if e = os.WriteFile(blocked, []byte("not a directory"), 0600); e != nil {
		t.Fatal(e)
	}
	db.Exec(`UPDATE playback_sessions SET mode='hls',state='stopped' WHERE id=?`, a.ID)
	db.Exec(`INSERT INTO playback_artifacts(session_id,directory,status) VALUES(?,?,'building')`, a.ID, filepath.Join(blocked, a.ID))
	h := &HLS{db: db, root: blocked, active: map[string]context.CancelFunc{}}
	s.ConfigureHLS(h)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.Run(ctx); close(done) }()
	time.Sleep(1200 * time.Millisecond)
	cancel()
	<-done
	page, e := s.Activity(context.Background(), p, scope, "recent", 40, "")
	if e != nil || page.Items[0].CleanupStatus != "pending" {
		t.Fatal("failed filesystem cleanup presented as complete", page, e)
	}
}

func TestActivityFailedAndExpiredCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, condition := range []string{"failed", "expired"} {
		t.Run(condition, func(t *testing.T) {
			db, s, p, scope, item, _ := activityFixture(t)
			a, e := s.Create(p, item, "automatic", "cleanup")
			if e != nil {
				t.Fatal(e)
			}
			root := t.TempDir()
			dir := filepath.Join(root, a.ID)
			os.Mkdir(dir, 0700)
			os.WriteFile(filepath.Join(dir, "master.m3u8"), []byte("fixture"), 0600)
			db.Exec(`UPDATE playback_sessions SET mode='hls',state='failed' WHERE id=?`, a.ID)
			if condition == "expired" {
				db.Exec(`UPDATE playback_sessions SET state='paused',expires_at=? WHERE id=?`, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), a.ID)
			}
			db.Exec(`INSERT INTO playback_artifacts(session_id,directory,status) VALUES(?,?,'failed')`, a.ID, dir)
			cancelled := make(chan struct{}, 1)
			h := &HLS{db: db, root: root, active: map[string]context.CancelFunc{a.ID: func() {
				select {
				case cancelled <- struct{}{}:
				default:
				}
			}}}
			s.ConfigureHLS(h)
			if condition == "failed" {
				receipt, e := s.ActivityStop(context.Background(), p, scope, a.ID, ActivityStop{operation(300), a.Generation}, false)
				if e != nil || receipt.State != "failed" || receipt.CleanupStatus != "pending" {
					t.Fatal(receipt, e)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { h.Run(ctx); close(done) }()
			defer func() { cancel(); <-done }()
			select {
			case <-cancelled:
			case <-time.After(3 * time.Second):
				t.Fatal("terminal worker not cancelled")
			}
			h.mu.Lock()
			delete(h.active, a.ID)
			h.mu.Unlock()
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				if _, e = os.Stat(dir); os.IsNotExist(e) {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if _, e = os.Stat(dir); !os.IsNotExist(e) {
				t.Fatal("artifact retained")
			}
			// Filesystem removal precedes the durable cleanup receipt. Wait for
			// that receipt rather than racing the transaction after os.Stat.
			var page ActivityPage
			for time.Now().Before(deadline) {
				page, e = s.Activity(context.Background(), p, scope, "recent", 40, "")
				if e != nil || page.Items[0].CleanupStatus == "complete" {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if e != nil || page.Items[0].CleanupStatus != "complete" {
				t.Fatal(page, e)
			}
			if condition == "failed" && page.Items[0].State != "failed" {
				t.Fatal("failed state overwritten")
			}
		})
	}
}

func TestActivityDeletionBetweenPages(t *testing.T) {
	db, s, p, scope, item, _ := activityFixture(t)
	for i := 0; i < 45; i++ {
		if _, e := s.Create(p, item, "automatic", fmt.Sprint(i)); e != nil {
			t.Fatal(e)
		}
	}
	page, e := s.Activity(context.Background(), p, scope, "recent", 40, "")
	if e != nil {
		t.Fatal(e)
	}
	var victim string
	db.QueryRow(`SELECT session_id FROM playback_observations ORDER BY ordinal LIMIT 1`).Scan(&victim)
	db.Exec(`DELETE FROM playback_sessions WHERE id=?`, victim)
	next, e := s.Activity(context.Background(), p, scope, "recent", 40, page.NextCursor)
	if e != nil || len(next.Items) != 4 || next.NextCursor != "" {
		t.Fatal(next, e)
	}
	for _, r := range next.Items {
		if r.SessionID == victim {
			t.Fatal("deleted row returned")
		}
	}
}

func TestActivitySparseOpenCost(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, s, p, scope, _, id := activityFixture(t)
	start := time.Now()
	asset := firstCatalogAssetToken(t, db)
	_, e := db.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<100000) INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration) SELECT 'sparse-'||x,?,'owner','profile',?,?,x,CASE WHEN x=1 THEN 'paused' ELSE 'stopped' END,'sparse-grant-'||x,'not-a-real-grant','2099-01-01T00:00:00Z','sparse-request-'||x,100 FROM n`, p.Hash, id, asset)
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("100000 SQL-only sessions seed=%s", time.Since(start))
	rows, e := db.Query(`EXPLAIN QUERY PLAN SELECT o.ordinal,ps.id FROM playback_observations o JOIN playback_sessions ps ON ps.id=o.session_id WHERE o.ordinal<=100000 AND o.ordinal<100001 AND ps.state IN ('starting','playing','paused') AND ps.expires_at>'2026-01-01' ORDER BY o.ordinal DESC LIMIT 41`)
	if e != nil {
		t.Fatal(e)
	}
	for rows.Next() {
		var a, b, c int
		var detail string
		rows.Scan(&a, &b, &c, &detail)
		t.Log(detail)
	}
	rows.Close()
	for i := 0; i < 4; i++ {
		start = time.Now()
		page, e := s.Activity(context.Background(), p, scope, "open", 40, "")
		if e != nil || len(page.Items) != 1 {
			t.Fatal(page, e)
		}
		t.Logf("sparse open full API query run%d=%s", i+1, time.Since(start))
	}
}

func TestActivityVideoHeightUsesCommittedGeometry(t *testing.T) {
	for _, tc := range []struct {
		name string
		plan DeliveryPlan
		want int
	}{
		{"effective cap", DeliveryPlan{VideoAction: "convert", TargetHeight: 720, Trace: &DecisionTrace{Video: &TraceVideo{Width: 1920, Height: 1080}}}, 720},
		{"never upscale to a ceiling", DeliveryPlan{VideoAction: "convert", TargetHeight: 1080, Trace: &DecisionTrace{Video: &TraceVideo{Width: 1280, Height: 720}}}, 720},
		{"width bound", DeliveryPlan{VideoAction: "convert", TargetWidth: 960, TargetHeight: 1080, Trace: &DecisionTrace{Video: &TraceVideo{Width: 1920, Height: 1080}}}, 540},
		{"rotation", DeliveryPlan{VideoAction: "convert", TargetWidth: 1920, TargetHeight: 1080, Trace: &DecisionTrace{Video: &TraceVideo{Width: 1080, Height: 1920, Rotation: 90}}}, 1080},
		{"anamorphic rotation", DeliveryPlan{VideoAction: "convert", Trace: &DecisionTrace{Video: &TraceVideo{Width: 720, Height: 576, Rotation: 90, SampleAspectRatio: "16:15"}}}, 768},
		{"unknown source", DeliveryPlan{VideoAction: "convert", TargetWidth: 1280, TargetHeight: 720}, 0},
		{"incomplete source", DeliveryPlan{VideoAction: "convert", TargetHeight: 720, Trace: &DecisionTrace{Video: &TraceVideo{Height: 1080}}}, 0},
		{"copy", DeliveryPlan{VideoAction: "copy", TargetHeight: 720, Trace: &DecisionTrace{Video: &TraceVideo{Width: 1920, Height: 1080}}}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ActivityVideoHeight(tc.plan); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestActivityVideoHeightFollowsActualClientCappedPlan(t *testing.T) {
	client := chromeProfile()
	client.Video = []ClientVideoCodec{{Codec: "h264", BitDepths: []int{8}, DynamicRanges: []string{"sdr"}, MaxWidth: 1280, MaxHeight: 720}}
	for _, known := range []bool{true, false} {
		detail := assets.StreamDetail{BitDepth: 8, DynamicRange: "sdr", FrameRate: 24}
		if known {
			detail.Width, detail.Height = 1920, 1080
		}
		p := decide(t, videoSource("mkv", "vc1", detail, SourceAudio{Index: 1, Codec: "aac", Channels: 2}), client, func(in *DeliveryInput) {
			in.Server = plannerToolchain()
			in.Target = QualityTarget{RungID: "1080p-low", MaxVideoHeight: 1080, AllowHDR: true}
		})
		if p.VideoAction != "convert" || p.TargetHeight > 720 {
			t.Fatalf("client ceiling was not committed: %+v", p)
		}
		want := 0
		if known {
			want = 720
		}
		if got := ActivityVideoHeight(p); got != want {
			t.Fatalf("known=%v: committed height %d, want %d", known, got, want)
		}
	}
}
