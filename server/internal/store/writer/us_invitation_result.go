package writer

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/securitypolicy"
)

// usInvitationSavedResult is the COMPLETE immutable native result of one
// invitation create (550), edit (551) or revoke (552). It captures the product
// Invitation DTO fields and the ordered library cohort. For 550 only, the
// separately sealed exact Code artifact is retained in the receipt header;
// replay reads it to prove the original digest but never projects it again.
type usInvitationSavedResult struct {
	kind           CommandID
	ownerID        string
	id             string
	serverID       string
	notes          sqlNullString
	role           string
	state          string
	createdMS      int64
	expiresMS      int64
	acceptedMS     sqlNullInt64
	accountID      sqlNullString
	revision       int64
	boundUsername  string
	access         usInvitationAccess // mode/streams/remote/tokens/rating
	sourceCodeHash string
	libraries      []string
	appliedMS      int64
	code           usInvitationResultCode
}

// Aliases keep the result file free of database/sql imports at every use site
// while preserving NULL versus present-empty semantics exactly.
type sqlNullString struct {
	String string
	Valid  bool
}

type sqlNullInt64 struct {
	Int64 int64
	Valid bool
}

// usInvitationResultCode is the separately prepared AES-GCM invitation Code:
// key id, nonce and sealed body. It never holds the plaintext capability.
type usInvitationResultCode struct {
	keyID *string
	nonce []byte
	body  []byte
}

func (c usInvitationResultCode) present() bool {
	return c.keyID != nil || c.nonce != nil || c.body != nil
}

// usInvitationPublicResult is the exact product Invitation DTO carried to the
// first response. Code is copied only from the fresh dispatch's first-response
// projection, never from a replayed receipt.
type usInvitationPublicResult struct {
	ServerID         string                   `json:"serverId,omitempty"`
	ID               string                   `json:"id"`
	Notes            *string                  `json:"notes,omitempty"`
	Role             string                   `json:"role"`
	AllowedLibraries []string                 `json:"allowedLibraries"`
	State            string                   `json:"state"`
	CreatedAt        string                   `json:"createdAt"`
	ExpiresAt        string                   `json:"expiresAt"`
	AcceptedAt       string                   `json:"acceptedAt,omitempty"`
	AccountID        string                   `json:"accountId,omitempty"`
	Revision         int64                    `json:"revision"`
	PorticoUsername  string                   `json:"porticoUsername,omitempty"`
	Access           usInvitationPublicAccess `json:"access"`
	Code             string                   `json:"code,omitempty"`
}

type usInvitationPublicAccess struct {
	MaxStreamsMode   string `json:"maxStreamsMode"`
	MaxStreams       int    `json:"maxStreams"`
	AllowRemote      bool   `json:"allowRemote"`
	AccessTokens     bool   `json:"accessTokens"`
	MaxContentRating string `json:"maxContentRating"`
}

func usInvitationResultCodec(kind CommandID) (string, error) {
	switch kind {
	case usInvitationCreate:
		return "invitation_create_v1", nil
	case usInvitationEdit:
		return "invitation_edit_v1", nil
	case usInvitationRevoke:
		return "invitation_revoke_v1", nil
	}
	return "", ErrInvalid
}

func validateUSInvitationResult(v usInvitationSavedResult) error {
	if _, err := usInvitationResultCodec(v.kind); err != nil {
		return err
	}
	if !usIdentifier(v.ownerID) || !usIdentifier(v.id) || !usIdentifier(v.serverID) || !usHex(v.sourceCodeHash, 64) ||
		(v.role != "member" && v.role != "admin") || v.createdMS < 0 || v.expiresMS < 0 || v.revision < 1 ||
		v.revision >= usMaxRevision || v.appliedMS < 0 || len(v.boundUsername) > 64 || !utf8.ValidString(v.boundUsername) {
		return ErrResolveRequired
	}
	if v.notes.Valid && (!safeUSInvitationText(v.notes.String, 500) || v.notes.String == "") {
		return ErrResolveRequired
	}
	// The three admitted purposes never produce an accepted result: acceptance is
	// a later command family and would otherwise need its own retained image.
	if v.acceptedMS.Valid || v.accountID.Valid {
		return ErrResolveRequired
	}
	switch v.kind {
	case usInvitationCreate:
		if v.state != "pending" || v.revision != 1 {
			return ErrResolveRequired
		}
	case usInvitationEdit:
		if v.state != "pending" {
			return ErrResolveRequired
		}
	case usInvitationRevoke:
		if v.state != "revoked" {
			return ErrResolveRequired
		}
	}
	if err := validateUSInvitationResultAccess(v.access); err != nil {
		return err
	}
	if v.libraries == nil || len(v.libraries) > usInvitationMaxLibraries {
		return ErrResolveRequired
	}
	seen := make(map[string]struct{}, len(v.libraries))
	for _, id := range v.libraries {
		if !usIdentifier(id) {
			return ErrResolveRequired
		}
		if _, exists := seen[id]; exists {
			return ErrResolveRequired
		}
		seen[id] = struct{}{}
	}
	if v.kind == usInvitationCreate {
		if v.code.keyID == nil || len(*v.code.keyID) < 1 || len(*v.code.keyID) > 128 ||
			len(v.code.nonce) != 12 || len(v.code.body) != usInvitationLegacyCodeLen+16 {
			return ErrResolveRequired
		}
	} else if v.code.present() {
		return ErrResolveRequired
	}
	return nil
}

func validateUSInvitationResultAccess(a usInvitationAccess) error {
	switch a.mode {
	case "inherit", "unlimited":
		if a.streams != 0 {
			return ErrResolveRequired
		}
	case "limit":
		if !securitypolicy.ValidStreamMode(a.mode, a.streams) {
			return ErrResolveRequired
		}
	default:
		return ErrResolveRequired
	}
	if a.rating != strings.ToUpper(a.rating) || len(a.rating) > 32 || !utf8.ValidString(a.rating) {
		return ErrResolveRequired
	}
	if a.rating != "" {
		if _, ok := securitypolicy.RatingAge(a.rating); !ok {
			return ErrResolveRequired
		}
	}
	return nil
}

// usInvitationResultDigest is the closed v1 result codec. It binds the original
// identity/request, every public DTO field, the ordered library cohort and the
// separately sealed Code artifact. It never reads current source.
func usInvitationResultDigest(identity CommandIdentity, request RequestDigest, v usInvitationSavedResult) (ResultDigest, error) {
	if identity.Incarnation == [16]byte{} || identity.OperationID == [16]byte{} || identity.Sequence <= 0 || request == (RequestDigest{}) {
		return ResultDigest{}, ErrResolveRequired
	}
	if err := validateUSInvitationResult(v); err != nil {
		return ResultDigest{}, err
	}
	codec, _ := usInvitationResultCodec(v.kind)
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
	for _, s := range []string{v.ownerID, v.id, v.serverID} {
		usNativeResultText(&e, s)
	}
	e.flag(v.notes.Valid)
	if v.notes.Valid {
		usNativeResultText(&e, v.notes.String)
	}
	usNativeResultText(&e, v.role)
	usNativeResultText(&e, v.state)
	e.number(uint64(v.createdMS))
	e.number(uint64(v.expiresMS))
	e.flag(v.acceptedMS.Valid)
	if v.acceptedMS.Valid {
		e.number(uint64(v.acceptedMS.Int64))
	}
	e.flag(v.accountID.Valid)
	if v.accountID.Valid {
		usNativeResultText(&e, v.accountID.String)
	}
	e.number(uint64(v.revision))
	e.number(uint64(v.appliedMS))
	usNativeResultText(&e, v.boundUsername)
	usNativeResultText(&e, v.access.mode)
	e.number(uint64(v.access.streams))
	e.flag(v.access.allowRemote)
	e.flag(v.access.accessTokens)
	usNativeResultText(&e, v.access.rating)
	usNativeResultText(&e, v.sourceCodeHash)
	e.flag(v.libraries != nil)
	e.number(uint64(len(v.libraries)))
	for _, id := range v.libraries {
		usNativeResultText(&e, id)
	}
	e.flag(v.code.keyID != nil)
	if v.code.keyID != nil {
		usNativeResultText(&e, *v.code.keyID)
	}
	for _, b := range [][]byte{v.code.nonce, v.code.body} {
		e.flag(b != nil)
		e.number(uint64(len(b)))
		e.bytes(b)
	}
	if e.err != nil {
		return ResultDigest{}, e.err
	}
	var out ResultDigest
	copy(out[:], e.h.Sum(nil))
	return out, nil
}

func cloneUSInvitationCode(v usInvitationResultCode) usInvitationResultCode {
	out := usInvitationResultCode{}
	if v.keyID != nil {
		s := strings.Clone(*v.keyID)
		out.keyID = &s
	}
	out.nonce = append([]byte(nil), v.nonce...)
	out.body = append([]byte(nil), v.body...)
	return out
}

// usInvitationResultVariableBytes is exactly the sum of the three generated
// variable_bytes columns: receipt header, typed invitation result and each
// ordered library child. It is a logical charge, not index/page/WAL bytes.
func usInvitationResultVariableBytes(v usInvitationSavedResult) (int64, error) {
	if err := validateUSInvitationResult(v); err != nil {
		return 0, err
	}
	codec, _ := usInvitationResultCodec(v.kind)
	header := len(v.ownerID) + 16 + 16 + 32 + 32 + len(codec)
	if v.code.keyID != nil {
		header += len(*v.code.keyID)
	}
	header += len(v.code.nonce) + len(v.code.body)
	typed := len(v.ownerID) + 16 + len(v.id) + len(v.serverID) + len(v.role) + len(v.state) + len(v.boundUsername) +
		len(v.access.mode) + len(v.access.rating) + len(v.sourceCodeHash)
	if v.notes.Valid {
		typed += len(v.notes.String)
	}
	if v.accountID.Valid {
		typed += len(v.accountID.String)
	}
	total := int64(header + typed)
	for _, id := range v.libraries {
		total += int64(len(v.ownerID) + 16 + len(usInvitationInvitationCohort) + len(id))
	}
	return total, nil
}

// usInvitationPublic builds the product DTO. code is empty for every replay; a
// fresh create supplies the sealed artifact separately, never this value.
func (v usInvitationSavedResult) usInvitationPublic(code string) usInvitationPublicResult {
	out := usInvitationPublicResult{
		ServerID: v.serverID, ID: v.id, Role: v.role, State: v.state,
		CreatedAt: time.UnixMilli(v.createdMS).UTC().Format(time.RFC3339),
		ExpiresAt: time.UnixMilli(v.expiresMS).UTC().Format(time.RFC3339),
		Revision:  v.revision, PorticoUsername: v.boundUsername,
		Access: usInvitationPublicAccess{MaxStreamsMode: v.access.mode, MaxStreams: v.access.streams,
			AllowRemote: v.access.allowRemote, AccessTokens: v.access.accessTokens, MaxContentRating: v.access.rating},
		Code: code,
	}
	if v.notes.Valid {
		note := v.notes.String
		out.Notes = &note
	}
	if v.acceptedMS.Valid {
		out.AcceptedAt = time.UnixMilli(v.acceptedMS.Int64).UTC().Format(time.RFC3339)
	}
	if v.accountID.Valid {
		out.AccountID = v.accountID.String
	}
	out.AllowedLibraries = append([]string(nil), v.libraries...)
	if out.AllowedLibraries == nil {
		out.AllowedLibraries = []string{}
	}
	return out
}

func (usInvitationSavedResult) String() string   { return "usInvitationSavedResult{redacted}" }
func (usInvitationSavedResult) GoString() string { return "usInvitationSavedResult{redacted}" }
func (usInvitationSavedResult) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usInvitationSavedResult")
}

func (usInvitationResultCode) String() string   { return "usInvitationResultCode{redacted}" }
func (usInvitationResultCode) GoString() string { return "usInvitationResultCode{redacted}" }
func (usInvitationResultCode) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usInvitationResultCode")
}
