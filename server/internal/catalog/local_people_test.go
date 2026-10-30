package catalog

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// Someone credited only by name in local metadata has a person page; when a
// provider names them on the same title they become that provider's person,
// keep their page, and later local credits find them.
func TestLocalPeopleHavePagesAndJoinTheProviderPerson(t *testing.T) {
	c := catalogtest.Open(t)
	movies := c.Library("m", "Movies", "movie", "/m")
	film := c.Movie(movies, "/m/Film.mkv", "Film", 2001)
	other := c.Movie(movies, "/m/Other.mkv", "Other", 2002)
	local := []compactcatalog.Credit{{PersonKey: compactcatalog.LocalPersonKey("Jane Doe"), PersonName: "Jane Doe", CreditID: "a", CreditedName: "Jane Doe", Role: "Actor", Department: "Acting"}}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if err := compactcatalog.SetCreditsTx(ctx, tx, film.ID, "nfo", local); err != nil {
			return err
		}
		// The provider's person already exists from another title.
		return compactcatalog.SetCreditsTx(ctx, tx, other.ID, "tmdb", []compactcatalog.Credit{{PersonKey: "tmdb:42", PersonName: "Jane Doe", ProviderPersonID: "42", CreditID: "b", CreditedName: "Jane Doe", Role: "Actor", Department: "Acting"}})
	})
	var token string
	if err := c.DB.QueryRow(`SELECT p.token FROM catalog_credits x JOIN catalog_people p ON p.id=x.person_id WHERE x.entity_id=?`, film.ID).Scan(&token); err != nil {
		t.Fatalf("a locally credited person has no person: %v", err)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.AdoptLocalPersonTx(ctx, tx, film.ID, "tmdb", "42", "Jane Doe")
	})
	if n := rows(t, c, `SELECT count(DISTINCT person_id) FROM catalog_credits`); n != 1 {
		t.Fatalf("people after the provider named her: %d", n)
	}
	s := New(c.DB)
	var provider string
	if err := c.DB.QueryRow(`SELECT token FROM catalog_people WHERE identity_key='tmdb:42'`).Scan(&provider); err != nil {
		t.Fatal(err)
	}
	if got := s.SurvivingPersonID(context.Background(), token); got != provider {
		t.Fatalf("old person page id leads to %q, want %q", got, provider)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetCreditsTx(ctx, tx, film.ID, "nfo", local)
	})
	if n := rows(t, c, `SELECT count(*) FROM catalog_people`); n != 1 {
		t.Fatalf("a later local credit made a second person: %d people", n)
	}
}
