package writer

import (
	"bytes"
	"math"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/scanpath"
)

// Separate complete input: adding terminal purposes never changes the already
// published discovery/EOF request type or its canonical digest.
type scanDirectoryTerminalRequest struct {
	Snapshot                                        inventoryClaimRequest
	Parent                                          scanRootWitness
	Warnings                                        int64 // captured native inventory_runs.warnings, NOT a result echo
	CursorKind, NextCursor                          string
	RawChildName, RawChildPath, RefusedRelativePath []byte
	ChildIdentity, ChildRevision                    string
}

type scanDirectoryTerminalCommand struct {
	purpose  CommandID
	identity CommandIdentity
	request  scanDirectoryTerminalRequest
	digest   RequestDigest
	valid    bool
}

func newScanDirectoryRefuseCommand(id CommandIdentity, s inventoryClaimRequest, parent scanRootWitness, warnings int64, page, child nativeinventory.Page) (*scanDirectoryTerminalCommand, error) {
	if len(page.Entries) != 1 || len(page.Issues) != 0 || page.Version != 1 || page.ErrorCode != "" || page.Complete || page.Unstable || !page.Entries[0].Directory || child.Version != 1 || child.ErrorCode != "" || child.Unstable {
		return nil, ErrConflict
	}
	if err := preflightDirectoryObservation(s, parent, page, child); err != nil {
		return nil, err
	}
	e := page.Entries[0]
	if page.RootIdentity != s.Original.RootIdentity || page.DirectoryIdentity != parent.Identity || page.DirectoryRevision != parent.Revision || child.RootIdentity != s.Original.RootIdentity || child.DirectoryIdentity == "" || child.DirectoryIdentity != e.ObjectIdentity || child.DirectoryRevision == "" {
		return nil, ErrConflict
	}
	raw, err := terminalRelativePath(s.Original.ResolvedRoot, e.Path)
	if err != nil {
		return nil, err
	}
	return newScanDirectoryTerminalCommand(InventoryDirectoryRefuse, id, scanDirectoryTerminalRequest{Snapshot: s, Parent: parent, Warnings: warnings, CursorKind: inventoryLinuxCursor, NextCursor: page.NextCursor, RawChildName: e.Name, RawChildPath: e.Path, RefusedRelativePath: raw, ChildIdentity: child.DirectoryIdentity, ChildRevision: child.DirectoryRevision})
}

// Traversal uses the captured durable terminal root; it does not synthesize a
// new EOF observation. The SAME-turn indexed frontier probes remain mandatory.
func newScanTraversalCompleteCommand(id CommandIdentity, s inventoryClaimRequest) (*scanDirectoryTerminalCommand, error) {
	return newScanDirectoryTerminalCommand(InventoryTraversalComplete, id, scanDirectoryTerminalRequest{Snapshot: s})
}

func newScanDirectoryTerminalCommand(kind CommandID, id CommandIdentity, q scanDirectoryTerminalRequest) (*scanDirectoryTerminalCommand, error) {
	if err := validateScanDirectoryTerminalRequest(kind, id, q); err != nil {
		return nil, err
	}
	d, err := canonicalRequestDigest(kind, 1, id, q)
	if err != nil {
		return nil, err
	}
	q.Snapshot.Original = cloneScanRunOriginalScope(q.Snapshot.Original)
	q.Snapshot.Run.phase = strings.Clone(q.Snapshot.Run.phase)
	q.Snapshot.Run.scanMode = strings.Clone(q.Snapshot.Run.scanMode)
	q.Snapshot.Root = cloneDirectoryWitness(q.Snapshot.Root)
	q.Snapshot.FinishedAt = strings.Clone(q.Snapshot.FinishedAt)
	q.Snapshot.JobStatus = strings.Clone(q.Snapshot.JobStatus)
	q.Snapshot.JobCreatedAt = strings.Clone(q.Snapshot.JobCreatedAt)
	q.Snapshot.JobErrorText = strings.Clone(q.Snapshot.JobErrorText)
	q.Parent = cloneDirectoryWitness(q.Parent)
	q.CursorKind, q.NextCursor = strings.Clone(q.CursorKind), strings.Clone(q.NextCursor)
	q.ChildIdentity, q.ChildRevision = strings.Clone(q.ChildIdentity), strings.Clone(q.ChildRevision)
	q.RawChildName, q.RawChildPath, q.RefusedRelativePath = bytes.Clone(q.RawChildName), bytes.Clone(q.RawChildPath), bytes.Clone(q.RefusedRelativePath)
	return &scanDirectoryTerminalCommand{kind, id, q, d, true}, nil
}

func (c *scanDirectoryTerminalCommand) reference() ScanReceiptRequest {
	o := c.request.Snapshot.Original
	return ScanReceiptRequest{RunID: o.RunID, LibraryID: o.LibraryID, LibraryKey: o.LibraryKey, Receipt: ReceiptReference{c.identity, c.digest}}
}

func terminalRequestProjection(q scanDirectoryTerminalRequest) scanDirectoryRequest {
	return scanDirectoryRequest{Snapshot: q.Snapshot, Parent: q.Parent, CursorKind: q.CursorKind, NextCursor: q.NextCursor, ChildIdentity: q.ChildIdentity, ChildRevision: q.ChildRevision, RawChildName: q.RawChildName, RawChildPath: q.RawChildPath}
}
func terminalRequestBytes(q scanDirectoryTerminalRequest) (uint64, error) {
	n, err := directoryRequestBytes(terminalRequestProjection(q))
	if err != nil {
		return 0, err
	}
	if uint64(len(q.RefusedRelativePath)) > uint64(maxCanonicalRequestBytes)-n {
		return 0, ErrCap
	}
	return n + uint64(len(q.RefusedRelativePath)), nil
}

func validateScanDirectoryTerminalRequest(kind CommandID, id CommandIdentity, q scanDirectoryTerminalRequest) error {
	if _, err := terminalRequestBytes(q); err != nil {
		return err
	}
	for _, s := range directoryRequestTexts(terminalRequestProjection(q)) {
		if !utf8.ValidString(s) || strings.IndexByte(s, 0) >= 0 {
			return ErrInvalid
		}
	}
	s, o, r := q.Snapshot, q.Snapshot.Original, q.Snapshot.Run
	if id.Sequence <= 0 || o.RunID == "" || o.LibraryID <= 0 || o.LibraryKey == "" || o.SourceID == "" || o.SourceGeneration <= 0 || o.SourceIncarnation == "" || o.RootIdentity == "" || o.PolicyRevision <= 0 || !scanLifecycleMode(o.ScanMode) ||
		!utf8.Valid(o.ResolvedRoot) || bytes.IndexByte(o.ResolvedRoot, 0) >= 0 || len(o.ResolvedRoot) == 0 || o.ResolvedRoot[0] != '/' || path.Clean(string(o.ResolvedRoot)) != string(o.ResolvedRoot) || o.FollowSymlinks ||
		r.revision <= 0 || r.policyRevision != o.PolicyRevision || r.scanMode != o.ScanMode || r.phase != "inventory" || r.leaseOwner <= 0 || r.leaseGeneration <= 0 || r.leaseExpiresMS <= 0 || r.lastSequence < 0 || r.acknowledgedSequence < 0 || r.acknowledgedSequence > r.lastSequence ||
		s.JobStatus != "running" || s.Authoritative != 0 || s.RootRebound != 0 || s.FinishedAt != "" || s.Discovered < 0 || s.Processed < 0 || s.Attempts < 0 || s.Attempts > math.MaxUint32 || s.NextAttemptMS.Present && s.NextAttemptMS.Value < 0 || !s.NextAttemptMS.Present && s.NextAttemptMS.Value != 0 ||
		s.Root.Path != "." || s.Root.Identity == "" || s.Root.Revision == "" || s.Root.Pages < 0 || s.Root.Skipped < 0 || s.Root.Skipped > s.Root.Pages {
		return ErrInvalid
	}
	if _, err := inventoryFreshCookie(s.Root.Cursor); err != nil {
		return err
	}
	if s.Root.State != "pending" && s.Root.State != "done" && s.Root.State != "blocked" {
		return ErrInvalid
	}
	switch kind {
	case InventoryDirectoryRefuse:
		p := q.Parent
		if q.Warnings < 0 || p.Path == "" || !validDirectoryRelative(p.Path) || p.Identity == "" || p.Revision == "" || p.State != "pending" || p.Pages < 0 || p.Skipped < 0 || p.Skipped > p.Pages || q.CursorKind != inventoryLinuxCursor || q.ChildIdentity == "" || q.ChildRevision == "" {
			return ErrInvalid
		}
		old, err := inventoryFreshCookie(p.Cursor)
		if err != nil {
			return err
		}
		next, err := inventoryFreshCookie(q.NextCursor)
		if err != nil {
			return err
		}
		if q.NextCursor == "" || old == next {
			return ErrConflict
		}
		raw, err := terminalRelativePath(o.ResolvedRoot, q.RawChildPath)
		if err != nil {
			return err
		}
		if !bytes.Equal(raw, q.RefusedRelativePath) || !validTerminalRawChild(p.Path, q.RawChildName, raw) {
			return ErrConflict
		}
		_, err = terminalDiagnostic(raw)
		return err
	case InventoryTraversalComplete:
		if s.Root.State != "done" && s.Root.State != "blocked" {
			return ErrConflict
		}
		if q.Parent != (scanRootWitness{}) || q.Warnings != 0 || q.CursorKind != "" || q.NextCursor != "" || len(q.RawChildName) != 0 || len(q.RawChildPath) != 0 || len(q.RefusedRelativePath) != 0 || q.ChildIdentity != "" || q.ChildRevision != "" {
			return ErrInvalid
		}
		return nil
	default:
		return ErrInvalid
	}
}

// Borrow the exact native suffix under the ORIGINAL absolute root boundary.
// Only the final component may be unrepresentable; never clean raw bytes.
func terminalRelativePath(root, absolute []byte) ([]byte, error) {
	if len(root) == 0 || len(root) > maxCanonicalRequestBytes || len(absolute) > maxCanonicalRequestBytes-len(root) {
		return nil, ErrCap
	}
	if !utf8.Valid(root) || bytes.IndexByte(root, 0) >= 0 || bytes.IndexByte(absolute, 0) >= 0 || root[0] != '/' || path.Clean(string(root)) != string(root) {
		return nil, ErrInvalid
	}
	n := len(root)
	if len(absolute) <= n || !bytes.Equal(root, absolute[:n]) {
		return nil, ErrInvalid
	}
	if n != 1 {
		if absolute[n] != '/' {
			return nil, ErrInvalid
		}
		n++
	}
	if n >= len(absolute) {
		return nil, ErrInvalid
	}
	return absolute[n:], nil
}
func validTerminalRawChild(parent string, name, raw []byte) bool {
	if !validDirectoryRelative(parent) || len(name) == 0 || utf8.Valid(name) || bytes.IndexByte(name, 0) >= 0 || bytes.IndexByte(name, '/') >= 0 || bytes.IndexByte(raw, 0) >= 0 {
		return false
	}
	if parent == "." {
		return bytes.Equal(name, raw)
	}
	return len(raw) == len(parent)+1+len(name) && string(raw[:len(parent)]) == parent && raw[len(parent)] == '/' && bytes.Equal(raw[len(parent)+1:], name)
}
func terminalDiagnostic(raw []byte) (string, error) {
	display, err := scanpath.EncodeFilename(raw, maxCanonicalRequestBytes)
	if err != nil {
		return "", ErrCap
	}
	inverse, err := scanpath.DecodeFilename(display, maxCanonicalRequestBytes)
	if err != nil || !bytes.Equal(inverse, raw) {
		return "", ErrInvalid
	}
	return display, nil
}
