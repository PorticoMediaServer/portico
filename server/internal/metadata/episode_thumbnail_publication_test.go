package metadata

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"image"
	"image/jpeg"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"testing"
)

type thumbnailPublicationAuthority struct {
	calls int
	err   error
}

func (a *thumbnailPublicationAuthority) CheckTx(ctx context.Context, tx *sql.Tx) error {
	a.calls++
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
		return err
	}
	return a.err
}
func episodePublicationFixture(t *testing.T) (*Service, *catalogtest.Catalog, RepairTarget, RepairTarget) {
	t.Helper()
	c := catalogtest.Open(t)
	lib := c.Library("episodes", "Episodes", "tv", "/episodes")
	show := c.Show(lib, "A show", 2020)
	season := c.Season(show, 1)
	first := c.Episode(show, season, 1, "/episodes/Show.S01E01.mkv")
	second := c.Episode(show, season, 2, "/episodes/Show.S01E02.mkv")
	c.Exec(`UPDATE library_sources SET identity_confirmed=1,root_identity='root-a' WHERE id='episodes'`)
	for _, it := range []catalogtest.Item{first, second} {
		c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state) SELECT ?,id,?,incarnation,?,'revision-a','{}',1000,1,'available' FROM library_sources WHERE id='episodes'`, it.Public, it.Token, it.Public+".mkv")
	}
	s := New(c.DB, "test-token")
	if err := s.SetArtworkDirectory(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	return s, c, RepairTarget{"item", first.Public}, RepairTarget{"item", second.Public}
}
func capturePublication(t *testing.T, s *Service, target RepairTarget) episodeThumbnailCapture {
	t.Helper()
	g, err := dbwork.BeginSnapshot(context.Background(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Rollback()
	out, err := captureEpisodeThumbnailTx(context.Background(), g.Tx(), target, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func preparePublication(t *testing.T, s *Service, check func(context.Context, *sql.Tx) error) episodeThumbnailEncoding {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 320, 180)), nil); err != nil {
		t.Fatal(err)
	}
	out, err := s.prepareEpisodeThumbnail(context.Background(), b.Bytes(), check)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestEpisodeThumbnailPublicationSelectsExactlyOneActualEpisode(t *testing.T) {
	s, c, first, second := episodePublicationFixture(t)
	ctx := context.Background()
	capture := capturePublication(t, s, first)
	sizeChecks := 0
	encoded := preparePublication(t, s, func(context.Context, *sql.Tx) error { sizeChecks++; return nil })
	a := &thumbnailPublicationAuthority{}
	ownerChecks := 0
	owner := func(*sql.Tx) error { ownerChecks++; return nil }
	out, err := s.publishEpisodeThumbnail(ctx, capture, encoded, a, true, MBActor{Authority: "local", AccountID: "owner", ProfileID: "owner-profile"}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if a.calls != 1 || sizeChecks != 1 || ownerChecks != 2 || out.Revision == capture.RepairRevision || len(out.Snapshot.Artwork) != 1 || !out.Snapshot.Artwork[0].Locked {
		t.Fatalf("publication/checks: %+v authority=%d size=%d owner=%d", out, a.calls, sizeChecks, ownerChecks)
	}
	var origin, provider string
	if err = c.DB.QueryRow(`SELECT provider,origin FROM artwork_candidates WHERE id=?`, out.Snapshot.Artwork[0].CandidateID).Scan(&provider, &origin); err != nil {
		t.Fatal(err)
	}
	if provider != "generated" || len(origin) < 8 || origin[:8] != "episode:" {
		t.Fatalf("wrong provenance %s %s", provider, origin)
	}
	var count int
	if err = c.DB.QueryRow(`SELECT COUNT(*) FROM artwork_uploads`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("generation impersonates upload: %d %v", count, err)
	}
	if err = c.DB.QueryRow(`SELECT COUNT(*) FROM artwork_selections WHERE kind='item' AND entity_id=?`, c.ID(second.ID)).Scan(&count); err != nil || count != 0 {
		t.Fatalf("another episode changed: %d %v", count, err)
	}
	file, _, err := s.EntityArtworkVariant(ctx, first, "still", "", ArtworkSmall, "")
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
}
func TestEpisodeThumbnailPublicationRejectsStaleOrRevokedAuthority(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, change := range []string{"source_revision", "root_generation", "source_disabled", "owner_choice", "permission", "size"} {
		t.Run(change, func(t *testing.T) {
			s, c, target, _ := episodePublicationFixture(t)
			ctx := context.Background()
			capture := capturePublication(t, s, target)
			revoked := errors.New("revoked")
			checkSize := func(context.Context, *sql.Tx) error { return nil }
			a := &thumbnailPublicationAuthority{}
			switch change {
			case "source_revision":
				c.Exec(`UPDATE inventory_objects SET revision='revision-b' WHERE id=?`, target.ID)
			case "root_generation":
				c.Exec(`UPDATE library_sources SET generation=generation+1 WHERE id='episodes'`)
			case "source_disabled":
				c.Exec(`UPDATE library_sources SET enabled=0 WHERE id='episodes'`)
			case "owner_choice":
				value := "Owner changed title"
				if _, err := s.Repair(ctx, target, RepairCommand{Action: "edit", ExpectedRevision: capture.RepairRevision, Fields: map[string]RepairFieldEdit{"title": {Value: &value}}}, MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}, func(*sql.Tx) error { return nil }); err != nil {
					t.Fatal(err)
				}
			case "permission":
				a.err = revoked
			case "size":
				checkSize = func(context.Context, *sql.Tx) error { return revoked }
			}
			picture := preparePublication(t, s, checkSize)
			if _, err := s.publishEpisodeThumbnail(ctx, capture, picture, a, true, MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}, func(*sql.Tx) error { return nil }); err == nil {
				t.Fatal("stale/revoked publication accepted")
			}
			var count int
			if err := c.DB.QueryRow(`SELECT COUNT(*) FROM artwork_selections WHERE role='still'`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("stale selection persisted: %d %v", count, err)
			}
		})
	}
}
func TestEpisodeThumbnailAutomaticPublicationPreservesExplicitChoice(t *testing.T) {
	s, _, target, _ := episodePublicationFixture(t)
	ctx := context.Background()
	a := &thumbnailPublicationAuthority{}
	picture := preparePublication(t, s, func(context.Context, *sql.Tx) error { return nil })
	first, err := s.publishEpisodeThumbnail(ctx, capturePublication(t, s, target), picture, a, true, MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}, func(*sql.Tx) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	captured := capturePublication(t, s, target)
	if !captured.Held {
		t.Fatal("explicit still is not held")
	}
	after, err := s.publishEpisodeThumbnail(ctx, captured, picture, a, false, MBActor{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != first.Revision || after.Snapshot.Artwork[0].CandidateID != first.Snapshot.Artwork[0].CandidateID {
		t.Fatal("automatic publication replaced explicit choice")
	}
}
func TestEpisodeThumbnailPublicationRequiresBothAuthorityAndSizeFence(t *testing.T) {
	s, _, target, _ := episodePublicationFixture(t)
	if _, err := s.prepareEpisodeThumbnail(context.Background(), nil, nil); !errors.Is(err, ErrRepairInput) {
		t.Fatal("missing encoding fence accepted", err)
	}
	picture := preparePublication(t, s, func(context.Context, *sql.Tx) error { return nil })
	if _, err := s.publishEpisodeThumbnail(context.Background(), capturePublication(t, s, target), picture, nil, true, MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}, func(*sql.Tx) error { return nil }); !errors.Is(err, ErrRepairInput) {
		t.Fatal("missing source authority accepted", err)
	}
}
