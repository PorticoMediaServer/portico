package metadata

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
	"testing"
)

func TestScannedNameEvidencePrefersFileAndEmbeddedOverConflictingFolder(t *testing.T) {
	_, db, _, _ := integrationScreen(t)
	item := integrationMovie(t, db)
	integrationExec(t, db, `UPDATE catalog_assets SET path='/owned/Folder (2000) {tmdb-12}/Heat (1995).mkv' WHERE id=?`, item.Asset)
	var source string
	if err := db.QueryRow(`SELECT id FROM library_sources ORDER BY id LIMIT 1`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	integrationExec(t, db, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) SELECT 'named',id,?,incarnation,'Folder (2000) {tmdb-12}/Heat (1995).mkv','named-revision','{}',1,1 FROM library_sources WHERE id=?`, item.Token, source)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	b, err := readScreenBase(context.Background(), tx, "item", item.Public)
	if err != nil {
		t.Fatal(err)
	}
	b.Title = "Heat"
	b.Year = 1995
	names, year, ids := screenQueries(b)
	if len(names) != 1 || names[0] != "Heat" || year != 1995 || len(ids) != 0 {
		t.Fatalf("file evidence: %v %d %v", names, year, ids)
	}
	if len(b.Evidence) < 2 || b.Evidence[0].Source != string(naming.FileName) {
		t.Fatal("folder/file provenance lost", b.Evidence)
	}
	b.Evidence = append([]MatchEvidence{{Title: "Embedded title", Source: string(naming.EmbeddedMetadata), Year: 1995, Confidence: .99, IDs: []metadataprovider.ScreenID{{Provider: "tmdb", Type: "movie", ID: "949"}}}}, b.Evidence...)
	names, _, ids = screenQueries(b)
	if len(names) == 0 || names[0] != "Embedded title" || len(ids) != 1 || ids[0].ID != "949" {
		t.Fatalf("embedded evidence: %v %v", names, ids)
	}
}
