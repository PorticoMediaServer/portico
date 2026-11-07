package scan

import (
	"fmt"
	"path"
	"strings"
)

// ReconcileKind describes the next preparation step, never an accepted change.
// In particular RelocationCandidate still needs a fresh native old-path check:
// a second hardlink is not a move and must retain its separate location.
type ReconcileKind uint8

const (
	ReconcileNewLocation ReconcileKind = 1
	ReconcileUnchanged   ReconcileKind = 2
	ReconcileRefresh     ReconcileKind = 3
	ReconcileReplacement ReconcileKind = 4
	ReconcileNeedLookup  ReconcileKind = 5
	ReconcileRelocation  ReconcileKind = 6
	ReconcileAmbiguous   ReconcileKind = 7
)

// ReconcileInput is a bounded point/path lookup plus at most two raw native
// identity candidates. MoveWindowComplete distinguishes a real bounded query
// from an omitted or truncated candidate list. No filename similarity is used.
// Current and AcceptedBasic are the same Basic inputs PlanBasic already owns.
type ReconcileInput struct {
	Current            BasicInput
	AcceptedBasic      *BasicInput
	AtPath             *FileWitness
	MoveCandidates     []FileWitness
	MoveWindowComplete bool
}

type ReconcilePlan struct {
	Kind  ReconcileKind
	Basic BasicPlan
	// Previous is owned evidence only, not a durable object or public identity.
	Previous FileWitness
}

// PlanReconcile schedules exact unchanged reuse, changed-file refresh and
// conservative move inspection. It cannot allocate identities, relocate a
// source, infer absence, advance a cursor or mark a receipt Applied. The writer
// must re-read current source/run/lease/object facts in its accepting turn.
// byteBudget counts the complete raw witness string payload, including rejected
// candidates; callers reserve actual working memory separately before entry.
func PlanReconcile(in ReconcileInput, byteBudget int64) (ReconcilePlan, error) {
	if byteBudget <= 0 || len(in.MoveCandidates) > 2 {
		return ReconcilePlan{}, ErrCapacity
	}
	remaining := byteBudget
	charge := func(w FileWitness) bool {
		for _, s := range []string{w.Source.SourceID, w.Source.Incarnation, w.Source.Revision, w.RelativePath, w.ObjectIdentity, w.Revision} {
			if int64(len(s)) > remaining {
				return false
			}
			remaining -= int64(len(s))
		}
		return true
	}
	current := in.Current.File
	if !charge(current) || in.AtPath != nil && !charge(*in.AtPath) {
		return ReconcilePlan{}, ErrCapacity
	}
	chargeBasic := func(b BasicInput) bool {
		for _, s := range []string{b.ProbeRecipe, b.NFO.Revision, b.Artwork.Revision, b.Subtitles.Revision} {
			if int64(len(s)) > remaining {
				return false
			}
			remaining -= int64(len(s))
		}
		return true
	}
	if !chargeBasic(in.Current) || in.AcceptedBasic != nil && (!charge(in.AcceptedBasic.File) || !chargeBasic(*in.AcceptedBasic)) {
		return ReconcilePlan{}, ErrCapacity
	}
	for _, candidate := range in.MoveCandidates {
		if !charge(candidate) {
			return ReconcilePlan{}, ErrCapacity
		}
	}
	if !current.valid() || !in.Current.Available {
		return ReconcilePlan{}, fmt.Errorf("%w: reconcile requires an observed available file", ErrInvalid)
	}
	if in.AtPath != nil && (!in.AtPath.valid() || !iaSameSource(current.Source, in.AtPath.Source) || in.AtPath.RelativePath != current.RelativePath) {
		return ReconcilePlan{}, fmt.Errorf("%w: reconcile path witness", ErrInvalid)
	}
	for i, candidate := range in.MoveCandidates {
		if !candidate.valid() || !iaSameSource(current.Source, candidate.Source) {
			return ReconcilePlan{}, fmt.Errorf("%w: reconcile candidate scope", ErrInvalid)
		}
		for _, earlier := range in.MoveCandidates[:i] {
			if earlier.RelativePath == candidate.RelativePath {
				return ReconcilePlan{}, fmt.Errorf("%w: duplicate candidate location", ErrInvalid)
			}
		}
	}
	// Basic reuse remains exact and includes policy, recipe and all observed
	// local sidecars. Size/mtime alone never skips a probe or accepts new facts.
	result := ReconcilePlan{Basic: PlanBasic(in.Current, in.AcceptedBasic)}
	if in.AtPath != nil {
		previous := *in.AtPath
		result.Previous = iaCloneWitness(previous)
		switch {
		case current == previous:
			result.Kind = ReconcileUnchanged
		case current.Size != previous.Size || current.ModifiedNS != previous.ModifiedNS:
			result.Kind = ReconcileRefresh
			if current.Source.Incarnation != previous.Source.Incarnation || current.ObjectIdentity != "" && previous.ObjectIdentity != "" && current.ObjectIdentity != previous.ObjectIdentity {
				result.Kind = ReconcileReplacement
			}
		default:
			// Remounts can renumber inodes with unchanged content evidence.
			// Preserve the retained location; re-prove the new Basic vector.
			result.Kind = ReconcileRefresh
		}
		return result, nil
	}
	if !in.MoveWindowComplete {
		result.Kind = ReconcileNeedLookup
		return result, nil
	}
	result.Kind = ReconcileNewLocation
	for _, candidate := range in.MoveCandidates {
		if current.ObjectIdentity == "" || current.ObjectIdentity != candidate.ObjectIdentity || current.Source != candidate.Source ||
			current.RelativePath == candidate.RelativePath || current.Size != candidate.Size || current.ModifiedNS != candidate.ModifiedNS || current.Revision != candidate.Revision {
			continue
		}
		if result.Kind == ReconcileRelocation {
			return ReconcilePlan{Kind: ReconcileAmbiguous, Basic: result.Basic}, nil
		}
		result.Kind, result.Previous = ReconcileRelocation, iaCloneWitness(candidate)
	}
	return result, nil
}

func iaSameSource(a, b SourceWitness) bool {
	return a.LibraryID == b.LibraryID && a.SourceID == b.SourceID
}

func iaCloneWitness(w FileWitness) FileWitness {
	w.Source.SourceID = strings.Clone(w.Source.SourceID)
	w.Source.Incarnation = strings.Clone(w.Source.Incarnation)
	w.Source.Revision = strings.Clone(w.Source.Revision)
	w.RelativePath = strings.Clone(w.RelativePath)
	w.ObjectIdentity = strings.Clone(w.ObjectIdentity)
	w.Revision = strings.Clone(w.Revision)
	return w
}

// DirectoryCoverage is one original source/run's COMPLETE parent directory
// observation. These values are preparation input, not minted authority. EOF,
// post-listing verification and absence eligibility are separate predicates.
type DirectoryCoverage struct {
	Source                                     SourceWitness
	RunID, Directory, Identity, Revision       string
	EOF, Verified, Unstable, Protected, Failed bool
}

// MayCheckMissing nominates an unseen file for a fresh writer-side absence
// check. It never declares it missing/deleted. An incomplete/protected subtree,
// source mismatch or a different parent cannot nominate even one object.
func MayCheckMissing(coverage DirectoryCoverage, file FileWitness, seenRunID string) bool {
	return file.valid() && coverage.Source == file.Source && coverage.RunID != "" &&
		coverage.RunID != seenRunID && coverage.Identity != "" && coverage.Revision != "" &&
		coverage.Directory == path.Dir(file.RelativePath) && coverage.EOF && coverage.Verified &&
		!coverage.Unstable && !coverage.Protected && !coverage.Failed
}
