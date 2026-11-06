package httpapi

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/eventfeed"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func authorityTestRole(t *testing.T, d Dependencies, role string) {
	t.Helper()
	ctx := context.Background()
	if err := dbwork.WithWriteTx(ctx, d.DB, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		if _, err := identity.ExecMembershipTx(ctx, tx, "member", `UPDATE direct_memberships SET role=?,revision=revision+1 WHERE account_id='member'`, role); err != nil {
			return err
		}
		return identity.RetierFamiliesTx(ctx, tx, "member", role)
	}); err != nil {
		t.Fatal(err)
	}
}

func authorityTestHub(t *testing.T, d *Dependencies) {
	t.Helper()
	d.Events = eventfeed.New(d.DB)
	ready, release := d.Events.Subscribe()
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); d.Events.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case <-ready: // The real feed's initial pass follows its security subscription.
	case <-time.After(5 * time.Second):
		t.Fatal("event feed did not become ready")
	}
}

func authorityTestFrame(r *bufio.Reader) (eventfeed.Event, error) {
	kind, data := "", ""
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return eventfeed.Event{}, err
		}
		switch {
		case strings.HasPrefix(line, "event: "):
			kind = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		case line == "\n" && kind != "":
			var event eventfeed.Event
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				return event, err
			}
			if event.Type != kind {
				return event, fmt.Errorf("frame kind %q differs from %q", kind, event.Type)
			}
			return event, nil
		}
	}
}

func TestCurrentViewerBypassesCachedAuthority(t *testing.T) {
	d, _, _, member := notificationFixture(t)
	authorityTestRole(t, d, "admin")
	old, err := d.Identity.Authenticate(member.AccessToken)
	if err != nil || old.Role != "admin" {
		t.Fatal(old, err)
	}
	authorityTestRole(t, d, "member")
	// A strict identity read must bypass even an otherwise-live cache entry.
	d.principals = newPrincipalCache()
	d.principals.store(old.Hash, old, time.Now(), dbwork.AuthorityGeneration())
	w := notificationRequest(http.HandlerFunc(d.currentViewer), "GET", member.AccessToken, "/v1/me", "")
	var reply struct{ Viewer identity.Viewer }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &reply) != nil || reply.Viewer.Role != "member" {
		t.Fatalf("/v1/me reused cached authority: %d %s", w.Code, w.Body.String())
	}
	if got := d.principals.stats().Hits; got != 0 {
		t.Fatalf("strict identity read consulted cache %d times", got)
	}
}

func TestLiveAuthorityFeedReportsRoleAndLibraryChanges(t *testing.T) {
	for _, change := range []string{"role", "libraries"} {
		t.Run(change, func(t *testing.T) {
			d, _, _, member := notificationFixture(t)
			authorityTestRole(t, d, "admin")
			authorityTestHub(t, &d)
			mux := http.NewServeMux()
			d.foundationRoutes(mux)
			server := httptest.NewServer(mux)
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/v1/events", nil)
			req.Header.Set("Authorization", "Bearer "+member.AccessToken)
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			reader := bufio.NewReader(response.Body)
			if opened, err := authorityTestFrame(reader); err != nil || opened.Type != "stream.opened" {
				t.Fatal("open authority stream", opened, err)
			}
			if change == "role" {
				authorityTestRole(t, d, "member")
			} else if err := dbwork.WithWriteTx(ctx, d.DB, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
				_, err := identity.ExecMembershipTx(ctx, tx, "member", `UPDATE direct_memberships SET allowed_libraries='["changed-library"]',revision=revision+1 WHERE account_id='member'`)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			for {
				event, err := authorityTestFrame(reader)
				if err != nil {
					t.Fatalf("no authority hint after %s change: %v", change, err)
				}
				if event.Type == "authority.changed" {
					if event.Revision != "3" || len(event.Data) != 0 || event.Resource.ID != "self" {
						t.Fatalf("authority event disclosed more than membership revision: %+v", event)
					}
					break
				}
			}
		})
	}
}

func TestDisableClosesLiveAuthorityStreamAndRefusesLongPoll(t *testing.T) {
	d, _, _, member := notificationFixture(t)
	authorityTestHub(t, &d)
	mux := http.NewServeMux()
	d.foundationRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+member.AccessToken)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	if opened, err := authorityTestFrame(reader); err != nil || opened.Type != "stream.opened" {
		t.Fatal(opened, err)
	}
	p, err := d.Identity.Authenticate(member.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	wake, release := d.Events.SubscribeFor(p, false)
	defer release()
	start, err := d.Events.Read(ctx, p, false, "")
	if err != nil {
		t.Fatal(err)
	}
	poll := httptest.NewRequest("GET", "/v1/events?waitMs=25000", nil).WithContext(ctx)
	poll.Header.Set("Authorization", "Bearer "+member.AccessToken)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); d.eventLongPoll(w, poll, p, wake, start, 25000) }()
	if err := dbwork.WithWriteTx(ctx, d.DB, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		if _, err := identity.ExecMembershipTx(ctx, tx, "member", `UPDATE direct_memberships SET disabled=1,revision=revision+1 WHERE account_id='member'`); err != nil {
			return err
		}
		return identity.RevokeFamiliesMatchingTx(ctx, tx, identity.RevokedMembershipRemoved, `authority=? AND account_id=?`, "local", "member")
	}); err != nil {
		t.Fatal(err)
	}
	for {
		event, err := authorityTestFrame(reader)
		if err != nil {
			t.Fatalf("disabled stream closed without its terminal hint: %v", err)
		}
		if event.Type == "stream.closed" {
			var data struct{ Reason string }
			if json.Unmarshal(event.Data, &data) != nil || data.Reason != "authority_changed" {
				t.Fatal("wrong terminal reason", string(event.Data))
			}
			break
		}
		if event.Type == "authority.changed" {
			t.Fatal("revoked stream delivered a normal authority page")
		}
	}
	if _, err := reader.ReadString('\n'); err == nil {
		t.Fatal("disabled stream remained open after its terminal hint")
	}
	select {
	case <-done:
		if w.Code != 401 {
			t.Fatalf("disabled parked poll: %d %s", w.Code, w.Body.String())
		}
	case <-ctx.Done():
		t.Fatal("disabled parked poll did not retire")
	}
}

func TestAuthorityChangeReachesOtherProfilesAndDevices(t *testing.T) {
	d, _, _, member := notificationFixture(t)
	if _, err := d.DB.Exec(`INSERT INTO direct_profiles(id,account_id,name) VALUES('other-profile','member','Another profile')`); err != nil {
		t.Fatal(err)
	}
	other, err := d.Identity.Issue("member", "other-profile", "local", "member", 1)
	if err != nil {
		t.Fatal(err)
	}
	authorityTestHub(t, &d)
	mux := http.NewServeMux()
	d.foundationRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	readers := []*bufio.Reader{}
	devices := map[string]bool{}
	for _, token := range []string{member.AccessToken, other.AccessToken} {
		p, err := d.Identity.Authenticate(token)
		if err != nil || p.DeviceID == "" || devices[p.DeviceID] {
			t.Fatal("fixture must cover distinct authenticated devices", err)
		}
		devices[p.DeviceID] = true
		req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/v1/events", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		reader := bufio.NewReader(response.Body)
		if event, err := authorityTestFrame(reader); err != nil || event.Type != "stream.opened" {
			t.Fatal(event, err)
		}
		readers = append(readers, reader)
	}
	authorityTestRole(t, d, "admin")
	for _, reader := range readers {
		for {
			event, err := authorityTestFrame(reader)
			if err != nil {
				t.Fatal("another profile/device missed its authority change", err)
			}
			if event.Type == "authority.changed" {
				if event.Revision != "2" || len(event.Data) != 0 {
					t.Fatal("unexpected own authority hint", event)
				}
				break
			}
		}
	}
}

func TestRoleChangeAnswersParkedPollWithOnlyItsAuthorityHint(t *testing.T) {
	d, _, _, member := notificationFixture(t)
	authorityTestRole(t, d, "admin")
	authorityTestHub(t, &d)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	r := httptest.NewRequest("GET", "/v1/events?waitMs=25000", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+member.AccessToken)
	p, admin, err := d.eventAuthority(r)
	if err != nil {
		t.Fatal(err)
	}
	r, err = d.withCapacityAdmission(r, p)
	if err != nil {
		t.Fatal(err)
	}
	wake, release := d.Events.SubscribeForContext(r.Context(), p, admin)
	defer release()
	start, err := d.Events.Read(r.Context(), p, admin, "")
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); d.eventLongPoll(w, r, p, wake, start, 25000) }()
	authorityTestRole(t, d, "member")
	select {
	case <-done:
		var page eventfeed.Page
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Events) != 1 || page.Events[0].Type != "authority.changed" || page.Events[0].Revision != "3" {
			t.Fatalf("parked role change: %d %s", w.Code, w.Body.String())
		}
	case <-ctx.Done():
		t.Fatal("parked role change did not retire")
	}
}
