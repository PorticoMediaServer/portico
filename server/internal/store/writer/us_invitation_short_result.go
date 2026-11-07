package writer

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math"
	"slices"
)

// Complete immutable 553 response/issuance evidence. Public retry omits Code;
// private replay still verifies this whole saved card without mutable source.
type usInvitationShortSavedResult struct {
	kind                              CommandID
	version                           int
	ownerID, issuerServer             string
	appliedMS                         int64
	invitationID                      string
	invitationRevision                int64
	codeHash, role                    string
	invitationExpiryMS                int64
	serverID                          string
	publicKey                         []byte
	keyIncarnation                    string
	resetGeneration                   int64
	signInKind                        string
	kindRevision, issuedMS, expiresMS int64
	lookupHash, bindingHash           string
	wire                              usInvitationShortWire
}

func (usInvitationShortSavedResult) String() string { return "usInvitationShortSavedResult{redacted}" }
func (usInvitationShortSavedResult) GoString() string {
	return "usInvitationShortSavedResult{redacted}"
}
func (usInvitationShortSavedResult) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usInvitationShortSavedResult")
}

func cloneUSInvitationShortResult(v usInvitationShortSavedResult) usInvitationShortSavedResult {
	v.publicKey = slices.Clone(v.publicKey)
	v.wire.nonce, v.wire.body, v.wire.aad = slices.Clone(v.wire.nonce), slices.Clone(v.wire.body), slices.Clone(v.wire.aad)
	return v
}
func (v usInvitationShortSavedResult) manifest() usInvitationShortAAD {
	return usInvitationShortAAD{purpose: "portico-us-invitation-short-display-aad-v1", ownerID: v.ownerID, issuerServer: v.issuerServer, invitationID: v.invitationID, revision: v.invitationRevision, codeHash: v.codeHash, role: v.role, invitationExpiryMS: v.invitationExpiryMS, serverID: v.serverID, publicKey: v.publicKey, keyIncarnation: v.keyIncarnation, resetGeneration: v.resetGeneration, signInKind: v.signInKind, kindRevision: v.kindRevision, issuedMS: v.issuedMS, expiresMS: v.expiresMS, lookupHash: v.lookupHash, bindingHash: v.bindingHash, keyID: v.wire.keyID}
}
func validateUSInvitationShortResult(v usInvitationShortSavedResult) error {
	if v.kind != usInvitationShortIssue || v.version != 1 || !usIdentifier(v.ownerID) || !usIdentifier(v.issuerServer) || !usIdentifier(v.invitationID) || v.invitationRevision < 1 || !usHex(v.codeHash, 64) || v.role != "member" && v.role != "admin" || v.issuedMS <= 0 || v.issuedMS > math.MaxInt64-600000 || v.invitationExpiryMS <= v.issuedMS || v.expiresMS != min(v.invitationExpiryMS, v.issuedMS+600000) || v.appliedMS < v.issuedMS || v.appliedMS >= v.expiresMS || len(v.publicKey) != 32 || !usIdentifier(v.keyIncarnation) || v.resetGeneration < 0 || v.signInKind != "local" && v.signInKind != "portico" || v.kindRevision < 0 || v.signInKind == "local" && v.kindRevision == 0 || !usHex(v.lookupHash, 64) || !usHex(v.bindingHash, 64) || !usIdentifier(v.wire.keyID) || len(v.wire.nonce) != 12 || len(v.wire.body) != 35 || len(v.wire.aad) != 32 {
		return ErrResolveRequired
	}
	h := sha256.New()
	_, _ = h.Write([]byte("portico.server.identity.v1\x00"))
	_, _ = h.Write(v.publicKey)
	if v.serverID != "srv_"+base64.RawURLEncoding.EncodeToString(h.Sum(nil)) {
		return ErrResolveRequired
	}
	return nil
}
func usInvitationShortResultDigest(identity CommandIdentity, request RequestDigest, v usInvitationShortSavedResult) (ResultDigest, error) {
	if identity.Incarnation == [16]byte{} || identity.OperationID == [16]byte{} || identity.Sequence <= 0 || request == (RequestDigest{}) {
		return ResultDigest{}, ErrResolveRequired
	}
	if err := validateUSInvitationShortResult(v); err != nil {
		return ResultDigest{}, err
	}
	aad, err := canonicalRequestDigest(usInvitationShortIssue, 1, identity, v.manifest())
	if err != nil || !bytes.Equal(aad[:], v.wire.aad) {
		return ResultDigest{}, ErrResolveRequired
	}
	e := requestEncoder{h: sha256.New()}
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("users_security")
	e.text("invitation_short_issue_v1")
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
	e.text(v.invitationID)
	e.number(uint64(v.invitationRevision))
	e.text(v.codeHash)
	e.text(v.role)
	e.number(uint64(v.invitationExpiryMS))
	e.text(v.serverID)
	e.bytes(v.publicKey)
	e.text(v.keyIncarnation)
	e.number(uint64(v.resetGeneration))
	e.text(v.signInKind)
	e.number(uint64(v.kindRevision))
	e.number(uint64(v.issuedMS))
	e.number(uint64(v.expiresMS))
	e.text(v.lookupHash)
	e.text(v.bindingHash)
	e.text(v.wire.keyID)
	e.bytes(v.wire.nonce)
	e.bytes(v.wire.body)
	e.bytes(v.wire.aad)
	if e.err != nil {
		return ResultDigest{}, e.err
	}
	var digest ResultDigest
	copy(digest[:], e.h.Sum(nil))
	return digest, nil
}

// Exact logical native variable columns: header owner+96+codec; child owner+16,
// typed TEXT/BLOB fields. issuerServer is already stored/charged in its owner.
func usInvitationShortResultVariableBytes(v usInvitationShortSavedResult) (int64, error) {
	if err := validateUSInvitationShortResult(v); err != nil {
		return 0, err
	}
	n := len(v.ownerID) + 96 + len("invitation_short_issue_v1") + len(v.ownerID) + 16 + len(v.invitationID) + len(v.codeHash) + len(v.role) + len(v.serverID) + len(v.publicKey) + len(v.keyIncarnation) + len(v.signInKind) + len(v.lookupHash) + len(v.bindingHash) + len(v.wire.keyID) + len(v.wire.nonce) + len(v.wire.body) + len(v.wire.aad)
	return int64(n), nil
}
