package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"testing"
)

func TestReadingCollectionExplicit200AdmissionExecutionReceipt(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c, s, v, items := readingBrowseFixture(t, "book")
	var asset int64
	if err := c.DB.QueryRow(`SELECT asset_id FROM reading_publications WHERE item_id=?`, items[0].ID).Scan(&asset); err != nil {
		t.Fatal(err)
	}
	library := c.Handle("reading")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for n := 4; n < 200; n++ {
			id, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Ebook, Key: fmt.Sprintf("collection-book-%d", n), Title: fmt.Sprintf("Book %d", n)})
			if err != nil {
				return err
			}
			asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: fmt.Sprintf("/reading/collection-book-%d.pdf", n), Size: 100, ModifiedNS: 1, Container: "pdf"})
			if err != nil {
				return err
			}
			if err = compactcatalog.LinkAssetTx(ctx, tx, id, asset, compactcatalog.Link{}); err != nil {
				return err
			}
			if _, err = tx.Exec(`INSERT INTO reading_publications(item_id,asset_id,format) VALUES(?,?,'pdf')`, id, asset); err != nil {
				return err
			}
			items = append(items, catalogtest.Item{ID: id})
		}
		return nil
	})
	for n := range items {
		if items[n].Public == "" {
			items[n].Public = c.Public(items[n].ID)
		}
	}
	c.Drain()
	p := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "a", ProfileID: "p", Role: "member"}}
	v.Profile = identity.PersonalKey(p.Viewer)
	actor := jobActor(p)
	c.Exec(`INSERT INTO saved_resources(id,kind,owner_key,owner_authority,owner_account,owner_profile,name,created_at) VALUES('reading-collection','collection',?,?,?,?, 'Reading','now')`, actorKey(actor), actor.Authority, actor.AccountID, actor.ProfileID)
	denied := map[string]bool{}
	s.BulkCommands.ReadingCollectionAuthorize = func(_ context.Context, _ *sql.Tx, live identity.Principal, id string) error {
		if !identity.CanUsePersonalState(live) || denied[id] {
			return identity.ErrForbidden
		}
		return nil
	}
	access := func(context.Context, *sql.Tx, identity.Principal, string) (string, []any, error) {
		return "1", nil, nil
	}
	scheduler := operations.NewScheduler(operations.New(c.DB))
	if err := scheduler.Register(s.BulkAdapter(access)); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, item := range items {
		ids = append(ids, item.Public)
	}
	revision := int64(1)
	request := JobRequest{OperationID: "reading-200", Command: "collection-add", Selector: JobSelector{Items: &JobItems{IDs: ids}}, Args: JobArguments{CollectionID: "reading-collection", ExpectedRevision: &revision}}
	for _, command := range []string{"personal-state", "playlist-add"} {
		ordinary := request
		ordinary.Command = command
		err := dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
			query, args, err := s.WithContext(ctx).bulkSelection(p, v, ordinary)
			if err != nil {
				return err
			}
			var count int
			err = dbwork.Snapshot(ctx).QueryRow(`SELECT count(*) FROM (`+query+`)`, args...).Scan(&count)
			if count != 0 {
				t.Fatalf("%s admitted Reading: %d", command, count)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	job, err := s.CreateBulkJob(context.Background(), p, v, request, scheduler, access)
	if err != nil {
		t.Fatal(err)
	}
	job = finishCapture(t, s, job, access)
	if job.Total != 200 {
		t.Fatalf("captured %d, want 200", job.Total)
	}
	denied[items[0].Public] = true
	for job.State == "queued" || job.State == "running" {
		job, err = s.AdvanceBulkJob(context.Background(), job.JobID, access)
		if err != nil {
			t.Fatal(err)
		}
	}
	if job.Done != 199 || job.Failed != 1 {
		t.Fatalf("job %+v", job)
	}
	var count int
	if err = c.DB.QueryRow(`SELECT count(*) FROM saved_resource_entries WHERE resource_id='reading-collection'`).Scan(&count); err != nil || count != 199 {
		t.Fatal(count, err)
	}
	var receipt HomeDestinationReceipt
	readReceipt := func() {
		t.Helper()
		err = dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
			var err error
			receipt, err = s.WithContext(ctx).HomeDestinationReceiptTx(ctx, dbwork.Snapshot(ctx), "server", p, v, HomeDestinationReceiptRequest{Kind: "collection", DestinationID: "reading-collection", JobIDs: []string{job.JobID}}, access)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	readReceipt()
	if receipt.Added != 199 || receipt.Unknown != 1 || receipt.Complete || len(receipt.Failures) != 0 {
		t.Fatalf("revoked outcome disclosed: %+v", receipt)
	}
	delete(denied, items[0].Public)
	readReceipt()
	if receipt.Added != 199 || receipt.Failed != 1 || !receipt.Complete || len(receipt.FailureEntries) != 1 || receipt.FailureEntries[0].Kind != "ebook" {
		t.Fatalf("Reading failure receipt: %+v", receipt)
	}
	denied[items[0].Public] = true
	request.OperationID = "reading-denied"
	if _, err = s.CreateBulkJob(context.Background(), p, v, request, scheduler, access); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("missing grant: %v", err)
	}
}

func TestReadingCollectionDoesNotWidenContainerOrQuerySelectors(t *testing.T) {
	c, s, v, items := readingBrowseFixture(t, "comic")
	c.Drain()
	p := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "a", ProfileID: "p", Role: "member"}}
	s.BulkCommands.ReadingCollectionAuthorize = func(context.Context, *sql.Tx, identity.Principal, string) error { return nil }
	for _, selector := range []JobSelector{{Items: &JobItems{IDs: []string{items[0].Public}}}, {Container: &JobContainer{Kind: "library", ID: "reading"}}, {Query: &JobQuery{LibraryID: "reading", Pivot: "comics"}}} {
		err := dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
			query, args, err := s.WithContext(ctx).bulkSelection(p, v, JobRequest{Command: "collection-add", Selector: selector})
			if err != nil {
				return err
			}
			var count int
			if err := dbwork.Snapshot(ctx).QueryRow(`SELECT count(*) FROM (`+query+`)`, args...).Scan(&count); err != nil {
				return err
			}
			want := 0
			if selector.Items != nil {
				want = 1
			}
			if count != want {
				t.Fatalf("selector %+v admitted %d, want %d", selector, count, want)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
