package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/hosted"
)

func TestRemoteOutsideIsCheckedOnlyByTheOwnerAndFailuresStayUnknown(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s).AccessToken
	s.link.stop()
	<-s.link.done
	now := time.Now().UTC()
	calls, response := 0, http.StatusOK
	proof := hosted.ReachabilityResult{CheckedAt: now, Addresses: []hosted.ReachabilityAddress{
		{Address: "203.0.113.8", Port: 443, Reachable: true}, {Address: "2001:db8::1", Port: 32500, Reachable: false}}}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if response == -1 {
			<-r.Context().Done()
			return
		}
		w.WriteHeader(response)
		if response == http.StatusOK {
			_ = json.NewEncoder(w).Encode(proof)
		}
	}))
	defer fake.Close()
	s.link.client = hosted.New(hosted.Options{Origin: fake.URL, HTTP: fake.Client(), ServerID: s.id, Key: s.identity.key, Now: func() time.Time { return now }})
	const route = "/v1/admin/remote-access/check"
	var out RemoteAccessStatus
	if w := call(t, s, "POST", route, owner, nil, &out); w.Code != 200 || out.Outside != nil || calls != 0 {
		t.Fatal("local server contacted Services", w.Code, out.Outside, calls)
	}
	if err := s.db.LinkServer(context.Background(), db.NewLink{OwnerID: 1, AccountID: "account", IssuedAt: now, Now: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.db.SaveSetting(context.Background(), db.SignInKindKey, db.SignInPortico, now); err != nil {
		t.Fatal(err)
	}
	if got := get[RemoteAccessStatus](t, s, owner, "/v1/admin/remote-access"); got.Outside != nil || calls != 0 {
		t.Fatal("page load made a call", got.Outside, calls)
	}
	if w := call(t, s, "POST", route, "", nil, nil); w.Code != 401 || calls != 0 {
		t.Fatal(w.Code, calls)
	}
	if w := call(t, s, "POST", route, owner, nil, &out); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if out.Outside == nil || out.Outside.CheckedAt == nil || !out.Outside.CheckedAt.Equal(proof.CheckedAt) || len(out.Outside.Addresses) != 2 {
		t.Fatal(out.Outside)
	}
	for i, addr := range proof.Addresses {
		if out.Outside.Addresses[i] != addr {
			t.Fatal("changed Services' result", out.Outside)
		}
	}
	_ = get[RemoteAccessStatus](t, s, owner, "/v1/admin/remote-access")
	if calls != 1 {
		t.Fatal("reading the result checked again", calls)
	}
	response = 404
	out = RemoteAccessStatus{}
	if w := call(t, s, "POST", route, owner, nil, &out); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if out.Outside == nil || out.Outside.Problem == "" || out.Outside.CheckedAt != nil || len(out.Outside.Addresses) != 0 {
		t.Fatal("refusal was called unreachable", out.Outside)
	}
	response = -1
	timeoutClient := fake.Client()
	timeoutClient.Timeout = 20 * time.Millisecond
	s.link.client = hosted.New(hosted.Options{Origin: fake.URL, HTTP: timeoutClient, ServerID: s.id, Key: s.identity.key, Now: func() time.Time { return now }})
	out = RemoteAccessStatus{}
	if w := call(t, s, "POST", route, owner, nil, &out); w.Code != 200 || out.Outside == nil || out.Outside.Problem == "" || out.Outside.CheckedAt != nil || len(out.Outside.Addresses) != 0 {
		t.Fatal("timeout was called unreachable", w.Code, out.Outside)
	}

}
