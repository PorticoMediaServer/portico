package writer

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/securitypolicy"
)

// Immutable native Invitation DTO facts, not current authority. Parent compares
// the creation server with current server identity before saving; edit/revoke
// retain the existing DTO's empty serverId. Code is creation-only protected wire.
// Cohort ordinals are slice positions; count is the saved header result_count.
// Nullable native facts remain nullable even where the public DTO omits them.
type usInvitationSavedResult struct {
	kind                  CommandID
	version, count        int
	ownerID, id, serverID string
	issuerServer          string // original immutable owner.server_id; distinct from link DTO serverId
	notes                 sql.NullString
	role, state           string
	createdMS, expiresMS  int64
	acceptedMS            sql.NullInt64
	accountID             sql.NullString
	revision              int64
	boundUsername         string
	access                usInvitationAccess
	codeHash              string
	libraries             []string
	appliedMS             int64
	code                  usInvitationProtectedCode
}

func (usInvitationSavedResult) String() string   { return "usInvitationSavedResult{redacted}" }
func (usInvitationSavedResult) GoString() string { return "usInvitationSavedResult{redacted}" }
func (usInvitationSavedResult) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usInvitationSavedResult")
}

func usInvitationCodec(kind CommandID) (string, error) {
	switch kind {
	case usInvitationCreate:
		return "invitation_create_v1", nil
	case usInvitationEdit:
		return "invitation_edit_v1", nil
	case usInvitationRevoke:
		return "invitation_revoke_v1", nil
	default:
		return "", ErrInvalid
	}
}

func validateUSInvitationResult(v usInvitationSavedResult) error {
	if _, err := usInvitationCodec(v.kind); err != nil {
		return err
	}
	if v.version != 1 || !usIdentifier(v.ownerID) || !usIdentifier(v.issuerServer) || !usIdentifier(v.id) || !usHex(v.codeHash, 64) || v.count != len(v.libraries) || v.count > usInvitationMaxLibraries || v.libraries == nil || v.createdMS < 0 || v.expiresMS <= v.createdMS || v.appliedMS < v.createdMS || v.revision < 1 || v.revision > usMaxRevision {
		return ErrResolveRequired
	}
	// These three purposes cannot accept an invitation. Null payloads must be
	// canonical so inaccessible sql.Null* backing values cannot escape binding.
	if v.acceptedMS.Valid || v.acceptedMS.Int64 != 0 || v.accountID.Valid || v.accountID.String != "" || !v.notes.Valid && v.notes.String != "" {
		return ErrResolveRequired
	}
	if v.notes.Valid && !safeUSInvitationText(v.notes.String, 500) {
		return ErrResolveRequired
	}
	if v.role != "member" && v.role != "admin" || !safeUSInvitationText(v.boundUsername, 64) || len(v.boundUsername) > 64 || strings.ToLower(strings.TrimSpace(v.boundUsername)) != v.boundUsername || strings.ContainsAny(v.boundUsername, " \t\r\n") {
		return ErrResolveRequired
	}
	if !securitypolicy.ValidStreamMode(v.access.mode, v.access.streams) || len(v.access.rating) > 32 || strings.ToUpper(strings.TrimSpace(v.access.rating)) != v.access.rating {
		return ErrResolveRequired
	}
	if _, ok := securitypolicy.RatingAge(v.access.rating); v.access.rating != "" && !ok {
		return ErrResolveRequired
	}
	seen := make(map[string]bool, len(v.libraries))
	for _, id := range v.libraries {
		if !usIdentifier(id) || seen[id] {
			return ErrResolveRequired
		}
		seen[id] = true
	}
	if v.kind == usInvitationCreate {
		if v.serverID != "" && !usIdentifier(v.serverID) || v.state != "pending" || v.revision != 1 || v.createdMS != v.appliedMS || v.expiresMS-v.createdMS > usInvitationMaxSpanMS || v.expiresMS-v.createdMS < usInvitationHourMS || (v.expiresMS-v.createdMS)%usInvitationHourMS != 0 || !usIdentifier(v.code.keyID) || len(v.code.nonce) != 12 || len(v.code.body) != 59 {
			return ErrResolveRequired
		}
	} else {
		if v.serverID != "" || v.revision < 2 || v.code.keyID != "" || v.code.nonce != nil || v.code.body != nil || v.code.manifest != [32]byte{} {
			return ErrResolveRequired
		}
		if v.kind == usInvitationEdit && (v.state != "pending" || v.expiresMS <= v.appliedMS || v.expiresMS-v.appliedMS > usInvitationMaxSpanMS) || v.kind == usInvitationRevoke && v.state != "revoked" {
			return ErrResolveRequired
		}
	}
	return nil
}

// Closed W LP/integer encoding. No source reads, JSON, decryption or grants.
// Manifest is checked from saved facts, not separately persisted or trusted.
func usInvitationResultDigest(identity CommandIdentity, request RequestDigest, v usInvitationSavedResult) (ResultDigest, error) {
	if identity.Incarnation == [16]byte{} || identity.OperationID == [16]byte{} || identity.Sequence <= 0 || request == (RequestDigest{}) {
		return ResultDigest{}, ErrResolveRequired
	}
	if err := validateUSInvitationResult(v); err != nil {
		return ResultDigest{}, err
	}
	if v.kind == usInvitationCreate && v.code.manifest != usInvitationCodeAAD(identity, v.ownerID, v.issuerServer, v.id, v.codeHash, v.code.keyID) {
		return ResultDigest{}, ErrResolveRequired
	}
	codec, _ := usInvitationCodec(v.kind)
	e := requestEncoder{h: sha256.New()}
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("users_security")
	e.text(codec)
	e.text("applied")
	e.bytes(identity.Incarnation[:])
	e.number(uint64(identity.Sequence))
	e.bytes(identity.OperationID[:])
	e.bytes(request[:])
	e.number(uint64(v.kind))
	e.number(uint64(v.version))
	e.text(v.ownerID)
	e.text(v.issuerServer)
	e.number(uint64(v.appliedMS))
	e.text(v.id)
	e.text(v.serverID)
	e.flag(v.notes.Valid)
	if v.notes.Valid {
		e.text(v.notes.String)
	}
	e.text(v.role)
	e.text(v.state)
	e.number(uint64(v.createdMS))
	e.number(uint64(v.expiresMS))
	e.flag(v.acceptedMS.Valid)
	if v.acceptedMS.Valid {
		e.number(uint64(v.acceptedMS.Int64))
	}
	e.flag(v.accountID.Valid)
	if v.accountID.Valid {
		e.text(v.accountID.String)
	}
	e.number(uint64(v.revision))
	e.text(v.boundUsername)
	e.text(v.access.mode)
	e.number(uint64(v.access.streams))
	e.flag(v.access.allowRemote)
	e.flag(v.access.accessTokens)
	e.text(v.access.rating)
	e.text(v.codeHash)
	e.flag(v.libraries != nil)
	e.number(uint64(v.count))
	for ordinal, id := range v.libraries {
		e.number(uint64(ordinal))
		e.text(id)
	}
	e.flag(v.kind == usInvitationCreate)
	if v.kind == usInvitationCreate {
		e.text(v.code.keyID)
		e.bytes(v.code.nonce)
		e.bytes(v.code.body)
	}
	if e.err != nil {
		return ResultDigest{}, e.err
	}
	var out ResultDigest
	copy(out[:], e.h.Sum(nil))
	return out, nil
}

// Logical native variable columns only, not physical pages/index/WAL or quota:
// header = owner + 16 incarnation + 16 operation + 32 request + 32 result +
// codec + keyID + nonce + body; child repeats owner + 16 incarnation, then
// every TEXT column (nullable NULL charges zero). Each library repeats owner +
// 16 incarnation + len("invitation") + library. Integers charge no variable bytes.
func usInvitationResultVariableBytes(v usInvitationSavedResult) (int64, error) {
	if err := validateUSInvitationResult(v); err != nil {
		return 0, err
	}
	codec, _ := usInvitationCodec(v.kind)
	n := len(v.ownerID) + 96 + len(codec) + len(v.code.keyID) + len(v.code.nonce) + len(v.code.body)
	n += len(v.ownerID) + 16 + len(v.id) + len(v.serverID) + len(v.notes.String) + len(v.role) + len(v.state) + len(v.accountID.String) + len(v.boundUsername) + len(v.access.mode) + len(v.access.rating) + len(v.codeHash)
	for _, id := range v.libraries {
		n += len(v.ownerID) + 16 + len("invitation") + len(id)
	}
	return int64(n), nil
}
