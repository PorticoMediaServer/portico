package metadata

import (
	"context"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

type tvdbEpisodeFixture struct {
	tvdbFixture
	characters func(context.Context, int64) ([]metadataprovider.TVDBEpisodeCharacter, error)
}

func (f tvdbEpisodeFixture) EpisodeCharacters(c context.Context, id int64) ([]metadataprovider.TVDBEpisodeCharacter, error) {
	return f.characters(c, id)
}

// Spec — Page Content §2: a TVDB-matched episode's extended characters become
// normalized catalogue credits linked to people. One fetch per episode, then
// evidence stops refetches.
func TestTVDBEpisodeCreditsWriteGuestStarsAsPeople(t *testing.T) {
	db, _ := tvdbDB(t)
	defer db.Close()
	names := tvdbNames(t, db)
	s := New(db, "")
	calls := 0
	s.tvdb = tvdbEpisodeFixture{characters: func(_ context.Context, id int64) ([]metadataprovider.TVDBEpisodeCharacter, error) {
		calls++
		if id != 100 {
			return nil, errors.New("unexpected episode")
		}
		return []metadataprovider.TVDBEpisodeCharacter{{PersonID: 1, PersonName: "Ada Actor", Character: "Captain", Type: "Actor"}, {PersonID: 2, PersonName: "Gus Guest", Character: "Stranger", Type: "Guest Star"}}, nil
	}}
	if _, err := db.Exec(`INSERT INTO provider_evidence(item_id,provider,provider_id,payload,observed_at) VALUES(?,'tvdb',100,'{}','now')`, names["one"].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.tvdbEpisodeCreditsStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT p.name,role.label,department.label,c.ord FROM catalog_credits c JOIN catalog_people p ON p.id=c.person_id JOIN catalog_credit_labels role ON role.id=c.role_id JOIN catalog_credit_labels department ON department.id=c.department_id WHERE c.entity_id=? AND c.provider='tvdb' ORDER BY c.ord`, names["one"].ID)
	if err != nil {
		t.Fatal(err)
	}
	type credit struct {
		name, role, department string
		ordinal                int
	}
	got := []credit{}
	for rows.Next() {
		var c credit
		if err = rows.Scan(&c.name, &c.role, &c.department, &c.ordinal); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		got = append(got, c)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (credit{"Ada Actor", "Captain", "Acting", 0}) || got[1] != (credit{"Gus Guest", "Stranger", "Guest", 1}) {
		t.Fatalf("episode credits: %+v", got)
	}
	var key string
	if err = db.QueryRow(`SELECT identity_key FROM catalog_people WHERE identity_key='tvdb:1'`).Scan(&key); err != nil {
		t.Fatal("guest star is a person", err)
	}
	var links int
	if err = db.QueryRow(`SELECT count(*) FROM catalog_credits WHERE entity_id=? AND person_id IS NOT NULL`, names["one"].ID).Scan(&links); err != nil || links != 2 {
		t.Fatal("people links", links, err)
	}
	var credits, attempts int
	if err = db.QueryRow(`SELECT credits,attempts FROM tvdb_episode_credit_evidence WHERE item_id=? AND provider_id=100`, names["one"].ID).Scan(&credits, &attempts); err != nil || credits != 2 || attempts != 0 {
		t.Fatal("evidence", credits, attempts, err)
	}
	// A second step fetches nothing: the pair already has its evidence.
	if err = s.tvdbEpisodeCreditsStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("refetched", calls)
	}
}

func TestTVDBEpisodeCreditsFailuresRetryFiveTimes(t *testing.T) {
	db, _ := tvdbDB(t)
	defer db.Close()
	names := tvdbNames(t, db)
	s := New(db, "")
	calls := 0
	s.tvdb = tvdbEpisodeFixture{characters: func(context.Context, int64) ([]metadataprovider.TVDBEpisodeCharacter, error) {
		calls++
		return nil, errors.New("upstream gone")
	}}
	if _, err := db.Exec(`INSERT INTO provider_evidence(item_id,provider,provider_id,payload,observed_at) VALUES(?,'tvdb',100,'{}','now')`, names["one"].ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if err := s.tvdbEpisodeCreditsStep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 5 {
		t.Fatal("retry bound", calls)
	}
	var links int
	if err := db.QueryRow(`SELECT count(*) FROM catalog_credits WHERE entity_id=? AND provider='tvdb'`, names["one"].ID).Scan(&links); err != nil || links != 0 {
		t.Fatal("failed fetch wrote credits", links, err)
	}
}

// A library whose TVDB policy is off (local metadata only, or TVDB turned off)
// never asks TVDB for its episodes' credits.
func TestTVDBEpisodeCreditsSkipLibrariesWithTVDBOff(t *testing.T) {
	db, _ := tvdbDB(t)
	defer db.Close()
	names := tvdbNames(t, db)
	s := New(db, "")
	calls := 0
	s.tvdb = tvdbEpisodeFixture{characters: func(context.Context, int64) ([]metadataprovider.TVDBEpisodeCharacter, error) {
		calls++
		return nil, nil
	}}
	if _, err := db.Exec(`INSERT INTO provider_evidence(item_id,provider,provider_id,payload,observed_at) VALUES(?,'tvdb',100,'{}','now')`, names["one"].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE tvdb_provider_policies SET enabled=0`); err != nil {
		t.Fatal(err)
	}
	if err := s.tvdbEpisodeCreditsStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("TVDB asked for a library with TVDB off", calls)
	}
}
