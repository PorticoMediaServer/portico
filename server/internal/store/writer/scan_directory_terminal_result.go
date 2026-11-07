package writer

import (
	"bytes"
	"path"
	"strings"
	"time"
	"unicode/utf8"
)

type scanDirectoryTerminalSaved struct {
	Original                                                               scanRunOriginalScope
	AcceptedRunRevision, AppliedMS                                         int64
	Directory                                                              scanRootWitness // traversal ONLY: actual original terminal root
	InventoryCompletedAt                                                   string
	TraversalBlocked, TraversalProtected                                   bool
	RefusedRelativePath                                                    []byte
	RefusedDirectoryIdentity, RefusedDirectoryRevision                     []byte
	ParentPath, ParentIdentity, ParentRevision, ExpectedCursor, NextCursor string
}
type inventoryDirectoryRefuseResult struct{ facts scanDirectoryTerminalSaved }
type inventoryTraversalCompleteResult struct{ facts scanDirectoryTerminalSaved }
type scanDirectoryTerminalCommandResult struct {
	Outcome ReceiptOutcome
	Value   scanDirectoryTerminalSaved
	Digest  ResultDigest
}

func directoryTerminalResultDigest(kind CommandID, id CommandIdentity, request RequestDigest, v scanDirectoryTerminalSaved) (ResultDigest, error) {
	switch kind {
	case InventoryDirectoryRefuse:
		return canonicalResultDigest(id, request, inventoryDirectoryRefuseResult{v})
	case InventoryTraversalComplete:
		return canonicalResultDigest(id, request, inventoryTraversalCompleteResult{v})
	default:
		return ResultDigest{}, ErrInvalid
	}
}

// Hooks for the OWNER's closed canonicalResultDigest preflight/write switches.
// No independent encoder, callback, wire digest or alternate result dispatcher.
func preflightDirectoryTerminalResult(kind CommandID, v scanDirectoryTerminalSaved) error {
	o := v.Original
	texts := []string{o.RunID, o.LibraryKey, o.SourceID, o.SourceIncarnation, o.RootIdentity, o.ScanMode, v.Directory.Path, v.Directory.Identity, v.Directory.Revision, v.Directory.Cursor, v.Directory.State, v.InventoryCompletedAt, v.ParentPath, v.ParentIdentity, v.ParentRevision, v.ExpectedCursor, v.NextCursor}
	remaining := maxCanonicalResultBytes - 4096
	for _, s := range texts {
		if len(s) > remaining {
			return ErrCap
		}
		remaining -= len(s)
	}
	for _, b := range [][]byte{o.ResolvedRoot, v.RefusedRelativePath, v.RefusedDirectoryIdentity, v.RefusedDirectoryRevision} {
		if len(b) > remaining {
			return ErrCap
		}
		remaining -= len(b)
	}
	for _, s := range texts {
		if !utf8.ValidString(s) || strings.IndexByte(s, 0) >= 0 {
			return ErrInvalid
		}
	}
	if o.RunID == "" || o.LibraryID <= 0 || o.LibraryKey == "" || o.SourceID == "" || o.SourceGeneration <= 0 || o.SourceIncarnation == "" || o.RootIdentity == "" || o.PolicyRevision <= 0 || !scanLifecycleMode(o.ScanMode) || o.FollowSymlinks || !utf8.Valid(o.ResolvedRoot) || len(o.ResolvedRoot) == 0 || bytes.IndexByte(o.ResolvedRoot, 0) >= 0 || o.ResolvedRoot[0] != '/' || path.Clean(string(o.ResolvedRoot)) != string(o.ResolvedRoot) || v.AcceptedRunRevision <= 0 || v.AppliedMS < 0 {
		return ErrInvalid
	}
	switch kind {
	case InventoryDirectoryRefuse:
		if v.Directory != (scanRootWitness{}) || v.InventoryCompletedAt != "" || v.TraversalBlocked || v.TraversalProtected || !validDirectoryRelative(v.ParentPath) || v.ParentIdentity == "" || v.ParentRevision == "" || len(v.RefusedDirectoryIdentity) == 0 || len(v.RefusedDirectoryRevision) == 0 || !utf8.Valid(v.RefusedDirectoryIdentity) || !utf8.Valid(v.RefusedDirectoryRevision) || bytes.IndexByte(v.RefusedDirectoryIdentity, 0) >= 0 || bytes.IndexByte(v.RefusedDirectoryRevision, 0) >= 0 {
			return ErrInvalid
		}
		old, err := inventoryFreshCookie(v.ExpectedCursor)
		if err != nil {
			return err
		}
		next, err := inventoryFreshCookie(v.NextCursor)
		if err != nil {
			return err
		}
		if v.NextCursor == "" || old == next {
			return ErrInvalid
		}
		i := bytes.LastIndexByte(v.RefusedRelativePath, '/')
		if !validTerminalRawChild(v.ParentPath, v.RefusedRelativePath[i+1:], v.RefusedRelativePath) {
			return ErrInvalid
		}
		_, err = terminalDiagnostic(v.RefusedRelativePath)
		return err
	case InventoryTraversalComplete:
		d := v.Directory
		if d.Path != "." || d.Identity == "" || d.Revision == "" || d.State != "done" && d.State != "blocked" || d.Pages < 0 || d.Skipped < 0 || d.Skipped > d.Pages || d.State == "blocked" && !v.TraversalBlocked || len(v.RefusedRelativePath) != 0 || len(v.RefusedDirectoryIdentity) != 0 || len(v.RefusedDirectoryRevision) != 0 || v.ParentPath != "" || v.ParentIdentity != "" || v.ParentRevision != "" || v.ExpectedCursor != "" || v.NextCursor != "" {
			return ErrInvalid
		}
		if _, err := inventoryFreshCookie(d.Cursor); err != nil {
			return err
		}
		tm, err := time.Parse("2006-01-02T15:04:05.000000000Z", v.InventoryCompletedAt)
		if err != nil || len(v.InventoryCompletedAt) != 30 || tm.UnixMilli() != v.AppliedMS || tm.UTC().Format("2006-01-02T15:04:05.000000000Z") != v.InventoryCompletedAt {
			return ErrInvalid
		}
		return nil
	default:
		return ErrInvalid
	}
}

func writeDirectoryTerminalResult(e *requestEncoder, kind CommandID, id CommandIdentity, request RequestDigest, v scanDirectoryTerminalSaved) error {
	name := "inventory_directory_refuse"
	if kind == InventoryTraversalComplete {
		name = "inventory_traversal_complete"
	} else if kind != InventoryDirectoryRefuse {
		return ErrInvalid
	}
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text(name)
	e.text("directory_terminal")
	e.text("applied")
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(request[:])
	o := v.Original
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
	if kind == InventoryDirectoryRefuse {
		e.text("directory")
		e.text("filename_unrepresentable")
		for _, b := range [][]byte{v.RefusedRelativePath, v.RefusedDirectoryIdentity, v.RefusedDirectoryRevision} {
			e.number(uint64(len(b)))
			e.bytes(b)
		}
		e.text(inventoryLinuxCursor)
		e.text(v.ParentPath)
		e.text(v.ParentIdentity)
		e.text(v.ParentRevision)
		e.text(v.ExpectedCursor)
		e.text(v.NextCursor)
	} else {
		d := v.Directory
		e.text(d.Path)
		e.text(d.Identity)
		e.text(d.Revision)
		e.text(d.Cursor)
		e.text(d.State)
		e.number(uint64(d.Pages))
		e.number(uint64(d.Skipped))
		e.text(v.InventoryCompletedAt)
		e.flag(v.TraversalBlocked)
		e.flag(v.TraversalProtected)
	}
	e.number(0)
	e.number(0) // honest result_count and member cohort
	return e.err
}

func directoryTerminalHistoricalResult(q ScanReceiptRequest, l scanDirectoryTerminalLookup) scanDirectoryTerminalCommandResult {
	out := scanDirectoryTerminalCommandResult{Outcome: ReceiptOutcome{Reference: q.Receipt}}
	switch l.decision {
	case receiptFresh:
		out.Outcome.State = Pending
	case receiptRetired:
		out.Outcome.State = ResultRetired
	case receiptReplay:
		out.Outcome.State = Applied
		out.Outcome.Replayed = true
		out.Value, out.Digest = l.result, l.digest
	}
	return out
}
