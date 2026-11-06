package writer

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

// These are complete original inputs, not native observation proof, ingress,
// old-work exclusion, a grant or a lease issuer. Only W composition may obtain
// the actual witnesses and submit the resulting command through its frame.
type inventoryCreateRequest struct {
	Scope                      inventoryScope
	Source                     inventorySource
	PolicyRevision             int64
	ScanMode                   string
	RootIdentity, RootRevision string
}

type scanRootWitness struct {
	Path, Identity, Revision, Cursor, State string
	Pages, Skipped                          int64
}

type scanOptionalMS struct {
	Present bool
	Value   int64
}

type inventoryClaimRequest struct {
	Original              scanRunOriginalScope
	Run                   inventoryRun
	Root                  scanRootWitness
	Discovered, Processed int64
	Authoritative         int64
	RootRebound           int64
	FinishedAt            string
	JobStatus             string
	JobCreatedAt          string
	JobErrorText          string
	Attempts              int64
	NextAttemptMS         scanOptionalMS
}

type inventoryCreateCommand struct {
	identity CommandIdentity
	request  inventoryCreateRequest
	digest   RequestDigest
	valid    bool
}

type inventoryClaimCommand struct {
	identity CommandIdentity
	request  inventoryClaimRequest
	digest   RequestDigest
	valid    bool
}

// Creation binds the preallocated identity and actual observed pending-root
// witness. The generated AppliedMS and request digest are not input fields, so
// there is no self-referential digest or post-effect rewrite of this request.
func newInventoryCreateCommand(identity CommandIdentity, in inventoryCreateRequest) (*inventoryCreateCommand, error) {
	if identity.Sequence != 1 || in.Scope.runID == "" || in.Scope.libraryKey == "" || in.Scope.libraryID <= 0 ||
		in.Source.id == "" || in.Source.incarnation == "" || in.Source.rootIdentity == "" || in.Source.generation <= 0 ||
		in.PolicyRevision <= 0 || in.RootIdentity == "" || in.RootRevision == "" || !scanLifecycleMode(in.ScanMode) {
		return nil, invalid("inventory_create_request")
	}
	if err := preflightScanLifecycleRequest(in.Source.resolvedRoot, in.Scope.runID, in.Scope.libraryKey,
		in.Source.id, in.Source.incarnation, in.Source.rootIdentity, in.ScanMode, in.RootIdentity, in.RootRevision); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(InventoryCreate, 1, identity, in)
	if err != nil {
		return nil, err
	}
	in.Scope.runID, in.Scope.libraryKey = strings.Clone(in.Scope.runID), strings.Clone(in.Scope.libraryKey)
	in.Source.id, in.Source.incarnation = strings.Clone(in.Source.id), strings.Clone(in.Source.incarnation)
	in.Source.rootIdentity = strings.Clone(in.Source.rootIdentity)
	in.Source.resolvedRoot = bytes.Clone(in.Source.resolvedRoot)
	in.ScanMode, in.RootIdentity, in.RootRevision = strings.Clone(in.ScanMode), strings.Clone(in.RootIdentity), strings.Clone(in.RootRevision)
	return &inventoryCreateCommand{identity: identity, request: in, digest: digest, valid: true}, nil
}

// Preparation is shape-only. It does not read a clock or reject an expired
// captured lease, due time, stale revision or exhausted counter before history.
// Actual claim branch and checked increments are selected after receipt miss.
func newInventoryClaimCommand(identity CommandIdentity, in inventoryClaimRequest) (*inventoryClaimCommand, error) {
	o, r := in.Original, in.Run
	if identity.Sequence <= 1 || o.RunID == "" || o.LibraryKey == "" || o.LibraryID <= 0 ||
		o.SourceID == "" || o.SourceIncarnation == "" || o.RootIdentity == "" || o.SourceGeneration <= 0 || o.PolicyRevision <= 0 ||
		!scanLifecycleMode(o.ScanMode) || r.revision <= 0 || r.policyRevision != o.PolicyRevision || r.scanMode != o.ScanMode || r.phase != "inventory" ||
		r.leaseOwner < 0 || r.leaseGeneration < 0 || r.leaseExpiresMS < 0 || r.lastSequence < 0 || r.acknowledgedSequence < 0 ||
		in.Root.Path != "." || in.Root.Identity == "" || in.Root.Revision == "" || in.Root.State != "pending" ||
		in.Root.Pages < 0 || in.Root.Skipped < 0 || in.Root.Skipped > in.Root.Pages ||
		in.Discovered < 0 || in.Processed < 0 || in.Authoritative < 0 || in.RootRebound < 0 ||
		in.Attempts < 0 || in.Attempts > 1<<32-1 ||
		(in.JobStatus != "queued" && in.JobStatus != "running") ||
		(!in.NextAttemptMS.Present && in.NextAttemptMS.Value != 0) || in.NextAttemptMS.Value < 0 {
		return nil, invalid("inventory_claim_request")
	}
	zeroLease := r.leaseOwner == 0 && r.leaseGeneration == 0 && r.leaseExpiresMS == 0
	positiveLease := r.leaseOwner > 0 && r.leaseGeneration > 0 && r.leaseExpiresMS > 0
	if !zeroLease && !positiveLease {
		return nil, invalid("inventory_claim_lease_shape")
	}
	if err := preflightScanLifecycleRequest(o.ResolvedRoot, o.RunID, o.LibraryKey, o.SourceID, o.SourceIncarnation,
		o.RootIdentity, o.ScanMode, r.phase, r.scanMode, in.Root.Path, in.Root.Identity, in.Root.Revision,
		in.Root.Cursor, in.Root.State, in.FinishedAt, in.JobStatus, in.JobCreatedAt, in.JobErrorText); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(InventoryClaim, 1, identity, in)
	if err != nil {
		return nil, err
	}
	in.Original = cloneScanRunOriginalScope(o)
	in.Run.phase, in.Run.scanMode = strings.Clone(r.phase), strings.Clone(r.scanMode)
	in.Root.Path, in.Root.Identity = strings.Clone(in.Root.Path), strings.Clone(in.Root.Identity)
	in.Root.Revision, in.Root.Cursor, in.Root.State = strings.Clone(in.Root.Revision), strings.Clone(in.Root.Cursor), strings.Clone(in.Root.State)
	in.JobStatus = strings.Clone(in.JobStatus)
	in.JobCreatedAt, in.JobErrorText, in.FinishedAt = strings.Clone(in.JobCreatedAt), strings.Clone(in.JobErrorText), strings.Clone(in.FinishedAt)
	return &inventoryClaimCommand{identity: identity, request: in, digest: digest, valid: true}, nil
}

func preflightScanLifecycleRequest(root []byte, texts ...string) error {
	remaining := maxCanonicalRequestBytes
	if len(root) == 0 || len(root) > remaining {
		return invalid("scan_lifecycle_request_bytes")
	}
	remaining -= len(root)
	for _, s := range texts {
		if len(s) > remaining {
			return invalid("scan_lifecycle_request_bytes")
		}
		remaining -= len(s)
	}
	// Full payload lengths precede every scan; the shared canonical encoder then
	// bounds the complete typed wire before either constructor clones anything.
	if !utf8.Valid(root) || bytes.IndexByte(root, 0) >= 0 {
		return invalid("scan_lifecycle_request_root")
	}
	for _, s := range texts {
		if !utf8.ValidString(s) {
			return invalid("scan_lifecycle_request_utf8")
		}
	}
	return nil
}

func scanLifecycleMode(mode string) bool {
	switch mode {
	case "first-import", "incremental", "integrity", "full":
		return true
	default:
		return false
	}
}

func cloneScanRunOriginalScope(o scanRunOriginalScope) scanRunOriginalScope {
	o.RunID, o.LibraryKey = strings.Clone(o.RunID), strings.Clone(o.LibraryKey)
	o.SourceID, o.SourceIncarnation = strings.Clone(o.SourceID), strings.Clone(o.SourceIncarnation)
	o.RootIdentity, o.ScanMode = strings.Clone(o.RootIdentity), strings.Clone(o.ScanMode)
	o.ResolvedRoot = bytes.Clone(o.ResolvedRoot)
	return o
}
