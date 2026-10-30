package httpapi

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/apispec"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

func TestFeedbackUnicodeLengthContract(t *testing.T) {
	_, mux, owner, member := notificationFixture(t)
	doc, schema, err := apispec.Schema("FeedbackSubmission")
	if err != nil {
		t.Fatal(err)
	}
	for i, tc := range []struct {
		name, message string
		status        int
		specValid     bool
	}{
		{"CJK maximum", strings.Repeat("日", 2000), 200, true},
		{"astral maximum", strings.Repeat("😀", 2000), 200, true},
		{"over maximum", strings.Repeat("日", 2001), 400, false},
		{"under minimum", strings.Repeat("日", 7), 400, false},
		{"control", "message\u0001unsafe", 400, true},
		{"bidi", "message\u202eunsafe", 400, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(operations.FeedbackSubmission{OperationID: fmt.Sprintf("length-%d", i), Kind: "playback", Category: "buffering", Message: tc.message})
			if err != nil {
				t.Fatal(err)
			}
			if ok := len(doc.ValidateJSON(schema, body)) == 0; ok != tc.specValid {
				t.Fatalf("spec acceptance %v, want %v", ok, tc.specValid)
			}
			w := notificationRequest(mux, "POST", member.AccessToken, "/v1/feedback/reports", string(body))
			if w.Code != tc.status {
				t.Fatalf("status %d want %d: %s", w.Code, tc.status, w.Body.String())
			}
			assertSpecResponse(t, "POST", "/v1/feedback/reports", w)
			if w.Code != 200 {
				return
			}
			var result struct {
				Data operations.FeedbackSubmissionResult
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			for j, size := range []int{2001, 2000} {
				reply, _ := json.Marshal(operations.FeedbackTransition{OperationID: fmt.Sprintf("reply-%d-%d", i, j), ExpectedRevision: result.Data.Report.Revision, Status: "resolved", Reply: strings.Repeat("😀", size)})
				w = notificationRequest(mux, "POST", owner.AccessToken, "/v1/admin/feedback/reports/"+result.Data.Report.ID+"/status", string(reply))
				want := 200
				if size > 2000 {
					want = 400
				}
				if w.Code != want {
					t.Fatalf("reply %d status %d: %s", size, w.Code, w.Body.String())
				}
				assertSpecResponse(t, "POST", "/v1/admin/feedback/reports/{id}/status", w)
			}
		})
	}
}
