package writer

import "unicode/utf8"

type scanDirectorySaved struct {
	Original                                                               scanRunOriginalScope
	AcceptedRunRevision, AppliedMS                                         int64
	Directory                                                              scanRootWitness
	ParentPath, ParentIdentity, ParentRevision, ExpectedCursor, NextCursor string
	ParentPages, ParentSkipped                                             int64 // populated ONLY by discovery
}
type inventoryDirectoryDiscoverResult struct{ facts scanDirectorySaved }
type inventoryDirectoryEOFResult struct{ facts scanDirectorySaved }
type scanDirectoryCommandResult struct {
	Outcome ReceiptOutcome
	Value   scanDirectorySaved
	Digest  ResultDigest
}

func directoryResultDigest(kind CommandID, id CommandIdentity, request RequestDigest, v scanDirectorySaved) (ResultDigest, error) {
	switch kind {
	case InventoryDirectoryDiscover:
		return canonicalResultDigest(id, request, inventoryDirectoryDiscoverResult{v})
	case InventoryDirectoryEOF:
		return canonicalResultDigest(id, request, inventoryDirectoryEOFResult{v})
	default:
		return ResultDigest{}, ErrInvalid
	}
}
func preflightDirectoryResult(kind CommandID, v scanDirectorySaved) error {
	if v.AcceptedRunRevision <= 0 || v.AppliedMS < 0 || v.Original.LibraryID <= 0 || v.Original.SourceGeneration <= 0 || v.Original.PolicyRevision <= 0 || v.Directory.Pages < 0 || v.Directory.Skipped < 0 {
		return ErrInvalid
	}
	texts := []string{v.Original.RunID, v.Original.LibraryKey, v.Original.SourceID, v.Original.SourceIncarnation, v.Original.RootIdentity, v.Original.ScanMode, v.Directory.Path, v.Directory.Identity, v.Directory.Revision, v.Directory.Cursor, v.Directory.State, v.ParentPath, v.ParentIdentity, v.ParentRevision, v.ExpectedCursor, v.NextCursor}
	remaining := maxCanonicalResultBytes - 4096 // complete fixed scalar/LP envelope
	for _, s := range texts {
		if len(s) > remaining {
			return ErrCap
		}
		remaining -= len(s)
	}
	if len(v.Original.ResolvedRoot) > remaining {
		return ErrCap
	}
	for _, s := range texts {
		if !utf8.ValidString(s) {
			return ErrInvalid
		}
	}
	if !utf8.Valid(v.Original.ResolvedRoot) || len(v.Original.ResolvedRoot) == 0 {
		return ErrInvalid
	}
	if v.Original.RunID == "" || v.Original.LibraryKey == "" || v.Original.SourceID == "" || v.Original.SourceIncarnation == "" || v.Original.RootIdentity == "" || !scanLifecycleMode(v.Original.ScanMode) ||
		!validDirectoryRelative(v.Directory.Path) || v.Directory.Identity == "" || v.Directory.Revision == "" || !validDirectoryRelative(v.ParentPath) || v.ParentIdentity == "" || v.ParentRevision == "" {
		return ErrInvalid
	}
	if _, err := inventoryFreshCookie(v.ExpectedCursor); err != nil {
		return err
	}
	next, err := inventoryFreshCookie(v.NextCursor)
	if err != nil {
		return err
	}
	switch kind {
	case InventoryDirectoryDiscover:
		old, _ := inventoryFreshCookie(v.ExpectedCursor)
		if v.Directory.State != "pending" || v.Directory.Cursor != "" || v.Directory.Pages != 0 || v.Directory.Skipped != 0 || v.Directory.Path == v.ParentPath || v.NextCursor == "" || old == next || v.ParentPages <= 0 || v.ParentSkipped < 0 {
			return ErrInvalid
		}
	case InventoryDirectoryEOF:
		if v.Directory.Path != v.ParentPath || v.Directory.Identity != v.ParentIdentity || v.Directory.Revision != v.ParentRevision || v.Directory.State != "done" || v.Directory.Pages <= 0 || v.Directory.Cursor != v.NextCursor || v.ParentPages != 0 || v.ParentSkipped != 0 {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
func writeDirectoryResult(e *requestEncoder, kind CommandID, id CommandIdentity, request RequestDigest, v scanDirectorySaved) error {
	name := "inventory_directory_discover"
	if kind == InventoryDirectoryEOF {
		name = "inventory_directory_eof"
	}
	o := v.Original
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text(name)
	e.text("directory")
	e.text("applied")
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(request[:])
	e.text(o.RunID)
	e.number(uint64(o.LibraryID))
	e.text(o.LibraryKey)
	e.text(o.SourceID)
	e.number(uint64(o.SourceGeneration))
	e.text(o.SourceIncarnation)
	e.text(o.RootIdentity)
	e.number(uint64(len(o.ResolvedRoot)))
	e.bytes(o.ResolvedRoot)
	e.flag(o.FollowSymlinks)
	e.number(uint64(o.PolicyRevision))
	e.text(o.ScanMode)
	e.bytes(o.CreateOperationID[:])
	e.bytes(o.CreateRequestDigest[:])
	e.number(uint64(v.AcceptedRunRevision))
	e.number(uint64(v.AppliedMS))
	e.bytes([]byte{0}) // source_revision SQL NULL
	e.text(inventoryLinuxCursor)
	e.text(v.ParentPath)
	e.text(v.ParentIdentity)
	e.text(v.ParentRevision)
	e.text(v.ExpectedCursor)
	e.text(v.NextCursor)
	d := v.Directory
	e.text(d.Path)
	e.text(d.Identity)
	e.text(d.Revision)
	e.text(d.Cursor)
	e.text(d.State)
	e.number(uint64(d.Pages))
	e.number(uint64(d.Skipped))
	e.flag(kind == InventoryDirectoryDiscover)
	if kind == InventoryDirectoryDiscover {
		e.number(uint64(v.ParentPages))
		e.number(uint64(v.ParentSkipped))
	}
	e.number(0)
	e.number(0) // honest result_count and member cohort
	return e.err
}
