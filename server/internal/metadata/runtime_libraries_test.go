package metadata

import (
	"context"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"testing"
)

func TestMetadataLibraryChangesRollbackWithTheGlobalDocument(t *testing.T) {
	svc, _, _ := screenFixture(t)
	ctx := context.Background()
	before, err := svc.MetadataSettings(ctx)
	if err != nil || len(before.Libraries) == 0 {
		t.Fatal(before, err)
	}
	row := before.Libraries[0]
	local := AgentLocal
	change := MetadataLibraryChange{ID: row.ID, ExpectedRevision: row.Revision, Agent: &local}
	_, err = svc.UpdateMetadataSettings(ctx, MetadataSettingsUpdate{ExpectedRevision: before.Revision, LocalArtworkLimitBytes: before.LocalArtworkLimitBytes, IncludeAdult: !before.IncludeAdult, Libraries: []MetadataLibraryChange{change, change}}, nil)
	if !errors.Is(err, ErrRepairInput) {
		t.Fatal("duplicate row accepted", err)
	}
	after, err := svc.MetadataSettings(ctx)
	if err != nil || after.Revision != before.Revision || after.IncludeAdult != before.IncludeAdult || after.Libraries[0].Agent != row.Agent || after.Libraries[0].Revision != row.Revision {
		t.Fatal("partial library/global write escaped rollback", after, err)
	}
	change.ExpectedRevision = "0000000000000000000000000000000000000000000000000000000000000000"
	_, err = svc.UpdateMetadataSettings(ctx, MetadataSettingsUpdate{ExpectedRevision: before.Revision, LocalArtworkLimitBytes: before.LocalArtworkLimitBytes, Libraries: []MetadataLibraryChange{change}}, nil)
	if !errors.Is(err, ErrScreenConflict) {
		t.Fatal("stale library row accepted", err)
	}
}

func TestOptionalMetadataLibraryProjectionCannotDisableLegacyGlobalSettings(t *testing.T) {
	svc, db, _ := screenFixture(t)
	publicationExec(t, db, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<513) INSERT INTO libraries(id,name,kind,root) SELECT 'projection-'||i,'Projection '||i,'movie','/synthetic/projection-'||i FROM n`)
	ctx := context.Background()
	before, err := svc.MetadataSettings(ctx)
	if err != nil || before.LibrariesComplete == nil || *before.LibrariesComplete || len(before.Libraries) != 0 {
		t.Fatal("oversized projection blocked or misrepresented global read", before, err)
	}
	after, err := svc.UpdateMetadataSettings(ctx, MetadataSettingsUpdate{ExpectedRevision: before.Revision, LocalArtworkLimitBytes: before.LocalArtworkLimitBytes, IncludeAdult: !before.IncludeAdult}, nil)
	if err != nil || after.IncludeAdult == before.IncludeAdult || after.LibrariesComplete == nil || *after.LibrariesComplete {
		t.Fatal("legacy global write blocked", after, err)
	}
}

func TestMetadataLibraryCASNoticesFirstPreferenceMaterialization(t *testing.T) {
	svc, db, _ := screenFixture(t)
	ctx := context.Background()
	initial, err := svc.MetadataSettings(ctx)
	if err != nil || len(initial.Libraries) == 0 {
		t.Fatal(initial, err)
	}
	id := initial.Libraries[0].ID
	publicationExec(t, db, `DELETE FROM library_preferences WHERE library_id=?`, id)
	before, err := svc.MetadataSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := dbwork.Begin(ctx, db, dbwork.ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback()
	prefs, err := catalog.LibraryPreferencesTx(ctx, gate.Tx(), id)
	if err != nil {
		t.Fatal(err)
	}
	prefs.SortIgnoreArticles = !prefs.SortIgnoreArticles
	if err = catalog.ApplyLibraryPreferencesTx(ctx, gate.Tx(), id, prefs); err != nil {
		t.Fatal(err)
	}
	if err = gate.Commit(); err != nil {
		t.Fatal(err)
	}
	after, err := svc.MetadataSettings(ctx)
	if err != nil || after.Libraries[0].Revision == before.Libraries[0].Revision {
		t.Fatal("first preference insert did not retire row CAS", after, err)
	}
	local := AgentLocal
	_, err = svc.UpdateMetadataSettings(ctx, MetadataSettingsUpdate{ExpectedRevision: before.Revision, LocalArtworkLimitBytes: before.LocalArtworkLimitBytes, Libraries: []MetadataLibraryChange{{ID: id, ExpectedRevision: before.Libraries[0].Revision, Agent: &local}}}, nil)
	if !errors.Is(err, ErrScreenConflict) {
		t.Fatal("first insert survived stale metadata row", err)
	}
}
