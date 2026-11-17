package db_test

import (
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A committed metadata save cannot keep an old failure, and a rolled-back save cannot lose
// that failure. Current main-profile visibility still controls the write after a page read.
func TestMetadataJobOutcomesCommitWithTheirWrites(t *testing.T) {
	l := newLibrary(t, 60)
	if err := l.d.ExecForTest(ctx, `UPDATE profiles SET is_primary = 1 WHERE id = ?`, l.all.ProfileID); err != nil {
		t.Fatal(err)
	}
	films := everything(t, l.d, l.all, db.ItemQuery{Library: l.libs[0].PublicID, Kinds: []db.ItemKind{db.KindMovie}})
	item := films[0]
	job, err := l.d.StartJob(ctx, db.NewJob{Kind: "edit_items", Target: item.PublicID, OwnerID: l.all.UserID})
	if err != nil {
		t.Fatal(err)
	}
	refuse := func() {
		t.Helper()
		if err := l.d.RecordMetadataJobOutcome(ctx, job.ID, item.PublicID, "invalid_metadata"); err != nil {
			t.Fatal(err)
		}
	}
	check := func(want bool) {
		t.Helper()
		got, err := l.d.JobItemFailed(ctx, job.ID, item.ID)
		if err != nil || got != want {
			t.Fatalf("failure present %v, want %v: %v", got, want, err)
		}
	}
	refuse()
	doc, err := l.d.MetadataDocument(ctx, item.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	name := "Changed before the error"
	credits := []db.CreditEdit{{Person: "per_missing", Role: "actor"}}
	_, err = l.d.EditMetadata(ctx, item.PublicID, doc.Revision, db.MetadataEdit{JobID: job.ID, Title: &name, Credits: &credits})
	if !errors.Is(err, db.ErrInvalid) {
		t.Fatalf("late save refusal: %v", err)
	}
	check(true)
	if got, err := l.d.Item(ctx, l.all, item.PublicID); err != nil || got.Title != item.Title {
		t.Fatalf("rolled-back save changed the title: %+v %v", got, err)
	}
	year := 2002
	if _, err := l.d.EditMetadata(ctx, item.PublicID, doc.Revision, db.MetadataEdit{JobID: job.ID, Year: &year}); err != nil {
		t.Fatal(err)
	}
	check(false)
	// Provider details, an empty refresh answer and an unmatched lookup are all completed
	// outcomes, including when they change a field used by the whole-view selection.
	for _, save := range []func() error{
		func() error {
			return l.d.ApplyDetails(ctx, db.Background, item.ID, db.ItemDetails{JobID: job.ID, Year: 2003})
		},
		func() error { return l.d.MarkJobRefreshed(ctx, job.ID, item.ID) },
		func() error { return l.d.SetJobUnmatched(ctx, job.ID, item.ID, "no_results", "[]") },
	} {
		refuse()
		if err := save(); err != nil {
			t.Fatal(err)
		}
		check(false)
	}
	refuse()
	profile := profileID(t, l.d, l.all)
	if _, err := l.d.SetProfileRestrictions(ctx, l.all.UserID, profile, db.Restrictions{AllLibraries: false}, time.Now()); err != nil {
		t.Fatal(err)
	}
	doc, err = l.d.MetadataDocument(ctx, item.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.d.EditMetadata(ctx, item.PublicID, doc.Revision, db.MetadataEdit{JobID: job.ID, Year: &year}); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("hidden title was edited: %v", err)
	}
	if err := l.d.RecordMetadataJobOutcome(ctx, job.ID, item.PublicID, "invalid_metadata"); err != nil {
		t.Fatal(err)
	}
	check(false)
}
