package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/eventfeed"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
)

func TestLibraryAgentChangeRefreshesAnotherDevicesHeldVocabulary(t *testing.T) {
	f := administrationFixtureNewWith(t, func(d *Dependencies) {
		d.Metadata = metadata.New(d.DB, "")
		d.Events = eventfeed.New(d.DB)
	})
	catalogtest.New(t, f.deps.DB).Library("movies", "Movies", "movie", "/movies")
	writer, err := f.deps.Identity.Issue("owner", "owner-profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	watcher, err := f.deps.Identity.Issue("owner", "owner-profile", "local", "owner", 1)
	if err != nil || writer.DeviceID == watcher.DeviceID {
		t.Fatalf("test needs distinct authenticated devices: %v", err)
	}
	flush := func() {
		t.Helper()
		if err := dbwork.WithWriteTx(context.Background(), f.deps.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
			_, err := apievents.Flush(context.Background(), tx, 100)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	var held catalog.BrowseCapabilities
	reads := 0
	readVocabulary := func() {
		t.Helper()
		w := f.call("GET", "/v1/libraries/movies/browse-capabilities?pivot=movies", watcher.AccessToken, "")
		if err := json.Unmarshal(w.Body.Bytes(), &held); err != nil || w.Code != 200 {
			t.Fatalf("vocabulary: %d %s %v", w.Code, w.Body, err)
		}
		reads++
	}
	hasMatch := func() bool {
		for _, field := range held.Fields {
			if field.ID == "match" {
				return true
			}
		}
		return false
	}
	readVocabulary()
	if !hasMatch() {
		t.Fatal("online vocabulary did not offer the match filter")
	}
	flush()
	start := eventStart(t, f.call("GET", "/v1/events?waitMs=0", watcher.AccessToken, ""))
	agent, err := f.deps.Metadata.LibraryAgent(context.Background(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	w := f.call("PUT", "/v1/libraries/movies/metadata/agent", writer.AccessToken, fmt.Sprintf(`{"expectedRevision":%d,"agent":"local"}`, agent.Revision))
	if w.Code != 200 {
		t.Fatalf("other device agent write: %d %s", w.Code, w.Body)
	}
	flush()
	w = f.call("GET", "/v1/events?after="+start+"&waitMs=0", watcher.AccessToken, "")
	var page eventfeed.Page
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 {
		t.Fatalf("other device feed: %d %s %v", w.Code, w.Body, err)
	}
	// The held non-reading vocabulary depends on settings:server, as the
	// browse-capabilities query does. Content changes only refresh its pages.
	for _, event := range page.Events {
		if event.Type == "settings.changed" && event.Resource.Kind == "settings" && event.Resource.ID == "server" {
			readVocabulary()
			break
		}
	}
	if reads != 2 || hasMatch() {
		t.Fatalf("held vocabulary missed the agent change: reads=%d match=%v events=%+v", reads, hasMatch(), page.Events)
	}
}
