package httpapi

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// Automatic home networks follow the host's attached interfaces, so a fixture
// that poses a peer as a home client has to say which network is home. This
// saves the same owner policy a real installation would save; locality itself
// is still decided only by the server from the saved networks.
func declareHomeNetworks(t *testing.T, db *sql.DB, networks ...string) {
	t.Helper()
	ctx := context.Background()
	console := operations.New(db)
	document, err := console.RuntimeSettings(ctx, operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	values := document.Requested
	values.LANNetworks = append([]string{"127.0.0.0/8", "::1/128"}, networks...)
	key := "fixture-home-networks-" + strings.NewReplacer("/", "-", ":", "-").Replace(strings.Join(networks, "-"))
	if _, err = console.ApplySettings(ctx, identity.Principal{}, operations.AllowServerScope, operations.SettingsChange{
		ExpectedRevision: document.Revision,
		IdempotencyKey:   key,
		Values:           values,
	}); err != nil {
		t.Fatal(err)
	}
}

// v1HomeNetworks declares the fixture's home networks and rebuilds the router,
// because building the fixture already served a setup request, which reads the
// request-entry policy eagerly. The rebuilt router reads the saved document on
// its first request instead of the host's interfaces.
func v1HomeNetworks(t *testing.T, f *v1Fixture, networks ...string) {
	t.Helper()
	declareHomeNetworks(t, f.db, networks...)
	f.deps.Console = operations.New(f.db)
	f.handler = New(f.deps)
	f.deps.v1 = f.v1
}
