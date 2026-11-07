package writer

import (
	"fmt"
	"math"
)

// Native typed policy mirrors identity/sign_in_policy.go without importing the
// legacy identity package (and its SQLite function registrations). Completeness
// of source JSON, including an explicit false boolean, is an outer adapter duty.
type usSignInAttemptPolicy string
type usSignInTwoStepRequirement string
type usSignInStayDays int
type usSignInPINRememberDays int
type usSignInPasswordMinimum int
type usSignInMemberResetMinutes int
type usSignInEndedSessionsDays int

type usSignInPolicyValues struct {
	StaySignedInDays       usSignInStayDays
	PINRememberDays        usSignInPINRememberDays
	PasswordAttempts       usSignInAttemptPolicy
	PINAttempts            usSignInAttemptPolicy
	PasswordMinLength      usSignInPasswordMinimum
	MemberResetCodeMinutes usSignInMemberResetMinutes
	RequireTwoStep         usSignInTwoStepRequirement
	RememberAccounts       bool
	EndedSessionsDays      usSignInEndedSessionsDays
}

func defaultUSSignInPolicy() usSignInPolicyValues {
	return usSignInPolicyValues{90, 30, "standard", "standard", 8, 60, "nobody", true, 90}
}
func (v usSignInPolicyValues) valid() bool {
	attempt := func(p usSignInAttemptPolicy) bool { return p == "relaxed" || p == "standard" || p == "strict" }
	return (v.StaySignedInDays == 7 || v.StaySignedInDays == 30 || v.StaySignedInDays == 90 || v.StaySignedInDays == 365) &&
		(v.PINRememberDays == 0 || v.PINRememberDays == 7 || v.PINRememberDays == 30 || v.PINRememberDays == 90) &&
		attempt(v.PasswordAttempts) && attempt(v.PINAttempts) &&
		(v.PasswordMinLength == 8 || v.PasswordMinLength == 10 || v.PasswordMinLength == 12 || v.PasswordMinLength == 16) &&
		(v.MemberResetCodeMinutes == 15 || v.MemberResetCodeMinutes == 60 || v.MemberResetCodeMinutes == 1440) &&
		(v.RequireTwoStep == "nobody" || v.RequireTwoStep == "administrators" || v.RequireTwoStep == "everyone") &&
		(v.EndedSessionsDays == 30 || v.EndedSessionsDays == 90 || v.EndedSessionsDays == 365)
}

// A captured singleton comparison witness, not current authorization.
type usSignInPolicySnapshot struct {
	Revision int64
	Values   usSignInPolicyValues
}
type usSignInPolicyRequest struct {
	identity         CommandIdentity
	ownerID          string
	issuer           usPolicyIssuer
	original         usSignInPolicySnapshot
	expectedRevision int64
	values           usSignInPolicyValues
}

// Preparation only: no receipt/result authority, writer or effect capability.
// Parent composition must prove current owner/main-profile authority. It must
// also adopt the F-family remember_until UPDATE and all-browser-account DELETE
// semantics of ReplaceSignInPolicyTx before offering an equivalent native effect.
type usSignInPolicyCommand struct {
	request usSignInPolicyRequest
	digest  RequestDigest
}

func prepareUSSignInPolicy(in *usSignInPolicyRequest) (*usSignInPolicyCommand, error) {
	if in == nil || !validUSSecurityIdentity(in.identity, in.ownerID, in.issuer) ||
		in.expectedRevision < 1 || in.expectedRevision == math.MaxInt64 ||
		in.original.Revision != in.expectedRevision || !in.original.Values.valid() || !in.values.valid() {
		return nil, invalid("us_sign_in_policy")
	}
	// All fields are native scalar values: this copy freezes the complete input,
	// including OLD transition witnesses and false-valued RememberAccounts.
	r := *in
	d, err := canonicalRequestDigest(usSignInPolicyReplace, 1, r.identity, r)
	if err != nil {
		return nil, err
	}
	return &usSignInPolicyCommand{request: r, digest: d}, nil
}
func (c *usSignInPolicyCommand) checkedDigest() (RequestDigest, error) {
	if c == nil {
		return RequestDigest{}, invalid("us_sign_in_policy_nil")
	}
	d, err := canonicalRequestDigest(usSignInPolicyReplace, 1, c.request.identity, c.request)
	if err != nil {
		return RequestDigest{}, err
	}
	if d != c.digest {
		return RequestDigest{}, invalid("us_sign_in_policy_changed")
	}
	return d, nil
}
func (usSignInPolicyValues) String() string     { return "usSignInPolicyValues{redacted}" }
func (v usSignInPolicyValues) GoString() string { return v.String() }
func (usSignInPolicyValues) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usSignInPolicyValues")
}
func (usSignInPolicySnapshot) String() string     { return "usSignInPolicySnapshot{redacted}" }
func (v usSignInPolicySnapshot) GoString() string { return v.String() }
func (usSignInPolicySnapshot) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usSignInPolicySnapshot")
}
func (usSignInPolicyRequest) String() string     { return "usSignInPolicyRequest{redacted}" }
func (v usSignInPolicyRequest) GoString() string { return v.String() }
func (usSignInPolicyRequest) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usSignInPolicyRequest")
}
func (usSignInPolicyCommand) String() string     { return "usSignInPolicyCommand{redacted}" }
func (v usSignInPolicyCommand) GoString() string { return v.String() }
func (usSignInPolicyCommand) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usSignInPolicyCommand")
}
