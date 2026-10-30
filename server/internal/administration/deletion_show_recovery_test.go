package administration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func showDeleteFixture(t *testing.T, episodes int) (*Service, *sql.DB, string, catalogtest.Item, catalogtest.Item, []catalogtest.Item) {
	t.Helper()
	s, db, root := newService(t)
	c := catalogtest.New(t, db)
	library := c.Library("tv", "TV", "tv", root)
	show := c.Show(library, "Show", 2020)
	season := c.Season(show, 1)
	for n := 2; n <= 41; n++ {
		c.Season(show, n)
	}
	var members []catalogtest.Item
	for n := 1; n <= episodes; n++ {
		members = append(members, c.Episode(show, season, n, filepath.Join(root, fmt.Sprintf("episode-%d.mkv", n))))
	}
	c.Drain()
	doc, e := s.LibrarySettingsFor(context.Background(), allow, "tv")
	if e != nil {
		t.Fatal(e)
	}
	doc.Settings.AllowMediaDeletion = true
	if _, e = s.SaveLibrarySettings(context.Background(), allow, "tv", Change[LibrarySettings]{ExpectedRevision: doc.Revision, Settings: doc.Settings, OperationID: "show-policy"}); e != nil {
		t.Fatal(e)
	}
	return s, db, root, show, season, members
}

func showDeleteRequest(t *testing.T, s *Service, item, operation string) DeleteRequest {
	t.Helper()
	p, e := s.PreviewDelete(context.Background(), allow, []string{item})
	if e != nil {
		t.Fatal(e)
	}
	return DeleteRequest{ItemIDs: []string{item}, ExpectedRevision: p.Revision, Confirmation: p.Confirmation, OperationID: operation}
}

func assertShowReceiptReplay(t *testing.T, s *Service, db *sql.DB, request DeleteRequest, show int64) {
	t.Helper()
	var entities, receipts int
	if e := db.QueryRow(`SELECT count(*) FROM catalog_entities WHERE id=?`, show).Scan(&entities); e != nil {
		t.Fatal(e)
	}
	if e := db.QueryRow(`SELECT count(*) FROM admin_receipts WHERE scope='media-delete' AND operation_id=?`, request.OperationID).Scan(&receipts); e != nil {
		t.Fatal(e)
	}
	if entities != 0 || receipts != 1 {
		t.Fatal("Show result preceded final receipt", entities, receipts)
	}
	out, e := s.Delete(context.Background(), allow, "owner", request)
	if e != nil || out.Removed != 1 || out.Failed != 0 {
		t.Fatal("Show receipt replay", out, e)
	}
}

func TestP252bAdministrationWholeShowFortyOneEpisodesReceiptWithoutScheduler(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, _, show, _, _ := showDeleteFixture(t, 41)
	request := showDeleteRequest(t, s, show.Public, "whole-show")
	previous := 41
	auth := func(ctx context.Context, tx *sql.Tx) error {
		var seasons int
		if e := tx.QueryRow(`SELECT count(*) FROM catalog_seasons WHERE show_id=?`, show.ID).Scan(&seasons); e != nil {
			return e
		}
		if previous-seasons > 1 {
			t.Fatalf("whole Show writer deleted Season list: %d -> %d", previous, seasons)
		}
		previous = seasons
		return nil
	}
	out, e := s.Delete(context.Background(), auth, "owner", request)
	if e != nil || out.Removed != 1 || out.Failed != 0 {
		t.Fatal("canonical whole Show deletion", out, e)
	}
	var automatic, anchors int
	db.QueryRow(`SELECT count(*) FROM catalog_entity_mutations WHERE owner_kind='empty-parent' AND subject_id=?`, show.ID).Scan(&automatic)
	db.QueryRow(`SELECT count(*) FROM catalog_entity_mutations WHERE owner_kind='administration' AND cause_operation='whole-show' AND parent_mutation_id IS NULL AND acknowledged=1`).Scan(&anchors)
	if automatic != 0 || anchors != 1 {
		t.Fatal("whole Show lost one command anchor", automatic, anchors)
	}
	assertShowReceiptReplay(t, s, db, request, show.ID)
}

func TestP252bAdministrationWholeShowFileFailureAdmitsNoIntent(t *testing.T) {
	s, db, root, show, _, _ := showDeleteFixture(t, 1)
	if e := os.WriteFile(filepath.Join(root, "episode-1.mkv"), []byte("replacement bytes"), 0600); e != nil {
		t.Fatal(e)
	}
	request := showDeleteRequest(t, s, show.Public, "show-file-failure")
	request.DeleteFiles = true
	out, e := s.Delete(context.Background(), allow, "owner", request)
	if e != nil || out.Failed != 1 || out.Removed != 0 {
		t.Fatal(out, e)
	}
	var intents int
	db.QueryRow(`SELECT count(*) FROM catalog_entity_mutations`).Scan(&intents)
	if intents != 0 {
		t.Fatal("file refusal left upfront intent", intents)
	}
}

func TestP252bAdministrationFreshEmptyShowSupersedesInterruptedSeason(t *testing.T) {
	s, db, root, show, season, _ := showDeleteFixture(t, 0)
	old := showDeleteRequest(t, s, season.Public, "old-empty-season")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	auth := func(ctx context.Context, tx *sql.Tx) error {
		var active bool
		if e := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_entity_mutations WHERE owner_kind='administration' AND cause_operation='old-empty-season' AND status='active')`).Scan(&active); e != nil {
			return e
		}
		if active {
			cancel()
			return context.Canceled
		}
		return nil
	}
	if _, e := s.Delete(ctx, auth, "owner", old); !errors.Is(e, context.Canceled) {
		t.Fatal("empty Season not interrupted after admission", e)
	}
	if e := db.Close(); e != nil {
		t.Fatal(e)
	}
	var e error
	db, e = persistence.Open(filepath.Join(root, "server.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s = New(db)
	fresh := showDeleteRequest(t, s, show.Public, "changed-show-approval")
	calls := 0
	changed := func(ctx context.Context, tx *sql.Tx) error {
		calls++
		if calls != 2 {
			return nil
		}
		asset, _, e := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: filepath.Join(root, "new-show-file"), Size: 1, ModifiedNS: 1})
		if e != nil {
			return e
		}
		return compactcatalog.LinkAssetTx(ctx, tx, show.ID, asset, compactcatalog.Link{})
	}
	out, e := s.Delete(context.Background(), changed, "owner", fresh)
	if e != nil || out.Failed != 1 || out.Removed != 0 {
		t.Fatal("changed approval did not refuse before unwind", out, e)
	}
	var active, oldReceipts int
	db.QueryRow(`SELECT count(*) FROM catalog_entity_mutations WHERE owner_kind='administration' AND cause_operation='old-empty-season' AND status='active' AND acknowledged=0`).Scan(&active)
	if active != 1 {
		t.Fatal("changed Show approval consumed old Season", active)
	}
	fresh = showDeleteRequest(t, s, show.Public, "fresh-empty-show")
	out, e = s.Delete(context.Background(), allow, "owner", fresh)
	if e != nil || out.Removed != 1 || out.Failed != 0 {
		t.Fatal("fresh empty Show could not supersede Season", out, e)
	}
	db.QueryRow(`SELECT count(*) FROM admin_receipts WHERE operation_id='old-empty-season'`).Scan(&oldReceipts)
	if oldReceipts != 0 {
		t.Fatal("abandoned Season fabricated old receipt", oldReceipts)
	}
	assertShowReceiptReplay(t, s, db, fresh, show.ID)
}

func TestP252bAdministrationFreshShowDrivesQueuedEmptyParentBeforeReceipt(t *testing.T) {
	s, db, _, show, _, episodes := showDeleteFixture(t, 1)
	first := showDeleteRequest(t, s, episodes[0].Public, "old-direct-episode")
	if out, e := s.Delete(context.Background(), allow, "owner", first); e != nil || out.Removed != 1 {
		t.Fatal(out, e)
	}
	var root, state string
	if e := db.QueryRow(`SELECT m.mutation_id,o.state FROM catalog_entity_mutations m JOIN console_operations o ON o.resource=m.mutation_id AND o.kind='catalog-mutation' WHERE m.owner_kind='empty-parent' AND m.entity_id=?`, show.ID).Scan(&root, &state); e != nil || state != "queued" {
		t.Fatal("old automatic root not queued", state, e)
	}
	fresh := showDeleteRequest(t, s, show.Public, "fresh-show-after-auto")
	if out, e := s.Delete(context.Background(), allow, "owner", fresh); e != nil || out.Removed != 1 || out.Failed != 0 {
		t.Fatal("fresh Show returned before automatic handoff", out, e)
	}
	var terminal, ack, rootParent bool
	if e := db.QueryRow(`SELECT m.status='terminal',m.acknowledged,m.parent_mutation_id IS NULL,o.state FROM catalog_entity_mutations m JOIN console_operations o ON o.resource=m.mutation_id AND o.kind='catalog-mutation' WHERE m.mutation_id=?`, root).Scan(&terminal, &ack, &rootParent, &state); e != nil || !terminal || !ack || !rootParent || state != "succeeded" {
		t.Fatal("automatic console/ack CAS or parent binding lost", terminal, ack, rootParent, state, e)
	}
	assertShowReceiptReplay(t, s, db, fresh, show.ID)
}
