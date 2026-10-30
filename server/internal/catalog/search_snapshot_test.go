package catalog

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// changeDuring is a request context that commits a library change every time
// the search asks whether its request is still live, which it does between its
// groups.
type changeDuring struct {
	context.Context
	change func()
}

func (c changeDuring) Err() error {
	c.change()
	return c.Context.Err()
}

// A search composed on one read snapshot answers from that snapshot: a library
// that publishes changes while its groups run does not reject the first page.
func TestSearchOnOneSnapshotSurvivesConcurrentLibraryChanges(t *testing.T) {
	c, err := searchTestCatalog(t, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	movies := c.Library("m", "Movies", "movie", "/m")
	c.Movie(movies, "/m/harbor.mkv", "Harbor", 2020)
	c.Drain()
	s := New(c.DB)
	// Statements already issued may consult the context after Search returns.
	var changes, finished atomic.Int64
	var out SearchEnvelope
	var seen int64
	err = dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
		live := changeDuring{Context: ctx, change: func() {
			if finished.Load() != 0 {
				return
			}
			if _, changeErr := c.DB.Exec(`UPDATE library_revisions SET revision=revision+1 WHERE library_id='m'`); changeErr == nil {
				changes.Add(1)
			}
		}}
		var searchErr error
		out, searchErr = s.WithContext(live).Search(live, SearchRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}, ServerID: "server", Q: "Harbor", Limit: 40})
		finished.Store(1)
		if searchErr != nil {
			return searchErr
		}
		return dbwork.Snapshot(ctx).QueryRowContext(ctx, `SELECT revision FROM library_revisions WHERE library_id='m'`).Scan(&seen)
	})
	if changes.Load() < 2 {
		t.Fatalf("the library changed %d times during the search", changes.Load())
	}
	if err != nil {
		t.Fatalf("a first page read from one snapshot was rejected: %v", err)
	}
	if len(out.Groups) == 0 || len(out.Groups[0].Items) != 1 {
		t.Fatalf("search results: %+v", out.Groups)
	}
	if out.Revision.Catalog != seen {
		t.Fatalf("revision %d is not the snapshot's %d", out.Revision.Catalog, seen)
	}
}
