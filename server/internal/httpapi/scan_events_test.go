package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/eventfeed"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestEventsLibraryScanRespectsRestrictions(t *testing.T) {
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	id, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, lib := range []string{"lib-a", "lib-b"} {
		if _, err = db.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES(?,?,?,?)`, lib, lib, "movie", "/"+lib); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('res-acc','res-acc',x'00','res-prof',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO direct_memberships(account_id,role,allowed_libraries,revision,disabled) VALUES('res-acc','member','["lib-a","lib-b"]',1,0)
	 ON CONFLICT(account_id) DO UPDATE SET role='member',allowed_libraries=excluded.allowed_libraries,disabled=0`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO direct_profiles(id,account_id,name,is_primary,position,allowed_libraries) VALUES('res-prof','res-acc','Restricted',1,0,'["lib-a"]')
	 ON CONFLICT(id) DO UPDATE SET account_id=excluded.account_id,is_primary=1,deleted=0,allowed_libraries=excluded.allowed_libraries`); err != nil {
		t.Fatal(err)
	}
	env, err := id.Issue("res-acc", "res-prof", "local", "member", 1)
	if err != nil {
		t.Fatal(err)
	}
	control, err := hosted.New(db, id, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	d := Dependencies{DB: db, Identity: id, Hosted: control}
	d.Events = eventfeed.New(d.DB)
	d.Events.LibraryVisible = func(tx *sql.Tx, p identity.Principal, library string) bool {
		return d.Hosted.AllowedTx(p, library, tx) == nil
	}
	mux := http.NewServeMux()
	d.foundationRoutes(mux)
	appendScan := func(library string) {
		if err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
			return apievents.Append(tx, apievents.LibraryAudience(library), "library.scan.updated", "library", library, "1758715200000", map[string]any{"state": "started", "found": 5})
		}); err != nil {
			t.Fatal(err)
		}
	}
	start := eventStart(t, notificationRequest(mux, "GET", env.AccessToken, "/v1/events?waitMs=0", ""))
	appendScan("lib-a")
	appendScan("lib-b")
	w := notificationRequest(mux, "GET", env.AccessToken, "/v1/events?after="+start+"&waitMs=0", "")
	if w.Code != 200 {
		t.Fatalf("events: %d %s", w.Code, w.Body.String())
	}
	var page eventfeed.Page
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 {
		t.Fatalf("restricted profile must receive only lib-a: %+v", page)
	}
	if page.Events[0].Resource.ID != "lib-a" || page.Events[0].Type != "library.scan.updated" {
		t.Fatalf("wrong scan event: %+v", page.Events[0])
	}
}
