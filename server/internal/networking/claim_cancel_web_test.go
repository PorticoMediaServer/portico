package networking

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"testing"
)

// A web-approval claim nobody approved has no claim_operations row at Hosted,
// so Hosted answers its cancellation with 409 approval_required. For a
// mode-"intent" cancellation (ClaimGeneration "", nothing ever committed at
// Hosted) that answer is Hosted's acknowledgement: there is no credential or
// grant to revoke, and BeginCancel already fenced later approvals by
// advancing reset_generation. A committed claim still needs Hosted's ack.
func TestClaimCoordinatorCancelWithoutHostedOperationCompletes(t *testing.T) {
	c, s, r := fixture(t)
	r.cancelErr = ErrApprovalRequired
	v, e := c.Cancel(fixtureClaimContext(t))
	if e != nil || v.Stage != Cancelled {
		t.Fatalf("intent cancel not acknowledged: %v %+v", e, v)
	}
	if s.value.Stage != Cancelled {
		t.Fatal("cancellation not acknowledged in store")
	}
	cancels := r.cancels
	v, e = c.Cancel(fixtureClaimContext(t))
	if e != nil || v.Stage != Cancelled {
		t.Fatalf("second cancel failed: %v", e)
	}
	if r.cancels != cancels {
		t.Fatal("second cancel re-sent to Hosted")
	}
}

func TestClaimCoordinatorCommittedCancelNeedsHostedAck(t *testing.T) {
	c, s, r := fixture(t)
	if _, e := c.Step(fixtureClaimContext(t)); e != nil {
		t.Fatal(e)
	}
	r.cancelErr = ErrApprovalRequired
	v, e := c.Cancel(fixtureClaimContext(t))
	if !errors.Is(e, ErrApprovalRequired) {
		t.Fatalf("committed cancel did not need Hosted ack: %v", e)
	}
	if v.Stage != CancelPending || s.value.Stage != CancelPending {
		t.Fatal("committed cancel left without Hosted acknowledgement")
	}
}

func TestClaimCoordinatorPendingCancelReplaysOnContinue(t *testing.T) {
	c, s, r := fixture(t)
	r.cancelErr = errors.New("offline")
	v, e := c.Cancel(fixtureClaimContext(t))
	if !errors.Is(e, ErrUnavailable) || v.Stage != CancelPending {
		t.Fatalf("offline cancel not pending: %v %s", e, v.Stage)
	}
	original := s.cancellation.RequestID
	r.cancelErr = ErrApprovalRequired
	v, e = c.Step(fixtureClaimContext(t))
	if e != nil || v.Stage != Cancelled {
		t.Fatalf("continue did not replay cancellation: %v %+v", e, v)
	}
	if s.cancellation.RequestID != original || r.cancels != 2 {
		t.Fatal("continue did not replay the stored request id")
	}
}

// End to end: prepare (web-style, no account) then cancel while the stub
// Hosted answers 409 with the real nested approval_required envelope. The
// intent clears locally (snapshot unconfigured with action prepare) and a new
// prepare succeeds.
func TestClaimHandlerIntentCancelWithoutHostedOperationClearsIntent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	_, h, _ := handlerFixture(t)
	httpTr, e := NewHTTPTransport("https://hosted.example", &credentialFixture{})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(httpTr.CloseIdleConnections)
	cancels := 0
	httpTr.client.Transport = claimRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/server-claims/cancel" {
			t.Errorf("unexpected Hosted call %s", r.URL.Path)
			return reply(500, `{}`), nil
		}
		cancels++
		return reply(409, `{"error":{"code":"approval_required","message":"This server claim could not be completed.","retryable":false}}`), nil
	})
	h.transport = httpTr
	prepare := func() ClaimStatus {
		s, code := handlerCall(t, h, "GET", "/v1/networking/claim", nil)
		if code != 200 || s.State != "unconfigured" {
			t.Fatalf("initial: %d %+v", code, s)
		}
		body := json.RawMessage(`{"expected":{"serverId":"` + s.Identity.ServerID + `","localGeneration":"` + strconv.FormatInt(s.Identity.LocalGeneration, 10) + `"}}`)
		s, code = handlerCall(t, h, "POST", "/v1/networking/claim/prepare", body)
		if code != 200 || s.State != "prepared" || s.AccountID != UnboundAccount {
			t.Fatalf("prepare: %d %+v", code, s)
		}
		return s
	}
	s := prepare()
	s, code := handlerCall(t, h, "POST", "/v1/networking/claim/cancel", struct {
		Expected ClaimExpected `json:"expected"`
	}{*s.Operation})
	if code != 200 || s.State != "unconfigured" || len(s.Actions) != 1 || s.Actions[0] != "prepare" {
		t.Fatalf("cancel: %d %+v", code, s)
	}
	if cancels != 1 {
		t.Fatalf("expected one Hosted cancel, got %d", cancels)
	}
	prepare()
}
