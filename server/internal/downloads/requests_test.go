package downloads

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/contentaccess"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

var downloadScale = flag.Bool("download-scale", false, "run the lane's Linux container download acceptance fixture")

func requestTestAccess(ctx context.Context, tx *sql.Tx, p identity.Principal, device, item string) (string, []any, error) {
	if device != "" && device != "device" {
		return "", nil, identity.ErrUnauthorized
	}
	return contentaccess.VisibleItemsSQL(ctx, tx, p, item, nil)
}
func requestHarness(t *testing.T, n int) *harness {
	t.Helper()
	h := newHarness(t)
	h.service.SetRequestAccess(requestTestAccess)
	show, showID := addCompactFixtureEntity(t, h.db, "lib", compactcatalog.Show, "request-show", "Show", 0, "", 0, map[string]any{"local_key": "request-show"})
	h.show = show
	season, seasonID := addCompactFixtureEntity(t, h.db, "lib", compactcatalog.Season, "request-season", "Season 1", 0, "", showID, map[string]any{"show_id": showID, "number": 1})
	h.season = season
	h.itemIDs, h.itemEntities = map[string]string{}, map[string]int64{}
	for i := 1; i <= n; i++ {
		key := fmt.Sprintf("ep-%04d", i)
		id, entity := addCompactFixtureEntity(t, h.db, "lib", compactcatalog.Episode, "request-"+key, "Episode", 0, "2020-01-01T00:00:00Z", seasonID, map[string]any{"show_id": showID, "season_id": seasonID, "numbering": "seasonal", "number": i, "local_identity_status": "parsed"})
		addCompactFixtureLink(t, h.db, entity, h.assetID)
		h.itemIDs[key], h.itemEntities[key] = id, entity
	}
	settleDownloadsProjection(t, h.db)
	return h
}
func newContainerRequest(op, show string) ContainerRequest {
	return ContainerRequest{OperationID: op, Target: ContainerTarget{"show", show}, DeviceID: "device", Quality: QualityOriginal, Policy: EpisodePolicy{Episodes: "all"}}
}
func drainRequestAdmission(t *testing.T, h *harness, ctx context.Context, id string) {
	t.Helper()
	for i := 0; i < 2000; i++ {
		if e := h.service.advanceRequests(ctx); e != nil {
			t.Fatal(e)
		}
		var state string
		if e := h.db.QueryRow(`SELECT state FROM download_requests WHERE id=?`, id).Scan(&state); e != nil {
			t.Fatal(e)
		}
		if state == "complete" {
			return
		}
		if state == "failed" {
			var reason string
			h.db.QueryRow(`SELECT error_code FROM download_requests WHERE id=?`, id).Scan(&reason)
			t.Fatal("request failed", reason)
		}
	}
	t.Fatal("request did not finish")
}
func TestContainerRequestRestartIdempotencyAndPolicies(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	h := requestHarness(t, 43)
	ctx := context.WithValue(context.Background(), requestBatchObserverKey{}, func(phase string, n int) {
		if n > 20 {
			t.Fatal("unbounded batch", phase, n)
		}
	})
	req := newContainerRequest("whole-show", h.show)
	first, e := h.service.SubmitContainer(ctx, h.viewer, req)
	if e != nil || first.TotalKnown {
		t.Fatal(first, e)
	}
	if e = h.service.advanceRequests(ctx); e != nil {
		t.Fatal(e)
	}
	h.db.Close()
	h.db, e = persistence.Open(filepath.Join(h.root, "state", "portico.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer h.db.Close()
	h.service, e = New(Options{DB: h.db, Now: func() time.Time { return h.now }})
	if e != nil {
		t.Fatal(e)
	}
	h.service.SetRequestAccess(requestTestAccess)
	drainRequestAdmission(t, h, ctx, first.RequestID)
	replay, e := h.service.SubmitContainer(ctx, h.viewer, req)
	if e != nil || replay.RequestID != first.RequestID || replay.Total != 43 || !replay.TotalKnown {
		t.Fatal(replay, e)
	}
	req.Quality = "720p-high"
	if _, e = h.service.SubmitContainer(ctx, h.viewer, req); !errors.Is(e, ErrRequestConflict) {
		t.Fatal("altered replay", e)
	}
	var receipts int
	if e = h.db.QueryRow(`SELECT count(*) FROM console_receipts`).Scan(&receipts); e != nil || receipts != 0 {
		t.Fatal("per-item receipts", receipts, e)
	}
	other := h.viewer
	other.ProfileID = "other"
	if _, e = h.service.ContainerRequest(ctx, other, first.RequestID, "", 100); e != ErrNotFound {
		t.Fatal("other profile", e)
	}
	seen := 0
	cursor := ""
	for {
		page, e := h.service.ContainerRequest(ctx, h.viewer, first.RequestID, cursor, 7)
		if e != nil {
			t.Fatal(e)
		}
		for _, item := range page.Items {
			seen++
			if item.ItemID != h.itemIDs[fmt.Sprintf("ep-%04d", seen)] {
				t.Fatal("order", item)
			}
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if seen != 43 {
		t.Fatal(seen)
	}
	if e = dbwork.WithWriteTx(ctx, h.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO personal_watched_intents(profile_id,item_id,watched,authored_at) VALUES(?,?,1,'2026-09-24T12:00:00Z')`, identity.PersonalKey(h.viewer.Viewer), h.itemEntities["ep-0001"])
		return err
	}); e != nil {
		t.Fatal(e)
	}
	next := newContainerRequest("next", h.show)
	next.Policy = EpisodePolicy{"next", 2}
	got, e := h.service.SubmitContainer(ctx, h.viewer, next)
	if e != nil {
		t.Fatal(e)
	}
	drainRequestAdmission(t, h, ctx, got.RequestID)
	got, e = h.service.ContainerRequest(ctx, h.viewer, got.RequestID, "", 100)
	if e != nil || got.Total != 2 || got.Items[0].ItemID != h.itemIDs["ep-0002"] || got.Items[1].ItemID != h.itemIDs["ep-0003"] {
		t.Fatal(got, e)
	}
	unwatched := newContainerRequest("unwatched", h.show)
	unwatched.Policy.Episodes = "unwatched"
	got, e = h.service.SubmitContainer(ctx, h.viewer, unwatched)
	if e != nil {
		t.Fatal(e)
	}
	drainRequestAdmission(t, h, ctx, got.RequestID)
	got, e = h.service.ContainerRequest(ctx, h.viewer, got.RequestID, "", 100)
	if e != nil || got.Total != 42 {
		t.Fatal(got, e)
	}
}
func TestContainerRequestSelectionAndAuthorityChanges(t *testing.T) {
	h := requestHarness(t, 24)
	ctx := context.Background()
	r, e := h.service.SubmitContainer(ctx, h.viewer, newContainerRequest("changed", h.show))
	if e != nil {
		t.Fatal(e)
	}
	if e = h.service.advanceRequests(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = h.db.Exec(`UPDATE library_revisions SET revision=revision+1`); e != nil {
		t.Fatal(e)
	}
	if e = h.service.advanceRequests(ctx); e != nil {
		t.Fatal(e)
	}
	// The library moved while the selection was being read (a scan added a
	// file somewhere). Nothing had been admitted, so the reading starts again;
	// the request does not fail.
	var n, captured, restarts int
	h.db.QueryRow(`SELECT count(*) FROM download_preparations`).Scan(&n)
	if n != 0 {
		t.Fatal("effects before capture", n)
	}
	if e = h.db.QueryRow(`SELECT total,restarts FROM download_requests WHERE id=?`, r.RequestID).Scan(&captured, &restarts); e != nil || captured != 0 || restarts != 1 {
		t.Fatal("a changed selection was not read again", captured, restarts, e)
	}
	drainRequestAdmission(t, h, ctx, r.RequestID)
	out, e := h.service.ContainerRequest(ctx, h.viewer, r.RequestID, "", 100)
	if e != nil || out.ErrorCode != "" || out.Total != 24 {
		t.Fatal(out, e)
	}
	// A library that never holds still is read through anyway.
	busy, e := h.service.SubmitContainer(ctx, h.viewer, ContainerRequest{OperationID: "busy", Target: ContainerTarget{Kind: "show", ID: h.show}, DeviceID: "device", Quality: "720p-high", Policy: EpisodePolicy{Episodes: "all"}})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 40; i++ {
		if _, e = h.db.Exec(`UPDATE library_revisions SET revision=revision+1`); e != nil {
			t.Fatal(e)
		}
		if e = h.service.advanceRequests(ctx); e != nil {
			t.Fatal(e)
		}
	}
	if out, e = h.service.ContainerRequest(ctx, h.viewer, busy.RequestID, "", 100); e != nil || out.ErrorCode != "" || out.Total != 24 {
		t.Fatal("a request against a busy library never finished", out, e)
	}
	// The viewer's own state is no part of "all of it".
	if _, e = h.db.Exec(`DELETE FROM download_requests`); e != nil {
		t.Fatal(e)
	}
	all := newContainerRequest("all-of-it", h.show)
	if _, e = h.service.SubmitContainer(ctx, h.viewer, all); e != nil {
		t.Fatal(e)
	}
	if _, e = h.db.Exec(`INSERT INTO viewer_revisions(profile_id,library_id,revision) SELECT ?,library_id,99 FROM library_revisions WHERE true ON CONFLICT(profile_id,library_id) DO UPDATE SET revision=viewer_revisions.revision+1`, identity.PersonalKey(h.viewer.Viewer)); e != nil {
		t.Fatal(e)
	}
	if e = h.service.advanceRequests(ctx); e != nil {
		t.Fatal(e)
	}
	if e = h.db.QueryRow(`SELECT restarts FROM download_requests WHERE operation_id='all-of-it'`).Scan(&restarts); e != nil || restarts != 0 {
		t.Fatal("watching something restarted a request for everything", restarts, e)
	}
	r, e = h.service.SubmitContainer(ctx, h.viewer, newContainerRequest("revoked", h.show))
	if e != nil {
		t.Fatal(e)
	}
	h.service.SetRequestAccess(func(context.Context, *sql.Tx, identity.Principal, string, string) (string, []any, error) {
		return "", nil, identity.ErrUnauthorized
	})
	if e = h.service.advanceRequests(ctx); e != nil {
		t.Fatal(e)
	}
	var code string
	if e = h.db.QueryRow(`SELECT error_code FROM download_requests WHERE id=?`, r.RequestID).Scan(&code); e != nil || code != "authority_revoked" {
		t.Fatal(code, e)
	}
}
func TestContainerRequestCapacityWaitsForRunningSlots(t *testing.T) {
	h := requestHarness(t, 3)
	ctx := context.Background()
	// Existing active work fills all slots; ready claims never consume one.
	for i := 0; i < MaxLivePerViewer; i++ {
		if _, e := h.db.Exec(`INSERT INTO download_preparations(id,profile_key,authority,account_id,profile_id,item_id,library_id,quality,origin,state,created_ms,updated_ms) VALUES(?,?,'local','account','profile',?,'lib',?,'item','running',1,1)`, fmt.Sprint("busy-", i), operations.ViewerKey(h.viewer), h.itemID, fmt.Sprintf("busy-%d", i)); e != nil {
			t.Fatal(e)
		}
	}
	r, e := h.service.SubmitContainer(ctx, h.viewer, newContainerRequest("capacity", h.show))
	if e != nil {
		t.Fatal(e)
	}
	drainRequestAdmission(t, h, ctx, r.RequestID)
	view, e := h.service.ContainerRequest(ctx, h.viewer, r.RequestID, "", 100)
	if e != nil || view.Queued != 3 {
		t.Fatal(view, e)
	}
	id := view.Items[0].Preparation.ID
	plan, e := h.service.prepare(ctx, id)
	if e != nil || plan.hashBytes {
		t.Fatal("slot exceeded", plan, e)
	}
	if _, e = h.db.Exec(`UPDATE download_preparations SET state='ready' WHERE id='busy-0'`); e != nil {
		t.Fatal(e)
	}
	plan, e = h.service.prepare(ctx, id)
	if e != nil || !plan.hashBytes {
		t.Fatal("free slot not used", plan, e)
	}
	var running int
	h.db.QueryRow(`SELECT count(*) FROM download_preparations WHERE state='running'`).Scan(&running)
	if running != MaxLivePerViewer {
		t.Fatal(running)
	}
}
func TestContainerDownloads500Runner(t *testing.T) {
	if !*downloadScale {
		t.Skip("opt-in Linux acceptance fixture")
	}
	if runtime.GOOS != "linux" {
		t.Fatal("runner only")
	}
	started := time.Now()
	h := requestHarness(t, 500)
	t.Log("fixture", time.Since(started))
	ctx := context.WithValue(context.Background(), requestBatchObserverKey{}, func(phase string, n int) {
		if n > 20 {
			t.Fatal(phase, n)
		}
	})
	started = time.Now()
	request, e := h.service.SubmitContainer(ctx, h.viewer, newContainerRequest("500", h.show))
	if e != nil {
		t.Fatal(e)
	}
	t.Log("admission", time.Since(started))
	drainRequestAdmission(t, h, ctx, request.RequestID)
	capture := time.Since(started)
	// Fill the real durable active slots without hashing yet, then restart.
	cursor := ""
	for {
		view, e := h.service.ContainerRequest(ctx, h.viewer, request.RequestID, cursor, 100)
		if e != nil {
			t.Fatal(e)
		}
		for _, item := range view.Items {
			if _, e = h.service.prepare(ctx, item.Preparation.ID); e != nil {
				t.Fatal(e)
			}
		}
		cursor = view.NextCursor
		if cursor == "" {
			break
		}
	}
	var running int
	h.db.QueryRow(`SELECT count(*) FROM download_preparations WHERE state='running'`).Scan(&running)
	if running != 200 {
		t.Fatal("did not fill concurrency limit", running)
	}
	h.db.Close()
	h.db, e = persistence.Open(filepath.Join(h.root, "state", "portico.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer h.db.Close()
	h.service, e = New(Options{DB: h.db, Now: func() time.Time { return h.now }})
	if e != nil {
		t.Fatal(e)
	}
	h.service.SetRequestAccess(requestTestAccess)
	for pass := 0; pass < 1000; pass++ {
		if e = h.service.Advance(ctx); e != nil {
			t.Fatal(e)
		}
		h.db.QueryRow(`SELECT count(*) FROM download_preparations WHERE state='running'`).Scan(&running)
		if running > 200 {
			t.Fatal("concurrency exceeded", running)
		}
		view, e := h.service.ContainerRequest(ctx, h.viewer, request.RequestID, "", 1)
		if e != nil {
			t.Fatal(e)
		}
		if view.Ready == 500 {
			t.Logf("total=%d ready=%d queued=%d preparing=%d failed=%d capture_and_admit=%s complete=%s peak_running=200", view.Total, view.Ready, view.Queued, view.Preparing, view.Failed, capture, time.Since(started))
			return
		}
		if view.Failed > 0 {
			t.Fatal(view)
		}
	}
	t.Fatal("queue did not drain")
}

func TestContainerRequestKindsAndStorageRefusal(t *testing.T) {
	h := requestHarness(t, 3)
	ctx := context.Background()
	_, artistID := addCompactFixtureEntity(t, h.db, "lib", compactcatalog.Artist, "request-artist", "Artist", 0, "", 0, map[string]any{"local_key": "request-artist"})
	album, albumID := addCompactFixtureEntity(t, h.db, "lib", compactcatalog.Album, "request-album", "Album", 2020, "", artistID, map[string]any{"artist_id": artistID, "local_key": "request-album"})
	bookLibraryID := int64(0)
	if e := h.db.QueryRow(`SELECT id FROM catalog_libraries WHERE library_id='lib'`).Scan(&bookLibraryID); e != nil {
		t.Fatal(e)
	}
	book, bookID := addCompactFixtureEntity(t, h.db, "lib", compactcatalog.Book, "request-book", "Book", 0, "", 0, map[string]any{"library_id": bookLibraryID, "local_key": "request-book", "author": "Author", "narrator": "Narrator"})
	if _, e := h.db.Exec(`INSERT INTO catalog_playlists(token,owner_authority,owner_account,owner_profile,name,creation_operation,creation_hash,created_at) VALUES('playlist','local','account','profile','Playlist','fixture','hash','2020-01-01T00:00:00Z')`); e != nil {
		t.Fatal(e)
	}
	songPublic, partPublic := make([]string, 2), make([]string, 2)
	songIDs, partIDs := make([]int64, 2), make([]int64, 2)
	for i := 1; i <= 2; i++ {
		key := fmt.Sprint("request-song-", i)
		songPublic[i-1], songIDs[i-1] = addCompactFixtureEntity(t, h.db, "lib", compactcatalog.Track, key, "Song", 0, "", albumID, map[string]any{"album_id": albumID, "track_number": 3 - i})
		key = fmt.Sprint("request-part-", i)
		partPublic[i-1], partIDs[i-1] = addCompactFixtureEntity(t, h.db, "lib", compactcatalog.Part, key, "Part", 0, "", bookID, map[string]any{"book_id": bookID, "part_number": 3 - i})
		addCompactFixtureLink(t, h.db, songIDs[i-1], h.assetID)
		addCompactFixtureLink(t, h.db, partIDs[i-1], h.assetID)
	}
	if _, e := h.db.Exec(`INSERT INTO catalog_playlist_entries(token,playlist_id,item_id,position) VALUES('one',(SELECT id FROM catalog_playlists WHERE token='playlist'),?,0),('two',(SELECT id FROM catalog_playlists WHERE token='playlist'),?,1),('three',(SELECT id FROM catalog_playlists WHERE token='playlist'),?,2)`, h.itemEntities["ep-0003"], h.itemEntities["ep-0001"], h.itemEntities["ep-0003"]); e != nil {
		t.Fatal(e)
	}
	if _, e := h.db.Exec(`UPDATE catalog_playlist_entries SET order_key=CASE token WHEN 'one' THEN 'V' WHEN 'two' THEN 'V0V' ELSE 'z' END WHERE playlist_id=(SELECT id FROM catalog_playlists WHERE token='playlist')`); e != nil {
		t.Fatal(e)
	}
	settleDownloadsProjection(t, h.db)
	for _, c := range []struct {
		kind, id, first string
		n               int
	}{{"season", h.season, h.itemIDs["ep-0001"], 3}, {"album", album, songPublic[1], 2}, {"book", book, partPublic[1], 2}, {"playlist", "playlist", h.itemIDs["ep-0003"], 2}} {
		query, bound := requestSelection(ContainerTarget{c.kind, c.id})
		plans, e := h.db.Query(`EXPLAIN QUERY PLAN SELECT id,position FROM (`+query+`) WHERE (position,id)>(?,?) ORDER BY position,id LIMIT 20`, append(bound, "", "")...)
		if e != nil {
			t.Fatal(e)
		}
		for plans.Next() {
			var a, b, cost int
			var detail string
			if e = plans.Scan(&a, &b, &cost, &detail); e != nil {
				t.Fatal(e)
			}
			if strings.Contains(detail, "SCAN i") {
				t.Fatal("library-wide item scan", detail)
			}
			t.Log(c.kind, detail)
		}
		if e = plans.Err(); e != nil {
			t.Fatal(e)
		}
		plans.Close()
		req := newContainerRequest(c.kind, h.show)
		req.Target = ContainerTarget{c.kind, c.id}
		r, e := h.service.SubmitContainer(ctx, h.viewer, req)
		if e != nil {
			t.Fatal(c, e)
		}
		drainRequestAdmission(t, h, ctx, r.RequestID)
		out, e := h.service.ContainerRequest(ctx, h.viewer, r.RequestID, "", 100)
		if e != nil || out.Total != c.n || out.Items[0].ItemID != c.first {
			t.Fatal(c, out, e)
		}
	}
	if _, e := h.db.Exec(`UPDATE download_settings SET max_prepared_bytes=1`); e != nil {
		t.Fatal(e)
	}
	req := newContainerRequest("no-space", h.show)
	req.Quality = "720p-high"
	r, e := h.service.SubmitContainer(ctx, h.viewer, req)
	if e != nil {
		t.Fatal(e)
	}
	drainRequestAdmission(t, h, ctx, r.RequestID)
	out, e := h.service.ContainerRequest(ctx, h.viewer, r.RequestID, "", 100)
	if e != nil || out.Failed != 3 || out.Items[0].Reason != ReasonStorageFull {
		t.Fatal(out, e)
	}
	stranger := h.viewer
	stranger.ProfileID = "other"
	req.OperationID = "not-shared"
	req.Target = ContainerTarget{"playlist", "playlist"}
	if _, e = h.service.SubmitContainer(ctx, stranger, req); e != ErrNotFound {
		t.Fatal("unshared playlist", e)
	}
}

type requestOptimizer struct {
	calls      int
	operations []string
}

func (o *requestOptimizer) RequestOptimization(_ context.Context, _ identity.Principal, _, _, _, operation string) error {
	o.operations = append(o.operations, operation)
	o.calls++
	return nil
}
func TestDownloadOptimizationIsDeferredUntilWorkerAdmission(t *testing.T) {
	h := requestHarness(t, 1)
	optimizer := &requestOptimizer{}
	h.service.optimizer = optimizer
	ctx := context.Background()
	out, e := h.service.Submit(ctx, h.viewer, Request{OperationID: "optimizer", MediaID: h.itemIDs["ep-0001"], Quality: "720p-high"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if optimizer.calls != 0 {
		t.Fatal("request started conversion")
	}
	if _, e = h.service.step(ctx, out.Items[0].ID); e != nil || optimizer.calls != 1 {
		t.Fatal(optimizer.calls, e)
	}
	// The optimizer receipt is retried only until its acknowledgement is durable.
	if _, e = h.service.step(ctx, out.Items[0].ID); e != nil || optimizer.calls != 1 {
		t.Fatal("duplicate optimization", optimizer.calls, e)
	}
	failed, e := h.service.Get(ctx, h.viewer, out.Items[0].ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = h.service.Act(ctx, h.viewer, failed.ID, Command{OperationID: "retry-conversion", Action: ActionRetry, ExpectedRevision: failed.Revision}); e != nil {
		t.Fatal(e)
	}
	if _, e = h.service.step(ctx, failed.ID); e != nil || optimizer.calls != 2 {
		t.Fatal("retry did not queue a new producer attempt", optimizer.calls, e)
	}
	if optimizer.operations[0] == optimizer.operations[1] {
		t.Fatal("retry reused failed producer operation")
	}
}

func TestContainerRequestCursorDoesNotExposeHiddenOrdinals(t *testing.T) {
	h := requestHarness(t, 4)
	ctx := context.Background()
	request, err := h.service.SubmitContainer(ctx, h.viewer, newContainerRequest("cursor", h.show))
	if err != nil {
		t.Fatal(err)
	}
	drainRequestAdmission(t, h, ctx, request.RequestID)
	h.service.SetRequestAccess(func(ctx context.Context, tx *sql.Tx, p identity.Principal, device, item string) (string, []any, error) {
		visible, args, err := requestTestAccess(ctx, tx, p, device, item)
		return "(" + visible + ") AND " + item + "<>?", append(args, h.itemEntities["ep-0002"]), err
	})
	page, err := h.service.ContainerRequest(ctx, h.viewer, request.RequestID, "", 2)
	if err != nil || page.Total != 3 || len(page.Items) != 2 || page.Items[1].ItemID != h.itemIDs["ep-0003"] || page.NextCursor != page.Items[1].ItemID {
		t.Fatalf("visible page: %+v %v", page, err)
	}
	rest, err := h.service.ContainerRequest(ctx, h.viewer, request.RequestID, page.NextCursor, 2)
	if err != nil || len(rest.Items) != 1 || rest.Items[0].ItemID != h.itemIDs["ep-0004"] || rest.NextCursor != "" {
		t.Fatalf("continuation: %+v %v", rest, err)
	}
	if _, err = h.service.ContainerRequest(ctx, h.viewer, request.RequestID, "not-a-member", 2); !errors.Is(err, ErrInput) {
		t.Fatal("foreign cursor", err)
	}
}

func TestContainerDownloadOptionsAggregateAllBatchesAndRefuseForeignDevice(t *testing.T) {
	h := requestHarness(t, 43)
	ctx := context.WithValue(context.Background(), requestBatchObserverKey{}, func(phase string, n int) {
		if n > requestBatchSize {
			t.Fatalf("unbounded %s batch %d", phase, n)
		}
	})
	in := ContainerOptionsRequest{Target: ContainerTarget{Kind: "show", ID: h.show}, DeviceID: "device", Policy: EpisodePolicy{Episodes: "all"}}
	one, err := h.service.Options(ctx, h.viewer, h.itemIDs["ep-0001"], true)
	if err != nil {
		t.Fatal(err)
	}
	out, err := h.service.ContainerOptions(ctx, h.viewer, in, true)
	if err != nil {
		t.Fatal(err)
	}
	if out.Total != 43 || len(out.Options) != len(one.Options) {
		t.Fatalf("aggregate %+v", out)
	}
	for _, q := range out.Options {
		var found *QualityOption
		for i := range one.Options {
			if one.Options[i].Quality == q.Quality {
				found = &one.Options[i]
			}
		}
		if found == nil || q.EstimatedBytes != 43*found.Bytes || q.Estimated != found.Estimated || q.Available != found.Available {
			t.Fatalf("quality %+v item %+v", q, found)
		}
	}
	in.DeviceID = "foreign-device"
	if _, err = h.service.ContainerOptions(ctx, h.viewer, in, true); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("foreign device %v", err)
	}
	in.DeviceID = "device"
	in.Target.ID = "missing"
	if _, err = h.service.ContainerOptions(ctx, h.viewer, in, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing %v", err)
	}
}

// A device finds every request still at work, however old and however many,
// a page at a time; a finished one stays only for a day. A poll can read the
// counts alone, and pruning keeps a finished request whose members are still
// being prepared.
func TestRequestDiscoveryPagesActiveRequestsAndStatusOnlyReads(t *testing.T) {
	h := requestHarness(t, 2)
	ctx := context.Background()
	ids := []string{}
	for i := 0; i < 5; i++ {
		h.now = h.now.Add(time.Second)
		r, e := h.service.SubmitContainer(ctx, h.viewer, newContainerRequest(fmt.Sprintf("discover-%d", i), h.show))
		if e != nil {
			t.Fatal(e)
		}
		drainRequestAdmission(t, h, ctx, r.RequestID)
		ids = append(ids, r.RequestID)
	}
	list := func() []RequestSummary {
		t.Helper()
		out, cursor := []RequestSummary{}, ""
		for pages := 0; ; pages++ {
			page, e := h.service.Requests(ctx, h.viewer, "device", cursor, 2)
			if e != nil || len(page.Requests) > 2 || pages > 5 {
				t.Fatal(page, e)
			}
			out = append(out, page.Requests...)
			if cursor = page.NextCursor; cursor == "" {
				return out
			}
		}
	}
	got := list()
	if len(got) != 5 || got[0].RequestID != ids[4] || got[4].RequestID != ids[0] {
		t.Fatal("newest first across pages", got)
	}
	// Two days on, only the request with a member still queued is at work. The
	// requests share their preparations here, so the others let go of theirs.
	keep := ids[1]
	if _, e := h.db.Exec(`UPDATE download_request_items SET preparation_id='' WHERE request_id<>?`, keep); e != nil {
		t.Fatal(e)
	}
	h.now = h.now.Add(48 * time.Hour)
	got = list()
	if len(got) != 1 || got[0].RequestID != keep || got[0].State != "preparing" {
		t.Fatal("old active request", got)
	}
	for _, bad := range []struct {
		cursor string
		limit  int
	}{{"x", 2}, {"1.", 2}, {"", 0}, {"", MaxRequestPage + 1}} {
		if _, e := h.service.Requests(ctx, h.viewer, "device", bad.cursor, bad.limit); !errors.Is(e, ErrInput) {
			t.Fatal("admitted", bad, e)
		}
	}
	status, e := h.service.ContainerRequest(ctx, h.viewer, keep, "", 0)
	if e != nil || len(status.Items) != 0 || status.NextCursor != "" || status.Total != 2 || status.Queued != 2 || status.State != "preparing" {
		t.Fatal("status only", status, e)
	}
	if _, e = h.service.ContainerRequest(ctx, h.viewer, keep, h.itemIDs["ep-0001"], 0); !errors.Is(e, ErrInput) {
		t.Fatal("status read with a cursor", e)
	}
	full, e := h.service.ContainerRequest(ctx, h.viewer, keep, "", 100)
	if e != nil || len(full.Items) != 2 || full.Items[0].Preparation == nil || full.Items[1].Preparation == nil {
		t.Fatal("batched preparations", full, e)
	}
	// A month on, pruning removes the finished requests but not the one at work.
	h.now = h.now.Add(31 * 24 * time.Hour)
	if e = h.service.prune(ctx); e != nil {
		t.Fatal(e)
	}
	var left []string
	rows, e := h.db.Query(`SELECT id FROM download_requests`)
	if e != nil {
		t.Fatal(e)
	}
	for rows.Next() {
		var id string
		rows.Scan(&id)
		left = append(left, id)
	}
	rows.Close()
	if len(left) != 1 || left[0] != keep {
		t.Fatal("prune", left)
	}
}
