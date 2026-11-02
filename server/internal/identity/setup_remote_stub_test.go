package identity

import (
	"context"
	"database/sql"
)

// stubSetupRemote stands in for the console store's writer (operations
// imports identity, so these tests can't wire the real one; httpapi's tests do).
func stubSetupRemote(s *Service) {
	s.SetupRemoteTx = func(context.Context, *sql.Tx, string, string, string) error { return nil }
}
