package operations

import (
	"context"
	"database/sql"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// WireSetupRemote gives setup its writer for Remote access and Remote
// security (O-169). cmd/server and every test server that runs setup use it.
func WireSetupRemote(id *identity.Service) {
	id.SetupRemoteTx = func(ctx context.Context, tx *sql.Tx, access, security string) error {
		return SetupRemoteChoicesTx(ctx, tx, access, security, time.Now())
	}
}
