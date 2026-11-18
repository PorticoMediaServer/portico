package api

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/playqueue"
)

// "Still watching?" is due on a video queue once as many episodes as the profile names have
// played one after another untouched (3 unless changed), never with it turned off, and not
// after the person touches the queue.
func TestStillWatchingIsDueAfterTheEpisodesTheProfileNames(t *testing.T) {
	th := newTheatre(t)
	q := th.create(QueueCreate{Source: playqueue.QueueSource{Items: []string{th.eps[0], th.eps[1]}}}).Queue
	path := "/v1/queues/" + q.ID
	if q.Medium != "video" || q.PassoutCheckDue {
		t.Fatalf("a new video queue: %+v", q)
	}
	th.queue("PATCH", path, "", QueueChange{Repeat: "one"}, &q)
	ended := func(session string) {
		t.Helper()
		th.queue("POST", path+"/advance", "", QueueAdvance{Reason: "completion", SessionID: session}, &q)
	}
	ended("ses_1")
	ended("ses_2")
	if q.AutomaticAdvances != 2 || q.PassoutCheckDue {
		t.Fatalf("after two episodes: %+v", q)
	}
	ended("ses_3")
	if q.AutomaticAdvances != 3 || !q.PassoutCheckDue {
		t.Fatalf("after three episodes the check is not due: %+v", q)
	}
	// The same answer reads it on the queue itself.
	var read Queue
	th.queue("GET", path, "", nil, &read)
	if !read.PassoutCheckDue {
		t.Fatalf("the queue read does not say it is due: %+v", read)
	}
	save := func(values map[string]any) {
		t.Helper()
		if code := status(t, th.s, "PATCH", "/v1/preferences", th.owner.AccessToken, PreferencesEdit{Scope: "profile-server", DeviceClass: "web", Values: values}); code != 200 {
			t.Fatalf("saving %v answered %d", values, code)
		}
	}
	save(map[string]any{"playback.passoutProtection": false})
	th.queue("GET", path, "", nil, &read)
	if read.PassoutCheckDue {
		t.Fatal("with Still watching? off the check is due")
	}
	save(map[string]any{"playback.passoutProtection": true, "playback.passoutAfterEpisodes": 4})
	th.queue("GET", path, "", nil, &read)
	if read.PassoutCheckDue {
		t.Fatal("at three of four episodes the check is due")
	}
	// The person touching the queue starts the count again.
	th.queue("POST", path+"/advance", "", QueueAdvance{Reason: "next"}, &q)
	if q.AutomaticAdvances != 0 || q.PassoutCheckDue {
		t.Fatalf("after Next: %+v", q)
	}
}
