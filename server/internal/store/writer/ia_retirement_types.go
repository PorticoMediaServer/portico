package writer

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

// These draft values implement IA-SCHEMA-001's original request contract, not
// a production entry or a selected DDL carrier. INT group C owns native storage;
// W still owns current disclosure, one physical turn and terminal COMMIT truth.
// A constructor cannot establish current owner role, a deny fence or retirement.
type iaRetirementScope struct {
	libraryID                               int64
	libraryKey, sourceID, sourceIncarnation string
	sourceGeneration, libraryRevision       int64
	resolvedRoot, rootIdentity              string
}

// Actor values bind the original request but never authenticate the caller.
// Fresh execution must resolve CURRENT conjunctive lifetimes and owner role;
// replay must authorize disclosure before reading the saved historical result.
type iaRetirementActor struct {
	authority, accountID, profileID, membershipID     string
	accountEpoch, membershipRevision, profileRevision int64
}

type iaRetirementRequest struct {
	scope iaRetirementScope
	actor iaRetirementActor
	// Raw confirmation is retained in the request digest. Fresh execution
	// compares TrimSpace(confirmation) with the CURRENT library name under
	// libraryRevision; preparation never substitutes that authority check.
	libraryName, confirmation string
}

type iaRetirementCommand struct {
	purpose  CommandID
	identity CommandIdentity
	request  iaRetirementRequest
	digest   RequestDigest
	valid    bool
}

const iaRetirementRequestVersion uint16 = 1

// newIARetirementCommand freezes a complete BEGIN request. Library retirement
// has no synthetic source witness. Source retirement binds the exact original
// source incarnation/generation; neither namespace nor actor is re-created by
// retry. Identity/incarnation allocation remains the trusted issuer's job.
func newIARetirementCommand(purpose CommandID, identity CommandIdentity, request iaRetirementRequest) (*iaRetirementCommand, error) {
	if purpose != IARetireLibrary && purpose != IARetireSource || identity.Sequence != 1 ||
		identity.Incarnation == ([16]byte{}) || identity.OperationID == ([16]byte{}) {
		return nil, invalid("ia_retirement_identity")
	}
	s, a := request.scope, request.actor
	if s.libraryID <= 0 || s.libraryKey == "" || s.libraryRevision <= 0 || s.resolvedRoot == "" ||
		a.authority == "" || a.accountID == "" || a.profileID == "" || a.membershipID == "" ||
		a.accountEpoch <= 0 || a.membershipRevision <= 0 || a.profileRevision <= 0 {
		return nil, invalid("ia_retirement_scope")
	}
	if purpose == IARetireLibrary && (s.sourceID != "" || s.sourceIncarnation != "" || s.sourceGeneration != 0 || s.rootIdentity != "" || request.libraryName == "" || request.confirmation == "") ||
		purpose == IARetireSource && (s.sourceID == "" || s.sourceIncarnation == "" || s.sourceGeneration <= 0 || s.rootIdentity == "" || request.libraryName != "" || request.confirmation != "") {
		return nil, invalid("ia_retirement_source_variant")
	}
	texts := []*string{&request.scope.libraryKey, &request.scope.sourceID, &request.scope.sourceIncarnation,
		&request.scope.resolvedRoot, &request.scope.rootIdentity, &request.libraryName, &request.confirmation,
		&request.actor.authority, &request.actor.accountID, &request.actor.profileID, &request.actor.membershipID}
	remaining := maxCanonicalRequestBytes
	for _, value := range texts {
		if len(*value) > remaining {
			return nil, &PolicyError{Field: "ia_retirement_bytes", Limit: maxCanonicalRequestBytes, Cause: ErrCap}
		}
		remaining -= len(*value)
	}
	for _, value := range texts {
		if !utf8.ValidString(*value) || strings.IndexByte(*value, 0) >= 0 {
			return nil, invalid("ia_retirement_text")
		}
	}
	digest, err := canonicalRequestDigest(purpose, iaRetirementRequestVersion, identity, request)
	if err != nil {
		return nil, err
	}
	for _, value := range texts {
		*value = strings.Clone(*value)
	}
	return &iaRetirementCommand{purpose: purpose, identity: identity, request: request, digest: digest, valid: true}, nil
}

// Revalidate the immutable original at private entry. This is NOT a current
// authority/source check, nor permission to execute without W's issuer.
func (c *iaRetirementCommand) checkOriginal() error {
	if c == nil || !c.valid {
		return invalid("ia_retirement_command")
	}
	checked, err := newIARetirementCommand(c.purpose, c.identity, c.request)
	if err != nil || !bytes.Equal(checked.digest[:], c.digest[:]) {
		return ErrConflict
	}
	return nil
}
