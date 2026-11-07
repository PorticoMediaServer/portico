package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"math"
	"reflect"
)

func directorySnapshot(q scanDirectoryRequest, id CommandIdentity) scanRunSnapshot {
	s := q.Snapshot
	r := s.Run
	return scanRunSnapshot{run: scanRunNative{scope: scanNativeOriginal(s.Original, id.Incarnation), phase: r.phase, finishedAt: s.FinishedAt,
		authoritative: s.Authoritative, rootRebound: s.RootRebound, discovered: s.Discovered, leaseOwner: r.leaseOwner, leaseGeneration: r.leaseGeneration, leaseExpiresMS: r.leaseExpiresMS,
		attempts: s.Attempts, nextAttemptMS: sql.NullInt64{Int64: s.NextAttemptMS.Value, Valid: s.NextAttemptMS.Present}, revision: r.revision, lastSequence: r.lastSequence, acknowledgedSequence: r.acknowledgedSequence},
		job:  scanRunJob{libraryKey: s.Original.LibraryKey, status: s.JobStatus, createdAt: s.JobCreatedAt, errorText: s.JobErrorText, processed: s.Processed},
		root: scanRunRoot{identity: s.Root.Identity, revision: s.Root.Revision, cursor: s.Root.Cursor, state: s.Root.State, pages: s.Root.Pages, skipped: s.Root.Skipped}}
}

// SAME admitted frame as history/current disclosure. No admission, transaction,
// callback, syscall, clock substitute or partial Applied boundary is introduced.
func applyScanDirectory(ctx context.Context, t *commandTurn, c *scanDirectoryCommand, original scanRunOriginalScope) (out scanDirectorySaved, digest ResultDigest, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(t), struct {
			Value  scanDirectorySaved
			Digest ResultDigest
		}{out, digest}, err)
		out, digest, err = v.Value, v.Digest, e
	}()
	if c == nil || !c.valid || t == nil || t.transaction == nil || t.transaction.descriptor.ID != c.purpose {
		return out, digest, ErrInvalid
	}
	q := c.request
	s := q.Snapshot
	r := s.Run
	o := s.Original
	if err = validateScanDirectoryRequest(c.purpose, c.identity, q); err != nil {
		return out, digest, err
	}
	actualDigest, e := canonicalRequestDigest(c.purpose, 1, c.identity, q)
	if e != nil || actualDigest != c.digest || !reflect.DeepEqual(o, original) {
		return out, digest, errors.Join(ErrConflict, e)
	}
	if r.revision == math.MaxInt64 || r.lastSequence == math.MaxInt64 || q.Parent.Pages == math.MaxInt64 || r.acknowledgedSequence != r.lastSequence || c.identity.Sequence != r.lastSequence+1 {
		return out, digest, ErrConflict
	}
	n, err := directoryRequestBytes(q)
	if err != nil {
		return out, digest, err
	}
	for _, point := range []struct {
		id   stmt.ID
		args []any
	}{{scanDirectoryRunLengths, []any{o.RunID}}, {scanDirectoryJobLengths, []any{o.RunID}}, {scanDirectoryRootLengths, []any{o.RunID, "."}}, {scanDirectoryRootLengths, []any{o.RunID, q.Parent.Path}}, {scanSourceLengths, []any{o.SourceID, o.LibraryKey}}, {scanLibraryRootLengths, []any{o.LibraryID, o.LibraryKey}}} {
		size, e := directoryNativeBytes(ctx, t, point.id, point.args...)
		if e != nil {
			return out, digest, errors.Join(ErrConflict, e)
		}
		n += size
	}
	if err = directoryMemory(ctx, t, n); err != nil {
		return out, digest, err
	}
	before, err := readScanRunSnapshot(ctx, t, o.RunID)
	if err != nil || before != directorySnapshot(q, c.identity) {
		return out, digest, errors.Join(ErrConflict, err)
	}
	var active int64
	if err = directoryPoint(ctx, t, scanDirectoryActive, []any{o.SourceID, o.RunID}, &active); err != nil || active != 1 {
		return out, digest, err
	}
	source, err := captureScanSource(ctx, t, o.LibraryID, o.LibraryKey, o.SourceID)
	if err != nil {
		return out, digest, err
	}
	if source.policyRevision != o.PolicyRevision || source.source.generation != o.SourceGeneration || source.source.incarnation != o.SourceIncarnation || source.source.rootIdentity != o.RootIdentity || source.source.followSymlinks != o.FollowSymlinks || !bytes.Equal(source.source.resolvedRoot, o.ResolvedRoot) {
		return out, digest, ErrConflict
	}
	var sourceID string
	err = directoryPoint(ctx, t, scanFreshSource, []any{o.SourceID, o.LibraryKey, o.SourceGeneration, o.SourceIncarnation, o.RootIdentity, string(o.ResolvedRoot), int64(0)}, &sourceID)
	if err != nil || sourceID != o.SourceID {
		return out, digest, errors.Join(ErrConflict, err)
	}
	parent, err := scanLifecycleRootQuery(ctx, t, scanLifecycleRoot, o.RunID, q.Parent.Path)
	if err != nil || parent != (scanRunRoot{identity: q.Parent.Identity, revision: q.Parent.Revision, cursor: q.Parent.Cursor, state: q.Parent.State, pages: q.Parent.Pages, skipped: q.Parent.Skipped}) {
		return out, digest, errors.Join(ErrConflict, err)
	}
	applied, err := t.commandTimeMS(ctx, c.purpose, r.leaseExpiresMS)
	if err != nil {
		return out, digest, err
	}
	if s.NextAttemptMS.Present && s.NextAttemptMS.Value > applied {
		return out, digest, ErrConflict
	}
	var directory scanRootWitness
	if c.purpose == InventoryDirectoryDiscover {
		for _, point := range []struct {
			id    stmt.ID
			value string
		}{{scanDirectoryPath, q.ChildPath}, {scanDirectoryVisited, q.ChildIdentity}} {
			rows, e := t.query(ctx, t.steps[point.id].statementStep, o.RunID, point.value)
			if e != nil {
				return out, digest, e
			}
			exists := rows.Next()
			e = errors.Join(rows.Err(), rows.Close())
			if e != nil || exists {
				return out, digest, errors.Join(ErrConflict, e)
			}
		}
		err = directoryPoint(ctx, t, scanDirectoryInsert, []any{o.RunID, q.ChildPath, q.ChildIdentity, q.ChildRevision}, &directory.Path, &directory.Identity, &directory.Revision, &directory.Cursor, &directory.State, &directory.Pages, &directory.Skipped)
		if err != nil || directory != (scanRootWitness{Path: q.ChildPath, Identity: q.ChildIdentity, Revision: q.ChildRevision, State: "pending"}) {
			return out, digest, errors.Join(ErrConflict, err)
		}
	}
	cas := scanDirectoryDiscoverCAS
	state := "pending"
	if c.purpose == InventoryDirectoryEOF {
		cas = scanDirectoryEOFCAS
		state = "done"
	}
	var post scanRootWitness
	err = directoryPoint(ctx, t, cas, []any{q.NextCursor, q.Parent.Pages + 1, o.RunID, q.Parent.Path, q.Parent.Identity, q.Parent.Revision, q.Parent.Cursor, q.Parent.Pages, q.Parent.Skipped}, &post.Path, &post.Identity, &post.Revision, &post.Cursor, &post.State, &post.Pages, &post.Skipped)
	want := q.Parent
	want.Cursor, want.Pages, want.State = q.NextCursor, q.Parent.Pages+1, state
	if err != nil || post != want {
		return out, digest, errors.Join(ErrConflict, err)
	}
	var revision, sequence int64
	err = directoryPoint(ctx, t, scanDirectoryRunCAS, []any{r.revision + 1, c.identity.Sequence, o.RunID, c.identity.Incarnation[:], r.revision, r.lastSequence, r.acknowledgedSequence, r.leaseOwner, r.leaseGeneration, r.leaseExpiresMS}, &revision, &sequence)
	if err != nil || revision != r.revision+1 || sequence != c.identity.Sequence {
		return out, digest, errors.Join(ErrConflict, err)
	}
	if c.purpose == InventoryDirectoryEOF {
		directory = post
	}
	out = scanDirectorySaved{Original: original, AcceptedRunRevision: revision, AppliedMS: applied, Directory: directory, ParentPath: q.Parent.Path, ParentIdentity: q.Parent.Identity, ParentRevision: q.Parent.Revision, ExpectedCursor: q.Parent.Cursor, NextCursor: post.Cursor}
	var pages, skipped any
	if c.purpose == InventoryDirectoryDiscover {
		out.ParentPages, out.ParentSkipped = post.Pages, post.Skipped
		pages, skipped = post.Pages, post.Skipped
	}
	digest, err = directoryResultDigest(c.purpose, c.identity, c.digest, out)
	if err != nil {
		return out, digest, err
	}
	kind, _ := Describe(c.purpose)
	_, err = t.exec(ctx, t.steps[scanDirectoryReceiptInsert].statementStep, o.RunID, c.identity.Incarnation[:], c.identity.Sequence, c.identity.OperationID[:], c.digest[:], kind.Name, revision, applied, o.SourceID, o.SourceIncarnation, nil, post.Cursor, int64(0), digest[:], inventoryLinuxCursor, []byte(q.Parent.Path), []byte(q.Parent.Identity), []byte(q.Parent.Revision), []byte(q.Parent.Cursor), []byte(post.Cursor), directory.Path, directory.Identity, directory.Revision, directory.Cursor, directory.State, directory.Pages, directory.Skipped, pages, skipped)
	if err != nil {
		return out, digest, err
	}
	// Verify the immutable native receipt and its zero-member cohort immediately;
	// output is decoded from stored rows, never echoed from the request.
	saved, err := lookupScanDirectory(ctx, t, c.reference(), c.purpose)
	if err != nil || saved.decision != receiptReplay || saved.digest != digest || !reflect.DeepEqual(saved.result, out) {
		return out, digest, errors.Join(ErrResolveRequired, err)
	}
	return saved.result, saved.digest, nil
}
