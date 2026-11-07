package writer

import "fmt"

// These immutable requests retain every original comparison witness. Fresh
// authority remains separate and must precede any receipt disclosure.
type usMemberCommand struct {
	identity CommandIdentity
	request  usMemberCommandRequest
	digest   RequestDigest
}
type usMemberCommandRequest struct {
	ownerID  string
	issuer   usPolicyIssuer
	prepared usMemberPrepared
}
type usAccountEpochCommand struct {
	identity CommandIdentity
	request  usAccountEpochCommandRequest
	digest   RequestDigest
}
type usAccountEpochCommandRequest struct {
	ownerID  string
	issuer   usPolicyIssuer
	prepared usAccountEpochPrepared
}

func prepareUSMemberCommand(kind CommandID, identity CommandIdentity, owner string, issuer usPolicyIssuer, in usMemberRequest) (*usMemberCommand, error) {
	if !validUSSecurityIdentity(identity, owner, issuer) {
		return nil, invalid("us_member_scope")
	}
	p, err := prepareUSMember(kind, in)
	if err != nil {
		return nil, err
	}
	request := usMemberCommandRequest{ownerID: owner, issuer: issuer, prepared: *p}
	digest, err := canonicalRequestDigest(kind, 1, identity, request)
	if err != nil {
		return nil, err
	}
	return &usMemberCommand{identity: identity, request: request, digest: digest}, nil
}

func prepareUSAccountEpochCommand(identity CommandIdentity, owner string, issuer usPolicyIssuer, in usMemberTarget) (*usAccountEpochCommand, error) {
	if !validUSSecurityIdentity(identity, owner, issuer) {
		return nil, invalid("us_account_epoch_scope")
	}
	p, err := prepareUSAccountEpoch(in)
	if err != nil {
		return nil, err
	}
	request := usAccountEpochCommandRequest{ownerID: owner, issuer: issuer, prepared: *p}
	digest, err := canonicalRequestDigest(usAccountEpochAdvance, 1, identity, request)
	if err != nil {
		return nil, err
	}
	return &usAccountEpochCommand{identity: identity, request: request, digest: digest}, nil
}

func (usMemberCommand) String() string             { return "usMemberCommand{redacted}" }
func (usMemberCommand) GoString() string           { return "usMemberCommand{redacted}" }
func (usMemberCommand) Format(s fmt.State, _ rune) { formatUSPreparation(s, "usMemberCommand") }
func (usMemberCommandRequest) String() string      { return "usMemberCommandRequest{redacted}" }
func (usMemberCommandRequest) GoString() string    { return "usMemberCommandRequest{redacted}" }
func (usMemberCommandRequest) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usMemberCommandRequest")
}
func (usAccountEpochCommand) String() string   { return "usAccountEpochCommand{redacted}" }
func (usAccountEpochCommand) GoString() string { return "usAccountEpochCommand{redacted}" }
func (usAccountEpochCommand) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usAccountEpochCommand")
}
func (usAccountEpochCommandRequest) String() string { return "usAccountEpochCommandRequest{redacted}" }
func (usAccountEpochCommandRequest) GoString() string {
	return "usAccountEpochCommandRequest{redacted}"
}
func (usAccountEpochCommandRequest) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usAccountEpochCommandRequest")
}
