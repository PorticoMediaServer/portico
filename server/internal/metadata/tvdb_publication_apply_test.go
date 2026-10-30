package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

func tvdbProjectionReady(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	s, db := tvdbPublicationDB(t)
	tvdbPublicationSelected(t, s)
	p := tvdbPublicationClaim(t, s)
	if err := s.commitTVDBPage(context.Background(), *p, metadataprovider.EpisodePage{SeriesID: 42, Order: "absolute", Page: 0, Episodes: []metadataprovider.Episode{tvdbPublicationEpisode(100, 1), tvdbPublicationEpisode(101, 2)}}); err != nil {
		t.Fatal(err)
	}
	return s, db
}
func tvdbProjectionClaim(t *testing.T, s *Service) *tvdbPublication {
	t.Helper()
	p, err := s.claimTVDBProjection(context.Background())
	if err != nil || p == nil {
		t.Fatal("projection claim", p, err)
	}
	return p
}

func tvdbUpdateEpisodeFacts(t *testing.T, db *sql.DB, item catalogtest.Item, fields map[string]any) {
	t.Helper()
	var showID int64
	var seasonID sql.NullInt64
	var numbering, localIdentityStatus, orderingBasis string
	var number int
	if err := db.QueryRow(`SELECT show_id,season_id,numbering,number,local_identity_status,ordering_basis FROM catalog_episodes WHERE entity_id=?`, item.ID).Scan(&showID, &seasonID, &numbering, &number, &localIdentityStatus, &orderingBasis); err != nil {
		t.Fatal(err)
	}
	var season any
	if seasonID.Valid {
		season = seasonID.Int64
	}
	facts := map[string]any{
		"show_id": showID, "season_id": season, "numbering": numbering, "number": number,
		"local_identity_status": localIdentityStatus, "ordering_basis": orderingBasis,
	}
	for name, value := range fields {
		facts[name] = value
	}
	catalogtest.New(t, db).Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetFieldsTx(ctx, tx, item.ID, compactcatalog.Automatic, facts)
	})
}

type tvdbLocalEpisode struct {
	name, title string
	number      int
}

func tvdbLocalEpisodes(t *testing.T, c *catalogtest.Catalog, show catalogtest.Item, episodes []tvdbLocalEpisode) catalogtest.Names {
	t.Helper()
	ids := make(map[string]int64, len(episodes))
	library := c.Handle("lib")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for _, episode := range episodes {
			id, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{
				Library: library, Kind: compactcatalog.Episode, Parent: show.ID,
				Key: "tvdb-publication:" + episode.name, Title: episode.title,
			})
			if err != nil {
				return err
			}
			if err = compactcatalog.SetFactsTx(ctx, tx, id, map[string]any{
				"show_id": show.ID, "season_id": nil, "numbering": "absolute", "number": episode.number,
				"local_identity_status": "parsed", "ordering_basis": "unspecified",
			}); err != nil {
				return err
			}
			ids[episode.name] = id
		}
		return nil
	})
	names := catalogtest.Names{}
	for name, id := range ids {
		names[name] = catalogtest.Item{ID: id, Public: c.Public(id)}
	}
	return names
}

func TestTVDBProjectionFreshTargetAndManualText(t *testing.T) {
	s, db := tvdbProjectionReady(t)
	epItem := tvdbPublicationNames(t, db)["ep"]
	auth := func(*sql.Tx) error { return nil }
	target := RepairTarget{Kind: "item", ID: epItem.Public}
	state, err := s.RepairState(context.Background(), target, auth)
	if err != nil {
		t.Fatal(err)
	}
	title := "My title"
	_, err = s.Repair(context.Background(), target, RepairCommand{ExpectedRevision: state.Revision, Action: "edit", Fields: map[string]RepairFieldEdit{"title": {Value: &title}}}, MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}, auth)
	if err != nil {
		t.Fatal(err)
	}
	p := tvdbProjectionClaim(t, s)
	if tvdbPublicationText(t, db, `SELECT count(*) FROM tvdb_projection_targets WHERE operation_id=?`, p.ID) != "1" {
		t.Fatal("missing concrete target")
	}
	if err := s.publishTVDBProjection(context.Background(), *p); err != nil {
		t.Fatal(err)
	}
	if tvdbPublicationText(t, db, `SELECT e.title||':'||d.overview FROM catalog_entities e JOIN catalog_item_details d ON d.entity_id=e.id WHERE e.id=?`, epItem.ID) != "My title:Observed overview" {
		t.Fatal("manual text lost")
	}
	if tvdbPublicationText(t, db, `SELECT automatic_value FROM metadata_owner_fields WHERE kind='item' AND entity_id=? AND field='title'`, epItem.ID) != "Observed episode" || tvdbPublicationText(t, db, `SELECT provider_id FROM provider_evidence WHERE item_id=? AND provider='tvdb'`, epItem.ID) != "100" {
		t.Fatal("automatic evidence not published")
	}
	if tvdbPublicationText(t, db, `SELECT complete FROM tvdb_projection_coverage`) != "1" {
		t.Fatal("coverage incomplete")
	}
}
func TestTVDBProjectionChangedTargetCannotPublish(t *testing.T) {
	changes := []struct {
		name   string
		mutate func(*testing.T, *sql.DB, catalogtest.Names)
	}{
		{"description", func(t *testing.T, db *sql.DB, names catalogtest.Names) {
			catalogtest.New(t, db).Fields(names["ep"].ID, map[string]any{"overview": "Owner edit"})
		}},
		{"manual", func(t *testing.T, db *sql.DB, names catalogtest.Names) {
			tvdbUpdateEpisodeFacts(t, db, names["ep"], map[string]any{"local_identity_status": "manual"})
		}},
		{"number", func(t *testing.T, db *sql.DB, names catalogtest.Names) {
			tvdbUpdateEpisodeFacts(t, db, names["ep"], map[string]any{"number": 2})
		}},
		{"source", func(t *testing.T, db *sql.DB, names catalogtest.Names) {
			tvdbPublicationExec(t, db, `UPDATE metadata_publication_heads SET source_revision=source_revision+1 WHERE item_id=?`, names["ep"].ID)
		}},
		{"identity", func(t *testing.T, db *sql.DB, names catalogtest.Names) {
			tvdbPublicationExec(t, db, `INSERT INTO provider_evidence(item_id,provider,provider_id,payload,observed_at) VALUES(?,'tvdb',999,'{}','now')`, names["ep"].ID)
		}},
		{"item_aba", func(t *testing.T, db *sql.DB, names catalogtest.Names) {
			c := catalogtest.New(t, db)
			c.Delete(names["ep"].ID)
			show := names["show"]
			names["ep"] = c.Entity(compactcatalog.Entity{Library: c.Handle("lib"), Kind: compactcatalog.Episode, Parent: show.ID, Key: "tvdb-publication:ep", Title: "New instance"}, map[string]any{
				"show_id": show.ID, "season_id": nil, "numbering": "absolute", "number": 1, "local_identity_status": "parsed",
			})
		}},
	}
	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			s, db := tvdbProjectionReady(t)
			names := tvdbPublicationNames(t, db)
			p := tvdbProjectionClaim(t, s)
			change.mutate(t, db, names)
			before := tvdbPublicationText(t, db, `SELECT e.title||':'||COALESCE(d.overview,'') FROM catalog_entities e LEFT JOIN catalog_item_details d ON d.entity_id=e.id WHERE e.id=?`, names["ep"].ID)
			err := s.publishTVDBProjection(context.Background(), *p)
			if !errors.Is(err, errTVDBStale) {
				t.Fatal("stale target accepted", err)
			}
			if err = s.failTVDBPublication(context.Background(), *p, err); err != nil {
				t.Fatal(err)
			}
			if tvdbPublicationText(t, db, `SELECT e.title||':'||COALESCE(d.overview,'') FROM catalog_entities e LEFT JOIN catalog_item_details d ON d.entity_id=e.id WHERE e.id=?`, names["ep"].ID) != before || tvdbPublicationText(t, db, `SELECT attempts FROM tvdb_jobs WHERE show_id=?`, names["show"].ID) != "0" {
				t.Fatal("stale target mutated content or retry budget")
			}
		})
	}
}
func TestTVDBProjectionIdentityConflictPreservesAccepted(t *testing.T) {
	s, db := tvdbProjectionReady(t)
	epItem := tvdbPublicationNames(t, db)["ep"]
	tvdbPublicationExec(t, db, `INSERT INTO provider_evidence(item_id,provider,provider_id,payload,observed_at) VALUES(?,'tvdb',999,'{}','now');INSERT INTO tvdb_episode_links(item_id,provider_id,status,observed_at) VALUES(?,999,'matched','now')`, epItem.ID, epItem.ID)
	p := tvdbProjectionClaim(t, s)
	if tvdbPublicationText(t, db, `SELECT result_status FROM tvdb_projection_targets WHERE operation_id=?`, p.ID) != "identity_conflict" {
		t.Fatal("accepted identity overwritten during resolve")
	}
	if err := s.publishTVDBProjection(context.Background(), *p); err != nil {
		t.Fatal(err)
	}
	if tvdbPublicationText(t, db, `SELECT provider_id FROM provider_evidence WHERE item_id=?`, epItem.ID) != "999" || tvdbPublicationText(t, db, `SELECT provider_id||':'||status FROM tvdb_episode_links WHERE item_id=?`, epItem.ID) != "999:identity_conflict" || tvdbPublicationText(t, db, `SELECT title FROM catalog_entities WHERE id=?`, epItem.ID) != "Accepted local title" {
		t.Fatal("conflict displaced accepted metadata")
	}
}
func TestTVDBProjectionProviderReceiptAndTargetDigest(t *testing.T) {
	for _, kind := range []string{"page", "target"} {
		t.Run(kind, func(t *testing.T) {
			s, db := tvdbProjectionReady(t)
			epItem := tvdbPublicationNames(t, db)["ep"]
			p := tvdbProjectionClaim(t, s)
			if kind == "page" {
				tvdbPublicationExec(t, db, `UPDATE tvdb_episode_evidence SET title='Edited cache' WHERE provider_id=100`)
			} else {
				tvdbPublicationExec(t, db, `UPDATE tvdb_projection_targets SET title='Edited target' WHERE operation_id=?`, p.ID)
			}
			err := s.publishTVDBProjection(context.Background(), *p)
			if err == nil {
				t.Fatal("edited stage published")
			}
			if err = s.failTVDBPublication(context.Background(), *p, err); err != nil {
				t.Fatal(err)
			}
			if tvdbPublicationText(t, db, `SELECT title FROM catalog_entities WHERE id=?`, epItem.ID) != "Accepted local title" || tvdbPublicationText(t, db, `SELECT count(*) FROM provider_evidence`) != "0" {
				t.Fatal("edited stage changed canonical")
			}
		})
	}
}
func TestTVDBProjectionBoundedContinuationRestartsBeforeCursor(t *testing.T) {
	s, db := tvdbProjectionReady(t)
	names := tvdbPublicationNames(t, db)
	show := names["show"]
	c := catalogtest.New(t, db)
	c.Delete(names["ep"].ID)
	otherShow := c.Show(c.Handle("lib"), "Other Show", 2020)
	parked := tvdbLocalEpisodes(t, c, otherShow, []tvdbLocalEpisode{{name: "before", title: "Before", number: 106}})
	episodes := make([]tvdbLocalEpisode, 105)
	for i := 1; i <= 105; i++ {
		episodes[i-1] = tvdbLocalEpisode{name: fmt.Sprintf("episode-%03d", i), title: "Local", number: i}
	}
	names = tvdbLocalEpisodes(t, c, show, episodes)
	p := tvdbProjectionClaim(t, s)
	if tvdbPublicationText(t, db, `SELECT count(*) FROM tvdb_projection_targets WHERE operation_id=?`, p.ID) != fmt.Sprint(tvdbProjectionTargets) {
		t.Fatal("target batch unbounded")
	}
	if err := s.publishTVDBProjection(context.Background(), *p); err != nil {
		t.Fatal(err)
	}
	if tvdbPublicationText(t, db, `SELECT apply_after FROM tvdb_jobs WHERE show_id=?`, show.ID) != fmt.Sprint(names[fmt.Sprintf("episode-%03d", tvdbProjectionTargets)].ID) {
		t.Fatal("wrong cursor")
	}
	parked["before"] = c.Entity(compactcatalog.Entity{Library: c.Handle("lib"), Kind: compactcatalog.Episode, Parent: show.ID, Key: "tvdb-publication:before", Title: "Before"}, map[string]any{
		"show_id": show.ID, "season_id": nil, "numbering": "absolute", "number": 106,
		"local_identity_status": "parsed", "ordering_basis": "unspecified",
	})
	p = tvdbProjectionClaim(t, s)
	if p.After != "" || tvdbPublicationText(t, db, `SELECT item_id FROM tvdb_projection_targets WHERE operation_id=? AND ordinal=0`, p.ID) != fmt.Sprint(parked["before"].ID) {
		t.Fatal("insertion before cursor omitted")
	}
	if err := s.publishTVDBProjection(context.Background(), *p); err != nil {
		t.Fatal(err)
	}
	for pages := 0; tvdbPublicationText(t, db, `SELECT status FROM tvdb_jobs WHERE show_id=?`, show.ID) != "complete"; pages++ {
		if pages > 106/tvdbProjectionTargets+1 {
			t.Fatal("projection did not finish in as many pages as it has targets")
		}
		p = tvdbProjectionClaim(t, s)
		if err := s.publishTVDBProjection(context.Background(), *p); err != nil {
			t.Fatal(err)
		}
	}
	if tvdbPublicationText(t, db, `SELECT count(*) FROM tvdb_episode_links`) != "106" {
		t.Fatal("bounded coverage missed target")
	}
}
func TestTVDBProjectionNewItemNeedsFreshClaim(t *testing.T) {
	s, db := tvdbProjectionReady(t)
	names := tvdbPublicationNames(t, db)
	p := tvdbProjectionClaim(t, s)
	c := catalogtest.New(t, db)
	names["new"] = c.Entity(compactcatalog.Entity{Library: c.Handle("lib"), Kind: compactcatalog.Episode, Parent: names["show"].ID, Key: "tvdb-publication:new", Title: "New local"}, map[string]any{
		"show_id": names["show"].ID, "season_id": nil, "numbering": "absolute", "number": 2,
		"local_identity_status": "parsed", "ordering_basis": "unspecified",
	})
	if err := s.publishTVDBProjection(context.Background(), *p); !errors.Is(err, errTVDBStale) {
		t.Fatal("old claim adopted inserted target", err)
	}
	if err := s.failTVDBPublication(context.Background(), *p, errTVDBStale); err != nil {
		t.Fatal(err)
	}
	fresh := tvdbProjectionClaim(t, s)
	if fresh.ID == p.ID {
		t.Fatal("old operation reused")
	}
	if err := s.publishTVDBProjection(context.Background(), *fresh); err != nil {
		t.Fatal(err)
	}
	if tvdbPublicationText(t, db, `SELECT title FROM catalog_entities WHERE id=?`, names["new"].ID) != "Observed episode" {
		t.Fatal("fresh target failed")
	}
}
func TestTVDBProjectionRollbackAndExpiry(t *testing.T) {
	t.Run("rollback", func(t *testing.T) {
		s, db := tvdbProjectionReady(t)
		epItem := tvdbPublicationNames(t, db)["ep"]
		p := tvdbProjectionClaim(t, s)
		tvdbPublicationExec(t, db, `CREATE TRIGGER reject_coverage BEFORE INSERT ON tvdb_projection_coverage BEGIN SELECT RAISE(ABORT,'controlled failure');END`)
		if err := s.publishTVDBProjection(context.Background(), *p); err == nil {
			t.Fatal("expected transaction failure")
		}
		if tvdbPublicationText(t, db, `SELECT title FROM catalog_entities WHERE id=?`, epItem.ID) != "Accepted local title" || tvdbPublicationText(t, db, `SELECT count(*) FROM provider_evidence`) != "0" || tvdbPublicationText(t, db, `SELECT status FROM tvdb_publication_operations WHERE id=?`, p.ID) != "ready" {
			t.Fatal("partial batch escaped rollback")
		}
	})
	t.Run("expiry", func(t *testing.T) {
		s, db := tvdbProjectionReady(t)
		p := tvdbProjectionClaim(t, s)
		s.now = func() time.Time { return time.Date(2026, 9, 5, 12, 2, 0, 0, time.UTC) }
		if err := s.publishTVDBProjection(context.Background(), *p); !errors.Is(err, errTVDBStale) {
			t.Fatal(err)
		}
		if tvdbPublicationText(t, db, `SELECT count(*) FROM provider_evidence`) != "0" {
			t.Fatal("expired projection wrote")
		}
	})
}
