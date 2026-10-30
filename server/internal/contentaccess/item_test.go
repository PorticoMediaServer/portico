package contentaccess

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sort"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// One film or show per rating, in one library, with the rating spellings
// classified as the background worker would.
func ratedLibrary(t *testing.T, ratings map[string][]string, labels map[string][]string, tags map[string][]string) (*sql.DB, map[string]string) {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','owner-profile',1);
 INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('member','member',x'00','member-profile',1);
 INSERT INTO libraries(id,name,kind,root) VALUES('lib','Library','movie','/library');`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	public := map[string]string{}
	err = dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		handle, e := compactcatalog.LibraryTx(ctx, tx, "lib")
		if e != nil {
			return e
		}
		for name, values := range ratings {
			id, _, e := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: handle, Kind: compactcatalog.Movie, Key: "rated:" + name, Title: name})
			if e != nil {
				return e
			}
			if e = compactcatalog.SetAttributesTx(ctx, tx, id, "contentRating", values); e != nil {
				return e
			}
			if e = compactcatalog.SetAttributesTx(ctx, tx, id, "label", labels[name]); e != nil {
				return e
			}
			if e = compactcatalog.SetAttributesTx(ctx, tx, id, "tag", tags[name]); e != nil {
				return e
			}
			if public[name], e = entityid.Public(ctx, tx, id); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for {
		more, err := compactcatalog.ClassifyPendingRatings(ctx, db, identity.RatingAge)
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
	}
	return db, public
}

func member() identity.Principal {
	return identity.Principal{Viewer: identity.Viewer{AccountID: "member", ProfileID: "member-profile", Authority: "local", Role: identity.TierMember}}
}

func setLimits(t *testing.T, db *sql.DB, body string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO access_limits VALUES('member',1,0,?) ON CONFLICT(account_id) DO UPDATE SET body=excluded.body,revision=access_limits.revision+1`, body); err != nil {
		t.Fatal(err)
	}
}

func setProfile(t *testing.T, db *sql.DB, system, code string, age int, allowUnrated bool, labels string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO profile_restrictions(profile_id,rating_system,maximum_age_rating,maximum_age,allow_unrated,blocked_labels,allow_downloads,allow_live_tv,allow_dvr,allow_watch_together,revision) VALUES('member-profile',?,?,?,?,?,1,1,1,1,2)
 ON CONFLICT(profile_id) DO UPDATE SET rating_system=excluded.rating_system,maximum_age_rating=excluded.maximum_age_rating,maximum_age=excluded.maximum_age,allow_unrated=excluded.allow_unrated,blocked_labels=excluded.blocked_labels,revision=profile_restrictions.revision+1`, system, code, age, allowUnrated, labels); err != nil {
		t.Fatal(err)
	}
}

// visible is what the two fences agree the member may see: the per-item check
// and the set predicate must give the same answer for every title.
func visible(t *testing.T, db *sql.DB, public map[string]string) []string {
	t.Helper()
	ctx := context.Background()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	clause, args, err := VisibleItemsSQL(ctx, tx, member(), "e.id", nil)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for name, id := range public {
		itemErr := VisibleItemTx(ctx, tx, member(), id)
		if itemErr != nil && !errors.Is(itemErr, identity.ErrContentRestricted) {
			t.Fatal(itemErr)
		}
		var listed bool
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_entities e WHERE e.public_id=pid_blob(?) AND `+clause+`)`, append([]any{id}, args...)...).Scan(&listed); err != nil {
			t.Fatal(err)
		}
		if listed != (itemErr == nil) {
			t.Fatalf("%s: the list predicate says %v and the item check says %v", name, listed, itemErr)
		}
		if listed {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func same(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// An account limited to PG sees TV-PG shows, because both are for ages eight
// and up. The limit used to be an ordered list of American codes in which PG
// came before TV-PG, so a PG limit hid TV-PG.
func TestAnAccountRatingLimitComparesAgesAcrossRatingSystems(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, public := ratedLibrary(t, map[string][]string{
		"g": {"G"}, "tv-y7": {"TV-Y7"}, "pg": {"PG"}, "tv-pg": {"TV-PG"}, "bbfc-pg": {"gb/PG"}, "fsk-6": {"FSK 6"},
		"pg-13": {"PG-13"}, "tv-14": {"TV-14"}, "fsk-12": {"FSK 12"}, "bbfc-15": {"15"}, "r": {"R"}, "tv-ma": {"TV-MA"},
		"unrated": {}, "unknown": {"Banana"},
	}, nil, nil)
	for _, c := range []struct {
		body string
		want []string
	}{
		{`{"maxContentRating":"PG","allowUnrated":false}`, []string{"bbfc-pg", "fsk-6", "g", "pg", "tv-pg", "tv-y7"}},
		{`{"ratingSystem":"mpaa","maxContentRating":"PG","allowUnrated":false}`, []string{"bbfc-pg", "fsk-6", "g", "pg", "tv-pg", "tv-y7"}},
		{`{"maxContentRating":"PG-13","allowUnrated":false}`, []string{"bbfc-pg", "fsk-12", "fsk-6", "g", "pg", "pg-13", "tv-pg", "tv-y7"}},
		{`{"ratingSystem":"us-tv","maxContentRating":"TV-14","allowUnrated":false}`, []string{"bbfc-pg", "fsk-12", "fsk-6", "g", "pg", "pg-13", "tv-14", "tv-pg", "tv-y7"}},
		{`{"ratingSystem":"bbfc","maxContentRating":"15","allowUnrated":false}`, []string{"bbfc-15", "bbfc-pg", "fsk-12", "fsk-6", "g", "pg", "pg-13", "tv-14", "tv-pg", "tv-y7"}},
		// Unrated titles follow the account's own switch, not the ceiling.
		{`{"maxContentRating":"PG","allowUnrated":true}`, []string{"bbfc-pg", "fsk-6", "g", "pg", "tv-pg", "tv-y7", "unknown", "unrated"}},
		// The switch works without a ceiling too.
		{`{"allowUnrated":false}`, []string{"bbfc-15", "bbfc-pg", "fsk-12", "fsk-6", "g", "pg", "pg-13", "r", "tv-14", "tv-ma", "tv-pg", "tv-y7"}},
	} {
		setLimits(t, db, c.body)
		if got := visible(t, db, public); !same(got, c.want) {
			t.Fatalf("limits %s:\n got  %v\n want %v", c.body, got, c.want)
		}
	}
}

// The account's limit and the profile's own restriction are one rule: the
// lower ceiling holds, and either side blocking unrated titles blocks them.
func TestAccountLimitsAndProfileRestrictionsAreOneRule(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, public := ratedLibrary(t, map[string][]string{"g": {"G"}, "pg": {"PG"}, "pg-13": {"PG-13"}, "r": {"R"}, "unrated": {}},
		map[string][]string{"pg": {"Scary"}}, map[string][]string{"g": {"Halloween"}, "pg-13": {"Scary"}})
	// Account allows up to R; the profile is held to PG-13.
	setLimits(t, db, `{"maxContentRating":"R","allowUnrated":true}`)
	setProfile(t, db, "mpaa", "PG-13", 13, true, `[]`)
	if got, want := visible(t, db, public), []string{"g", "pg", "pg-13", "unrated"}; !same(got, want) {
		t.Fatalf("profile lower than account: got %v want %v", got, want)
	}
	// Account held to PG; the profile would allow R.
	setLimits(t, db, `{"maxContentRating":"PG","allowUnrated":true}`)
	setProfile(t, db, "mpaa", "R", 17, true, `[]`)
	if got, want := visible(t, db, public), []string{"g", "pg", "unrated"}; !same(got, want) {
		t.Fatalf("account lower than profile: got %v want %v", got, want)
	}
	// Either side blocks unrated titles.
	setProfile(t, db, "mpaa", "R", 17, false, `[]`)
	if got, want := visible(t, db, public), []string{"g", "pg"}; !same(got, want) {
		t.Fatalf("profile blocks unrated: got %v want %v", got, want)
	}
	setLimits(t, db, `{"maxContentRating":"R","allowUnrated":false}`)
	setProfile(t, db, "", "", -1, true, `[]`)
	if got, want := visible(t, db, public), []string{"g", "pg", "pg-13", "r"}; !same(got, want) {
		t.Fatalf("account blocks unrated: got %v want %v", got, want)
	}
	// The account's denied list matches labels and tags; the profile's own
	// blocked list matches labels.
	setLimits(t, db, `{"allowUnrated":true,"tagPolicy":{"deniedLabels":["scary"]}}`)
	if got, want := visible(t, db, public), []string{"g", "r", "unrated"}; !same(got, want) {
		t.Fatalf("account denied list: got %v want %v", got, want)
	}
	setLimits(t, db, `{"allowUnrated":true}`)
	setProfile(t, db, "", "", -1, true, `["Scary"]`)
	if got, want := visible(t, db, public), []string{"g", "pg-13", "r", "unrated"}; !same(got, want) {
		t.Fatalf("profile blocked labels: got %v want %v", got, want)
	}
	// With nothing set, nothing is hidden and the viewer is unrestricted.
	setProfile(t, db, "", "", -1, true, `[]`)
	if got := visible(t, db, public); len(got) != len(public) {
		t.Fatalf("no restrictions hid something: %v", got)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if open, err := Unrestricted(context.Background(), tx, member(), func(string) error { return nil }); err != nil || !open {
		t.Fatalf("an unlimited member is not reported unrestricted: %v %v", open, err)
	}
}

// A rating edited upwards hides the title in the same write.
func TestARatingRaisedAboveTheLimitHidesTheTitleAtOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, public := ratedLibrary(t, map[string][]string{"family": {"PG"}}, nil, nil)
	setLimits(t, db, `{"maxContentRating":"PG","allowUnrated":true}`)
	if got := visible(t, db, public); len(got) != 1 {
		t.Fatalf("a PG title under a PG limit: %v", got)
	}
	ctx := context.Background()
	if err := dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		var id int64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)`, public["family"]).Scan(&id); err != nil {
			return err
		}
		return compactcatalog.SetAttributesTx(ctx, tx, id, "contentRating", []string{"TV-MA"})
	}); err != nil {
		t.Fatal(err)
	}
	if got := visible(t, db, public); len(got) != 0 {
		t.Fatalf("a title re-rated TV-MA is still visible under a PG limit: %v", got)
	}
}
