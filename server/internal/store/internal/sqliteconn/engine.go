package sqliteconn

import (
	"context"
	"database/sql"
	"errors"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

// EngineIdentity is values-only native evidence. Keep it bound to the acquired
// physical connection's lifetime; never reuse a startup/Capture sample for a
// later or replacement connection. Search-policy interpretation is not owned
// by the connection helper.
type EngineIdentity struct{ Version, SourceID string }

// AttestEngine reads the actual library pair without SQL, preparation, policy
// mutation or a connection acquisition. Config/Settings JSON is unchanged.
// Assert remains a separate prerequisite, not implied by this observation.
func AttestEngine(ctx context.Context, conn *sql.Conn) (EngineIdentity, error) {
	if ctx == nil || conn == nil {
		return EngineIdentity{}, ErrPolicy
	}
	if err := ctx.Err(); err != nil {
		return EngineIdentity{}, err
	}
	version, sourceID, err := sqlite.Engine(conn)
	if err = errors.Join(err, ctx.Err()); err != nil {
		return EngineIdentity{}, err
	}
	if version == "" || sourceID == "" || !utf8.ValidString(version) || !utf8.ValidString(sourceID) {
		return EngineIdentity{}, ErrPolicy
	}
	return EngineIdentity{Version: version, SourceID: sourceID}, nil
}
