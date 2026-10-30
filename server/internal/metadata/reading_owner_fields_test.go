package metadata

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
)

func readingOwnerFixture(t *testing.T, kind compactcatalog.Kind, libraryKind string) (*Service, *catalogtest.Catalog, RepairTarget) {
	t.Helper()
	c := catalogtest.Open(t)
	root := t.TempDir()
	library := c.Library("read", "Read", libraryKind, root)
	path := filepath.Join(root, "Original.pdf")
	if err := os.WriteFile(path, readingtest.PDF(), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	item := c.Entity(compactcatalog.Entity{Library: library, Kind: kind, Key: "reading:original", Title: "Original", Added: "2026-10-23T00:00:00Z"}, nil)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: "pdf"})
		if err != nil {
			return err
		}
		if err = compactcatalog.LinkAssetTx(ctx, tx, item.ID, asset, compactcatalog.Link{}); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO reading_publications(item_id,asset_id,format) VALUES(?,?,'pdf')`, item.ID, asset)
		return err
	})
	c.Drain()
	return New(c.DB, "test-token"), c, RepairTarget{Kind: "item", ID: item.Public}
}

func TestReadingAttributionOwnerEditorUsesRealNonPlayableItems(t *testing.T) {
	for _, tc := range []struct {
		kind    compactcatalog.Kind
		library string
	}{{compactcatalog.Ebook, "book"}, {compactcatalog.Comic, "comic"}} {
		t.Run(tc.library, func(t *testing.T) {
			s, c, target := readingOwnerFixture(t, tc.kind, tc.library)
			ctx := context.Background()
			before := dbwork.ChangingCommits()
			state, err := s.RepairState(ctx, target, allowAll)
			if err != nil {
				t.Fatal(err)
			}
			if dbwork.ChangingCommits() != before {
				t.Fatal("editor read adopted state")
			}
			if len(state.ArtworkRoles) != 1 || state.ArtworkRoles[0] != "cover" {
				t.Fatal("Reading editor must offer exactly its checked owner-cover role", state.ArtworkRoles)
			}
			if len(state.Artwork.Candidates) != 0 || len(state.Artwork.Jobs) != 0 {
				t.Fatal("scalar editor advertised automatic artwork work")
			}
			if state.Snapshot.Fields["author"].Value != "" || state.Snapshot.Fields["series"].Value != "" {
				t.Fatal("invented attribution")
			}
			author, series, number := "A & B", "Dune", "3.5"
			after, err := s.Repair(ctx, target, RepairCommand{ExpectedRevision: state.Revision, Action: "edit", Fields: map[string]RepairFieldEdit{"author": {Value: &author}, "series": {Value: &series}, "seriesNumber": {Value: &number}}}, editorActor(), allowAll)
			if err != nil {
				t.Fatal(err)
			}
			for field, value := range map[string]string{"author": author, "series": series, "seriesNumber": number} {
				got := after.Snapshot.Fields[field]
				if got.Value != value || got.Source != "owner" || !got.Locked {
					t.Fatal("owner attribution not retained", field, got)
				}
			}
			if _, err = s.Repair(ctx, target, RepairCommand{ExpectedRevision: state.Revision, Action: "edit", Fields: map[string]RepairFieldEdit{"author": {Value: &author}}}, editorActor(), allowAll); !errors.Is(err, ErrRepairConflict) {
				t.Fatal("stale write", err)
			}
			for _, bad := range []string{"NaN", "Inf", "-1", "0x1p2", "1e999"} {
				if _, err = s.Repair(ctx, target, RepairCommand{ExpectedRevision: after.Revision, Action: "edit", Fields: map[string]RepairFieldEdit{"seriesNumber": {Value: &bad}}}, editorActor(), allowAll); !errors.Is(err, ErrRepairInput) {
					t.Fatal("invalid decimal", bad, err)
				}
			}
			empty := ""
			cleared, err := s.Repair(ctx, target, RepairCommand{ExpectedRevision: after.Revision, Action: "edit", Fields: map[string]RepairFieldEdit{"author": {Value: &empty}, "series": {Value: &empty}, "seriesNumber": {Value: &empty}}}, editorActor(), allowAll)
			if err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"author", "series", "seriesNumber"} {
				got := cleared.Snapshot.Fields[field]
				if got.Value != "" || got.Locked || got.Source == "owner" {
					t.Fatal("clear retained owner fact", field, got)
				}
			}
			var people, audio, heads int
			if err = c.DB.QueryRow(`SELECT (SELECT count(*) FROM catalog_people),(SELECT count(*) FROM catalog_books),(SELECT count(*) FROM metadata_publication_heads WHERE item_id=?)`, c.ID(target.ID)).Scan(&people, &audio, &heads); err != nil || people != 0 || audio != 0 || heads != 0 {
				t.Fatal("scalar write adopted a person/audio/publication engine", people, audio, heads, err)
			}
			if _, err = s.Repair(ctx, target, RepairCommand{ExpectedRevision: cleared.Revision, Action: "identify", Confirm: true}, editorActor(), allowAll); !errors.Is(err, ErrRepairInput) {
				t.Fatal("provider action admitted", err)
			}
		})
	}
}

func TestReadingAttributionOwnerAuthorityAndBulkRemainFenced(t *testing.T) {
	s, _, target := readingOwnerFixture(t, compactcatalog.Ebook, "book")
	ctx := context.Background()
	state, err := s.RepairState(ctx, target, allowAll)
	if err != nil {
		t.Fatal(err)
	}
	value := "Accepted owner"
	denied := errors.New("current owner refused")
	if _, err = s.Repair(ctx, target, RepairCommand{ExpectedRevision: state.Revision, Action: "edit", Fields: map[string]RepairFieldEdit{"author": {Value: &value}}}, editorActor(), func(*sql.Tx) error { return denied }); !errors.Is(err, denied) {
		t.Fatal(err)
	}
	receipt, err := s.BulkEdit(ctx, BulkEdit{OperationID: "reading-owner-attribution", Targets: []BulkTarget{{Kind: target.Kind, ID: target.ID, ExpectedRevision: state.Revision}}, Fields: map[string]RepairFieldEdit{"author": {Value: &value}}}, editorActor(), allowAll)
	if err != nil {
		t.Fatal(receipt, err)
	}
	after, err := s.RepairState(ctx, target, allowAll)
	if err != nil || after.Snapshot.Fields["author"].Value != value || !after.Snapshot.Fields["author"].Locked {
		t.Fatal("bulk did not use the owner writer", after.Snapshot.Fields, err)
	}
}

// A cleared Reading attribution falls back to the source value the writer
// actually holds, not to a stale recorded automatic column, and "use
// automatic" restores that same value.
func TestReadingOwnerClearFallsBackToTheCurrentSourceValue(t *testing.T) {
	s, c, target := readingOwnerFixture(t, compactcatalog.Ebook, "book")
	ctx := context.Background()
	seedReadingSourceValues(t, c, target)
	// An unlocked owner row left behind by an earlier clear, carrying a stale
	// recorded automatic value.
	c.Exec(`INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) VALUES('item',?,'author','','Stale Author',0,'owner','now')`, c.ID(target.ID))
	state, err := s.RepairState(ctx, target, allowAll)
	if err != nil {
		t.Fatal(err)
	}
	author := state.Snapshot.Fields["author"]
	if author.Value != "Genuine Author" || author.Automatic != "Genuine Author" || author.Locked || author.Source != "automatic" {
		t.Fatal("cleared attribution did not fall back to the current source value", author)
	}
	// A locked owner value still wins over the source.
	owner := "Owner Author"
	locked := editField(t, s, target, "author", RepairFieldEdit{Value: &owner})
	if got := locked.Snapshot.Fields["author"]; got.Value != owner || got.Source != "owner" || !got.Locked || got.Automatic != "Genuine Author" {
		t.Fatal("owner value did not win", got)
	}
	// Use automatic restores the genuine source value and releases the lock.
	restored := editField(t, s, target, "author", RepairFieldEdit{Automatic: true})
	if got := restored.Snapshot.Fields["author"]; got.Value != "Genuine Author" || got.Locked || got.Source != "automatic" {
		t.Fatal("use automatic did not restore the source value", got)
	}
	// An explicit clear keeps the same fallback.
	empty := ""
	cleared := editField(t, s, target, "author", RepairFieldEdit{Value: &empty})
	if got := cleared.Snapshot.Fields["author"]; got.Value != "Genuine Author" || got.Locked || got.Source != "automatic" {
		t.Fatal("explicit clear did not fall back to the source value", got)
	}
	// The fallback is the writer's current value, read in the same transaction,
	// even when no publication has run since it changed.
	c.Exec(`UPDATE reading_source_values SET text_value='Updated Author' WHERE item_id=? AND field='author'`, c.ID(target.ID))
	state, err = s.RepairState(ctx, target, allowAll)
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Snapshot.Fields["author"]; got.Value != "Updated Author" || got.Automatic != "Updated Author" {
		t.Fatal("fallback served a stale source value", got)
	}
}

// seedReadingSourceValues records the primary-version source facts the automatic
// seam reads: one live inventory object and an author/series/number row.
func seedReadingSourceValues(t *testing.T, c *catalogtest.Catalog, target RepairTarget) {
	t.Helper()
	entity := c.ID(target.ID)
	var asset int64
	var token string
	if err := c.DB.QueryRow(`SELECT p.asset_id,a.token FROM reading_publications p JOIN catalog_assets a ON a.id=p.asset_id WHERE p.item_id=?`, entity).Scan(&asset, &token); err != nil {
		t.Fatal(err)
	}
	var source, incarnation string
	if err := c.DB.QueryRow(`SELECT s.id,s.incarnation FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN catalog_entities e ON e.library_id=cl.id WHERE e.id=?`, entity).Scan(&source, &incarnation); err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('reading-object',?,?,?,'Original.pdf','r1','{}',1,1)`, source, token, incarnation)
	c.Exec(`INSERT INTO reading_source_values(item_id,asset_id,field,text_value,source,inventory_object_id,source_revision) VALUES(?,?,'author','Genuine Author','file-metadata','reading-object','r1')`, entity, asset)
	c.Exec(`INSERT INTO reading_path_head(item_id,asset_id,publisher_sequence,object_id,source_revision,series,series_source,series_number) VALUES(?,?,1,'reading-object','r1','Genuine Series','folder',2.5)`, entity, asset)
}

// The canonical source is the value even when no owner row exists at all: the
// editor must show author/series/seriesNumber from the current source, and a
// genuine zero number is a value rather than absence.
func TestReadingOwnerFieldsReadCanonicalValuesWithoutOwnerRows(t *testing.T) {
	s, c, target := readingOwnerFixture(t, compactcatalog.Ebook, "book")
	ctx := context.Background()
	seedReadingSourceValues(t, c, target)
	c.Exec(`UPDATE reading_path_head SET series_number=0 WHERE item_id=?`, c.ID(target.ID))
	var rows int
	if err := c.DB.QueryRow(`SELECT count(*) FROM metadata_owner_fields WHERE kind='item' AND entity_id=?`, c.ID(target.ID)).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("fixture has owner rows: %d %v", rows, err)
	}
	state, err := s.RepairState(ctx, target, allowAll)
	if err != nil {
		t.Fatal(err)
	}
	for field, want := range map[string]string{"author": "Genuine Author", "series": "Genuine Series", "seriesNumber": "0"} {
		got := state.Snapshot.Fields[field]
		if got.Value != want || got.Automatic != want || got.Locked {
			t.Fatalf("%s without an owner row: %+v", field, got)
		}
	}
}

// When the canonical source is absent, withdrawn or no longer current, the
// effective value and the automatic value are genuinely empty. A stale recorded
// automatic column must not resurrect them; only a locked owner value stands.
func TestReadingOwnerCanonicalAbsenceClearsStaleAutomatic(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		prepare func(*catalogtest.Catalog, RepairTarget)
	}{
		{"missing", func(*catalogtest.Catalog, RepairTarget) {}},
		{"withdrawn", func(c *catalogtest.Catalog, target RepairTarget) {
			seedReadingSourceValues(t, c, target)
			c.Exec(`DELETE FROM reading_source_values WHERE item_id=?`, c.ID(target.ID))
			c.Exec(`DELETE FROM reading_path_head WHERE item_id=?`, c.ID(target.ID))
		}},
		{"current-source-invalid", func(c *catalogtest.Catalog, target RepairTarget) {
			seedReadingSourceValues(t, c, target)
			c.Exec(`UPDATE inventory_objects SET retired=1 WHERE id='reading-object'`)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c, target := readingOwnerFixture(t, compactcatalog.Ebook, "book")
			c.Exec(`INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) VALUES('item',?,'author','','Stale Author',0,'owner','now')`, c.ID(target.ID))
			tc.prepare(c, target)
			state, err := s.RepairState(ctx, target, allowAll)
			if err != nil {
				t.Fatal(err)
			}
			if got := state.Snapshot.Fields["author"]; got.Value != "" || got.Automatic != "" || got.Locked {
				t.Fatalf("canonical absence kept %+v", got)
			}
			// A locked owner value is kept, but not the stale automatic value.
			c.Exec(`UPDATE metadata_owner_fields SET value='Owner Author',locked=1 WHERE kind='item' AND entity_id=? AND field='author'`, c.ID(target.ID))
			state, err = s.RepairState(ctx, target, allowAll)
			if err != nil {
				t.Fatal(err)
			}
			if got := state.Snapshot.Fields["author"]; got.Value != "Owner Author" || got.Automatic != "" || !got.Locked || got.Source != "owner" {
				t.Fatalf("locked owner value with canonical absence: %+v", got)
			}
			// Use automatic clears the effective value and releases the lock.
			restored := editField(t, s, target, "author", RepairFieldEdit{Automatic: true})
			if got := restored.Snapshot.Fields["author"]; got.Value != "" || got.Automatic != "" || got.Locked {
				t.Fatalf("use automatic with canonical absence: %+v", got)
			}
			// An explicit clear keeps canonical absence.
			empty := ""
			cleared := editField(t, s, target, "author", RepairFieldEdit{Value: &empty})
			if got := cleared.Snapshot.Fields["author"]; got.Value != "" || got.Automatic != "" || got.Locked {
				t.Fatalf("explicit clear with canonical absence: %+v", got)
			}
		})
	}
}

func TestReadingRepeatedOwnerEditKeepsAttributionInputAndUpdatesAccounting(t *testing.T) {
	_, c, target := readingOwnerFixture(t, compactcatalog.Ebook, "book")
	entity := c.ID(target.ID)
	current := RepairField{Value: "Same Author", Automatic: "", Locked: true}
	value := current.Value
	spec := RepairFieldSpec{Type: "text"}
	write := func(actor, at string) {
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			return applyRepairField(ctx, tx, target, "author", spec, current, RepairFieldEdit{Value: &value}, actor, at)
		})
	}
	write("first-owner", "first-time")
	var before, after int64
	if err := c.DB.QueryRow(`SELECT input_generation FROM reading_attribution_state WHERE library_id=(SELECT library_id FROM catalog_entities WHERE id=?)`, entity).Scan(&before); err != nil {
		t.Fatal(err)
	}
	write("second-owner", "second-time")
	if err := c.DB.QueryRow(`SELECT input_generation FROM reading_attribution_state WHERE library_id=(SELECT library_id FROM catalog_entities WHERE id=?)`, entity).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("same effective owner edit invalidated: %d -> %d", before, after)
	}
	var actor, at string
	if err := c.DB.QueryRow(`SELECT actor,observed_at FROM metadata_owner_fields WHERE kind='item' AND entity_id=? AND field='author'`, entity).Scan(&actor, &at); err != nil || actor != "second-owner" || at != "second-time" {
		t.Fatalf("accounting=%s/%s err=%v", actor, at, err)
	}
	value = "Changed Author"
	write("second-owner", "third-time")
	if err := c.DB.QueryRow(`SELECT input_generation FROM reading_attribution_state WHERE library_id=(SELECT library_id FROM catalog_entities WHERE id=?)`, entity).Scan(&after); err != nil || after <= before {
		t.Fatalf("real owner change did not invalidate: %d -> %d err=%v", before, after, err)
	}
}
