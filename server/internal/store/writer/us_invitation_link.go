package writer

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Owned comparison facts from the original preparation, not publication or
// private-key proof. Link serverId is distinct from the original owner scope.
type usInvitationLinkBinding struct {
	mode                     string
	revision                 int64
	serverID, keyIncarnation string
	publicKey                [32]byte
	resetGeneration          int64
}

func (usInvitationLinkBinding) String() string   { return "usInvitationLinkBinding{redacted}" }
func (usInvitationLinkBinding) GoString() string { return "usInvitationLinkBinding{redacted}" }
func (usInvitationLinkBinding) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usInvitationLinkBinding")
}

// Same domain-separated namespace binding as networking.ServerIdentity. This
// runs outside writer custody and neither opens nor imports the legacy service.
func prepareUSInvitationLink(mode string, revision int64, serverID string, publicKey []byte, incarnation string, reset int64) (usInvitationLinkBinding, error) {
	out := usInvitationLinkBinding{mode: mode, revision: revision}
	if revision < 1 {
		return usInvitationLinkBinding{}, ErrInvalid
	}
	switch mode {
	case "portico":
		if serverID != "" || len(publicKey) != 0 || incarnation != "" || reset != 0 {
			return usInvitationLinkBinding{}, ErrInvalid
		}
	case "local":
		if len(publicKey) != 32 || !usIdentifier(incarnation) || reset < 0 {
			return usInvitationLinkBinding{}, ErrInvalid
		}
		h := sha256.New()
		_, _ = h.Write([]byte("portico.server.identity.v1\x00"))
		_, _ = h.Write(publicKey)
		if serverID != "srv_"+base64.RawURLEncoding.EncodeToString(h.Sum(nil)) {
			return usInvitationLinkBinding{}, ErrInvalid
		}
		out.serverID = serverID
		out.keyIncarnation = incarnation
		copy(out.publicKey[:], publicKey)
		out.resetGeneration = reset
	default:
		return usInvitationLinkBinding{}, ErrInvalid
	}
	return out, nil
}

const (
	usInvitationLinkKind stmt.ID = "us.invitation.link.kind"
	usInvitationLinkKey  stmt.ID = "us.invitation.link.key"
)

func usInvitationLinkDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	defs := []stmt.Definition{
		{ID: usInvitationLinkKind, SQL: `SELECT 1 FROM identity_state WHERE singleton=1 AND sign_in_kind=? AND revision=?`, Params: []stmt.Param{text("kind"), integer("revision")}, Cohorts: []stmt.Cohort{{Name: "kind", Bindings: []any{"local", int64(1)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"identity_state"}, Seeks: []string{"rowid=?"}}},
		{ID: usInvitationLinkKey, SQL: `SELECT 1 FROM networking_claim_identity AS i CROSS JOIN networking_server_identities AS k INDEXED BY sqlite_autoindex_networking_server_identities_1 ON k.server_id=i.server_id AND k.public_key=i.public_key WHERE i.singleton=1 AND i.server_id=? AND i.public_key=? AND k.key_incarnation=? AND i.reset_generation=?`, Params: []stmt.Param{text("server"), {Name: "public_key", Type: stmt.Blob}, text("incarnation"), integer("generation")}, Cohorts: []stmt.Cohort{{Name: "key", Bindings: []any{"srv_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", make([]byte, 32), "key-incarnation", int64(0)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"i"}, Indexes: []string{"sqlite_autoindex_networking_server_identities_1"}, Seeks: []string{"rowid=?", "server_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "networking_server_identities", Name: "sqlite_autoindex_networking_server_identities_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "server_id", Collation: "BINARY"}}}}}},
	}
	for j := range defs {
		defs[j].Kind = stmt.Read
		defs[j].Owner = "store/writer US invitation link binding"
		defs[j].CallSites = []string{"store/writer/us_invitation_link.go"}
	}
	return defs
}
func checkUSInvitationFreshLink(ctx context.Context, turn *commandTurn, p usInvitationLinkBinding) error {
	// Only fixed numeric outcomes leave SQLite; all text/blob comparisons use
	// frozen owned witnesses. A staged key is a link expectation, never installed
	// Hosted/route success. Current owner authority is independently required.
	if err := usAuthorityPresence(ctx, turn, usInvitationLinkKind, true, p.mode, p.revision); err != nil {
		return err
	}
	if p.mode == "portico" {
		return nil
	}
	if p.mode != "local" {
		return ErrInvalid
	}
	return usAuthorityPresence(ctx, turn, usInvitationLinkKey, true, p.serverID, p.publicKey[:], p.keyIncarnation, p.resetGeneration)
}
