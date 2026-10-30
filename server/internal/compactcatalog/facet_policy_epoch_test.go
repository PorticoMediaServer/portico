package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

func TestFacetPolicyEpochSeparatesAuthorityFromContent(t *testing.T) {
	db := cc1OpenDB(t, "facet-policy-epoch.sqlite")
	lib := cc1Library(t, db, "policy", "Policy", "movie", "/policy")
	id := facetTestEntity(t, db, lib, "movie:policy")
	cc1Drain(t, db)
	epoch := func() int64 {
		v, err := FacetPolicyEpoch(context.Background(), db)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	before := epoch()
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return SetTermsTx(ctx, tx, id, VocabGenre, "owner", []Term{{Name: "Genre"}})
	})
	cc1Drain(t, db)
	if epoch() != before {
		t.Fatal("genre content churn revoked active basis authority")
	}
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return SetAttributesTx(ctx, tx, id, "studio", []string{"Studio"})
	})
	cc1Exec(t, db, `UPDATE catalog_entities SET title='Changed title' WHERE id=?`, id)
	cc1Drain(t, db)
	if epoch() != before {
		t.Fatal("studio/title churn changed policy epoch")
	}
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return SetAttributesTx(ctx, tx, id, "tag", []string{"member denied"})
	})
	if epoch() <= before {
		t.Fatal("tag accepting source did not revoke authority")
	}
	before = epoch()
	cc1Exec(t, db, `UPDATE catalog_item_attribute_edges SET source_value='new spelling' WHERE item_id=? AND term_id IN(SELECT id FROM catalog_attribute_terms WHERE field_id=3)`, id)
	if epoch() <= before {
		t.Fatal("raw member-denied spelling did not revoke authority")
	}
	before = epoch()
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return SetAttributesTx(ctx, tx, id, "contentRating", []string{"test rating"})
	})
	if epoch() <= before {
		t.Fatal("rating edge did not revoke authority")
	}
	before = epoch()
	cc1Exec(t, db, `INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('test rating',12)`)
	if epoch() <= before {
		t.Fatal("classification insert missing from authority")
	}
	before = epoch()
	cc1Exec(t, db, `UPDATE content_rating_ages SET minimum_age=18 WHERE value_key='test rating'`)
	if epoch() <= before {
		t.Fatal("classification update missing from authority")
	}
	before = epoch()
	cc1Exec(t, db, `UPDATE catalog_attribute_terms SET value_key='different-key' WHERE field_id=3`)
	if epoch() <= before {
		t.Fatal("policy dictionary key was not fenced before source fanout")
	}
	before = epoch()
	cc1Exec(t, db, `UPDATE catalog_attribute_terms SET value_key='studio-key' WHERE field_id=(SELECT id FROM catalog_attribute_fields WHERE field='studio')`)
	if epoch() != before {
		t.Fatal("studio dictionary edit changed authority")
	}
	cc1Exec(t, db, `UPDATE catalog_entities SET retired=1 WHERE id=?`, id)
	if epoch() <= before {
		t.Fatal("retirement did not revoke authority")
	}
}

func TestFacetGenreEpochFirstMemberIsSeparateAuthority(t *testing.T) {
	db := cc1OpenDB(t, "facet-genre-epoch.sqlite")
	lib := cc1Library(t, db, "genre-policy", "Genre policy", "tv", "/genre-policy")
	show := cc1Entity(t, db, Entity{Library: lib, Kind: Show, Key: "show", Title: "Empty show"}, map[string]any{"local_key": "show"})
	cc1Drain(t, db)
	// Unit setup of the library metadata normally accepted by genre publication.
	cc1Exec(t, db, `INSERT INTO catalog_search_genre_accepted_targets(library_id,max_entity_id) VALUES(?,?)`, lib, show)
	policy, err := FacetPolicyEpoch(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	genre, err := FacetGenreEpoch(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	child := facetTestEntity(t, db, lib, "movie:child")
	// New entity/browse materialization is content only. Use a non-self source
	// to prove that the genre empty fallback depends on ALL raw memberships.
	cc1Exec(t, db, `INSERT INTO catalog_browse_memberships(entity_id,item_id,source) VALUES(?,?,8)`, show, child)
	current, err := FacetPolicyEpoch(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	nextGenre, err := FacetGenreEpoch(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if current != policy || nextGenre <= genre {
		t.Fatal("first raw member was not isolated to genre authority")
	}
	cc1Exec(t, db, `CREATE TABLE genre_epoch_stamps(n INTEGER); CREATE TRIGGER observe_genre_epoch_stamp AFTER UPDATE OF genre_epoch ON catalog_facet_source_state BEGIN INSERT INTO genre_epoch_stamps VALUES(1); END;`)
	// Another source for the same member cannot revoke an existential witness.
	cc1Exec(t, db, `INSERT INTO catalog_browse_memberships(entity_id,item_id,source) VALUES(?,?,1)`, show, child)
	after, err := FacetGenreEpoch(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if after != nextGenre {
		t.Fatal("later membership inserted a genre revocation")
	}
	// Exercise the opposite full-PK range as well: a larger member sees an
	// existing lower member, while source=1 above saw the existing higher key.
	nextChild := facetTestEntity(t, db, lib, "movie:later-child")
	cc1Exec(t, db, `INSERT INTO catalog_browse_memberships(entity_id,item_id,source) VALUES(?,?,8)`, show, nextChild)
	var stamps int
	if err = db.QueryRow(`SELECT count(*) FROM genre_epoch_stamps`).Scan(&stamps); err != nil || stamps != 0 {
		t.Fatal("false WHEN predicate still stamped genre authority", stamps, err)
	}
	cc1Exec(t, db, `DELETE FROM catalog_browse_memberships WHERE entity_id=? AND item_id=? AND source=1`, show, child)
	current, err = FacetPolicyEpoch(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if current <= policy {
		t.Fatal("membership removal weakened existing revocation")
	}
}

func TestFacetPolicyEpochBulkContentMaterializationDoesNotRevokeSimple(t *testing.T) {
	db := cc1OpenDB(t, "facet-policy-bulk.sqlite")
	lib := cc1Library(t, db, "policy-bulk", "Policy bulk", "movie", "/policy-bulk")
	facetTestEntity(t, db, lib, "movie:existing")
	cc1Drain(t, db)
	before, err := FacetPolicyEpoch(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	genreBefore, err := FacetGenreEpoch(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 13; i++ {
		id := facetTestEntity(t, db, lib, fmt.Sprintf("movie:new:%d", i))
		cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
			if err := SetTermsTx(ctx, tx, id, VocabGenre, "scan", []Term{{Name: "New genre"}}); err != nil {
				return err
			}
			return SetAttributesTx(ctx, tx, id, "studio", []string{"New studio"})
		})
	}
	cc1Drain(t, db)
	after, err := FacetPolicyEpoch(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	genreAfter, err := FacetGenreEpoch(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("bulk new content/materialization blanket-revoked SIMPLE authority")
	}
	if genreAfter != genreBefore {
		t.Fatal("new unaccepted targets revoked Search genre authority")
	}
}
