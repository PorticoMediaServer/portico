package httpapi

import (
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/apispec"
	"github.com/PorticoMediaServer/portico/server/internal/apiwire"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"testing"
	"time"
)

func TestCaptureContract(t *testing.T) {
	d, mux, owner, _ := notificationFixture(t)
	d.consoleRoutes(mux)
	now := time.Now()
	d.Console.Now = func() time.Time { return now }
	doc, schema, err := apispec.Schema("Capture")
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range []string{"scheduler", "storage", "playback", "client", "support"} {
		body, _ := json.Marshal(operations.Capture{IdempotencyKey: component, Component: component, ExpiresAt: apiwire.Instant(now.Add(time.Hour).UnixMilli())})
		valid := component == "scheduler"
		if ok := len(doc.ValidateJSON(schema, body)) == 0; ok != valid {
			t.Fatalf("%s schema acceptance %v", component, ok)
		}
		w := notificationRequest(mux, "POST", owner.AccessToken, "/v1/admin/console/diagnostics/capture", string(body))
		want := 400
		if valid {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("%s: %d %s", component, w.Code, w.Body.String())
		}
		if valid {
			assertSpecResponse(t, "POST", "/v1/admin/console/diagnostics/capture", w)
		}
	}
	body, _ := json.Marshal(operations.Capture{Component: "scheduler", ExpiresAt: apiwire.Instant(now.Add(time.Hour + time.Millisecond).UnixMilli())})
	if w := notificationRequest(mux, "POST", owner.AccessToken, "/v1/admin/console/diagnostics/capture", string(body)); w.Code != 400 {
		t.Fatalf("accepted overlong capture: %d", w.Code)
	}
}
