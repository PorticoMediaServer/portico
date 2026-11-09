package writer

import "math"

// MG-18 accepting commands 845/846. The leaf effects and statements keep the
// native singleton CAS (1419/1425); this file only binds one bounded public
// administration request and its canonical digest to a command identity and the
// closed NORMAL purpose. The request never carries an observed snapshot, a root,
// a caller lifetime or a boolean that could stand in for authority.
const mgDVRPolicyRequestVersion uint16 = 1

// The caller names the revision it observed. An absent singleton is not
// created here, a stale revision is a conflict and identical desired values are
// still an explicit new revision, never a silent no-op receipt.
type mgDVRDefaultsPolicyRequest struct {
	expected int64
	input    mgDVRDefaultsInput
}
type mgDVRStoragePolicyRequest struct {
	expected int64
	policy   mgDVRStorageDesired
}

func (r mgDVRDefaultsPolicyRequest) validate() error {
	if r.expected < 1 || r.expected == math.MaxInt64 {
		return ErrInvalid
	}
	// Freeze is deterministic and performs no effect. Validating it here keeps a
	// malformed public input out of the receipt namespace entirely.
	if _, err := freezeMGDVRDefaults(r.input); err != nil {
		return err
	}
	return nil
}
func (r mgDVRStoragePolicyRequest) validate() error {
	if r.expected < 1 || r.expected == math.MaxInt64 {
		return ErrInvalid
	}
	if _, err := freezeMGDVRStorage(r.policy); err != nil {
		return err
	}
	return nil
}
func (r mgDVRDefaultsPolicyRequest) digest(id CommandIdentity) (RequestDigest, error) {
	if !mgPersonalIdentityValid(id) {
		return RequestDigest{}, ErrInvalid
	}
	if err := r.validate(); err != nil {
		return RequestDigest{}, err
	}
	return canonicalRequestDigest(MGSetDVRDefaults, mgDVRPolicyRequestVersion, id, r)
}
func (r mgDVRStoragePolicyRequest) digest(id CommandIdentity) (RequestDigest, error) {
	if !mgPersonalIdentityValid(id) {
		return RequestDigest{}, ErrInvalid
	}
	if err := r.validate(); err != nil {
		return RequestDigest{}, err
	}
	return canonicalRequestDigest(MGSetDVRStoragePolicy, mgDVRPolicyRequestVersion, id, r)
}

// Purpose and durability bind the private policy effect to the closed command it
// serves. A FULL, foreign or closed command cannot borrow these NORMAL commands,
// and a caller-supplied purpose number selects nothing.
func mgDVRPolicyPurpose(t *commandTurn, purpose CommandID) error {
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != purpose ||
		t.transaction.descriptor.Durability != Normal || !t.transaction.policyVerified ||
		!t.transaction.begun || t.transaction.done {
		return ErrInvalid
	}
	switch purpose {
	case MGSetDVRDefaults, MGSetDVRStoragePolicy:
		return nil
	}
	return ErrInvalid
}

// One accepting outcome. decision is the receipt classification; only Replay
// carries the saved result and only Fresh carries a newly persisted one. Retired
// is a complete zero result, never a reconstructed current policy.
type mgDVRPolicyAcceptance struct {
	decision receiptDecision
	policy   mgDVRPolicyResult
	digest   ResultDigest
}
