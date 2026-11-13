package metadata

import (
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

type availabilityProvider struct{ Provider }

func (availabilityProvider) Name() string { return "tvdb" }

func TestLibraryProviderAvailabilityFollowsRegistration(t *testing.T) {
	s := &Service{}
	available := func(name string) bool {
		return slices.ContainsFunc(s.LibraryProviders(db.LibraryShows), func(p LibraryMetadataProvider) bool { return p.ID == name && p.Available })
	}
	if !available("local") || available("tvdb") {
		t.Fatal("only local is available before registration")
	}
	s.Use(availabilityProvider{})
	if !available("tvdb") {
		t.Fatal("registered TheTVDB is unavailable")
	}
	s.mu.Lock()
	delete(s.Providers, "tvdb") // Clearing the owner's credential removes its registration.
	s.mu.Unlock()
	if available("tvdb") || !available("local") {
		t.Fatal("availability was kept after the credential was removed")
	}
}
