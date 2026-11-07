package writer

import (
	"bytes"
	"context"
	"errors"
	"math"
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Caller has completed current disclosure + lookupScanDirectoryTerminal and
// selected receiptFresh in this SAME admitted transaction. original is the
// native history owner scope, never a current-source reconstruction.
func applyScanDirectoryTerminal(ctx context.Context, t *commandTurn, c *scanDirectoryTerminalCommand, original scanRunOriginalScope) (out scanDirectoryTerminalSaved, digest ResultDigest, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(t), struct {
			Value  scanDirectoryTerminalSaved
			Digest ResultDigest
		}{out, digest}, err)
		out, digest, err = v.Value, v.Digest, e
	}()
	if c == nil || !c.valid || t == nil || t.transaction == nil || t.transaction.descriptor.ID != c.purpose {
		return out, digest, ErrInvalid
	}
	q := c.request
	s, o, r := q.Snapshot, q.Snapshot.Original, q.Snapshot.Run
	if err = validateScanDirectoryTerminalRequest(c.purpose, c.identity, q); err != nil {
		return out, digest, err
	}
	actual, e := canonicalRequestDigest(c.purpose, 1, c.identity, q)
	if e != nil || actual != c.digest || !reflect.DeepEqual(o, original) {
		return out, digest, errors.Join(ErrConflict, e)
	}
	if r.revision == math.MaxInt64 || r.lastSequence == math.MaxInt64 || r.acknowledgedSequence != r.lastSequence || c.identity.Sequence != r.lastSequence+1 || c.purpose == InventoryDirectoryRefuse && (q.Warnings == math.MaxInt64 || q.Parent.Pages == math.MaxInt64 || q.Parent.Skipped == math.MaxInt64) {
		return out, digest, ErrConflict
	}
	n, err := terminalRequestBytes(q)
	if err != nil {
		return out, digest, err
	}
	points := []struct {
		id   stmt.ID
		args []any
	}{
		{scanDirectoryRunLengths, []any{o.RunID}}, {scanDirectoryJobLengths, []any{o.RunID}}, {scanDirectoryRootLengths, []any{o.RunID, "."}},
		{scanSourceLengths, []any{o.SourceID, o.LibraryKey}}, {scanLibraryRootLengths, []any{o.LibraryID, o.LibraryKey}},
	}
	if c.purpose == InventoryDirectoryRefuse {
		points = append(points, struct {
			id   stmt.ID
			args []any
		}{scanDirectoryRootLengths, []any{o.RunID, q.Parent.Path}})
	}
	for _, p := range points {
		size, e := directoryNativeBytes(ctx, t, p.id, p.args...)
		if e != nil {
			return out, digest, errors.Join(ErrConflict, e)
		}
		n += size
	}
	if err = directoryMemory(ctx, t, n); err != nil {
		return out, digest, err
	}
	before, err := readScanRunSnapshot(ctx, t, o.RunID)
	if err != nil || before != directorySnapshot(terminalRequestProjection(q), c.identity) {
		return out, digest, errors.Join(ErrConflict, err)
	}
	var warnings int64
	if err = directoryPoint(ctx, t, scanTerminalRunState, []any{o.RunID}, &warnings); err != nil || warnings < 0 || c.purpose == InventoryDirectoryRefuse && warnings != q.Warnings {
		return out, digest, errors.Join(ErrConflict, err)
	}
	exists, err := terminalExists(ctx, t, scanDirectoryActive, o.SourceID, o.RunID)
	if err != nil || !exists {
		return out, digest, errors.Join(ErrConflict, err)
	}
	source, err := captureScanSource(ctx, t, o.LibraryID, o.LibraryKey, o.SourceID)
	if err != nil {
		return out, digest, err
	}
	if source.policyRevision != o.PolicyRevision || source.source.generation != o.SourceGeneration || source.source.incarnation != o.SourceIncarnation || source.source.rootIdentity != o.RootIdentity || source.source.followSymlinks != o.FollowSymlinks || !bytes.Equal(source.source.resolvedRoot, o.ResolvedRoot) {
		return out, digest, ErrConflict
	}
	var sourceID string
	if err = directoryPoint(ctx, t, scanFreshSource, []any{o.SourceID, o.LibraryKey, o.SourceGeneration, o.SourceIncarnation, o.RootIdentity, string(o.ResolvedRoot), int64(0)}, &sourceID); err != nil || sourceID != o.SourceID {
		return out, digest, errors.Join(ErrConflict, err)
	}
	// Shared clock validates owned high-water, original lease and full deadline.
	// Read that EXACT sample immediately; never re-sample or reconstruct UTC9
	// from truncated milliseconds for inventory_completed_at.
	applied, err := t.commandTimeMS(ctx, c.purpose, r.leaseExpiresMS)
	if err != nil {
		return out, digest, err
	}
	sampled := t.transaction.clockHighWater
	if sampled.UnixMilli() != applied {
		return out, digest, ErrInvalid
	}
	if s.NextAttemptMS.Present && s.NextAttemptMS.Value > applied {
		return out, digest, ErrConflict
	}
	casArgs := []any{r.revision + 1, c.identity.Sequence, o.RunID, c.identity.Incarnation[:], r.revision, r.lastSequence, r.acknowledgedSequence, r.leaseOwner, r.leaseGeneration, r.leaseExpiresMS}
	var revision, sequence int64
	var receiptID stmt.ID
	var receiptArgs []any
	common := func(next string) []any {
		name := "inventory_directory_refuse"
		if c.purpose == InventoryTraversalComplete {
			name = "inventory_traversal_complete"
		}
		return []any{o.RunID, c.identity.Incarnation[:], c.identity.Sequence, c.identity.OperationID[:], c.digest[:], name, revision, applied, o.SourceID, o.SourceIncarnation, nil, next, int64(0), digest[:]}
	}
	if c.purpose == InventoryDirectoryRefuse {
		parent, err := scanLifecycleRootQuery(ctx, t, scanLifecycleRoot, o.RunID, q.Parent.Path)
		want := scanRunRoot{identity: q.Parent.Identity, revision: q.Parent.Revision, cursor: q.Parent.Cursor, state: q.Parent.State, pages: q.Parent.Pages, skipped: q.Parent.Skipped}
		if err != nil || parent != want {
			return out, digest, errors.Join(ErrConflict, err)
		}
		display, err := terminalDiagnostic(q.RefusedRelativePath)
		if err != nil {
			return out, digest, err
		}
		// Same display of EITHER refused kind conflicts, including another pin.
		// Historical same-original replay was already selected before this body.
		for _, p := range []struct {
			id   stmt.ID
			args []any
		}{{scanTerminalIssueConflict, []any{o.RunID, display}}, {scanDirectoryVisited, []any{o.RunID, q.ChildIdentity}}} {
			found, e := terminalExists(ctx, t, p.id, p.args...)
			if e != nil || found {
				return out, digest, errors.Join(ErrConflict, e)
			}
		}
		var post scanRootWitness
		err = directoryPoint(ctx, t, scanTerminalParentCAS, []any{q.NextCursor, q.Parent.Pages + 1, q.Parent.Skipped + 1, o.RunID, q.Parent.Path, parent.identity, parent.revision, parent.cursor, parent.pages, parent.skipped}, &post.Path, &post.Identity, &post.Revision, &post.Cursor, &post.State, &post.Pages, &post.Skipped)
		wantPost := q.Parent
		wantPost.Cursor, wantPost.Pages, wantPost.Skipped = q.NextCursor, q.Parent.Pages+1, q.Parent.Skipped+1
		if err != nil || post != wantPost {
			return out, digest, errors.Join(ErrConflict, err)
		}
		var postWarnings int64
		args := append(append([]any{warnings + 1}, casArgs...), warnings)
		err = directoryPoint(ctx, t, scanTerminalRefuseRunCAS, args, &revision, &sequence, &postWarnings)
		if err != nil || revision != r.revision+1 || sequence != c.identity.Sequence || postWarnings != warnings+1 {
			return out, digest, errors.Join(ErrConflict, err)
		}
		out = scanDirectoryTerminalSaved{Original: original, AcceptedRunRevision: revision, AppliedMS: applied, RefusedRelativePath: q.RefusedRelativePath, RefusedDirectoryIdentity: []byte(q.ChildIdentity), RefusedDirectoryRevision: []byte(q.ChildRevision), ParentPath: post.Path, ParentIdentity: post.Identity, ParentRevision: post.Revision, ExpectedCursor: parent.cursor, NextCursor: post.Cursor}
		digest, err = directoryTerminalResultDigest(c.purpose, c.identity, c.digest, out)
		if err != nil {
			return out, digest, err
		}
		receiptID = scanTerminalRefuseReceipt
		receiptArgs = append(common(post.Cursor), out.RefusedRelativePath, "filename_unrepresentable", "directory", out.RefusedDirectoryIdentity, out.RefusedDirectoryRevision, inventoryLinuxCursor, []byte(post.Path), []byte(post.Identity), []byte(post.Revision), []byte(parent.cursor), []byte(post.Cursor))
	} else {
		// Closed native directory state means pending includes every unresolved
		// or in-flight durable row. No in-memory DFS/empty-cursor substitution.
		pending, e := terminalExists(ctx, t, scanTerminalPending, o.RunID)
		if e != nil || pending {
			return out, digest, errors.Join(ErrConflict, e)
		}
		blocked, e := terminalExists(ctx, t, scanTerminalBlocked, o.RunID)
		if e != nil {
			return out, digest, e
		}
		protected, e := terminalExists(ctx, t, scanTerminalProtected, o.RunID)
		if e != nil {
			return out, digest, e
		}
		completed := sampled.UTC().Format("2006-01-02T15:04:05.000000000Z")
		var stored string
		err = directoryPoint(ctx, t, scanTerminalTraversalCAS, append([]any{completed}, casArgs...), &revision, &sequence, &stored)
		if err != nil || revision != r.revision+1 || sequence != c.identity.Sequence || stored != completed {
			return out, digest, errors.Join(ErrConflict, err)
		}
		// Re-read ACTUAL original root after the listing-only CAS.
		size, e := directoryNativeBytes(ctx, t, scanDirectoryRootLengths, o.RunID, ".")
		if e != nil {
			return out, digest, e
		}
		if err = directoryMemory(ctx, t, n+size); err != nil {
			return out, digest, err
		}
		root, e := scanLifecycleRootQuery(ctx, t, scanLifecycleRoot, o.RunID, ".")
		if e != nil || root != before.root {
			return out, digest, errors.Join(ErrConflict, e)
		}
		out = scanDirectoryTerminalSaved{Original: original, AcceptedRunRevision: revision, AppliedMS: applied, Directory: scanRootWitness{Path: ".", Identity: root.identity, Revision: root.revision, Cursor: root.cursor, State: root.state, Pages: root.pages, Skipped: root.skipped}, InventoryCompletedAt: stored, TraversalBlocked: blocked, TraversalProtected: protected}
		digest, err = directoryTerminalResultDigest(c.purpose, c.identity, c.digest, out)
		if err != nil {
			return out, digest, err
		}
		receiptID = scanTerminalTraversalReceipt
		b, p := int64(0), int64(0)
		if blocked {
			b = 1
		}
		if protected {
			p = 1
		}
		receiptArgs = append(common(""), out.Directory.Path, root.identity, root.revision, root.cursor, root.state, root.pages, root.skipped, stored, b, p)
	}
	// Receipt FIRST: the following strict issue INSERT references its immutable
	// (run,operation,directory) parent through the adopted NO ACTION FK.
	changed, err := t.exec(ctx, t.steps[receiptID].statementStep, receiptArgs...)
	if err != nil {
		return out, digest, err
	}
	count, err := changed.RowsAffected()
	if err != nil || count != 1 {
		return out, digest, errors.Join(ErrConflict, err)
	}
	if c.purpose == InventoryDirectoryRefuse {
		display, e := terminalDiagnostic(out.RefusedRelativePath)
		if e != nil {
			return out, digest, e
		}
		var path, reason, kind string
		var protect, warning int64
		var operation []byte
		err = directoryPoint(ctx, t, scanTerminalIssueInsert, []any{o.RunID, display, c.identity.OperationID[:]}, &path, &reason, &protect, &warning, &kind, &operation)
		if err != nil || path != display || reason != "filename_unrepresentable" || protect != 1 || warning != 1 || kind != "directory" || !bytes.Equal(operation, c.identity.OperationID[:]) {
			return out, digest, errors.Join(ErrConflict, err)
		}
	}
	// Return ONLY decoded saved native values, never a request echo. Any error
	// clears every output; the owner's shared frame rolls back the entire cohort.
	saved, err := lookupScanDirectoryTerminal(ctx, t, c.reference(), c.purpose)
	if err != nil || saved.decision != receiptReplay || saved.digest != digest || !reflect.DeepEqual(saved.result, out) {
		return out, digest, errors.Join(ErrResolveRequired, err)
	}
	return saved.result, saved.digest, nil
}

func terminalExists(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (found bool, err error) {
	defer func() { found, err = finishCommand(ctx, commandLifetime(t), found, err) }()
	rows, err := t.query(ctx, t.steps[id].statementStep, args...)
	if err != nil {
		return false, err
	}
	found = rows.Next()
	if found && rows.Next() {
		err = ErrResolveRequired
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	return found, err
}
