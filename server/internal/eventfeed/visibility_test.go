package eventfeed

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestPersonalEventsFilterContentAndAdvanceCursor(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"}}
	audience := apievents.ProfileAudience(p.Authority, p.AccountID, p.ProfileID)
	h := New(db)
	h.LibraryVisible = func(_ *sql.Tx, _ identity.Principal, library string) bool { return library == "allowed" }
	checks := 0
	h.ContentVisible = func(_ context.Context, tx *sql.Tx, viewer identity.Principal, item string) bool {
		if tx == nil || viewer.ProfileID != p.ProfileID {
			t.Fatal("content check lost the page snapshot or viewer")
		}
		checks++
		return item == "visible"
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < PageLimit+1; i++ {
		if err := apievents.Append(tx, audience, "personal.changed", "item", "hidden", "1", nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, event := range []struct {
		kind, id string
		data     any
	}{
		{"item", "visible", nil},
		{"library", "allowed", map[string]any{"titles": []string{"hidden", "visible"}, "personal": "2"}},
		{"library", "allowed", map[string]any{"titles": []string{"visible"}, "personal": "3"}},
		{"library", "denied", nil},
		{"personal", "history", map[string]string{"observation": "4"}},
	} {
		if err := apievents.Append(tx, audience, "personal.changed", event.kind, event.id, "1", event.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// 106 raw rows: from the start that is more than a page, so one resync
	// and not a single content check.
	first, err := h.readAt(context.Background(), p, false, 0, true)
	if err != nil || len(first.Events) != 1 || first.Events[0].Type != "stream.resync" || first.NextAfter != strconv.Itoa(PageLimit+6) || checks != 0 {
		t.Fatalf("far-behind read must resync without checks: %+v checks=%d %v", first, checks, err)
	}
	// Exactly a page behind: 95 hidden rows are consumed (one cached verdict)
	// and the visible ones arrive.
	second, err := h.readAt(context.Background(), p, false, 6, true)
	if err != nil || second.More || len(second.Events) != 4 || second.NextAfter != strconv.Itoa(PageLimit+6) || checks != 2 {
		t.Fatalf("visible continuation: %+v checks=%d %v", second, checks, err)
	}
	encoded, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "hidden") || strings.Contains(string(encoded), "denied") {
		t.Fatalf("hidden content in retained event: %s", encoded)
	}
	var generic, visible map[string]json.RawMessage
	if err := json.Unmarshal(second.Events[1].Data, &generic); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Events[2].Data, &visible); err != nil {
		t.Fatal(err)
	}
	if _, present := generic["titles"]; present || string(generic["personal"]) != `"2"` || string(visible["titles"]) != `["visible"]` {
		t.Fatalf("generic or visible invalidation changed: %s %s", second.Events[1].Data, second.Events[2].Data)
	}
	last, err := strconv.ParseInt(second.NextAfter, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := h.readAt(context.Background(), p, false, last, true)
	if err != nil || len(empty.Events) != 0 || empty.NextAfter != second.NextAfter {
		t.Fatalf("cursor repeated or moved backwards: %+v %v", empty, err)
	}
}

func TestPersonalEventsWithoutContentFenceRedactTitles(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"}}
	h := New(db)
	h.LibraryVisible = func(*sql.Tx, identity.Principal, string) bool { return true }
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	audience := apievents.ProfileAudience(p.Authority, p.AccountID, p.ProfileID)
	if err := apievents.Append(tx, audience, "personal.changed", "item", "hidden", "1", nil); err != nil {
		t.Fatal(err)
	}
	if err := apievents.Append(tx, audience, "progress.changed", "library", "allowed", "1", map[string]any{"titles": []string{"hidden"}, "observation": "1"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	page, err := h.readAt(context.Background(), p, false, 0, true)
	if err != nil || len(page.Events) != 1 || page.NextAfter != "2" || strings.Contains(string(page.Events[0].Data), "hidden") || strings.Contains(string(page.Events[0].Data), "titles") {
		t.Fatalf("missing content authority disclosed identifiers: %+v %v", page, err)
	}
}
