package writer

import "slices"

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
