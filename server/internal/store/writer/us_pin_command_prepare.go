package writer

// Complete frozen request wrappers only. These cannot authenticate an issuer,
// issue a credential, enter a writer, declare Applied or mint an admission grant.
type usPINCommand struct {
	identity CommandIdentity
	request  usPINCommandRequest
	digest   RequestDigest
}
type usPINCommandRequest struct {
	ownerID           string
	issuer            usPolicyIssuer
	memberIncarnation string
	prepared          usPINPrepared
}

func prepareUSPINCommand(kind CommandID, identity CommandIdentity, owner string, issuer usPolicyIssuer, memberIncarnation string, in usPINRequest) (*usPINCommand, error) {
	if !validUSSecurityIdentity(identity, owner, issuer) || !usHex(memberIncarnation, 32) {
		return nil, invalid("us_pin_scope")
	}
	prepared, err := prepareUSPIN(kind, in)
	if err != nil {
		return nil, err
	}
	request := usPINCommandRequest{ownerID: owner, issuer: issuer, memberIncarnation: memberIncarnation, prepared: *prepared}
	digest, err := canonicalRequestDigest(kind, 1, identity, request)
	if err != nil {
		return nil, err
	}
	return &usPINCommand{identity: identity, request: request, digest: digest}, nil
}
