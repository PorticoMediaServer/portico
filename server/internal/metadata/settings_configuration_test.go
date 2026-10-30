package metadata

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

func TestArtworkConfigurationUsesCanonicalWriterAndRecoversProjection(t *testing.T) {
	s, db, _ := repairFixture(t)
	ctx := context.Background()
	store := operations.New(db)
	store.ConfigurationDomains = []operations.ConfigurationDomain{Configuration{}}
	path := filepath.Join(t.TempDir(), "portico.toml")
	if err := os.WriteFile(path, []byte("[metadata]\nartwork_max_edge=1280\n[unrelated]\nkeep=true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.InitializeConfiguration(ctx, path); err != nil {
		t.Fatal(err)
	}
	current, err := s.MetadataSettings(ctx)
	if err != nil || current.ArtworkMaxEdge != 1280 {
		t.Fatal(current, err)
	}
	doc, err := store.Configuration(ctx, operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, field := range doc.Fields {
		if field.ID == "metadata.artworkSize" {
			found = field.Source == "file" && *field.Current == 1280 && *field.Effective == 1280 && *field.Default == 1920
		}
	}
	if !found {
		t.Fatal("missing actual file/current/factory projection")
	}
	// An interrupted post-commit projection is recoverable before import; the
	// stale file cannot overwrite the committed owner value on restart.
	saveArtworkEdge(t, s, 2560)
	restarted := operations.New(db)
	restarted.ConfigurationDomains = store.ConfigurationDomains
	if err := restarted.InitializeConfiguration(ctx, path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), "artwork_max_edge = 2560") || !strings.Contains(string(raw), "keep = true") {
		t.Fatal(string(raw), err)
	}
	current, err = s.MetadataSettings(ctx)
	if err != nil || current.ArtworkMaxEdge != 2560 {
		t.Fatal("stale file won", current, err)
	}
	s.ConfigurationCommitted = restarted.FlushConfiguration
	saveArtworkEdge(t, s, 1920)
	raw, err = os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), "artwork_max_edge = 1920") {
		t.Fatal(string(raw), err)
	}
	// A mixed invalid import rolls back both the unrelated settings mutation
	// and the metadata value/revision/source/sweep.
	before, err := restarted.Settings(ctx, operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("[general]\nname='Must not commit'\n[metadata]\nartwork_max_edge=2561\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.ConfigFile.Reload(); err != nil {
		t.Fatal(err)
	}
	if err = restarted.ImportConfiguration(ctx); err == nil {
		t.Fatal("invalid size imported")
	}
	after, err := restarted.Settings(ctx, operations.AllowServerScope)
	if err != nil || after.Revision != before.Revision || after.Requested.Name != before.Requested.Name {
		t.Fatal("partial import", after, err)
	}
	current, err = s.MetadataSettings(ctx)
	if err != nil || current.ArtworkMaxEdge != 1920 {
		t.Fatal(current, err)
	}
}
