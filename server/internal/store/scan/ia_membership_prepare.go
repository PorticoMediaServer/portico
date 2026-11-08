package scan

import (
	"fmt"
	"strings"
)

// IAMembershipAssociation names which prepared association may own one observed
// video file. Exact extras evidence always wins: such a file is one member of a
// proven owner (or of an explicitly unresolved owner), never a second generic
// association of the same file.
type IAMembershipAssociation uint8

const (
	IAMembershipGeneric IAMembershipAssociation = iota + 1
	IAMembershipExtra
)

// IAMembershipOwner is one already-resolved owner candidate from a bounded
// lookup. It is evidence only: the writer must re-read the current entity,
// library and retirement facts before accepting anything.
type IAMembershipOwner struct {
	Entity, Library int64
	Retired         bool
}

// IAMembershipPlan is pure preparation. It allocates no identity, resolves no
// path and queues nothing. Owner is nil exactly when no candidate was proven:
// the parent path stays explicit and no owner id is invented. Editions and Parts
// retain every observed value in order; nothing is trimmed, folded or reduced to
// a first member.
type IAMembershipPlan struct {
	Association IAMembershipAssociation
	ExtraType   string
	ParentPath  string
	Owner       *IAMembershipOwner
	Editions    []string
	Parts       []int
}

// PrepareIAMembership classifies one prepared media file before generic
// association. A video extra is explicit membership evidence for its parent
// path; music, audiobook and reading folders are never reinterpreted as video
// extras. byteBudget bounds retained string payloads, not heap/RSS; the driver
// owns admission before this call.
func PrepareIAMembership(evidence MediaEvidence, owner *IAMembershipOwner, byteBudget int64) (IAMembershipPlan, error) {
	if byteBudget <= 0 {
		return IAMembershipPlan{}, ErrCapacity
	}
	kind := evidence.kind
	reading := evidence.reading
	if !iaMediaKind(kind) || !evidence.file.Witness.Source.valid() || evidence.file.SourceKey == "" {
		return IAMembershipPlan{}, fmt.Errorf("%w: membership evidence scope", ErrInvalid)
	}
	remaining := byteBudget
	charge := func(value string) bool {
		if int64(len(value)) > remaining {
			return false
		}
		remaining -= int64(len(value))
		return true
	}
	// One file is at most one extra. A wider cohort is refused, never reduced to
	// its first member.
	if len(reading.Extras) > 1 {
		return IAMembershipPlan{}, fmt.Errorf("%w: extra evidence cohort", ErrInvalid)
	}
	plan := IAMembershipPlan{Association: IAMembershipGeneric}
	if len(reading.Extras) == 1 {
		extra := reading.Extras[0]
		if !iaVideoKind(kind) {
			return IAMembershipPlan{}, fmt.Errorf("%w: extras are video membership only", ErrInvalid)
		}
		if !iaMembershipExtraKind(extra.Value.Kind) {
			return IAMembershipPlan{}, fmt.Errorf("%w: extra type", ErrInvalid)
		}
		if !charge(extra.Value.Kind) || !charge(extra.Value.ParentPath) {
			return IAMembershipPlan{}, ErrCapacity
		}
		plan.Association = IAMembershipExtra
		plan.ExtraType = strings.Clone(extra.Value.Kind)
		plan.ParentPath = strings.Clone(extra.Value.ParentPath)
	}
	// An owner candidate is only meaningful for an exact extra. A generic file
	// with an owner would invent a relationship the naming evidence never showed.
	if owner != nil {
		if plan.Association != IAMembershipExtra {
			return IAMembershipPlan{}, fmt.Errorf("%w: owner requires extra evidence", ErrInvalid)
		}
		library := evidence.file.Witness.Source.LibraryID
		if owner.Entity <= 0 || owner.Library != library || owner.Retired {
			return IAMembershipPlan{}, fmt.Errorf("%w: membership owner scope", ErrInvalid)
		}
		plan.Owner = &IAMembershipOwner{Entity: owner.Entity, Library: owner.Library}
	}
	for _, edition := range reading.Editions {
		if !charge(edition.Value) {
			return IAMembershipPlan{}, ErrCapacity
		}
		plan.Editions = append(plan.Editions, strings.Clone(edition.Value))
	}
	for _, part := range reading.Parts {
		plan.Parts = append(plan.Parts, part.Value)
	}
	return plan, nil
}

// iaVideoKind mirrors the merged evidence split: only video domains can carry
// video extras. Audio and reading "Bonus"/"Extras"/"Shorts" folders stay album,
// disc, part or volume evidence.
func iaVideoKind(kind string) bool {
	switch kind {
	case "movie", "tv", "anime":
		return true
	default:
		return false
	}
}

// iaMembershipExtraKind is the closed item_extras.kind domain.
func iaMembershipExtraKind(kind string) bool {
	switch kind {
	case "trailer", "featurette", "deleted_scene", "behind_the_scenes", "interview", "scene", "short", "other":
		return true
	default:
		return false
	}
}
