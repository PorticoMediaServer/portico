package httpapi

import "testing"

func TestOperationsCredentialScopesAndRestart(t *testing.T) {
	f := administrationFixtureNew(t)
	restarts := 0
	f.deps.Restart = func() { restarts++ }
	f.handler = New(f.deps)
	read := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"ops-read-key","name":"Read","scopes":["read"]}`)
	admin := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"ops-admin-key","name":"Operations","scopes":["admin"]}`)
	for _, route := range []struct{ method, path, body string }{
		{"POST", "/v1/admin/restart", `{}`},
		{"POST", "/v1/admin/backups/restore", `{}`},
		{"GET", "/v1/admin/diagnostics/client-logs", ""},
		{"GET", "/v1/admin/diagnostics/client-logs/report/download", ""},
		{"DELETE", "/v1/admin/diagnostics/client-logs/report", ""},
	} {
		w := f.call(route.method, route.path, read.Secret, route.body)
		if w.Code != 403 || registryErrorCode(t, w) != "insufficient_scope" {
			t.Fatalf("read key reached %s %s: %d %s", route.method, route.path, w.Code, w.Body)
		}
	}
	if restarts != 0 {
		t.Fatal("a refused request restarted the server")
	}
	// A report is the member's own: a key that may act for them sends one,
	// one that may only read does not.
	if w := f.call("POST", "/v1/diagnostics/client-logs", read.Secret, `{"body":"trace"}`); w.Code != 403 || registryErrorCode(t, w) != "insufficient_scope" {
		t.Fatalf("a read key uploaded a report: %d %s", w.Code, w.Body)
	}
	if w := f.call("POST", "/v1/diagnostics/client-logs", admin.Secret, `{"body":"trace"}`); w.Code >= 300 {
		t.Fatalf("an admin key could not upload a report: %d %s", w.Code, w.Body)
	}
	if w := f.call("GET", "/v1/admin/diagnostics/client-logs", admin.Secret, ""); w.Code != 200 {
		t.Fatalf("admin key cannot list reports: %d %s", w.Code, w.Body)
	}
	for _, bearer := range []string{admin.Secret, f.owner} {
		if w := f.call("POST", "/v1/admin/restart", bearer, `{}`); w.Code != 202 {
			t.Fatalf("authorized restart failed: %d %s", w.Code, w.Body)
		}
	}
	if restarts != 2 {
		t.Fatalf("restart callbacks: %d", restarts)
	}
	if w := f.call("POST", "/v1/admin/restart", f.member, `{}`); w.Code != 403 || restarts != 2 {
		t.Fatalf("member restarted the server: %d %s", w.Code, w.Body)
	}
}
