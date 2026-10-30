package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func clearRetainedNameFixture(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		inventoryNameCache.Lock()
		defer inventoryNameCache.Unlock()
		for id, s := range inventoryNameCache.entries {
			s.mu.Lock()
			if s.timer != nil {
				s.timer.Stop()
			}
			if s.file != nil {
				s.file.Close()
			}
			s.closed = true
			s.changes.Close()
			s.mu.Unlock()
			delete(inventoryNameCache.entries, id)
		}
	})
}
func TestRetainedNamesUseOneStreamAcrossFreshCallerHandles(t *testing.T) {
	clearRetainedNameFixture(t)
	root := t.TempDir()
	const total = 4097
	for i := 0; i < total; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("entry-%05d", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	cursor := ""
	seen := map[string]bool{}
	var retained *os.File
	for page := 0; page < 40; page++ {
		f, err := os.Open(root)
		if err != nil {
			t.Fatal(err)
		}
		names, next, done, err := retainedInventoryNames(f, cursor, 128)
		f.Close() // production opens/closes a new anchored handle every page
		if err != nil {
			t.Fatal(err)
		}
		if len(names) > 128 {
			t.Fatal("unbounded page")
		}
		for _, name := range names {
			if seen[name] {
				t.Fatal("duplicate name", name)
			}
			seen[name] = true
		}
		if done {
			if len(seen) != total {
				t.Fatalf("got %d of %d", len(seen), total)
			}
			return
		}
		id, _, _ := strings.Cut(next, ":")
		inventoryNameCache.Lock()
		s := inventoryNameCache.entries[id]
		inventoryNameCache.Unlock()
		if s == nil {
			t.Fatal("lost stream")
		}
		if retained == nil {
			retained = s.file
		} else if retained != s.file {
			t.Fatal("directory reopened instead of continued")
		}
		if cursor != "" {
			f, err = os.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			replay, replayNext, replayDone, replayErr := retainedInventoryNames(f, cursor, 128)
			f.Close()
			if replayErr != nil || replayDone || replayNext != next || !reflect.DeepEqual(names, replay) {
				t.Fatal("last-page retry advanced enumeration", replayErr)
			}
		}
		cursor = next
	}
	t.Fatal("linear listing failed to reach EOF")
}
func TestRetainedNamesRejectChangedDirectoryAndLostCursor(t *testing.T) {
	clearRetainedNameFixture(t)
	root := t.TempDir()
	for _, name := range []string{"one", "two"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	_, cursor, done, err := retainedInventoryNames(f, "", 1)
	f.Close()
	if err != nil || done || cursor == "" {
		t.Fatal("fixture", err)
	}
	other, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = retainedInventoryNames(other, cursor, 1); !errors.Is(err, ErrInventoryChanged) {
		t.Fatal("cursor crossed physical directories", err)
	}
	other.Close()
	if err = os.WriteFile(filepath.Join(root, "new-entry"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	f, err = os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, _, _, err = retainedInventoryNames(f, cursor, 1); !errors.Is(err, ErrInventoryChanged) {
		t.Fatal("changed directory continued", err)
	}
	if _, _, _, err = retainedInventoryNames(f, "lost-process:4", 1); !errors.Is(err, ErrInventoryChanged) {
		t.Fatal("lost continuation fabricated completion", err)
	}
}

func streamTestClient(t *testing.T, body string) (*Client, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("synthetic POSIX helper; real portable helper test is separate")
	}
	root := t.TempDir()
	script := filepath.Join(root, "helper")
	scriptBody := "#!/bin/sh\ncat >/dev/null\nprintf 'start\\n' >> '" + filepath.Join(root, "starts") + "'\n" + body
	if err := os.WriteFile(script, []byte(scriptBody), 0700); err != nil {
		t.Fatal(err)
	}
	c := New(script)
	c.Timeout = 10 * time.Millisecond
	c.SourceOperations = NewOperationScope()
	t.Cleanup(func() {
		c.SourceOperations.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := c.SourceOperations.Wait(ctx); err != nil {
			t.Error("physical helper not retired", err)
		}
		inventoryStreams.Lock()
		for id, s := range inventoryStreams.byID {
			if s.client == c {
				s.cancel()
				delete(inventoryStreams.byID, id)
				if inventoryStreams.byKey[s.key] == s {
					delete(inventoryStreams.byKey, s.key)
				}
			}
		}
		inventoryStreams.Unlock()
	})
	return c, root
}
func streamJSON(complete bool) string {
	raw, _ := json.Marshal(InventoryPage{RootIdentity: "fixture-root", DirectoryIdentity: "fixture-directory", DirectoryRevision: "fixture-revision", Entries: []Snapshot{}, NextCursor: "private-child-cursor", Complete: complete})
	return string(raw)
}
func awaitStreamPage(t *testing.T, c *Client, key string, r InventoryRequest) InventoryPage {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		p, err := c.retainedInventoryPage(context.Background(), key, r)
		if err == nil {
			return p
		}
		if !errors.Is(err, ErrBusy) {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("retained stream never progressed")
	return InventoryPage{}
}
func TestRetainedProducerSurvivesRequestDeadlineAndReplaysPage(t *testing.T) {
	c, root := streamTestClient(t, "sleep 0.15\nprintf '%s\\n' '"+streamJSON(false)+"'\nsleep 0.15\nprintf '%s\\n' '"+streamJSON(true)+"'\n")
	r := InventoryRequest{Root: root, RelativePath: ".", Limit: 128}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	_, err := c.retainedInventoryPage(ctx, "scan", r)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, ErrBusy) {
		t.Fatal("slow first page not pending", err)
	}
	first := awaitStreamPage(t, c, "scan", r)
	if first.Complete || first.NextCursor == "" {
		t.Fatal("partial stream published completion")
	}
	retry := awaitStreamPage(t, c, "scan", r)
	if !reflect.DeepEqual(first, retry) {
		t.Fatal("uncommitted first-page retry changed")
	}
	r.Cursor, r.RootIdentity, r.DirectoryRevision = first.NextCursor, first.RootIdentity, first.DirectoryRevision
	last := awaitStreamPage(t, c, "scan", r)
	if !last.Complete || last.NextCursor != "" {
		t.Fatal("full stream did not complete")
	}
	again := awaitStreamPage(t, c, "scan", r)
	if !reflect.DeepEqual(last, again) {
		t.Fatal("terminal page retry changed")
	}
	starts, err := os.ReadFile(filepath.Join(root, "starts"))
	if err != nil || strings.Count(string(starts), "start") != 1 {
		t.Fatal("short requests restarted provider", string(starts), err)
	}
	if c.Supervisor.Active() != 0 {
		t.Fatal("completion preceded physical exit")
	}
	// A stale/foreign continuation cannot manufacture a resumed page.
	r.Cursor = "stream1:lost-owner:2"
	if _, err = c.retainedInventoryPage(context.Background(), "scan", r); !errors.Is(err, ErrInventoryChanged) {
		t.Fatal("lost cursor accepted", err)
	}
}
func TestRetainedProducerRequiresSuccessfulEOFAndExit(t *testing.T) {
	for _, tail := range []string{"exit 7\n", "printf 'garbage\\n'\n", "printf '%s\\n' '" + streamJSON(false) + "'\n"} {
		t.Run(fmt.Sprintf("tail-%d", len(tail)), func(t *testing.T) {
			c, root := streamTestClient(t, "printf '%s\\n' '"+streamJSON(true)+"'\n"+tail)
			r := InventoryRequest{Root: root, RelativePath: ".", Limit: 128}
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				p, err := c.retainedInventoryPage(context.Background(), "bad-stream", r)
				if err != nil && !errors.Is(err, ErrBusy) {
					return
				}
				if p.Complete {
					t.Fatal("unverified terminal output authorized completion")
				}
			}
			t.Fatal("failed producer not reported")
		})
	}
}
func TestRetainedPortableHelperVisitsRealDirectory(t *testing.T) {
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
	for i := 0; i < 257; i++ {
		if err = os.WriteFile(filepath.Join(root, fmt.Sprintf("file-%04d", i)), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r := InventoryRequest{Root: root, RelativePath: ".", Limit: 128}
	seen := map[string]bool{}
	for n := 0; n < 5; n++ {
		p := awaitStreamPage(t, c, "portable-real", r)
		for _, entry := range p.Entries {
			if seen[entry.Name] {
				t.Fatal("duplicate", entry.Name)
			}
			seen[entry.Name] = true
		}
		if p.Complete {
			if len(seen) != 257 {
				t.Fatal("missing entries", len(seen))
			}
			return
		}
		r.Cursor, r.RootIdentity, r.DirectoryRevision = p.NextCursor, p.RootIdentity, p.DirectoryRevision
	}
	t.Fatal("real directory did not reach EOF")
}
