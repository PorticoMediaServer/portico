package writer

import (
	"strings"
	"unicode/utf8"
)

func describeUSProfileCommand(id CommandID) (Descriptor, bool) {
	switch id {
	case usProfileCreate:
		return Descriptor{ID: id, Name: "us_profile_create", Durability: Full}, true
	case usProfileMetadataEdit:
		return Descriptor{ID: id, Name: "us_profile_metadata_edit", Durability: Full}, true
	}
	return Descriptor{}, false
}

// Comparisons only: neither membership nor primary-profile flags grant access.
type usProfileTarget struct {
	member            usMemberTarget
	profile, lifetime string
	revision          int64
}

type usProfilePrepared struct {
	kind            CommandID
	target          usProfileTarget
	name, art       string
	setName, setArt bool
}

// IDs and entropy are generated once outside the hold and retained on retries.
func prepareUSProfileCreate(member usMemberTarget, id, lifetime, name, art string) (*usProfilePrepared, error) {
	if art == "" {
		art = "blue"
	}
	p, err := prepareUSProfileMetadata(usProfileTarget{member: member, profile: id, lifetime: lifetime, revision: 1}, &name, &art)
	if err != nil {
		return nil, err
	}
	p.kind = usProfileCreate
	return p, nil
}

func prepareUSProfileMetadata(target usProfileTarget, name, art *string) (*usProfilePrepared, error) {
	if !validUSMemberTarget(target.member) || !usIdentifier(target.profile) || !usHex(target.lifetime, 32) || target.revision < 1 || target.revision >= usMaxRevision || name == nil && art == nil {
		return nil, invalid("us_profile_target")
	}
	p := &usProfilePrepared{kind: usProfileMetadataEdit, target: target}
	if name != nil {
		v := strings.TrimSpace(*name)
		// Exact directName byte/control rules; no added Unicode policy.
		if v == "" || len(v) > 120 || !utf8.ValidString(v) || strings.IndexFunc(v, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
			return nil, invalid("name")
		}
		p.name, p.setName = strings.Clone(v), true
	}
	if art != nil {
		switch *art {
		case "blue", "violet", "mint", "coral", "gold", "slate", "rose", "sky":
		default:
			return nil, invalid("art")
		}
		p.art, p.setArt = strings.Clone(*art), true
	}
	return p, nil
}
