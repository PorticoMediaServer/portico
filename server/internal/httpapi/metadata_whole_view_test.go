package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

func TestBulkMetadataWholeViewEditsDisplayedShowsOnce(t *testing.T) {
	bulkMetadataDisplayedShows(t, false)
}

func TestBulkMetadataWholeViewReportsContainerFailures(t *testing.T) {
	bulkMetadataDisplayedShows(t, true)
}

func bulkMetadataDisplayedShows(t *testing.T, staleFirst bool) {
	d, owner, _ := tl6SupportFixture(t)
	c := catalogtest.New(t, d.DB)
	tv := c.Library("tv", "TV", "tv", "/tv")
	shows := []catalogtest.Item{}
	for n := 0; n < 3; n++ {
		show := c.Show(tv, fmt.Sprintf("Series %d", n), 2020)
		season := c.Season(show, 1)
		episode := c.Entity(compactcatalog.Entity{Library: tv, Kind: compactcatalog.Episode, Parent: season.ID, Key: fmt.Sprintf("episode-%d", n), Title: "Episode"}, map[string]any{"show_id": show.ID, "season_id": season.ID, "numbering": "seasonal", "number": 1})
		c.File(episode.ID, fmt.Sprintf("/tv/%d.mkv", n), 90)
		shows = append(shows, show)
	}
	c.Drain()
	d.Metadata = metadata.New(d.DB, "")
	d.Scheduler = operations.NewScheduler(operations.New(d.DB))
	d.Catalog.BulkCommands = d.bulkCommandHooks()
	if err := d.Scheduler.Register(d.Catalog.BulkAdapter(d.bulkAccess)); err != nil {
		t.Fatal(err)
	}
	p, err := d.Identity.Authenticate(owner.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	profile := identity.PersonalKey(p.Viewer)
	scopeRequest := httptest.NewRequest("GET", "/v1/jobs", nil)
	scopeRequest.Header.Set("Authorization", "Bearer "+owner.AccessToken)
	_, _, fence, err := d.homeScope(scopeRequest)
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := d.catalogViewer(scopeRequest, p, []string{"tv"}, fence)
	if err != nil {
		t.Fatal(err)
	}
	page, err := d.Catalog.BrowseEntities(viewer, catalog.BrowseRequest{Library: "tv", Profile: profile, ViewerFence: fence, Pivot: "shows", Limit: 37})
	if err != nil {
		t.Fatal(err)
	}
	network := "A network"
	request := catalog.JobRequest{OperationID: "whole-view-shows", Command: "metadata-edit", Selector: catalog.JobSelector{Query: &catalog.JobQuery{LibraryID: "tv", Pivot: "shows"}}, Expected: &catalog.JobExpected{CatalogRevision: page.PageInfo.Revision}, Args: catalog.JobArguments{Fields: map[string]catalog.JobFieldEdit{"network": {Value: &network}}}}
	raw, _ := json.Marshal(request)
	response := editorRequest(t, New(d), "POST", "/v1/jobs", owner.AccessToken, "application/json", raw)
	if response.Code != 202 {
		t.Fatalf("job HTTP %d %s", response.Code, response.Body.String())
	}
	var job catalog.BulkJob
	if err = json.Unmarshal(response.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	if staleFirst {
		for n := 0; n < 100; n++ {
			var state string
			if err = d.DB.QueryRow(`SELECT state FROM personal_jobs WHERE id=?`, job.JobID).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if state != "building" {
				break
			}
			job, err = d.Catalog.AdvanceBulkJob(context.Background(), job.JobID, d.bulkAccess)
			if err != nil {
				t.Fatal(err)
			}
		}
		if _, err = d.DB.Exec(`UPDATE catalog_shows SET network='Changed after capture' WHERE entity_id=?`, shows[0].ID); err != nil {
			t.Fatal(err)
		}
	}
	for n := 0; n < 100 && (job.State == "building" || job.State == "queued" || job.State == "running"); n++ {
		job, err = d.Catalog.AdvanceBulkJob(context.Background(), job.JobID, d.bulkAccess)
		if err != nil {
			t.Fatal(err)
		}
	}
	if staleFirst {
		if job.State != "partial" || job.Done != 2 || job.Failed != 1 {
			t.Fatalf("partial displayed targets %+v", job)
		}
		failures, err := d.Catalog.BulkFailures(profile, job.JobID, "", viewer)
		if err != nil || len(failures.Items) != 1 || failures.Items[0].ItemID != shows[0].Public || failures.Items[0].Code != "metadata_conflict" {
			t.Fatalf("container failure lost %+v %v", failures, err)
		}
		denied := viewer
		denied.Libraries = []string{}
		hidden, err := d.Catalog.BulkFailures(profile, job.JobID, "", denied)
		if err != nil || len(hidden.Items) != 0 {
			t.Fatalf("container failures leaked %+v %v", hidden, err)
		}
	} else if job.State != "complete" || job.Done != 3 || job.Failed != 0 {
		t.Fatalf("displayed targets %+v", job)
	}
	for index, show := range shows {
		var got string
		want := network
		if staleFirst && index == 0 {
			want = "Changed after capture"
		}
		if err = d.DB.QueryRow(`SELECT network FROM catalog_shows WHERE entity_id=?`, show.ID).Scan(&got); err != nil || got != want {
			t.Fatal("show not edited", got, err)
		}
	}
	var children int
	if err = d.DB.QueryRow(`SELECT count(*) FROM catalog_item_details d JOIN catalog_entities e ON e.id=d.entity_id WHERE e.library_id=? AND e.kind=4 AND d.network<>''`, tv).Scan(&children); err != nil || children != 0 {
		t.Fatal("edited descendants instead of displayed shows", children, err)
	}
}
