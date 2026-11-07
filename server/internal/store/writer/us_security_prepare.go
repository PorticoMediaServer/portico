package writer

import "slices"

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

// The complete prepared payload includes all original scope/target/revision,
// normalized policy, optional access-preservation state and generated code-hash
// witnesses. Cached SQL projections are also bound, never a parallel authority.
type usInvitationCommand struct {
	request usInvitationPrepared
	digest  RequestDigest
}

func freezeUSInvitation(prepared *usInvitationPrepared) (*usInvitationCommand, error) {
	if prepared == nil || !validUSInvitationScope(prepared.identity, prepared.scope) {
		return nil, invalid("us_invitation_scope")
	}
	if _, ok := describeUSInvitationCommand(prepared.kind); !ok {
		return nil, ErrInvalid
	}
	request := *prepared
	request.libraries = slices.Clone(prepared.libraries)
	digest, err := canonicalRequestDigest(request.kind, 1, request.identity, request)
	if err != nil {
		return nil, err
	}
	return &usInvitationCommand{request: request, digest: digest}, nil
}
func validUSSecurityIdentity(identity CommandIdentity, owner string, i usPolicyIssuer) bool {
	return identity.Incarnation != [16]byte{} && identity.OperationID != [16]byte{} && identity.Sequence > 0 && usIdentifier(owner) &&
		usIdentifier(i.server) && usIdentifier(i.account) && usIdentifier(i.profile) && usIdentifier(i.family) &&
		usHex(i.memberIncarnation, 32) && usHex(i.profileLifetime, 32) && usHex(i.installation, 32) && usHex(i.tokenDigest, 64) &&
		i.epoch > 0 && i.familyGeneration > 0 && i.authorityGeneration > 0
}
