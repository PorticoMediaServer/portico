package catalog

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// memberTestFiles reads the fixture links once, ordered exactly as the raw walks
// are, so the fake lane and the SQL lane are compared over the same facts. It does
// not call the member source or reuse its statements.
func memberTestFiles(t *testing.T, c *catalogtest.Catalog, item int64) []groupTestFile {
	t.Helper()
	rows, err := c.DB.Query(`SELECT l.asset_id,l.info_shape_id,l.part_index,l.available,a.info_order_available,a.size,a.height,a.token,a.duration
 FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=? ORDER BY l.part_index,l.asset_id`, item)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	files := []groupTestFile{}
	for rows.Next() {
		f := groupTestFile{}
		var shape sql.NullInt64
		if err := rows.Scan(&f.Asset, &shape, &f.Part, &f.Available, &f.Offered, &f.Size, &f.Height, &f.Token, &f.Duration); err != nil {
			t.Fatal(err)
		}
		if shape.Valid {
			f.Shape = shape.Int64
		}
		if f.Offered {
			f.OrderAvailable = 1
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return files
}

// memberTestSource is the same ordered lane in memory: the fixture rows in raw
// order and the accepted comparison without SQL. It is the independent oracle for
// the real source's walk, matching and ordinal accounting.
type memberTestSource struct {
	item  int64
	files []groupTestFile
	rows  []fileInfoOrderedRow
	index int
}

func (s *memberTestSource) AccountedBytes() int64 { return 512 }

func (s *memberTestSource) Check(context.Context) error { return nil }

func (s *memberTestSource) Next(context.Context) (fileInfoOrderedRow, bool, error) {
	if s.index == len(s.rows) {
		return fileInfoOrderedRow{}, false, nil
	}
	row := s.rows[s.index]
	s.index++
	return row, true, nil
}

func (s *memberTestSource) Compare(_ context.Context, a, b fileInfoOccurrence) (int, error) {
	if a.Item != s.item || b.Item != s.item || a.Asset <= 0 || b.Asset <= 0 {
		return 0, readprep.ErrInvalid
	}
	if a == b {
		return 0, nil
	}
	var fa, fb groupTestFile
	var oka, okb bool
	for _, f := range s.files {
		switch f.Asset {
		case a.Asset:
			fa, oka = f, true
		case b.Asset:
			fb, okb = f, true
		}
	}
	if !oka || !okb {
		return 0, readprep.ErrStale
	}
	cmp := groupInfoTestOrder(fa, fb)
	if cmp == 0 {
		return 0, readprep.ErrInvalid
	}
	return cmp, nil
}

// memberTestRows is the expected raw sequence for one captured group: every raw
// link in the group's walked range, in (part,asset) order, matched only when it is
// both offered and displayed.
func memberTestRows(group fileInfoGroupNumeric, files []groupTestFile, item int64) []fileInfoOrderedRow {
	rows := []fileInfoOrderedRow{}
	for _, f := range files {
		switch group.Type {
		case fileInfoGroupSingleton:
			if f.Asset != group.Head {
				continue
			}
		case fileInfoGroupShapeComposite:
			if f.Shape != group.Shape {
				continue
			}
		}
		match := f.Offered && f.Available
		rows = append(rows, fileInfoOrderedRow{
			Occurrence: fileInfoOccurrence{Item: item, Asset: f.Asset},
			Sequence:   int64(len(rows) + 1),
			Match:      match,
			Eligible:   match,
		})
	}
	return rows
}

func memberTestPoll(t *testing.T, plan readprep.Plan) (fileInfoOrderedSummary, error) {
	t.Helper()
	store, err := readprep.New(readprep.Config{Workers: 1, MaxBytes: 512 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	handle, err := store.Submit(ctx, "member-private", readprep.Key{Namespace: "member-test", Basis: "original-info-head", Canonical: "window"}, plan)
	if err != nil {
		return fileInfoOrderedSummary{}, err
	}
	defer store.Cancel(ctx, "member-private", handle)
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		result, err := store.Poll(ctx, "member-private", handle)
		if err != nil {
			return fileInfoOrderedSummary{}, err
		}
		if result.State == readprep.Ready {
			defer result.Release()
			if err := result.Reserve(1600); err != nil {
				return fileInfoOrderedSummary{}, err
			}
			if len(result.Payload) > 28+16*100 {
				t.Fatal("member lane retained something beyond bounded numeric coordinates")
			}
			return decodeFileInfoOrderedSummary(result.Payload)
		}
		result.Release()
		select {
		case <-timeout.C:
			t.Fatal("member preparation did not finish")
		default:
			runtime.Gosched()
		}
	}
}

func memberTestRun(t *testing.T, source fileInfoOrderedSource, spec fileInfoOrderedSpec) (fileInfoOrderedSummary, error) {
	t.Helper()
	if spec.Limit == 0 {
		spec.Limit = 100
	}
	if spec.Direction == 0 {
		spec.Direction = fileInfoForward
	}
	plan, err := fileInfoOrderedPlan(source, spec)
	if err != nil {
		return fileInfoOrderedSummary{}, err
	}
	return memberTestPoll(t, plan)
}

func memberTestSourceFor(t *testing.T, c *catalogtest.Catalog, item int64, group fileInfoGroupNumeric) fileInfoOrderedSource {
	t.Helper()
	head, err := compactcatalog.InfoHeadTx(context.Background(), c.DB, item)
	if err != nil {
		t.Fatal(err)
	}
	if !head.Ready() {
		t.Fatal("fixture item is not ready")
	}
	source, err := newFileInfoMemberSource(c.DB, item, group, 0, orderedInfoHeadCheck(c.DB, item, head.Desired))
	if err != nil {
		t.Fatal(err)
	}
	return source
}

// memberTestGroup returns the first captured group of one type.
func memberTestGroup(t *testing.T, groups []fileInfoGroupNumeric, kind byte) fileInfoGroupNumeric {
	t.Helper()
	for _, g := range groups {
		if g.Type == kind {
			return g
		}
	}
	t.Fatalf("no captured group of type %d in %+v", kind, groups)
	return fileInfoGroupNumeric{}
}

func memberTestSummary(t *testing.T, c *catalogtest.Catalog, item int64) fileInfoGroupSummary {
	t.Helper()
	summary, err := groupSQLRun(t, c, item, true, true, fileInfoGroupSpec{Limit: 100, Direction: fileInfoForward})
	if err != nil {
		t.Fatal(err)
	}
	return summary
}

// TestFileInfoMemberSourceMatchesBaselineGroupMembers is the fake/real baseline
// equivalence: for every captured group type the SQL member lane must publish
// exactly the in-memory lane's occurrences, total and eligible count.
func TestFileInfoMemberSourceMatchesBaselineGroupMembers(t *testing.T) {
	c, item, _ := groupSQLFixture(t)
	summary := memberTestSummary(t, c, item)
	files := memberTestFiles(t, c, item)
	if len(summary.Groups) < 3 {
		t.Fatalf("fixture groups are not discriminating: %+v", summary.Groups)
	}
	seen := map[byte]bool{}
	for _, group := range summary.Groups {
		seen[group.Type] = true
		got, err := memberTestRun(t, memberTestSourceFor(t, c, item, group), fileInfoOrderedSpec{Limit: 100, Direction: fileInfoForward})
		if err != nil {
			t.Fatalf("%+v: %v", group, err)
		}
		fake := &memberTestSource{item: item, files: files, rows: memberTestRows(group, files, item)}
		want, err := memberTestRun(t, fake, fileInfoOrderedSpec{Limit: 100, Direction: fileInfoForward})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("group %+v members %+v want %+v", group, got, want)
		}
		if got.Total != group.Members || got.Eligible != group.Members {
			t.Fatalf("group %+v counted total%d eligible%d", group, got.Total, got.Eligible)
		}
	}
	if !seen[fileInfoGroupSingleton] || !seen[fileInfoGroupShapeComposite] {
		t.Fatalf("fixture did not cover every grouping shape: %+v", summary.Groups)
	}
}

// TestFileInfoMemberSourceWalksSingletonOwnHead pins the clustered-singleton
// rule: a singleton replayed from a duplicate-part shape may have a later offered
// Head than the shape's first ShapeHead, and the member lane must publish that
// own Head rather than the shape head.
func TestFileInfoMemberSourceWalksSingletonOwnHead(t *testing.T) {
	c, item, _ := groupSQLFixture(t)
	summary := memberTestSummary(t, c, item)
	var singleton fileInfoGroupNumeric
	for _, group := range summary.Groups {
		if group.Type == fileInfoGroupSingleton && group.Head != group.ShapeHead {
			singleton = group
			break
		}
	}
	if singleton.Head == 0 {
		t.Fatalf("fixture has no clustered singleton: %+v", summary.Groups)
	}
	got, err := memberTestRun(t, memberTestSourceFor(t, c, item, singleton), fileInfoOrderedSpec{Limit: 100, Direction: fileInfoForward})
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 1 || len(got.Occurrences) != 1 || got.Occurrences[0].Asset != singleton.Head {
		t.Fatalf("clustered singleton %+v published %+v", singleton, got)
	}
	for _, occurrence := range got.Occurrences {
		if occurrence.Asset == singleton.ShapeHead {
			t.Fatalf("clustered singleton published the shape head instead: %+v", got)
		}
	}
}

// TestFileInfoMemberSourceWalksGlobalComposite covers the mixed-shape global
// composite, where the walk is the whole item in (part,asset) order rather than a
// single shape run.
func TestFileInfoMemberSourceWalksGlobalComposite(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("global", "Global", "movie", "/global")
	item := c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Key: "global", Title: "Global"}, nil)
	specs := []groupSQLFile{
		{path: "/global/0.mkv", part: 0, container: "mkv", codec: "h264", height: 2160, size: 100, duration: 1.0, available: true, linkAvailable: true},
		{path: "/global/1.mp4", part: 1, container: "mp4", codec: "hevc", height: 1080, size: 200, duration: 2.0, available: true, linkAvailable: true},
		{path: "/global/2.avi", part: 2, container: "avi", codec: "mpeg4", height: 480, size: 300, duration: 3.0, available: true, linkAvailable: false},
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
	summary := memberTestSummary(t, c, item.ID)
	if len(summary.Groups) != 1 || summary.Groups[0].Type != fileInfoGroupGlobalComposite {
		t.Fatalf("mixed shapes did not form one global composite: %+v", summary.Groups)
	}
	group := summary.Groups[0]
	files := memberTestFiles(t, c, item.ID)
	got, err := memberTestRun(t, memberTestSourceFor(t, c, item.ID, group), fileInfoOrderedSpec{Limit: 100, Direction: fileInfoForward})
	if err != nil {
		t.Fatal(err)
	}
	want, err := memberTestRun(t, &memberTestSource{item: item.ID, files: files, rows: memberTestRows(group, files, item.ID)}, fileInfoOrderedSpec{Limit: 100, Direction: fileInfoForward})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) || got.Total != 2 || len(got.Occurrences) != 2 {
		t.Fatalf("global composite members %+v want %+v", got, want)
	}
	// The walk is the item's raw part order, not the shape index order.
	var parts []int64
	for _, o := range got.Occurrences {
		var part int64
		if err := c.DB.QueryRow(`SELECT part_index FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, item.ID, o.Asset).Scan(&part); err != nil {
			t.Fatal(err)
		}
		parts = append(parts, part)
	}
	if !reflect.DeepEqual(parts, []int64{0, 1}) {
		t.Fatalf("global composite parts %v", parts)
	}
}

// TestFileInfoMemberSourceRejectsStaleGroupPoints pins the fail-stale rule: a
// vanished head, a re-shaped head, a NULL accepted shape and an anchor that is not
// a member must never be answered with a different group or an empty success.
func TestFileInfoMemberSourceRejectsStaleGroupPoints(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	newShapeComposite := func(t *testing.T) (*catalogtest.Catalog, int64, fileInfoGroupNumeric) {
		c, item, _ := groupSQLFixture(t)
		return c, item, memberTestGroup(t, memberTestSummary(t, c, item).Groups, fileInfoGroupShapeComposite)
	}
	t.Run("missing-head", func(t *testing.T) {
		c, item, group := newShapeComposite(t)
		// A captured descriptor whose head link has since vanished still names the
		// same shape identity; the missing membership point must fail stale rather
		// than start a walk of the remaining members.
		group.ShapeHead, group.Head = group.ShapeHead+(1<<40), group.Head+(1<<40)
		if _, err := memberTestRun(t, memberTestSourceFor(t, c, item, group), fileInfoOrderedSpec{Limit: 100, Direction: fileInfoForward}); !errors.Is(err, readprep.ErrStale) {
			t.Fatalf("missing head accepted: %v", err)
		}
	})
	t.Run("reshaped-head", func(t *testing.T) {
		c, item, group := newShapeComposite(t)
		var other int64
		if err := c.DB.QueryRow(`SELECT id FROM catalog_info_shapes WHERE id<>? LIMIT 1`, group.Shape).Scan(&other); err != nil {
			t.Fatal(err)
		}
		c.Exec(`UPDATE catalog_asset_links SET info_shape_id=? WHERE entity_id=? AND asset_id=?`, other, item, group.ShapeHead)
		if _, err := memberTestRun(t, memberTestSourceFor(t, c, item, group), fileInfoOrderedSpec{Limit: 100, Direction: fileInfoForward}); !errors.Is(err, readprep.ErrStale) {
			t.Fatalf("re-shaped head accepted: %v", err)
		}
	})
	t.Run("null-head-shape", func(t *testing.T) {
		c, item, group := newShapeComposite(t)
		c.Exec(`UPDATE catalog_asset_links SET info_shape_id=NULL WHERE entity_id=? AND asset_id=?`, item, group.ShapeHead)
		if _, err := memberTestRun(t, memberTestSourceFor(t, c, item, group), fileInfoOrderedSpec{Limit: 100, Direction: fileInfoForward}); !errors.Is(err, readprep.ErrStale) {
			t.Fatalf("NULL head shape accepted: %v", err)
		}
	})
	// An interior link with no accepted shape is not part of any accepted shape
	// run: the group lane's shape walk is what rejects that corruption, while this
	// lane can only walk the exact accepted shape the descriptor names. It must
	// therefore never invent the broken row as a member.
	t.Run("null-interior-shape-not-invented", func(t *testing.T) {
		c, item, group := newShapeComposite(t)
		files := memberTestFiles(t, c, item)
		var other int64
		for _, f := range files {
			if f.Shape == group.Shape && f.Asset != group.ShapeHead {
				other = f.Asset
				break
			}
		}
		if other == 0 {
			t.Fatal("fixture has no non-head member")
		}
		c.Exec(`UPDATE catalog_asset_links SET info_shape_id=NULL WHERE entity_id=? AND asset_id=?`, item, other)
		got, err := memberTestRun(t, memberTestSourceFor(t, c, item, group), fileInfoOrderedSpec{Limit: 100, Direction: fileInfoForward})
		if err != nil {
			t.Fatal(err)
		}
		for _, occurrence := range got.Occurrences {
			if occurrence.Asset == other {
				t.Fatalf("NULL-shape link was invented as a member: %+v", got)
			}
		}
	})
	t.Run("foreign-anchor", func(t *testing.T) {
		c, item, group := newShapeComposite(t)
		var foreign int64
		if err := c.DB.QueryRow(`SELECT l.asset_id FROM catalog_asset_links l WHERE l.entity_id=? AND l.info_shape_id<>? AND l.info_shape_id IS NOT NULL LIMIT 1`, item, group.Shape).Scan(&foreign); err != nil {
			t.Fatal(err)
		}
		spec := fileInfoOrderedSpec{Limit: 100, Direction: fileInfoForward, HasAnchor: true, Anchor: fileInfoOccurrence{Item: item, Asset: foreign}}
		if _, err := memberTestRun(t, memberTestSourceFor(t, c, item, group), spec); !errors.Is(err, readprep.ErrStale) {
			t.Fatalf("foreign anchor accepted: %v", err)
		}
	})
}

// TestFileInfoMemberSourceKeepsRawRejectionsVisible proves the lane publishes only
// offered AND displayed files while still consuming every raw row, and that a
// captured group whose members are no longer offered becomes an empty page rather
// than an error or a phantom member.
func TestFileInfoMemberSourceKeepsRawRejectionsVisible(t *testing.T) {
	c, item, _ := groupSQLFixture(t)
	summary := memberTestSummary(t, c, item)
	files := memberTestFiles(t, c, item)
	var partial fileInfoGroupNumeric
	for _, group := range summary.Groups {
		if group.Type == fileInfoGroupShapeComposite && group.OfferedMembers == 2 && group.Members == 1 {
			partial = group
		}
	}
	if partial.ShapeHead == 0 {
		t.Fatalf("fixture has no half-displayed composite: %+v", summary.Groups)
	}
	source := memberTestSourceFor(t, c, item, partial)
	got, err := memberTestRun(t, source, fileInfoOrderedSpec{Limit: 100, Direction: fileInfoForward})
	if err != nil {
		t.Fatal(err)
	}
	want, err := memberTestRun(t, &memberTestSource{item: item, files: files, rows: memberTestRows(partial, files, item)}, fileInfoOrderedSpec{Limit: 100, Direction: fileInfoForward})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) || got.Total != 1 || len(got.Occurrences) != 1 {
		t.Fatalf("half-displayed composite %+v want %+v", got, want)
	}
	// A captured singleton whose accepted availability disappears is an empty
	// page: one raw event, no match, no error.
	singleton := memberTestGroup(t, summary.Groups, fileInfoGroupSingleton)
	c.Exec(`UPDATE catalog_assets SET info_order_available=0 WHERE id=?`, singleton.Head)
	empty, err := memberTestRun(t, memberTestSourceFor(t, c, item, singleton), fileInfoOrderedSpec{Limit: 100, Direction: fileInfoForward})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Total != 0 || empty.Eligible != 0 || len(empty.Occurrences) != 0 {
		t.Fatalf("non-offered singleton published %+v", empty)
	}
}

func TestFileInfoMemberSourceValidatesGroupDescriptor(t *testing.T) {
	c, item, _ := groupSQLFixture(t)
	group := memberTestGroup(t, memberTestSummary(t, c, item).Groups, fileInfoGroupShapeComposite)
	head, err := compactcatalog.InfoHeadTx(context.Background(), c.DB, item)
	if err != nil {
		t.Fatal(err)
	}
	check := orderedInfoHeadCheck(c.DB, item, head.Desired)
	bad := []func(fileInfoGroupNumeric) fileInfoGroupNumeric{
		func(g fileInfoGroupNumeric) fileInfoGroupNumeric { g.Type = 0; return g },
		func(g fileInfoGroupNumeric) fileInfoGroupNumeric { g.Type = 4; return g },
		func(g fileInfoGroupNumeric) fileInfoGroupNumeric { g.Head = 0; return g },
		func(g fileInfoGroupNumeric) fileInfoGroupNumeric { g.ShapeHead = g.Head + 1; return g },
		func(g fileInfoGroupNumeric) fileInfoGroupNumeric { g.Members = g.OfferedMembers + 1; return g },
		func(g fileInfoGroupNumeric) fileInfoGroupNumeric { g.Shape = 0; return g },
	}
	for i, mutate := range bad {
		if _, err := newFileInfoMemberSource(c.DB, item, mutate(group), 0, check); !errors.Is(err, readprep.ErrInvalid) {
			t.Fatalf("descriptor %d accepted: %v", i, err)
		}
	}
	if _, err := newFileInfoMemberSource(c.DB, 0, group, 0, check); !errors.Is(err, readprep.ErrInvalid) {
		t.Fatal("zero item accepted")
	}
	if _, err := newFileInfoMemberSource(c.DB, item, group, -1, check); !errors.Is(err, readprep.ErrInvalid) {
		t.Fatal("negative capture accepted")
	}
	if _, err := newFileInfoMemberSource(c.DB, item, group, 0, nil); !errors.Is(err, readprep.ErrInvalid) {
		t.Fatal("nil check accepted")
	}
	source, err := newFileInfoMemberSource(c.DB, item, group, 64, check)
	if err != nil {
		t.Fatal(err)
	}
	if got := source.AccountedBytes(); got != fileInfoMemberSourceBytes+64 {
		t.Fatalf("fixed accounting %d", got)
	}
}

// TestFileInfoMemberSourcePlansIndexedSeeksWithoutSort keeps every member
// statement an indexed single-row seek: the head point on the link primary key and
// each walk on its existing index with a strict keyset bound.
func TestFileInfoMemberSourcePlansIndexedSeeksWithoutSort(t *testing.T) {
	c := catalogtest.Open(t)
	for _, check := range []struct {
		name  string
		query string
		args  []any
		want  []string
	}{
		{"head", fileInfoMemberHead, []any{1, 1}, []string{"SEARCH l USING PRIMARY KEY", "SEARCH a USING INTEGER PRIMARY KEY"}},
		{"shape-first", fileInfoMemberShapeFirst, []any{1, 1}, []string{"catalog_asset_links_shape", "info_shape_id=?"}},
		{"shape-current", fileInfoMemberShapeCurrent, []any{1, 1, 0, 0}, []string{"catalog_asset_links_shape", "part_index=?", "asset_id>?"}},
		{"shape-part-next", fileInfoMemberShapePartNext, []any{1, 1, 0}, []string{"catalog_asset_links_shape", "part_index>?"}},
		{"part-first", fileInfoMemberPartFirst, []any{1}, []string{"catalog_asset_links_order", "entity_id=?"}},
		{"part-current", fileInfoMemberPartCurrent, []any{1, 0, 0}, []string{"catalog_asset_links_order", "part_index=?", "asset_id>?"}},
		{"part-next", fileInfoMemberPartNext, []any{1, 0}, []string{"catalog_asset_links_order", "part_index>?"}},
	} {
		t.Run(check.name, func(t *testing.T) {
			rows, err := c.DB.Query(`EXPLAIN QUERY PLAN `+check.query, check.args...)
			if err != nil {
				t.Fatal(err)
			}
			details := []string{}
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				details = append(details, detail)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			rows.Close()
			joined := strings.Join(details, "\n")
			if strings.Contains(joined, "TEMP") || strings.Contains(joined, "SCAN ") {
				t.Fatalf("unbounded member plan:\n%s", joined)
			}
			for _, want := range check.want {
				if !strings.Contains(joined, want) {
					t.Fatalf("plan missing %q:\n%s", want, joined)
				}
			}
		})
	}
}
