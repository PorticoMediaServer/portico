package api

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestHiddenSourceProgrammeCannotBeWatched(t *testing.T) {
	s, owner, _ := recordable(t)
	if code := status(t, s, "PUT", "/v1/admin/settings/live", owner, map[string]any{"bufferSeconds": 0}); code != 200 {
		t.Fatal(code)
	}
	id := programmeID(t, s, owner, "A Grim Film")
	p := get[LiveProgrammeDetail](t, s, owner, "/v1/live/programmes/"+id)
	// Move the test clock to the rated programme without waiting or contacting a real source.
	claims, err := s.tokens.CheckAccess(owner, s.now())
	if err != nil {
		t.Fatal(err)
	}
	now := p.StartsAt.Add(time.Minute)
	clock := newRigClock(now)
	s.setClock(clock.Now)
	owner, _ = s.tokens.Access(claims, now, now.Add(time.Hour))
	var kid Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Kid"}, &kid)
	call(t, s, "PUT", "/v1/profiles/"+kid.ID+"/restrictions", owner, map[string]any{"maxRatingAge": 13}, nil)
	var selected Session
	call(t, s, "POST", "/v1/profiles/"+kid.ID+"/select", owner, ProfileSelect{}, &selected)
	if got := get[LiveProgrammeDetail](t, s, selected.AccessToken, "/v1/live/programmes/"+id); !got.NotAvailable {
		t.Fatal("programme not restricted")
	}
	if w := call(t, s, "POST", "/v1/playback/sessions", selected.AccessToken, PlaybackStart{Channel: p.Channel.ID}, nil); w.Code != 404 {
		t.Errorf("hidden airing started: HTTP %d", w.Code)
	}
}

func TestHiddenProgrammeCannotCreateRecordingRule(t *testing.T) {
	s, owner, _ := recordable(t)
	if code := status(t, s, "PUT", "/v1/admin/settings/live", owner, map[string]any{"bufferSeconds": 0}); code != 200 {
		t.Fatal(code)
	}
	id := programmeID(t, s, owner, "A Grim Film")
	var kid Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Kid"}, &kid)
	call(t, s, "PUT", "/v1/profiles/"+kid.ID+"/restrictions", owner, map[string]any{"maxRatingAge": 13}, nil)
	var selected Session
	call(t, s, "POST", "/v1/profiles/"+kid.ID+"/select", owner, ProfileSelect{}, &selected)
	w := call(t, s, "POST", "/v1/dvr/rules?preview=true", selected.AccessToken, DvrRuleSave{Programme: id}, nil)
	if w.Code != 404 {
		t.Errorf("hidden rule preview: %d %s", w.Code, w.Body)
	}
}

func TestSourceSessionStopsWhenTheProgrammeBecomesHidden(t *testing.T) {
	s, owner, _ := recordable(t)
	if code := status(t, s, "PUT", "/v1/admin/settings/live", owner, map[string]any{"bufferSeconds": 0}); code != 200 {
		t.Fatal(code)
	}
	id := programmeID(t, s, owner, "A Grim Film")
	p := get[LiveProgrammeDetail](t, s, owner, "/v1/live/programmes/"+id)
	claims, err := s.tokens.CheckAccess(owner, s.now())
	if err != nil {
		t.Fatal(err)
	}
	now := p.StartsAt.Add(-time.Minute)
	clock := newRigClock(now)
	s.setClock(clock.Now)
	owner, _ = s.tokens.Access(claims, now, now.Add(2*time.Hour))
	var kid Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Kid"}, &kid)
	call(t, s, "PUT", "/v1/profiles/"+kid.ID+"/restrictions", owner, map[string]any{"maxRatingAge": 13}, nil)
	var selected Session
	call(t, s, "POST", "/v1/profiles/"+kid.ID+"/select", owner, ProfileSelect{}, &selected)
	var started PlaybackSession
	if w := call(t, s, "POST", "/v1/playback/sessions", selected.AccessToken, PlaybackStart{Channel: p.Channel.ID}, &started); w.Code != 201 {
		t.Fatal("before programme", w.Code, w.Body)
	}
	probe := &livePermissionProbe{ResponseRecorder: httptest.NewRecorder(), began: make(chan struct{})}
	finished := make(chan struct{})
	go func() { s.ServeHTTP(probe, newRequest("GET", started.Presentation.URL, "", nil)); close(finished) }()
	select {
	case <-probe.began:
	case <-time.After(3 * time.Second):
		t.Fatal("live request sent no bytes")
	}
	views := s.viewings.All()
	if len(views) != 1 {
		t.Fatal(views)
	}
	clock.Set(p.StartsAt.Add(time.Second))
	s.viewings.Recheck(context.Background())
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Error("open byte request kept running")
	}
	if len(s.viewings.All()) != 0 {
		t.Error("live viewing survived hidden programme boundary")
	}
	select {
	case <-views[0].Done():
	default:
		t.Error("open byte stream was not told to end")
	}
	if code := status(t, s, "GET", started.Presentation.URL, "", nil); code != 404 {
		t.Errorf("ended grant: HTTP %d", code)
	}
}

type livePermissionProbe struct {
	*httptest.ResponseRecorder
	once  sync.Once
	began chan struct{}
}

func (w *livePermissionProbe) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.began) })
	return w.ResponseRecorder.Write(p)
}
