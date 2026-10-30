package httpapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/ingestion"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Opt-in integration host. Only upstream HTTP is fixture-controlled; all product
// routing, auth, scanning, metadata parsing/publication and playback are real.
func TestMetadataBrowserFixture(t *testing.T) {
	root := os.Getenv("PORTICO_METADATA_BROWSER_FIXTURE")
	if root == "" {
		t.Skip("opt-in isolated browser fixture")
	}
	if !filepath.IsAbs(root) || filepath.Base(root) != "metadata-review" {
		t.Fatal("requires owned absolute metadata-review directory")
	}
	listener, e := net.Listen("tcp", "127.0.0.1:19422")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	if e = os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(root, "db")); !os.IsNotExist(e) {
		t.Fatal("fixture database already exists; do not overwrite")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v4/login":
			json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]string{"token": "isolated-fixture-token"}})
		case r.URL.Path == "/v4/search":
			json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": []map[string]any{{"tvdb_id": "42", "name": "Metadata Review Fixture", "year": "2026", "type": "series", "overview": "Controlled upstream fixture: successful publication."}, {"tvdb_id": "43", "name": "Metadata Review Fixture — Retry Scenario", "year": "2026", "type": "series", "overview": "Controlled upstream fixture: fails until the fixture retry flag is enabled."}}})
		case strings.HasPrefix(r.URL.Path, "/v4/series/"):
			var id int64
			fmt.Sscanf(r.URL.Path, "/v4/series/%d/", &id)
			if id != 42 && id != 43 {
				http.Error(w, "fixture unknown series", 404)
				return
			}
			if id == 43 {
				if _, e := os.Stat(filepath.Join(root, "allow-retry")); e != nil {
					w.WriteHeader(422)
					json.NewEncoder(w).Encode(map[string]string{"status": "failure"})
					return
				}
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(1200 * time.Millisecond):
			}
			var episodes []map[string]any
			for i := 1; i <= 3; i++ {
				episodes = append(episodes, map[string]any{"id": id*100 + int64(i), "seriesId": id, "name": fmt.Sprintf("Verified Fixture %d — Episode %d", id, i), "seasonNumber": 1, "number": i, "overview": "Published by the actual metadata worker from controlled provider HTTP."})
			}
			json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"series": map[string]int64{"id": id}, "episodes": episodes}, "links": map[string]any{"next": nil}})
		default:
			http.Error(w, "fixture route unavailable", 404)
		}
	}))
	defer upstream.Close()
	pool := x509.NewCertPool()
	pool.AddCert(upstream.Certificate())
	original := http.DefaultTransport
	transport := original.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{RootCAs: pool, ServerName: "example.com", MinVersion: tls.VersionTLS12}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "api4.thetvdb.com:443" {
			return nil, errors.New("fixture denies external networking")
		}
		return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
	}
	http.DefaultTransport = transport
	defer func() { http.DefaultTransport = original; transport.CloseIdleConnections() }()
	db, e := persistence.Open(filepath.Join(root, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ident, e := identity.New(db, root)
	if e != nil {
		t.Fatal(e)
	}
	cat := catalog.New(db)
	scan := ingestion.New(db, cat, assets.Probe{})
	media := filepath.Join(root, "media")
	os.Mkdir(media, 0700)
	source := filepath.Join(root, "..", "..", "fixtures", "library-layout", "synthetic-master.mp4")
	for i := 1; i <= 3; i++ {
		if e = os.Link(source, filepath.Join(media, fmt.Sprintf("Metadata Review Fixture - S01E%02d.mp4", i))); e != nil {
			t.Fatal(e)
		}
	}
	lib, e := cat.Create("Metadata review fixture", "tv", media)
	if e != nil {
		t.Fatal(e)
	}
	job, e := scan.Queue(lib.ID)
	if e != nil {
		t.Fatal(e)
	}
	go scan.Run(ctx)
	deadline := time.Now().Add(30 * time.Second)
	for {
		j, e := scan.Get(job.ID)
		if e != nil {
			t.Fatal(e)
		}
		if j.Status == "complete" {
			break
		}
		if j.Status == "failed" || time.Now().After(deadline) {
			t.Fatal("fixture scan failed")
		}
		time.Sleep(100 * time.Millisecond)
	}
	var showID, seasonID, manualID int64
	if e = db.QueryRow(`SELECT s.entity_id FROM catalog_shows s JOIN catalog_entities e ON e.id=s.entity_id JOIN catalog_libraries l ON l.id=e.library_id WHERE l.library_id=?`, lib.ID).Scan(&showID); e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow(`SELECT entity_id FROM catalog_seasons WHERE show_id=?`, showID).Scan(&seasonID); e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow(`SELECT entity_id FROM catalog_episodes WHERE show_id=? AND number=3`, showID).Scan(&manualID); e != nil {
		t.Fatal(e)
	}
	catalogFixture := catalogtest.New(t, db)
	catalogFixture.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetFactsTx(ctx, tx, manualID, map[string]any{"local_identity_status": "manual"})
	})
	catalogFixture.Fields(manualID, map[string]any{"title": "Owner-preserved episode title"})
	catalogFixture.Drain()
	show, season, manual := catalogFixture.Public(showID), catalogFixture.Public(seasonID), catalogFixture.Public(manualID)
	md := metadata.New(db, "fixture-only-no-real-tmdb-token")
	go md.Run(ctx)
	control, e := hosted.New(db, ident, "", "", "")
	if e != nil {
		t.Fatal(e)
	}
	handler := New(Dependencies{DB: db, Identity: ident, Catalog: cat, Ingestion: scan, Playback: playback.New(db), Metadata: md, Hosted: control, Origins: []string{"http://127.0.0.1:19418"}})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()
	raw, _ := json.MarshalIndent(map[string]any{"fixture": true, "upstream": "controlled TVDB HTTPS transport only; no live providers", "serverUrl": "http://127.0.0.1:19422", "serverId": ident.ID(), "libraryId": lib.ID, "showId": show, "seasonId": season, "manualEpisodeId": manual, "setupTokenPath": storagepaths.Path(root, "setup-token"), "retryFlagPath": filepath.Join(root, "allow-retry"), "pid": os.Getpid()}, "", "  ")
	if e = os.WriteFile(filepath.Join(root, "fixture.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	<-ctx.Done()
}
