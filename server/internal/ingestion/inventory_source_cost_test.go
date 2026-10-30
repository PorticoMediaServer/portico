//go:build linux

package ingestion

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Measure complete producer turns, including managed subtitle rows, issues,
// folder progress and Reading semantic flushes, rather than just object inserts.
func TestInventorySourcePageCost(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, tc := range []struct {
		name, kind string
		files      []string
	}{
		{"media", "movie", []string{"A.mkv", "B.mkv", "C.mkv", "D.mkv"}},
		{"reading", "book", []string{"A.epub", "B.epub", "C.epub", "D.epub"}},
		{"subtitles", "movie", []string{"A.en.srt", "A.fr.srt", "A.de.srt", "A.es.srt"}},
		{"ignored", "movie", []string{".a", ".b", ".c", ".d"}},
		{"folders", "movie", nil},
		{"issues", "movie", nil},
		{"reading-folders", "book", []string{"E0/A.epub", "E1/B.epub"}},
		{"disc", "movie", []string{"Film/BDMV/STREAM/00001.m2ts"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLoopFixture(t, tc.kind, tc.files...)
			if tc.name == "folders" || tc.name == "issues" {
				for i := 0; i < 4; i++ {
					if err := os.MkdirAll(filepath.Join(f.root, fmt.Sprintf("E%d", i)), 0700); err != nil {
						t.Fatal(err)
					}
				}
			}
			if tc.name == "issues" {
				f.write(".ignore", strings.Repeat("[\n", 300))
			}
			job := f.queue()
			source := startInventorySource(t, f, job)
			if tc.name == "reading-folders" {
				for f.count(`SELECT count(*) FROM inventory_directories WHERE job_id=? AND relative_path='.' AND state='pending'`, job.ID) > 0 {
					if err := f.s.readDirectoryPage(context.Background(), job.ID, source, ".", inventorySourceNames); err != nil {
						t.Fatal(err)
					}
				}
			}

			for turn := 0; turn < 40; turn++ {
				if f.count(`SELECT count(*) FROM inventory_directories WHERE job_id=? AND state='pending'`, job.ID) == 0 {
					break
				}
				dbwork.ResetPlans()
				if err := f.s.inventoryPage(context.Background(), job.ID, source); err != nil {
					t.Fatal(err)
				}
				p := dbwork.Plans()
				if p.Measured != 1 {
					t.Fatalf("expected one atomic publication: %+v", p)
				}
				if p.Largest.Rows > 8192 || p.Largest.Pages > 1024 || p.Largest.VMOps > 2_000_000 {
					t.Fatalf("named envelope exceeded: %+v", p.Largest)
				}
				if tc.name != "issues" && p.Largest.Rows > 160 {
					t.Fatalf("ordinary fixture exceeds row target: %+v", p.Largest)
				}
				t.Logf("turn=%d measured=%d max=%+v", turn, p.Measured, p.Largest)
			}
			if n := f.count(`SELECT count(*) FROM inventory_directories WHERE job_id=? AND state='pending'`, job.ID); n != 0 {
				t.Fatalf("cost traversal left %d pending folders", n)
			}
		})
	}
}

func startInventorySource(t *testing.T, f *loopFixture, job Job) catalog.LibrarySource {
	t.Helper()
	f.exec(`UPDATE jobs SET status='running' WHERE id=?`, job.ID)
	source := f.librarySource()
	if err := f.s.readySource(context.Background(), job.ID, &source); err != nil {
		t.Fatal(err)
	}
	return source
}

func TestInventorySourceLimitDoesNotGrowWithSpeed(t *testing.T) {
	for _, speed := range []string{dbwork.SpeedGentle, dbwork.SpeedNormal, dbwork.SpeedFast} {
		if got := (*Service)(nil).inventoryBatch(dbwork.WithBackgroundSpeed(context.Background(), speed)); got != inventorySourceNames {
			t.Fatalf("%s names=%d", speed, got)
		}
	}
}

// Filtered names spend the same allowance as returned entries. Fixed ignore
// diagnostics spend no raw names, so the separate prelisting folder cap matters.
func TestInventorySourceRawBudgetIncludesIssuesAndEmptyPages(t *testing.T) {
	for _, helper := range []bool{false, true} {
		t.Run(fmt.Sprintf("helper=%v", helper), func(t *testing.T) {
			f := newLoopFixture(t, "movie", "A/.one", "A/.two", "A/.three", "B/.one", "B/.two", "B/.three")
			f.write(".ignore", strings.Repeat("[\n", 300))
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			client := storage.New(binary)
			requests := []storage.InventoryRequest{
				{Root: f.root, RelativePath: "A", Limit: inventorySourceNames},
				{Root: f.root, RelativePath: "B", Limit: inventorySourceNames},
			}
			totalRaw, complete := 0, 0
			for turn := 0; turn < 12 && len(requests) > 0; turn++ {
				var envelopes []storage.BudgetEnvelope
				if helper {
					envelopes, err = client.InventoryPagesBudget(context.Background(), "source-budget", requests, inventorySourceNames)
				} else {
					envelopes, err = storage.LocalInventoryPagesBudget(requests, inventorySourceNames)
				}
				if err != nil {
					t.Fatal(err)
				}
				if err = storage.CheckBudgetResponse(requests, inventorySourceNames, envelopes); err != nil {
					t.Fatal(err)
				}
				raw := 0
				var next []storage.InventoryRequest
				for i, e := range envelopes {
					r := requests[i]
					if e.Outcome == storage.BudgetSkipped {
						next = append(next, r)
						continue
					}
					if e.Outcome != storage.BudgetPage {
						t.Fatalf("unexpected envelope: %+v", e)
					}
					raw += e.RawUsed
					if len(e.Page.Entries) != 0 || len(e.Page.Issues) < 257 {
						t.Fatalf("expected issue-only page, got %+v", e.Page)
					}
					if e.Page.Complete {
						complete++
					} else {
						r.Cursor = e.Page.NextCursor
						r.DirectoryRevision = e.Page.DirectoryRevision
						r.RootIdentity = e.Page.RootIdentity
						next = append(next, r)
					}
				}
				if raw > inventorySourceNames {
					t.Fatalf("raw names=%d", raw)
				}
				totalRaw += raw
				requests = next
			}
			if totalRaw != 6 || complete != 2 {
				t.Fatalf("lost names/cursors: raw=%d complete=%d", totalRaw, complete)
			}
			// Two genuinely empty pages, each carrying a full fixed diagnostic set.
			for _, d := range []string{"C", "D"} {
				if err = os.Mkdir(filepath.Join(f.root, d), 0700); err != nil {
					t.Fatal(err)
				}
			}
			requests = []storage.InventoryRequest{{Root: f.root, RelativePath: "C", Limit: inventorySourceNames}, {Root: f.root, RelativePath: "D", Limit: inventorySourceNames}}
			var envelopes []storage.BudgetEnvelope
			if helper {
				envelopes, err = client.InventoryPagesBudget(context.Background(), "empty-source-budget", requests, inventorySourceNames)
			} else {
				envelopes, err = storage.LocalInventoryPagesBudget(requests, inventorySourceNames)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = storage.CheckBudgetResponse(requests, inventorySourceNames, envelopes); err != nil {
				t.Fatal(err)
			}
			for _, e := range envelopes {
				if e.RawUsed != 0 || e.Page == nil || !e.Page.Complete || len(e.Page.Issues) != 257 {
					t.Fatalf("empty issue page: %+v", e)
				}
			}
		})
	}
}
