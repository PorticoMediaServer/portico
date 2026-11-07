package writer

import (
	"bytes"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const inventoryProgressiveLimit = 128 // bounded future warmup + measured originals

// Component-only synthetic source; NOT production issuer, permit or profile.
// Sequential use only. The same native executor, complete Captured registry and
// factored W governor/recipe survive all originals, Resolve and ACK turns.
// Nothing here enables the opt-in target's steady/positive-warmup populations.
type inventoryProgressiveFixture struct {
	sharedMaintenance bool // only the actual attached keeper/CP may coexist
	sharedForeground  bool // only this independently owned class may coexist
	owner             *inventoryMemoryFixture
	base              *inventoryAcceptCommand
	current           *inventoryAcceptCommand
	turn              *commandTurn
	resolveTurn       *commandTurn // observation-only private owner views
	ackTurn           *commandTurn
	acknowledged      *int64 // actual handler return; separate from physical outcome
	resolved          *inventoryReceiptLookup
	snapshot          inventoryProgressiveSnapshot
	limit, offered    int
	ready             bool
	stopped           error
}

type inventoryProgressiveSnapshot struct {
	run                   inventoryRun
	source                inventorySource
	directory             inventoryDirectory
	incarnation           [16]byte
	discovered, processed int64
}

func newInventoryProgressiveFixture(t *testing.T, count int, clock func() time.Time, decorate ...func(driver.Conn) driver.Conn) *inventoryProgressiveFixture {
	t.Helper()
	if count < 1 || count > inventoryProgressiveLimit || clock == nil {
		t.Fatal("invalid bounded progressive fixture request")
	}
	e, _, original, registry := inventoryFreshFixtureDefinitions(t, inventoryCommandDefinitions(), decorate...)
	return inventoryProgressiveOwner(t, e, original, registry, count, clock)
}

func inventoryProgressiveOwner(t *testing.T, e *executor, original *inventoryAcceptCommand, registry *stmt.Registry, count int, clock func() time.Time, maintenance ...governedFixtureMaintenance) *inventoryProgressiveFixture {
	t.Helper()
	e.now = clock // before the first BEGIN; protocol controls may freeze it
	owner := newInventoryMemoryFixtureComposition(t, e, registry, original, inventoryMemoryBudget{preparationBytes: 4 << 20, reservedBytes: 4 << 20}, nil, maintenance...)
	f := &inventoryProgressiveFixture{owner: owner, base: original, limit: count, ready: true}
	var err error
	f.snapshot, err = f.currentSnapshot(t)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// No new command is allocated while any prior original is unresolved/unacked.
// Both receipt sequence and mutable witnesses come from the actual snapshot;
// the filename/object/next-cookie are declared synthetic NEW source observations.
func (f *inventoryProgressiveFixture) NextOriginal() (*inventoryAcceptCommand, error) {
	if f.stopped != nil {
		return nil, f.stopped
	}
	if err := f.joined(); err != nil {
		return nil, f.stop(err)
	}
	if !f.ready {
		return nil, ErrResolveRequired
	}
	if f.offered == f.limit {
		return nil, ErrCap
	}
	s := f.snapshot
	r := inventoryTestClone(f.base.request)
	r.run, r.source = s.run, s.source
	r.directory = s.directory
	r.directory.rawNames = 1
	r.directory.cursorKind = inventoryLinuxCursor
	cookie, err := inventoryFreshCookie(s.directory.expectedCursor)
	if err != nil {
		return nil, f.stop(err)
	}
	if s.directory.expectedCursor == "" {
		cookie = 41
	}
	r.directory.nextCursor = strconv.FormatInt(cookie+1, 10)
	identity := f.base.identity
	identity.Incarnation, identity.Sequence = s.incarnation, s.run.lastSequence+1
	binary.BigEndian.PutUint64(identity.OperationID[8:], uint64(identity.Sequence))
	file := &r.files[0]
	file.relativePath = fmt.Sprintf("Arrival-%d.mkv", identity.Sequence)
	file.assetPath = string(r.source.resolvedRoot) + "/" + file.relativePath
	file.sourceKey = fmt.Sprintf("progressive-source-key:%d", identity.Sequence)
	file.objectIdentity = fmt.Sprintf("dev:7:ino:%d", 1000+identity.Sequence)
	evidence := mediasource.InventoryEvidence{Kind: "local", Scope: r.source.id + ":" + r.source.incarnation,
		Object: fmt.Sprintf("ino:%d", 1000+identity.Sequence), Size: file.size, ModifiedNS: file.modifiedNS, ChangeToken: file.changeToken}
	file.revision, err = evidence.Revision()
	if err != nil {
		return nil, f.stop(err)
	}
	command, err := newInventoryAcceptCommand(identity, r)
	if err != nil {
		return nil, f.stop(err)
	}
	f.current, f.turn, f.resolved = command, nil, nil
	f.resolveTurn, f.ackTurn = nil, nil
	f.acknowledged = nil
	f.offered++
	f.ready = false
	return command, nil
}

func (f *inventoryProgressiveFixture) Begin(t *testing.T) (*commandTurn, error) {
	t.Helper()
	if f.stopped != nil {
		return nil, f.stopped
	}
	if f.current == nil || f.ready || f.turn != nil {
		return nil, ErrResolveRequired
	}
	if err := f.joined(); err != nil {
		return nil, f.stop(err)
	}
	turn, err := f.owner.begin(t, f.current)
	if err != nil {
		return nil, f.stop(err)
	}
	f.turn = turn
	q, err := f.current.receiptRequest()
	if err == nil {
		var lookup inventoryReceiptLookup
		lookup, err = lookupInventoryAccept(t.Context(), turn, q)
		if err == nil && lookup.decision != receiptFresh {
			err = ErrResolveRequired
		}
	}
	if err == nil {
		err = checkInventoryFreshLibrary(t.Context(), turn, q)
	}
	if err != nil {
		_, rollbackErr := turn.transaction.rollback(err)
		return nil, f.stop(errors.Join(err, rollbackErr))
	}
	return turn, nil
}

// Resolve is the actual W historical receipt decoder on a separately governed
// physical turn, using the SAME original reference. No later mutable snapshot
// reconstructs a missing result or failed physical attempt.
func (f *inventoryProgressiveFixture) Resolve(t *testing.T) error {
	t.Helper()
	if f.stopped != nil {
		return f.stopped
	}
	if f.current == nil || f.turn == nil || f.resolved != nil || f.ready {
		return ErrResolveRequired
	}
	if err := f.committed(f.turn.transaction); err != nil {
		return f.stop(err)
	}
	q, err := f.current.receiptRequest()
	if err != nil {
		return f.stop(err)
	}
	turn, err := f.owner.begin(t, f.current)
	f.resolveTurn = turn
	if err != nil {
		return f.stop(err)
	}
	defer turn.transaction.rollback(nil)
	lookup, err := lookupInventoryAccept(t.Context(), turn, q)
	if err != nil {
		return f.stop(err)
	}
	if lookup.decision != receiptReplay || lookup.owner.lastSequence != q.Receipt.Identity.Sequence ||
		lookup.result.SourceRevision != f.current.request.files[0].revision || lookup.result.NextCursor != f.current.request.directory.nextCursor {
		return f.stop(ErrResolveRequired)
	}
	if err := f.finish(turn); err != nil {
		return f.stop(err)
	}
	f.resolved = &lookup
	return nil
}

// ACK uses W's actual lookup + CAS + physical commit, then takes ONE native
// current snapshot. Only this success releases the next-original gate.
func (f *inventoryProgressiveFixture) AckAndSnapshot(t *testing.T) error {
	t.Helper()
	if f.stopped != nil {
		return f.stopped
	}
	if f.resolved == nil || f.ready {
		return ErrResolveRequired
	}
	if err := f.joined(); err != nil {
		return f.stop(err)
	}
	q, err := f.current.receiptRequest()
	if err != nil {
		return f.stop(err)
	}
	turn, err := f.owner.begin(t, f.current)
	f.ackTurn = turn
	if err != nil {
		return f.stop(err)
	}
	defer turn.transaction.rollback(nil)
	lookup, err := lookupInventoryAccept(t.Context(), turn, q)
	if err != nil {
		return f.stop(err)
	}
	if lookup.decision != receiptReplay || lookup.digest != f.resolved.digest {
		return f.stop(ErrResolveRequired)
	}
	ack, err := acknowledgeInventory(t.Context(), turn, q, lookup)
	if err != nil {
		return f.stop(err)
	}
	f.acknowledged = &ack
	if ack != q.Receipt.Identity.Sequence {
		return f.stop(ErrResolveRequired)
	}
	if err := f.finish(turn); err != nil {
		return f.stop(err)
	}
	snapshot, err := f.currentSnapshot(t)
	if err != nil {
		return f.stop(err)
	}
	if snapshot.run.lastSequence != ack || snapshot.run.revision != f.resolved.result.AcceptedRunRevision ||
		snapshot.directory.expectedCursor != f.resolved.result.NextCursor || snapshot.directory.pages != f.current.request.directory.pages+1 {
		return f.stop(ErrConflict)
	}
	f.snapshot, f.ready = snapshot, true
	return nil
}

func (f *inventoryProgressiveFixture) finish(turn *commandTurn) error {
	if err := turn.closeRows(); err != nil {
		return err
	}
	if _, err := turn.transaction.commit(); err != nil {
		return err
	}
	return f.committed(turn.transaction)
}

func (f *inventoryProgressiveFixture) committed(tx *transaction) error {
	if !tx.done || tx.result.state != committed || tx.finishErr != nil || !tx.policyVerified ||
		!tx.cleanupComplete || !tx.connectionReturned || !tx.retirementComplete || tx.physicalReturnedAt.IsZero() {
		return errors.Join(ErrResolveRequired, tx.finishErr)
	}
	return f.joined()
}

func (f *inventoryProgressiveFixture) joined() error {
	w := f.owner.executor
	if !w.mu.TryLock() {
		return ErrBusy
	}
	defer w.mu.Unlock()
	w.physicalFaults.mu.Lock()
	failed := w.physicalFaults.failed
	w.physicalFaults.mu.Unlock()
	if w.closed || w.fenced || w.ambiguous || failed || w.db.Stats().InUse != 0 || !f.joinedUsage(f.owner.authority.Usage()) {
		return ErrResolveRequired
	}
	return nil
}

func (f *inventoryProgressiveFixture) stop(err error) error {
	f.stopped = errors.Join(f.stopped, err)
	return f.stopped
}

// Test-only read of current native witnesses OUTSIDE HOLD, not production
// observation authority or an added statement/profile allowance. A single
// joined SELECT supplies one SQLite snapshot; no state is reset or repaired.
func (f *inventoryProgressiveFixture) currentSnapshot(t *testing.T) (inventoryProgressiveSnapshot, error) {
	t.Helper()
	var s inventoryProgressiveSnapshot
	if err := f.joined(); err != nil {
		return s, err
	}
	var incarnation []byte
	var root string
	var follow bool
	r := f.base.request
	err := f.owner.executor.db.QueryRowContext(t.Context(), `SELECT
r.command_incarnation,r.job_revision,r.policy_revision,r.job_lease_owner,r.job_lease_generation,r.job_lease_expires_ms,
r.last_command_sequence,r.acknowledged_command_sequence,r.phase,r.scan_mode,r.discovered,j.processed,
s.id,s.incarnation,s.root_identity,s.root,s.generation,s.follow_symlinks,
d.relative_path,d.identity,d.revision,d.cursor,d.pages,d.skipped
FROM inventory_runs r JOIN inventory_directories d ON d.job_id=r.job_id
JOIN jobs j ON j.id=r.job_id AND j.library_id=? AND j.status='running'
JOIN library_sources s ON s.id=r.source_id AND s.library_id=j.library_id
JOIN library_scan_policies p ON p.library_id=j.library_id AND p.revision=r.policy_revision
WHERE r.job_id=? AND d.relative_path=? AND r.original_library_id=? AND r.original_library_key=?
AND r.source_generation=s.generation AND r.root_incarnation=s.incarnation AND r.root_identity=s.root_identity
AND r.authoritative=0 AND r.root_rebound=0 AND r.finished_at='' AND d.state='pending'
AND s.kind='local' AND s.classification='local' AND s.enabled=1 AND s.identity_confirmed=1 AND s.health='ready' AND s.remote_source_id IS NULL`,
		r.scope.libraryKey, r.scope.runID, r.directory.path, r.scope.libraryID, r.scope.libraryKey).Scan(
		&incarnation, &s.run.revision, &s.run.policyRevision, &s.run.leaseOwner, &s.run.leaseGeneration, &s.run.leaseExpiresMS,
		&s.run.lastSequence, &s.run.acknowledgedSequence, &s.run.phase, &s.run.scanMode, &s.discovered, &s.processed,
		&s.source.id, &s.source.incarnation, &s.source.rootIdentity, &root, &s.source.generation, &follow,
		&s.directory.path, &s.directory.identity, &s.directory.revision, &s.directory.expectedCursor, &s.directory.pages, &s.directory.skipped)
	if err != nil {
		return inventoryProgressiveSnapshot{}, err
	}
	if len(incarnation) != 16 || !bytes.Equal(incarnation, f.base.identity.Incarnation[:]) || s.run.lastSequence != s.run.acknowledgedSequence ||
		s.source.id != r.source.id || s.source.incarnation != r.source.incarnation || s.source.rootIdentity != r.source.rootIdentity ||
		s.source.generation != r.source.generation || root != string(r.source.resolvedRoot) || follow != r.source.followSymlinks {
		return inventoryProgressiveSnapshot{}, ErrConflict
	}
	copy(s.incarnation[:], incarnation)
	s.source.resolvedRoot, s.source.followSymlinks = []byte(root), follow
	return s, f.joined()
}

func (f *inventoryProgressiveFixture) joinedUsage(usage [4]governor.Amounts) bool {
	if f.sharedForeground {
		usage[governor.Foreground] = governor.Amounts{}
	}
	if f.sharedMaintenance {
		usage[governor.Recovery] = governor.Amounts{}
	} // CP lifetime/active/shutdown separately joined by owner
	return usage == ([4]governor.Amounts{})
}
