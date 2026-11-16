package api

import "testing"

func TestManagementJobResultIsNotReadableFromChildProfile(t *testing.T) {
	th := newTheatre(t)
	s, owner := th.s, th.owner.AccessToken
	var job Job
	if w := call(t, s, "POST", "/v1/jobs", owner, JobStart{Kind: "edit_items", Target: JobTarget{Items: []string{th.film}}, Params: JSON(`{"year":2000}`)}, &job); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	waitForJob(t, s, owner, job.ID)
	var kid Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Kid"}, &kid)
	call(t, s, "PUT", "/v1/profiles/"+kid.ID+"/restrictions", owner, map[string]any{"maxRatingAge": 6}, nil)
	var selected Session
	call(t, s, "POST", "/v1/profiles/"+kid.ID+"/select", owner, ProfileSelect{}, &selected)
	if w := call(t, s, "GET", "/v1/jobs/"+job.ID, selected.AccessToken, nil, nil); w.Code != 404 {
		t.Errorf("child read management result: %d %s", w.Code, w.Body)
	}
	if code := status(t, s, "DELETE", "/v1/jobs/"+job.ID, selected.AccessToken, nil); code != 404 {
		t.Errorf("child cancelled management job: HTTP %d", code)
	}
	if code := status(t, s, "GET", "/v1/jobs/"+job.ID, onMain(t, s, signIn(t, s, 9)).AccessToken, nil); code != 200 {
		t.Errorf("owner result: HTTP %d", code)
	}

}
