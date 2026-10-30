package downloads

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/selectionactions"
)

const selectionDownloadTestDDL = `CREATE TABLE IF NOT EXISTS download_action_claims(profile_key TEXT NOT NULL,device_id TEXT NOT NULL,operation_id TEXT NOT NULL,preparation_id TEXT NOT NULL REFERENCES download_preparations(id) ON DELETE CASCADE,PRIMARY KEY(profile_key,device_id,operation_id,preparation_id)) WITHOUT ROWID, STRICT; CREATE INDEX IF NOT EXISTS download_action_claims_preparation ON download_action_claims(preparation_id)`

func selectionDownloadHarness(t *testing.T, n int) *harness {
	t.Helper()
	h := requestHarness(t, n)
	if _, e := h.db.Exec(selectionDownloadTestDDL); e != nil {
		t.Fatal(e)
	}
	// These domain fixtures model already-confirmed download commands. Real HTTP
	// creates each transfer header atomically through the journal accepting hook.
	for _, operation := range []string{"operation", "another-operation", "reuse-converted"} {
		if _, e := h.db.Exec(`INSERT INTO download_action_transfers(profile_key,device_id,operation_id,created_ms) VALUES(?,?,?,?)`, operations.ViewerKey(h.viewer), "device", operation, h.now.UnixMilli()); e != nil {
			t.Fatal(e)
		}
	}
	return h
}
func selectionDownloadTarget(t *testing.T, h *harness, item string, entity int64) selectionactions.Target {
	t.Helper()
	target := selectionactions.Target{EntityID: entity, ItemID: item, LibraryID: "lib", Kind: "movie", Title: "Film", OperationID: "operation"}
	var kind int
	var parent, added sql.NullInt64
	if e := h.db.QueryRow(`SELECT kind,parent_id,added_ms FROM catalog_entities WHERE id=?`, entity).Scan(&kind, &parent, &added); e != nil {
		t.Fatal(e)
	}
	target.Kind, _ = compactcatalog.Kind(kind).Name()
	var pv any
	if parent.Valid {
		pv = parent.Int64
	}
	target.Identity = selectionactions.TargetIdentity(target.ItemID, target.LibraryID, target.Kind, pv)
	if added.Valid {
		target.Lifetime = strconv.FormatInt(added.Int64, 10)
	}
	capture := &SelectionDownloadCapture{}
	for i := 0; i < 100; i++ {
		gated, e := dbwork.BeginSnapshot(context.Background(), h.db)
		if e != nil {
			t.Fatal(e)
		}
		pinned, done, e := CaptureSelectionDownload(context.Background(), gated.Tx(), target, capture)
		gated.Rollback()
		if e != nil {
			t.Fatal(e)
		}
		if done {
			return pinned
		}
	}
	t.Fatal("capture did not finish")
	return target
}
func selectionDownloadAuthorize(_ context.Context, _ *sql.Tx, _ identity.Principal, action, library string, target *selectionactions.Target) error {
	if action != "download" || library != "lib" || target == nil || target.LibraryID != library {
		return ErrInput
	}
	return nil
}
func selectionDownloadOptions() selectionactions.Options {
	return selectionactions.Options{DeviceID: "device", Quality: QualityOriginal}
}
func selectionDownloadCounts(t *testing.T, h *harness, wantPreps, wantClaims int) {
	t.Helper()
	var preps, claims int
	if e := h.db.QueryRow(`SELECT count(*) FROM download_preparations`).Scan(&preps); e != nil {
		t.Fatal(e)
	}
	if e := h.db.QueryRow(`SELECT count(*) FROM download_action_claims`).Scan(&claims); e != nil {
		t.Fatal(e)
	}
	if preps != wantPreps || claims != wantClaims {
		t.Fatalf("preparations/claims=%d/%d want %d/%d", preps, claims, wantPreps, wantClaims)
	}
}
func TestSelectionDownloadAtomicCheckpointAndReuse(t *testing.T) {
	h := selectionDownloadHarness(t, 0)
	ctx := context.Background()
	target := selectionDownloadTarget(t, h, h.item, h.itemID)
	stop := errors.New("checkpoint failed")
	apply := func(cp func(*sql.Tx, selectionactions.Effect) error) error {
		return h.service.ApplySelectionDownload(ctx, h.viewer, "download", selectionDownloadOptions(), target, nil, selectionDownloadAuthorize, cp)
	}
	if e := apply(func(tx *sql.Tx, effect selectionactions.Effect) error {
		if effect.Code != "" {
			t.Fatal(effect)
		}
		var n int
		if e := tx.QueryRow(`SELECT count(*) FROM download_action_claims`).Scan(&n); e != nil || n != 1 {
			t.Fatal(n, e)
		}
		return stop
	}); !errors.Is(e, stop) {
		t.Fatal(e)
	}
	selectionDownloadCounts(t, h, 0, 0)
	checkpoint := func(*sql.Tx, selectionactions.Effect) error { return nil }
	if e := apply(checkpoint); e != nil {
		t.Fatal(e)
	}
	if e := apply(checkpoint); e != nil {
		t.Fatal(e)
	}
	selectionDownloadCounts(t, h, 1, 1)
	target.OperationID = "another-operation"
	if e := apply(checkpoint); e != nil {
		t.Fatal(e)
	}
	selectionDownloadCounts(t, h, 1, 2)
	page, e := h.service.ListSelectionAction(ctx, h.viewer, target.OperationID, "device", "", 2)
	if e != nil || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
	if _, e = h.db.Exec(`DELETE FROM download_preparations WHERE id=?`, page.Items[0].ID); e != nil {
		t.Fatal(e)
	}
	selectionDownloadCounts(t, h, 0, 0)
}
func TestSelectionDownloadRawCaptureMatchesBestAndYields(t *testing.T) {
	h := selectionDownloadHarness(t, 0)
	ctx := context.Background()
	e := dbwork.WithWriteTx(ctx, h.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		for i := 0; i < 43; i++ {
			id, _, e := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: fmt.Sprintf("%s/version-%d.mp4", h.root, i), Size: int64(i + 100), ModifiedNS: 1, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Height: i % 4 * 720, Width: 1920, Duration: 60})
			if e != nil {
				return e
			}
			if e = compactcatalog.LinkAssetTx(ctx, tx, h.itemID, id, compactcatalog.Link{}); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	c := &SelectionDownloadCapture{}
	target := selectionDownloadTarget(t, h, h.item, h.itemID)
	steps := 0
	for {
		g, e := dbwork.BeginSnapshot(ctx, h.db)
		if e != nil {
			t.Fatal(e)
		}
		pinned, done, e := CaptureSelectionDownload(ctx, g.Tx(), target, c)
		g.Rollback()
		if e != nil {
			t.Fatal(e)
		}
		steps++
		if c.Bytes() > selectionDownloadScalar+1024 {
			t.Fatal("capture grew", c.Bytes())
		}
		if done {
			target = pinned
			break
		}
	}
	if steps != 3 {
		t.Fatalf("raw steps=%d want3", steps)
	}
	var expected string
	if e = h.db.QueryRow(`SELECT a.token FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=? ORDER BY `+compactcatalog.BestVersionOrder("l", "a")+` LIMIT 1`, h.itemID).Scan(&expected); e != nil {
		t.Fatal(e)
	}
	if target.VersionID != expected || !validDigest.MatchString(target.DownloadIdentity) {
		t.Fatal(target, expected)
	}
	// Pinning remains exact when a better version is added after confirmation.
	if _, e = h.db.Exec(`UPDATE catalog_assets SET height=99999 WHERE token=?`, h.asset); e != nil {
		t.Fatal(e)
	}
	var effect selectionactions.Effect
	if e = h.service.ApplySelectionDownload(ctx, h.viewer, "download", selectionDownloadOptions(), target, nil, selectionDownloadAuthorize, func(_ *sql.Tx, x selectionactions.Effect) error { effect = x; return nil }); e != nil {
		t.Fatal(e)
	}
	if effect.Code != "" {
		t.Fatal(effect)
	}
	var actual string
	if e = h.db.QueryRow(`SELECT source_asset_id FROM download_preparations`).Scan(&actual); e != nil || actual != expected {
		t.Fatal(actual, expected, e)
	}
}
func TestSelectionDownloadPinnedChangeAndLiveAuthority(t *testing.T) {
	h := selectionDownloadHarness(t, 0)
	ctx := context.Background()
	target := selectionDownloadTarget(t, h, h.item, h.itemID)
	if _, e := h.db.Exec(`UPDATE catalog_assets SET modified_ns=modified_ns+1 WHERE token=?`, h.asset); e != nil {
		t.Fatal(e)
	}
	var effect selectionactions.Effect
	cp := func(_ *sql.Tx, e selectionactions.Effect) error { effect = e; return nil }
	if e := h.service.ApplySelectionDownload(ctx, h.viewer, "download", selectionDownloadOptions(), target, nil, selectionDownloadAuthorize, cp); e != nil {
		t.Fatal(e)
	}
	if effect.Code != ReasonSourceChanged {
		t.Fatal(effect)
	}
	selectionDownloadCounts(t, h, 0, 0)
	denied := func(context.Context, *sql.Tx, identity.Principal, string, string, *selectionactions.Target) error {
		return identity.ErrForbidden
	}
	if e := h.service.ApplySelectionDownload(ctx, h.viewer, "download", selectionDownloadOptions(), target, nil, denied, cp); !errors.Is(e, identity.ErrForbidden) {
		t.Fatal(e)
	}
	h.service.SetRequestAccess(func(context.Context, *sql.Tx, identity.Principal, string, string) (string, []any, error) {
		return "", nil, identity.ErrUnauthorized
	})
	if e := h.service.ApplySelectionDownload(ctx, h.viewer, "download", selectionDownloadOptions(), target, nil, selectionDownloadAuthorize, cp); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal(e)
	}
	selectionDownloadCounts(t, h, 0, 0)
}
func TestSelectionDownloadInventoryRawPagesAndIsolation(t *testing.T) {
	h := selectionDownloadHarness(t, 7)
	ctx := context.Background()
	for i := 1; i <= 7; i++ {
		key := fmt.Sprintf("ep-%04d", i)
		target := selectionDownloadTarget(t, h, h.itemIDs[key], h.itemEntities[key])
		if e := h.service.ApplySelectionDownload(ctx, h.viewer, "download", selectionDownloadOptions(), target, nil, selectionDownloadAuthorize, func(*sql.Tx, selectionactions.Effect) error { return nil }); e != nil {
			t.Fatal(e)
		}
	}
	selectionDownloadCounts(t, h, 7, 7)
	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		page, e := h.service.ListSelectionAction(ctx, h.viewer, "operation", "device", cursor, 2)
		if e != nil {
			t.Fatal(e)
		}
		pages++
		if len(page.Items) > 2 {
			t.Fatal(page)
		}
		for _, p := range page.Items {
			if seen[p.ID] {
				t.Fatal("duplicate", p.ID)
			}
			seen[p.ID] = true
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(seen) != 7 || pages != 4 {
		t.Fatal(len(seen), pages)
	}
	other := h.viewer
	other.ProfileID = "foreign"
	for _, c := range []struct {
		p  identity.Principal
		op string
	}{{h.viewer, "foreign-op"}, {other, "operation"}} {
		page, e := h.service.ListSelectionAction(ctx, c.p, c.op, "device", "", 2)
		if !errors.Is(e, ErrNotFound) || len(page.Items) != 0 {
			t.Fatal("unknown scoped intent must not claim a successful empty inventory", page, e)
		}
	}
	if _, e := h.service.ListSelectionAction(ctx, h.viewer, "operation", "foreign-device", "", 2); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal(e)
	}
	h.service.SetRequestAccess(func(ctx context.Context, tx *sql.Tx, p identity.Principal, device, item string) (string, []any, error) {
		if device == "second-device" {
			device = "device"
		}
		return requestTestAccess(ctx, tx, p, device, item)
	})
	pageOther, e := h.service.ListSelectionAction(ctx, h.viewer, "operation", "second-device", "", 2)
	if !errors.Is(e, ErrNotFound) || len(pageOther.Items) != 0 {
		t.Fatal("other authorized device leaked claims or closure", pageOther, e)
	}
	h.service.SetLibraryAccess(func(context.Context, *sql.Tx, identity.Principal, string) error { return identity.ErrForbidden })
	page, e := h.service.ListSelectionAction(ctx, h.viewer, "operation", "device", "", 2)
	if e != nil || len(page.Items) != 0 || page.NextCursor == "" {
		t.Fatal("hidden raw page must advance", page, e)
	}
	h.service.SetRequestAccess(func(context.Context, *sql.Tx, identity.Principal, string, string) (string, []any, error) {
		return "", nil, identity.ErrUnauthorized
	})
	if _, e = h.service.ListSelectionAction(ctx, h.viewer, "operation", "device", page.NextCursor, 2); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal("revoked next page", e)
	}
	var n int
	if e = h.db.QueryRow(`SELECT count(*) FROM download_action_claims WHERE profile_key=?`, operations.ViewerKey(h.viewer)).Scan(&n); e != nil || n != 7 {
		t.Fatal(n, e)
	}
}
func TestSelectionDownloadMultipartRefusalNoClaims(t *testing.T) {
	h := selectionDownloadHarness(t, 0)
	ctx := context.Background()
	if _, e := h.db.Exec(`UPDATE catalog_asset_links SET part_index=1 WHERE entity_id=?`, h.itemID); e != nil {
		t.Fatal(e)
	}
	e := dbwork.WithWriteTx(ctx, h.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		id, _, e := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: h.root + "/part2.mp4", Size: 10, ModifiedNS: 1, Container: "mp4"})
		if e != nil {
			return e
		}
		return compactcatalog.LinkAssetTx(ctx, tx, h.itemID, id, compactcatalog.Link{Part: 2})
	})
	if e != nil {
		t.Fatal(e)
	}
	target := selectionDownloadTarget(t, h, h.item, h.itemID)
	if target.VersionID != "" || target.DownloadIdentity != ReasonSeveralFiles {
		t.Fatal(target)
	}
	if e = h.service.ApplySelectionDownload(ctx, h.viewer, "download", selectionDownloadOptions(), target, nil, selectionDownloadAuthorize, func(_ *sql.Tx, effect selectionactions.Effect) error {
		if effect.Code != ReasonSeveralFiles {
			t.Fatal(effect)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	selectionDownloadCounts(t, h, 0, 0)
}

func TestSelectionDownloadConvertedQuotaAndPolicy(t *testing.T) {
	h := selectionDownloadHarness(t, 0)
	ctx := context.Background()
	old := decoder.CurrentToolchain()
	decoder.ConfigureToolchain(decoder.ToolchainFacts{Encoders: map[string]bool{"libx264": true, "aac": true}})
	defer decoder.RestoreToolchain(old)
	target := selectionDownloadTarget(t, h, h.item, h.itemID)
	opts := selectionDownloadOptions()
	opts.Quality = "720p-high"
	var effect selectionactions.Effect
	cp := func(_ *sql.Tx, e selectionactions.Effect) error { effect = e; return nil }
	if _, e := h.db.Exec(`UPDATE download_settings SET max_prepared_bytes=1 WHERE singleton=1`); e != nil {
		t.Fatal(e)
	}
	if e := h.service.ApplySelectionDownload(ctx, h.viewer, "download", opts, target, nil, selectionDownloadAuthorize, cp); e != nil {
		t.Fatal(e)
	}
	if effect.Code != ReasonStorageFull {
		t.Fatal("converted storage ceiling", effect)
	}
	selectionDownloadCounts(t, h, 0, 0)
	if _, e := h.db.Exec(`UPDATE download_settings SET max_prepared_bytes=0 WHERE singleton=1`); e != nil {
		t.Fatal(e)
	}
	if e := h.service.ApplySelectionDownload(ctx, h.viewer, "download", opts, target, nil, selectionDownloadAuthorize, cp); e != nil {
		t.Fatal(e)
	}
	if effect.Code != "" {
		t.Fatal("converted admission", effect)
	}
	selectionDownloadCounts(t, h, 1, 1)
	var quality, recipe string
	if e := h.db.QueryRow(`SELECT quality,recipe FROM download_preparations`).Scan(&quality, &recipe); e != nil || quality != opts.Quality || recipe == "" {
		t.Fatal(quality, recipe, e)
	}
	if _, e := h.db.Exec(`UPDATE download_settings SET max_prepared_bytes=1 WHERE singleton=1`); e != nil {
		t.Fatal(e)
	}
	target.OperationID = "reuse-converted"
	if e := h.service.ApplySelectionDownload(ctx, h.viewer, "download", opts, target, nil, selectionDownloadAuthorize, cp); e != nil {
		t.Fatal(e)
	}
	if effect.Code != "" {
		t.Fatal("reused preparation was charged twice", effect)
	}
	selectionDownloadCounts(t, h, 1, 2)
}

func TestSelectionDownloadLifetimeRefusesReplacement(t *testing.T) {
	h := selectionDownloadHarness(t, 0)
	ctx := context.Background()
	target := selectionDownloadTarget(t, h, h.item, h.itemID)
	if _, e := h.db.Exec(`UPDATE catalog_entities SET added_ms=COALESCE(added_ms,0)+1 WHERE id=?`, h.itemID); e != nil {
		t.Fatal(e)
	}
	called := false
	e := h.service.ApplySelectionDownload(ctx, h.viewer, "download", selectionDownloadOptions(), target, nil, selectionDownloadAuthorize, func(*sql.Tx, selectionactions.Effect) error { called = true; return nil })
	if !errors.Is(e, ErrConflict) || called {
		t.Fatal("replacement inherited command", e, called)
	}
	var refusal *selectionactions.TargetFailure
	if !errors.As(e, &refusal) || refusal.Code != ReasonSourceChanged {
		t.Fatal("changed target must finish as a proved refusal", e)
	}
	selectionDownloadCounts(t, h, 0, 0)
}

func TestSelectionDownloadOversizedSourceRefusesBeforeRetention(t *testing.T) {
	h := selectionDownloadHarness(t, 0)
	ctx := context.Background()
	target := selectionDownloadTarget(t, h, h.item, h.itemID)
	if _, e := h.db.Exec(`UPDATE catalog_assets SET path=? WHERE token=?`, strings.Repeat("p", 70<<10), h.asset); e != nil {
		t.Fatal(e)
	}
	g, e := dbwork.BeginSnapshot(ctx, h.db)
	if e != nil {
		t.Fatal(e)
	}
	defer g.Rollback()
	c := &SelectionDownloadCapture{}
	if _, done, e := CaptureSelectionDownload(ctx, g.Tx(), target, c); done || !errors.Is(e, selectionactions.ErrCapacity) {
		t.Fatal(done, e)
	}
	if c.Bytes() > 1024 {
		t.Fatal("oversized source retained", c.Bytes())
	}
}
