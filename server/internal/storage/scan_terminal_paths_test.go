package storage_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/ingestion"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestScanTerminalPathsRetireBetweenTurns(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "terminal.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, path := range []string{"console_cancel", "source_removal", "library_removal"} {
		t.Run(path, func(t *testing.T) {
			cat := catalog.New(db)
			roots := []string{t.TempDir(), t.TempDir()}
			for _, root := range roots {
				for i := range 48 {
					if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("book-%03d.pdf", i)), nil, 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			lib, err := cat.Create(path, "book", roots[0])
			if err != nil {
				t.Fatal(err)
			}
			second := path + "-second"
			if _, err = db.Exec(`INSERT INTO library_sources(id,library_id,name,configured_root,root) VALUES(?,?,'Second',?,?)`, second, lib.ID, roots[1], roots[1]); err != nil {
				t.Fatal(err)
			}
			scan := ingestion.New(db, cat, assets.Probe{})
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			client := storage.New(binary)
			client.SourceOperations = storage.NewOperationScope()
			owner := storage.NewOperationScope()
			scan.SetStorage(client)
			t.Cleanup(func() {
				owner.Close()
				client.SourceOperations.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := client.SourceOperations.Wait(ctx); err != nil {
					t.Error("physical cleanup", err)
				}
			})
			ctx := storage.WithInventoryOperations(context.Background(), owner)
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err = scan.QueueOperationTx(ctx, tx, path, lib.ID); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			type retained struct {
				job, source string
				request     storage.InventoryRequest
				page        storage.InventoryPage
				retired     <-chan struct{}
			}
			var streams []retained
			rows, err := db.Query(`SELECT r.job_id,r.source_id,s.root FROM inventory_runs r JOIN library_sources s ON s.id=r.source_id WHERE s.library_id=? ORDER BY s.id`, lib.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var stream retained
				if err = rows.Scan(&stream.job, &stream.source, &stream.request.Root); err != nil {
					t.Fatal(err)
				}
				stream.request.RelativePath, stream.request.Limit = ".", 8
				streams = append(streams, stream)
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			if len(streams) != 2 {
				t.Fatal("fixture needs two durable jobs", len(streams))
			}
			for i := range streams {
				stream := &streams[i]
				deadline := time.Now().Add(5 * time.Second)
				for {
					stream.page, err = storage.RetainedInventoryPageForTest(client, ctx, stream.job, stream.request)
					if !errors.Is(err, storage.ErrBusy) || time.Now().After(deadline) {
						break
					}
					time.Sleep(5 * time.Millisecond)
				}
				if err != nil || stream.page.Complete || !scan.Quiescent(stream.job) {
					t.Fatal("fixture must retain an idle producer between quantums", stream.page, err)
				}
				stream.retired = storage.InventoryRetirementForTest(client, stream.job)
				if stream.retired == nil {
					t.Fatal("fixture lost its retained producer")
				}
			}
			if owner.Active() != 2 || client.SourceOperations.Active() != 2 {
				t.Fatal("fixture did not fill both portable stream slots")
			}
			control := func(action string, commit bool) {
				t.Helper()
				tx, err := db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if err = scan.ControlOperationTx(ctx, tx, path, action); err != nil {
					t.Fatal(err)
				}
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				scan.InterruptOperation(ctx, path)
			}
			var revision int64
			if err = db.QueryRow(`SELECT revision FROM library_configuration WHERE library_id=?`, lib.ID).Scan(&revision); err != nil {
				t.Fatal(err)
			}
			authorize := func(*sql.Tx) error { return nil }
			switch path {
			case "console_cancel":
				control("cancel", false)
				control("pause", true)
				if owner.Active() != 2 {
					t.Fatal("rollback or pause retired a resumable producer")
				}
				control("resume", true)
				control("cancel", true)
			case "source_removal":
				err = cat.RemoveSource(ctx, lib.ID, streams[0].source, revision, authorize, nil)
				if err == nil {
					scan.InterruptSource(ctx, streams[0].source)
				}
			case "library_removal":
				err = cat.RemoveLibrary(ctx, lib.ID, path, revision, authorize, func(context.Context, *sql.Tx, string, string) error { return nil })
				if err == nil {
					scan.InterruptLibrary(ctx, lib.ID)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			for i, stream := range streams {
				if path == "source_removal" && i == 1 {
					if replay, err := storage.RetainedInventoryPageForTest(client, ctx, stream.job, stream.request); err != nil || !reflect.DeepEqual(replay, stream.page) {
						t.Fatal("removal interrupted another source's producer", err)
					}
					continue
				}
				select {
				case <-stream.retired:
				case <-time.After(5 * time.Second):
					t.Fatal("terminal path retained its idle producer", stream.job)
				}
			}
			remaining := 0
			if path == "source_removal" {
				remaining = 1
			}
			if owner.Active() != remaining || client.SourceOperations.Active() != remaining {
				t.Fatal("terminal path did not retire exactly its affected jobs")
			}
		})
	}
}
