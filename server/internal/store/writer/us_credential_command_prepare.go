package writer

import "fmt"

// Frozen comparison requests only. These wrappers establish no authority and
// cannot issue credentials, enter the writer, or disclose a saved receipt.
type usPasswordReplaceCommand struct {
	identity CommandIdentity
	request  usPasswordReplaceCommandRequest
	digest   RequestDigest
}
type usPasswordReplaceCommandRequest struct {
	ownerID  string
	issuer   usPolicyIssuer
	prepared usPasswordReplacePrepared
}

func prepareUSPasswordReplaceCommand(identity CommandIdentity, owner string, issuer usPolicyIssuer, in usPasswordReplaceRequest) (*usPasswordReplaceCommand, error) {
	if !validUSSecurityIdentity(identity, owner, issuer) {
		return nil, invalid("us_credential_scope")
	}
	prepared, err := prepareUSPasswordReplace(in)
	if err != nil {
		return nil, err
	}
	request := usPasswordReplaceCommandRequest{ownerID: owner, issuer: issuer, prepared: *prepared}
	digest, err := canonicalRequestDigest(usLocalPasswordReplace, 1, identity, request)
	if err != nil {
		return nil, err
	}
	return &usPasswordReplaceCommand{identity: identity, request: request, digest: digest}, nil
}
func (usPasswordReplaceCommand) String() string   { return "usPasswordReplaceCommand{redacted}" }
func (usPasswordReplaceCommand) GoString() string { return "usPasswordReplaceCommand{redacted}" }
func (usPasswordReplaceCommand) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usPasswordReplaceCommand")
}
func (usPasswordReplaceCommandRequest) String() string {
	return "usPasswordReplaceCommandRequest{redacted}"
}
func (usPasswordReplaceCommandRequest) GoString() string {
	return "usPasswordReplaceCommandRequest{redacted}"
}
func (usPasswordReplaceCommandRequest) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usPasswordReplaceCommandRequest")
}

type usCredentialIssueCommand struct {
	identity CommandIdentity
	request  usCredentialIssueCommandRequest
	digest   RequestDigest
}
type usCredentialIssueCommandRequest struct {
	ownerID  string
	issuer   usPolicyIssuer
	prepared usCredentialIssuePrepared
}

func prepareUSCredentialIssueCommand(identity CommandIdentity, owner string, issuer usPolicyIssuer, in usCredentialIssueRequest, nowMS int64) (*usCredentialIssueCommand, error) {
	if !validUSSecurityIdentity(identity, owner, issuer) {
		return nil, invalid("us_credential_scope")
	}
	prepared, err := prepareUSCredentialIssue(in, nowMS)
	if err != nil {
		return nil, err
	}
	request := usCredentialIssueCommandRequest{ownerID: owner, issuer: issuer, prepared: *prepared}
	digest, err := canonicalRequestDigest(usAPICredentialIssue, 1, identity, request)
	if err != nil {
		return nil, err
	}
	return &usCredentialIssueCommand{identity: identity, request: request, digest: digest}, nil
}
func (usCredentialIssueCommand) String() string   { return "usCredentialIssueCommand{redacted}" }
func (usCredentialIssueCommand) GoString() string { return "usCredentialIssueCommand{redacted}" }
func (usCredentialIssueCommand) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usCredentialIssueCommand")
}
func (usCredentialIssueCommandRequest) String() string {
	return "usCredentialIssueCommandRequest{redacted}"
}
func (usCredentialIssueCommandRequest) GoString() string {
	return "usCredentialIssueCommandRequest{redacted}"
}
func (usCredentialIssueCommandRequest) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usCredentialIssueCommandRequest")
}

type usCredentialRevokeCommand struct {
	identity CommandIdentity
	request  usCredentialRevokeCommandRequest
	digest   RequestDigest
}
type usCredentialRevokeCommandRequest struct {
	ownerID  string
	issuer   usPolicyIssuer
	prepared usCredentialRevokePrepared
}

func prepareUSCredentialRevokeCommand(identity CommandIdentity, owner string, issuer usPolicyIssuer, in usCredentialRevokeRequest) (*usCredentialRevokeCommand, error) {
	if !validUSSecurityIdentity(identity, owner, issuer) {
		return nil, invalid("us_credential_scope")
	}
	prepared, err := prepareUSCredentialRevoke(in)
	if err != nil {
		return nil, err
	}
	request := usCredentialRevokeCommandRequest{ownerID: owner, issuer: issuer, prepared: *prepared}
	digest, err := canonicalRequestDigest(usAPICredentialRevoke, 1, identity, request)
	if err != nil {
		return nil, err
	}
	return &usCredentialRevokeCommand{identity: identity, request: request, digest: digest}, nil
}
func (usCredentialRevokeCommand) String() string   { return "usCredentialRevokeCommand{redacted}" }
func (usCredentialRevokeCommand) GoString() string { return "usCredentialRevokeCommand{redacted}" }
func (usCredentialRevokeCommand) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usCredentialRevokeCommand")
}
func (usCredentialRevokeCommandRequest) String() string {
	return "usCredentialRevokeCommandRequest{redacted}"
}
func (usCredentialRevokeCommandRequest) GoString() string {
	return "usCredentialRevokeCommandRequest{redacted}"
}
func (usCredentialRevokeCommandRequest) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usCredentialRevokeCommandRequest")
}
