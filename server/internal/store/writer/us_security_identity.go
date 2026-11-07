package writer

// Comparison witnesses only; no role, permissions, writer or admission authority.
func validUSSecurityIdentity(identity CommandIdentity, owner string, i usPolicyIssuer) bool {
	return identity.Incarnation != [16]byte{} && identity.OperationID != [16]byte{} && identity.Sequence > 0 && usIdentifier(owner) &&
		usIdentifier(i.server) && usIdentifier(i.account) && usIdentifier(i.profile) && usIdentifier(i.family) &&
		usHex(i.memberIncarnation, 32) && usHex(i.profileLifetime, 32) && usHex(i.installation, 32) && usIdentityDigest(i.tokenDigest) &&
		i.epoch > 0 && i.familyGeneration > 0 && i.authorityGeneration > 0
}
