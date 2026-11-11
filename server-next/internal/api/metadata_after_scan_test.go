package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// searchTimesOut is a provider whose searches never answer, as when a lookup times out, and
// whose details answer as usual. It counts the details it was asked for.
type searchTimesOut struct {
	metadata.Provider
	details atomic.Int64
}

func (p *searchTimesOut) Search(context.Context, metadata.Query) ([]metadata.Candidate, error) {
	return nil, fmt.Errorf("tmdb did not answer: %w", context.DeadlineExceeded)
}

func (p *searchTimesOut) Details(ctx context.Context, ref metadata.Ref, locale metadata.Locale) (metadata.Record, error) {
	p.details.Add(1)
	return p.Provider.Details(ctx, ref, locale)
}

// The run a scan starts refreshes the library's due titles even when its lookup stops: a new
// film the provider cannot be asked about leaves the lookup ended as provider down, and the
// matched film that is due is refreshed all the same.
func TestTheRefreshRunsWhateverTheLookupEndedWith(t *testing.T) {
	sh := newShelf(t, "the.matrix.1999.1080p.mkv")
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(sh.root, "zeta.two.2003.mkv"), make([]byte, 300), 0o644); err != nil {
		t.Fatal(err)
	}
	libraries, err := sh.s.db.AllLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&scan.Scanner{DB: sh.s.db, Prober: stillProber{}}).Scan(ctx, libraries[0]); err != nil {
		t.Fatal(err)
	}
	p := &searchTimesOut{Provider: sh.s.metadata.Providers["tmdb"]}
	sh.s.metadata.Providers["tmdb"] = p
	// Three days on, the film matched just now (new, so refreshed every two days) is due.
	err = sh.s.lookUpAndRefresh(ctx, libraries[0].ID, time.Now().Add(3*24*time.Hour))
	if err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Errorf("the run ended %v; want the lookup's provider down", err)
	}
	if p.details.Load() == 0 {
		t.Fatal("the lookup stopped, and the matched film that is due was not refreshed")
	}
}
