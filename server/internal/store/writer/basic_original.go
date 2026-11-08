package writer

import (
	"bytes"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Original native values only. Current disclosure, preparation custody and W's
// grant are transient outer inputs and never become request authority here.
type basicOriginalScope struct {
	scope  inventoryScope
	source inventorySource
	run    inventoryRun
}

func basicOriginalPreflight(identity CommandIdentity, original basicOriginalScope, s basicStreamFactStage) error {
	// Complete native variable payload before UTF-8, clones or shared hashing.
	remaining := maxCanonicalRequestBytes
	if len(original.source.resolvedRoot) > remaining {
		return ErrCap
	}
	remaining -= len(original.source.resolvedRoot)
	texts := [...]string{original.scope.runID, original.scope.libraryKey, original.source.id, original.source.incarnation, original.source.rootIdentity,
		original.run.phase, original.run.scanMode, s.assetToken, s.runID, s.sourceID, s.incarnation, s.objectID, s.objectRevision, s.fingerprint}
	for _, text := range texts {
		if len(text) > remaining {
			return ErrCap
		}
		remaining -= len(text)
	}
	if bytes.IndexByte(original.source.resolvedRoot, 0) >= 0 {
		return invalid("basic_native_root")
	}
	if !utf8.Valid(original.source.resolvedRoot) {
		return &PolicyError{Field: "basic_native_root", Cause: ErrFilenameUnrepresentable}
	}
	for _, text := range texts {
		if text == "" || !utf8.ValidString(text) {
			return invalid("basic_original_text")
		}
	}
	r, source, scope := original.run, original.source, original.scope
	if identity.Sequence <= 0 || scope.libraryID <= 0 || source.generation <= 0 || !filepath.IsAbs(string(source.resolvedRoot)) ||
		r.revision <= 0 || r.policyRevision <= 0 || r.leaseOwner <= 0 || r.leaseGeneration <= 0 || r.leaseExpiresMS <= 0 ||
		r.lastSequence != identity.Sequence-1 || r.acknowledgedSequence != r.lastSequence || !inventoryScanMode(r.scanMode) ||
		s.runID != scope.runID || s.libraryID != scope.libraryID || s.sourceID != source.id || s.incarnation != source.incarnation ||
		s.sourceGeneration != source.generation || s.policyRevision != r.policyRevision || s.assetID <= 0 || s.expectedEpoch <= 0 ||
		s.factRevision <= 0 || s.size < 0 || s.detailVersion < 0 || s.witnessFingerprint == ([32]byte{}) {
		return invalid("basic_original_scope")
	}
	// Phase eligibility, lease expiry, native equality and physical proof are
	// FRESH gates. A pure constructor cannot attest any of those observations.
	return nil
}

func cloneBasicOriginal(in basicOriginalScope) basicOriginalScope {
	in.scope.runID = strings.Clone(in.scope.runID)
	in.scope.libraryKey = strings.Clone(in.scope.libraryKey)
	in.source.id = strings.Clone(in.source.id)
	in.source.incarnation = strings.Clone(in.source.incarnation)
	in.source.rootIdentity = strings.Clone(in.source.rootIdentity)
	in.source.resolvedRoot = bytes.Clone(in.source.resolvedRoot)
	in.run.phase = strings.Clone(in.run.phase)
	in.run.scanMode = strings.Clone(in.run.scanMode)
	return in
}

func basicBool(v bool) int64 {
	if v {
		return 1
	}
	return 0
}
