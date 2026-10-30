package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// Root must provide its independently reviewed actual Open installer composition
// for this source checkpoint. No schema text or exported test-only installer is
// substituted here, and a missing dependency fails rather than skips.
func tvdbPublicationDB(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var present int
	if err = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN('metadata_publication_heads','tvdb_publication_operations')`).Scan(&present); err != nil || present != 2 {
		t.Fatal("root installer composition required", present, err)
	}
	c := catalogtest.New(t, db)
	lib := c.Library("lib", "TV", "tv", "/synthetic")
	show := c.Show(lib, "Fixture Show", 2020)
	c.Entity(compactcatalog.Entity{Library: lib, Kind: compactcatalog.Episode, Parent: show.ID, Key: "tvdb-publication:ep", Title: "Accepted local title"}, map[string]any{
		"show_id": show.ID, "season_id": nil, "numbering": "absolute", "number": 1,
		"local_identity_status": "parsed", "overview": "Accepted overview",
	})
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	s := &Service{db: db, now: func() time.Time { return now }}
	return s, db
}

func tvdbPublicationNames(t *testing.T, db *sql.DB) catalogtest.Names {
	t.Helper()
	names := catalogtest.Names{}
	for _, fixture := range []struct {
		name, title string
		kind        int
	}{
		{"show", "Fixture Show", 2}, {"ep", "Accepted local title", 4},
	} {
		var item catalogtest.Item
		if err := db.QueryRow(`SELECT id,pid(public_id) FROM catalog_entities WHERE kind=? AND title=? ORDER BY id LIMIT 1`, fixture.kind, fixture.title).Scan(&item.ID, &item.Public); err != nil {
			t.Fatalf("fixture %q: %v", fixture.name, err)
		}
		names[fixture.name] = item
	}
	return names
}
func tvdbPublicationExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatal(q, err)
	}
}
func tvdbPublicationText(t *testing.T, db *sql.DB, q string, args ...any) string {
	t.Helper()
	var v string
	if err := db.QueryRow(q, args...).Scan(&v); err != nil {
		t.Fatal(q, err)
	}
	return v
}
func tvdbPublicationClaim(t *testing.T, s *Service) *tvdbPublication {
	t.Helper()
	p, err := s.claimTVDBAcquisition(context.Background())
	if err != nil || p == nil {
		t.Fatal("claim", p, err)
	}
	return p
}
func tvdbPublicationCandidates() []metadataprovider.SeriesCandidate {
	return []metadataprovider.SeriesCandidate{{ID: "42", Type: "series", Name: "Alternate name", Year: "2020", Aliases: []string{"Fixture Show"}, Overview: "Observed show"}}
}
func tvdbPublicationEpisode(id int64, number int) metadataprovider.Episode {
	return metadataprovider.Episode{ID: id, SeriesID: 42, AbsoluteNumber: &number, Name: "Observed episode", Overview: "Observed overview"}
}
func tvdbPublicationSelected(t *testing.T, s *Service) {
	t.Helper()
	p := tvdbPublicationClaim(t, s)
	if err := s.publishTVDBSearch(context.Background(), *p, tvdbPublicationCandidates()); err != nil {
		t.Fatal(err)
	}
	show := tvdbPublicationNames(t, s.db)["show"].Public
	state, err := s.TVDBState(show)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SelectTVDB(show, TVDBSelection{ExpectedRevision: state.Revision, ProviderID: 42, Order: "absolute"}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestTVDBAcquisitionSearchAliasRequiresOrder(t *testing.T) {
	s, db := tvdbPublicationDB(t)
	names := tvdbPublicationNames(t, db)
	p := tvdbPublicationClaim(t, s)
	s.tvdb = tvdbFixture{search: func(_ context.Context, title string, year int) ([]metadataprovider.SeriesCandidate, error) {
		if title != "Fixture Show" || year != 2020 {
			t.Fatal("query substituted")
		}
		return tvdbPublicationCandidates(), nil
	}}
	if err := s.runTVDBAcquisition(context.Background(), *p); err != nil {
		t.Fatal(err)
	}
	if got := tvdbPublicationText(t, db, `SELECT status||':'||provider_id||':'||episode_order FROM tvdb_jobs WHERE show_id=?`, names["show"].ID); got != "needs_order:42:" {
		t.Fatal(got)
	}
	if tvdbPublicationText(t, db, `SELECT count(*) FROM provider_evidence`) != "0" || tvdbPublicationText(t, db, `SELECT count(*) FROM tvdb_publication_candidate_aliases`) != "1" {
		t.Fatal("alias stage touched accepted identity")
	}
	if tvdbPublicationText(t, db, `SELECT query_title||':'||query_year||':'||lease_until FROM tvdb_publication_operations WHERE id=?`, p.ID) != "Fixture Show:2020:"+p.LeaseUntil {
		t.Fatal("missing captured query")
	}
	if tvdbPublicationText(t, db, `SELECT title FROM catalog_entities WHERE id=?`, names["ep"].ID) != "Accepted local title" {
		t.Fatal("search changed episode")
	}
}
func TestTVDBAcquisitionSearchStaleSuccessAndFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	changes := []struct {
		name   string
		mutate func(*testing.T, *sql.DB, catalogtest.Names)
	}{
		{"title_aba", func(t *testing.T, db *sql.DB, names catalogtest.Names) {
			c := catalogtest.New(t, db)
			c.Fields(names["show"].ID, map[string]any{"title": "Changed"})
			c.Fields(names["show"].ID, map[string]any{"title": "Fixture Show"})
		}},
		{"policy_aba", func(t *testing.T, db *sql.DB, _ catalogtest.Names) {
			tvdbPublicationExec(t, db, `UPDATE tvdb_provider_policies SET enabled=0;UPDATE tvdb_provider_policies SET enabled=1`)
		}},
		{"source", func(t *testing.T, db *sql.DB, names catalogtest.Names) {
			tvdbPublicationExec(t, db, `UPDATE metadata_publication_heads SET source_revision=source_revision+1 WHERE item_id=?`, names["ep"].ID)
		}},
		{"job_revision", func(t *testing.T, db *sql.DB, names catalogtest.Names) {
			tvdbPublicationExec(t, db, `UPDATE tvdb_jobs SET revision=revision+1 WHERE show_id=?`, names["show"].ID)
		}},
		{"job_aba", func(t *testing.T, db *sql.DB, names catalogtest.Names) {
			tvdbPublicationExec(t, db, `DELETE FROM tvdb_jobs WHERE show_id=?;INSERT INTO tvdb_jobs(show_id) VALUES(?)`, names["show"].ID, names["show"].ID)
		}},
	}
	for _, change := range changes {
		for _, failure := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/failure_%t", change.name, failure), func(t *testing.T) {
				s, db := tvdbPublicationDB(t)
				names := tvdbPublicationNames(t, db)
				p := tvdbPublicationClaim(t, s)
				s.tvdb = tvdbFixture{search: func(context.Context, string, int) ([]metadataprovider.SeriesCandidate, error) {
					change.mutate(t, db, names)
					if failure {
						return nil, &metadataprovider.Error{Provider: "tvdb", Code: "unavailable", RetryAfter: time.Hour}
					}
					return tvdbPublicationCandidates(), nil
				}}
				if err := s.runTVDBAcquisition(context.Background(), *p); err != nil {
					t.Fatal(err)
				}
				if tvdbPublicationText(t, db, `SELECT count(*) FROM tvdb_series_candidates`) != "0" || tvdbPublicationText(t, db, `SELECT count(*) FROM metadata_provider_cooldowns`) != "0" {
					t.Fatal("stale result/error escaped")
				}
				if tvdbPublicationText(t, db, `SELECT status FROM tvdb_publication_operations WHERE id=?`, p.ID) != "stale" {
					t.Fatal("no stale receipt")
				}
				if tvdbPublicationText(t, db, `SELECT attempts FROM tvdb_jobs WHERE show_id=?`, names["show"].ID) != "0" {
					t.Fatal("stale failure changed attempts")
				}
			})
		}
	}
}
func TestTVDBAcquisitionPageAttestationAndNoProjection(t *testing.T) {
	s, db := tvdbPublicationDB(t)
	names := tvdbPublicationNames(t, db)
	tvdbPublicationSelected(t, s)
	p := tvdbPublicationClaim(t, s)
	next := 1
	if err := s.commitTVDBPage(context.Background(), *p, metadataprovider.EpisodePage{SeriesID: 42, Order: "absolute", Page: 0, NextPage: &next, Episodes: []metadataprovider.Episode{tvdbPublicationEpisode(100, 1)}}); err != nil {
		t.Fatal(err)
	}
	if tvdbPublicationText(t, db, `SELECT complete FROM tvdb_publication_sets`) != "0" {
		t.Fatal("partial set complete")
	}
	p = tvdbPublicationClaim(t, s)
	if err := s.commitTVDBPage(context.Background(), *p, metadataprovider.EpisodePage{SeriesID: 42, Order: "absolute", Page: 1, Episodes: []metadataprovider.Episode{tvdbPublicationEpisode(101, 2)}}); err != nil {
		t.Fatal(err)
	}
	if tvdbPublicationText(t, db, `SELECT count(*) FROM tvdb_publication_pages WHERE content_revision=validated_revision AND length(result_digest)=64`) != "2" || tvdbPublicationText(t, db, `SELECT complete FROM tvdb_publication_sets`) != "1" {
		t.Fatal("missing attestation")
	}
	if tvdbPublicationText(t, db, `SELECT status FROM tvdb_jobs WHERE show_id=?`, names["show"].ID) != "pending_apply" || tvdbPublicationText(t, db, `SELECT title FROM catalog_entities WHERE id=?`, names["ep"].ID) != "Accepted local title" || tvdbPublicationText(t, db, `SELECT count(*) FROM provider_evidence`) != "0" {
		t.Fatal("acquisition projected local metadata")
	}
}
func TestTVDBAcquisitionDirtyPriorPageReacquires(t *testing.T) {
	s, db := tvdbPublicationDB(t)
	names := tvdbPublicationNames(t, db)
	tvdbPublicationSelected(t, s)
	p := tvdbPublicationClaim(t, s)
	next := 1
	if err := s.commitTVDBPage(context.Background(), *p, metadataprovider.EpisodePage{SeriesID: 42, Order: "absolute", Page: 0, NextPage: &next, Episodes: []metadataprovider.Episode{tvdbPublicationEpisode(100, 1)}}); err != nil {
		t.Fatal(err)
	}
	tvdbPublicationExec(t, db, `UPDATE tvdb_episode_evidence SET title=title`)
	fresh := tvdbPublicationClaim(t, s)
	if fresh.Page != 0 || fresh.Generation <= p.Generation || fresh.Set == p.Set {
		t.Fatal("invalid receipts reused", fresh)
	}
	if tvdbPublicationText(t, db, `SELECT count(*) FROM tvdb_episode_evidence`) != "0" || tvdbPublicationText(t, db, `SELECT title FROM catalog_entities WHERE id=?`, names["ep"].ID) != "Accepted local title" {
		t.Fatal("reacquisition changed display or retained unowned input")
	}
}
func TestTVDBAcquisitionPageScopeAndLocalChanges(t *testing.T) {
	for _, localOnly := range []bool{false, true} {
		for _, failure := range []bool{false, true} {
			t.Run(fmt.Sprintf("local_%t/failure_%t", localOnly, failure), func(t *testing.T) {
				s, db := tvdbPublicationDB(t)
				names := tvdbPublicationNames(t, db)
				tvdbPublicationSelected(t, s)
				p := tvdbPublicationClaim(t, s)
				s.tvdb = tvdbFixture{pages: func(context.Context, int64, metadataprovider.EpisodeOrder, int) (metadataprovider.EpisodePage, error) {
					if localOnly {
						tvdbUpdateEpisodeFacts(t, db, names["ep"], map[string]any{"number": 2})
					} else {
						tvdbPublicationExec(t, db, `UPDATE tvdb_jobs SET generation=generation+1,revision=revision+1,episode_order='dvd' WHERE show_id=?`, names["show"].ID)
					}
					if failure {
						return metadataprovider.EpisodePage{}, &metadataprovider.Error{Provider: "tvdb", Code: "unavailable"}
					}
					return metadataprovider.EpisodePage{SeriesID: 42, Order: "absolute", Page: 0, Episodes: []metadataprovider.Episode{tvdbPublicationEpisode(100, 1)}}, nil
				}}
				if err := s.runTVDBAcquisition(context.Background(), *p); err != nil {
					t.Fatal(err)
				}
				expected := "stale"
				if localOnly {
					expected = "applied"
					if failure {
						expected = "failed"
					}
				}
				if tvdbPublicationText(t, db, `SELECT status FROM tvdb_publication_operations WHERE id=?`, p.ID) != expected {
					t.Fatal("wrong phase fence")
				}
			})
		}
	}
}
func TestTVDBAcquisitionDuplicatePageRollsBack(t *testing.T) {
	s, db := tvdbPublicationDB(t)
	tvdbPublicationSelected(t, s)
	p := tvdbPublicationClaim(t, s)
	next := 1
	if err := s.commitTVDBPage(context.Background(), *p, metadataprovider.EpisodePage{SeriesID: 42, Order: "absolute", Page: 0, NextPage: &next, Episodes: []metadataprovider.Episode{tvdbPublicationEpisode(100, 1)}}); err != nil {
		t.Fatal(err)
	}
	digest := tvdbPublicationText(t, db, `SELECT result_digest FROM tvdb_publication_pages WHERE page=0`)
	p = tvdbPublicationClaim(t, s)
	if err := s.commitTVDBPage(context.Background(), *p, metadataprovider.EpisodePage{SeriesID: 42, Order: "absolute", Page: 1, Episodes: []metadataprovider.Episode{tvdbPublicationEpisode(100, 2)}}); err == nil {
		t.Fatal("duplicate accepted")
	}
	if tvdbPublicationText(t, db, `SELECT count(*) FROM tvdb_publication_pages`) != "1" || tvdbPublicationText(t, db, `SELECT result_digest FROM tvdb_publication_pages WHERE page=0`) != digest || tvdbPublicationText(t, db, `SELECT page FROM tvdb_evidence_page_ownership`) != "0" {
		t.Fatal("duplicate moved ownership or changed prior page")
	}
}
func TestTVDBAcquisitionCancellationAndExpiry(t *testing.T) {
	s, db := tvdbPublicationDB(t)
	p := tvdbPublicationClaim(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	s.tvdb = tvdbFixture{search: func(context.Context, string, int) ([]metadataprovider.SeriesCandidate, error) {
		cancel()
		return nil, &metadataprovider.Error{Provider: "tvdb", Code: "unavailable", RetryAfter: time.Hour}
	}}
	if err := s.runTVDBAcquisition(ctx, *p); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if tvdbPublicationText(t, db, `SELECT attempts FROM tvdb_jobs`) != "0" || tvdbPublicationText(t, db, `SELECT count(*) FROM metadata_provider_cooldowns`) != "0" {
		t.Fatal("cancellation wrote failure")
	}
	s.now = func() time.Time { return time.Date(2026, 9, 5, 12, 2, 0, 0, time.UTC) }
	if err := s.publishTVDBSearch(context.Background(), *p, tvdbPublicationCandidates()); !errors.Is(err, errTVDBStale) {
		t.Fatal("expired result accepted", err)
	}
	fresh := tvdbPublicationClaim(t, s)
	if fresh.ID == p.ID {
		t.Fatal("expired lease reused")
	}
}
func TestTVDBAcquisitionBoundsAndRollback(t *testing.T) {
	t.Run("alias", func(t *testing.T) {
		s, db := tvdbPublicationDB(t)
		p := tvdbPublicationClaim(t, s)
		bad := tvdbPublicationCandidates()
		bad[0].Aliases = []string{strings.Repeat("x", 2049)}
		if err := s.publishTVDBSearch(context.Background(), *p, bad); err == nil {
			t.Fatal("oversized alias")
		}
		if tvdbPublicationText(t, db, `SELECT count(*) FROM tvdb_publication_candidates`) != "0" {
			t.Fatal("partial stage")
		}
	})
	t.Run("sql_rollback", func(t *testing.T) {
		s, db := tvdbPublicationDB(t)
		p := tvdbPublicationClaim(t, s)
		tvdbPublicationExec(t, db, `CREATE TRIGGER reject_search_publish BEFORE UPDATE OF status ON tvdb_jobs WHEN NEW.status='needs_order' BEGIN SELECT RAISE(ABORT,'controlled failure');END`)
		if err := s.publishTVDBSearch(context.Background(), *p, tvdbPublicationCandidates()); err == nil {
			t.Fatal("expected rollback")
		}
		if tvdbPublicationText(t, db, `SELECT count(*) FROM tvdb_series_candidates`) != "0" || tvdbPublicationText(t, db, `SELECT count(*) FROM tvdb_publication_candidates`) != "0" {
			t.Fatal("partial publication escaped")
		}
	})
	t.Run("eight_live", func(t *testing.T) {
		s, db := tvdbPublicationDB(t)
		c := catalogtest.New(t, db)
		library := c.Handle("lib")
		for i := 0; i < 8; i++ {
			c.Show(library, fmt.Sprintf("Other %d", i), 2020)
		}
		for i := 0; i < 8; i++ {
			tvdbPublicationClaim(t, s)
		}
		p, err := s.claimTVDBAcquisition(context.Background())
		if err != nil || p != nil {
			t.Fatal("live cap", p, err)
		}
	})
}

func TestTVDBAcquisitionAnimeAbsoluteOrder(t *testing.T) {
	s, db := tvdbPublicationDB(t)
	names := tvdbPublicationNames(t, db)
	tvdbPublicationExec(t, db, `UPDATE libraries SET kind='anime' WHERE id='lib'`)
	// The source-policy change must publish before provider acquisition.
	settleMetadataCatalogue(t, db)
	tvdbPublicationSelected(t, s)
	p := tvdbPublicationClaim(t, s)
	s.tvdb = tvdbFixture{pages: func(_ context.Context, id int64, order metadataprovider.EpisodeOrder, page int) (metadataprovider.EpisodePage, error) {
		if id != 42 || order != "absolute" || page != 0 {
			t.Fatal("anime accepted scope changed", id, order, page)
		}
		return metadataprovider.EpisodePage{SeriesID: id, Order: order, Page: page, Episodes: []metadataprovider.Episode{tvdbPublicationEpisode(100, 1)}}, nil
	}}
	if err := s.runTVDBAcquisition(context.Background(), *p); err != nil {
		t.Fatal(err)
	}
	if tvdbPublicationText(t, db, `SELECT status||':'||episode_order FROM tvdb_jobs WHERE show_id=?`, names["show"].ID) != "pending_apply:absolute" {
		t.Fatal("anime acquisition did not complete")
	}
	if tvdbPublicationText(t, db, `SELECT numbering||':'||number FROM catalog_episodes WHERE entity_id=?`, names["ep"].ID) != "absolute:1" {
		t.Fatal("anime local numbering changed")
	}
}

func tvdbPublicationManyShows(t *testing.T, c *catalogtest.Catalog, count int) {
	t.Helper()
	library := c.Handle("lib")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for i := 0; i < count; i++ {
			key := fmt.Sprintf("unrelated:%04d", i)
			id, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{
				Library: library, Kind: compactcatalog.Show, Key: compactcatalog.ShowKey(key), Title: "Other", Year: 2020,
			})
			if err != nil {
				return err
			}
			if err = compactcatalog.SetFactsTx(ctx, tx, id, map[string]any{"local_key": key}); err != nil {
				return err
			}
		}
		return nil
	})
}

func TestTVDBAcquisitionCleanupUsesIndexedOwner(t *testing.T) {
	s, db := tvdbPublicationDB(t)
	tvdbPublicationManyShows(t, catalogtest.New(t, db), 1000)
	p := tvdbPublicationClaim(t, s)
	tvdbPublicationExec(t, db, `INSERT INTO tvdb_publication_candidates VALUES(?,0,42,'Retained',2020,'','{}')`, p.ID)
	tvdbPublicationExec(t, db, `UPDATE tvdb_publication_operations SET status='failed' WHERE id=?`, p.ID)
	for _, q := range []string{tvdbTerminalPayloadQuery, tvdbTerminalDeleteQuery} {
		args := []any{tvdbStamp(s.publicationTime())}
		if q == tvdbTerminalDeleteQuery {
			args = append(args, tvdbStamp(s.publicationTime().Add(-24*time.Hour)))
		}
		rows, err := db.Query(`EXPLAIN QUERY PLAN `+q, args...)
		if err != nil {
			t.Fatal(err)
		}
		indexed := false
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if strings.Contains(detail, "SCAN j") {
				rows.Close()
				t.Fatal("catalog job scan in cleanup", detail)
			}
			if strings.Contains(detail, "SEARCH j") && (strings.Contains(detail, "show_id=?") || strings.Contains(detail, "rowid=?")) {
				indexed = true
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !indexed {
			t.Fatal("cleanup lacks indexed captured owner")
		}
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.cleanupTVDBPublications(context.Background(), tx); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if tvdbPublicationText(t, db, `SELECT count(*) FROM tvdb_publication_candidates WHERE operation_id=?`, p.ID) != "1" || tvdbPublicationText(t, db, `SELECT lease FROM tvdb_jobs WHERE show_id=?`, p.ShowID) != p.ID {
		t.Fatal("matching live lease or payload was removed")
	}
	tvdbPublicationExec(t, db, `UPDATE tvdb_jobs SET lease='',lease_until='' WHERE show_id=?`, p.ShowID)
	tx, err = db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.cleanupTVDBPublications(context.Background(), tx); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if tvdbPublicationText(t, db, `SELECT count(*) FROM tvdb_publication_candidates WHERE operation_id=?`, p.ID) != "0" || tvdbPublicationText(t, db, `SELECT count(*) FROM tvdb_publication_operations WHERE id=?`, p.ID) != "1" {
		t.Fatal("terminal compaction did not retain compact receipt")
	}
}
