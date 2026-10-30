package metadata

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestAttentionLooseEpisodeKeepAsIsOnlyReturnsAfterFileChange(t *testing.T) {
	c := catalogtest.Open(t)
	tv := c.Library("tv", "TV", "tv", "/tv")
	item := c.Entity(compactcatalog.Entity{Library: tv, Kind: compactcatalog.Episode, Key: "loose:source", Title: "Clean title"}, nil)
	item.Asset, item.Token = c.File(item.ID, "/tv/Clean.Title.1080p.mkv", 90)
	c.Drain()
	service := New(c.DB, "")
	ctx := context.Background()
	page, err := service.Attention(ctx, "tv", "", 25)
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("loose list %+v %v", page, err)
	}
	state, err := service.RepairState(ctx, RepairTarget{Kind: "item", ID: item.Public}, allowAll)
	if err != nil {
		t.Fatal(err)
	}
	if state.Snapshot.Fields["episodeNumber"].Value != "" || state.Snapshot.Fields["seasonNumber"].Value != "" {
		t.Fatal("invented loose episode numbers", state.Snapshot.Fields)
	}
	entry := page.Entries[0]
	if entry.ItemKind != "episode" || entry.ParentID != "" || entry.Title != "Clean title" || len(entry.Candidates) != 0 {
		t.Fatal(entry)
	}
	command := AttentionCommand{Kind: "item", EntityID: item.Public, ExpectedRevision: entry.Revision, Action: "dismiss"}
	denied := errors.New("denied")
	if err = service.AttentionAction(ctx, "tv", command, MBActor{}, func(*sql.Tx) error { return denied }); !errors.Is(err, denied) {
		t.Fatal(err)
	}
	if err = service.AttentionAction(ctx, "tv", command, MBActor{}, allowAll); err != nil {
		t.Fatal(err)
	}
	page, err = service.Attention(ctx, "tv", "", 25)
	if err != nil || len(page.Entries) != 0 {
		t.Fatal(page, err)
	}
	var available int
	if err = c.DB.QueryRow(`SELECT available FROM catalog_item_availability WHERE entity_id=?`, item.ID).Scan(&available); err != nil || available != 1 {
		t.Fatal("Keep as is changed playability", available, err)
	}
	if err = dbwork.WithWriteTx(ctx, c.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE catalog_assets SET modified_ns=modified_ns+1 WHERE id=?`, item.Asset)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	page, err = service.Attention(ctx, "tv", "", 25)
	if err != nil || len(page.Entries) != 1 || page.Entries[0].EntityID != item.Public {
		t.Fatal("changed file identity", page, err)
	}
	if err = service.AttentionAction(ctx, "tv", command, MBActor{}, allowAll); !errors.Is(err, ErrScreenConflict) {
		t.Fatal("stale file decision accepted", err)
	}
	if err = dbwork.WithWriteTx(ctx, c.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE library_metadata_agents SET agent='local' WHERE library_id='tv'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	page, err = service.Attention(ctx, "tv", "", 25)
	if err != nil || page.Total != 0 || len(page.Entries) != 0 {
		t.Fatal("local-only library listed unmatched", page, err)
	}
}

func TestAttentionCandidatePosterUsesOwnerProxyWithoutFetching(t *testing.T) {
	service, db, _ := screenFixture(t)
	item := publicationFixtureMovie(t, db)
	screenConfirm(t, service)
	ctx := context.Background()
	publicationExec(t, db, `UPDATE screen_metadata_work SET year_seed=0,title_seed='Film',status='pending' WHERE target_id=?`, item.ID)
	if err := service.ScreenStep(ctx); err != nil {
		t.Fatal(err)
	}
	publicationExec(t, db, `UPDATE screen_metadata_candidates SET payload=json_set(payload,'$.posterPath','/candidate.jpg') WHERE target_id=?`, item.ID)
	page, err := service.Attention(ctx, "lib", "", 25)
	if err != nil || len(page.Entries) != 1 || len(page.Entries[0].Candidates) != 1 {
		t.Fatal(page, err)
	}
	candidate := page.Entries[0].Candidates[0]
	if !strings.HasPrefix(candidate.PosterURL, "/v1/metadata/item/"+item.Public+"/art/poster?candidate=attention%3A") {
		t.Fatal("provider image escaped proxy", candidate.PosterURL)
	}
	denied := errors.New("revoked")
	if _, _, err = service.PreviewAttentionArtwork(ctx, RepairTarget{Kind: "item", ID: item.Public}, candidate.Key, ArtworkSmall, func(*sql.Tx) error { return denied }); !errors.Is(err, denied) {
		t.Fatal("preview authority not rechecked", err)
	}
	var jobs int
	if err = db.QueryRow(`SELECT count(*) FROM artwork_jobs`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 0 {
		t.Fatal("listing waited on or queued provider images", jobs)
	}
	target := RepairTarget{Kind: "item", ID: item.Public}
	if _, _, err = service.PreviewAttentionArtwork(ctx, target, candidate.Key, ArtworkSmall, allowAll); !errors.Is(err, ErrArtworkPending) {
		t.Fatal("authorized candidate was not queued for the existing artwork worker", err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM artwork_jobs WHERE preview=1 AND origin='https://image.tmdb.org/t/p/original/candidate.jpg'`).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatal("candidate preview did not retain its allowlisted origin", jobs, err)
	}
	publicationExec(t, db, `UPDATE screen_metadata_work SET title_seed='Changed after search' WHERE target_id=?`, item.ID)
	if _, _, err = service.PreviewAttentionArtwork(ctx, target, candidate.Key, ArtworkSmall, allowAll); !errors.Is(err, ErrScreenConflict) {
		t.Fatal("stale candidate was not refused", err)
	}
}
