package writer

import (
	"database/sql"
	"encoding/json"
	"slices"
)

const usMaxRevision int64 = 9007199254740991

// These are captured comparison witnesses, never authority. W must establish
// current issuer authority in the SAME transaction before receipt disclosure.
// No role string or caller-created scope can grant permission.
type usPolicyIssuer struct {
	server, account, profile, family                 string
	memberIncarnation, profileLifetime, installation string
	tokenDigest                                      string
	epoch, familyGeneration, authorityGeneration     int64
}

type usLibraryPolicyRequest struct {
	ownerID                    string // original retained security receipt owner
	issuer                     usPolicyIssuer
	account, memberIncarnation string
	profile, profileLifetime   string // empty only for member-level purpose
	expectedRevision           int64
	libraries                  []string // profile nil inherits; explicit [] denies
}

// A frozen request is preparation only: it owns no writer, governor grant,
// receipt classification, Applied result or token-issuance capability.
type usLibraryPolicyCommand struct {
	kind     CommandID
	identity CommandIdentity
	request  usLibraryPolicyRequest
	digest   RequestDigest
	policy   sql.NullString // prepared before writer custody, not caller-selected SQL
}

func prepareUSLibraryPolicy(kind CommandID, identity CommandIdentity, in usLibraryPolicyRequest) (*usLibraryPolicyCommand, error) {
	// Check RAW bounds and all scalar witnesses before clone/JSON/digest work.
	if kind != USMemberLibraries && kind != USProfileLibraries {
		return nil, invalid("us_policy_purpose")
	}
	if identity.Incarnation == [16]byte{} || identity.OperationID == [16]byte{} || identity.Sequence <= 0 {
		return nil, invalid("us_policy_identity")
	}
	if !usIdentifier(in.ownerID) || !usIdentifier(in.account) || !usHex(in.memberIncarnation, 32) ||
		in.expectedRevision < 1 || in.expectedRevision >= usMaxRevision {
		return nil, invalid("us_policy_target")
	}
	if kind == USMemberLibraries && (in.profile != "" || in.profileLifetime != "") ||
		kind == USProfileLibraries && (!usIdentifier(in.profile) || !usHex(in.profileLifetime, 32)) {
		return nil, invalid("us_policy_profile")
	}
	i := in.issuer
	if !usIdentifier(i.server) || !usIdentifier(i.account) || !usIdentifier(i.profile) || !usIdentifier(i.family) ||
		!usHex(i.memberIncarnation, 32) || !usHex(i.profileLifetime, 32) || !usHex(i.installation, 32) ||
		!usIdentityDigest(i.tokenDigest) || i.epoch <= 0 || i.familyGeneration <= 0 || i.authorityGeneration <= 0 {
		return nil, invalid("us_policy_issuer")
	}
	if len(in.libraries) > usMaxPolicyLibraries {
		return nil, &PolicyError{Field: "us_policy_libraries", Limit: usMaxPolicyLibraries, Cause: ErrCap}
	}
	// Validate every ID before allocating bounded duplicate-detection state.
	for _, id := range in.libraries {
		if !usIdentifier(id) {
			return nil, invalid("us_policy_library")
		}
	}
	seen := make(map[string]struct{}, len(in.libraries))
	for _, id := range in.libraries {
		if _, duplicate := seen[id]; duplicate {
			return nil, invalid("us_policy_library")
		}
		seen[id] = struct{}{}
	}
	in.libraries = slices.Clone(in.libraries)
	digest, err := canonicalRequestDigest(kind, 1, identity, in)
	if err != nil {
		return nil, err
	}
	command := &usLibraryPolicyCommand{kind: kind, identity: identity, request: in, digest: digest}
	if kind == USProfileLibraries && in.libraries == nil {
		return command, nil
	}
	ids := in.libraries
	if ids == nil {
		ids = []string{}
	}
	data, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	command.policy = sql.NullString{String: string(data), Valid: true}
	return command, nil
}

// Database encoding is a projection of the frozen input. The original nil/empty
// distinction remains in its request digest. Member nil denies; profile nil
// inherits. No profile list can expand the current membership's permissions.
func (c *usLibraryPolicyCommand) policyJSON() (any, error) {
	if c == nil || c.kind != USMemberLibraries && c.kind != USProfileLibraries {
		return nil, ErrInvalid
	}
	if !c.policy.Valid {
		return nil, nil
	}
	return c.policy.String, nil
}

func usHex(v string, size int) bool {
	if len(v) != size {
		return false
	}
	for _, c := range v {
		if !(c >= 'a' && c <= 'f' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
