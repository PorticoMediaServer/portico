package httpapi

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestCredentialUseFlushesWithoutAnotherRequest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	// The database's write gate is process-global; isolate the virtual clock
	// so its channels cannot escape into tests using the real clock.
	if os.Getenv("PORTICO_CREDENTIAL_USE_TEST_CHILD") != "1" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(binary, "-test.run=^TestCredentialUseFlushesWithoutAnotherRequest$", "-test.count=1")
		cmd.Env = append(os.Environ(), "PORTICO_CREDENTIAL_USE_TEST_CHILD=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("idle flush: %v\n%s", err, output)
		}
		return
	}
	synctest.Test(t, func(t *testing.T) {
		f := administrationFixtureNew(t)
		key := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"last-used-idle","name":"Script","scopes":["read"]}`)
		if w := f.call("GET", "/v1/me", key.Secret, ""); w.Code != 200 {
			t.Fatalf("use: %d %s", w.Code, w.Body)
		}
		time.Sleep(credentialUseFlush + time.Second)
		synctest.Wait()
		w := f.call("GET", "/v1/admin/api-keys", f.owner, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"lastUsedAt"`) {
			t.Fatalf("last-used time was not flushed while idle: %d %s", w.Code, w.Body)
		}
	})
}

func TestCredentialUseFlushRetainsFailedBatch(t *testing.T) {
	f := administrationFixtureNew(t)
	key := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"last-used-retry","name":"Script","scopes":["read"]}`)
	gate, store := f.deps.credentials, f.deps.Access.Access
	now := time.Now()
	gate.used(store, key.Credential.ID, now)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := gate.flushUses(cancelled, store); err == nil {
		t.Fatal("cancelled write succeeded")
	}
	gate.used(store, key.Credential.ID, now.Add(time.Second))
	if err := gate.flushUses(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	var used, requests int64
	if err := f.deps.DB.QueryRow(`SELECT last_used_ms,requests FROM api_credential_use WHERE credential_id=?`, key.Credential.ID).Scan(&used, &requests); err != nil {
		t.Fatal(err)
	}
	if requests != 2 || used != now.Add(time.Second).UnixMilli() {
		t.Fatalf("failed batch was lost or counted twice: used=%d requests=%d", used, requests)
	}
}

func TestCredentialUseFlushOnShutdown(t *testing.T) {
	f := administrationFixtureNew(t)
	key := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"last-used-shutdown","name":"Script","scopes":["read"]}`)
	if w := f.call("GET", "/v1/me", key.Secret, ""); w.Code != 200 {
		t.Fatalf("use: %d", w.Code)
	}
	if err := FlushCredentialUse(context.Background(), f.handler); err != nil {
		t.Fatal(err)
	}
	if err := FlushCredentialUse(context.Background(), f.handler); err != nil {
		t.Fatal(err)
	}
	var used, requests int64
	if err := f.deps.DB.QueryRow(`SELECT last_used_ms,requests FROM api_credential_use WHERE credential_id=?`, key.Credential.ID).Scan(&used, &requests); err != nil {
		t.Fatal(err)
	}
	if used <= 0 || requests != 1 {
		t.Fatalf("shutdown lost or duplicated usage: used=%d requests=%d", used, requests)
	}
	f.deps.credentials.mu.Lock()
	defer f.deps.credentials.mu.Unlock()
	if f.deps.credentials.useTimer != nil || !f.deps.credentials.closed {
		t.Fatal("shutdown left the timer running")
	}
}
