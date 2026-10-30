package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// Accepted/materialized-row model proof, not an image decode or worker-loop
// claim. One API call must find a missing tail past the former 1024 prefix.
func TestUnmaterializedArtworkFindsTailLateArrivalsAndCurrentFence(t *testing.T) {
	ctx := context.Background()
	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	item := c.Movie(lib, "/m/Film.mkv", "Film", 2000)
	other := c.Movie(lib, "/m/Other.mkv", "Other", 2001)
	s := New(c.DB)
	source, err := s.LibrarySource(ctx, "m")
	if err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('missing-object',?,?,?,'Film.mkv','r1','{}',1,1)`, source.ID, item.Token, source.Incarnation)
	const prefix = 1100
	key := func(n int) string { return fmt.Sprintf("%064x", n) }
	values := []assets.LocalArtworkCandidate{}
	for n := 1; n <= prefix+1; n++ {
		values = append(values, assets.LocalArtworkCandidate{Slot: "item/poster", Key: key(n), SourceName: fmt.Sprintf("poster-%d.png", n), SourceRank: 1024 + int64(n)})
	}
	write := func() {
		t.Helper()
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			return s.commitInventoryArtworkCandidatesTx(ctx, tx, source, "missing-object", "r1", item.Token, "movie", values)
		})
	}
	write()
	fence := strings.Repeat("f", 64)
	oldFence := strings.Repeat("e", 64)
	materialize := func(numbers ...int) {
		t.Helper()
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			for _, n := range numbers {
				_, err := tx.ExecContext(ctx, `INSERT INTO artwork_candidates(id,kind,entity_id,role,provider,image_id,origin,attribution,source_fence,observed_at) VALUES(?,'item',?,'poster','local',?,?,'Local file',?,'now') ON CONFLICT(id) DO UPDATE SET source_fence=excluded.source_fence`, fmt.Sprintf("materialized-%d", n), item.ID, fmt.Sprint(n), "local:"+key(n), fence)
				if err != nil {
					return err
				}
			}
			return nil
		})
	}
	numbers := make([]int, prefix)
	for i := range numbers {
		numbers[i] = i + 1
	}
	materialize(numbers...)
	read := func(after *LocalArtworkCandidateCursor, limit int) []LocalArtworkCandidateCursor {
		t.Helper()
		tx, err := c.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		page, err := ReadUnmaterializedLocalArtworkCandidatePageTx(ctx, tx, "item", item.ID, "poster", fence, after, limit)
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	expect := func(page []LocalArtworkCandidateCursor, want ...int) {
		t.Helper()
		if len(page) != len(want) {
			t.Fatalf("missing page size %d want %d", len(page), len(want))
		}
		for i, n := range want {
			if page[i].Key != key(n) {
				t.Fatalf("missing entry %d=%+v want key %d", i, page[i], n)
			}
		}
	}
	expect(read(nil, 8), prefix+1)
	// A new worker pass starts at nil, using committed materialization as its
	// durable progress. Lower and middle additions cannot hide behind a hint.
	values = append(values,
		assets.LocalArtworkCandidate{Slot: "item/poster", Key: key(prefix + 2), SourceName: "poster.jpg", SourceRank: 16},
		assets.LocalArtworkCandidate{Slot: "item/poster", Key: key(prefix + 3), SourceName: "poster-0500.png", SourceRank: 1524})
	write()
	expect(read(nil, 8), prefix+2, prefix+3, prefix+1)
	tx, err := c.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	first, err := ReadUnmaterializedLocalArtworkCandidatePageTx(ctx, tx, "item", item.ID, "poster", fence, nil, 1)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	expect(first, prefix+2)
	next, err := ReadUnmaterializedLocalArtworkCandidatePageTx(ctx, tx, "item", item.ID, "poster", fence, &first[0], 1)
	tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	expect(next, prefix+3)
	materialize(prefix+1, prefix+2, prefix+3)
	expect(read(nil, 8))
	// Same origin alone is insufficient: only this exact target/slot/subject,
	// provider and current fence may suppress an admitted candidate.
	c.Exec(`UPDATE artwork_candidates SET source_fence=? WHERE id='materialized-500'`, oldFence)
	for _, row := range []struct {
		id, kind, role, subject, provider, origin string
		entity                                    int64
	}{
		{"other-entity", "item", "poster", "", "local", "local:" + key(500), other.ID},
		{"other-kind", "show", "poster", "", "local", "local:" + key(500), item.ID},
		{"other-role", "item", "backdrop", "", "local", "local:" + key(500), item.ID},
		{"other-subject", "item", "poster", "face", "local", "local:" + key(500), item.ID},
		{"other-provider", "item", "poster", "", "tmdb", "local:" + key(500), item.ID},
		{"other-origin", "item", "poster", "", "local", "local:" + key(500) + "suffix", item.ID},
	} {
		c.Exec(`INSERT INTO artwork_candidates(id,kind,entity_id,role,subject,provider,image_id,origin,attribution,source_fence,observed_at) VALUES(?,?,?,?,?,?,?,?,'Local file',?,'now')`, row.id, row.kind, row.entity, row.role, row.subject, row.provider, row.id, row.origin, fence)
	}
	expect(read(nil, 8), 500)
	materialize(500)
	expect(read(nil, 8))
	c.Exec(`UPDATE artwork_candidates SET source_fence=? WHERE id='materialized-500'`, oldFence)
	c.Exec(`UPDATE inventory_objects SET revision='r2' WHERE id='missing-object'`)
	expect(read(nil, 8))
	c.Exec(`UPDATE inventory_objects SET revision='r1' WHERE id='missing-object'`)
	c.Exec(`UPDATE library_sources SET health='offline' WHERE id=?`, source.ID)
	expect(read(nil, 8))
	c.Exec(`UPDATE library_sources SET health='unknown' WHERE id=?`, source.ID)
	c.Exec(`UPDATE library_sources SET incarnation='different-root' WHERE id=?`, source.ID)
	expect(read(nil, 8))
	c.Exec(`UPDATE library_sources SET incarnation=? WHERE id=?`, source.Incarnation, source.ID)
	c.Exec(`DELETE FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, item.ID, item.Asset)
	expect(read(nil, 8))
}

func TestUnmaterializedArtworkRequiresFenceTransactionAndPageBound(t *testing.T) {
	ctx := context.Background()
	if _, err := ReadUnmaterializedLocalArtworkCandidatePageTx(ctx, nil, "item", 1, "poster", "fence", nil, 8); err == nil {
		t.Fatal("nil transaction accepted")
	}
	c := catalogtest.Open(t)
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := ReadUnmaterializedLocalArtworkCandidatePageTx(ctx, tx, "item", 1, "poster", "", nil, 8); err == nil {
		t.Fatal("unknown fence accepted")
	}
	for _, limit := range []int{0, 129} {
		if _, err := ReadUnmaterializedLocalArtworkCandidatePageTx(ctx, tx, "item", 1, "poster", "fence", nil, limit); err == nil {
			t.Fatal("invalid page bound accepted")
		}
	}
}
