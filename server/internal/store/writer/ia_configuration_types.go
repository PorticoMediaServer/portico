package writer

import (
	"bytes"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Immutable observed source facts, not an inspection/permission capability.
// The issuer first calls scan.PrepareLibraryRoots for the WHOLE raw batch,
// before any inspection IO, then freezes actual observations here. Retrying
// never re-observes/rebinds a root to manufacture the original command.
const iaConfigurationMaxSources, iaConfigurationMaxRootBytes = 64, 8192

type iaConfigurationSource struct {
	id, name, incarnation                      string
	generation                                 int64
	remoteSourceID                             string
	remoteSourceGeneration                     int64
	configuredRoot, resolvedRoot, rootIdentity string
	kind, classification                       string
	enabled, followSymlinks, identityConfirmed bool
	intervalSeconds                            int64
	// Retained scan/health values form a complete source result. They are
	// captured facts, never replaced by whatever a live read returns on retry.
	health, nextScanAt, lastCompleteAt, lastProgressAt string
}

type iaConfigurationRequest struct {
	acceptReplacement, replacement, scanAfter bool

	actor                                    iaRetirementActor // same IA current-owner lifetime vector, no UO dependency
	libraryID, expectedConfigurationRevision int64
	libraryKey, libraryName, libraryKind     string
	action                                   string // create_library, add_source, update_source, disable_source, enable_source
	before                                   *iaConfigurationSource
	sources                                  []iaConfigurationSource
}

type iaConfigurationCommand struct {
	purpose  CommandID
	identity CommandIdentity
	request  iaConfigurationRequest
	digest   RequestDigest
	valid    bool
}

func iaConfigurationSourceTexts(s *iaConfigurationSource) []*string {
	return []*string{&s.id, &s.name, &s.incarnation, &s.configuredRoot, &s.resolvedRoot, &s.rootIdentity, &s.kind, &s.classification, &s.health, &s.nextScanAt, &s.lastCompleteAt, &s.lastProgressAt, &s.remoteSourceID}
}

// Preparation only: private, bounded and inert. This cannot allocate an opaque
// principal/birth/root, prove inspection or overlap, select a grant, or accept
// any configuration change. Current owner/source/configuration CAS and original
// operation resolution remain the sole W composition's responsibilities.
func newIAConfigurationCommand(purpose CommandID, id CommandIdentity, r iaConfigurationRequest) (*iaConfigurationCommand, error) {
	if purpose != IAConfigureLibrary && purpose != IAConfigureSource || id.Sequence != 1 || id.OperationID == ([16]byte{}) || id.Incarnation == ([16]byte{}) {
		return nil, invalid("ia_configuration_identity")
	}
	if len(r.sources) == 0 || len(r.sources) > iaConfigurationMaxSources {
		return nil, invalid("ia_configuration_source_count")
	}
	a := r.actor
	if r.libraryKey == "" || a.authority == "" || a.accountID == "" || a.profileID == "" || a.membershipID == "" || a.accountEpoch <= 0 || a.membershipRevision <= 0 || a.profileRevision <= 0 {
		return nil, invalid("ia_configuration_owner")
	}
	if purpose == IAConfigureLibrary {
		if r.action != "create_library" || r.libraryID != 0 || r.expectedConfigurationRevision != 0 || r.libraryName == "" || r.libraryKind == "" || r.before != nil {
			return nil, invalid("ia_configuration_library_variant")
		}
	} else {
		if r.libraryID <= 0 || r.expectedConfigurationRevision <= 0 || len(r.sources) != 1 || r.libraryName != "" || r.libraryKind != "" {
			return nil, invalid("ia_configuration_source_variant")
		}
		switch r.action {
		case "add_source":
			if r.before != nil {
				return nil, invalid("ia_configuration_add_before")
			}
		case "update_source", "disable_source", "enable_source":
			if r.before == nil {
				return nil, invalid("ia_configuration_missing_before")
			}
		default:
			return nil, invalid("ia_configuration_action")
		}
	}
	// Count the entire byte envelope before cloning, UTF-8 scans or hashing.
	// This is a preparation payload cap, separate from native/result/debt and
	// physical memory budgets. Canonical framing has its own subsequent bound.
	texts := []*string{&r.libraryKey, &r.libraryName, &r.libraryKind, &r.action, &r.actor.authority, &r.actor.accountID, &r.actor.profileID, &r.actor.membershipID}
	for i := range r.sources {
		texts = append(texts, iaConfigurationSourceTexts(&r.sources[i])...)
	}
	if r.before != nil {
		texts = append(texts, iaConfigurationSourceTexts(r.before)...)
	}
	remaining := maxCanonicalRequestBytes
	for _, s := range texts {
		if len(*s) > remaining {
			return nil, &PolicyError{Field: "ia_configuration_bytes", Limit: maxCanonicalRequestBytes, Cause: ErrCap}
		}
		remaining -= len(*s)
	}
	for _, s := range texts {
		if !utf8.ValidString(*s) || strings.IndexByte(*s, 0) >= 0 {
			return nil, invalid("ia_configuration_text")
		}
	}
	if purpose == IAConfigureLibrary {
		switch r.libraryKind {
		case "movie", "tv", "anime", "music", "audiobook", "book", "comic":
		default:
			return nil, invalid("ia_configuration_library_kind")
		}
		if len(r.libraryName) > 100 || strings.TrimSpace(r.libraryName) != r.libraryName || strings.IndexFunc(r.libraryName, unicode.IsControl) >= 0 {
			return nil, invalid("ia_configuration_library_name")
		}
	}
	seen := make(map[string]bool, len(r.sources))
	for _, s := range r.sources {
		if s.id == "" || s.incarnation == "" || s.generation <= 0 || s.rootIdentity == "" || s.health == "" || s.configuredRoot == "" || s.resolvedRoot == "" || len(s.configuredRoot) > iaConfigurationMaxRootBytes || len(s.resolvedRoot) > iaConfigurationMaxRootBytes ||
			(s.kind != "local" && s.kind != "remote") || (s.classification != "local" && s.classification != "network") || s.intervalSeconds < 0 || s.intervalSeconds > 0 && s.intervalSeconds < 900 || seen[s.id] {
			return nil, invalid("ia_configuration_source_facts")
		}
		seen[s.id] = true
		if len(s.name) > 100 || strings.IndexFunc(s.name, unicode.IsControl) >= 0 || strings.IndexFunc(s.configuredRoot, unicode.IsControl) >= 0 || strings.IndexFunc(s.resolvedRoot, unicode.IsControl) >= 0 || s.intervalSeconds > 365*86400 || s.kind == "remote" && (s.classification != "network" || s.followSymlinks) {
			return nil, invalid("ia_configuration_source_policy")
		}
		if (s.remoteSourceID == "") != (s.remoteSourceGeneration == 0) || s.remoteSourceGeneration < 0 || len(s.remoteSourceID) > 256 {
			return nil, invalid("ia_configuration_remote_source")
		}
		if purpose == IAConfigureLibrary || r.action == "add_source" {
			if s.generation != 1 || !s.enabled || !s.identityConfirmed {
				return nil, invalid("ia_configuration_new_source")
			}
		}
	}
	if r.before != nil {
		before, after := *r.before, r.sources[0]
		if before.id != after.id || before.incarnation == "" || before.generation <= 0 || before.generation == 1<<63-1 || before.configuredRoot == "" || before.resolvedRoot == "" || before.rootIdentity == "" || after.generation != before.generation+1 {
			return nil, invalid("ia_configuration_original_source")
		}
		switch r.action {
		case "update_source":
			if r.replacement && (!r.acceptReplacement || after.incarnation == before.incarnation) || !r.replacement && after.incarnation != before.incarnation || !after.enabled || !after.identityConfirmed {
				return nil, invalid("ia_configuration_replacement")
			}
		case "disable_source", "enable_source":
			if after.incarnation != before.incarnation || after.configuredRoot != before.configuredRoot || after.resolvedRoot != before.resolvedRoot || after.rootIdentity != before.rootIdentity || after.kind != before.kind || after.classification != before.classification || after.enabled != (r.action == "enable_source") {
				return nil, invalid("ia_configuration_toggle")
			}
		}
	}
	digest, err := canonicalRequestDigest(purpose, 1, id, r)
	if err != nil {
		return nil, err
	}
	// Only now take ownership. Never normalize exact namespace/root bytes.
	r.sources = append([]iaConfigurationSource(nil), r.sources...)
	if r.before != nil {
		before := *r.before
		r.before = &before
	}
	texts = []*string{&r.libraryKey, &r.libraryName, &r.libraryKind, &r.action, &r.actor.authority, &r.actor.accountID, &r.actor.profileID, &r.actor.membershipID}
	for i := range r.sources {
		texts = append(texts, iaConfigurationSourceTexts(&r.sources[i])...)
	}
	if r.before != nil {
		texts = append(texts, iaConfigurationSourceTexts(r.before)...)
	}
	for _, s := range texts {
		*s = strings.Clone(*s)
	}
	return &iaConfigurationCommand{purpose: purpose, identity: id, request: r, digest: digest, valid: true}, nil
}

func (c *iaConfigurationCommand) checkOriginal() error {
	if c == nil || !c.valid {
		return invalid("ia_configuration_command")
	}
	checked, err := newIAConfigurationCommand(c.purpose, c.identity, c.request)
	if err != nil || !bytes.Equal(checked.digest[:], c.digest[:]) {
		return ErrConflict
	}
	return nil
}
