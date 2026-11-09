package read

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

//go:embed rb_group_term_small_test.go
var rbTermJournal string

func rbTermHash(t *testing.T) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(readfixture.BrowseHash(t)+rbTermJournal)))
}
func rbTermSource(t *testing.T) *browseFixture {
	f := newBrowseSource(t)
	f.exec(t, `DELETE FROM catalog_entities WHERE id>8`)
	f.exec(t, `UPDATE catalog_entities SET retired=0,year=1999`)
	f.exec(t, `UPDATE catalog_entities SET year=1800 WHERE id=1`)
	f.exec(t, `UPDATE catalog_entities SET year=2199 WHERE id=2`)
	f.exec(t, `UPDATE catalog_entities SET year=0 WHERE id=3`)
	f.exec(t, `UPDATE catalog_entities SET year=2200 WHERE id=4`)
	f.exec(t, `UPDATE catalog_entities SET year=1799 WHERE id=5`)
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=8`)
	f.exec(t, `INSERT INTO catalog_terms(id,vocab,key,label) VALUES(1,1,'rock','The Rock'),(2,1,'jazz','Jazz'),(3,1,'orphan','Orphan'),(4,2,'wrong','Wrong vocabulary')`)
	f.exec(t, `INSERT INTO catalog_entity_terms(entity_id,term_id) VALUES(1,1),(1,2),(1,3),(1,4),(2,1),(3,1),(8,2)`)
	f.exec(t, `INSERT INTO catalog_term_sources(entity_id,term_id,provider,source_id,label_override) VALUES(1,1,'owner','a',NULL),(1,1,'owner','b',NULL),(1,2,'owner','c',''),(1,4,'owner','d',NULL),(2,1,'owner','a','Alt Rock'),(3,1,'owner','a','The Rock'),(8,2,'owner','a',NULL)`)
	return f
}
func rbTermMusic(t *testing.T, f *browseFixture) {
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(5,'artist',0,1,1,1,1),(6,'album',0,1,1,1,1),(7,'song',1,0,1,1,1)`)
	f.exec(t, "BEGIN")
	f.exec(t, "PRAGMA defer_foreign_keys=ON")
	f.exec(t, `UPDATE catalog_subjects SET kind=5 WHERE id=9`)
	f.exec(t, `UPDATE catalog_subjects SET kind=6 WHERE id=10`)
	f.exec(t, `UPDATE catalog_subjects SET kind=7 WHERE id<=8`)
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) SELECT 9,public_id,library_id,5,'Artist','','','','',0,public_id,public_id FROM catalog_subjects WHERE id=9`)
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,artist_id) SELECT 10,public_id,library_id,6,'Album','','','','',0,public_id,public_id,9 FROM catalog_subjects WHERE id=10`)
	f.exec(t, `UPDATE catalog_entities SET kind=7,album_id=10 WHERE id<=8`)
	f.exec(t, "COMMIT")
	f.exec(t, `UPDATE libraries SET kind='music' WHERE id='movies'`)
	f.input.Kind = 7
}
func rbTermFixture(t *testing.T, live bool) *browseFixture {
	f := rbTermSource(t)
	gc := governor.Config{Now: time.Now, MaxGrantsPerClass: 2}
	gc.Partitions[governor.Foreground] = governor.Amounts{governor.Reader: 1, governor.MemoryBytes: governor.ForegroundRequestBytes}
	if live {
		gc = readfixture.LiveConfig(time.Now)
	}
	return composeReadFixture(t, f, append(browseDefinitions(), RBTermSmallDefinitions()...), gc, !live, rbTermHash(t))
}
func rbTermRead(t *testing.T, f *browseFixture, field RBTermField, value string) (RBTermSmallResult, BrowseReadResult, error) {
	t.Helper()
	return f.reader.rbTermSmall(t.Context(), rbGroupRequest(), time.Second, f.output, RBTermSmallInput{View: f.input, Field: field, Members: value != "", Value: value})
}
func TestRBTermSmallComplete(t *testing.T) { synctest.Test(t, testRBTermSmallComplete) }
func testRBTermSmallComplete(t *testing.T) {
	f := rbTermFixture(t, false)
	p, c, e := rbTermRead(t, f, RBTermDecade, "1990")
	if e != nil {
		t.Fatal(e)
	}
	if !p.Exhausted || p.RawFetched != 8 || p.RawPositions != 8 || len(p.Groups) != 3 || p.Groups[0].ID != "decade:1800" || p.Groups[1].Title != "1990s" || p.Groups[1].Count != 2 || !reflect.DeepEqual(p.Groups[1].MemberIDs, []int64{6, 7}) || len(p.Members) != 2 || c.PublicationStatements == 0 || p.source == [32]byte{} {
		t.Fatalf("complete decade %+v %+v", p, c)
	}
	rbTermMusic(t, f)
	p, _, e = rbTermRead(t, f, RBTermGenre, "The Rock")
	if e != nil {
		t.Fatal(e)
	}
	if !p.Exhausted || p.RawFetched != 14 || len(p.Groups) != 2 || p.Groups[0].Value != "Alt Rock" || p.Groups[1].Value != "The Rock" || p.Groups[1].Count != 2 || !reflect.DeepEqual(p.Groups[1].MemberIDs, []int64{1, 3}) || len(p.Members) != 2 {
		t.Fatalf("complete genre %+v", p)
	}
	f.input.Descending = true
	p, _, e = rbTermRead(t, f, RBTermGenre, "The Rock")
	if e != nil || p.Groups[0].Value != "The Rock" || p.Members[0].ID != 3 {
		t.Fatal("reverse", p, e)
	}
	// Dictionary and source-less edge do not prove a group's eligible member.
	p, _, e = rbTermRead(t, f, RBTermGenre, "Orphan")
	if e != nil || len(p.Members) != 0 {
		t.Fatal("orphan", p, e)
	}
	f.exec(t, `UPDATE catalog_entities SET retired=1`)
	p, _, e = rbTermRead(t, f, RBTermGenre, "")
	if e != nil || !p.Exhausted || len(p.Groups) != 0 || p.RawFetched != 8 {
		t.Fatal("rejected EOF", p, e)
	}
}

func TestRBTermSmallTVAndDVR(t *testing.T) { synctest.Test(t, testRBTermSmallTVAndDVR) }
func testRBTermSmallTVAndDVR(t *testing.T) {
	f := rbTermFixture(t, false)
	f.exec(t, "BEGIN")
	f.exec(t, "PRAGMA defer_foreign_keys=ON")
	f.exec(t, `UPDATE catalog_subjects SET kind=2 WHERE id IN(1,3)`)
	f.exec(t, `UPDATE catalog_entities SET kind=2 WHERE id IN(1,3)`)
	f.exec(t, "COMMIT")
	f.exec(t, `UPDATE libraries SET kind='tv' WHERE id='movies'`)
	f.input.Kind = 2
	p, _, e := rbTermRead(t, f, RBTermDecade, "")
	if e != nil || len(p.Groups) != 1 || p.Groups[0].Value != "1800" || p.RawFetched != 2 {
		t.Fatal("own show year only", p, e)
	}
	f.exec(t, "BEGIN")
	f.exec(t, "PRAGMA defer_foreign_keys=ON")
	f.exec(t, `UPDATE catalog_subjects SET kind=1 WHERE id IN(1,3)`)
	f.exec(t, `UPDATE catalog_entities SET kind=1 WHERE id IN(1,3)`)
	f.exec(t, "COMMIT")
	f.exec(t, `UPDATE libraries SET kind='movie' WHERE id='movies'`)
	f.input.Kind = 1
	f.exec(t, `INSERT INTO dvr_catalog_retirements(item_id,requested_ms) VALUES(6,1),(7,1)`)
	p, _, e = rbTermRead(t, f, RBTermDecade, "")
	if e != nil || len(p.Groups) != 2 || p.RawFetched != 8 {
		t.Fatal("retirement witnesses", p, e)
	}
}

func TestRBTermSmallRefusalAndPublication(t *testing.T) {
	for _, mode := range []string{"holders", "terms", "bytes", "restriction", "revoked", "metadata", "preferences"} {
		rbTermVirtualCase(t, mode, func(t *testing.T) {
			f := rbTermFixture(t, false)
			field := RBTermDecade
			switch mode {
			case "holders":
				// Raw budget is before eligibility; the 65th retired row still refuses.
				for id := int64(9); id <= 65; id++ {
					f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,retired) SELECT ?,public_id,library_id,kind,'Tail','','','','',0,public_id,public_id,1 FROM catalog_subjects WHERE id=?`, id, id)
				}
			case "terms":
				rbTermMusic(t, f)
				field = RBTermGenre
				for n := 0; n < 17; n++ {
					f.exec(t, `INSERT INTO catalog_term_sources(entity_id,term_id,provider,source_id,label_override) VALUES(1,2,'z',?,'')`, fmt.Sprint(n))
				}
			case "bytes":
				f.exec(t, `UPDATE catalog_entities SET title=? WHERE id=8`, strings.Repeat("x", 140000))
			case "restriction":
				f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,0)`)
			default:
				if mode == "preferences" {
					f.exec(t, "UPDATE libraries SET preference_present=1,preference_revision=1,preference_group_collections=0,preference_local_tags='off',preference_lyrics_source='files',preference_title_source='tags',preference_episode_numbering='season',preference_find_extras=0,preference_sort_ignore_articles=1 WHERE id='movies'")
				}
				sql := map[string]string{"revoked": `UPDATE authorization_session_families SET revoked=1`, "metadata": `UPDATE libraries SET data_metadata_revision=data_metadata_revision+1 WHERE id='movies'`, "preferences": `UPDATE libraries SET preference_revision=preference_revision+1 WHERE id='movies'`}[mode]
				f.reader.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() { f.exec(t, sql) }}
			}
			p, _, e := rbTermRead(t, f, field, "")
			if e == nil || !reflect.DeepEqual(p, RBTermSmallResult{}) {
				t.Fatal("partial output", p, e)
			}
		})
	}
}
func TestRBTermSmallPublicLIVE(t *testing.T) { synctest.Test(t, testRBTermSmallPublicLIVE) }
func testRBTermSmallPublicLIVE(t *testing.T) {
	f := rbTermFixture(t, true)
	q, request, output := liveRequests("rb-term-small")
	in := RBTermSmallInput{View: f.input, Field: RBTermDecade}
	p, _, e := f.reader.RBTermSmall(t.Context(), request, time.Second, BrowseOutput{}, in)
	if !errors.Is(e, ErrInput) || !reflect.DeepEqual(p, RBTermSmallResult{}) {
		t.Fatal("LIVE bypass", e)
	}
	_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		p, c, e := f.reader.RBTermSmall(ctx, request, time.Second, out, in)
		if e != nil {
			return e
		}
		if len(p.Groups) != 3 {
			t.Fatal("live result", p, c)
		}
		copyBytes, e := json.Marshal(p.Groups)
		if e != nil || len(copyBytes) == 0 {
			t.Fatal("consumer copy", e)
		}
		assertLiveUsage(t, f, 1, 0)
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	assertLiveUsage(t, f, 0, 0)
}
func TestRBTermSmallCapture(t *testing.T) {
	f := rbTermSource(t)
	defs := RBTermSmallDefinitions()
	for _, d := range defs {
		for _, c := range d.Cohorts {
			rows, e := f.conn.QueryContext(t.Context(), d.SQL, c.Bindings...)
			if e != nil {
				t.Fatal(e)
			}
			present := rows.Next()
			e = rows.Err()
			rows.Close()
			if e != nil || present != (c.Name == "present") {
				t.Fatal("cohort", d.ID, c.Name, e)
			}
		}
	}

	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		g := captureBrowse(t, f, defs, stats, rbTermHash(t))
		if _, e := stmt.New(defs, g.Receipt()); e != nil {
			t.Fatal(e)
		}
	}
}

func rbTermVirtualCase(t *testing.T, name string, fn func(*testing.T)) {
	t.Helper()
	t.Run(name, func(t *testing.T) { synctest.Test(t, fn) })
}

func TestRBTermSmallRawTranscript(t *testing.T) {
	for _, pair := range [][2][][]any{
		{{{string([]byte{0x80})}}, {{string([]byte{0x81})}}},
		{{{nil}}, {{[]byte(nil)}}},
		{{{"ab", "c"}}, {{"a", "bc"}}},
	} {
		var sums [2][32]byte
		for i, rows := range pair {
			work := rbTermSmallWork{hash: sha256.New()}
			if e := rbTermRecord(&work, "raw", rows); e != nil {
				t.Fatal(e)
			}
			copy(sums[i][:], work.hash.Sum(nil))
		}
		if sums[0] == sums[1] {
			t.Fatal("distinct native rows collapsed into one transcript", pair)
		}
	}
}

// All other bounds stay below their limits so each refusal proves its named
// boundary. In particular 257 combined rows uses only four terms on one holder.
func TestRBTermSmallExactBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		holders, terms           int
		extra, firstOnly, refuse bool
		raw                      int
	}{
		{name: "combined256", holders: 64, terms: 3, raw: 256},
		{name: "combined257", holders: 64, terms: 3, extra: true, refuse: true},
		{name: "holders64", holders: 64, terms: 0, raw: 64},
		{name: "holders65", holders: 65, terms: 0, refuse: true},
		{name: "terms16", holders: 8, terms: 16, firstOnly: true, raw: 24},
		{name: "terms17", holders: 8, terms: 17, firstOnly: true, refuse: true},
	} {
		rbTermVirtualCase(t, tc.name, func(t *testing.T) {
			f := rbTermFixture(t, false)
			rbTermMusic(t, f)
			f.exec(t, "DELETE FROM catalog_term_sources")
			f.exec(t, "DELETE FROM catalog_entity_terms")
			f.exec(t, "UPDATE catalog_entities SET retired=0 WHERE kind=7")
			for n := 0; n < tc.holders; n++ {
				id := int64(n + 1)
				if id >= 9 {
					id += 2
				}
				if id >= 11 {
					f.exec(t, "UPDATE catalog_subjects SET kind=7 WHERE id=?", id)
					f.exec(t, "INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,album_id) SELECT id,public_id,library_id,7,'Boundary','boundary','b','boundary','b',0,public_id,public_id,10 FROM catalog_subjects WHERE id=?", id)
				}
				count := tc.terms
				if tc.firstOnly && n > 0 {
					count = 0
				}
				if tc.extra && n == 0 {
					count++
				}
				if count > 0 {
					f.exec(t, "INSERT INTO catalog_entity_terms(entity_id,term_id) VALUES(?,1)", id)
				}
				for term := 0; term < count; term++ {
					f.exec(t, "INSERT INTO catalog_term_sources(entity_id,term_id,provider,source_id) VALUES(?,1,'owner',?)", id, fmt.Sprintf("%03d", term))
				}
			}
			page, _, err := rbTermRead(t, f, RBTermGenre, "")
			if tc.refuse {
				if !errors.Is(err, ErrNotReady) || !reflect.DeepEqual(page, RBTermSmallResult{}) {
					t.Fatal("over-bound source escaped", page, err)
				}
				return
			}
			if err != nil || !page.Exhausted || page.RawFetched != tc.raw || page.RawPositions != tc.raw {
				t.Fatal("exact bound refused or truncated", page, err)
			}
			if tc.terms > 0 {
				count := tc.holders
				if tc.firstOnly {
					count = 1
				}
				if len(page.Groups) != 1 || page.Groups[0].Count != count || len(page.Groups[0].MemberIDs) != count {
					t.Fatal("boundary membership incomplete", page)
				}
			}
		})
	}
}
