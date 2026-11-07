package read

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

func newRSDetailFixture(t *testing.T, live bool) (*browseFixture, *ForegroundReader) {
	t.Helper()
	f := composeReadFixture(t, newRSDetailSource(t), append(HarnessDefinitions(), TitleFactsDefinitions()...), readfixture.LiveConfig(time.Now), !live, rsDetailHash(t))
	r, err := NewForegroundReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}
func TestRSDetailEveryCatalogueKind(t *testing.T) {
	f, r := newRSDetailFixture(t, false)
	for kind := int64(1); kind <= 10; kind++ {
		in := TitleFactsInput{View: f.input, Item: 1100 + kind}
		in.View.Kind = kind
		d, cost, err := r.facts(t.Context(), foregroundRequest(), time.Second, f.output, in)
		if err != nil {
			t.Fatal(kind, err)
		}
		extra := 0
		if kind == 10 {
			extra = 1
		}
		if d.Item.ID != in.Item || d.Item.Kind != kind || d.Year != 2024 || d.Item.Overview != "Accepted overview" || d.OriginalTitle != "Original" || d.Edition != "Deluxe" || d.AddedAt == nil || *d.AddedAt != "2026-11-01T00:00:00.000000000Z" || d.LastContentAddedAt == nil || cost.LookupStatements != 10+extra || cost.PublicationStatements != 7+extra {
			t.Fatalf("kind%d: %+v %+v", kind, d, cost)
		}
		switch kind {
		case 2:
			if d.Show == nil || !d.Show.HideSeasons || !d.Show.AbsoluteNumbering || !d.Show.NewestFirst || d.Show.SettingsRevision != 3 || d.Show.Ranges != "1-7" {
				t.Fatal(d)
			}
		case 3:
			if d.Season == nil || d.Season.Show != 1102 || d.Season.Number != 3 {
				t.Fatal(d)
			}
		case 4:
			if d.Episode == nil || d.Episode.Show != 1102 || d.Episode.Season != nil || d.Episode.Number != 7 || d.Episode.Numbering != "absolute" || d.Episode.AirDate != nil || d.Episode.IdentityStatus != nil {
				t.Fatal(d)
			}
		case 6:
			if d.Album == nil || d.Album.Artist != 1105 || d.Album.RecordLabel != "Record label" || d.Album.EditionKey != "deluxe" {
				t.Fatal(d)
			}
		case 7:
			if d.Song == nil || d.Song.Album != 1106 || d.Song.Disc == nil || *d.Song.Disc != 2 || *d.Song.Track != 9 {
				t.Fatal(d)
			}
		case 8:
			if d.Book == nil || d.Book.Author != "Author" || d.Book.Narrator != "Narrator" || d.Book.SeriesPosition != "2.5" {
				t.Fatal(d)
			}
		case 9:
			if d.AudioFile == nil || d.AudioFile.Book != 1108 || *d.AudioFile.Part != 4 {
				t.Fatal(d)
			}
		case 10:
			if d.Collection == nil || d.Collection.Revision != 5 || *d.Collection.NameKey != "typed collection" {
				t.Fatal(d)
			}
		}
	}
	// Optional ordering coordinates stay NULL; a default presentation number is not a source fact.
	f.exec(t, `UPDATE catalog_entities SET disc_number=NULL,track_number=NULL,added_at=NULL,last_content_added_at=NULL WHERE id=1107`)
	in := TitleFactsInput{View: f.input, Item: 1107}
	in.View.Kind = 7
	d, _, err := r.facts(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || d.Song.Disc != nil || d.Song.Track != nil || d.AddedAt != nil || d.LastContentAddedAt != nil {
		t.Fatal("NULL fact changed", d, err)
	}
	in.View.Kind = 1
	d, _, err = r.facts(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(d, TitleFacts{}) {
		t.Fatal("wrong kind escaped", err)
	}
}
func TestRSDetailPublicationAndRetry(t *testing.T) {
	// Assert whole-retry and fresh denial ordering with the original one-second
	// budget, independent of shared runner startup and race-instrumentation time.
	synctest.Test(t, testRSDetailPublicationAndRetry)
}

func testRSDetailPublicationAndRetry(t *testing.T) {
	f, r := newRSDetailFixture(t, false)
	in := TitleFactsInput{View: f.input, Item: 1101}
	r.snapshots = &rsByteBoundarySnapshots{Snapshots: f.snapshots, after: func() {
		f.exec(t, `UPDATE catalog_entities SET original_title='Fresh source',revision=revision+1 WHERE id=1101`)
	}}
	d, cost, err := r.facts(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || cost.Attempts != 2 || d.OriginalTitle != "Fresh source" {
		t.Fatal("source retry", cost.Attempts, d, err)
	}
	r.snapshots = &rsByteBoundarySnapshots{Snapshots: f.snapshots, after: func() { f.exec(t, `UPDATE authorization_session_families SET revoked=1`) }}
	d, _, err = r.facts(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(d, TitleFacts{}) {
		t.Fatal("revoked draft escaped", err)
	}
}
func TestRSDetailLIVEAndCancellation(t *testing.T) {
	f, r := newRSDetailFixture(t, true)
	in := TitleFactsInput{View: f.input, Item: 1101}
	q, request, output := liveRequests("rs-detail")
	d, _, err := r.Facts(t.Context(), request, time.Second, BrowseOutput{}, in)
	if !errors.Is(err, ErrInput) || !reflect.DeepEqual(d, TitleFacts{}) {
		t.Fatal("unadmitted read", err)
	}
	_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		d, _, err := r.Facts(ctx, request, time.Second, out, in)
		if err != nil || d.Item.ID != 1101 || f.authority.ForegroundUsage().Live != 1 {
			t.Fatal("public facts", d, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertLiveUsage(t, f, 0, 0)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	d, _, err = r.Facts(ctx, request, time.Second, BrowseOutput{}, in)
	if err == nil || !reflect.DeepEqual(d, TitleFacts{}) {
		t.Fatal("cancelled output", err)
	}
}
