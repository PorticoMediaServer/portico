package read

import (
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// rsTechnicalBoundaryPoll funds the shared read window to the governor's
// 4096-row maximum. Without that, both a 4096-key inventory and a 4097-key
// truncation would stop at the caller's smaller row allowance, and the family's
// own cap would never be exercised.
func rsTechnicalBoundaryPoll(t *testing.T, r *RSSelectedTechnicalReader, f *browseFixture, w *RSTechnicalWork) (RSTechnicalFacts, error) {
	t.Helper()
	request := foregroundRequest()
	request.Budget.RawRows = rsTechnicalRows
	p, _, e := r.poll(t.Context(), request, time.Second, f.output, RSSelectedTechnicalInput{View: f.input, Item: 1, Asset: 1, Work: w})
	return p, e
}

func rsTechnicalBoundaryUntil(t *testing.T, r *RSSelectedTechnicalReader, f *browseFixture, w *RSTechnicalWork, what string, ready func() bool) {
	t.Helper()
	for n := 0; n < 16; n++ {
		p, e := rsTechnicalBoundaryPoll(t, r, f, w)
		if e != nil {
			t.Fatalf("%s: poll %d (phase %d, family %q): %v", what, n, w.phase, w.family, e)
		}
		if !p.Preparing {
			t.Fatalf("%s: completed before its key inventory: %+v", what, p)
		}
		if ready() {
			return
		}
	}
	t.Fatalf("%s: key inventory was not reached", what)
}

func rsTechnicalBoundaryRefusal(t *testing.T, r *RSSelectedTechnicalReader, f *browseFixture, w *RSTechnicalWork, what string) error {
	t.Helper()
	for n := 0; n < 16; n++ {
		p, e := rsTechnicalBoundaryPoll(t, r, f, w)
		if e != nil {
			if errors.Is(e, governor.ErrSnapshotBudget) {
				t.Fatalf("%s: budget refusal instead of the typed capacity refusal: %v", what, e)
			}
			if !errors.Is(e, ErrUnqualified) {
				t.Fatalf("%s: unexpected refusal: %v", what, e)
			}
			if p.Preparing || p.StreamState != "" || len(p.Streams) != 0 {
				t.Fatalf("%s: truncated facts escaped: %+v", what, p)
			}
			return e
		}
		if !p.Preparing {
			t.Fatalf("%s: published a truncated exact answer after %d polls: %+v", what, n, p)
		}
	}
	t.Fatalf("%s: no capacity refusal", what)
	return nil
}

func rsTechnicalSeedStreams(t *testing.T, f *browseFixture, n int) {
	t.Helper()
	f.exec(t, `DELETE FROM asset_streams`)
	f.exec(t, `WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM n WHERE i<?) INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced) SELECT 'asset',i,'audio','aac','eng',printf('st-%04d',i),2,'stereo',0,0 FROM n`, n-1)
}

func rsTechnicalSeedChapters(t *testing.T, f *browseFixture, n int) {
	t.Helper()
	f.exec(t, `DELETE FROM asset_chapters`)
	f.exec(t, `WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM n WHERE i<?) INSERT INTO asset_chapters(asset_id,chapter_index,title,start_seconds,end_seconds) SELECT 'asset',i,printf('ch-%04d',i),0,0 FROM n`, n-1)
}

func rsTechnicalSeedSubtitles(t *testing.T, f *browseFixture, n int) {
	t.Helper()
	f.exec(t, `DELETE FROM asset_subtitles`)
	f.exec(t, `WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM n WHERE i<?) INSERT INTO asset_subtitles(id,asset_id,origin,locator,stream_index,format,language,title,is_default,is_forced,reason,hearing_impaired) SELECT printf('sub-%04d',i),'asset','embedded',printf('loc-%04d',i),0,'srt','eng',printf('bulk-%04d',i),0,0,'',0 FROM n`, n-1)
}

func rsTechnicalSeedDiscTitles(t *testing.T, f *browseFixture, n int) {
	t.Helper()
	f.exec(t, `UPDATE direct_memberships SET role='owner' WHERE account_id='a'`)
	f.exec(t, `DELETE FROM inventory_disc_titles`)
	f.exec(t, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<?) INSERT INTO inventory_disc_titles(object_id,title_key,selection_token,title_number,disc_order,duration_ms,angle_count) SELECT 'tech-object',printf('pl:%d',i),printf('sel-%d',i),i,0,60000,1 FROM n`, n)
}

func rsTechnicalSeedDiscChapters(t *testing.T, f *browseFixture, n int) {
	t.Helper()
	f.exec(t, `UPDATE direct_memberships SET role='owner' WHERE account_id='a'`)
	f.exec(t, `DELETE FROM inventory_disc_chapters`)
	f.exec(t, `WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM n WHERE i<?) INSERT INTO inventory_disc_chapters(object_id,title_key,chapter_order,start_ms) SELECT 'tech-object','pl:1',i,0 FROM n`, n-1)
}

// TestRSTechnicalKeyInventoryExactBoundary pins the per-family key lookahead: a
// full 4096-key inventory is accepted and its last key decodes, while one extra
// key is the family's typed capacity refusal, never a 4096-row truncated answer
// and never a read-window budget refusal.
func TestRSTechnicalKeyInventoryExactBoundary(t *testing.T) {
	for name, c := range map[string]struct {
		seed func(*testing.T, *browseFixture, int)
		load func(*RSTechnicalWork) bool
		last func(*RSTechnicalWork) bool
	}{
		"stream": {
			seed: rsTechnicalSeedStreams,
			load: func(w *RSTechnicalWork) bool { return w.family == "stream" && len(w.keys) == rsTechnicalRows },
			last: func(w *RSTechnicalWork) bool {
				return len(w.facts.Streams) == 2 && w.facts.Streams[1].Index == 4095 && w.facts.Streams[1].Title == "st-4095"
			},
		},
		"chapter": {
			seed: rsTechnicalSeedChapters,
			load: func(w *RSTechnicalWork) bool { return w.family == "chapter" && len(w.keys) == rsTechnicalRows },
			last: func(w *RSTechnicalWork) bool {
				return len(w.facts.Chapters) == 2 && w.facts.Chapters[1].Index == 4095 && w.facts.Chapters[1].Title == "ch-4095"
			},
		},
		"subtitle": {
			seed: rsTechnicalSeedSubtitles,
			load: func(w *RSTechnicalWork) bool { return w.family == "subtitle" && len(w.keys) == rsTechnicalRows },
			last: func(w *RSTechnicalWork) bool {
				return len(w.facts.Subtitles) == 2 && w.facts.Subtitles[1].Title == "bulk-4095"
			},
		},
		"disctitle": {
			seed: rsTechnicalSeedDiscTitles,
			load: func(w *RSTechnicalWork) bool { return w.family == "disctitle" && len(w.keys) == rsTechnicalRows },
			last: func(w *RSTechnicalWork) bool {
				return len(w.facts.Disc.Titles) == 2 && w.facts.Disc.Titles[1].Number == 4096
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Run("4096", func(t *testing.T) {
				f, r := rsTechnicalFixture(t)
				c.seed(t, f, rsTechnicalRows)
				w := rsTechnicalBegin(t, f, r, RSSelectedTechnicalInput{})
				rsTechnicalBoundaryUntil(t, r, f, w, name, func() bool { return c.load(w) })
				// The inventory is complete. Decode its last key without walking
				// 4096 per-key polls, and require the exact boundary row.
				w.index = len(w.keys) - 1
				if _, e := rsTechnicalBoundaryPoll(t, r, f, w); e != nil {
					t.Fatalf("%s: last key %v", name, e)
				}
				if !c.last(w) {
					t.Fatalf("%s: last boundary row mismatch: %+v", name, w.facts)
				}
			})
			t.Run("4097", func(t *testing.T) {
				f, r := rsTechnicalFixture(t)
				c.seed(t, f, rsTechnicalRows+1)
				w := rsTechnicalBegin(t, f, r, RSSelectedTechnicalInput{})
				rsTechnicalBoundaryRefusal(t, r, f, w, name)
				if len(w.keys) != 0 {
					t.Fatalf("%s: truncated %d-key inventory retained", name, len(w.keys))
				}
			})
		})
	}
}

// TestRSTechnicalDiscChaptersExactBoundary pins the chapter count lookahead for
// one owner disc title: 4096 chapters is a complete count, 4097 is a typed
// refusal rather than a silently capped 4096.
func TestRSTechnicalDiscChaptersExactBoundary(t *testing.T) {
	t.Run("4096", func(t *testing.T) {
		f, r := rsTechnicalFixture(t)
		rsTechnicalSeedDiscChapters(t, f, rsTechnicalRows)
		w := rsTechnicalBegin(t, f, r, RSSelectedTechnicalInput{})
		rsTechnicalBoundaryUntil(t, r, f, w, "discchapters", func() bool {
			return w.facts.Disc != nil && w.facts.Disc.Chapters[1] != 0
		})
		if got := w.facts.Disc.Chapters[1]; got != rsTechnicalRows {
			t.Fatalf("chapter count %d want %d", got, rsTechnicalRows)
		}
	})
	t.Run("4097", func(t *testing.T) {
		f, r := rsTechnicalFixture(t)
		rsTechnicalSeedDiscChapters(t, f, rsTechnicalRows+1)
		w := rsTechnicalBegin(t, f, r, RSSelectedTechnicalInput{})
		rsTechnicalBoundaryRefusal(t, r, f, w, "discchapters")
		if _, ok := w.facts.Disc.Chapters[1]; ok {
			t.Fatalf("truncated chapter count retained: %d", w.facts.Disc.Chapters[1])
		}
	})
}

// TestRSTechnicalStickyFailureKeepsErrorClass holds the first failure's class on
// later polls of the same job; a failed preparation is not bad input.
func TestRSTechnicalStickyFailureKeepsErrorClass(t *testing.T) {
	f, r := rsTechnicalFixture(t)
	w := rsTechnicalBegin(t, f, r, RSSelectedTechnicalInput{})
	if p, e := rsTechnicalPoll(t, r, f, w); e != nil || !p.Preparing {
		t.Fatalf("bind %+v %v", p, e)
	}
	f.exec(t, `UPDATE asset_stream_facts SET revision=revision+1 WHERE asset_id='asset'`)
	for n := 0; n < 3; n++ {
		if _, e := rsTechnicalPoll(t, r, f, w); !errors.Is(e, ErrStale) {
			t.Fatalf("poll %d reclassified the failure: %v", n, e)
		}
	}
}
