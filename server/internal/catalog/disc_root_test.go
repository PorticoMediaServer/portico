package catalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// A folder that holds BDMV is published as one available asset with the exact
// directory observation, and its analysis is queued like any other title.
func TestDiscRootFolderBecomesOneQueuedTitle(t *testing.T) {
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	library, err := s.Create("Disc root unit", "movie", root)
	if err != nil {
		t.Fatal(err)
	}
	source, err := scanLibrarySource(db.QueryRow(`SELECT `+sourceColumns+` FROM library_sources WHERE library_id=? ORDER BY id LIMIT 1`, library.ID))
	if err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(root, "Film")
	if err = os.MkdirAll(filepath.Join(folder, "BDMV"), 0o700); err != nil {
		t.Fatal(err)
	}
	v, err := storage.LocalInventoryDirectory(storage.InventoryRequest{Root: root, RootIdentity: source.RootIdentity, RelativePath: "Film"})
	if err != nil {
		t.Fatal(err)
	}
	v.Name = "Film"
	job := identity.Token()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES(?,?,'running','unit')`, job, library.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision) VALUES(?,?,?,?,?,0)`, job, source.ID, source.Generation, source.Incarnation, source.RootIdentity); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = s.CommitInventoryDiscRoot(context.Background(), job, "Film", v); err != nil {
		t.Fatal(err)
	}
	var object, asset, state string
	var size, modified int64
	if err = db.QueryRow(`SELECT id,asset_id,state,size,modified_ns FROM inventory_objects WHERE source_id=? AND relative_path='Film'`, source.ID).Scan(&object, &asset, &state, &size, &modified); err != nil {
		t.Fatal(err)
	}
	if state != "available" || size != v.Size || modified != v.ModifiedNS {
		t.Fatalf("object: %s %d %d", state, size, modified)
	}
	var path, container string
	if err = db.QueryRow(`SELECT path,container FROM catalog_assets WHERE token=?`, asset).Scan(&path, &container); err != nil {
		t.Fatal(err)
	}
	if path != folder {
		t.Fatalf("asset path %q", path)
	}
	var queued int
	if err = db.QueryRow(`SELECT count(*) FROM inventory_analysis_queue WHERE job_id=? AND object_id=?`, job, object).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 1 {
		t.Fatalf("analysis queue rows: %d", queued)
	}
}

// The folder's size/mtime is not the title's content revision; the committed
// structure fingerprint is the evidence token.
func TestDiscStructureWitness(t *testing.T) {
	if DiscStructureWitness("") != "" {
		t.Fatal("an unobserved structure produced evidence")
	}
	a, b := DiscStructureWitness("a"), DiscStructureWitness("b")
	if a == "" || a == b || len(a) != len("disc-structure:")+64 {
		t.Fatalf("witness %q %q", a, b)
	}
	if a != DiscStructureWitness("a") {
		t.Fatal("witness is not deterministic")
	}
}
