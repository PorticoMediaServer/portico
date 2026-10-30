package catalog

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// fileInfoHeadSubtitleRowFixture is one raw subtitle_resources row plus the
// current revision and render row a real offer would have. It deliberately
// allows a current revision without that revision row, a foreign item, a
// deleted row and a disc-scoped row so the point lane can prove each rejection.
type fileInfoHeadSubtitleRowFixture struct {
	id                     string
	asset                  string
	item                   int64
	scope, owner           string
	current                int64
	size, modified         int64
	deleted                int
	origin, format         string
	language, title        string
	renderer               string
	defaulted, forced, sdh bool
}

func insertFileInfoHeadSubtitle(t *testing.T, c *catalogtest.Catalog, f fileInfoHeadSubtitleRowFixture) {
	t.Helper()
	c.Exec(`INSERT INTO subtitle_resources(id,item_id,source_id,scope,owner,current_revision,deleted,discovered,discovery_id,discovery_revision) VALUES(?,?,?,?,?,?,?,0,'',0)`, f.id, f.item, f.asset, f.scope, f.owner, f.current, f.deleted)
	c.Exec(`INSERT INTO subtitle_revisions(resource_id,revision,digest,size,format,language,title,origin,rights,original_digest,source_size,source_modified_ns,source_facts_revision,created_at) VALUES(?,1,'digest',0,?,?,?,?,'','',?,?,1,'2026-01-01T00:00:00Z')`, f.id, f.format, f.language, f.title, f.origin, f.size, f.modified)
	c.Exec(`INSERT INTO subtitle_render_revisions(resource_id,revision,renderer,is_default,is_forced,source_evidence,hearing_impaired) VALUES(?,1,?,?,?,'',?)`, f.id, f.renderer, f.defaulted, f.forced, f.sdh)
}

func fileInfoHeadRowID(t *testing.T, c *catalogtest.Catalog, id string) int64 {
	t.Helper()
	ids := fileInfoTrackRowIDs(t, c, `SELECT rowid FROM subtitle_resources WHERE id=?`, id)
	if len(ids) != 1 {
		t.Fatalf("resource %q rows %v", id, ids)
	}
	return ids[0]
}

func fileInfoHeadOccurrences(c *catalogtest.Catalog, asset int64, ids ...string) []fileInfoOccurrence {
	out := make([]fileInfoOccurrence, 0, len(ids))
	for _, id := range ids {
		var rowid int64
		if err := c.DB.QueryRow(`SELECT rowid FROM subtitle_resources WHERE id=?`, id).Scan(&rowid); err != nil {
			c.T.Fatal(err)
		}
		out = append(out, fileInfoOccurrence{Item: rowid, Asset: asset})
	}
	return out
}

// TestFileInfoHeadTrackSourceAudioOffersRawAudioOrder proves the offered audio
// lane is the existing raw audio adapter: every stored audio stream of the
// group head is offered in stream_index order, every non-audio stream is a
// consumed reject, and the page totals describe the offered set rather than the
// raw table.
func TestFileInfoHeadTrackSourceAudioOffersRawAudioOrder(t *testing.T) {
	c, movie, size, modified, check := newFileInfoTrackFixture(t)
	insertFileInfoTrackAudio(t, c, movie, size, modified)
	want := fileInfoTrackRowIDs(t, c, `SELECT rowid FROM asset_streams WHERE asset_id=? AND type='audio' ORDER BY stream_index`, movie.Token)
	if len(want) != 5 {
		t.Fatalf("fixture audio rows %v", want)
	}
	read := func(spec fileInfoOrderedSpec) fileInfoOrderedSummary {
		t.Helper()
		source, err := newFileInfoHeadTrackSource(c.DB, movie.ID, movie.Asset, size, modified, fileInfoLaneAudio, "actor-a", 64, check)
		if err != nil {
			t.Fatal(err)
		}
		if got := source.AccountedBytes(); got != fileInfoTrackSourceBytes+64 {
			t.Fatalf("audio head accounting %d", got)
		}
		page, err := orderedInfoTestRun(t, source, spec)
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	page := read(fileInfoOrderedSpec{Limit: 100, Direction: fileInfoForward})
	if page.Total != int64(len(want)) || page.Eligible != int64(len(want)) || len(page.Occurrences) != len(want) {
		t.Fatalf("offered audio totals %+v", page)
	}
	got := make([]int64, 0, len(page.Occurrences))
	for _, occurrence := range page.Occurrences {
		if occurrence.Asset != movie.Asset {
			t.Fatalf("foreign asset in offered audio %+v", occurrence)
		}
		got = append(got, occurrence.Item)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("offered audio order %v want %v", got, want)
	}
	// A short page still walks the same raw lane, so a rejected stream before
	// the next audio row is an event instead of a skipped row.
	page = read(fileInfoOrderedSpec{Limit: 2, Direction: fileInfoForward})
	if !reflect.DeepEqual(page.Occurrences, []fileInfoOccurrence{{Item: want[0], Asset: movie.Asset}, {Item: want[1], Asset: movie.Asset}}) || page.Total != 5 || page.Eligible != 5 || page.Earlier || !page.Later {
		t.Fatalf("offered audio short page %+v", page)
	}
}

// TestFileInfoHeadTrackSourceSubtitlesOfferExactlyTheDefaultResources is the
// offered-subtitle oracle. The only rows that may appear are the head asset's
// current, undeleted, shared-or-owned, accepted-stat, default-disc resources;
// their order is resource id BINARY, not insertion order, raw subtitle order or
// rowid order. Personal resources that never had an asset_subtitles row are
// included, and a raw row that happens to share an id prefix is not consulted.
func TestFileInfoHeadTrackSourceSubtitlesOfferExactlyTheDefaultResources(t *testing.T) {
	c, movie, size, modified, check := newFileInfoTrackFixture(t)
	var library int64
	if err := c.DB.QueryRow(`SELECT library_id FROM catalog_entities WHERE id=?`, movie.ID).Scan(&library); err != nil {
		t.Fatal(err)
	}
	other := c.Movie(library, "/tracks/Other.mkv", "Other", 2026)
	long := "Head " + strings.Repeat("z", 4096)
	// Insertion order differs from resource id order on purpose.
	rows := []fileInfoHeadSubtitleRowFixture{
		{id: "head-z", asset: movie.Token, item: movie.ID, scope: "shared", current: 1, size: size, modified: modified, origin: "embedded", format: "srt", language: "en", title: "Offered Z", renderer: "external_text"},
		{id: "head-p", asset: movie.Token, item: movie.ID, scope: "personal", owner: "actor-a", current: 1, size: size, modified: modified, origin: "upload", format: "srt", language: "en", title: "Personal A", renderer: "external_text"},
		{id: "head-q", asset: movie.Token, item: movie.ID, scope: "personal", owner: "actor-b", current: 1, size: size, modified: modified, origin: "upload", format: "vtt", language: "en", title: "Personal B", renderer: "external_text"},
		{id: "head-d", asset: movie.Token, item: movie.ID, scope: "shared", current: 1, size: size, modified: modified, deleted: 1, origin: "upload", format: "srt", language: "en", title: "Deleted", renderer: "external_text"},
		{id: "head-a", asset: movie.Token, item: movie.ID, scope: "shared", current: 1, size: size, modified: modified, origin: "embedded", format: "srt", language: "en", title: "Alpha", renderer: "external_text", defaulted: true},
		{id: "head-stat", asset: movie.Token, item: movie.ID, scope: "shared", current: 1, size: size + 1, modified: modified, origin: "upload", format: "srt", language: "en", title: "Stale Stat", renderer: "external_text"},
		{id: "head-rev", asset: movie.Token, item: movie.ID, scope: "shared", current: 2, size: size, modified: modified, origin: "upload", format: "srt", language: "en", title: "Missing Revision", renderer: "external_text"},
		{id: "head-item", asset: movie.Token, item: other.ID, scope: "shared", current: 1, size: size, modified: modified, origin: "upload", format: "srt", language: "en", title: "Other Item", renderer: "external_text"},
		{id: "head-disc", asset: movie.Token, item: movie.ID, scope: "shared", current: 1, size: size, modified: modified, origin: "embedded", format: "srt", language: "en", title: "Disc Default", renderer: "burn_in"},
		{id: "head-z0", asset: movie.Token, item: movie.ID, scope: "shared", current: 1, size: size, modified: modified, origin: "sidecar", format: "ass", language: "en", title: long, renderer: "burn_in", forced: true, sdh: true},
	}
	for _, row := range rows {
		insertFileInfoHeadSubtitle(t, c, row)
	}
	// A disc-scoped row without current inventory title facts is not the
	// default disc resource, exactly as listTx's private predicate decides.
	c.Exec(`INSERT INTO subtitle_disc_resources(resource_id,object_id,title_key,title_facts_revision,stream_index,source_revision,fingerprint,policy_revision,origin_us,timing_known) VALUES('head-disc','object','title','facts',0,'source','fingerprint',1,0,1)`)
	// Raw subtitle evidence is a separate lane. Its id deliberately shares the
	// "head-z" prefix with an offered resource and its title differs, so the
	// offered row must come from subtitle_resources, not the raw table. A second
	// raw row exists only in asset_subtitles and must never appear as offered.
	c.Exec(`INSERT OR IGNORE INTO asset_subtitle_facts(asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) VALUES(?,1,?,?,0,0,'unknown','head-fixture')`, movie.Token, size, modified)
	c.Exec(`INSERT INTO asset_subtitles(id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason) VALUES('head-z',?,'embedded','3',3,NULL,'srt','en','Raw Z Track',0,0,'')`, movie.Token)
	c.Exec(`INSERT INTO asset_subtitles(id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason) VALUES('raw-only',?,'embedded','4',4,NULL,'srt','en','Raw Only',0,0,'')`, movie.Token)
	// The id oracle is BINARY order over the whole table, filtered by the same
	// data rules the point lane applies.
	wantA := []string{"head-a", "head-p", "head-z", "head-z0"}
	wantB := []string{"head-a", "head-q", "head-z", "head-z0"}
	read := func(actor string, spec fileInfoOrderedSpec) fileInfoOrderedSummary {
		t.Helper()
		source, err := newFileInfoHeadTrackSource(c.DB, movie.ID, movie.Asset, size, modified, fileInfoLaneSubtitles, actor, 96, check)
		if err != nil {
			t.Fatal(err)
		}
		if got := source.AccountedBytes(); got != fileInfoHeadTrackSourceBytes+96 {
			t.Fatalf("subtitle head accounting %d", got)
		}
		page, err := orderedInfoTestRun(t, source, spec)
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	page := read("actor-a", fileInfoOrderedSpec{Limit: 100, Direction: fileInfoForward})
	want := fileInfoHeadOccurrences(c, movie.Asset, wantA...)
	if page.Total != int64(len(want)) || page.Eligible != int64(len(want)) || !reflect.DeepEqual(page.Occurrences, want) {
		t.Fatalf("offered subtitles %+v want %v", page, want)
	}
	// Personal rows are exactly the caller's own; the other viewer's row is
	// not an empty slot or a source-health flap.
	pageB := read("actor-b", fileInfoOrderedSpec{Limit: 100, Direction: fileInfoForward})
	wantBPage := fileInfoHeadOccurrences(c, movie.Asset, wantB...)
	if pageB.Total != int64(len(wantBPage)) || pageB.Eligible != int64(len(wantBPage)) || !reflect.DeepEqual(pageB.Occurrences, wantBPage) {
		t.Fatalf("other viewer subtitles %+v want %v", pageB, wantBPage)
	}
	// Pagination keeps the offered order while the raw ROWID walk visits every
	// resource row, including the rejected ones between the offered rows.
	wantPage := func(lo, hi int) []fileInfoOccurrence { return want[lo:hi] }
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
		{"after-last", fileInfoForward, 3, 0, 0, true, false},
		{"before-middle", fileInfoBackward, 2, 0, 2, false, true},
		{"before-first", fileInfoBackward, 0, 0, 0, false, true},
		{"reload-middle", fileInfoReload, 1, 1, 3, true, true},
		{"reload-last", fileInfoReload, 3, 3, 4, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := fileInfoOrderedSpec{Limit: 2, Direction: tc.direction}
			if tc.anchor >= 0 {
				spec.HasAnchor, spec.Anchor = true, want[tc.anchor]
			}
			page := read("actor-a", spec)
			wantFirst := int64(0)
			if tc.hi > tc.lo {
				wantFirst = int64(tc.lo + 1)
			}
			if !reflect.DeepEqual(page.Occurrences, wantPage(tc.lo, tc.hi)) || page.FirstOrdinal != wantFirst || page.Earlier != tc.earlier || page.Later != tc.later || page.Total != 4 {
				t.Fatalf("page %+v want %v first=%d earlier=%t later=%t total=4", page, wantPage(tc.lo, tc.hi), wantFirst, tc.earlier, tc.later)
			}
		})
	}
	// Every raw resource row is one event, in ROWID order, and the decision is
	// applied after the row exists. Nothing is filtered out of the walk.
	source, err := newFileInfoHeadTrackSource(c.DB, movie.ID, movie.Asset, size, modified, fileInfoLaneSubtitles, "actor-a", 0, check)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = source.Check(ctx); err != nil {
		t.Fatal(err)
	}
	all := fileInfoTrackRowIDs(t, c, `SELECT rowid FROM subtitle_resources WHERE source_id=? ORDER BY rowid`, movie.Token)
	if len(all) != len(rows) {
		t.Fatalf("fixture resource rows %d want %d", len(all), len(rows))
	}
	offered := map[int64]bool{}
	for _, occurrence := range want {
		offered[occurrence.Item] = true
	}
	matched := map[int64]bool{}
	for i, rowid := range all {
		row, exists, err := source.Next(ctx)
		if err != nil || !exists {
			t.Fatalf("raw resource event %d: %v %t", i, err, exists)
		}
		if row.Sequence != int64(i+1) || row.Occurrence.Item != rowid || row.Occurrence.Asset != movie.Asset || row.Match != offered[rowid] || row.Eligible != row.Match {
			t.Fatalf("raw resource event %d: %+v want rowid=%d match=%t", i, row, rowid, offered[rowid])
		}
		if row.Match {
			matched[rowid] = true
		}
	}
	if len(matched) != len(want) {
		t.Fatalf("offered matches %v want %v", matched, want)
	}
	for _, occurrence := range want {
		if !matched[occurrence.Item] {
			t.Fatalf("raw walk missed offered occurrence %+v", occurrence)
		}
	}
	if _, exists, err := source.Next(ctx); err != nil || exists {
		t.Fatalf("offered subtitle lane did not end: %t %v", exists, err)
	}
	// Compare is exact resource id BINARY order through two ROWID points. The
	// "head-z" prefix of "head-z0" is a real shorter id, not a prefix collapse.
	source, err = newFileInfoHeadTrackSource(c.DB, movie.ID, movie.Asset, size, modified, fileInfoLaneSubtitles, "actor-a", 0, check)
	if err != nil {
		t.Fatal(err)
	}
	z := fileInfoOccurrence{Item: fileInfoHeadRowID(t, c, "head-z"), Asset: movie.Asset}
	z0 := fileInfoOccurrence{Item: fileInfoHeadRowID(t, c, "head-z0"), Asset: movie.Asset}
	if cmp, err := source.Compare(ctx, z, z0); err != nil || cmp != -1 {
		t.Fatalf("prefix order %d %v", cmp, err)
	}
	if cmp, err := source.Compare(ctx, z0, z); err != nil || cmp != 1 {
		t.Fatalf("reverse prefix order %d %v", cmp, err)
	}
	if cmp, err := source.Compare(ctx, z, z); err != nil || cmp != 0 {
		t.Fatalf("same occurrence %d %v", cmp, err)
	}
	if _, err := source.Compare(ctx, z, fileInfoOccurrence{Item: z.Item, Asset: movie.Asset + 1}); !errors.Is(err, readprep.ErrInvalid) {
		t.Fatalf("foreign asset compare %v", err)
	}
	if _, err := source.Compare(ctx, z, fileInfoOccurrence{Item: 1 << 40, Asset: movie.Asset}); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("missing compare point %v", err)
	}
	// Hydration is the exact pointed resource: its resource title wins over the
	// raw row that shares the id, and the text cost covers the long title.
	track, err := fileInfoHeadSubtitleRow(ctx, c.DB, z.Item, movie.ID, movie.Asset, size, modified, "actor-a")
	if err != nil || track.ID != "head-z" || track.Title != "Offered Z" || track.Codec != "srt" || track.External {
		t.Fatalf("hydrated raw-prefix resource %+v %v", track, err)
	}
	alpha, err := fileInfoHeadSubtitleRow(ctx, c.DB, fileInfoHeadRowID(t, c, "head-a"), movie.ID, movie.Asset, size, modified, "actor-a")
	if err != nil || alpha.ID != "head-a" || alpha.Title != "Alpha" || !alpha.Default || alpha.External {
		t.Fatalf("hydrated default resource %+v %v", alpha, err)
	}
	sidecar, err := fileInfoHeadSubtitleRow(ctx, c.DB, z0.Item, movie.ID, movie.Asset, size, modified, "actor-a")
	if err != nil || sidecar.ID != "head-z0" || sidecar.Title != long || sidecar.Codec != "ass" || !sidecar.External || !sidecar.Forced || !sidecar.HearingImpaired {
		t.Fatalf("hydrated sidecar resource %+v %v", sidecar, err)
	}
	bytes, err := fileInfoHeadSubtitleTextBytes(ctx, c.DB, z0.Item, movie.ID, movie.Asset, size, modified, "actor-a")
	wantBytes := int64(len("head-z0") + len(movie.Token) + len("shared") + len("en") + len(long) + len("ass") + len("sidecar") + len("digest") + len("burn_in"))
	if err != nil || bytes != wantBytes || bytes <= int64(len(long)) {
		t.Fatalf("subtitle text bytes %d want %d %v", bytes, wantBytes, err)
	}
	// A personal row is not hydrated for another viewer.
	if _, err := fileInfoHeadSubtitleRow(ctx, c.DB, fileInfoHeadRowID(t, c, "head-q"), movie.ID, movie.Asset, size, modified, "actor-a"); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("foreign personal hydration %v", err)
	}
	if _, err := fileInfoHeadSubtitleTextBytes(ctx, c.DB, fileInfoHeadRowID(t, c, "head-q"), movie.ID, movie.Asset, size, modified, "actor-a"); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("foreign personal text bytes %v", err)
	}
	if _, err := fileInfoHeadSubtitleRow(ctx, c.DB, 1<<40, movie.ID, movie.Asset, size, modified, "actor-a"); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("missing hydration %v", err)
	}
	// A removed anchor is stale, never a restart from the current first row.
	if err = source.Check(ctx); err != nil {
		t.Fatal(err)
	}
	first, exists, err := source.Next(ctx)
	if err != nil || !exists {
		t.Fatalf("anchor fixture %+v %t %v", first, exists, err)
	}
	c.Exec(`DELETE FROM subtitle_render_revisions WHERE resource_id=(SELECT id FROM subtitle_resources WHERE rowid=?)`, first.Occurrence.Item)
	c.Exec(`DELETE FROM subtitle_revisions WHERE resource_id=(SELECT id FROM subtitle_resources WHERE rowid=?)`, first.Occurrence.Item)
	c.Exec(`DELETE FROM subtitle_resources WHERE rowid=?`, first.Occurrence.Item)
	if _, _, err = source.Next(ctx); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("removed offered anchor %v", err)
	}
}

// TestFileInfoHeadTrackSourceBindsAcceptedParentFactsNotRawAssetStat keeps the
// accepted OLD frame usable while the raw catalogue row is already NEW, and
// empties both lanes when the accepted stat has no matching technical/source
// row. The live head probe proves the raw change has not pre-fenced it.
func TestFileInfoHeadTrackSourceBindsAcceptedParentFactsNotRawAssetStat(t *testing.T) {
	c, movie, size, modified, check := newFileInfoTrackFixture(t)
	insertFileInfoTrackAudio(t, c, movie, size, modified)
	insertFileInfoHeadSubtitle(t, c, fileInfoHeadSubtitleRowFixture{id: "head-stat", asset: movie.Token, item: movie.ID, scope: "shared", current: 1, size: size, modified: modified, origin: "upload", format: "srt", language: "en", title: "Stat", renderer: "external_text"})
	read := func(boundSize, boundModified int64, lane byte) fileInfoOrderedSummary {
		t.Helper()
		source, err := newFileInfoHeadTrackSource(c.DB, movie.ID, movie.Asset, boundSize, boundModified, lane, "actor-a", 0, check)
		if err != nil {
			t.Fatal(err)
		}
		page, err := orderedInfoTestRun(t, source, fileInfoOrderedSpec{Limit: 100, Direction: fileInfoForward})
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	// The raw catalogue stat moves first; the accepted OLD frame is still the
	// binding for both the raw technical fact and the resource source stat.
	c.Exec(`UPDATE catalog_assets SET size=size+1,modified_ns=modified_ns+1 WHERE id=?`, movie.Asset)
	if err := check(context.Background()); err != nil {
		t.Fatalf("raw asset stat pre-fenced the accepted head: %v", err)
	}
	if page := read(size, modified, fileInfoLaneAudio); page.Total != 5 || page.Eligible != 5 {
		t.Fatalf("accepted OLD audio %+v", page)
	}
	if page := read(size, modified, fileInfoLaneSubtitles); page.Total != 1 || page.Eligible != 1 {
		t.Fatalf("accepted OLD subtitles %+v", page)
	}
	// The raw NEW stat is not the accepted parent: the technical/source facts
	// do not match it, so the lanes are truthfully empty instead of adopting it.
	if page := read(size+1, modified+1, fileInfoLaneAudio); page.Total != 0 || page.Eligible != 0 {
		t.Fatalf("raw NEW audio %+v", page)
	}
	if page := read(size+1, modified+1, fileInfoLaneSubtitles); page.Total != 0 || page.Eligible != 0 {
		t.Fatalf("raw NEW subtitles %+v", page)
	}
}

// TestFileInfoHeadTrackSourceDoesNotFlapOnSourceHealth proves the offered lane
// does not consult library-source health or object state. The parent admitted
// one exact asset; inventory churn underneath it must not withdraw a personal or
// shared subtitle offer on a later FileInfo read.
func TestFileInfoHeadTrackSourceDoesNotFlapOnSourceHealth(t *testing.T) {
	c, movie, size, modified, check := newFileInfoTrackFixture(t)
	c.Exec(`INSERT INTO library_sources(id,library_id,kind,name,configured_root,root) VALUES('head-source','tracks','local','Heads','/track-head','/track-head')`)
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('head-object','head-source',?,'inc','Film.mkv','rev','{}',?,?)`, movie.Token, size, modified)
	insertFileInfoHeadSubtitle(t, c, fileInfoHeadSubtitleRowFixture{id: "head-health", asset: movie.Token, item: movie.ID, scope: "shared", current: 1, size: size, modified: modified, origin: "upload", format: "srt", language: "en", title: "Health", renderer: "external_text"})
	read := func() fileInfoOrderedSummary {
		t.Helper()
		source, err := newFileInfoHeadTrackSource(c.DB, movie.ID, movie.Asset, size, modified, fileInfoLaneSubtitles, "actor-a", 0, check)
		if err != nil {
			t.Fatal(err)
		}
		page, err := orderedInfoTestRun(t, source, fileInfoOrderedSpec{Limit: 100, Direction: fileInfoForward})
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	if page := read(); page.Total != 1 {
		t.Fatalf("initial offer %+v", page)
	}
	c.Exec(`UPDATE library_sources SET health='root_changed',enabled=0 WHERE id='head-source'`)
	c.Exec(`UPDATE inventory_objects SET state='unavailable',retired=1 WHERE id='head-object'`)
	if page := read(); page.Total != 1 || page.Eligible != 1 {
		t.Fatalf("source health changed the offer %+v", page)
	}
}

// TestFileInfoHeadTrackSourceSubtitlesEndsEmptyLane covers an asset with no raw
// resources and the empty-subtitle construction. Neither lane invents a row or
// an error at the end.
func TestFileInfoHeadTrackSourceSubtitlesEndsEmptyLane(t *testing.T) {
	c, _, _, _, _ := newFileInfoTrackFixture(t)
	var library int64
	if err := c.DB.QueryRow(`SELECT library_id FROM catalog_entities LIMIT 1`).Scan(&library); err != nil {
		t.Fatal(err)
	}
	empty := c.Movie(library, "/tracks/HeadEmpty.mkv", "Head Empty", 2026)
	var emptySize, emptyModified int64
	if err := c.DB.QueryRow(`SELECT size,modified_ns FROM catalog_assets WHERE id=?`, empty.Asset).Scan(&emptySize, &emptyModified); err != nil {
		t.Fatal(err)
	}
	permissive := func(context.Context) error { return nil }
	ctx := context.Background()
	for _, lane := range []byte{fileInfoLaneAudio, fileInfoLaneSubtitles} {
		source, err := newFileInfoHeadTrackSource(c.DB, empty.ID, empty.Asset, emptySize, emptyModified, lane, "actor-a", 0, permissive)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists, err := source.Next(ctx); err != nil || exists {
			t.Fatalf("lane %d empty start %t %v", lane, exists, err)
		}
	}
}

func TestFileInfoHeadTrackSourceRejectsInvalidConstruction(t *testing.T) {
	c, movie, size, modified, check := newFileInfoTrackFixture(t)
	for _, tc := range []struct {
		name     string
		db       *sql.DB
		item     int64
		asset    int64
		size     int64
		modified int64
		lane     byte
		actor    string
		capture  int64
		check    fileInfoBasisCheck
	}{
		{"no database", nil, movie.ID, movie.Asset, size, modified, fileInfoLaneAudio, "actor-a", 0, check},
		{"no item", c.DB, 0, movie.Asset, size, modified, fileInfoLaneAudio, "actor-a", 0, check},
		{"no asset", c.DB, movie.ID, 0, size, modified, fileInfoLaneAudio, "actor-a", 0, check},
		{"negative capture", c.DB, movie.ID, movie.Asset, size, modified, fileInfoLaneAudio, "actor-a", -1, check},
		{"files lane", c.DB, movie.ID, movie.Asset, size, modified, fileInfoLaneFiles, "actor-a", 0, check},
		{"disc lane", c.DB, movie.ID, movie.Asset, size, modified, fileInfoLaneDisc, "actor-a", 0, check},
		{"no actor", c.DB, movie.ID, movie.Asset, size, modified, fileInfoLaneSubtitles, "", 0, check},
		{"no check", c.DB, movie.ID, movie.Asset, size, modified, fileInfoLaneSubtitles, "actor-a", 0, nil},
	} {
		source, err := newFileInfoHeadTrackSource(tc.db, tc.item, tc.asset, tc.size, tc.modified, tc.lane, tc.actor, tc.capture, tc.check)
		if !errors.Is(err, readprep.ErrInvalid) || source != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
	// Legal negative accepted scalars remain exact, as in the raw adapter.
	source, err := newFileInfoHeadTrackSource(c.DB, movie.ID, movie.Asset, -size, -modified, fileInfoLaneSubtitles, "actor-a", 0, check)
	if err != nil {
		t.Fatal(err)
	}
	if got := source.AccountedBytes(); got != fileInfoHeadTrackSourceBytes {
		t.Fatalf("fixed accounting %d", got)
	}
}

func TestFileInfoHeadTrackSourcePlansPointRowsWithoutSort(t *testing.T) {
	c, movie, size, modified, _ := newFileInfoTrackFixture(t)
	insertFileInfoHeadSubtitle(t, c, fileInfoHeadSubtitleRowFixture{id: "head-a", asset: movie.Token, item: movie.ID, scope: "shared", current: 1, size: size, modified: modified, origin: "upload", format: "srt", language: "en", title: "Plan A", renderer: "external_text"})
	insertFileInfoHeadSubtitle(t, c, fileInfoHeadSubtitleRowFixture{id: "head-b", asset: movie.Token, item: movie.ID, scope: "shared", current: 1, size: size, modified: modified, origin: "upload", format: "srt", language: "en", title: "Plan B", renderer: "external_text"})
	a := fileInfoHeadRowID(t, c, "head-a")
	b := fileInfoHeadRowID(t, c, "head-b")
	for _, tc := range []struct {
		name  string
		query string
		args  []any
		want  []string
	}{
		{"first", fileInfoHeadSubtitleFirst, []any{movie.Asset}, []string{"SEARCH r USING COVERING INDEX subtitle_resources_source_id_fk", "source_id=?"}},
		{"seek", fileInfoHeadSubtitleSeek, []any{movie.Asset, a}, []string{"SEARCH r USING COVERING INDEX subtitle_resources_source_id_fk", "rowid>?"}},
		{"anchor", fileInfoHeadSubtitleAnchor, []any{a, movie.Asset}, []string{"SEARCH subtitle_resources USING INTEGER PRIMARY KEY"}},
		{"compare", fileInfoHeadSubtitleCompare, []any{a, movie.Asset, b, movie.Asset}, []string{"SEARCH subtitle_resources USING INTEGER PRIMARY KEY"}},
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
			if strings.Contains(joined, "TEMP") || strings.Contains(joined, "SCAN subtitle_resources") {
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
