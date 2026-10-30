package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"strings"
	"testing"
)

func TestHomeDestinationPickerHTTPUsesLiveActorAndBoundedContract(t *testing.T) {
	handler, owner, _ := editorHTTPFixture(t)
	body, _ := json.Marshal(catalog.HomeDestinationPickerRequest{Kind: "playlist", Query: "", Limit: 40})
	response := editorRequest(t, handler, "POST", "/v1/home/destination-picker", owner, "application/json", body)
	if response.Code != 200 {
		t.Fatalf("picker read: %d %s", response.Code, response.Body.String())
	}
	tl6AssertSpecResponse(t, "POST", "/v1/home/destination-picker", response)
	var page catalog.HomeDestinationPickerPage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) > 40 || page.ViewerFence == "" {
		t.Fatal(page)
	}
	// A malformed request is a request error, not a stale page: only the cursor
	// that cannot be decoded below says invalid_cursor. Both codes are already
	// declared on this route, so the wire set does not change.
	code := func(body catalog.HomeDestinationPickerRequest) (int, string) {
		t.Helper()
		raw, _ := json.Marshal(body)
		w := editorRequest(t, handler, "POST", "/v1/home/destination-picker", owner, "application/json", raw)
		return w.Code, bodyErrorCode(w.Body.String())
	}
	tooMany := make([]string, 201)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("item-%03d", i)
	}
	for name, body := range map[string]catalog.HomeDestinationPickerRequest{
		"oversized window": {Kind: "playlist", Query: "", Limit: 41},
		"oversized id set": {Kind: "playlist", Query: "", Limit: 40, ItemIDs: tooMany},
		"duplicate id set": {Kind: "playlist", Query: "", Limit: 40, ItemIDs: []string{"item-a", "item-a"}},
		"unknown kind":     {Kind: "library", Query: "", Limit: 40},
	} {
		status, got := code(body)
		if status != http.StatusBadRequest || got != "invalid_request" {
			t.Fatalf("%s answered %d %q, want 400 invalid_request", name, status, got)
		}
	}
	if status, got := code(catalog.HomeDestinationPickerRequest{Kind: "playlist", Query: "", Limit: 40, Cursor: "not-a-cursor"}); status != http.StatusBadRequest || got != "invalid_cursor" {
		t.Fatalf("a cursor that cannot be decoded answered %d %q, want 400 invalid_cursor", status, got)
	}
	unauthenticated := editorRequest(t, handler, "POST", "/v1/home/destination-picker", "", "application/json", body)
	if unauthenticated.Code == 200 {
		t.Fatal("anonymous picker disclosed personal resources")
	}
}

func TestHomeDestinationPickerHidesRestrictedMembershipAndArtwork(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	m := buildRestrictionMatrix(t)
	hidden := m.hiddenItems["movie"][0]
	for kind, destination := range map[string]string{"playlist": m.playlist, "collection": m.savedResource} {
		for _, selected := range []string{hidden, absentTwin(hidden)} {
			body := fmt.Sprintf(`{"kind":%q,"limit":40,"itemIds":[%q,%q]}`, kind, selected, m.visibleMovie)
			status, raw := m.call("POST", "/v1/home/destination-picker", body, "restricted")
			if status != http.StatusOK || strings.Contains(raw, hidden) {
				t.Fatalf("%s picker disclosed restricted content: %d %s", kind, status, raw)
			}
			var page catalog.HomeDestinationPickerPage
			if err := json.Unmarshal([]byte(raw), &page); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, row := range page.Rows {
				if row.ID == destination {
					found = true
					if row.AlreadyIn == nil || *row.AlreadyIn != 1 || len(row.Artwork) != 1 || row.Artwork[0].ID != m.visibleMovie {
						t.Fatalf("%s picker did not retain only visible membership/artwork: %+v", kind, row)
					}
				}
			}
			if !found {
				t.Fatalf("%s picker never reached the mixed-content destination", kind)
			}
		}
	}
}
