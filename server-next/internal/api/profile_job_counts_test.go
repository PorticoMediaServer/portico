package api

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func hideProfileJobTitle(t *testing.T, th *theatre, id string) {
	t.Helper()
	locks, err := th.s.db.MetadataLocks(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := th.s.db.EditMetadata(context.Background(), id, locks.Revision, db.MetadataEdit{ContentRating: new("R")}); err != nil {
		t.Fatal(err)
	}
	main := get[Page[Profile]](t, th.s, th.owner.AccessToken, "/v1/profiles").Items[0].ID
	if code := status(t, th.s, "PUT", "/v1/profiles/"+main+"/restrictions", th.owner.AccessToken, map[string]any{"maxRatingAge": 13, "allowUnrated": true}); code != 200 {
		t.Fatal(code)
	}
}

func TestFinishedProfileJobCountsFollowCurrentReaderAccess(t *testing.T) {
	th := newTheatre(t)
	owner := th.owner.AccessToken
	var job Job
	req := JobStart{Kind: stateJobKind, Target: JobTarget{Filter: "library=" + th.libs[db.LibraryShows].PublicID + "&kind=episode"}, Params: JSON(`{"favorite":true}`)}
	if w := call(t, th.s, "POST", "/v1/jobs", owner, req, &job); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	done := waitForJob(t, th.s, owner, job.ID)
	if done.Progress == nil || done.Progress.Done != 2 {
		t.Fatal(done)
	}
	hideProfileJobTitle(t, th, th.eps[0])
	current := get[Job](t, th.s, owner, "/v1/jobs/"+job.ID)
	if current.Progress == nil || current.Progress.Done != 1 || current.Progress.Total == nil || *current.Progress.Total != 1 {
		t.Errorf("hidden finished count: %+v", current.Progress)
	}
	// The owner's management list keeps the original whole-job counts by the lead's ruling.
	found := false
	for _, raw := range get[Page[AdminJob]](t, th.s, owner, "/v1/admin/jobs").Items {
		if raw.ID == job.ID {
			found = true
			if raw.Progress == nil || raw.Progress.Done != 2 {
				t.Errorf("management count changed: %+v", raw.Progress)
			}
		}
	}
	if !found {
		t.Error("management job absent")
	}

	stored, err := th.s.db.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	faultJob, err := th.s.db.StartJob(context.Background(), db.NewJob{Kind: stateJobKind, Target: "profile-count-fault", Payload: stored.Payload, OwnerID: stored.OwnerID})
	if err != nil {
		t.Fatal(err)
	}
	if ran, err := th.s.db.RunJob(context.Background(), faultJob.ID); err != nil || !ran {
		t.Fatal(ran, err)
	}
	c := CallerOf(t, th.s, owner)
	v := db.Viewer{UserID: c.UserID, ProfileID: c.ProfileID}
	var edits []db.ItemEdit
	for _, id := range th.eps {
		edits = append(edits, db.ItemEdit{ItemID: id, StateEdit: db.StateEdit{Favorite: new(true)}})
	}
	if _, _, err := th.s.db.SetJobStates(context.Background(), v, edits, faultJob.ID); err != nil {
		t.Fatal(err)
	}
	if err := th.s.db.JobProgress(context.Background(), faultJob.ID, 2, 2); err != nil {
		t.Fatal(err)
	}
	running := get[Job](t, th.s, owner, "/v1/jobs/"+faultJob.PublicID)
	if running.Progress == nil || running.Progress.Done != 1 || running.Progress.Total != nil {
		t.Errorf("running selection leaked original total: %+v", running.Progress)
	}
	fault := "The view could not be read after 2 titles, so this stopped there. What was done stays done; asking again finishes it."
	if err := th.s.db.FinishJob(context.Background(), faultJob.ID, db.JobFailed, fault+"\x1fHidden episode details"); err != nil {
		t.Fatal(err)
	}
	current = get[Job](t, th.s, owner, "/v1/jobs/"+faultJob.PublicID)
	if current.Error != "The view could not be read. What was done stays done; asking again finishes it." {
		t.Errorf("profile route kept stored counts or private detail: %q", current.Error)
	}
	for _, raw := range get[Page[AdminJob]](t, th.s, owner, "/v1/admin/jobs").Items {
		if raw.ID == faultJob.PublicID && raw.Error != fault+" Hidden episode details" {
			t.Errorf("management fault changed: %q", raw.Error)
		}
	}
}

func TestListJobNoteDoesNotCountANewlyHiddenExpandedChild(t *testing.T) {
	th := newTheatre(t)
	owner := th.owner.AccessToken
	var list ListCreated
	call(t, th.s, "POST", "/v1/lists", owner, ListCreate{Kind: "playlist", Name: "Show"}, &list)
	var job Job
	req := JobStart{Kind: listViewJobKind, Target: JobTarget{Filter: "library=" + th.libs[db.LibraryShows].PublicID + "&kind=show"}, Params: JSON(`{"list":"` + list.ID + `"}`)}
	if w := call(t, th.s, "POST", "/v1/jobs", owner, req, &job); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	done := waitForJob(t, th.s, owner, job.ID)
	if done.Error != "Added 2." {
		t.Fatal(done)
	}
	call(t, th.s, "POST", "/v1/lists/"+list.ID+"/entries", owner, ListAdd{Items: []string{th.film}}, nil)
	hideProfileJobTitle(t, th, th.eps[0])
	current := get[Job](t, th.s, owner, "/v1/jobs/"+job.ID)
	if current.Error != "Added 1." {
		t.Errorf("hidden child in list-job note: %q", current.Error)
	}
}
