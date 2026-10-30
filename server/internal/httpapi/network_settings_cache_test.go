package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"testing"
)

func TestSettingsReplayDoesNotRestoreProxyTrust(t *testing.T) {
	f := administrationFixtureNew(t)
	ctx := context.Background()
	doc, err := f.deps.Console.Settings(ctx, operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	v := doc.Effective
	v.TrustedProxies = []string{"203.0.113.9/32"}
	v.LANNetworks = []string{"127.0.0.0/8"}
	first := operations.SettingsChange{ExpectedRevision: doc.Revision, IdempotencyKey: "first-network-save", Values: v}
	b, _ := json.Marshal(first)
	w := f.call("PATCH", "/v1/admin/console/settings", f.owner, string(b))
	if w.Code != 200 {
		t.Fatalf("first save %d %s", w.Code, w.Body)
	}
	doc, err = f.deps.Console.Settings(ctx, operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	v = doc.Effective
	v.TrustedProxies = []string{}
	second := operations.SettingsChange{ExpectedRevision: doc.Revision, IdempotencyKey: "second-network-save", Values: v}
	b2, _ := json.Marshal(second)
	w = f.call("PATCH", "/v1/admin/console/settings", f.owner, string(b2))
	if w.Code != 200 {
		t.Fatalf("second save %d %s", w.Code, w.Body)
	}
	probe := func() ConnectionDocument {
		r := httptest.NewRequest("GET", "http://media.example/v1/networking/connection", nil)
		r.RemoteAddr = "203.0.113.9:42"
		r.Header.Set("X-Forwarded-For", "127.0.0.1")
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		var out ConnectionDocument
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 {
			t.Fatalf("probe %d %s %v", w.Code, w.Body, err)
		}
		return out
	}
	if out := probe(); !out.Remote {
		t.Fatalf("removed trust still honoured before replay: %+v", out)
	}
	w = f.call("PATCH", "/v1/admin/console/settings", f.owner, string(b))
	if w.Code != 200 {
		t.Fatalf("replay %d %s", w.Code, w.Body)
	}
	current, _ := f.deps.Console.Settings(ctx, operations.AllowServerScope)
	out := probe()
	fmt.Printf("persisted revision=%d trustedProxies=%v; after replay connection=%+v\n", current.Revision, current.Effective.TrustedProxies, out)
	if !out.Remote || out.ClientAddress != "203.0.113.9" {
		t.Fatal("old receipt reinstated revoked proxy trust in active request policy")
	}
}

func TestConnectionPolicyRejectsOlderSaveCompletion(t *testing.T) {
	cache := &connectionPolicyCache{}
	newer := operations.DefaultSettings()
	newer.LANNetworks = []string{}
	newer.TrustedProxies = []string{}
	cache.set(operations.SettingsDocument{Revision: 3, Effective: newer})
	older := operations.DefaultSettings()
	older.TrustedProxies = []string{"203.0.113.9/32"}
	cache.set(operations.SettingsDocument{Revision: 2, Effective: older})
	live := cache.setting.Load()
	if live.revision != 3 || len(live.locality.lan) != 0 || len(live.locality.proxies) != 0 {
		t.Fatalf("older completion replaced the current policy: %+v", live)
	}
}
