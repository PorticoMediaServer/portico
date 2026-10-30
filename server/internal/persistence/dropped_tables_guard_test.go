package persistence

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The catalogue's old tables were replaced by the compact tables when the
// schema was squashed into one baseline. A statement that still names one fails
// only when it runs, so this reads every string literal in the server's
// production code and fails on SQL that names a dropped catalogue table or a
// legacy id column.
func TestNoProductionSQLNamesADroppedCatalogueTable(t *testing.T) {
	dropped := `items|shows|seasons|episodes|artists|albums|songs|song_artists|books|book_files|book_chapters|item_assets|assets|` +
		`metadata_genres|metadata_credits|metadata_credit_people|people|people_credits|people_names|collections|collection_items|collection_order|` +
		`playlists|playlist_entries|item_manual_metadata|item_visibility|item_asset_visibility|item_visibility_dirty|search_documents|search_titles|` +
		`browse_entity_rows|browse_entity_membership|browse_entity_buckets|home_item_buckets|movie_counts|recent_counts|movie_related_facets|` +
		`catalog_item_attributes|catalog_source_dirty|catalog_projection_state|catalog_projection_scheduler|catalog_native_dirty|catalog_public_identities|` +
		`audio_book_metadata|audio_album_identity|people_generation`
	statement := regexp.MustCompile(`(?i)\b(FROM|JOIN|INTO|UPDATE|TABLE)\s+(` + dropped + `)\b`)
	legacy := regexp.MustCompile(`\blegacy_id\b|catalog_\w+_legacy\b`)
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasPrefix(d.Name(), "zz_local_") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		checked++
		rel, _ := filepath.Rel(root, path)
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			text, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			if m := statement.FindString(text); m != "" {
				t.Errorf("%s: SQL names a dropped catalogue table: %q", rel, m)
			}
			if m := legacy.FindString(text); m != "" {
				t.Errorf("%s: SQL names a legacy id column: %q", rel, m)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 500 {
		t.Fatalf("only %d files were checked; the walk is broken", checked)
	}
}
