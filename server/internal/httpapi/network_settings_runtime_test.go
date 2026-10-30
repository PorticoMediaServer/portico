package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"testing"
)

func TestConnectivitySaveAppliesRuntimeSettings(t *testing.T) {
	f := administrationFixtureNew(t)
	d := f.deps
	calls := 0
	d.SettingsApplied = func(context.Context) { calls++ }
	h := New(d)
	doc, e := d.Console.Settings(context.Background(), operations.AllowServerScope)
	if e != nil {
		t.Fatal(e)
	}
	b := fmt.Sprintf(`{"expectedRevision":%d,"operationId":"network-runtime-save","remoteBitrateLimitKbps":6000}`, doc.Revision)
	r := httptest.NewRequest("PATCH", "http://media.example/v1/admin/connectivity/policy", bytes.NewBufferString(b))
	r.RemoteAddr = "127.0.0.1:9"
	r.Header.Set("Authorization", "Bearer "+f.owner)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("save %d %s", w.Code, w.Body)
	}
	if calls != 1 {
		t.Fatalf("connectivity settings saved but SettingsApplied callback called %d times", calls)
	}
}
func TestConsoleSaveStopsDiscovery(t *testing.T) {
	f := administrationFixtureNew(t)
	a := f.deps.Access.Advertiser
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.Bind(ctx, func(ctx context.Context) { <-ctx.Done() })
	a.Apply(true)
	defer a.Close()
	doc, e := f.deps.Console.Settings(ctx, operations.AllowServerScope)
	if e != nil {
		t.Fatal(e)
	}
	v := doc.Effective
	v.LANDiscoveryEnabled = false
	b, _ := json.Marshal(operations.SettingsChange{ExpectedRevision: doc.Revision, IdempotencyKey: "discovery-runtime-save", Values: v})
	w := f.call("PATCH", "/v1/admin/console/settings", f.owner, string(b))
	if w.Code != 200 {
		t.Fatalf("save %d %s", w.Code, w.Body)
	}
	if s := a.Status(false); s.State != "stopped" {
		t.Fatalf("saved discovery=false but runtime=%+v", s)
	}
}

func TestConnectivityChangePreservesPendingConnectionLimit(t *testing.T) {
	f := administrationFixtureNew(t)
	applied := 0
	f.deps.Console.AppliedConnectionLimit = &applied
	ctx := context.Background()
	doc, err := f.deps.Console.Settings(ctx, operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	values := doc.Requested
	values.ConnectionLimit = 128
	body, err := json.Marshal(operations.SettingsChange{ExpectedRevision: doc.Revision, IdempotencyKey: "pending-connection-limit", Values: values})
	if err != nil {
		t.Fatal(err)
	}
	if w := f.call("PATCH", "/v1/admin/console/settings", f.owner, string(body)); w.Code != 200 {
		t.Fatalf("save %d %s", w.Code, w.Body)
	}
	doc, err = f.deps.Console.Settings(ctx, operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	patch := fmt.Sprintf(`{"expectedRevision":%d,"operationId":"pending-limit-other-setting","advertiseUrl":"https://media.example"}`, doc.Revision)
	if w := f.call("PATCH", "/v1/admin/connectivity/policy", f.owner, patch); w.Code != 200 {
		t.Fatalf("partial save %d %s", w.Code, w.Body)
	}
	doc, err = f.deps.Console.Settings(ctx, operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Requested.ConnectionLimit != 128 || doc.Effective.ConnectionLimit != 0 {
		t.Fatalf("partial save lost pending connection limit: requested=%d effective=%d", doc.Requested.ConnectionLimit, doc.Effective.ConnectionLimit)
	}
}

func TestSettingsReplayDoesNotRestartDiscovery(t *testing.T) {
	f := administrationFixtureNew(t)
	a := f.deps.Access.Advertiser
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.Bind(ctx, func(ctx context.Context) { <-ctx.Done() })
	a.Apply(true)
	defer a.Close()
	doc, err := f.deps.Console.Settings(ctx, operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	values := doc.Requested
	values.LANDiscoveryEnabled = true
	first, _ := json.Marshal(operations.SettingsChange{ExpectedRevision: doc.Revision, IdempotencyKey: "discovery-on-save", Values: values})
	if w := f.call("PATCH", "/v1/admin/console/settings", f.owner, string(first)); w.Code != 200 {
		t.Fatalf("first save %d %s", w.Code, w.Body)
	}
	doc, err = f.deps.Console.Settings(ctx, operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	values = doc.Requested
	values.LANDiscoveryEnabled = false
	second, _ := json.Marshal(operations.SettingsChange{ExpectedRevision: doc.Revision, IdempotencyKey: "discovery-off-save", Values: values})
	if w := f.call("PATCH", "/v1/admin/console/settings", f.owner, string(second)); w.Code != 200 {
		t.Fatalf("second save %d %s", w.Code, w.Body)
	}
	if w := f.call("PATCH", "/v1/admin/console/settings", f.owner, string(first)); w.Code != 200 {
		t.Fatalf("replay %d %s", w.Code, w.Body)
	}
	if s := a.Status(false); s.State != "stopped" {
		t.Fatalf("historical receipt restarted discovery: %+v", s)
	}
}
