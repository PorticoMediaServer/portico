package writer

import "fmt"

// Frozen comparison witnesses only. Current authority must be established in
// the writer transaction BEFORE receipt disclosure; this grants no admission.
type usProfileCommand struct {
	identity CommandIdentity
	request  usProfileCommandRequest
	digest   RequestDigest
}
type usProfileCommandRequest struct {
	ownerID  string
	issuer   usPolicyIssuer
	prepared usProfilePrepared
}

func prepareUSProfileCreateCommand(identity CommandIdentity, owner string, issuer usPolicyIssuer, member usMemberTarget, id, lifetime, name, art string) (*usProfileCommand, error) {
	if !validUSSecurityIdentity(identity, owner, issuer) {
		return nil, invalid("us_profile_scope")
	}
	p, err := prepareUSProfileCreate(member, id, lifetime, name, art)
	if err != nil {
		return nil, err
	}
	return freezeUSProfileCommand(identity, owner, issuer, *p)
}

func prepareUSProfileMetadataCommand(identity CommandIdentity, owner string, issuer usPolicyIssuer, target usProfileTarget, name, art *string) (*usProfileCommand, error) {
	if !validUSSecurityIdentity(identity, owner, issuer) {
		return nil, invalid("us_profile_scope")
	}
	p, err := prepareUSProfileMetadata(target, name, art)
	if err != nil {
		return nil, err
	}
	return freezeUSProfileCommand(identity, owner, issuer, *p)
}

func freezeUSProfileCommand(identity CommandIdentity, owner string, issuer usPolicyIssuer, p usProfilePrepared) (*usProfileCommand, error) {
	if !validUSSecurityIdentity(identity, owner, issuer) {
		return nil, invalid("us_profile_scope")
	}
	// Revalidate even package-local forged preparations, including unused fields.
	var name, art *string
	if p.setName {
		name = &p.name
	} else if p.name != "" {
		return nil, invalid("name")
	}
	if p.setArt {
		art = &p.art
	} else if p.art != "" {
		return nil, invalid("art")
	}
	checked, err := prepareUSProfileMetadata(p.target, name, art)
	if err != nil {
		return nil, err
	}
	if p.kind == usProfileCreate {
		if !p.setName || !p.setArt || p.target.revision != 1 {
			return nil, invalid("us_profile_create")
		}
		checked.kind = usProfileCreate
	} else if p.kind != usProfileMetadataEdit {
		return nil, invalid("us_profile_purpose")
	}
	if *checked != p {
		return nil, invalid("us_profile_prepared")
	}
	request := usProfileCommandRequest{ownerID: owner, issuer: issuer, prepared: *checked}
	digest, err := canonicalRequestDigest(p.kind, 1, identity, request)
	if err != nil {
		return nil, err
	}
	return &usProfileCommand{identity: identity, request: request, digest: digest}, nil
}

func (usProfileCommand) String() string             { return "usProfileCommand{redacted}" }
func (usProfileCommand) GoString() string           { return "usProfileCommand{redacted}" }
func (usProfileCommand) Format(s fmt.State, _ rune) { formatUSPreparation(s, "usProfileCommand") }
func (usProfileCommandRequest) String() string      { return "usProfileCommandRequest{redacted}" }
func (usProfileCommandRequest) GoString() string    { return "usProfileCommandRequest{redacted}" }
func (usProfileCommandRequest) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usProfileCommandRequest")
}
