package metadata

import (
	"context"
	"errors"
	"testing"
)

// Mutating one option's Languages must not change the next call's list: every
// LibraryAgentOptions call builds a fresh copy of ScreenMetadataLanguages.
func TestLibraryAgentOptionsLanguagesAreCopies(t *testing.T) {
	first := LibraryAgentOptions("movie")
	if len(first[0].Languages) != len(ScreenMetadataLanguages) {
		t.Fatalf("movie online languages %d, want %d", len(first[0].Languages), len(ScreenMetadataLanguages))
	}
	first[0].Languages[0] = "xx"
	second := LibraryAgentOptions("movie")
	if second[0].Languages[0] != "en" {
		t.Fatalf("shared language list mutated to %q", second[0].Languages[0])
	}
	if ScreenMetadataLanguages[0] != "en" {
		t.Fatalf("ScreenMetadataLanguages mutated to %q", ScreenMetadataLanguages[0])
	}
}

// A local-only library never reaches a provider: the screen matcher parks its
// targets, artwork from providers is refused and an owner search is answered
// with ErrLocalMetadataOnly. Switching back to the online agent restores
// matching with the kind's default providers.
func TestLocalOnlyLibraryNeverGoesOnline(t *testing.T) {
	s, db, p := screenFixture(t)
	item := publicationFixtureMovie(t, db)
	ctx := context.Background()
	agent, err := s.LibraryAgent(ctx, "lib")
	if err != nil || agent.Agent != AgentOnline || len(agent.Agents) != 2 || agent.Agents[1].ID != AgentLocal {
		t.Fatal("default agent", agent, err)
	}
	if _, err = s.SetLibraryAgent(ctx, "lib", agent.Revision+1, AgentLocal, nil); !errors.Is(err, ErrLibraryAgentConflict) {
		t.Fatal("stale revision accepted", err)
	}
	if _, err = s.SetLibraryAgent(ctx, "lib", agent.Revision, "somewhere", nil); !errors.Is(err, ErrLibraryAgentInput) {
		t.Fatal("unknown agent accepted", err)
	}
	if agent, err = s.SetLibraryAgent(ctx, "lib", agent.Revision, AgentLocal, nil); err != nil || agent.Agent != AgentLocal {
		t.Fatal("switch to local", agent, err)
	}
	settleScreenFanout(t, s)
	if err = s.ScreenStep(ctx); err != nil {
		t.Fatal(err)
	}
	if p.calls != 0 || publicationScalar(t, db, `SELECT status FROM screen_metadata_work WHERE target_id=?`, item.ID) != "provider_disabled" {
		t.Fatal("local-only library reached a provider", p.calls)
	}
	policy, err := s.ScreenPolicy(ctx, "lib")
	if err != nil || policy.Agent != AgentLocal || policy.Status != "disabled" {
		t.Fatal("screen policy under local-only", policy.Agent, policy.Status, err)
	}
	state, err := s.ScreenState(ctx, "item", item.Public)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SearchScreen(ctx, "item", item.Public, ScreenSearch{ExpectedRevision: state.Revision, Provider: "tmdb", Query: "Film"}, nil); !errors.Is(err, ErrLocalMetadataOnly) {
		t.Fatal("owner search on a local-only library", err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := s.artworkProviderEnabled(ctx, tx, RepairTarget{Kind: "item", ID: item.Public}, "tmdb")
	tx.Rollback()
	if err != nil || enabled {
		t.Fatal("provider artwork allowed for a local-only library", enabled, err)
	}
	if agent, err = s.SetLibraryAgent(ctx, "lib", agent.Revision, AgentOnline, nil); err != nil || agent.Agent != AgentOnline {
		t.Fatal("switch back online", agent, err)
	}
	settleScreenFanout(t, s)
	if err = s.ScreenStep(ctx); err != nil {
		t.Fatal(err)
	}
	if p.calls == 0 {
		t.Fatal("online agent did not resume matching")
	}
	// The older per-library switch and the agent stay one decision.
	policy, _ = s.ScreenPolicy(ctx, "lib")
	if err = s.UpdateScreenPolicy(ctx, "lib", ScreenPolicyUpdate{ExpectedRevision: policy.Revision, ExpectedConsentRevision: policy.ConsentRevision, DisclosureVersion: ScreenDisclosureVersion, Enabled: false, Providers: policy.Providers, Language: "en-US", RefreshMode: "replace_unlocked"}, nil); err != nil {
		t.Fatal(err)
	}
	if agent, _ = s.LibraryAgent(ctx, "lib"); agent.Agent != AgentLocal {
		t.Fatal("turning the library's lookups off did not select local-only", agent.Agent)
	}
}
