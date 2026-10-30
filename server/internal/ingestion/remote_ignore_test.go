package ingestion

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"testing"
)

func TestManagedIgnoreRulesUseStagedNamesAndPreserveAdapterPages(t *testing.T) {
	f := newLoopFixture(t, "movie")
	source, err := f.s.catalog.LibrarySource(context.Background(), f.source)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct{ id, relative, name, contents string }{{"root-list", ".", ".plexignore", "Ignored*.mkv\n"}, {"child-list", "Nested", ".ignore", "Skip*.mkv\n[broken\n"}} {
		directory := filepath.Join(f.root, entry.relative)
		f.exec(`INSERT INTO remote_listing_sessions(id,job_id,source_id,generation,directory,ready,created_at) VALUES(?,'job','remote','generation',?,1,1)`, entry.id, directory)
		snapshot := storage.Snapshot{Path: filepath.Join(directory, entry.name), Name: entry.name, Size: int64(len(entry.contents)), Revision: "revision"}
		raw, _ := json.Marshal(snapshot)
		f.exec(`INSERT INTO remote_listing_entries(listing_id,name,snapshot) VALUES(?,?,?)`, entry.id, snapshot.Path, string(raw))
	}
	reads := 0
	read := func(ctx context.Context, path string, _ int64) ([]byte, error) {
		reads++
		captured, ok := ctx.Value(remoteIgnoreReadKey{}).(remoteIgnoreRead)
		wantListing := "child-list"
		if filepath.Base(path) == ".plexignore" {
			wantListing = "root-list"
		}
		if !ok || captured.job != "job" || captured.source != source.ID || captured.listing != wantListing || captured.member.Path != path || captured.member.Revision != "revision" {
			t.Fatal("ignore callback lost the actual selected listing/member", captured)
		}
		if filepath.Base(path) == ".plexignore" {
			return []byte("Ignored*.mkv\n"), nil
		}
		return []byte("Skip*.mkv\n[broken\n"), nil
	}
	entries := []storage.Snapshot{}
	for _, name := range []string{"Ignored (2000).mkv", "Skipped (2001).mkv", "Kept (2002).mkv"} {
		entries = append(entries, storage.Snapshot{Name: name, Path: filepath.Join(f.root, "Nested", name)})
	}
	remote := storage.RemotePage{ID: "child-list", SourceID: "remote", Generation: "generation", Directory: filepath.Join(f.root, "Nested"), Entries: entries, Complete: true}
	for n := 0; n < 2; n++ {
		page := storage.InventoryPage{Remote: &remote, Entries: entries, Complete: true}
		if err = f.s.applyRemoteIgnores(context.Background(), "job", source, "Nested", &page, read); err != nil {
			t.Fatal(err)
		}
		if len(page.Entries) != 1 || page.Entries[0].Name != "Kept (2002).mkv" {
			t.Fatalf("filtered: %+v", page.Entries)
		}
		if len(remote.Entries) != 3 {
			t.Fatal("adapter commit page was modified")
		}
		found := false
		for _, issue := range page.Issues {
			if issue.Reason == "invalid_ignore_pattern" && issue.Line == 2 {
				found = true
			}
		}
		if !found {
			t.Fatal("managed invalid line not reported")
		}
	}
	if reads != 2 {
		t.Fatalf("ignore contents reread for each media page: %d", reads)
	}
	f.s.forgetListing("job")
	if len(f.s.ignoreScopes) != 0 {
		t.Fatal("remote ignore cache survived scan completion")
	}
}

func TestRemoteIgnoreReadRejectsMissingOrMismatchedListingBeforeIO(t *testing.T) {
	// No service/DB/storage is needed for a refusal before admission. A path,
	// pool name or unrelated observed member never supplies listing authority.
	s := &Service{}
	for name, captured := range map[string]*remoteIgnoreRead{
		"missing":      nil,
		"other job":    {job: "other", source: "source", listing: "listing", member: storage.Snapshot{Path: "/root/.ignore"}},
		"other source": {job: "job", source: "other", listing: "listing", member: storage.Snapshot{Path: "/root/.ignore"}},
		"other member": {job: "job", source: "source", listing: "listing", member: storage.Snapshot{Path: "/root/movie.mkv"}},
		"no listing":   {job: "job", source: "source", member: storage.Snapshot{Path: "/root/.ignore"}},
		"directory":    {job: "job", source: "source", listing: "listing", member: storage.Snapshot{Path: "/root/.ignore", Directory: true}},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if captured != nil {
				ctx = context.WithValue(ctx, remoteIgnoreReadKey{}, *captured)
			}
			if _, err := s.readRemoteIgnore(ctx, "job", catalog.LibrarySource{ID: "source"}, "/root/.ignore", 4096); !errors.Is(err, storage.ErrRemoteConfig) {
				t.Fatal("unbound ignore read reached I/O", err)
			}
		})
	}
}

func TestRemoteIgnoreChangedObservationStopsPageWithoutReplacement(t *testing.T) {
	f := newLoopFixture(t, "movie")
	source, err := f.s.catalog.LibrarySource(context.Background(), f.source)
	if err != nil {
		t.Fatal(err)
	}
	member := storage.Snapshot{Path: filepath.Join(f.root, ".ignore"), Name: ".ignore", Size: 8, Revision: "listed"}
	raw, _ := json.Marshal(member)
	f.exec(`INSERT INTO remote_listing_sessions(id,job_id,source_id,generation,directory,ready,created_at) VALUES('ignore-list','job','remote','generation',?,1,1)`, f.root)
	f.exec(`INSERT INTO remote_listing_entries(listing_id,name,snapshot) VALUES('ignore-list',?,?)`, member.Path, string(raw))
	for _, test := range []struct {
		name string
		err  error
		stop bool
	}{
		{"changed member", storage.ErrRemoteChanged, true},
		{"changed listing", storage.ErrInventoryChanged, true},
		{"unreadable provider", storage.ErrRemoteOffline, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			f.s.forgetListing("job")
			entry := storage.Snapshot{Path: filepath.Join(f.root, "Movie.mkv"), Name: "Movie.mkv"}
			page := storage.InventoryPage{Remote: &storage.RemotePage{ID: "ignore-list", SourceID: "remote", Generation: "generation", Directory: f.root}, Entries: []storage.Snapshot{entry}, Complete: true}
			reads := 0
			err := f.s.applyRemoteIgnores(context.Background(), "job", source, ".", &page, func(context.Context, string, int64) ([]byte, error) {
				reads++
				return nil, errors.Join(errors.New("read failed"), test.err)
			})
			if errors.Is(err, test.err) != test.stop || reads != 1 || len(page.Entries) != 1 || page.Entries[0] != entry {
				t.Fatal("changed config was retried, replaced or silently ignored", err, reads, page)
			}
			if test.stop {
				if len(page.Issues) != 0 || len(f.s.ignoreScopes["job"]) != 0 {
					t.Fatal("stale ignore rules were cached or downgraded to a warning", page.Issues)
				}
			} else if err != nil || len(page.Issues) != 1 || page.Issues[0].Reason != "ignore_unreadable" {
				t.Fatal("genuine unreadable-file warning policy changed", err, page.Issues)
			}
		})
	}
}
