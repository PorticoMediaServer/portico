package read

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// rbHomePrepareStamp is the schema's only accepted timestamp width: the
// added_at/last_content_added_at CHECKs require exactly 30 characters.
const rbHomePrepareStamp = "2006-01-02T15:04:05.000000000Z"

func rbHomePrepareFixtureDefinitions() []stmt.Definition {
	return append(homeFixedAllDefinitions(), rbHomePrepareDefinitions()...)
}

func rbHomePrepareFixtureHash(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("rb_home_prepare_test.go")
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(browsePlanFixtureHash(t)), data...)))
}

func rbHomePrepareConfig() governor.Config {
	cfg := readfixture.LiveConfig(time.Now)
	cfg.Foreground.AssignedBytes += 20 << 20
	cfg.Foreground.EffectiveBytes += 20 << 20
	cfg.Foreground.NonBrowseBytes += 16 << 20
	protected := cfg.Partitions[governor.Foreground][governor.MemoryBytes]
	cfg.Partitions[governor.Foreground][governor.MemoryBytes] += 80 << 20
	cfg.Partitions[governor.Foreground][governor.WALBytes] += 1 << 20
	cfg.Partitions[governor.Background] = governor.Amounts{governor.MemoryBytes: 4096, governor.WALBytes: 4096}
	cfg.Preparation = &governor.PreparationProfile{MaxActive: 4, MaxBytes: 16 << 20, MaxJobBytes: 16 << 20, ProtectedBytes: protected}
	return cfg
}

func rbHomePrepareFixture(t *testing.T, seed func(*browseFixture)) (*browseFixture, *RBHomePrepareReader) {
	t.Helper()
	f := homeFixedSeed(t, 0)
	seed(f)
	f = composeReadFixture(t, f, rbHomePrepareFixtureDefinitions(), rbHomePrepareConfig(), false, rbHomePrepareFixtureHash(t))
	r, err := NewRBHomePrepareReader(f.snapshots, f.authority)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}

// rbHomePrepareLibrarySeed adds one visible library with its own source, so a
// binding in that library can be proven against its own root.
func rbHomePrepareLibrarySeed(t *testing.T, f *browseFixture, id string, native int64, kind string) {
	t.Helper()
	f.exec(t, `INSERT INTO libraries(id,name,kind,root) VALUES(?,?,?,'/media')`, id, id, kind)
	f.exec(t, `INSERT INTO catalog_libraries(id,library_id,root) VALUES(?,?,'/media')`, native, id)
	f.exec(t, `INSERT INTO library_sources(id,library_id,configured_root,root,incarnation,generation,identity_confirmed) VALUES(?,?,'/media','/media',?,3,1)`, "source-"+id, id, "inc-"+id)
}

func rbHomePrepareMemberLibraries(t *testing.T, f *browseFixture, ids ...string) {
	t.Helper()
	quoted := make([]string, 0, len(ids))
	for _, id := range ids {
		quoted = append(quoted, `"`+id+`"`)
	}
	f.exec(t, `UPDATE direct_memberships SET allowed_libraries=? WHERE account_id='a'`, "["+strings.Join(quoted, ",")+"]")
}

// rbHomePrepareEntitySeed inserts one schema-legal catalogue row. Every kind
// carries the columns its own CHECK requires: seasons a season number, episodes
// an episode number, and artists their own artist pointer.
func rbHomePrepareEntitySeed(t *testing.T, f *browseFixture, id, library, kind int64, parent, show, season, album, book any, date string) {
	t.Helper()
	b := make([]byte, 16)
	binary.BigEndian.PutUint64(b[8:], uint64(id))
	var seasonNumber, episodeNumber, artistID any
	if kind == 3 {
		seasonNumber = int64(1)
	}
	if kind == 4 {
		// The episode-position index is unique per (show, season, numbering,
		// episode), so the fixture's distinct ids also give distinct numbers.
		episodeNumber = id
	}
	if kind == 6 {
		artistID = id
	}
	f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,?,?)`, id, b, library, kind)
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,parent_id,show_id,season_id,season_number,episode_number,album_id,book_id,artist_id,numbering,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,playback_origin_incarnation,playback_origin_revision,added_at,last_content_added_at,admission_own_age) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,'seasonal','Prepared','prepared','prepared','prepared','prepared',0,?,?,?,1,?,?,10)`, id, b, library, kind, parent, show, season, seasonNumber, episodeNumber, album, book, artistID, b, b, b, date, date)
}

func rbHomePrepareAssetSeed(t *testing.T, f *browseFixture, id, library int64, source, incarnation string) {
	t.Helper()
	f.exec(t, `INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,playback_origin_incarnation,playback_origin_revision,prepared_source_revision) SELECT id,printf('prep-%d',id),library_id,printf('/media/prep-%d',id),1000,1,'mkv','h264','aac',1920,1080,120,public_id,1,1 FROM catalog_entities WHERE id=?`, id)
	f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id,playback_origin_incarnation,playback_origin_revision) SELECT id,id,library_id,public_id,1 FROM catalog_entities WHERE id=?`, id)
	f.exec(t, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES(?,?,?,?,?,'object-rev','{}',1000,1)`, fmt.Sprintf("prep-object-%d", id), source, fmt.Sprintf("prep-%d", id), incarnation, fmt.Sprintf("prep-%d", id))
}

func rbHomePrepareStateSeed(t *testing.T, f *browseFixture, id, pos int64, state string) {
	t.Helper()
	f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,resume_position,resume_unit,activity_at,activity_state,resume_playback_id) SELECT 'p',public_id,id,library_id,?,0,'2026-11-02T12:00:00.000000000Z',?,'prep-play' FROM catalog_entities WHERE id=?`, pos, state, id)
}

func rbHomePrepareBegin(t *testing.T, r *RBHomePrepareReader, f *browseFixture, row string) *RBHomePreparePreparation {
	t.Helper()
	job, err := r.Begin(t.Context(), 1, 4<<20, RBHomePrepareInput{View: f.input, RowID: row})
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func rbHomePrepareRequest() governor.Request {
	return governor.Request{Key: governor.JobKey{Domain: 395, ID: "rb-home-prepare-quantum"}, Owner: 1, Class: governor.Foreground, Need: governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 12 << 20}, Budget: governor.UnitBudget{RawRows: 2048, VMOps: 100000, Pages: 256}, Deadline: time.Now().Add(time.Second)}
}

func rbHomePrepareRun(t *testing.T, job *RBHomePreparePreparation) RBHomePrepareAdvance {
	t.Helper()
	var last RBHomePrepareAdvance
	for n := 0; n < 256; n++ {
		advance, err := job.Advance(t.Context(), rbHomePrepareRequest(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		last.Restarts += advance.Restarts
		last.RawRows += advance.RawRows
		last.Statements += advance.Statements
		if advance.Ready {
			last.Ready = true
			return last
		}
	}
	t.Fatal("preparation did not finish")
	return last
}

func rbHomePrepareRead(t *testing.T, f *browseFixture, job *RBHomePreparePreparation) (RBHomePreparePage, BrowseReadResult, error) {
	t.Helper()
	live, request, output := liveRequests("rb-home-prepare")
	request.Budget.RawRows = 2048
	var page RBHomePreparePage
	var cost BrowseReadResult
	_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
		var err error
		page, cost, err = job.Row(ctx, request, time.Second, out, f.input)
		return err
	})
	return page, cost, err
}

// rbHomePrepareOracle is a plain-SQL oracle over the fixture: it groups the raw
// leaf rows by their actual work pointer and applies only the published
// newest-first / descending public-id tie order (see sortHomeCards and
// homeFixedRecent). It never calls the reader.
func rbHomePrepareOracle(t *testing.T, f *browseFixture, libraries []int64, maxAge int64, limit int) []int64 {
	t.Helper()
	allowed := map[int64]bool{}
	for _, id := range libraries {
		allowed[id] = true
	}
	type fact struct{ date string }
	works := map[int64]fact{}
	publics := map[int64][]byte{}
	rows, err := f.conn.QueryContext(t.Context(), `SELECT id,library_id,kind,public_id,show_id,retired,admission_own_age,added_at FROM catalog_entities`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, library, kind, retired int64
		var public []byte
		var show, age sqlNullInt
		var added sqlNullText
		if err := rows.Scan(&id, &library, &kind, &public, &show, &retired, &age, &added); err != nil {
			t.Fatal(err)
		}
		publics[id] = public
		if !allowed[library] || retired != 0 {
			continue
		}
		if maxAge >= 0 && (!age.ok || age.value > maxAge) {
			continue
		}
		work := id
		if kind == 4 {
			if !show.ok {
				continue
			}
			work = show.value
		} else if kind != 1 {
			continue
		}
		if !added.ok {
			continue
		}
		if current, ok := works[work]; !ok || added.text > current.date {
			works[work] = fact{date: added.text}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, len(works))
	for id := range works {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(a, b int) bool {
		x, y := works[ids[a]], works[ids[b]]
		if x.date != y.date {
			return x.date > y.date
		}
		return strings.Compare(entityid.Encode(publics[ids[a]]), entityid.Encode(publics[ids[b]])) > 0
	})
	if len(ids) > limit {
		ids = ids[:limit]
	}
	return ids
}

// sqlNullInt/sqlNullText keep the oracle independent of database/sql imports in
// the reader test file.
type sqlNullInt struct {
	value int64
	ok    bool
}

func (n *sqlNullInt) Scan(src any) error {
	if src == nil {
		*n = sqlNullInt{}
		return nil
	}
	value, err := toInt64(src)
	if err != nil {
		return err
	}
	*n = sqlNullInt{value: value, ok: true}
	return nil
}

type sqlNullText struct {
	text string
	ok   bool
}

func (n *sqlNullText) Scan(src any) error {
	if src == nil {
		*n = sqlNullText{}
		return nil
	}
	switch value := src.(type) {
	case string:
		*n = sqlNullText{text: value, ok: true}
	case []byte:
		*n = sqlNullText{text: string(value), ok: true}
	default:
		return fmt.Errorf("expected text, got %T", src)
	}
	return nil
}

func toInt64(src any) (int64, error) {
	switch value := src.(type) {
	case int64:
		return value, nil
	case []byte:
		var out int64
		if _, err := fmt.Sscan(string(value), &out); err != nil {
			return 0, err
		}
		return out, nil
	}
	return 0, fmt.Errorf("expected integer, got %T", src)
}

func TestRBHomePrepareRecentLarge(t *testing.T) {
	homeFixedCase(t, "forty_entities_one_library", func(t *testing.T) {
		f, r := rbHomePrepareFixture(t, func(f *browseFixture) {
			for n := int64(0); n < 40; n++ {
				id := 2000 + n
				date := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Minute).Format(rbHomePrepareStamp)
				rbHomePrepareEntitySeed(t, f, id, 1, 1, nil, nil, nil, nil, nil, date)
				rbHomePrepareAssetSeed(t, f, id, 1, "fixed-source", "fixed-inc")
			}
		})
		job := rbHomePrepareBegin(t, r, f, "new_on_server")
		defer job.Close()
		done := rbHomePrepareRun(t, job)
		if !done.Ready || done.RawRows < 40 {
			t.Fatal("preparation did not walk the complete source", done)
		}
		page, _, err := rbHomePrepareRead(t, f, job)
		if err != nil || !page.Exact || !page.Exhausted || page.Pending || len(page.Items) != 40 {
			t.Fatal(page, err)
		}
		want := rbHomePrepareOracle(t, f, []int64{1}, 12, 100)
		if len(want) != 40 {
			t.Fatal("oracle membership", len(want))
		}
		for n := range want {
			if page.Items[n].Work.ID != want[n] {
				t.Fatalf("row %d got %d want %d", n, page.Items[n].Work.ID, want[n])
			}
		}
	})
	homeFixedCase(t, "six_libraries", func(t *testing.T) {
		f, r := rbHomePrepareFixture(t, func(f *browseFixture) {
			libraries := []string{"lib3", "lib4", "lib5", "lib6"}
			for n, id := range libraries {
				rbHomePrepareLibrarySeed(t, f, id, int64(3+n), "movie")
			}
			rbHomePrepareMemberLibraries(t, f, "movies", "other", "lib3", "lib4", "lib5", "lib6")
			which := []int64{1, 3, 4, 5, 6}
			name := map[int64]string{1: "movies", 3: "lib3", 4: "lib4", 5: "lib5", 6: "lib6"}
			for n := int64(0); n < 35; n++ {
				library := which[int(n)%len(which)]
				date := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Minute).Format(rbHomePrepareStamp)
				rbHomePrepareEntitySeed(t, f, 3000+n, library, 1, nil, nil, nil, nil, nil, date)
				source, incarnation := "fixed-source", "fixed-inc"
				if library != 1 {
					source, incarnation = "source-"+name[library], "inc-"+name[library]
				}
				rbHomePrepareAssetSeed(t, f, 3000+n, library, source, incarnation)
			}
		})
		job := rbHomePrepareBegin(t, r, f, "new_on_server")
		defer job.Close()
		rbHomePrepareRun(t, job)
		page, _, err := rbHomePrepareRead(t, f, job)
		if err != nil || !page.Exact || len(page.Items) != 35 {
			t.Fatal(page, err)
		}
		want := rbHomePrepareOracle(t, f, []int64{1, 3, 4, 5, 6}, 12, 100)
		for n := range want {
			if page.Items[n].Work.ID != want[n] {
				t.Fatalf("row %d got %d want %d", n, page.Items[n].Work.ID, want[n])
			}
		}
	})
	homeFixedCase(t, "equal_time_public_ids", func(t *testing.T) {
		f, r := rbHomePrepareFixture(t, func(f *browseFixture) {
			date := "2026-10-01T00:00:00.000000000Z"
			for n := int64(0); n < 130; n++ {
				id := 2000 + n
				rbHomePrepareEntitySeed(t, f, id, 1, 1, nil, nil, nil, nil, nil, date)
				rbHomePrepareAssetSeed(t, f, id, 1, "fixed-source", "fixed-inc")
			}
		})
		job := rbHomePrepareBegin(t, r, f, "new_on_server")
		defer job.Close()
		rbHomePrepareRun(t, job)
		page, _, err := rbHomePrepareRead(t, f, job)
		if err != nil || !page.Exact || len(page.Items) != 100 {
			t.Fatal(page, err)
		}
		if page.Items[0].Work.ID == 0 || page.Items[99].Added != "2026-10-01T00:00:00.000000000Z" {
			t.Fatal("equal-instant shelf", page.Items[0], page.Items[99])
		}
		want := rbHomePrepareOracle(t, f, []int64{1}, 12, 100)
		for n := range want {
			if page.Items[n].Work.ID != want[n] {
				t.Fatalf("equal-time tie %d got %d want %d", n, page.Items[n].Work.ID, want[n])
			}
		}
	})
}

func TestRBHomePrepareHiddenNewestChild(t *testing.T) {
	homeFixedCase(t, "restricted_leaf_completion", func(t *testing.T) {
		f, r := rbHomePrepareFixture(t, func(f *browseFixture) {
			rbHomePrepareLibrarySeed(t, f, "shows", 3, "tv")
			rbHomePrepareMemberLibraries(t, f, "movies", "other", "shows")
			rbHomePrepareEntitySeed(t, f, 2000, 3, 2, nil, nil, nil, nil, nil, "2026-11-04T00:00:00.000000000Z")
			rbHomePrepareEntitySeed(t, f, 2100, 3, 3, int64(2000), int64(2000), nil, nil, nil, "2026-11-04T00:00:00.000000000Z")
			for n := int64(0); n < 3; n++ {
				rbHomePrepareEntitySeed(t, f, 3000+n, 3, 4, int64(2100), int64(2000), int64(2100), nil, nil, fmt.Sprintf("2026-11-01T12:0%d:00.000000000Z", n))
				rbHomePrepareAssetSeed(t, f, 3000+n, 3, "source-shows", "inc-shows")
			}
			f.exec(t, `UPDATE catalog_entities SET admission_own_age=18,added_at='2026-11-06T00:00:00.000000000Z' WHERE id=3002`)
		})
		job := rbHomePrepareBegin(t, r, f, "new_on_server")
		defer job.Close()
		rbHomePrepareRun(t, job)
		page, _, err := rbHomePrepareRead(t, f, job)
		if err != nil || !page.Exact || len(page.Items) != 1 {
			t.Fatal(page, err)
		}
		card := page.Items[0]
		if card.Work.ID != 2000 || card.Target.ID != 2100 || card.Added != "2026-11-01T12:01:00.000000000Z" || card.EligibleLeaves != 2 {
			t.Fatal("hidden newest child changed the shelf", card)
		}
	})
}

func TestRBHomePrepareActivityRecency(t *testing.T) {
	homeFixedCase(t, "album_and_book_recent_fallback", func(t *testing.T) {
		f, r := rbHomePrepareFixture(t, func(f *browseFixture) {
			rbHomePrepareLibrarySeed(t, f, "music", 3, "music")
			rbHomePrepareLibrarySeed(t, f, "audio", 4, "audiobook")
			rbHomePrepareMemberLibraries(t, f, "movies", "other", "music", "audio")
			rbHomePrepareEntitySeed(t, f, 2000, 3, 6, nil, nil, nil, nil, nil, "2026-10-01T00:00:00.000000000Z")
			rbHomePrepareEntitySeed(t, f, 2001, 3, 6, nil, nil, nil, nil, nil, "2026-10-05T00:00:00.000000000Z")
			rbHomePrepareEntitySeed(t, f, 3000, 3, 7, int64(2000), nil, nil, int64(2000), nil, "2026-10-01T00:00:00.000000000Z")
			rbHomePrepareEntitySeed(t, f, 3001, 3, 7, int64(2001), nil, nil, int64(2001), nil, "2026-10-05T00:00:00.000000000Z")
			rbHomePrepareEntitySeed(t, f, 2100, 4, 8, nil, nil, nil, nil, nil, "2026-10-03T00:00:00.000000000Z")
			rbHomePrepareEntitySeed(t, f, 3100, 4, 9, int64(2100), nil, nil, nil, int64(2100), "2026-10-03T00:00:00.000000000Z")
			for _, id := range []int64{3000, 3001, 3100} {
				rbHomePrepareAssetSeed(t, f, id, entityLibrary(id), "source-"+libraryName(entityLibrary(id)), "inc-"+libraryName(entityLibrary(id)))
			}
		})
		job := rbHomePrepareBegin(t, r, f, "listen_next")
		defer job.Close()
		rbHomePrepareRun(t, job)
		page, _, err := rbHomePrepareRead(t, f, job)
		if err != nil || !page.Exact || page.Pending || len(page.Items) != 3 {
			t.Fatal(page, err)
		}
		want := []int64{2001, 2100, 2000}
		for n := range want {
			if page.Items[n].Work.ID != want[n] {
				t.Fatalf("recency %d got %d want %d", n, page.Items[n].Work.ID, want[n])
			}
		}
	})
	homeFixedCase(t, "movie_continue", func(t *testing.T) {
		f, r := rbHomePrepareFixture(t, func(f *browseFixture) {
			for id := int64(2000); id < 2002; id++ {
				rbHomePrepareEntitySeed(t, f, id, 1, 1, nil, nil, nil, nil, nil, "2026-10-01T00:00:00.000000000Z")
				rbHomePrepareAssetSeed(t, f, id, 1, "fixed-source", "fixed-inc")
				rbHomePrepareStateSeed(t, f, id, 5000, "in_progress")
			}
			f.exec(t, `UPDATE personal_items SET dismissed_playback_id=resume_playback_id WHERE item_id=2001`)
		})
		job := rbHomePrepareBegin(t, r, f, "continue")
		defer job.Close()
		rbHomePrepareRun(t, job)
		page, _, err := rbHomePrepareRead(t, f, job)
		if err != nil || !page.Exact || len(page.Items) != 1 || page.Items[0].Target.ID != 2000 || page.Items[0].ResumePosition != 5000 {
			t.Fatal(page, err)
		}
	})
	homeFixedCase(t, "book_resume_child", func(t *testing.T) {
		f, r := rbHomePrepareFixture(t, func(f *browseFixture) {
			rbHomePrepareLibrarySeed(t, f, "audio", 3, "audiobook")
			rbHomePrepareMemberLibraries(t, f, "movies", "other", "audio")
			rbHomePrepareEntitySeed(t, f, 2000, 3, 8, nil, nil, nil, nil, nil, "2026-10-01T00:00:00.000000000Z")
			rbHomePrepareEntitySeed(t, f, 3000, 3, 9, int64(2000), nil, nil, nil, int64(2000), "2026-10-01T00:00:00.000000000Z")
			rbHomePrepareEntitySeed(t, f, 3001, 3, 9, int64(2000), nil, nil, nil, int64(2000), "2026-10-01T00:00:00.000000000Z")
			rbHomePrepareAssetSeed(t, f, 3000, 3, "source-audio", "inc-audio")
			rbHomePrepareStateSeed(t, f, 2000, 7000, "in_progress")
			rbHomePrepareStateSeed(t, f, 3000, 7000, "in_progress")
			f.exec(t, `UPDATE personal_items SET resume_item_public_id=(SELECT public_id FROM catalog_entities WHERE id=3000) WHERE item_id=2000`)
		})
		job := rbHomePrepareBegin(t, r, f, "continue_books")
		defer job.Close()
		rbHomePrepareRun(t, job)
		page, _, err := rbHomePrepareRead(t, f, job)
		if err != nil || !page.Exact || len(page.Items) != 1 || page.Items[0].Work.ID != 2000 || page.Items[0].Target.ID != 3000 {
			t.Fatal(page, err)
		}
	})
}

func entityLibrary(id int64) int64 {
	if id >= 3100 {
		return 4
	}
	return 3
}

func libraryName(native int64) string {
	if native == 4 {
		return "audio"
	}
	return "music"
}

func TestRBHomePrepareSparseRawWindow(t *testing.T) {
	homeFixedCase(t, "retired_rows_advance_the_cursor", func(t *testing.T) {
		f, r := rbHomePrepareFixture(t, func(f *browseFixture) {
			date := "2026-10-01T00:00:00.000000000Z"
			for n := int64(0); n < 70; n++ {
				id := 2000 + n
				rbHomePrepareEntitySeed(t, f, id, 1, 1, nil, nil, nil, nil, nil, date)
				rbHomePrepareAssetSeed(t, f, id, 1, "fixed-source", "fixed-inc")
			}
			f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id<2070`)
			for n := int64(0); n < 2; n++ {
				id := 3000 + n
				rbHomePrepareEntitySeed(t, f, id, 1, 1, nil, nil, nil, nil, nil, date)
				rbHomePrepareAssetSeed(t, f, id, 1, "fixed-source", "fixed-inc")
			}
		})
		job := rbHomePrepareBegin(t, r, f, "new_on_server")
		defer job.Close()
		done := rbHomePrepareRun(t, job)
		if !done.Ready || done.RawRows < 72 {
			t.Fatal("retired raw rows did not stay in the stream", done)
		}
		page, _, err := rbHomePrepareRead(t, f, job)
		if err != nil || !page.Exact || len(page.Items) != 2 {
			t.Fatal(page, err)
		}
	})
}

func TestRBHomePrepareLifecycle(t *testing.T) {
	homeFixedCase(t, "undersized_lease_retains_nothing", func(t *testing.T) {
		f, r := rbHomePrepareFixture(t, func(f *browseFixture) {
			rbHomePrepareEntitySeed(t, f, 2000, 1, 1, nil, nil, nil, nil, nil, "2026-10-01T00:00:00.000000000Z")
			rbHomePrepareAssetSeed(t, f, 2000, 1, "fixed-source", "fixed-inc")
		})
		job, err := r.Begin(t.Context(), 1, 8192, RBHomePrepareInput{View: f.input, RowID: "new_on_server"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := job.Advance(t.Context(), rbHomePrepareRequest(), time.Second); !errors.Is(err, errRBHomePrepareCapacity) {
			t.Fatal("undersized preparation did not refuse", err)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("capacity refusal retained bytes")
		}
	})
	homeFixedCase(t, "cross_poll_cancellation", func(t *testing.T) {
		f, r := rbHomePrepareFixture(t, func(f *browseFixture) {
			for id := int64(2000); id < 2004; id++ {
				rbHomePrepareEntitySeed(t, f, id, 1, 1, nil, nil, nil, nil, nil, "2026-10-01T00:00:00.000000000Z")
				rbHomePrepareAssetSeed(t, f, id, 1, "fixed-source", "fixed-inc")
			}
		})
		ctx, cancel := context.WithCancel(t.Context())
		job, err := r.Begin(ctx, 1, 1<<20, RBHomePrepareInput{View: f.input, RowID: "new_on_server"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := job.Advance(t.Context(), rbHomePrepareRequest(), time.Second); err != nil {
			t.Fatal(err)
		}
		cancel()
		if _, err := job.Advance(t.Context(), rbHomePrepareRequest(), time.Second); !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled preparation poll continued", err)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("cancelled preparation retained values or bytes")
		}
	})
	homeFixedCase(t, "original_deadline", func(t *testing.T) {
		f, r := rbHomePrepareFixture(t, func(f *browseFixture) {
			rbHomePrepareEntitySeed(t, f, 2000, 1, 1, nil, nil, nil, nil, nil, "2026-10-01T00:00:00.000000000Z")
			rbHomePrepareAssetSeed(t, f, 2000, 1, "fixed-source", "fixed-inc")
		})
		job := rbHomePrepareBegin(t, r, f, "new_on_server")
		if _, err := job.Advance(t.Context(), rbHomePrepareRequest(), time.Second); err != nil {
			t.Fatal(err)
		}
		time.Sleep(governor.PreparationLifetime + time.Second)
		if _, err := job.Advance(t.Context(), rbHomePrepareRequest(), time.Second); !errors.Is(err, governor.ErrPreparationExpired) {
			t.Fatal("preparation outlived its original deadline", err)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("expired preparation retained values or bytes")
		}
	})
	homeFixedCase(t, "content_invalidation_restarts", func(t *testing.T) {
		f, r := rbHomePrepareFixture(t, func(f *browseFixture) {
			for n := int64(0); n < 12; n++ {
				id := 2000 + n
				date := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Minute).Format(rbHomePrepareStamp)
				rbHomePrepareEntitySeed(t, f, id, 1, 1, nil, nil, nil, nil, nil, date)
				rbHomePrepareAssetSeed(t, f, id, 1, "fixed-source", "fixed-inc")
			}
		})
		job := rbHomePrepareBegin(t, r, f, "new_on_server")
		defer job.Close()
		if _, err := job.Advance(t.Context(), rbHomePrepareRequest(), time.Second); err != nil {
			t.Fatal(err)
		}
		original := job.entry.lease.Deadline()
		bytes := f.authority.Usage()[governor.Foreground][governor.MemoryBytes]
		f.exec(t, `UPDATE libraries SET data_revision=data_revision+1 WHERE id='movies'`)
		done := rbHomePrepareRun(t, job)
		if !done.Ready || done.Restarts != 1 {
			t.Fatalf("content invalidation did not restart exactly once: %+v", done)
		}
		if !job.entry.lease.Deadline().Equal(original) || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != bytes {
			t.Fatal("restart renewed the deadline or re-admitted physical bytes")
		}
		page, _, err := rbHomePrepareRead(t, f, job)
		if err != nil || !page.Exact || len(page.Items) != 12 {
			t.Fatal(page, err)
		}
	})
	homeFixedCase(t, "provider_identity_pending", func(t *testing.T) {
		f, r := rbHomePrepareFixture(t, func(f *browseFixture) {
			rbHomePrepareEntitySeed(t, f, 2000, 1, 1, nil, nil, nil, nil, nil, "2026-10-01T00:00:00.000000000Z")
			rbHomePrepareAssetSeed(t, f, 2000, 1, "fixed-source", "fixed-inc")
			f.exec(t, `INSERT INTO metadata_entity_external_ids(entity_id,provider,subject_kind,provider_id) VALUES(2000,'tmdb','movie','42')`)
		})
		job := rbHomePrepareBegin(t, r, f, "new_on_server")
		defer job.Close()
		rbHomePrepareRun(t, job)
		page, _, err := rbHomePrepareRead(t, f, job)
		if err != nil || !page.Pending || page.PendingReason != "provider_edition_grouping_preparation" || page.Exact {
			t.Fatal(page, err)
		}
	})
	homeFixedCase(t, "tv_continue_named_refusal", func(t *testing.T) {
		f, r := rbHomePrepareFixture(t, func(f *browseFixture) {
			rbHomePrepareLibrarySeed(t, f, "shows", 3, "tv")
			rbHomePrepareMemberLibraries(t, f, "movies", "other", "shows")
			rbHomePrepareEntitySeed(t, f, 2000, 3, 2, nil, nil, nil, nil, nil, "2026-10-01T00:00:00.000000000Z")
			rbHomePrepareEntitySeed(t, f, 2100, 3, 4, nil, int64(2000), nil, nil, nil, "2026-10-01T00:00:00.000000000Z")
			rbHomePrepareAssetSeed(t, f, 2100, 3, "source-shows", "inc-shows")
		})
		job := rbHomePrepareBegin(t, r, f, "continue")
		defer job.Close()
		rbHomePrepareRun(t, job)
		page, _, err := rbHomePrepareRead(t, f, job)
		if err != nil || !page.Pending || page.PendingReason != "show_container_on_deck_policy_uncomposed" || page.Exact {
			t.Fatal(page, err)
		}
	})
	homeFixedCase(t, "handoff_drift_is_stale", func(t *testing.T) {
		f, r := rbHomePrepareFixture(t, func(f *browseFixture) {
			rbHomePrepareEntitySeed(t, f, 2000, 1, 1, nil, nil, nil, nil, nil, "2026-10-01T00:00:00.000000000Z")
			rbHomePrepareAssetSeed(t, f, 2000, 1, "fixed-source", "fixed-inc")
		})
		job := rbHomePrepareBegin(t, r, f, "new_on_server")
		rbHomePrepareRun(t, job)
		f.exec(t, `UPDATE libraries SET data_revision=data_revision+1 WHERE id='movies'`)
		if _, _, err := rbHomePrepareRead(t, f, job); !errors.Is(err, ErrStale) {
			t.Fatal("drifted handoff was not stale", err)
		}
		if f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("stale handoff retained bytes")
		}
	})
}

func TestRBHomePrepareWindowBounds(t *testing.T) {
	if err := rbHomePrepareChronologyOctets(make([][]any, rbHomePrepareRaw+1), rbHomePrepareRaw); !errors.Is(err, ErrUnqualified) {
		t.Fatal("cap+1 chronology window was truncated instead of refused", err)
	}
	row := []any{int64(1), "short", make([]byte, 16), int64(0), nil, nil, nil, nil}
	if err := rbHomePrepareChronologyOctets([][]any{row}, rbHomePrepareRaw); !errors.Is(err, ErrUnqualified) {
		t.Fatal("non-schema timestamp width accepted", err)
	}
	if err := rbHomePrepareChildOctets(make([][]any, rbHomePrepareChildWindow+1), rbHomePrepareChildWindow); !errors.Is(err, ErrUnqualified) {
		t.Fatal("cap+1 child window was truncated instead of refused", err)
	}
	if err := rbHomePreparePersonalOctets(make([][]any, rbHomePreparePersonalWindow+1), rbHomePreparePersonalWindow); !errors.Is(err, ErrUnqualified) {
		t.Fatal("cap+1 personal window was truncated instead of refused", err)
	}
}
