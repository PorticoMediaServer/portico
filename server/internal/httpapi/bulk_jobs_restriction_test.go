package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"testing"
	"time"
)

// Called inside the exhaustive route matrix so watermark/count regressions are
// part of its required security gate, including newly visible and new members.
func (m *restrictionEnv) containerWatermarkSecurity(t *testing.T) {
	ctx := context.Background()
	for _, command := range []string{"refresh", "metadata-edit"} {
		body := fmt.Sprintf(`{"operationId":"deny-%s","command":"%s","selector":{"items":{"ids":[%q]}},"args":{}}`, command, command, m.visibleMovie)
		if code, raw := m.call("POST", "/v1/jobs", body, "restricted"); code != 401 && code != 403 {
			t.Fatalf("restricted owner command %s: %d %s", command, code, raw)
		}
	}
	var library string
	if e := m.db.QueryRow(`SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?)`, m.visibleEntities["show"][0]).Scan(&library); e != nil {
		t.Fatal(e)
	}
	c := m.fixtures
	show := c.Show(c.Handle(library), "Watermark", 2026)
	season := c.Season(show, 1)
	add := func(name, rating, added string, n int) catalogtest.Item {
		t.Helper()
		item := c.Episode(show, season, n, fmt.Sprintf("/watermark/%s.mkv", name))
		c.Fields(item.ID, map[string]any{"added_text": added})
		c.Attributes(item.ID, "contentRating", rating)
		if _, e := m.catalog.ClassifyPendingRatings(ctx); e != nil {
			t.Fatal(e)
		}
		c.Drain()
		return item
	}
	items := catalogtest.Names{
		"watermark-visible": add("watermark-visible", "G", "2026-01-01T00:00:00Z", 1),
		"watermark-hidden":  add("watermark-hidden", "NC-17", "2026-01-01T00:00:00Z", 2),
	}
	path := "/v1/containers/show/" + show.Public + "/personal-state"
	put := func(rev int, watched bool) {
		t.Helper()
		code, body := m.call("PUT", path, fmt.Sprintf(`{"expectedRevision":%d,"watched":%t}`, rev, watched), "restricted")
		if code != 200 {
			t.Fatalf("put: %d %s", code, body)
		}
	}
	counts := func(w, u int64) {
		t.Helper()
		code, body := m.call("GET", path, "", "restricted")
		var got catalog.ContainerPersonalView
		if e := json.Unmarshal([]byte(body), &got); e != nil || code != 200 || got.WatchedCount != w || got.UnwatchedCount != u {
			t.Fatalf("counts want %d/%d: %d %s %v", w, u, code, body, e)
		}
	}
	put(0, true)
	counts(1, 0) // Hidden member contributes neither watched nor unwatched.
	if code, _ := m.call("GET", "/v1/items/"+items["watermark-hidden"].Public, "", "restricted"); code != 404 {
		t.Fatalf("hidden item: %d", code)
	}
	c.Attributes(items["watermark-hidden"].ID, "contentRating", "G")
	if _, e := m.catalog.ClassifyPendingRatings(ctx); e != nil {
		t.Fatal(e)
	}
	c.Drain()
	counts(2, 0)
	profile := identity.PersonalKey(m.principals["restricted"].Viewer)
	got, e := m.catalog.Personal(profile, items["watermark-hidden"].Public)
	if e != nil || !got.Watched {
		t.Fatal("newly visible member must inherit", got, e)
	}
	items["watermark-new"] = add("watermark-new", "G", time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano), 3)
	counts(2, 1)
	no := false
	if _, e = m.catalog.SetPersonal("local:matrix-limited", profile, items["watermark-visible"].Public, catalog.PersonalMutation{OperationID: "matrix-explicit-false", ExpectedRevision: 1, Watched: &no}, nil); e != nil {
		t.Fatal(e)
	}
	counts(1, 2) // Explicit item state wins over the container.
	yes := true
	if _, e = m.catalog.SetPersonal("local:matrix-limited", profile, items["watermark-hidden"].Public, catalog.PersonalMutation{OperationID: "matrix-explicit-true", ExpectedRevision: 1, Watched: &yes}, nil); e != nil {
		t.Fatal(e)
	}
	put(1, false)
	counts(0, 3)
	counts(0, 3)
	// The job routes are isolated by profile and re-check current content access.
	body := fmt.Sprintf(`{"operationId":"matrix-job-isolation","command":"personal-state","selector":{"items":{"ids":[%q,%q]}},"args":{"favorite":true}}`, items["watermark-visible"].Public, items["watermark-hidden"].Public)
	code, body := m.call("POST", "/v1/jobs", body, "restricted")
	var job catalog.BulkJob
	if e = json.Unmarshal([]byte(body), &job); e != nil || code != 202 {
		t.Fatalf("create job: %d %s %v", code, body, e)
	}
	for _, suffix := range []string{"", "/failures"} {
		if code, body = m.call("GET", "/v1/jobs/"+job.JobID+suffix, "", "open"); code != 404 {
			t.Fatalf("other profile job %s: %d %s", suffix, code, body)
		}
	}
	if result, e := m.catalog.BulkAdapter(m.bulkAccess).Step(ctx, job.JobID); e != nil || result.State != "succeeded" {
		t.Fatal(result, e)
	}
	if code, body = m.call("GET", "/v1/jobs/"+job.JobID, "", "restricted"); code != 200 {
		t.Fatalf("own job: %d %s", code, body)
	}
}

func TestContainerUndoHTTPReceiptAndRestrictionScope(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	m := buildRestrictionMatrix(t)
	m.containerWatermarkSecurity(t)
	show := m.visibleEntities["show"][0]
	path := "/v1/containers/show/" + show + "/personal-state"
	code, body := m.call("GET", path, "", "restricted")
	var before catalog.ContainerPersonalView
	if e := json.Unmarshal([]byte(body), &before); e != nil || code != 200 {
		t.Fatal(code, body, e)
	}
	code, body = m.call("PUT", path, fmt.Sprintf(`{"expectedRevision":%d,"watched":true}`, before.Revision), "restricted")
	var mark catalog.ContainerPersonal
	if e := json.Unmarshal([]byte(body), &mark); e != nil || code != 200 || mark.UndoToken == "" {
		t.Fatal(code, body, e)
	}
	request := fmt.Sprintf(`{"expectedRevision":%d,"undoToken":%q}`, mark.Revision, mark.UndoToken)
	code, body = m.call("POST", path+"/undo", request, "open")
	if code != 412 {
		t.Fatal("other profile used undo receipt", code, body)
	}
	code, body = m.call("POST", path+"/undo", request, "restricted")
	var restored catalog.ContainerPersonal
	if e := json.Unmarshal([]byte(body), &restored); e != nil || code != 200 || restored.Watched != before.Watched || restored.Revision != mark.Revision+1 {
		t.Fatal(code, body, e)
	}
	code, body = m.call("POST", path+"/undo", request, "restricted")
	if code != 412 {
		t.Fatal("used receipt reapplied", code, body)
	}
}
