package read

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"
)

func TestRSDetailComposedEveryKind(t *testing.T) {
	f, r := newRSDetailComposedFixture(t, false)
	for kind := int64(1); kind <= 10; kind++ {
		in := RSDetailComposedInput{View: f.input, Item: 1100 + kind}
		in.View.Kind = kind
		d, cost, err := r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
		if err != nil {
			t.Fatal(kind, err)
		}
		if d.Facts.Item.ID != in.Item || d.Facts.Item.Kind != kind || d.Facts.Year != 2024 || d.Facts.Item.Overview != "Accepted overview" || d.Facts.OriginalTitle != "Original" || d.Facts.Edition != "Deluxe" || d.Facts.AddedAt == nil || d.Technical != RSDetailFactPending || d.Provider != RSDetailFactPending || d.Related != RSDetailFactAbsent || cost.Attempts != 1 || len(cost.LookupWindows) < 6 {
			t.Fatalf("kind%d: %+v %+v %v", kind, d, cost, err)
		}
		switch kind {
		case 1:
			if len(d.Cast) != 1 || len(d.Crew) != 1 || d.Cast[0].Name != "Composed actor" || d.Crew[0].Name != "Composed director" || d.Art.State != RSDetailFactPresent || d.Art.Fact == nil || d.Art.Fact.Role != "poster" || len(d.Images) != 1 {
				t.Fatal("movie common windows", d)
			}
		case 2:
			if d.Facts.Show == nil || !d.Facts.Show.HideSeasons || !d.Facts.Show.AbsoluteNumbering || d.Facts.Show.SettingsRevision != 3 || d.Facts.Show.Ranges != "1-7" || len(d.Children) != 2 || !d.ChildrenExhausted || d.Art.State != RSDetailFactAbsent {
				t.Fatal("show", d)
			}
		case 3:
			if d.Facts.Season == nil || d.Facts.Season.Show != 1102 || d.Facts.Season.Number != 3 || d.Art.State != RSDetailFactPresent {
				t.Fatal("season", d)
			}
		case 4:
			if d.Facts.Episode == nil || d.Facts.Episode.Show != 1102 || d.Facts.Episode.Season == nil || *d.Facts.Episode.Season != 1103 || d.Facts.Episode.Number != 7 || d.Facts.Episode.Numbering != "absolute" || d.Facts.Episode.AirDate != nil || d.Facts.Episode.IdentityStatus != nil || d.Art.Fact == nil || d.Art.Fact.Role != "still" {
				t.Fatal("episode", d)
			}
		case 5:
			if d.Art.Fact == nil || d.Art.Fact.Role != "portrait" {
				t.Fatal("artist art", d)
			}
		case 6:
			if d.Facts.Album == nil || d.Facts.Album.Artist != 1105 || d.Facts.Album.RecordLabel != "Record label" || d.Facts.Album.EditionKey != "deluxe" || d.Art.Fact == nil || d.Art.Fact.Role != "cover" {
				t.Fatal("album", d)
			}
		case 7:
			if d.Facts.Song == nil || d.Facts.Song.Album != 1106 || d.Facts.Song.Disc == nil || *d.Facts.Song.Disc != 2 || *d.Facts.Song.Track != 9 {
				t.Fatal("song", d)
			}
		case 8:
			if d.Facts.Book == nil || d.Facts.Book.Author != "Author" || d.Facts.Book.Narrator != "Narrator" || d.Facts.Book.SeriesPosition != "2.5" || d.EffectiveSeries != RSDetailFactPending || d.Resume != RSDetailFactPending {
				t.Fatal("book", d)
			}
		case 9:
			if d.Facts.AudioFile == nil || d.Facts.AudioFile.Book != 1108 || d.Facts.AudioFile.Part == nil || *d.Facts.AudioFile.Part != 4 || d.EffectiveSeries != RSDetailFactPending || d.Resume != RSDetailFactPending || d.Art.Fact == nil {
				t.Fatal("audiobook file", d)
			}
		case 10:
			if d.Facts.Collection == nil || d.Facts.Collection.Revision != 5 || d.Facts.Collection.NameKey == nil || *d.Facts.Collection.NameKey != "typed collection" || d.Art.Fact == nil {
				t.Fatal("collection", d)
			}
		}
		if d.Reading != nil {
			t.Fatal("timed-media kind invented Reading facts", kind)
		}
	}
	f.exec(t, `UPDATE libraries SET kind='book' WHERE id='movies'`)
	in := RSDetailComposedInput{View: f.input, Item: 1120}
	in.View.Kind = 20
	d, _, err := r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || d.Reading == nil || !d.Reading.Available || d.Reading.Title != "Composed ebook" || d.Reading.Author.Value == nil || *d.Reading.Author.Value != "Composed author" || d.Reading.Series.Value == nil || *d.Reading.Series.Value != "Composed series" || d.Reading.NumberedOrder == nil || *d.Reading.NumberedOrder != 1 || d.Reading.NumberValue == nil || *d.Reading.NumberValue != 0 || d.Facts.Item.Kind != 20 || d.Facts.Book != nil || d.Resume != RSDetailFactPending {
		t.Fatal("ebook detail", d, err)
	}
	f.exec(t, `UPDATE libraries SET kind='comic' WHERE id='movies'`)
	in = RSDetailComposedInput{View: f.input, Item: 1121}
	in.View.Kind = 21
	d, _, err = r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || d.Reading == nil || !d.Reading.Available || d.Reading.Title != "Composed comic" || d.Facts.Item.Kind != 21 || d.Resume != RSDetailFactPending {
		t.Fatal("comic detail", d, err)
	}
}

func TestRSDetailComposedMissingAndNull(t *testing.T) {
	f, r := newRSDetailComposedFixture(t, false)
	in := RSDetailComposedInput{View: f.input, Item: 999999}
	d, _, err := r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(d, RSDetailComposed{}) {
		t.Fatal("missing entity escaped", d, err)
	}
	in.View.Kind = 99
	in.Item = 1101
	if d, _, err = r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in); !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(d, RSDetailComposed{}) {
		t.Fatal("unsupported kind escaped", d, err)
	}
	// NULL optional source values remain absent; they are not defaulted numbers.
	f.exec(t, `UPDATE catalog_entities SET disc_number=NULL,track_number=NULL,added_at=NULL,last_content_added_at=NULL WHERE id=1107`)
	in = RSDetailComposedInput{View: f.input, Item: 1107}
	in.View.Kind = 7
	if d, _, err = r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in); err != nil || d.Facts.Song == nil || d.Facts.Song.Disc != nil || d.Facts.Song.Track != nil || d.Facts.AddedAt != nil || d.Facts.LastContentAddedAt != nil {
		t.Fatal("NULL source values changed", d, err)
	}
	// A personal row is distinct from a NULL rating inside one.
	if d, _, err = r.detail(t.Context(), foregroundRequest(), time.Second, f.output, RSDetailComposedInput{View: f.input, Item: 1101}); err != nil || d.Personal.Revision != 0 || d.Personal.Rating != nil {
		t.Fatal("missing personal row", d, err)
	}
	f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,revision,resume_position,rating) SELECT 'p',public_id,id,library_id,2,500,NULL FROM catalog_entities WHERE id=1101`)
	if d, _, err = r.detail(t.Context(), foregroundRequest(), time.Second, f.output, RSDetailComposedInput{View: f.input, Item: 1101}); err != nil || d.Personal.Revision != 2 || d.Personal.ResumePosition != 500 || d.Personal.Rating != nil {
		t.Fatal("NULL personal rating", d, err)
	}
}

func TestRSDetailComposedReadingUnavailableRetained(t *testing.T) {
	f, r := newRSDetailComposedFixture(t, false)
	f.exec(t, `UPDATE libraries SET kind='book' WHERE id='movies'`)
	in := RSDetailComposedInput{View: f.input, Item: 1120}
	in.View.Kind = 20
	d, _, err := r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || d.Reading == nil || !d.Reading.Available {
		t.Fatal("available Reading", d, err)
	}
	f.exec(t, `UPDATE inventory_objects SET retired=1 WHERE asset_id='asset'`)
	d, _, err = r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || d.Reading == nil || d.Reading.Available || d.Reading.ID != 1120 || d.Reading.Title != "Composed ebook" || d.Reading.Author.Value == nil {
		t.Fatal("accepted unavailable Reading was not retained", d, err)
	}
}

func TestRSDetailComposedNoSelectedFileFacts(t *testing.T) {
	f, r := newRSDetailComposedFixture(t, false)
	in := RSDetailComposedInput{View: f.input, Item: 1101}
	d, _, err := r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || d.Technical != RSDetailFactPending || d.Provider != RSDetailFactPending || d.Related != RSDetailFactAbsent || d.Reading != nil || d.Facts.AudioFile != nil {
		t.Fatal("pending states", d, err)
	}
	// The composed DTO carries only bounded link/version rows. Selected file,
	// stream, chapter and disc facts remain task 03's accepted technical source;
	// no such value is inferred or invented here.
	in.Item = 1107
	in.View.Kind = 7
	d, _, err = r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || len(d.Versions) != 1 || d.Versions[0].Asset != 1 || !d.Versions[0].Available {
		t.Fatal("link/version facts", d, err)
	}
}

func TestRSDetailComposedArtPendingIsNotInvented(t *testing.T) {
	f, r := newRSDetailComposedFixture(t, false)
	// The season's selected poster renditions are not ready. The composed slot
	// reports pending instead of borrowing a digest, URL or rendition size.
	f.exec(t, `UPDATE artwork_blobs SET status='deleting' WHERE digest=?`, rsArtDigest(2))
	in := RSDetailComposedInput{View: f.input, Item: 1103}
	in.View.Kind = 3
	d, _, err := r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || d.Art.State != RSDetailFactPending || d.Art.Fact != nil || d.Facts.Season == nil {
		t.Fatal("pending art", d, err)
	}
}

func TestRSDetailComposedPersonalProfileIsolation(t *testing.T) {
	f, r := newRSDetailComposedFixture(t, false)
	f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,revision,resume_position,favorite,rating,not_interested,activity_at,activity_state) SELECT 'p',public_id,id,library_id,4,500,1,7,1,'2026-11-06T00:00:00.000000000Z','in_progress' FROM catalog_entities WHERE id=1101`)
	f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,revision,resume_position,favorite,rating,not_interested,activity_at,activity_state) SELECT 'q',public_id,id,library_id,6,999,0,NULL,0,'2026-11-06T00:00:00.000000000Z','in_progress' FROM catalog_entities WHERE id=1101`)
	d, _, err := r.detail(t.Context(), foregroundRequest(), time.Second, f.output, RSDetailComposedInput{View: f.input, Item: 1101})
	if err != nil || d.Personal.Revision != 4 || d.Personal.ResumePosition != 500 || !d.Personal.Favorite || !d.Personal.NotInterested || d.Personal.Rating == nil || *d.Personal.Rating != 7 {
		t.Fatal("profile p projection", d, err)
	}
	f.exec(t, `INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('dq','b','iq','fixture-b','test','test','1','2026-11-05T00:00:00Z','2026-11-05T00:00:00Z')`)
	f.exec(t, `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,device_id,command_member_incarnation,command_profile_lifetime,command_incarnation) SELECT 'fq','s','b','q','local','member',1,'2026-11-07T00:00:00Z',1,'dq',m.incarnation,p.lifetime,x'00800000000000000000000000000002' FROM direct_memberships AS m JOIN direct_profiles AS p ON p.account_id=m.account_id WHERE m.account_id='b' AND p.id='q'`)
	f.exec(t, `INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES('qtoken','fq',1,'2026-11-07T00:00:00Z')`)
	f.exec(t, `UPDATE direct_memberships SET allowed_libraries='["movies","other"]' WHERE account_id='b'`)
	in := RSDetailComposedInput{View: f.input, Item: 1101}
	in.View.TokenHash = "qtoken"
	d, _, err = r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || d.Personal.Revision != 6 || d.Personal.ResumePosition != 999 || d.Personal.Favorite || d.Personal.NotInterested || d.Personal.Rating != nil {
		t.Fatal("profile q projection", d, err)
	}
}

func TestRSDetailComposedMutationAfterEncoding(t *testing.T) {
	synctest.Test(t, testRSDetailComposedMutationAfterEncoding)
}

func testRSDetailComposedMutationAfterEncoding(t *testing.T) {
	f, r := newRSDetailComposedFixture(t, false)
	in := RSDetailComposedInput{View: f.input, Item: 1101}
	r.snapshots = &rsByteBoundarySnapshots{Snapshots: f.snapshots, after: func() {
		f.exec(t, `UPDATE catalog_entities SET original_title='Fresh composed source',revision=revision+1 WHERE id=1101`)
	}}
	d, cost, err := r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || cost.Attempts != 2 || d.Facts.OriginalTitle != "Fresh composed source" {
		t.Fatal("source retry after encoded draft", cost.Attempts, d, err)
	}
	r.snapshots = &rsByteBoundarySnapshots{Snapshots: f.snapshots, after: func() { f.exec(t, `UPDATE authorization_session_families SET revoked=1`) }}
	d, _, err = r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(d, RSDetailComposed{}) {
		t.Fatal("revoked draft escaped", d, err)
	}
}

func TestRSDetailComposedLIVEAndCancellation(t *testing.T) {
	f, r := newRSDetailComposedFixture(t, true)
	in := RSDetailComposedInput{View: f.input, Item: 1101}
	q, request, output := liveRequests("rs-detail-composed")
	d, _, err := r.Detail(t.Context(), request, time.Second, BrowseOutput{}, in)
	if !errors.Is(err, ErrInput) || !reflect.DeepEqual(d, RSDetailComposed{}) {
		t.Fatal("unadmitted read", d, err)
	}
	_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		d, _, err := r.Detail(ctx, request, time.Second, out, in)
		if err != nil || d.Facts.Item.ID != 1101 || f.authority.ForegroundUsage().Live != 1 {
			t.Fatal("public composed detail", d, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertLiveUsage(t, f, 0, 0)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	d, _, err = r.Detail(ctx, request, time.Second, BrowseOutput{}, in)
	if err == nil || !reflect.DeepEqual(d, RSDetailComposed{}) {
		t.Fatal("cancelled output", d, err)
	}
}
