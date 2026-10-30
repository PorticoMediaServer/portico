package catalog

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// newFileInfoTrackFixture builds the real catalogue baseline for one movie file
// and returns the accepted parent facts the original opening would bind.
func newFileInfoTrackFixture(t *testing.T) (*catalogtest.Catalog, catalogtest.Item, int64, int64, fileInfoBasisCheck) {
	t.Helper()
	c := catalogtest.Open(t)
	library := c.Library("tracks", "Tracks", "movie", "/tracks")
	movie := c.Movie(library, "/tracks/Film.mkv", "Film", 2026)
	c.Drain()
	var size, modified int64
	if err := c.DB.QueryRow(`SELECT size,modified_ns FROM catalog_assets WHERE id=?`, movie.Asset).Scan(&size, &modified); err != nil {
		t.Fatal(err)
	}
	// The production head is captured as accepted and ready: the
	// orderedInfoHeadCheck below is the live authority probe the owner supplies,
	// not a permissive stub. A raw stat change queues an OLD header but does not
	// pre-fence it yet, so this ready head keeps admitting the accepted OLD frame
	// until the worker actually advances the publication.
	head, err := compactcatalog.InfoHeadTx(context.Background(), c.DB, movie.ID)
	if err != nil || !head.Ready() {
		t.Fatalf("fixture info head %+v: %v", head, err)
	}
	return c, movie, size, modified, orderedInfoHeadCheck(c.DB, movie.ID, head.Desired)
}

// fileInfoTrackAudioRows is inserted in an order that differs from stream_index
// order, so the raw walk must seek the index instead of following ROWID. Every
// stream row is a raw event now: the video stream is a non-matching audio event
// and the subtitle stream is a non-matching audio event too, because the type
// decision follows LIMIT instead of filtering the audio subset before it.
var fileInfoTrackAudioRows = []struct {
	index int64
	kind  string
}{
	{9, "audio"},
	{0, "video"},
	{math.MaxInt64, "audio"},
	{5, "audio"},
	{math.MinInt64, "audio"},
	{3, "audio"},
	{7, "subtitle"},
}

func insertFileInfoTrackAudio(t *testing.T, c *catalogtest.Catalog, movie catalogtest.Item, size, modified int64) {
	t.Helper()
	c.Exec(`INSERT OR IGNORE INTO asset_stream_facts(asset_id,revision,size,modified_ns,fingerprint) VALUES(?,1,?,?,'track-fixture')`, movie.Token, size, modified)
	for _, row := range fileInfoTrackAudioRows {
		c.Exec(`INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced) VALUES(?,?,?,'aac','eng','',2,'stereo',0,0)`, movie.Token, row.index, row.kind)
	}
}

func fileInfoTrackRowIDs(t *testing.T, c *catalogtest.Catalog, query string, args ...any) []int64 {
	t.Helper()
	rows, err := c.DB.Query(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func fileInfoStreamRowID(t *testing.T, c *catalogtest.Catalog, token string, index int64) int64 {
	t.Helper()
	ids := fileInfoTrackRowIDs(t, c, `SELECT rowid FROM asset_streams WHERE asset_id=? AND stream_index=?`, token, index)
	if len(ids) != 1 {
		t.Fatalf("stream %d rows %v", index, ids)
	}
	return ids[0]
}

func fileInfoSubtitleRowID(t *testing.T, c *catalogtest.Catalog, id string) int64 {
	t.Helper()
	ids := fileInfoTrackRowIDs(t, c, `SELECT rowid FROM asset_subtitles WHERE id=?`, id)
	if len(ids) != 1 {
		t.Fatalf("subtitle %q rows %v", id, ids)
	}
	return ids[0]
}

func TestFileInfoTrackSourceRawAudioMatchesStoredStreamOrder(t *testing.T) {
	c, movie, size, modified, check := newFileInfoTrackFixture(t)
	insertFileInfoTrackAudio(t, c, movie, size, modified)
	// The oracle is the exact title_files_batch audio order: stream_index ASC.
	want := fileInfoTrackRowIDs(t, c, `SELECT rowid FROM asset_streams WHERE asset_id=? AND type='audio' ORDER BY stream_index`, movie.Token)
	if len(want) != 5 {
		t.Fatalf("fixture audio rows %v", want)
	}
	read := func(spec fileInfoOrderedSpec) fileInfoOrderedSummary {
		t.Helper()
		source, err := newFileInfoTrackSource(c.DB, movie.Asset, size, modified, fileInfoLaneAudio, 64, check)
		if err != nil {
			t.Fatal(err)
		}
		page, err := orderedInfoTestRun(t, source, spec)
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	page := read(fileInfoOrderedSpec{Limit: 100, Direction: fileInfoForward})
	if page.Total != int64(len(want)) || page.Eligible != 0 || len(page.Occurrences) != len(want) {
		t.Fatalf("raw audio totals %+v", page)
	}
	// Raw lanes claim no playback offer, so every occurrence stays ineligible
	// even though the raw rows themselves are complete.
	got := make([]int64, 0, len(page.Occurrences))
	for _, occurrence := range page.Occurrences {
		if occurrence.Asset != movie.Asset {
			t.Fatalf("foreign asset in occurrence %+v", occurrence)
		}
		got = append(got, occurrence.Item)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stored audio order %v want %v", got, want)
	}
	wantPage := func(lo, hi int) []fileInfoOccurrence {
		out := make([]fileInfoOccurrence, 0, hi-lo)
		for _, rowid := range want[lo:hi] {
			out = append(out, fileInfoOccurrence{Item: rowid, Asset: movie.Asset})
		}
		return out
	}
	for _, tc := range []struct {
		name      string
		direction byte
		anchor    int
		lo, hi    int
		earlier   bool
		later     bool
	}{
		{"initial", fileInfoForward, -1, 0, 2, false, true},
		{"after-middle", fileInfoForward, 1, 2, 4, true, true},
		{"after-last", fileInfoForward, 4, 0, 0, true, false},
		{"before-middle", fileInfoBackward, 2, 0, 2, false, true},
		{"before-last", fileInfoBackward, 4, 2, 4, true, true},
		{"before-first", fileInfoBackward, 0, 0, 0, false, true},
		{"reload-middle", fileInfoReload, 2, 2, 4, true, true},
		{"reload-last", fileInfoReload, 4, 4, 5, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := fileInfoOrderedSpec{Limit: 2, Direction: tc.direction}
			if tc.anchor >= 0 {
				spec.HasAnchor, spec.Anchor = true, fileInfoOccurrence{Item: want[tc.anchor], Asset: movie.Asset}
			}
			page := read(spec)
			wantFirst := int64(0)
			if tc.hi > tc.lo {
				wantFirst = int64(tc.lo + 1)
			}
			if !reflect.DeepEqual(page.Occurrences, wantPage(tc.lo, tc.hi)) || page.FirstOrdinal != wantFirst || page.Earlier != tc.earlier || page.Later != tc.later || page.Total != 5 {
				t.Fatalf("page %+v want %v first=%d earlier=%t later=%t total=5", page, wantPage(tc.lo, tc.hi), wantFirst, tc.earlier, tc.later)
			}
		})
	}
	// Every raw stream row is one event, with no type filter before LIMIT. The
	// rejected video stream and the rejected subtitle stream both consume an
	// event in index order, and the events that match are exactly the stored
	// audio order above.
	source, err := newFileInfoTrackSource(c.DB, movie.Asset, size, modified, fileInfoLaneAudio, 64, check)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = source.Check(ctx); err != nil {
		t.Fatal(err)
	}
	all := fileInfoTrackRowIDs(t, c, `SELECT rowid FROM asset_streams WHERE asset_id=? ORDER BY stream_index`, movie.Token)
	if len(all) != 7 {
		t.Fatalf("fixture lane rows %v", all)
	}
	video := fileInfoStreamRowID(t, c, movie.Token, 0)
	subtitleStream := fileInfoStreamRowID(t, c, movie.Token, 7)
	if video == subtitleStream {
		t.Fatal("fixture cannot distinguish the rejected raw streams")
	}
	matched := make([]int64, 0, len(want))
	for i := range all {
		row, exists, err := source.Next(ctx)
		if err != nil || !exists {
			t.Fatalf("raw event %d: %v %t", i, err, exists)
		}
		wantMatch := all[i] != video && all[i] != subtitleStream
		if row.Sequence != int64(i+1) || row.Occurrence.Item != all[i] || row.Occurrence.Asset != movie.Asset || row.Match != wantMatch {
			t.Fatalf("raw event %d: %+v", i, row)
		}
		if row.Match {
			matched = append(matched, row.Occurrence.Item)
		}
	}
	if !reflect.DeepEqual(matched, want) {
		t.Fatalf("filtered raw matches %v want %v", matched, want)
	}
	if len(matched) != len(want) {
		t.Fatalf("rejected streams changed the accepted audio set %v", matched)
	}
	if row, exists, err := source.Next(ctx); err != nil || exists {
		t.Fatalf("raw lane did not end: %+v %t %v", row, exists, err)
	}
}

func TestFileInfoTrackSourceRawSubtitlesMatchStoredOriginOrder(t *testing.T) {
	c, movie, size, modified, check := newFileInfoTrackFixture(t)
	// The fact status stays 'unknown': raw subtitles are never filtered by
	// status, reason or personal offer.
	c.Exec(`INSERT OR IGNORE INTO asset_subtitle_facts(asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) VALUES(?,1,?,?,0,0,'unknown','track-fixture')`, movie.Token, size, modified)
	c.Exec(`INSERT INTO subtitle_sidecars(id,path,size,modified_ns,digest) VALUES('track-side-a','/tracks/a.srt',10,1,'digest')`)
	c.Exec(`INSERT INTO subtitle_sidecars(id,path,size,modified_ns,digest) VALUES('track-side-z','/tracks/z.srt',10,1,'digest')`)
	// A legal long text id proves the walk keeps no scalar anchor: only the
	// numeric ROWID survives in memory.
	long := "track-z-" + strings.Repeat("q", 4096)
	embedded9 := movie.Token + "-embedded-9"
	embedded3 := movie.Token + "-embedded-3"
	c.Exec(`INSERT INTO asset_subtitles(id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason) VALUES(?,?,'sidecar','z.srt',-1,'track-side-z','srt','en','Sidecar Z',0,0,'')`, long, movie.Token)
	c.Exec(`INSERT INTO asset_subtitles(id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason) VALUES(?,?,'embedded','9',9,NULL,'srt','en','Embedded Nine',0,0,'')`, embedded9, movie.Token)
	c.Exec(`INSERT INTO asset_subtitles(id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason) VALUES(?,?,'sidecar','a.srt',-1,'track-side-a','srt','en','Sidecar A',0,0,'')`, "track-s-a", movie.Token)
	c.Exec(`INSERT INTO asset_subtitles(id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason) VALUES(?,?,'embedded','3',3,NULL,'ass','en','Embedded Three',0,0,'')`, embedded3, movie.Token)
	// The oracle is the exact title_files_batch subtitle order:
	// origin ASC, stream_index ASC, id BINARY ASC.
	want := fileInfoTrackRowIDs(t, c, `SELECT rowid FROM asset_subtitles WHERE asset_id=? ORDER BY origin,stream_index,id`, movie.Token)
	if len(want) != 4 {
		t.Fatalf("fixture subtitle rows %v", want)
	}
	rowidOrder := fileInfoTrackRowIDs(t, c, `SELECT rowid FROM asset_subtitles WHERE asset_id=? ORDER BY rowid`, movie.Token)
	if reflect.DeepEqual(rowidOrder, want) {
		t.Fatal("fixture cannot distinguish stored order from ROWID order")
	}
	if fileInfoSubtitleRowID(t, c, embedded3) != want[0] || fileInfoSubtitleRowID(t, c, long) != want[3] {
		t.Fatalf("fixture stored order %v", want)
	}
	read := func(spec fileInfoOrderedSpec) fileInfoOrderedSummary {
		t.Helper()
		source, err := newFileInfoTrackSource(c.DB, movie.Asset, size, modified, fileInfoLaneSubtitles, 128, check)
		if err != nil {
			t.Fatal(err)
		}
		if got := source.AccountedBytes(); got != fileInfoTrackSourceBytes+128 {
			t.Fatalf("row/long-id dependent accounting %d", got)
		}
		page, err := orderedInfoTestRun(t, source, spec)
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	page := read(fileInfoOrderedSpec{Limit: 100, Direction: fileInfoForward})
	if page.Total != 4 || len(page.Occurrences) != 4 {
		t.Fatalf("raw subtitle totals %+v", page)
	}
	got := make([]int64, 0, 4)
	for _, occurrence := range page.Occurrences {
		got = append(got, occurrence.Item)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stored subtitle order %v want %v", got, want)
	}
	wantPage := func(lo, hi int) []fileInfoOccurrence {
		out := make([]fileInfoOccurrence, 0, hi-lo)
		for _, rowid := range want[lo:hi] {
			out = append(out, fileInfoOccurrence{Item: rowid, Asset: movie.Asset})
		}
		return out
	}
	for _, tc := range []struct {
		name      string
		direction byte
		anchor    int
		lo, hi    int
		earlier   bool
		later     bool
	}{
		{"initial", fileInfoForward, -1, 0, 2, false, true},
		{"after-middle", fileInfoForward, 1, 2, 4, true, false},
		{"before-middle", fileInfoBackward, 2, 0, 2, false, true},
		{"before-first", fileInfoBackward, 0, 0, 0, false, true},
		{"reload-middle", fileInfoReload, 1, 1, 3, true, true},
		{"reload-last", fileInfoReload, 3, 3, 4, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := fileInfoOrderedSpec{Limit: 2, Direction: tc.direction}
			if tc.anchor >= 0 {
				spec.HasAnchor, spec.Anchor = true, fileInfoOccurrence{Item: want[tc.anchor], Asset: movie.Asset}
			}
			page := read(spec)
			wantFirst := int64(0)
			if tc.hi > tc.lo {
				wantFirst = int64(tc.lo + 1)
			}
			if !reflect.DeepEqual(page.Occurrences, wantPage(tc.lo, tc.hi)) || page.FirstOrdinal != wantFirst || page.Earlier != tc.earlier || page.Later != tc.later || page.Total != 4 {
				t.Fatalf("page %+v want %v first=%d earlier=%t later=%t total=4", page, wantPage(tc.lo, tc.hi), wantFirst, tc.earlier, tc.later)
			}
		})
	}
	// Every raw subtitle row is one event, in the (asset_id,id) index walk.
	source, err := newFileInfoTrackSource(c.DB, movie.Asset, size, modified, fileInfoLaneSubtitles, 0, check)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	idOrder := fileInfoTrackRowIDs(t, c, `SELECT rowid FROM asset_subtitles WHERE asset_id=? ORDER BY id`, movie.Token)
	for i := range idOrder {
		row, exists, err := source.Next(ctx)
		if err != nil || !exists || !row.Match || row.Sequence != int64(i+1) || row.Occurrence.Item != idOrder[i] || row.Occurrence.Asset != movie.Asset {
			t.Fatalf("raw subtitle event %d: %+v %t %v", i, row, exists, err)
		}
	}
	if _, exists, err := source.Next(ctx); err != nil || exists {
		t.Fatalf("raw subtitle lane did not end: %t %v", exists, err)
	}
}

func TestFileInfoTrackSourceRejectsMismatchedParentFacts(t *testing.T) {
	c, movie, size, modified, check := newFileInfoTrackFixture(t)
	insertFileInfoTrackAudio(t, c, movie, size, modified)
	read := func(boundSize, boundModified int64) (fileInfoOrderedSummary, error) {
		source, err := newFileInfoTrackSource(c.DB, movie.Asset, boundSize, boundModified, fileInfoLaneAudio, 64, check)
		if err != nil {
			t.Fatal(err)
		}
		return orderedInfoTestRun(t, source, fileInfoOrderedSpec{Limit: 100, Direction: fileInfoForward})
	}
	page, err := read(size, modified)
	if err != nil || page.Total != 5 || len(page.Occurrences) != 5 {
		t.Fatalf("accepted parent %+v %v", page, err)
	}
	// The raw catalogue row is already NEW while the accepted OLD frame is
	// still usable. The update queued an OLD header (a phase-0 change row) but
	// did not pre-fence it, so the captured production head stays ready and the
	// lanes remain bound to the accepted technical parent facts.
	c.Exec(`UPDATE catalog_assets SET size=size+1,modified_ns=modified_ns+1 WHERE id=?`, movie.Asset)
	if err := check(context.Background()); err != nil {
		t.Fatalf("raw asset stat pre-fenced the accepted head: %v", err)
	}
	var oldHeaders int
	if err := c.DB.QueryRow(`SELECT count(*) FROM catalog_info_changes WHERE asset_id=? AND asset_token=? AND phase=0`, movie.Asset, movie.Token).Scan(&oldHeaders); err != nil || oldHeaders == 0 {
		t.Fatalf("raw asset stat did not queue an OLD header: %d %v", oldHeaders, err)
	}
	page, err = read(size, modified)
	if err != nil || page.Total != 5 || len(page.Occurrences) != 5 {
		t.Fatalf("accepted OLD parent after raw stat churn %+v %v", page, err)
	}
	// The accepted stat alone is not a parent: without a matching stored
	// technical fact row the fact point is NULL and the raw lane is empty rather
	// than guessing OLD rows out of the change tables.
	page, err = read(size+1, modified+1)
	if err != nil || page.Total != 0 || len(page.Occurrences) != 0 || page.Earlier || page.Later {
		t.Fatalf("accepted stat without a technical parent claimed rows %+v %v", page, err)
	}
	// The stored technical parent facts are the binding. Neither half of the
	// pair is weakenable: a mismatched size, then a mismatched modification,
	// both empty the lane, and the exact pair restores it.
	c.Exec(`UPDATE asset_stream_facts SET size=size+1 WHERE asset_id=?`, movie.Token)
	page, err = read(size+1, modified+1)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 || len(page.Occurrences) != 0 {
		t.Fatalf("mismatched technical size claimed rows %+v", page)
	}
	c.Exec(`UPDATE asset_stream_facts SET modified_ns=modified_ns+1 WHERE asset_id=?`, movie.Token)
	page, err = read(size+1, modified+1)
	if err != nil || page.Total != 5 || len(page.Occurrences) != 5 {
		t.Fatalf("matching technical parent %+v %v", page, err)
	}
	c.Exec(`UPDATE asset_stream_facts SET modified_ns=modified_ns+1 WHERE asset_id=?`, movie.Token)
	page, err = read(size+1, modified+1)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 || len(page.Occurrences) != 0 {
		t.Fatalf("mismatched technical modification claimed rows %+v", page)
	}
	page, err = read(size+1, modified+2)
	if err != nil || page.Total != 5 {
		t.Fatalf("live technical parent %+v %v", page, err)
	}
}

func TestFileInfoTrackSourceBindsExactInt64ParentFacts(t *testing.T) {
	c, movie, size, modified, check := newFileInfoTrackFixture(t)
	insertFileInfoTrackAudio(t, c, movie, size, modified)
	if size <= 0 {
		t.Fatalf("fixture cannot negate size %d", size)
	}
	// The schema stores both scalars as int64 and the accepted InfoAsset carries
	// the same domain, so a legal negative size or modification is an exact
	// parent binding rather than an invalid construction. int64Min must compare
	// as itself, not clamp or collide with its neighbour.
	negSize, negModified := -size, int64(math.MinInt64)
	c.Exec(`UPDATE asset_stream_facts SET size=?,modified_ns=? WHERE asset_id=?`, negSize, negModified, movie.Token)
	source, err := newFileInfoTrackSource(c.DB, movie.Asset, negSize, negModified, fileInfoLaneAudio, 64, check)
	if err != nil {
		t.Fatal(err)
	}
	page, err := orderedInfoTestRun(t, source, fileInfoOrderedSpec{Limit: 100, Direction: fileInfoForward})
	if err != nil || page.Total != 5 || len(page.Occurrences) != 5 {
		t.Fatalf("exact negative parent facts %+v %v", page, err)
	}
	source, err = newFileInfoTrackSource(c.DB, movie.Asset, negSize, negModified+1, fileInfoLaneAudio, 0, check)
	if err != nil {
		t.Fatal(err)
	}
	page, err = orderedInfoTestRun(t, source, fileInfoOrderedSpec{Limit: 100, Direction: fileInfoForward})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 || len(page.Occurrences) != 0 {
		t.Fatalf("neighbouring modification accepted as exact parent %+v", page)
	}
}

func TestFileInfoTrackSourceFailsMissingAnchorAndEndsEmptyLane(t *testing.T) {
	c, movie, size, modified, check := newFileInfoTrackFixture(t)
	insertFileInfoTrackAudio(t, c, movie, size, modified)
	ctx := context.Background()
	source, err := newFileInfoTrackSource(c.DB, movie.Asset, size, modified, fileInfoLaneAudio, 64, check)
	if err != nil {
		t.Fatal(err)
	}
	if err = source.Check(ctx); err != nil {
		t.Fatal(err)
	}
	row, exists, err := source.Next(ctx)
	if err != nil || !exists || !row.Match {
		t.Fatalf("first raw event %+v %t %v", row, exists, err)
	}
	// A child removed under the walk fails the live check through the reducer;
	// it is never a silent restart from the current first row.
	c.Exec(`DELETE FROM asset_streams WHERE rowid=?`, row.Occurrence.Item)
	if _, _, err = source.Next(ctx); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("removed raw anchor: %v", err)
	}
	// An empty lane ends with no event and no error, without a rowid-0 row.
	var library int64
	if err := c.DB.QueryRow(`SELECT library_id FROM catalog_entities WHERE id=?`, movie.ID).Scan(&library); err != nil {
		t.Fatal(err)
	}
	emp := c.Movie(library, "/tracks/Empty.mkv", "Empty", 2026)
	var emptySize, emptyModified int64
	if err := c.DB.QueryRow(`SELECT size,modified_ns FROM catalog_assets WHERE id=?`, emp.Asset).Scan(&emptySize, &emptyModified); err != nil {
		t.Fatal(err)
	}
	permissive := func(context.Context) error { return nil }
	for _, lane := range []byte{fileInfoLaneAudio, fileInfoLaneSubtitles} {
		empty, err := newFileInfoTrackSource(c.DB, emp.Asset, emptySize, emptyModified, lane, 0, permissive)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists, err := empty.Next(ctx); err != nil || exists {
			t.Fatalf("lane %d empty start %t %v", lane, exists, err)
		}
	}
}

func TestFileInfoTrackSourceRejectsInvalidConstruction(t *testing.T) {
	c, movie, size, modified, check := newFileInfoTrackFixture(t)
	// size and modified have no invented lower bound: int64 negatives are legal
	// and are exercised by TestFileInfoTrackSourceBindsExactInt64ParentFacts.
	for _, tc := range []struct {
		name     string
		db       *sql.DB
		asset    int64
		size     int64
		modified int64
		lane     byte
		capture  int64
		check    fileInfoBasisCheck
	}{
		{"no database", nil, movie.Asset, size, modified, fileInfoLaneAudio, 0, check},
		{"no asset", c.DB, 0, size, modified, fileInfoLaneAudio, 0, check},
		{"negative capture", c.DB, movie.Asset, size, modified, fileInfoLaneAudio, -1, check},
		{"files lane", c.DB, movie.Asset, size, modified, fileInfoLaneFiles, 0, check},
		{"disc lane", c.DB, movie.Asset, size, modified, fileInfoLaneDisc, 0, check},
		{"no check", c.DB, movie.Asset, size, modified, fileInfoLaneAudio, 0, nil},
	} {
		source, err := newFileInfoTrackSource(tc.db, tc.asset, tc.size, tc.modified, tc.lane, tc.capture, tc.check)
		if !errors.Is(err, readprep.ErrInvalid) || source != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
	source, err := newFileInfoTrackSource(c.DB, movie.Asset, size, modified, fileInfoLaneSubtitles, 0, check)
	if err != nil {
		t.Fatal(err)
	}
	if got := source.AccountedBytes(); got != fileInfoTrackSourceBytes {
		t.Fatalf("fixed accounting %d", got)
	}
}

func TestFileInfoTrackSourcePlansSeekRawRowsWithoutSort(t *testing.T) {
	c, movie, size, modified, _ := newFileInfoTrackFixture(t)
	insertFileInfoTrackAudio(t, c, movie, size, modified)
	c.Exec(`INSERT OR IGNORE INTO asset_subtitle_facts(asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) VALUES(?,1,?,?,0,0,'unknown','track-fixture')`, movie.Token, size, modified)
	c.Exec(`INSERT INTO asset_subtitles(id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason) VALUES(?,?,'embedded','3',3,NULL,'ass','en','Embedded Three',0,0,'')`, movie.Token+"-embedded-3", movie.Token)
	c.Exec(`INSERT INTO asset_subtitles(id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason) VALUES(?,?,'embedded','9',9,NULL,'srt','en','Embedded Nine',0,0,'')`, movie.Token+"-embedded-9", movie.Token)
	audio := fileInfoTrackRowIDs(t, c, `SELECT rowid FROM asset_streams WHERE asset_id=? ORDER BY stream_index`, movie.Token)
	subtitles := fileInfoTrackRowIDs(t, c, `SELECT rowid FROM asset_subtitles WHERE asset_id=? ORDER BY id`, movie.Token)
	if len(audio) < 2 || len(subtitles) < 2 {
		t.Fatalf("fixture rows audio=%v subtitles=%v", audio, subtitles)
	}
	// The raw first/seek statements stay indexed row lookups and the parent
	// binding is a primary-key point on the technical fact row. The anchor
	// probe is a separate constant point statement, so Next may run two, but
	// neither plan sorts or scans a table.
	for _, tc := range []struct {
		name  string
		query string
		args  []any
		want  []string
	}{
		{"audio-first", fileInfoTrackAudioFirst, []any{size, modified, movie.Asset}, []string{"SEARCH s USING", "asset_streams", "asset_id=?", "asset_stream_facts"}},
		{"audio-seek", fileInfoTrackAudioSeek, []any{size, modified, movie.Asset, audio[0]}, []string{"SEARCH s USING", "asset_streams", "stream_index>?", "asset_stream_facts"}},
		{"audio-anchor", fileInfoTrackAudioAnchor, []any{audio[0], movie.Asset}, []string{"SEARCH asset_streams USING INTEGER PRIMARY KEY"}},
		{"subtitle-first", fileInfoTrackSubtitleFirst, []any{size, modified, movie.Asset}, []string{"SEARCH s USING", "asset_subtitles_asset", "asset_id=?", "asset_subtitle_facts"}},
		{"subtitle-seek", fileInfoTrackSubtitleSeek, []any{size, modified, movie.Asset, subtitles[0]}, []string{"SEARCH s USING", "asset_subtitles_asset", "id>?", "asset_subtitle_facts"}},
		{"subtitle-anchor", fileInfoTrackSubtitleAnchor, []any{subtitles[0], movie.Asset}, []string{"SEARCH asset_subtitles USING INTEGER PRIMARY KEY"}},
		{"audio-compare", fileInfoTrackAudioCompare, []any{movie.Asset, audio[0], movie.Asset, audio[len(audio)-1]}, []string{"SEARCH asset_streams USING INTEGER PRIMARY KEY"}},
		{"subtitle-compare", fileInfoTrackSubtitleCompare, []any{movie.Asset, subtitles[0], movie.Asset, subtitles[len(subtitles)-1]}, []string{"SEARCH asset_subtitles USING INTEGER PRIMARY KEY"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := c.DB.Query(`EXPLAIN QUERY PLAN `+tc.query, tc.args...)
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
				t.Fatalf("unbounded plan:\n%s", joined)
			}
			for _, want := range tc.want {
				if !strings.Contains(joined, want) {
					t.Fatalf("plan missing %q:\n%s", want, joined)
				}
			}
		})
	}
}
