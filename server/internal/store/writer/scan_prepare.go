package writer

import (
	"bytes"
	"errors"
	"unicode/utf8"
)

// ErrFilenameUnrepresentable distinguishes invalid UTF-8 native path bytes
// under CHANGE-003's TEXT path policy. It is a preparation cause, not proof of
// an eligible InventoryRefuse observation, cursor progress or a stored issue.
var ErrFilenameUnrepresentable = errors.New("inventory filename is not representable as UTF-8")

// InventoryAcceptInput is the complete ORIGINAL pre-allocation observation.
// It is not filesystem proof, current disclosure or resource admission. Trusted
// composition must own preparation memory before constructing the command.
type InventoryAcceptInput struct {
	Scope     InventoryScopeInput
	Source    InventorySourceInput
	Run       InventoryRunInput
	Directory InventoryDirectoryInput
	Files     []InventoryFilenameInput
}

type InventoryScopeInput struct {
	RunID, LibraryKey string
	LibraryID         int64
}
type InventorySourceInput struct {
	ID, Incarnation, RootIdentity string
	ResolvedRoot                  []byte // exact ORIGINAL resolved native root, not display text
	Generation                    int64
	FollowSymlinks                bool
}
type InventoryRunInput struct {
	Revision, PolicyRevision                    int64
	LeaseOwner, LeaseGeneration, LeaseExpiresMS int64
	LastSequence, AcknowledgedSequence          int64
	Phase, ScanMode                             string
}
type InventoryDirectoryInput struct {
	Path                                           []byte // exact native relative path; root directory is []byte(".")
	Identity, Revision, ExpectedCursor, NextCursor string
	CursorKind                                     string
	Pages, Skipped, RawNames                       int64
	Complete, Unstable                             bool
}
type InventoryFilenameInput struct {
	RelativePath, AssetPath                                                        []byte // exact native relative/absolute paths; no normalization
	SourceKey, ObjectIdentity, Revision, ChangeToken                               string
	Directory                                                                      bool
	Size, ModifiedNS                                                               int64
	Title, SortTitle, SortKey, SortHead, SortKeyKeepArticles, SortHeadKeepArticles string
	Year, Part                                                                     int64 // Part0 is a whole source; positive Part is the legacy multipart link index
	MetadataLanguage                                                               string
}

// NewInventoryAcceptCommand freezes ONE bounded ordinary-movie input through
// the existing private constructor and canonical encoder. No clock, ID, grant,
// principal, SQL or availability is created. It is not a complete driver/run/
// directory/EOF/Basic API. Unsupported fresh shapes remain unqualified at W.
func NewInventoryAcceptCommand(identity CommandIdentity, input InventoryAcceptInput) (InventoryAcceptCommand, error) {
	if len(input.Files) == 0 {
		return nil, invalid("inventory_request")
	}
	if err := count("inventory_files", len(input.Files), maxInventoryAcceptFiles); err != nil {
		return nil, err
	}
	if err := inventoryPublicPathPreflight(input); err != nil {
		return nil, err
	}
	// Fixed N1 projection after ALL public payload lengths have been checked.
	// Byte-to-string conversion is exact; the private constructor separately
	// preflights its full typed canonical wire before hashing and freezing.
	f := input.Files[0]
	files := [1]inventoryFile{{sourceKey: f.SourceKey, relativePath: string(f.RelativePath), assetPath: string(f.AssetPath),
		objectIdentity: f.ObjectIdentity, revision: f.Revision, changeToken: f.ChangeToken, directory: f.Directory, size: f.Size, modifiedNS: f.ModifiedNS,
		title: f.Title, sortTitle: f.SortTitle, sortKey: f.SortKey, sortHead: f.SortHead, sortKeyKeepArticles: f.SortKeyKeepArticles,
		sortHeadKeepArticles: f.SortHeadKeepArticles, year: f.Year, part: f.Part, metadataLanguage: f.MetadataLanguage}}
	s, r, d := input.Source, input.Run, input.Directory
	in := inventoryAcceptRequest{variant: inventoryOrdinaryBatch,
		scope:  inventoryScope{runID: input.Scope.RunID, libraryKey: input.Scope.LibraryKey, libraryID: input.Scope.LibraryID},
		source: inventorySource{id: s.ID, incarnation: s.Incarnation, rootIdentity: s.RootIdentity, resolvedRoot: s.ResolvedRoot, generation: s.Generation, followSymlinks: s.FollowSymlinks},
		run: inventoryRun{revision: r.Revision, policyRevision: r.PolicyRevision, leaseOwner: r.LeaseOwner, leaseGeneration: r.LeaseGeneration,
			leaseExpiresMS: r.LeaseExpiresMS, lastSequence: r.LastSequence, acknowledgedSequence: r.AcknowledgedSequence, phase: r.Phase, scanMode: r.ScanMode},
		directory: inventoryDirectory{path: string(d.Path), identity: d.Identity, revision: d.Revision, expectedCursor: d.ExpectedCursor, nextCursor: d.NextCursor,
			cursorKind: d.CursorKind, pages: d.Pages, skipped: d.Skipped, rawNames: d.RawNames, complete: d.Complete, unstable: d.Unstable}, files: files[:]}
	c, err := newInventoryAcceptCommand(identity, in)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Native paths arrive as bytes, never JSON strings. The FIRST private SQL
// shape supports UTF-8 TEXT paths only; refuse other bytes without replacing,
// escaping or normalizing them into a different source identity. Root remains
// exact BLOB input and its fresh transport/containment proof is independent.
// Lengths of ALL inputs are checked before any byte-to-string copy. The shared
// constructor separately preflights the complete canonical wire before hashing.
func inventoryPublicPathPreflight(in InventoryAcceptInput) error {
	f, s, r, d := in.Files[0], in.Source, in.Run, in.Directory
	texts := [...]string{inventoryOrdinaryBatch, in.Scope.RunID, in.Scope.LibraryKey, s.ID, s.Incarnation, s.RootIdentity,
		r.Phase, r.ScanMode, d.Identity, d.Revision, d.ExpectedCursor, d.NextCursor, d.CursorKind,
		f.SourceKey, f.ObjectIdentity, f.Revision, f.ChangeToken, f.Title, f.SortTitle, f.SortKey, f.SortHead, f.SortKeyKeepArticles, f.SortHeadKeepArticles, f.MetadataLanguage}
	paths := [...][]byte{s.ResolvedRoot, d.Path, f.RelativePath, f.AssetPath}
	remaining := uint64(maxCanonicalRequestBytes)
	for _, v := range texts {
		if uint64(len(v)) > remaining {
			return inventoryRequestCapacity()
		}
		remaining -= uint64(len(v))
	}
	for _, v := range paths {
		if uint64(len(v)) > remaining {
			return inventoryRequestCapacity()
		}
		remaining -= uint64(len(v))
	}
	// NUL is invalid native input, never the filename_unrepresentable poison
	// case. Check ALL paths first, including mixed NUL/invalid-UTF8 input.
	for _, v := range paths {
		if bytes.IndexByte(v, 0) >= 0 {
			return invalid("inventory_native_path")
		}
	}
	for _, v := range paths[1:] {
		if !utf8.Valid(v) {
			return &PolicyError{Field: "inventory_native_path", Cause: ErrFilenameUnrepresentable}
		}
	}
	return nil
}

// InventoryAcceptReference returns the ORIGINAL identity/digest/scope needed
// for Resolve/Ack after a zero/error result. It grants no current disclosure or
// Applied proof and never invokes a caller interface method or callback.
func InventoryAcceptReference(command InventoryAcceptCommand) (ScanReceiptRequest, error) {
	return inventoryAcceptReference(command)
}
