package api

import "testing"

// Reset recommendations answers with this server's id and the app's operation id, which the
// app checks before saying it is done, and refuses an operation id it could not have made.
func TestResetRecommendationsAnswers(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	var done RecommendationsResetDone
	if w := call(t, s, "POST", "/v1/me/recommendations:reset", owner, RecommendationsReset{OperationID: "op_1-a"}, &done); w.Code != 200 ||
		done.ServerID != s.id || done.OperationID != "op_1-a" {
		t.Fatalf("reset answered %d: %s", w.Code, w.Body)
	}
	if code := status(t, s, "POST", "/v1/me/recommendations:reset", owner, RecommendationsReset{OperationID: "not one"}); code != 400 {
		t.Errorf("an operation id with a space answered %d, want 400", code)
	}
}
