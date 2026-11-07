package writer

// Draft purposes only. Root must add these explicit FULL descriptors to its
// closed US registration; this helper does not register an accepting command.
const (
	usMemberRoleChange    CommandID = 501
	usMembershipDisable   CommandID = 502
	usAccountEpochAdvance CommandID = 503
)

func describeUSMemberCommand(id CommandID) (Descriptor, bool) {
	switch id {
	case usMemberRoleChange:
		return Descriptor{ID: id, Name: "us_member_role_change", Durability: Full}, true
	case usMembershipDisable:
		return Descriptor{ID: id, Name: "us_membership_disable", Durability: Full}, true
	case usAccountEpochAdvance:
		return Descriptor{ID: id, Name: "us_account_epoch_advance", Durability: Full}, true
	default:
		return Descriptor{}, false
	}
}

// Target comparisons only, never issuer authority. Membership incarnation is
// the source lifetime; account epoch and membership revision are independent
// CAS witnesses. No caller role, permission, bearer or family is accepted.
type usMemberTarget struct {
	account, incarnation            string
	expectedRevision, expectedEpoch int64
}

type usMemberRequest struct {
	target usMemberTarget
	role   string // desired TARGET tier, never the caller's tier
}

// All inputs are immutable scalar values. These preparations own no opener,
// admission grant, command identity/digest, receipt or authority capability.
type usMemberPrepared struct {
	kind    CommandID
	request usMemberRequest
}

type usAccountEpochPrepared struct{ target usMemberTarget }

func validUSMemberTarget(in usMemberTarget) bool {
	return usIdentifier(in.account) && usHex(in.incarnation, 32) &&
		in.expectedRevision >= 1 && in.expectedRevision <= usMaxRevision &&
		in.expectedEpoch >= 1 && in.expectedEpoch < usMaxRevision
}

func prepareUSMember(kind CommandID, in usMemberRequest) (*usMemberPrepared, error) {
	if kind != usMemberRoleChange && kind != usMembershipDisable {
		return nil, invalid("us_member_purpose")
	}
	if !validUSMemberTarget(in.target) || in.target.expectedRevision == usMaxRevision {
		return nil, invalid("us_member_target")
	}
	if kind == usMemberRoleChange && in.role != "admin" && in.role != "member" ||
		kind == usMembershipDisable && in.role != "" {
		return nil, invalid("us_member_role")
	}
	return &usMemberPrepared{kind: kind, request: in}, nil
}

func prepareUSAccountEpoch(in usMemberTarget) (*usAccountEpochPrepared, error) {
	if !validUSMemberTarget(in) {
		return nil, invalid("us_account_epoch_target")
	}
	return &usAccountEpochPrepared{target: in}, nil
}
