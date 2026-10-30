package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

type lookupSubtitleProvider struct{ downloads int }

func (*lookupSubtitleProvider) ID() string { return "fixture" }
func (*lookupSubtitleProvider) Search(context.Context, string, string, int) ([]subtitles.ProviderCandidate, error) {
	return []subtitles.ProviderCandidate{{FileID: 42, Language: "en", Title: "Fixture"}}, nil
}
func (p *lookupSubtitleProvider) Download(context.Context, int64) ([]byte, error) {
	p.downloads++
	return []byte("1\n00:00:00,000 --> 00:00:02,000\nHello\n"), nil
}

func TestSubtitleLookupEpochNeverRevivesCapabilityDuringFanout(t *testing.T) {
	c := catalogtest.Open(t)
	dir := t.TempDir()
	library := c.Library("library", "Films", "movie", dir)
	item := c.Movie(library, filepath.Join(dir, "movie.mp4"), "Film", 2024)
	c.Drain()
	principal := identity.Principal{Viewer: identity.Viewer{AccountID: "account", ProfileID: "profile", ServerID: "server", Authority: "local", Role: "owner"}, Hash: "login", Epoch: 1}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	provider := &lookupSubtitleProvider{}
	service, err := subtitles.New(subtitles.Options{DB: c.DB, Directory: filepath.Join(dir, "subtitles"), Storage: storage.New(executable), HelperBinary: executable, Provider: provider, Authorize: func(ctx context.Context, tx *sql.Tx, p identity.Principal, target string) (identity.Principal, error) {
		if target != item.Public {
			return p, identity.ErrUnauthorized
		}
		return p, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	f := struct {
		s       *subtitles.Service
		db      *sql.DB
		catalog *catalogtest.Catalog
		item    catalogtest.Item
		p       identity.Principal
	}{service, c.DB, c, item, principal}
	search := func() string {
		t.Helper()
		found, err := f.s.Search(context.Background(), f.p, f.item.Public, subtitles.SearchRequest{SourceID: f.item.Token, Language: "en", Page: 1})
		if err != nil || len(found.Candidates) != 1 {
			t.Fatal(found, err)
		}
		var stored, current int64
		if err = f.db.QueryRow(`SELECT c.lookup_epoch,s.lookup_epoch FROM subtitle_provider_candidates c CROSS JOIN metadata_runtime_settings s WHERE c.id=? AND s.singleton=1`, found.Candidates[0].ID).Scan(&stored, &current); err != nil || stored != current {
			t.Fatal("search did not bind epoch", stored, current, err)
		}
		return found.Candidates[0].ID
	}
	old := search()
	// A different viewer's obsolete capabilities give cleanup a multi-turn pass.
	for i := 0; i < 30; i++ {
		f.catalog.Exec(`INSERT INTO subtitle_provider_candidates(id,actor,item_id,source_id,source_size,source_modified_ns,provider,file_id,language,title,attribution,expires_at) VALUES(?,'other',?,'source',1,1,'fixture',42,'en','Subtitle','','later')`, fmt.Sprintf("seed-%03d", i), f.item.ID)
	}
	runtime := New(f.db, "")
	save := func(online bool) {
		t.Helper()
		current, err := runtime.MetadataSettings(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_, err = runtime.UpdateMetadataSettings(context.Background(), MetadataSettingsUpdate{ExpectedRevision: current.Revision, LocalArtworkLimitBytes: current.LocalArtworkLimitBytes, IncludeAdult: current.IncludeAdult, OnlineLookups: &online}, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	apply := func(id, op string) error {
		_, err := f.s.Apply(context.Background(), f.p, f.item.Public, subtitles.ApplyRequest{OperationID: op, CandidateID: id, Scope: "personal", Rights: "Fixture"})
		return err
	}
	save(false)
	f.catalog.Exec(`UPDATE metadata_lookup_fanout SET last_served_ms=100 WHERE effect<>'subtitles'`)
	if err := runtime.LookupFanoutStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	var pass, pending int
	if err := f.db.QueryRow(`SELECT pass_generation,pending FROM metadata_lookup_fanout WHERE effect='subtitles'`).Scan(&pass, &pending); err != nil || pass != 1 || pending != 1 {
		t.Fatal("fixture did not keep cleanup mid-flight", pass, pending, err)
	}
	save(true)
	if err := apply(old, "old-after-on"); !errors.Is(err, subtitles.ErrConflict) {
		t.Fatal("pre-toggle candidate revived", err)
	}
	current := search()
	save(false)
	save(true)
	// Do not settle cleanup: the retained capability must fail by epoch alone.
	var retained int
	if err := f.db.QueryRow(`SELECT count(*) FROM subtitle_provider_candidates WHERE id=?`, current).Scan(&retained); err != nil || retained != 1 {
		t.Fatal(retained, err)
	}
	if err := apply(current, "current-after-off-on"); !errors.Is(err, subtitles.ErrConflict) {
		t.Fatal("mid-pass candidate revived", err)
	}
	if provider.downloads != 0 {
		t.Fatal("obsolete capability reached transport", provider.downloads)
	}
	fresh := search()
	// Cleanup must not remove current-epoch capabilities even inside an old pass.
	for i := 0; i < 100; i++ {
		var due bool
		if err := f.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM metadata_lookup_fanout WHERE pending=1)`).Scan(&due); err != nil {
			t.Fatal(err)
		}
		if !due {
			break
		}
		if err := runtime.LookupFanoutStep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if err := apply(fresh, "fresh-after-fanout"); err != nil || provider.downloads != 1 {
		t.Fatal("fresh capability did not survive cleanup", err, provider.downloads)
	}
}
