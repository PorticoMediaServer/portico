package httpapi

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
)

// These are storage/projection tests. The generic editor's real Reading writer
// and the public attribution DTO are independently owned and not implied here.
func TestReadingPrivateOwnerAttributionsUseAcceptedRows(t *testing.T) {
	r := newReadingFixture(t, "book", "pdf")
	ctx := context.Background()
	write := func(field, value string, locked bool) {
		t.Helper()
		_, err := r.f.db.Exec(`INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at)
 SELECT 'item',id,?,?,'',?,?,datetime('now') FROM catalog_entities WHERE public_id=pid_blob(?)
 ON CONFLICT(kind,entity_id,field) DO UPDATE SET value=excluded.value,locked=excluded.locked,actor=excluded.actor,observed_at=excluded.observed_at`, field, value, locked, identity.PersonalKey(r.p.Viewer), r.item)
		if err != nil {
			t.Fatal(err)
		}
	}
	list, err := r.s.List(ctx, r.p, r.library, "", 1)
	if err != nil || len(list.Items) != 1 || list.Items[0].Author != nil || list.Items[0].Series != nil {
		t.Fatalf("absent facts invented: %+v %v", list, err)
	}
	write("author", "A & B", true)
	write("series", "Dune", true)
	write("seriesNumber", "2.5", true)
	list, err = r.s.List(ctx, r.p, r.library, "", 1)
	if err != nil || len(list.Items) != 1 {
		t.Fatal(err)
	}
	row := list.Items[0]
	if row.Author == nil || row.Author.Value != "A & B" || row.Author.Source != compactcatalog.ReadingOwner || row.Series == nil || row.Series.Name != "Dune" || row.Series.Source != compactcatalog.ReadingOwner || row.Series.Number == nil || *row.Series.Number != 2.5 || row.Series.ID != compactcatalog.ReadingSeriesID(r.library, "dune") {
		t.Fatalf("actual owner rows lost: %+v", row)
	}
	session, _, err := r.s.Prepare(ctx, r.p, r.item, r.source)
	if err != nil {
		t.Fatal(err)
	}
	defer r.s.CloseSession(r.p, session.ID)
	page := int64(0)
	if _, saved, err := r.s.SaveSessionProgress(ctx, r.p, session.ID, 0, reading.Location{Format: "pdf", PageIndex: &page, Progression: .25}); err != nil || !saved {
		t.Fatal("actual resume setup", saved, err)
	}
	resume, err := r.s.Continue(ctx, r.p, r.library, "", 1)
	if err != nil || len(resume.Items) != 1 || resume.Items[0].Item.Author == nil || resume.Items[0].Item.Author.Value != "A & B" || resume.Items[0].Item.Series == nil || resume.Items[0].Item.Series.ID != row.Series.ID {
		t.Fatalf("Continue projection differs: %+v %v", resume, err)
	}
	write("author", "", true)
	write("series", "old", false)
	write("seriesNumber", "", false)
	list, err = r.s.List(ctx, r.p, r.library, "", 1)
	if err != nil || len(list.Items) != 1 || list.Items[0].Author != nil || list.Items[0].Series != nil {
		t.Fatalf("clear invented automatic fallback: %+v %v", list, err)
	}
}

func TestReadingPrivateOwnerProjectionHonorsCurrentProfile(t *testing.T) {
	r := newReadingFixture(t, "book", "pdf")
	ctx := context.Background()
	owner := readingOwnerLogin(t, r)
	profiles, err := r.f.id.CreateDirectProfile(ctx, owner.AccountToken, "Attribution reader", "blue")
	if err != nil {
		t.Fatal(err)
	}
	var child identity.DirectProfile
	for _, p := range profiles.Profiles {
		if !p.Primary && p.Name == "Attribution reader" {
			child = p
		}
	}
	selected, err := r.f.id.SelectDirectProfile(ctx, owner.AccountToken, child.ID, identity.DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.f.id.Authenticate(selected.Session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.f.db.Exec(`INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at)
 SELECT 'item',id,'author','Private attribution','',1,?,datetime('now') FROM catalog_entities WHERE public_id=pid_blob(?)`, identity.PersonalKey(r.p.Viewer), r.item); err != nil {
		t.Fatal(err)
	}
	list, err := r.s.List(ctx, p, r.library, "", 1)
	if err != nil || len(list.Items) != 1 || list.Items[0].Author == nil {
		t.Fatal("ordinary non-primary profile cannot read accepted metadata", err)
	}
	if _, err = r.f.id.SetProfileRestrictions(ctx, owner.AccountToken, child.ID, identity.ProfileRestrictionEdit{ExpectedRevision: 1, AllowUnrated: false}); err != nil {
		t.Fatal(err)
	}
	p, err = r.f.id.Authenticate(selected.Session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	list, err = r.s.List(ctx, p, r.library, "", 1)
	if err != nil || len(list.Items) != 0 || list.NextCursor != "" {
		t.Fatalf("restricted metadata leaked: %+v %v", list, err)
	}
	list, err = r.s.List(ctx, r.p, r.library, "", 1)
	if err != nil || len(list.Items) != 1 || list.Items[0].Author == nil {
		t.Fatal("sibling owner metadata was changed", err)
	}
}
