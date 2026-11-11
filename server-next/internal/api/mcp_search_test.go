package api

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

// An assistant finds titles by name through the server's own search, as its key, so a key
// acting as a limited profile does not find what that profile may not see (area 13 rule 34).
func TestAssistantFindsTitles(t *testing.T) {
	th := newTheatre(t)
	th.s.db.StartSearch(filepath.Join(t.TempDir(), "search.db"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := th.s.db.SearchCaughtUp(context.Background()); err != nil {
		t.Fatal(err)
	}
	on := true
	status(t, th.s, "PUT", "/v1/admin/settings/integrations", th.owner.AccessToken, IntegrationSettingsEdit{Assistants: &on})
	var owner KeyCreated
	call(t, th.s, "POST", "/v1/me/tokens", th.owner.AccessToken, KeyCreate{Name: "Look", Scopes: []string{"read"}}, &owner)
	if text, failed := toolCall(t, th.s, owner.Secret, "search_titles", map[string]any{"query": "heat"}); failed || !strings.Contains(text, th.film) {
		t.Errorf("searching for the film found %s", text)
	}
	if text, failed := toolCall(t, th.s, owner.Secret, "search_titles", map[string]any{"query": "heat", "kind": "movie"}); failed || !strings.Contains(text, th.film) {
		t.Errorf("searching films for the film found %s", text)
	}
	if text, failed := toolCall(t, th.s, kidsKey(t, th), "search_titles", map[string]any{"query": "heat"}); failed || strings.Contains(text, th.film) || strings.Contains(text, "Heat") {
		t.Errorf("a limited profile's assistant found a film it may not see: %s", text)
	}
}
