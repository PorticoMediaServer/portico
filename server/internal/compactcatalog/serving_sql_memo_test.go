package compactcatalog

import (
	"fmt"
	"testing"
)

// The memo returns exactly what the rewrite returns, and stops growing at its cap.
func TestServingSQLMemoMatchesTheRewriteAndIsBounded(t *testing.T) {
	queries := []string{
		`SELECT r.id FROM catalog_browse_rows r INDEXED BY catalog_browse_rows_sort WHERE r.library_id=? ORDER BY r.sort_key COLLATE NOCASE`,
		`SELECT id FROM catalog_browse_rows WHERE library_id=? ORDER BY COALESCE(added_text,'')`,
		`SELECT id FROM libraries WHERE id=?`,
	}
	for _, query := range queries {
		want := servingSQL(query)
		for range 3 {
			if got := ServingSQL(query); got != want {
				t.Fatalf("memoized rewrite of %q = %q, want %q", query, got, want)
			}
		}
	}
	for i := range servingSQLMemoLimit + 100 {
		query := fmt.Sprintf(`SELECT id FROM catalog_browse_rows WHERE id=%d`, i)
		if got, want := ServingSQL(query), servingSQL(query); got != want {
			t.Fatalf("rewrite %d differs past the cap", i)
		}
	}
	if count := servingSQLMemo.count.Load(); count > servingSQLMemoLimit {
		t.Fatalf("memo grew to %d entries, cap %d", count, servingSQLMemoLimit)
	}
}

func BenchmarkServingSQL(b *testing.B) {
	query := `SELECT r.id FROM catalog_browse_rows r INDEXED BY catalog_browse_rows_sort WHERE r.library_id=? ORDER BY r.sort_key COLLATE NOCASE`
	b.Run("memo", func(b *testing.B) {
		for range b.N {
			ServingSQL(query)
		}
	})
	b.Run("rewrite", func(b *testing.B) {
		for range b.N {
			servingSQL(query)
		}
	})
}
