package metadata

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func repairFixture(t *testing.T) (*Service, *sql.DB, RepairTarget) {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := catalogtest.New(t, db)
	lib := c.Library("lib", "Movies", "movie", "/local")
	movie := c.Movie(lib, "/local/movie.mkv", "Before", 2020)
	s := New(db, "test-token")
	if err = s.SetArtworkDirectory(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	return s, db, RepairTarget{"item", movie.Public}
}

func repairIntegrationPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	im.Set(0, 0, color.RGBA{R: 200, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestOwnerRepairCASUndoAndRelationshipLock(t *testing.T) {
	s, db, target := repairFixture(t)
	ctx := context.Background()
	auth := func(*sql.Tx) error { return nil }
	actor := MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}
	before, err := s.RepairState(ctx, target, auth)
	if err != nil {
		t.Fatal(err)
	}
	title := "Owner title"
	after, err := s.Repair(ctx, target, RepairCommand{ExpectedRevision: before.Revision, Action: "edit", Fields: map[string]RepairFieldEdit{"title": {Value: &title}}}, actor, auth)
	if err != nil {
		t.Fatal(err)
	}
	if after.Snapshot.Fields["title"].Value != title || !after.Snapshot.Fields["title"].Locked {
		t.Fatal("owner lock not projected")
	}
	if _, err = s.Repair(ctx, target, RepairCommand{ExpectedRevision: before.Revision, Action: "edit", Fields: map[string]RepairFieldEdit{"title": {Value: &title}}}, actor, auth); !errors.Is(err, ErrRepairConflict) {
		t.Fatal("stale write accepted", err)
	}
	restored, err := s.Repair(ctx, target, RepairCommand{ExpectedRevision: after.Revision, Action: "undo", Confirm: true, HistoryID: after.History[0].ID}, actor, auth)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Snapshot.Fields["title"].Value != "Before" {
		t.Fatal("undo failed")
	}
	locked := true
	state, err := s.Repair(ctx, target, RepairCommand{ExpectedRevision: restored.Revision, Action: "relationship_lock", Role: "genre", Locked: &locked, Confirm: true}, actor, auth)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Snapshot.RelationshipLocks["genre"] {
		t.Fatal("empty list lock lost")
	}
	var n int
	c := catalogtest.New(t, db)
	if err = db.QueryRow(`SELECT count(*) FROM catalog_entities WHERE library_id=? AND kind=1`, c.Handle("lib")).Scan(&n); err != nil || n != 1 {
		t.Fatal("repair duplicated media", err)
	}
}
func TestOwnerRepairLocalCreditCannotForgeProviderPerson(t *testing.T) {
	s, _, target := repairFixture(t)
	ctx := context.Background()
	auth := func(*sql.Tx) error { return nil }
	actor := MBActor{Authority: "local", AccountID: "owner"}
	before, err := s.RepairState(ctx, target, auth)
	if err != nil {
		t.Fatal(err)
	}
	rel := RepairRelationship{Kind: "credit", Label: "Same Name", Role: "Performer", Provider: "tmdb", TargetKind: "person", TargetID: "123"}
	after, err := s.Repair(ctx, target, RepairCommand{ExpectedRevision: before.Revision, Action: "edit_relationships", Role: "credit", Confirm: true, Relationships: []RepairRelationship{rel}}, actor, auth)
	if err != nil {
		t.Fatal(err)
	}
	got := after.Snapshot.Relationships[0]
	if got.Provider != "manual" || got.TargetKind != "scoped_credit" || got.TargetID == "123" {
		t.Fatal("name forged canonical person", got)
	}
}

func TestManualCreditWriterRefusesUnpublishedProviderChanges(t *testing.T) {
	for _, change := range []string{"remove", "label", "role", "department", "identity", "ordinal"} {
		t.Run(change, func(t *testing.T) {
			s, db, target := repairFixture(t)
			ctx := context.Background()
			auth := func(*sql.Tx) error { return nil }
			actor := MBActor{Authority: "local", AccountID: "owner"}
			c := catalogtest.New(t, db)
			c.Write(func(ctx context.Context, tx *sql.Tx) error {
				entity, err := resolveEntity(ctx, tx, target.ID)
				if err != nil {
					return err
				}
				return compactcatalog.SetCreditsTx(ctx, tx, entity, "tmdb", []compactcatalog.Credit{{PersonKey: "tmdb:42", PersonName: "Original", CreditID: "provider-credit", ProviderPersonID: "42", CreditedName: "Original", Role: "Performer", Department: "Acting"}})
			})
			before, err := s.RepairState(ctx, target, auth)
			if err != nil {
				t.Fatal(err)
			}
			values := append([]RepairRelationship{}, before.Snapshot.Relationships...)
			if len(values) != 1 || values[0].Provider != "tmdb" {
				t.Fatal("expected the published provider credit", values)
			}
			switch change {
			case "remove":
				values = nil
			case "label":
				values[0].Label = "Changed"
			case "role":
				values[0].Role = "Director"
			case "department":
				values[0].Department = "Directing"
			case "identity":
				values[0].TargetID = "different-person"
			case "ordinal":
				values[0].Ordinal++
			}
			_, err = s.Repair(ctx, target, RepairCommand{ExpectedRevision: before.Revision, Action: "edit_relationships", Role: "credit", Confirm: true, Relationships: values}, actor, auth)
			if !errors.Is(err, ErrRepairInput) {
				t.Fatal("unsupported provider mutation was accepted", err)
			}
			// Preserving that provider row while adding a scoped manual credit
			// remains a supported command after the refused mutation.
			values = append(append([]RepairRelationship{}, before.Snapshot.Relationships...), RepairRelationship{Kind: "credit", Label: "Manual", Role: "Performer"})
			after, err := s.Repair(ctx, target, RepairCommand{ExpectedRevision: before.Revision, Action: "edit_relationships", Role: "credit", Confirm: true, Relationships: values}, actor, auth)
			if err != nil || len(after.Snapshot.Relationships) != 2 || after.Snapshot.Relationships[0].Provider != "tmdb" || after.Snapshot.Relationships[0].Label != "Original" || after.Snapshot.Relationships[1].Provider != "manual" {
				t.Fatal("manual addition did not preserve the provider credit", after.Snapshot.Relationships, err)
			}
		})
	}
}
func TestDurableArtworkSelectionFailureAndStaleFence(t *testing.T) {
	s, db, target := repairFixture(t)
	ctx := context.Background()
	auth := func(*sql.Tx) error { return nil }
	actor := MBActor{Authority: "local", AccountID: "owner"}
	path := filepath.Join(t.TempDir(), "local.png")
	if err := os.WriteFile(path, repairIntegrationPNG(t, 40, 60), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.LocalArtwork = func(string) (*os.File, string, error) { calls++; f, e := os.Open(path); return f, "image/png", e }
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := artworkFence(ctx, tx, target)
	if err != nil {
		t.Fatal(err)
	}
	id, err := insertArtworkCandidate(ctx, tx, target, "poster", "", "local", "local-image", "local:"+strings.Repeat("a", 64), "", "Local artwork", fence, "2026-09-06T00:00:00Z", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	before, err := s.RepairState(ctx, target, auth)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := s.Repair(ctx, target, RepairCommand{ExpectedRevision: before.Revision, Action: "select_artwork", CandidateID: id, Confirm: true}, actor, auth)
	if err != nil {
		t.Fatal(err)
	}
	if len(queued.Snapshot.Artwork) != 0 || calls != 0 {
		t.Fatal("request acquired provider bytes")
	}
	if err = s.ArtworkStep(ctx); err != nil {
		t.Fatal(err)
	}
	selected, err := s.RepairState(ctx, target, auth)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Snapshot.Artwork) != 1 {
		t.Fatal("worker did not publish selection")
	}
	digest := selected.Snapshot.Artwork[0].Digest
	if _, err = s.Repair(ctx, target, RepairCommand{ExpectedRevision: selected.Revision, Action: "repair_assets"}, actor, auth); err != nil {
		t.Fatal(err)
	}
	s.LocalArtwork = func(string) (*os.File, string, error) { return nil, "", os.ErrNotExist }
	if err = s.ArtworkStep(ctx); err != nil {
		t.Fatal(err)
	}
	failed, err := s.RepairState(ctx, target, auth)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Snapshot.Artwork[0].Digest != digest {
		t.Fatal("failed fetch erased last image")
	}
	entityID := catalogtest.New(t, db).ID(target.ID)
	if _, err = db.Exec(`UPDATE metadata_publication_heads SET source_revision=source_revision+1 WHERE item_id=?;UPDATE artwork_jobs SET next_attempt='' WHERE kind='item' AND entity_id=?`, entityID, entityID); err != nil {
		t.Fatal(err)
	}
	if err = s.ArtworkStep(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = db.QueryRow(`SELECT status FROM artwork_jobs WHERE kind='item' AND entity_id=? AND role='poster' AND preview=0`, entityID).Scan(&status); err != nil || status != "stale" {
		t.Fatal("stale publication not fenced", status, err)
	}
}

func TestOwnerRepairUnmatchedIdentityLockAndUnlock(t *testing.T) {
	s, db, target := repairFixture(t)
	ctx := context.Background()
	auth := func(*sql.Tx) error { return nil }
	actor := MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}
	state, err := s.RepairState(ctx, target, auth)
	if err != nil {
		t.Fatal(err)
	}
	for _, locked := range []bool{true, false} {
		state, err = s.Repair(ctx, target, RepairCommand{ExpectedRevision: state.Revision, Action: "relationship_lock", Role: "identity", Locked: &locked, Confirm: true}, actor, auth)
		if err != nil {
			t.Fatal(err)
		}
		if state.Snapshot.Identity == nil || state.Snapshot.Identity.Locked != locked {
			t.Fatal("identity lock projection", state.Snapshot.Identity)
		}
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		actual, e := repairIdentityLocked(ctx, tx, target)
		tx.Rollback()
		if e != nil || actual != locked {
			t.Fatal("worker lock decision", actual, e)
		}
	}
}
