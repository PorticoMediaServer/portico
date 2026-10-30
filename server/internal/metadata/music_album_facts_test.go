package metadata

import (
	"context"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

func mbLabelFixture() metadataprovider.Release {
	release := mbReleaseFixture()
	release.LabelInfo = []metadataprovider.ReleaseLabel{{CatalogNumber: "X1", Label: &metadataprovider.Label{ID: mbGroup, Name: "Label Name"}}}
	release.Genres = []metadataprovider.MBGenre{{Name: "Rock", Count: 5}}
	return release
}

func mbLabelService(t *testing.T, release metadataprovider.Release) (*Service, string) {
	t.Helper()
	db, _ := mbDB(t)
	t.Cleanup(func() { db.Close() })
	mbCurrentExec(t, db, `INSERT INTO audio_tag_evidence VALUES('lib',?,'musicbrainz_albumid','embedded',?)`, mbAssetToken(t, db), mbRelease)
	mbCurrentExec(t, db, `UPDATE mb_jobs SET status='unavailable' WHERE kind='song'`)
	svc := New(db, "")
	svc.mb = mbFixture{
		release: func(_ context.Context, id string) (metadataprovider.ReleaseLookup, error) {
			return metadataprovider.ReleaseLookup{RequestedID: id, Release: release}, nil
		},
		record: func(context.Context, string) (metadataprovider.RecordingLookup, error) {
			return metadataprovider.RecordingLookup{}, errors.New("unexpected")
		},
		searchRecord: func(context.Context, string, string) ([]metadataprovider.Recording, error) {
			return nil, errors.New("unexpected")
		},
		searchRelease: func(context.Context, string, string) ([]metadataprovider.ReleaseCandidate, error) {
			return nil, errors.New("unexpected")
		},
	}
	for n := 0; n < 3; n++ {
		if e := svc.MusicBrainzStep(context.Background()); e != nil {
			t.Fatal(n, e)
		}
	}
	album := mbPublicID(t, db, "Local Album")
	state, e := svc.MusicBrainzState("album", album)
	if e != nil || state.Status != "matched" {
		t.Fatal(state, e)
	}
	return svc, album
}

// Spec — Page Content §2 item 5: the release's label fills the album's empty
// owner field, and its genres persist in the evidence payload.
func TestMBAlbumLabelFillsEmptyOwnerField(t *testing.T) {
	svc, _ := mbLabelService(t, mbLabelFixture())
	var label string
	if e := svc.db.QueryRow(`SELECT label FROM catalog_albums WHERE entity_id=?`, mbIntID(t, svc.db, "Local Album")).Scan(&label); e != nil || label != "Label Name" {
		t.Fatal("label not published", label, e)
	}
	var genre string
	if e := svc.db.QueryRow(`SELECT json_extract(payload,'$.genres[0].name') FROM mb_release_evidence`).Scan(&genre); e != nil || genre != "Rock" {
		t.Fatal("genres not persisted", genre, e)
	}
}

// An owner's label is never overwritten by the provider.
func TestMBAlbumLabelPreservesOwnerText(t *testing.T) {
	svc, _ := mbLabelService(t, mbLabelFixture())
	db := svc.db
	driveMBCatalogAdoptions(t, db)
	catalogtest.New(t, db).Fields(mbIntID(t, db, "Local Album"), map[string]any{"label": "Owner Label"})
	// Publishing the same release again keeps the owner's text.
	mbCurrentExec(t, db, `UPDATE mb_jobs SET status='pending',attempts=0,next_attempt='',error='',generation=generation+1,revision=revision+1 WHERE kind='album'`)
	for n := 0; n < 3; n++ {
		if e := svc.MusicBrainzStep(context.Background()); e != nil {
			t.Fatal(n, e)
		}
	}
	var label string
	if e := db.QueryRow(`SELECT label FROM catalog_albums WHERE entity_id=?`, mbIntID(t, db, "Local Album")).Scan(&label); e != nil || label != "Owner Label" {
		t.Fatal("owner label overwritten", label, e)
	}
}
