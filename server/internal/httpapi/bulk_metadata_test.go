package httpapi

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestBulkOwnerMetadataCommandsRestartAndReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, command := range []string{"metadata-edit", "refresh"} {
		t.Run(command, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			path := filepath.Join(root, "db")
			db, e := persistence.Open(path)
			if e != nil {
				t.Fatal(e)
			}
			defer func() { db.Close() }()
			if _, e = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','profile',1)`); e != nil {
				t.Fatal(e)
			}
			catTest := catalogtest.New(t, db)
			catTest.Library("lib", "Movies", "movie", "/media")
			ids := []string{}
			for i := 0; i < 43; i++ {
				name := fmt.Sprintf("movie-%03d", i)
				item := catTest.Movie(catTest.Handle("lib"), filepath.Join("/media", name+".mkv"), name, 2020)
				ids = append(ids, item.Public)
			}
			catTest.Drain()
			ident, e := identity.New(db, root)
			if e != nil {
				t.Fatal(e)
			}
			token, e := ident.Issue("owner", "profile", "local", "owner", 1)
			if e != nil {
				t.Fatal(e)
			}
			p, e := ident.Authenticate(token.AccessToken)
			if e != nil {
				t.Fatal(e)
			}
			setup := func() Dependencies {
				d := Dependencies{Administration: administration.New(db), DB: db, Identity: ident, Catalog: catalog.New(db), Metadata: metadata.New(db, ""), Scheduler: operations.NewScheduler(operations.New(db))}
				d.Catalog.BulkCommands = d.bulkCommandHooks()
				if e := d.Scheduler.Register(d.Catalog.BulkAdapter(d.bulkAccess)); e != nil {
					t.Fatal(e)
				}
				return d
			}
			d := setup()
			// Creating an item queues its metadata work; the refresh must add exactly one generation per item.
			var generationsBefore int64
			db.QueryRow(`SELECT COALESCE(sum(generation),0) FROM screen_metadata_work WHERE target_kind='item'`).Scan(&generationsBefore)
			var receiptsBefore int
			db.QueryRow(`SELECT count(*) FROM admin_receipts`).Scan(&receiptsBefore)
			v := catalog.Viewer{Profile: identity.PersonalKey(p.Viewer), Libraries: []string{"lib"}}
			args := catalog.JobArguments{}
			rating := "PG"
			if command == "metadata-edit" {
				args.Fields = map[string]catalog.JobFieldEdit{"contentRating": {Value: &rating}}
			}
			in := catalog.JobRequest{OperationID: "metadata-job", Command: command, Selector: catalog.JobSelector{Items: &catalog.JobItems{IDs: ids}}, Args: args}
			j, e := d.Catalog.CreateBulkJob(ctx, p, v, in, d.Scheduler, d.bulkAccess)
			if e != nil {
				t.Fatal(e)
			}
			for !j.TotalKnown {
				jobID := j.JobID
				j, e = d.Catalog.AdvanceBulkJob(ctx, j.JobID, d.bulkAccess)
				if e != nil {
					t.Fatalf("capture job %s state=%s: %v", jobID, j.State, e)
				}
			}
			jobID := j.JobID
			j, e = d.Catalog.AdvanceBulkJob(ctx, j.JobID, d.bulkAccess)
			// An owner metadata command applies two members per transaction (its
			// fan-out is about sixty rows a member).
			firstBatch := int64(2)
			if e != nil || j.Done != firstBatch {
				failures, _ := d.Catalog.BulkFailures(v.Profile, jobID, "", v)
				t.Fatalf("first bulk batch for %s: job=%+v error=%v failures=%+v", jobID, j, e, failures)
			}
			db.Close()
			db, e = persistence.Open(path)
			if e != nil {
				t.Fatal(e)
			}
			ident, e = identity.New(db, root)
			if e != nil {
				t.Fatal(e)
			}
			d = setup()
			for j.State == "queued" || j.State == "running" {
				j, e = d.Catalog.AdvanceBulkJob(ctx, j.JobID, d.bulkAccess)
				if e != nil {
					t.Fatal(e)
				}
			}
			if j.State != "complete" || j.Done != 43 {
				t.Fatal(j)
			}
			before := int64(0)
			if command == "refresh" {
				db.QueryRow(`SELECT sum(generation) FROM screen_metadata_work WHERE target_kind='item'`).Scan(&before)
				if before-generationsBefore != 43 {
					t.Fatal("refresh repeated or omitted", generationsBefore, before)
				}
			} else if command == "metadata-edit" {
				func() {
					for _, id := range ids {
						state, e := d.Metadata.RepairState(ctx, metadata.RepairTarget{Kind: "item", ID: id}, func(*sql.Tx) error { return nil })
						if e != nil {
							t.Fatal(e)
						}
						if state.Snapshot.Fields["contentRating"].Value != "PG" || !state.Snapshot.Fields["contentRating"].Locked {
							t.Fatal(state)
						}
					}
				}()
			}
			replay, e := d.Catalog.CreateBulkJob(ctx, p, v, in, d.Scheduler, d.bulkAccess)
			if e != nil || replay.JobID != j.JobID || replay.Done != 43 {
				t.Fatal(replay, e)
			}
			if command == "refresh" {
				var after int64
				db.QueryRow(`SELECT sum(generation) FROM screen_metadata_work WHERE target_kind='item'`).Scan(&after)
				if after != before {
					t.Fatal("replay refreshed twice")
				}
			}
			in.OperationID = "revoked-owner"
			pending, e := d.Catalog.CreateBulkJob(ctx, p, v, in, d.Scheduler, d.bulkAccess)
			if e != nil {
				t.Fatal(e)
			}
			for !pending.TotalKnown {
				pending, e = d.Catalog.AdvanceBulkJob(ctx, pending.JobID, d.bulkAccess)
				if e != nil {
					t.Fatal(e)
				}
			}
			if e = ident.LogoutToken(ctx, token.AccessToken); e != nil {
				t.Fatal(e)
			}
			pending, e = d.Catalog.AdvanceBulkJob(ctx, pending.JobID, d.bulkAccess)
			if e != nil || pending.State != "failed" || pending.ErrorCode != "authority_revoked" || pending.Done != 0 {
				t.Fatal("revoked owner performed work", pending, e)
			}

		})
	}
}
