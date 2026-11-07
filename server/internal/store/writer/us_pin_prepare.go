package writer

import "slices"

// Local draft purposes. Root must compose these FULL descriptors into the
// canonical registry; preparation deliberately has no request identity yet.
const (
	usPINSet   CommandID = 532
	usPINClear CommandID = 533
)

func describeUSPINCommand(id CommandID) (Descriptor, bool) {
	switch id {
	case usPINSet:
		return Descriptor{ID: id, Name: "us_profile_pin_set", Durability: Full}, true
	case usPINClear:
		return Descriptor{ID: id, Name: "us_profile_pin_clear", Durability: Full}, true
	default:
		return Descriptor{}, false
	}
}

// These are target comparison witnesses, never issuer authority. In particular
// no role, main-profile flag or caller-supplied permission grants access.
type usPINRequest struct {
	account, profile, profileLifetime     string
	expectedRevision, expectedPINRevision int64
	hash                                  []byte // already computed under governed admission
}

// Frozen preparation only. Hashing, PIN verification, entropy and IO all belong
// outside writer custody. Root owns canonical identity/digest and proof binding.
type usPINPrepared struct {
	kind    CommandID
	request usPINRequest
}

func prepareUSPIN(kind CommandID, in usPINRequest) (*usPINPrepared, error) {
	if _, ok := describeUSPINCommand(kind); !ok {
		return nil, invalid("us_pin_purpose")
	}
	if !usIdentifier(in.account) || !usIdentifier(in.profile) || !usHex(in.profileLifetime, 32) ||
		in.expectedRevision < 1 || in.expectedRevision >= usMaxRevision ||
		in.expectedPINRevision < 1 || in.expectedPINRevision >= usMaxRevision {
		return nil, invalid("us_pin_target")
	}
	if kind == usPINSet && !usPINHashEnvelope(in.hash) || kind == usPINClear && len(in.hash) != 0 {
		return nil, invalid("us_pin_hash")
	}
	in.hash = slices.Clone(in.hash)
	return &usPINPrepared{kind: kind, request: in}, nil
}

// identity/hash_cost.go requires cost 8 for newly generated profile PIN hashes.
// Accept the supported bcrypt 2a/2b/2y envelopes at exactly that cost, never a
// password-cost hash or arbitrary stored bytes. This checks encoding only; it
// cannot prove which PIN was hashed or that governed hashing took place.
func usPINHashEnvelope(hash []byte) bool {
	if len(hash) != 60 || hash[0] != '$' || hash[1] != '2' ||
		(hash[2] != 'a' && hash[2] != 'b' && hash[2] != 'y') ||
		hash[3] != '$' || hash[4] != '0' || hash[5] != '8' || hash[6] != '$' {
		return false
	}
	const alphabet = "./ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	for n, b := range hash[7:] {
		index := -1
		for i := range len(alphabet) {
			if b == alphabet[i] {
				index = i
				break
			}
		}
		// bcrypt's 16-byte salt and 23-byte digest have canonical unused bits.
		if index < 0 || n == 21 && index&15 != 0 || n == 52 && index&3 != 0 {
			return false
		}
	}
	return true
}
