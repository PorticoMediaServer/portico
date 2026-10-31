package networking

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Back from an unapproved setup (setupflow S8) retires its claim on this server and
// stops the wait for Portico Services' decision. From then on the server asks Portico
// Services nothing about it: not the open wait, not a withdrawal, not a resumed wait,
// not recovery. A decision Portico Services would send later has nowhere to land.
func TestAStoppedSetupWatchAsksPorticoServicesNothingMore(t *testing.T) {
	_, h, _ := handlerFixture(t)
	httpTr, err := NewHTTPTransport("https://hosted.example", &credentialFixture{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(httpTr.CloseIdleConnections)
	var calls, streams atomic.Int32
	var streamClosed atomic.Bool
	httpTr.client.Transport = claimRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		switch r.URL.Path {
		case "/v1/server-claims/pending":
			code := strings.Repeat("a", 49)
			return reply(200, `{"code":"`+code+`","serverId":"s","name":"Test server","address":"https://web.example","requestedAt":"2026-10-15T00:00:00Z","expiresAt":"2026-10-15T00:10:00Z","status":"pending"}`), nil
		case "/v1/server-claims/events":
			streams.Add(1)
			<-r.Context().Done()
			streamClosed.Store(true)
			return nil, r.Context().Err()
		}
		return reply(200, `{"status":"approved"}`), nil
	})
	h.transport = httpTr
	owner := h.authorize
	var guard ClaimRequestGuard
	h.authorize = func(r *http.Request) (ClaimRequestOwner, error) {
		o, e := owner(r)
		o.Resume, guard = "the-owner", o.Guard
		return o, e
	}

	s, code := handlerCall(t, h, "GET", "/v1/networking/claim", nil)
	if code != 200 {
		t.Fatalf("initial: %d", code)
	}
	body := json.RawMessage(`{"expected":{"serverId":"` + s.Identity.ServerID + `","localGeneration":"` + strconv.FormatInt(s.Identity.LocalGeneration, 10) + `"}}`)
	if s, code = handlerCall(t, h, "POST", "/v1/networking/claim/prepare", body); code != 200 || s.Operation == nil {
		t.Fatalf("prepare: %d %+v", code, s)
	}
	expected := struct {
		Expected ClaimExpected `json:"expected"`
	}{*s.Operation}
	if s, code = handlerCall(t, h, "POST", "/v1/networking/claim/web-approval", expected); code != 200 || s.ClaimCode == "" {
		t.Fatalf("web approval: %d %+v", code, s)
	}
	operation := s.Operation.OperationID
	expected.Expected = *s.Operation
	waitFor(t, func() bool { return streams.Load() == 1 })

	// What identity.AbandonSetup commits, then what it asks of networking after commit.
	if _, err = h.store.db.Exec(`UPDATE networking_claim_intents SET stage='cancelled',revision=revision+1 WHERE operation_id=?;
 UPDATE networking_claim_identity SET active_operation_id=NULL WHERE singleton=1 AND active_operation_id=?`, operation, operation); err != nil {
		t.Fatal(err)
	}
	h.StopSetupWatch(operation)
	waitFor(t, func() bool { _, running := h.webWatches.Load(operation); return !running && streamClosed.Load() })
	before := calls.Load()

	// A restart, a page that waits again, and the recovery loop find nothing to ask.
	h.ResumeWebClaims(t.Context(), func(string) ClaimRequestGuard { return guard })
	raw, _ := json.Marshal(expected)
	r := httptest.NewRequest("POST", "/v1/networking/claim/await-approval", bytes.NewReader(raw))
	r.Header.Set("Origin", "https://web.example")
	r.Header.Set("Authorization", "Bearer current")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.awaitWebClaim(w, r)
	if w.Code == 200 {
		t.Fatalf("a wait on the abandoned claim was accepted: %s", w.Body.String())
	}
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); h.RunWebClaimRecovery(ctx, func(string) ClaimRequestGuard { return guard }) }()
	h.wakeClaimRecovery()
	time.Sleep(300 * time.Millisecond)
	h.wakeClaimRecovery()
	time.Sleep(300 * time.Millisecond)
	stop()
	<-done

	if after := calls.Load(); after != before {
		t.Fatalf("the server asked Portico Services %d more time(s) after Back", after-before)
	}
	var stage string
	var active any
	if err = h.store.db.QueryRow(`SELECT i.stage,c.active_operation_id FROM networking_claim_intents i, networking_claim_identity c WHERE i.operation_id=? AND c.singleton=1`, operation).Scan(&stage, &active); err != nil || stage != "cancelled" || active != nil {
		t.Fatalf("the abandoned claim moved: stage=%q active=%v %v", stage, active, err)
	}
	// Stopping an unknown or finished wait is harmless.
	h.StopSetupWatch(operation)
	h.StopSetupWatch("unknown")
	var none *ClaimHandler
	none.StopSetupWatch(operation)
}

// The race at the edge of Back: Portico Services' approval reaches the wait after Back
// has committed but before the wait is stopped. The approved path reads this server's
// own state first (the claim is cancelled and no longer active), refuses, and its
// withdrawal finds the claim cancelled: nothing is asked of Portico Services.
func TestALateApprovalAfterBackAsksPorticoServicesNothing(t *testing.T) {
	_, h, _ := handlerFixture(t)
	httpTr, err := NewHTTPTransport("https://hosted.example", &credentialFixture{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(httpTr.CloseIdleConnections)
	var calls, streams atomic.Int32
	release := make(chan string, 1)
	httpTr.client.Transport = claimRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		switch r.URL.Path {
		case "/v1/server-claims/pending":
			code := strings.Repeat("a", 49)
			return reply(200, `{"code":"`+code+`","serverId":"s","name":"Test server","address":"https://web.example","requestedAt":"2026-10-15T00:00:00Z","expiresAt":"2026-10-15T00:10:00Z","status":"pending"}`), nil
		case "/v1/server-claims/events":
			streams.Add(1)
			select {
			case answer := <-release:
				return reply(200, answer), nil
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}
		t.Errorf("Portico Services was asked %s after Back", r.URL.Path)
		return reply(500, `{}`), nil
	})
	h.transport = httpTr

	s, code := handlerCall(t, h, "GET", "/v1/networking/claim", nil)
	if code != 200 {
		t.Fatalf("initial: %d", code)
	}
	body := json.RawMessage(`{"expected":{"serverId":"` + s.Identity.ServerID + `","localGeneration":"` + strconv.FormatInt(s.Identity.LocalGeneration, 10) + `"}}`)
	if s, code = handlerCall(t, h, "POST", "/v1/networking/claim/prepare", body); code != 200 || s.Operation == nil {
		t.Fatalf("prepare: %d %+v", code, s)
	}
	expected := struct {
		Expected ClaimExpected `json:"expected"`
	}{*s.Operation}
	if s, code = handlerCall(t, h, "POST", "/v1/networking/claim/web-approval", expected); code != 200 || s.ClaimCode == "" {
		t.Fatalf("web approval: %d %+v", code, s)
	}
	operation := s.Operation.OperationID
	waitFor(t, func() bool { return streams.Load() == 1 })

	// Back has committed (what identity.AbandonSetup writes) …
	if _, err = h.store.db.Exec(`UPDATE networking_claim_intents SET stage='cancelled',revision=revision+1 WHERE operation_id=?;
 UPDATE networking_claim_identity SET active_operation_id=NULL WHERE singleton=1 AND active_operation_id=?`, operation, operation); err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	// … and the approval arrives before the wait is stopped.
	current, ok := h.webWatches.Load(operation)
	if !ok {
		t.Fatal("no wait to deliver the approval to")
	}
	release <- `{"status":"approved","approvalEnvelope":{}}`
	select {
	case <-current.(*webClaimWatch).done:
	case <-time.After(10 * time.Second):
		t.Fatal("the late approval was never handled")
	}
	if answer := string(current.(*webClaimWatch).answer); answer != `{"state":"approval_changed"}` {
		t.Fatalf("the late approval answered %s", answer)
	}
	time.Sleep(200 * time.Millisecond) // any late withdrawal would run in this window
	h.StopSetupWatch(operation)

	if after := calls.Load(); after != before {
		t.Fatalf("the server asked Portico Services %d more time(s) after Back", after-before)
	}
	var stage string
	if err = h.store.db.QueryRow(`SELECT stage FROM networking_claim_intents WHERE operation_id=?`, operation).Scan(&stage); err != nil || stage != "cancelled" {
		t.Fatalf("the late approval moved the abandoned claim: %q %v", stage, err)
	}
	// Nor is anything queued for Portico Services to be told later.
	var queued int
	if err = h.store.db.QueryRow(`SELECT count(*) FROM networking_claim_cancellations WHERE operation_id=?`, operation).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("a withdrawal was queued for the abandoned claim: %d %v", queued, err)
	}
}
