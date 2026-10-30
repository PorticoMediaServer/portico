package identity

import (
	"context"
	"database/sql"
	"encoding/base64"
	"strings"
)

// LocalLinkServerIDTx projects the current protected-key publication for a
// local invitation, move-code or member-reset link. The caller owns its existing
// authorization/issuance transaction. This is an expected identity for the link
// consumer, never identity proof or authority to admit an account.
func LocalLinkServerIDTx(ctx context.Context, tx *sql.Tx) (string, error) {
	if tx == nil {
		return "", ErrDirectInput
	}
	kind, e := SignInKindTx(ctx, tx)
	if e != nil || kind != SignInLocal {
		return "", e
	}
	var id string
	// StageIdentityTx is the sole publication writer. Follow its current pointer,
	// rather than a configuration copy, historical identity or caller-supplied ID.
	e = tx.QueryRowContext(ctx, `SELECT i.server_id FROM networking_claim_identity i JOIN networking_server_identities k ON k.server_id=i.server_id AND k.public_key=i.public_key WHERE i.singleton=1`).Scan(&id)
	if e != nil {
		return "", e
	}
	if len(id) != 47 || !strings.HasPrefix(id, "srv_") {
		return "", ErrUnauthorized
	}
	for _, c := range id[4:] {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return "", ErrUnauthorized
		}
	}
	raw, e := base64.RawURLEncoding.Strict().DecodeString(id[4:])
	if e != nil || len(raw) != 32 {
		return "", ErrUnauthorized
	}
	return id, nil
}
