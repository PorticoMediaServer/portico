package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/downloads"
	"strings"
	"testing"
)

// Kept inside the route matrix so both new routes have non-vacuous security probes.
func (m *restrictionEnv) downloadRequestSecurity(t *testing.T) {
	ctx := context.Background()
	p := m.principals["restricted"]
	var device string
	if e := m.db.QueryRow(`SELECT b.device_id FROM authorization_family_tokens t JOIN identity_device_families b ON b.family_id=t.family_id WHERE t.token_hash=?`, p.Hash).Scan(&device); e != nil {
		t.Fatal(e)
	}
	var library string
	if e := m.db.QueryRow(`SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?)`, m.visibleEntities["show"][0]).Scan(&library); e != nil {
		t.Fatal(e)
	}
	c := m.fixtures
	show := c.Show(c.Handle(library), "Downloads", 2026)
	season := c.Season(show, 1)
	episodes := catalogtest.Names{}
	for i, rating := range []string{"G", "NC-17"} {
		name := fmt.Sprint("download-matrix-", i)
		item := c.Episode(show, season, i+1, "/download-matrix/"+name+".mkv")
		c.Attributes(item.ID, "contentRating", rating)
		episodes[name] = item
	}
	if _, e := m.catalog.ClassifyPendingRatings(ctx); e != nil {
		t.Fatal(e)
	}
	c.Drain()
	request := downloads.ContainerRequest{OperationID: "matrix-download", Target: downloads.ContainerTarget{Kind: "show", ID: show.Public}, DeviceID: device, Quality: "original", Policy: downloads.EpisodePolicy{Episodes: "all"}}
	submit := func(r downloads.ContainerRequest) (int, string) {
		raw, _ := json.Marshal(r)
		return m.call("POST", "/v1/downloads/requests", string(raw), "restricted")
	}
	wrong := request
	wrong.DeviceID = "another-device"
	if code, raw := submit(wrong); code != 401 {
		t.Fatal("wrong device", code, raw)
	}
	hidden := request
	hidden.Target.ID = m.hiddenEntities["show"][0]
	if code, raw := submit(hidden); code != 404 {
		t.Fatal("hidden target", code, raw)
	}
	code, raw := submit(request)
	var job downloads.RequestView
	if e := json.Unmarshal([]byte(raw), &job); e != nil || code != 202 {
		t.Fatal(code, raw, e)
	}
	for i := 0; i < 4; i++ {
		if e := m.downloads.Advance(ctx); e != nil {
			t.Fatal(e)
		}
	}
	// While one episode is visible the request is listed, by the show's name.
	if code, raw = m.call("GET", "/v1/downloads/requests?deviceId="+device, "", "restricted"); code != 200 || !strings.Contains(raw, show.Public) || !strings.Contains(raw, `"Downloads"`) {
		t.Fatal("the device's request is not listed", code, raw)
	}
	path := "/v1/downloads/requests/" + job.RequestID
	code, raw = m.call("GET", path, "", "restricted")
	if e := json.Unmarshal([]byte(raw), &job); e != nil || code != 200 || job.Total != 1 || len(job.Items) != 1 || job.Items[0].ItemID != episodes["download-matrix-0"].Public {
		t.Fatal(code, raw, e)
	}
	if code, raw = m.call("GET", path, "", "open"); code != 404 {
		t.Fatal("other profile", code, raw)
	}
	changed := request
	changed.Policy.Episodes = "unwatched"
	if code, raw = submit(changed); code != 422 {
		t.Fatal("changed replay", code, raw)
	}
	c.Attributes(episodes["download-matrix-0"].ID, "contentRating", "NC-17")
	if _, e := m.catalog.ClassifyPendingRatings(ctx); e != nil {
		t.Fatal(e)
	}
	c.Drain()
	code, raw = m.call("GET", path, "", "restricted")
	if e := json.Unmarshal([]byte(raw), &job); e != nil || code != 200 || job.Total != 0 || len(job.Items) != 0 {
		t.Fatal("newly hidden request content", code, raw, e)
	}
	// With nothing of the show left that the profile may see, the list of the
	// device's requests no longer names it or its title.
	if code, raw = m.call("GET", "/v1/downloads/requests?deviceId="+device, "", "restricted"); code != 200 || strings.Contains(raw, show.Public) || strings.Contains(raw, `"Downloads"`) {
		t.Fatal("a request for a show that is now hidden is still listed by name", code, raw)
	}
}
