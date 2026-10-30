package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// Settings loads the legacy header and typed configuration through one retained
// ConsoleClient. Both envelopes must describe the same original viewer.
func TestSettingsConfigurationSharesLegacyConsoleViewerFence(t *testing.T) {
	f := administrationFixtureNew(t)
	// Explicitly designated representative request class for this test;
	// the generic issued tokens do not establish a device platform.
	const deviceClass = "web"
	const preferencesPath = "/v1/preferences?deviceClass=" + deviceClass
	readScope := func(path, token string) ConsoleReplyScope {
		t.Helper()
		response := f.call("GET", path, token, "")
		if response.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body)
		}
		var envelope ConsoleReply[json.RawMessage]
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Scope.ServerID != f.deps.Identity.ID() || len(envelope.Scope.ViewerFence) != 64 {
			t.Fatalf("%s omitted the selected viewer scope", path)
		}
		return envelope.Scope
	}
	want := readScope(preferencesPath, f.owner)
	for _, path := range []string{
		"/v1/console/registry",
		"/v1/admin/console/settings",
		"/v1/admin/settings/configuration",
	} {
		if got := readScope(path, f.owner); got != want {
			t.Fatalf("%s changed the console viewer between responses", path)
		}
	}
	// A different valid credential for the same viewer must not mint a new
	// presentation identity. Each route still performs its normal authorization.
	successor, err := f.deps.Identity.Issue("owner", "owner-profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := readScope("/v1/admin/settings/configuration", successor.AccessToken); got != want {
		t.Fatal("a same-viewer credential changed the configuration fence")
	}
	if got := readScope(preferencesPath, f.member); got == want {
		t.Fatal("another viewer inherited the owner's console fence")
	}
}

func TestSettingsConfigurationOwnerReadAndCommittedRuntimeRefresh(t *testing.T) {
	f := administrationFixtureNew(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "portico.toml")
	if err := os.WriteFile(path, []byte("[streaming]\nmusic_away_quality='96'\nallow_conversion='sound'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.deps.Console.InitializeConfiguration(ctx, path); err != nil {
		t.Fatal(err)
	}
	adapter := &RegistryDeliverySettings{Console: f.deps.Console}
	adapter.Refresh(ctx)
	calls := 0
	f.deps.SettingsApplied = func(ctx context.Context) { calls++; adapter.Refresh(ctx) }
	f.handler = New(f.deps)
	for _, token := range []string{"", f.admin, f.member} {
		response := f.call("GET", "/v1/admin/settings/configuration", token, "")
		if response.Code != http.StatusUnauthorized && response.Code != http.StatusForbidden {
			t.Fatalf("non-owner configuration read: %d %s", response.Code, response.Body)
		}
	}
	response := f.call("GET", "/v1/admin/settings/configuration", f.owner, "")
	if response.Code != http.StatusOK {
		t.Fatalf("configuration read: %d %s", response.Code, response.Body)
	}
	var envelope ConsoleReply[operations.SettingsConfiguration]
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, field := range envelope.Data.Fields {
		if field.ID == "streaming.musicAwayQuality" {
			found = field.Source == "file" && field.Current != nil && *field.Current == "96" && field.Default != nil && *field.Default == "original"
		}
	}
	if !found || envelope.Data.Path != path {
		t.Fatal("configuration omitted real source/default", envelope.Data)
	}
	if err := os.WriteFile(path, []byte("[streaming]\nmusic_away_quality='320'\nallow_conversion='never'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.deps.Console.ConfigFile.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := f.deps.Console.ImportConfiguration(ctx); err != nil {
		t.Fatal(err)
	}
	if current := adapter.Delivery(); calls != 1 || current.MaxMusicQuality != "320" || current.ConversionMode != "never" {
		t.Fatal("file edit did not refresh current delivery policy", calls, current)
	}
	doc, err := f.deps.Console.Settings(ctx, operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	values := doc.Requested
	values.MusicAwayQuality, values.ConversionMode = "original", "all"
	body, err := json.Marshal(operations.SettingsChange{ExpectedRevision: doc.Revision, IdempotencyKey: "configuration-runtime-refresh", Values: values})
	if err != nil {
		t.Fatal(err)
	}
	response = f.call("PATCH", "/v1/admin/console/settings", f.owner, string(body))
	if response.Code != http.StatusOK {
		t.Fatalf("settings save: %d %s", response.Code, response.Body)
	}
	if current := adapter.Delivery(); calls != 2 || current.MaxMusicQuality != "original" || current.ConversionMode != "all" {
		t.Fatal("owner save did not refresh current delivery exactly once", calls, current)
	}
}

func TestSettingsConfigurationRestrictionClassificationAndOwnerMainProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	const pattern = "GET /v1/admin/settings/configuration"
	route, ok := restrictionRoutes()[pattern]
	if !ok || route.probe == nil || route.reason != "" {
		t.Fatal("configuration needs a genuine restricted-viewer probe, not an admin exemption")
	}
	m := buildRestrictionMatrix(t)
	paths := route.probe(m)
	if len(paths) != 1 || paths[0] != "/v1/admin/settings/configuration" {
		t.Fatal("configuration probe missed its actual route", paths)
	}
	m.check(t, pattern, "restricted", "GET", paths[0], "")
	if code, body := m.call("GET", paths[0], "", "restricted"); code != 403 {
		t.Fatalf("valid restricted viewer: %d %s", code, body)
	}
	f := administrationFixtureNew(t)
	ctx := context.Background()
	if err := f.deps.Console.InitializeConfiguration(ctx, filepath.Join(t.TempDir(), "portico.toml")); err != nil {
		t.Fatal(err)
	}
	if response := f.call("GET", paths[0], f.owner, ""); response.Code != 200 {
		t.Fatal("current owner main profile refused", response.Code, response.Body.String())
	}
	profiles, err := f.deps.Identity.CreateDirectProfile(ctx, f.owner, "Secondary owner", "blue")
	if err != nil {
		t.Fatal(err)
	}
	var child string
	for _, profile := range profiles.Profiles {
		if !profile.Primary && profile.Name == "Secondary owner" {
			child = profile.ID
		}
	}
	if child == "" {
		t.Fatal("no genuine secondary owner profile")
	}
	selected, err := f.deps.Identity.SelectDirectProfile(ctx, f.owner, child, identity.DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	if response := f.call("GET", paths[0], selected.Session.AccessToken, ""); response.Code != 403 {
		t.Fatal("secondary owner profile read server configuration", response.Code, response.Body.String())
	}
}
