package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// AppleDouble names cost a raw-name turn even though they are not catalogue
// entries. Changing the next page's budget must neither lose nor double-charge them.
func TestRetainedProducerHonorsEachContinuationBudget(t *testing.T) {
	for _, next := range []int{32, 8} {
		t.Run(fmt.Sprintf("16-to-%d", next), func(t *testing.T) {
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			c := New(binary)
			c.SourceOperations = NewOperationScope()
			t.Cleanup(func() {
				c.SourceOperations.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := c.SourceOperations.Wait(ctx); err != nil {
					t.Error("physical helper did not retire", err)
				}
			})
			root := t.TempDir()
			for i := range 96 {
				name := fmt.Sprintf("file-%03d.pdf", i)
				if i%3 == 0 {
					name = "._" + name
				}
				if err := os.WriteFile(filepath.Join(root, name), []byte("unchanged"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			r := InventoryRequest{Root: root, RelativePath: ".", Limit: 16}
			seen := map[string]bool{}
			rawTotal, entries := 0, 0
			var physical *inventoryStream
			for turn := 0; turn < 32; turn++ {
				page := awaitStreamPage(t, c, "adaptive-real", r)
				raw := len(page.Entries) + len(page.Issues)
				if raw > r.Limit || turn < 2 && !page.Complete && raw != r.Limit {
					t.Fatalf("raw budget %d returned %d names: %+v", r.Limit, raw, page)
				}
				for _, entry := range page.Entries {
					if seen[entry.Name] {
						t.Fatal("duplicate name", entry.Name)
					}
					seen[entry.Name] = true
					entries++
				}
				for _, issue := range page.Issues {
					if issue.Reason != "ignored_name" || seen[issue.Path] {
						t.Fatal("lost raw-name accounting", issue)
					}
					seen[issue.Path] = true
				}
				rawTotal += raw
				replay := awaitStreamPage(t, c, "adaptive-real", r)
				if !reflect.DeepEqual(page, replay) {
					t.Fatal("page replay changed after budget admission")
				}
				if page.Complete {
					if rawTotal != 96 || entries != 64 || c.SourceOperations.Active() != 0 {
						t.Fatal("EOF lost names or preceded physical retirement", rawTotal, entries, c.SourceOperations.Active())
					}
					return
				}
				parts := strings.Split(page.NextCursor, ":")[1]
				inventoryStreams.Lock()
				current := inventoryStreams.byID[parts]
				inventoryStreams.Unlock()
				if physical == nil {
					physical = current
				} else if current != physical {
					t.Fatal("budget change reopened the physical producer")
				}
				r.Cursor, r.RootIdentity, r.DirectoryRevision = page.NextCursor, page.RootIdentity, page.DirectoryRevision
				r.Limit = next
			}
			t.Fatal("bounded continuation never reached real EOF")
		})
	}
}

func TestRetainedProducerRejectsOverBudgetRawNames(t *testing.T) {
	// Filtered names are absent from Entries; their raw count still consumes
	// the admitted page budget and cannot be hidden by the helper.
	raw, err := json.Marshal(map[string]any{
		"rootIdentity": "fixture-root", "directoryIdentity": "fixture-directory",
		"directoryRevision": "fixture-revision", "rawUsed": 16,
		"entries": []Snapshot{}, "complete": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	c, root := streamTestClient(t, "printf '%s\\n' '"+string(raw)+"'\n")
	r := InventoryRequest{Root: root, RelativePath: ".", Limit: 8}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		page, err := c.retainedInventoryPage(context.Background(), "raw-overflow", r)
		if errors.Is(err, ErrBusy) {
			continue
		}
		if !errors.Is(err, ErrInventoryChanged) {
			t.Fatal("helper exceeded the eight-name budget", page, err)
		}
		return
	}
	t.Fatal("invalid helper did not retire")
}

func TestRetainedProducerUsesUnusedCreditToProveSmallDirectoryEOF(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := New(binary)
	c.SourceOperations = NewOperationScope()
	t.Cleanup(func() {
		c.SourceOperations.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := c.SourceOperations.Wait(ctx); err != nil {
			t.Error(err)
		}
	})
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "one.pdf"), []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	page := awaitStreamPage(t, c, "small-eof", InventoryRequest{Root: root, RelativePath: ".", Limit: 64})
	if !page.Complete || len(page.Entries) != 1 || page.NextCursor != "" || c.SourceOperations.Active() != 0 {
		t.Fatal("unused credit left a small directory's physical producer alive", page, c.SourceOperations.Active())
	}
}
