package writer

import (
	"bytes"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
)

// Private, complete original values. Trusted preparation owns both supervised
// observations BEFORE constructing this value; no syscall occurs under HOLD.
// These constructors grant no source, lease, principal or resource authority.
type scanDirectoryRequest struct {
	Snapshot                                inventoryClaimRequest
	Parent                                  scanRootWitness
	CursorKind, NextCursor                  string
	RawNames                                int64
	Complete, Unstable                      bool
	ChildPath, ChildIdentity, ChildRevision string
	RawChildName, RawChildPath              []byte
}

type scanDirectoryCommand struct {
	purpose  CommandID
	identity CommandIdentity
	request  scanDirectoryRequest
	digest   RequestDigest
	valid    bool
}

func newScanDirectoryDiscoverCommand(id CommandIdentity, snapshot inventoryClaimRequest, parent scanRootWitness, page, child nativeinventory.Page) (*scanDirectoryCommand, error) {
	if len(page.Entries) != 1 || len(page.Issues) != 0 || page.Complete || page.Unstable || page.ErrorCode != "" || page.Version != 1 ||
		child.Version != 1 || child.ErrorCode != "" || child.Unstable || !page.Entries[0].Directory {
		return nil, ErrConflict
	}
	e := page.Entries[0]
	if err := preflightDirectoryObservation(snapshot, parent, page, child); err != nil {
		return nil, err
	}
	// Lengths precede path conversion, UTF-8 scans and canonical hashing.
	if len(e.Path) > maxCanonicalRequestBytes || len(e.Name) > maxCanonicalRequestBytes-len(e.Path) {
		return nil, ErrCap
	}
	if bytes.IndexByte(e.Path, 0) >= 0 || bytes.IndexByte(e.Name, 0) >= 0 {
		return nil, ErrInvalid
	}
	if !utf8.Valid(e.Path) || !utf8.Valid(e.Name) {
		return nil, ErrFilenameUnrepresentable
	}
	if e.ObjectIdentity == "" || child.DirectoryIdentity != e.ObjectIdentity || child.DirectoryRevision == "" || child.RootIdentity != snapshot.Original.RootIdentity {
		return nil, ErrConflict
	}
	q := scanDirectoryRequest{Snapshot: snapshot, Parent: parent, CursorKind: inventoryLinuxCursor, NextCursor: page.NextCursor, RawNames: 1,
		ChildPath: string(e.Path), ChildIdentity: child.DirectoryIdentity, ChildRevision: child.DirectoryRevision, RawChildName: e.Name, RawChildPath: e.Path}
	if page.RootIdentity != snapshot.Original.RootIdentity || page.DirectoryIdentity != parent.Identity || page.DirectoryRevision != parent.Revision {
		return nil, ErrConflict
	}
	return newScanDirectoryCommand(InventoryDirectoryDiscover, id, q)
}

func newScanDirectoryEOFCommand(id CommandIdentity, snapshot inventoryClaimRequest, directory scanRootWitness, page nativeinventory.Page) (*scanDirectoryCommand, error) {
	if err := preflightDirectoryObservation(snapshot, directory, page, nativeinventory.Page{}); err != nil {
		return nil, err
	}
	if page.Version != 1 || page.ErrorCode != "" || len(page.Entries) != 0 || len(page.Issues) != 0 || !page.Complete || page.Unstable ||
		page.RootIdentity != snapshot.Original.RootIdentity || page.DirectoryIdentity != directory.Identity || page.DirectoryRevision != directory.Revision {
		return nil, ErrConflict
	}
	return newScanDirectoryCommand(InventoryDirectoryEOF, id, scanDirectoryRequest{Snapshot: snapshot, Parent: directory, CursorKind: inventoryLinuxCursor, NextCursor: page.NextCursor, Complete: true})
}

func newScanDirectoryCommand(kind CommandID, id CommandIdentity, q scanDirectoryRequest) (*scanDirectoryCommand, error) {
	if err := validateScanDirectoryRequest(kind, id, q); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(kind, 1, id, q)
	if err != nil {
		return nil, err
	}
	q.Snapshot.Original = cloneScanRunOriginalScope(q.Snapshot.Original)
	// Clone ALL typed text fields through the existing complete request walker.
	q.Snapshot.Run.phase = strings.Clone(q.Snapshot.Run.phase)
	q.Snapshot.Run.scanMode = strings.Clone(q.Snapshot.Run.scanMode)
	q.Snapshot.Root = cloneDirectoryWitness(q.Snapshot.Root)
	q.Snapshot.FinishedAt = strings.Clone(q.Snapshot.FinishedAt)
	q.Snapshot.JobStatus = strings.Clone(q.Snapshot.JobStatus)
	q.Snapshot.JobCreatedAt = strings.Clone(q.Snapshot.JobCreatedAt)
	q.Snapshot.JobErrorText = strings.Clone(q.Snapshot.JobErrorText)
	q.Parent = cloneDirectoryWitness(q.Parent)
	q.CursorKind = strings.Clone(q.CursorKind)
	q.NextCursor = strings.Clone(q.NextCursor)
	q.ChildPath = strings.Clone(q.ChildPath)
	q.ChildIdentity = strings.Clone(q.ChildIdentity)
	q.ChildRevision = strings.Clone(q.ChildRevision)
	q.RawChildName = bytes.Clone(q.RawChildName)
	q.RawChildPath = bytes.Clone(q.RawChildPath)
	return &scanDirectoryCommand{kind, id, q, digest, true}, nil
}

func cloneDirectoryWitness(v scanRootWitness) scanRootWitness {
	return scanRootWitness{strings.Clone(v.Path), strings.Clone(v.Identity), strings.Clone(v.Revision), strings.Clone(v.Cursor), strings.Clone(v.State), v.Pages, v.Skipped}
}

func directoryRequestTexts(q scanDirectoryRequest) []string {
	o, r, p, z := q.Snapshot.Original, q.Snapshot.Run, q.Parent, q.Snapshot.Root
	return []string{o.RunID, o.LibraryKey, o.SourceID, o.SourceIncarnation, o.RootIdentity, o.ScanMode, r.phase, r.scanMode,
		q.Snapshot.FinishedAt, q.Snapshot.JobStatus, q.Snapshot.JobCreatedAt, q.Snapshot.JobErrorText,
		z.Path, z.Identity, z.Revision, z.Cursor, z.State, p.Path, p.Identity, p.Revision, p.Cursor, p.State, q.CursorKind, q.NextCursor, q.ChildPath, q.ChildIdentity, q.ChildRevision}
}

func directoryRequestBytes(q scanDirectoryRequest) (uint64, error) {
	remaining := uint64(maxCanonicalRequestBytes)
	for _, s := range directoryRequestTexts(q) {
		if uint64(len(s)) > remaining {
			return 0, ErrCap
		}
		remaining -= uint64(len(s))
	}
	for _, b := range [][]byte{q.Snapshot.Original.ResolvedRoot, q.RawChildName, q.RawChildPath} {
		if uint64(len(b)) > remaining {
			return 0, ErrCap
		}
		remaining -= uint64(len(b))
	}
	return uint64(maxCanonicalRequestBytes) - remaining, nil
}

func validateScanDirectoryRequest(kind CommandID, id CommandIdentity, q scanDirectoryRequest) error {
	if _, err := directoryRequestBytes(q); err != nil {
		return err
	}
	for _, s := range directoryRequestTexts(q) {
		if !utf8.ValidString(s) || strings.IndexByte(s, 0) >= 0 {
			return ErrInvalid
		}
	}
	o, r, p := q.Snapshot.Original, q.Snapshot.Run, q.Parent
	if id.Sequence <= 0 || o.RunID == "" || o.LibraryID <= 0 || o.LibraryKey == "" || o.SourceID == "" || o.SourceGeneration <= 0 || o.SourceIncarnation == "" || o.RootIdentity == "" || o.PolicyRevision <= 0 || !scanLifecycleMode(o.ScanMode) ||
		!utf8.Valid(o.ResolvedRoot) || bytes.IndexByte(o.ResolvedRoot, 0) >= 0 || len(o.ResolvedRoot) == 0 || o.ResolvedRoot[0] != '/' || path.Clean(string(o.ResolvedRoot)) != string(o.ResolvedRoot) || o.FollowSymlinks ||
		r.revision <= 0 || r.policyRevision != o.PolicyRevision || r.scanMode != o.ScanMode || r.phase != "inventory" || r.leaseOwner <= 0 || r.leaseGeneration <= 0 || r.leaseExpiresMS <= 0 || r.lastSequence < 0 || r.acknowledgedSequence < 0 ||
		q.Snapshot.JobStatus != "running" || q.Snapshot.Authoritative != 0 || q.Snapshot.RootRebound != 0 || q.Snapshot.FinishedAt != "" ||
		q.Snapshot.Discovered < 0 || q.Snapshot.Processed < 0 || q.Snapshot.Root.Path != "." ||
		p.Path == "" || !validDirectoryRelative(p.Path) || p.Identity == "" || p.Revision == "" || p.State != "pending" || p.Pages < 0 || p.Skipped < 0 ||
		q.CursorKind != inventoryLinuxCursor || q.Unstable {
		return ErrInvalid
	}
	if _, err := inventoryFreshCookie(p.Cursor); err != nil {
		return err
	}
	next, err := inventoryFreshCookie(q.NextCursor)
	if err != nil {
		return err
	}
	switch kind {
	case InventoryDirectoryDiscover:
		old, _ := inventoryFreshCookie(p.Cursor)
		if q.RawNames != 1 || q.Complete || q.NextCursor == "" || old == next || q.ChildIdentity == "" || q.ChildRevision == "" ||
			!validDirectoryRelative(q.ChildPath) || q.ChildPath == "." || path.Dir(q.ChildPath) != p.Path ||
			string(q.RawChildPath) != q.ChildPath || string(q.RawChildName) != path.Base(q.ChildPath) {
			return ErrConflict
		}
	case InventoryDirectoryEOF:
		if q.RawNames != 0 || !q.Complete || q.ChildPath != "" || q.ChildIdentity != "" || q.ChildRevision != "" || len(q.RawChildName) != 0 || len(q.RawChildPath) != 0 {
			return ErrConflict
		}
	default:
		return ErrInvalid
	}
	return nil
}
func validDirectoryRelative(s string) bool {
	return s != "" && path.Clean(s) == s && s[0] != '/' && s != ".." && !strings.HasPrefix(s, "../")
}
func (c *scanDirectoryCommand) reference() ScanReceiptRequest {
	o := c.request.Snapshot.Original
	return ScanReceiptRequest{RunID: o.RunID, LibraryID: o.LibraryID, LibraryKey: o.LibraryKey, Receipt: ReceiptReference{c.identity, c.digest}}
}

// O(1) payload accounting before comparing native witnesses or converting path
// bytes. The independently supervised child observation is identity/revision
// proof only; none of its raw entries are consumed by discovery.
func preflightDirectoryObservation(s inventoryClaimRequest, parent scanRootWitness, page, child nativeinventory.Page) error {
	q := scanDirectoryRequest{Snapshot: s, Parent: parent, CursorKind: inventoryLinuxCursor, NextCursor: page.NextCursor}
	n, err := directoryRequestBytes(q)
	if err != nil {
		return err
	}
	for _, v := range []string{page.RootIdentity, page.DirectoryIdentity, page.DirectoryRevision, child.RootIdentity, child.DirectoryIdentity, child.DirectoryRevision} {
		if uint64(len(v)) > uint64(maxCanonicalRequestBytes)-n {
			return ErrCap
		}
		n += uint64(len(v))
	}
	if len(page.Entries) == 1 {
		e := page.Entries[0]
		for _, v := range []uint64{uint64(len(e.Path)), uint64(len(e.Name)), uint64(len(e.ObjectIdentity))} {
			if v > uint64(maxCanonicalRequestBytes)-n {
				return ErrCap
			}
			n += v
		}
	}
	return nil
}
