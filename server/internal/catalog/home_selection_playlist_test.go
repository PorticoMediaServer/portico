package catalog

import (
	"context"
	"encoding/json"
	"testing"
)

func TestHomeSelectionPlaylistSkipsExistingAcrossBulkContainerLeafAndReplay(t *testing.T) {
	service, scheduler, principal, viewer, access, _, names := bulkFixture(t, 3)
	actor := jobActor(principal)
	name := "Selection destination"
	created, err := service.MutatePlaylist(actor, "", "create", "", PlaylistMutation{OperationID: "selection-create", Name: &name}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Ordinary deliberate single Adds remain distinct playlist occurrences.
	one, err := service.MutatePlaylist(actor, created.PlaylistID, "add", "", PlaylistMutation{OperationID: "single-one", ExpectedRevision: created.Revision, ItemID: names["ep000000"].Public}, nil)
	if err != nil {
		t.Fatal(err)
	}
	two, err := service.MutatePlaylist(actor, created.PlaylistID, "add", "", PlaylistMutation{OperationID: "single-two", ExpectedRevision: one.Revision, ItemID: names["ep000000"].Public}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if one.EntryID == two.EntryID {
		t.Fatal("ordinary duplicate Add was collapsed")
	}
	resource, err := service.Playlist("server", "fence", created.PlaylistID, actor)
	if err != nil {
		t.Fatal(err)
	}
	if resource.EntryCount != 2 {
		t.Fatal(resource)
	}
	run := func(operation string, selector JobSelector, revision int64) BulkJob {
		t.Helper()
		request := JobRequest{OperationID: operation, Command: "playlist-add", Selector: selector, Args: JobArguments{PlaylistID: created.PlaylistID, ExpectedRevision: &revision, Placement: json.RawMessage(`"end"`)}}
		job, err := service.CreateBulkJob(context.Background(), principal, viewer, request, scheduler, access)
		if err != nil {
			t.Fatal(err)
		}
		job = finishCapture(t, service, job, access)
		for iteration := 0; iteration < 20 && !bulkTerminal(job.State); iteration++ {
			job, err = service.AdvanceBulkJob(context.Background(), job.JobID, access)
			if err != nil {
				t.Fatal(err)
			}
		}
		if job.State != "complete" || job.Failed != 0 {
			t.Fatal(job)
		}
		replay, err := service.CreateBulkJob(context.Background(), principal, viewer, request, scheduler, access)
		if err != nil || replay.JobID != job.JobID || replay.Done != job.Done {
			t.Fatalf("replay changed: %+v %v", replay, err)
		}
		return job
	}
	job := run("bulk-show", JobSelector{Container: &JobContainer{Kind: "show", ID: names["show"].Public}}, resource.Revision)
	resource, err = service.Playlist("server", "fence", created.PlaylistID, actor)
	if err != nil {
		t.Fatal(err)
	}
	if resource.EntryCount != 4 || job.Done != 3 {
		t.Fatalf("bulk doubled an existing item, or claimed done was new inserts: count=%d done=%d", resource.EntryCount, job.Done)
	}
	// A separately selected leaf overlaps the selected show. Neither that second
	// job nor a durable retry may add another occurrence of the same title.
	run("bulk-overlapping-leaf", JobSelector{Items: &JobItems{IDs: []string{names["ep000000"].Public, names["ep000001"].Public}}}, resource.Revision)
	resource, err = service.Playlist("server", "fence", created.PlaylistID, actor)
	if err != nil {
		t.Fatal(err)
	}
	if resource.EntryCount != 4 {
		t.Fatalf("overlapping leaf/container duplicated entries: %d", resource.EntryCount)
	}
}
