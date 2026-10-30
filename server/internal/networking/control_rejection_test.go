package networking

import (
	"net/http"
	"testing"
)

// A71: only a refusal that can never succeed is permanent. Rate limiting (429),
// request timeouts (408), server errors and authentication failures are retried
// with backoff; other 4xx answers are permanent.
func TestControlRejectionOnlyForPermanentRefusals(t *testing.T) {
	for _, c := range []struct {
		status    int
		permanent bool
	}{{400, true}, {404, true}, {422, true}, {408, false}, {429, false}, {401, false}, {403, false}, {500, false}, {503, false}} {
		// The fixture's credential is single-use (CallServer clears it), so each case gets its own.
		transport, v, _, _ := httpFixture(t)
		v.InstallationAcknowledged = true
		status := c.status
		transport.client.Transport = claimRoundTrip(func(*http.Request) (*http.Response, error) {
			return reply(status, `{"code":"x"}`), nil
		})
		err := transport.CallServer(fixtureClaimContext(t), v, PublishServerEvent, map[string]string{"kind": "rename"}, nil)
		if err == nil {
			t.Fatalf("%d: no error", status)
		}
		if got := ControlRejected(err); got != c.permanent {
			t.Fatalf("%d: permanent=%v, want %v (%v)", status, got, c.permanent, err)
		}
	}
}
