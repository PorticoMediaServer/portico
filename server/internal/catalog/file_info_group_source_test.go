package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

type groupSQLFile struct {
	path                     string
	part                     int64
	container, codec         string
	height, size             int64
	duration                 float64
	available, linkAvailable bool
}

// groupSQLFixture writes a small playable item through the real catalogue
// writer and the FI1 publication worker, then publishes its known link display
// intersection through the real availability completion hook. The batches stay
// well below the existing metadata fan-out budget; no large fixture or benchmark
// is implied.
func groupSQLFixture(t *testing.T) (*catalogtest.Catalog, int64, []int64) {
	t.Helper()
	c := catalogtest.Open(t)
	library := c.Library("groups", "Groups", "movie", "/groups")
	item := c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Key: "groups", Title: "Groups"}, nil)
	specs := []groupSQLFile{
		{path: "/groups/a0.mkv", part: 0, container: "mkv", codec: "h264", height: 2160, size: 100, duration: 1.0, available: true, linkAvailable: true},
		{path: "/groups/a1.mkv", part: 0, container: "mkv", codec: "h264", height: 2160, size: 101, duration: 1.5, available: true, linkAvailable: true},
		{path: "/groups/a2.mkv", part: 1, container: "mkv", codec: "h264", height: 2160, size: 102, duration: 2.0, available: true, linkAvailable: true},
		{path: "/groups/b0.mp4", part: 0, container: "mp4", codec: "hevc", height: 2160, size: 200, duration: 3.0, available: true, linkAvailable: true},
		{path: "/groups/b1.mp4", part: 1, container: "mp4", codec: "hevc", height: 2160, size: 201, duration: 4.0, available: true, linkAvailable: true},
		{path: "/groups/c0.avi", part: 0, container: "avi", codec: "mpeg4", height: 480, size: 300, duration: 5.0, available: true, linkAvailable: true},
		{path: "/groups/d0.mkv", part: 0, container: "mkv", codec: "h264", height: 720, size: 400, duration: 6.0, available: true, linkAvailable: false},
		{path: "/groups/d1.mkv", part: 1, container: "mkv", codec: "h264", height: 720, size: 401, duration: 7.0, available: true, linkAvailable: true},
		{path: "/groups/e0.mkv", part: 0, container: "mkv", codec: "hevc", height: 1080, size: 500, duration: 8.0, available: false, linkAvailable: false},
		{path: "/groups/e1.mkv", part: 1, container: "mkv", codec: "hevc", height: 1080, size: 501, duration: 9.0, available: false, linkAvailable: false},
	}
	assets := make([]int64, len(specs))
	for i, spec := range specs {
		asset, _ := c.File(item.ID, spec.path, spec.duration)
		assets[i] = asset
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			if err := compactcatalog.SetAssetTx(ctx, tx, asset, map[string]any{
				"container": spec.container, "video_codec": spec.codec, "height": spec.height,
				"size": spec.size, "available": spec.available,
			}); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `UPDATE catalog_asset_links SET part_index=? WHERE entity_id=? AND asset_id=?`, spec.part, item.ID, asset)
			return err
		})
	}
	c.Drain()
	groupSQLPublishLinkDisplay(t, c, item.ID, specs, assets)
	return c, item.ID, assets
}

// groupSQLPublishLinkDisplay writes the fixture's complete known per-link display
// output and completes the real informational availability acceptance for that
// output. The link flag is the display intersection and is independent of the
// accepted Options availability, so it is applied directly after drain; the
// accepting transaction then calls the same completion hook a holder
// publication uses. It must not be followed by a drain, which would republish
// the link flag from the asset and erase the intended display value.
func groupSQLPublishLinkDisplay(t *testing.T, c *catalogtest.Catalog, item int64, specs []groupSQLFile, assets []int64) {
	t.Helper()
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for i, spec := range specs {
			if spec.linkAvailable {
				continue
			}
			if _, err := tx.ExecContext(ctx, `UPDATE catalog_asset_links SET available=0 WHERE entity_id=? AND asset_id=? AND available<>0`, item, assets[i]); err != nil {
				return err
			}
		}
		return compactcatalog.CompleteInfoAvailabilityTx(ctx, tx, item)
	})
}

func groupSQLRun(t *testing.T, c *catalogtest.Catalog, item int64, canManage, canSplitKind bool, spec fileInfoGroupSpec) (fileInfoGroupSummary, error) {
	t.Helper()
	if spec.Limit == 0 {
		spec.Limit = 100
	}
	if spec.Direction == 0 {
		spec.Direction = fileInfoForward
	}
	head, err := compactcatalog.InfoHeadTx(context.Background(), c.DB, item)
	if err != nil {
		return fileInfoGroupSummary{}, err
	}
	if !head.Ready() {
		t.Fatal("fixture item is not ready")
	}
	plan, err := fileInfoGroupPlan(c.DB, item, canManage, canSplitKind, spec, 64, orderedInfoHeadCheck(c.DB, item, head.Desired))
	if err != nil {
		return fileInfoGroupSummary{}, err
	}
	return groupInfoPollPlan(t, plan)
}

// groupSQLOracle is the independent legacy Options projection over the current
// accepted canonical row. It does not call the reducer or its SQL.
func groupSQLOracle(t *testing.T, c *catalogtest.Catalog, item int64) (int64, int64, []fileInfoGroupNumeric) {
	t.Helper()
	rows, err := c.DB.Query(`SELECT l.asset_id,l.info_shape_id,l.part_index,l.available,a.info_order_available,a.size,a.height,a.token,a.duration
 FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=?`, item)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	files := []groupTestFile{}
	for rows.Next() {
		f := groupTestFile{OrderAvailable: 1}
		if err := rows.Scan(&f.Asset, &f.Shape, &f.Part, &f.Available, &f.Offered, &f.Size, &f.Height, &f.Token, &f.Duration); err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return groupOracleFromFiles(files)
}

func TestFileInfoGroupSQLSourceMatchesLegacyOptionsGrouping(t *testing.T) {
	c, item, _ := groupSQLFixture(t)
	raw, eligible, want := groupSQLOracle(t, c, item)
	page, err := groupSQLRun(t, c, item, true, true, fileInfoGroupSpec{Limit: 100, Direction: fileInfoForward})
	if err != nil {
		t.Fatal(err)
	}
	if page.Files != raw || page.Eligible != eligible || page.Total != int64(len(want)) || page.FirstOrdinal != 1 {
		t.Fatalf("counts %+v want files%d eligible%d total%d", page, raw, eligible, len(want))
	}
	if !reflect.DeepEqual(page.Groups, want) {
		t.Fatalf("groups %+v want %+v", page.Groups, want)
	}
	if !page.CanSplit {
		t.Fatal("displayed one-member shape lost split eligibility")
	}
	// Every walk and point comparator must be an indexed seek with no sort.
	for _, check := range []struct {
		name  string
		query string
		args  []any
		index string
	}{
		{"part", fileInfoGroupPartFirst, []any{item}, "catalog_asset_links_order"},
		{"part-current", fileInfoGroupPartCurrent, []any{item, 0, 0}, "catalog_asset_links_order"},
		{"part-next", fileInfoGroupPartNext, []any{item, 0}, "catalog_asset_links_order"},
		{"shape", fileInfoGroupShapeFirst, []any{item}, "catalog_asset_links_shape"},
		{"shape-current", fileInfoGroupShapeCurrent, []any{item, 1, 0, 0}, "catalog_asset_links_shape"},
		{"shape-part-next", fileInfoGroupShapePartNext, []any{item, 1, 0}, "catalog_asset_links_shape"},
		{"shape-next", fileInfoGroupShapeNext, []any{item, 1}, "catalog_asset_links_shape"},
		{"shape-file", fileInfoGroupShapeFileFirst, []any{item, 1}, "catalog_asset_links_shape"},
	} {
		plan, err := c.DB.Query(`EXPLAIN QUERY PLAN `+check.query, check.args...)
		if err != nil {
			t.Fatal(check.name, err)
		}
		details := []string{}
		for plan.Next() {
			var id, parent, unused int
			var detail string
			if err := plan.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			details = append(details, detail)
		}
		if err := plan.Err(); err != nil {
			t.Fatal(err)
		}
		plan.Close()
		joined := strings.Join(details, "\n")
		if strings.Contains(joined, "TEMP") || strings.Contains(joined, "SCAN ") {
			t.Fatalf("%s used an unbounded plan: %s", check.name, joined)
		}
		if !strings.Contains(joined, check.index) {
			t.Fatalf("%s did not use %s: %s", check.name, check.index, joined)
		}
		// The accepted OLD overlay is a correlated point probe on the
		// unique(asset_id,asset_token,slot) index with the accepted slot order, so
		// it must appear as an index search rather than a Go-side scalar overlay or
		// a temporary sort of the delta rows.
		if !strings.Contains(joined, "catalog_info_changes") {
			t.Fatalf("%s did not probe the accepted delta index: %s", check.name, joined)
		}
	}
}

func TestFileInfoGroupSQLSourceKeepsAcceptedFactsDuringRawChurn(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("churn", "Churn", "movie", "/churn")
	item := c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Key: "churn", Title: "Churn"}, nil)
	assets := []int64{}
	for i := 0; i < 4; i++ {
		asset, _ := c.File(item.ID, fmt.Sprintf("/churn/%d.mkv", i), float64(10+i))
		assets = append(assets, asset)
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			if err := compactcatalog.SetAssetTx(ctx, tx, asset, map[string]any{"height": int64(1080), "size": int64(100 + i), "available": true}); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `UPDATE catalog_asset_links SET part_index=? WHERE entity_id=? AND asset_id=?`, i, item.ID, asset)
			return err
		})
	}
	c.Drain()
	before, err := groupSQLRun(t, c, item.ID, true, true, fileInfoGroupSpec{Limit: 100, Direction: fileInfoForward})
	if err != nil || before.Total != 1 {
		t.Fatalf("initial accepted group %+v %v", before, err)
	}
	// Raw NEW must not reorder or regrow the accepted group while OLD is the
	// accepted basis. The accepting transaction installs the OLD deltas.
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetTx(ctx, tx, assets[0], map[string]any{"height": int64(2160), "size": int64(999999), "duration": 99.5, "available": false})
	})
	after, err := groupSQLRun(t, c, item.ID, true, true, fileInfoGroupSpec{Limit: 100, Direction: fileInfoForward})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("raw NEW escaped the accepted group basis: %+v want %+v", after, before)
	}
	// Once the deferred publication is accepted and drained, a new opening sees
	// the NEW facts.
	c.Drain()
	raw, eligible, want := groupSQLOracle(t, c, item.ID)
	accepted, err := groupSQLRun(t, c, item.ID, true, true, fileInfoGroupSpec{Limit: 100, Direction: fileInfoForward})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Files != raw || accepted.Eligible != eligible || !reflect.DeepEqual(accepted.Groups, want) {
		t.Fatalf("post-drain accepted group %+v want %+v", accepted, want)
	}
}

func TestFileInfoGroupSQLSourceRejectsMissingAcceptedShape(t *testing.T) {
	c, item, assets := groupSQLFixture(t)
	if _, err := groupSQLRun(t, c, item, true, true, fileInfoGroupSpec{Limit: 100, Direction: fileInfoForward}); err != nil {
		t.Fatal(err)
	}
	c.Exec(`UPDATE catalog_asset_links SET info_shape_id=NULL WHERE entity_id=? AND asset_id=?`, item, assets[0])
	if _, err := groupSQLRun(t, c, item, true, true, fileInfoGroupSpec{Limit: 100, Direction: fileInfoForward}); err == nil {
		t.Fatal("missing accepted shape was approximated")
	}
}

// groupSQLAccepted is the accepted scalar frame the group walk must reproduce:
// compactcatalog.ReadInfoAsset, the same reader the playback projection uses.
func groupSQLAccepted(t *testing.T, c *catalogtest.Catalog, asset int64, token string) compactcatalog.InfoAsset {
	t.Helper()
	var out compactcatalog.InfoAsset
	err := dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
		var err error
		out, err = compactcatalog.ReadInfoAsset(ctx, dbwork.Snapshot(ctx), asset, token, false)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// groupSQLOldDelta stages one source change with the accepted OLD size (field 2)
// and duration (field 9) deltas the accepting transaction installs.
func groupSQLOldDelta(t *testing.T, c *catalogtest.Catalog, asset int64, token string, slot, phase, size int64, duration float64) int64 {
	t.Helper()
	c.Exec(`INSERT INTO catalog_info_changes(asset_id,asset_token,slot,phase,expected_epoch,fingerprint,created_ms) VALUES(?,?,?,?,1,zeroblob(32),0)`, asset, token, slot, phase)
	var change int64
	if err := c.DB.QueryRow(`SELECT id FROM catalog_info_changes WHERE asset_id=? AND asset_token=? AND slot=?`, asset, token, slot).Scan(&change); err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO catalog_info_scalar_old(change_id,field,kind,old_integer) VALUES(?,2,1,?)`, change, size)
	c.Exec(`INSERT INTO catalog_info_scalar_old(change_id,field,kind,old_real) VALUES(?,9,2,?)`, change, duration)
	return change
}

// TestFileInfoGroupSQLSourceAcceptedOLDSlotOrder pins the group scalar overlay to
// the accepted ReadInfoAsset rule: the first pre-fence OLD delta per field in
// (slot,field) order. The active slot-0 OLD owns size and duration while it is
// before activation, so the queued slot-1 OLD must not win; once the active change
// has activated (phase>=3) it stops overlaying and the queued OLD is accepted.
// Both branches are compared with the accepted reader itself, so a slot-1-first
// probe cannot pass the test.
func TestFileInfoGroupSQLSourceAcceptedOLDSlotOrder(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("old", "Old", "movie", "/old")
	item := c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Key: "old", Title: "Old"}, nil)
	asset, token := c.File(item.ID, "/old/one.mkv", 10)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetTx(ctx, tx, asset, map[string]any{
			"container": "mkv", "video_codec": "h264", "height": int64(1080), "size": int64(100), "available": true,
		})
	})
	c.Drain()
	active := groupSQLOldDelta(t, c, asset, token, 0, 1, 111, 1.5)
	groupSQLOldDelta(t, c, asset, token, 1, 0, 222, 2.5)
	for _, tc := range []struct {
		name     string
		phase    int64
		size     int64
		duration float64
	}{
		{"active-slot0-pre-fence", 1, 111, 1.5},
		{"queued-slot1-after-active", 4, 222, 2.5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c.Exec(`UPDATE catalog_info_changes SET phase=? WHERE id=?`, tc.phase, active)
			accepted := groupSQLAccepted(t, c, asset, token)
			if accepted.Size != tc.size || accepted.Duration != tc.duration {
				t.Fatalf("accepted frame %+v want size%d duration%v", accepted, tc.size, tc.duration)
			}
			page, err := groupSQLRun(t, c, item.ID, true, true, fileInfoGroupSpec{Limit: 100, Direction: fileInfoForward})
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Groups) != 1 || page.Groups[0].SizeBytes != accepted.Size || page.Groups[0].DurationMS != int64(accepted.Duration*1000) {
				t.Fatalf("group overlay %+v does not match accepted %+v", page.Groups, accepted)
			}
		})
	}
}
