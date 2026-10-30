package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
)

func TestReadingPrivateReappearanceDoesNotSpliceNewSourcePosition(t *testing.T) {
	r := newReadingFixture(t, "book", "epub")
	ctx := context.Background()
	session, document, err := r.s.Prepare(ctx, r.p, r.item, r.source)
	if err != nil {
		t.Fatal(err)
	}
	loc := reading.Location{Format: "epub", CFI: "epubcfi(/6/4!/4/2:0)", Section: "EPUB/chapter.xhtml", Progression: .4}
	if _, _, err = r.s.SaveSessionProgress(ctx, r.p, session.ID, 0, loc); err != nil {
		t.Fatal(err)
	}
	if err = r.f.cat.RemoveTitle(ctx, r.item); err != nil {
		t.Fatal(err)
	}
	if _, err = r.s.OpenSession(ctx, r.p, session.ID); err == nil {
		t.Fatal("removed title reopened")
	}
	readingScan(t, r.f, r.library)
	var item, token string
	if err = r.f.db.QueryRow(`SELECT pid(e.public_id),a.token FROM reading_publications p JOIN catalog_entities e ON e.id=p.item_id JOIN catalog_assets a ON a.id=p.asset_id WHERE a.path=?`, r.path).Scan(&item, &token); err != nil {
		t.Fatal(err)
	}
	if item == r.item || token == r.source {
		t.Fatal("removed identities were reused", item, token)
	}
	fresh, err := r.s.Progress(ctx, r.p, item, token)
	if err != nil || fresh != nil {
		t.Fatal("new file inherited old CFI", fresh, err)
	}
	var oldRevision, cfi string
	if err = r.f.db.QueryRow(`SELECT p.source_revision,p.cfi FROM retained_reading_progress p JOIN retained_title_state s ON s.id=p.state_id WHERE s.profile_id=? AND p.source_id=?`, identity.PersonalKey(r.p.Viewer), r.source).Scan(&oldRevision, &cfi); err != nil || oldRevision != document.Selection.Revision || cfi != loc.CFI {
		t.Fatal(oldRevision, cfi, err)
	}
}

func TestReadingPrivateActualVersionSelectionAndCASAreIndependent(t *testing.T) {
	r := newReadingFixture(t, "book", "pdf")
	ctx := context.Background()
	alternate := filepath.Join(filepath.Dir(r.path), "Alternate publication.epub")
	if err := os.WriteFile(alternate, readingtest.EPUB(), 0600); err != nil {
		t.Fatal(err)
	}
	readingScan(t, r.f, r.library)
	var item, oldItem, alternateToken string
	var entity, asset int64
	if err := r.f.db.QueryRow(`SELECT e.id,pid(e.public_id) FROM catalog_entities e WHERE e.public_id=pid_blob(?)`, r.item).Scan(&entity, &item); err != nil {
		t.Fatal(err)
	}
	if err := r.f.db.QueryRow(`SELECT a.id,a.token,pid(e.public_id) FROM reading_publications p JOIN catalog_entities e ON e.id=p.item_id JOIN catalog_assets a ON a.id=p.asset_id WHERE a.path=?`, alternate).Scan(&asset, &alternateToken, &oldItem); err != nil {
		t.Fatal(err)
	}
	// Attach the genuinely scanned alternate through the canonical writer. No
	// inventory/source/permissions/revision facts are invented or replaced.
	err := dbwork.WithWriteTx(ctx, r.f.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		var previous int64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)`, oldItem).Scan(&previous); err != nil {
			return err
		}
		if err := compactcatalog.UnlinkAssetTx(ctx, tx, previous, asset); err != nil {
			return err
		}
		if err := compactcatalog.LinkAssetTx(ctx, tx, entity, asset, compactcatalog.Link{}); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO reading_publications(item_id,asset_id,format) VALUES(?,?,'epub')`, entity, asset)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	versions, err := r.s.Versions(ctx, r.p, item)
	if err != nil || len(versions) != 2 {
		t.Fatal(versions, err)
	}
	pdfSession, pdf, err := r.s.Prepare(ctx, r.p, item, r.source)
	if err != nil {
		t.Fatal(err)
	}
	epubSession, epub, err := r.s.Prepare(ctx, r.p, item, alternateToken)
	if err != nil || epub.Selection.Format != "epub" || pdf.Selection.Format != "pdf" || epub.Selection.Revision == pdf.Selection.Revision {
		t.Fatal(epub, pdf, err)
	}
	// The server's choice follows the reader's latest saved version, else the
	// first available version in asset order.
	chosen := func(want string) {
		t.Helper()
		choice, err := r.s.VersionChoice(ctx, r.p, item)
		if err != nil || choice.Chosen != want {
			t.Fatalf("chosen %q want %q (%v)", choice.Chosen, want, err)
		}
	}
	chosen(versions[0].SourceID)
	page := int64(0)
	if _, _, err = r.s.SaveSessionProgress(ctx, r.p, pdfSession.ID, 0, reading.Location{Format: "pdf", PageIndex: &page, Progression: .2}); err != nil {
		t.Fatal(err)
	}
	chosen(r.source)
	if _, _, err = r.s.SaveSessionProgress(ctx, r.p, epubSession.ID, 0, reading.Location{Format: "epub", CFI: "epubcfi(/6/4!/4/2:0)", Section: "EPUB/chapter.xhtml", Progression: .6}); err != nil {
		t.Fatal(err)
	}
	if _, err = r.f.db.Exec(`UPDATE reading_progress SET observed_at=observed_at+1000 WHERE asset_id=?`, asset); err != nil {
		t.Fatal(err)
	}
	chosen(alternateToken)
	for _, tc := range []struct {
		id       string
		format   string
		progress float64
	}{{pdfSession.ID, "pdf", .2}, {epubSession.ID, "epub", .6}} {
		p, err := r.s.SessionProgress(ctx, r.p, tc.id)
		if err != nil || p == nil || p.Revision != 1 || p.Location.Format != tc.format || p.Location.Progression != tc.progress {
			t.Fatal(p, err)
		}
	}
	continued, err := r.s.Continue(ctx, r.p, r.library, "", 10)
	if err != nil || len(continued.Items) != 1 || continued.Items[0].Item.SourceID != alternateToken || continued.Items[0].Progress.Location.Format != "epub" {
		t.Fatal("continue did not choose the actual most recent version", continued, err)
	}
	if _, _, err = r.s.SaveSessionProgress(ctx, r.p, epubSession.ID, 1, reading.Location{Format: "epub", CFI: "epubcfi(/6/4!/4/2:0)", Section: "EPUB/chapter.xhtml", Progression: 1}); err != nil {
		t.Fatal(err)
	}
	continued, err = r.s.Continue(ctx, r.p, r.library, "", 10)
	if err != nil || len(continued.Items) != 0 {
		t.Fatal("finished latest version fell back to older unfinished version", continued, err)
	}
	if _, err = r.s.Open(ctx, r.p, item, alternateToken, pdf.Selection.Revision); !errors.Is(err, reading.ErrChanged) {
		t.Fatal("mixed version revision", err)
	}
	// Unlink's composite FK removes only that version's publication/position.
	err = dbwork.WithWriteTx(ctx, r.f.db, dbwork.ClassInteractive, func(tx *sql.Tx) error { return compactcatalog.UnlinkAssetTx(ctx, tx, entity, asset) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.s.OpenSession(ctx, r.p, epubSession.ID); err == nil {
		t.Fatal("unlinked version reopened")
	}
	p, err := r.s.SessionProgress(ctx, r.p, pdfSession.ID)
	if err != nil || p == nil || p.Location.Format != "pdf" {
		t.Fatal("sibling version erased", p, err)
	}
}
