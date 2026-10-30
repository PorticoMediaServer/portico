package httpapi

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/access"
)

func TestPeopleInvitationTableSearchCountsAndPaginationUseWholeAuthorizedSet(t *testing.T) {
	f := administrationFixtureNew(t)
	for i := 0; i < 26; i++ {
		note := "other"
		if i%2 == 0 {
			note = "search-hit"
		}
		body := fmt.Sprintf(`{"operationId":"invitation-table-%03d","notes":%q,"role":"member","allowedLibraries":[],"expiresInHours":24}`, i, note)
		if w := f.call("POST", "/v1/admin/access/invitations", f.owner, body); w.Code != 201 {
			t.Fatal("genuine invitation creation", w.Code, w.Body)
		}
	}
	if w := f.call("POST", "/v1/admin/access/invitations", f.owner, `{"operationId":"invitation-table-peer","notes":"owner-private-needle","role":"admin","allowedLibraries":[],"expiresInHours":24}`); w.Code != 201 {
		t.Fatal("genuine peer invitation", w.Code, w.Body)
	}
	seen := map[string]bool{}
	cursor := ""
	for {
		path := "/v1/admin/access/invitations?q=search-hit&sort=status&direction=asc&limit=2&cursor=" + url.QueryEscape(cursor)
		w := f.call("GET", path, f.admin, "")
		var page access.InvitationTablePage
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.TotalCount != 27 || page.MatchedCount != 13 || len(page.Items) > 2 || page.Limit != 2 {
			t.Fatal("counted whole-set page", w.Code, w.Body)
		}
		for _, row := range page.Items {
			if seen[row.ID] || row.Code != "" || row.Notes == nil || *row.Notes != "search-hit" {
				t.Fatal("page repeated, leaked a code, or returned an unmatched row")
			}
			seen[row.ID] = true
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(seen) != 13 {
		t.Fatal("search filtered only a partial page", len(seen))
	}
	for _, actor := range []struct {
		token string
		count int64
	}{{f.owner, 1}, {f.admin, 0}} {
		w := f.call("GET", "/v1/admin/access/invitations?q=owner-private-needle", actor.token, "")
		var page access.InvitationTablePage
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.MatchedCount != actor.count || page.TotalCount != 27 {
			t.Fatal("private peer note affected administrator search/count", w.Code, w.Body)
		}
	}
	for _, query := range []string{"direction=sideways", "sort=username", "state=unknown", "q=" + strings.Repeat("x", 161), "limit=9999"} {
		if w := f.call("GET", "/v1/admin/access/invitations?"+query, f.admin, ""); w.Code != 400 {
			t.Fatal("hostile query was not refused", w.Code)
		}
	}
}
