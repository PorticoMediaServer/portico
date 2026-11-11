package updates

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A newer release at the owner's own address is found and said to be newer; Never looks
// nowhere; a development build is never told a release is newer.
func TestFindsANewerRelease(t *testing.T) {
	asked := 0
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tag_name":"v1.3.0","name":"Portico 1.3","body":"What is new","html_url":"https://example.invalid/r"}`))
	}))
	defer feed.Close()
	c := &Checker{Version: "v1.2.4", Now: time.Now}
	t.Setenv("PORTICO_UPDATES_ORIGIN", feed.URL)
	if got := c.Check(context.Background()); !got.Newer || got.Newest == nil || got.Newest.Notes != "What is new" {
		raw, _ := json.Marshal(got)
		t.Fatalf("the check found %s", raw)
	}
	if newer("v1.3.0", "dev") || newer("v1.2.4", "v1.2.4") || !newer("v2.0.0", "1.9.9") {
		t.Errorf("version order is wrong")
	}
	if asked != 1 {
		t.Errorf("the feed was asked %d times", asked)
	}
}
