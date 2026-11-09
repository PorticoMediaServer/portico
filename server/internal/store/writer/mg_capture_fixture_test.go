package writer

import (
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// The fixture uses the REAL embedded A/B/G+013 schema through mgPersonalFixture
// and MG-10's current guide/source population, then adds one current recording
// allocation. The capture commands only ever see native rows.
type mgCaptureFixtureValues struct {
	mgRecordingFixtureValues
	saved      mgRecordingAcceptance
	allocation mgCaptureAllocationFence
	launch     mgCaptureLaunch
	token      string
	instance   string
	artifact   string
	fence      string
}

func (v mgCaptureFixtureValues) claimLease() int64 {
	return v.allocation.leaseMS - int64(time.Hour/time.Millisecond)
}

func (v mgCaptureFixtureValues) renewLease() int64 {
	return v.allocation.leaseMS - int64(30*time.Minute/time.Millisecond)
}

func (v mgCaptureFixtureValues) claimRequest() mgCaptureClaimRequest {
	return mgCaptureClaimRequest{
		owner: v.owner, id: v.saved.result.id, expected: 1, token: v.token,
		ownerInstance: v.instance, artifactID: v.artifact, allocation: v.allocation,
		leaseMS: v.claimLease(), launch: v.launch, sourceRevision: v.scope.revision,
		sourceFence: v.fence,
	}
}

func (v mgCaptureFixtureValues) renewRequest() mgCaptureRenewRequest {
	return mgCaptureRenewRequest{
		owner: v.owner, id: v.saved.result.id, expected: 2, claimGeneration: 1,
		token: v.token, ownerInstance: v.instance, launch: v.launch,
		allocation: v.allocation, leaseMS: v.renewLease(),
	}
}

func (v mgCaptureFixtureValues) checkpointRequest() mgCaptureCheckpointRequest {
	now := v.now.Add(24*time.Hour + 10*time.Minute).UnixMilli()
	return mgCaptureCheckpointRequest{
		owner: v.owner, id: v.saved.result.id, expected: 3, claimGeneration: 1,
		token: v.token, ownerInstance: v.instance, launch: v.launch,
		allocation:      mgCaptureAllocationIdentity{v.allocation.id, v.allocation.token, v.allocation.generation},
		capturedStartMS: now, capturedEndMS: now + 5*60*1000, gapCount: 1,
		producerSucceeded: true,
	}
}

func mgCaptureFixture(t *testing.T) (*executor, string, mgCaptureFixtureValues) {
	t.Helper()
	w, path, base := mgRecordingFixture(t)
	fake := mgRecordingFake(base)
	saved := scheduleMGRecording(t, w, base, fake, mgPersonalIdentity(1, 1))
	ackMGRecording(t, w, base.root, 1)

	// The claim is taken at the recording's scheduled start, inside the padded
	// window, on the same synthetic clock (never the real clock).
	start := base.now.Add(24 * time.Hour).UTC().Truncate(time.Second)
	w.now = func() time.Time { return start.Add(10 * time.Minute) }
	now := w.now().UnixMilli()
	allocation := mgCaptureAllocationFence{
		id: "allocation-1", token: "allocation-token", generation: 1,
		leaseMS: now + int64(2*time.Hour/time.Millisecond),
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO live_allocations(id,source_id,channel_id,resource_id,kind,authority,account_id,profile_id,token,generation,state,lease_until_ms,created_ms,preempt_at_ms) VALUES(?,?,?,?,'recording','local',?,?,?,1,'active',?,?,0)`,
		allocation.id, base.scope.source, base.scope.channel, saved.result.id, base.owner.account,
		base.owner.profile, allocation.token, allocation.leaseMS, now); err != nil {
		t.Fatal(err)
	}
	var launch mgCaptureLaunch
	for i := range launch {
		launch[i] = byte(i + 1)
	}
	return w, path, mgCaptureFixtureValues{
		mgRecordingFixtureValues: base,
		saved:                    saved,
		allocation:               allocation,
		launch:                   launch,
		token:                    "claim-token",
		instance:                 "worker-1",
		artifact:                 "artifact-1",
		fence:                    strings.Repeat("a", 48),
	}
}

// The expected scalar mirrors the effect's exact post-state; it is never built
// from a live row read.
func (v mgCaptureFixtureValues) claimResult() mgCaptureResult {
	return mgCaptureResult{
		id: v.saved.result.id, owner: v.owner.key(), source: v.scope.source,
		channel: v.scope.channel, programme: v.scope.programme, generation: v.scope.generation,
		state: "preparing", token: v.token, ownerInstance: v.instance,
		allocationID: v.allocation.id, allocationToken: v.allocation.token,
		sourceFence: v.fence, artifactID: v.artifact, launch: v.launch,
		claimGeneration: 1, cancelGeneration: 0, leaseMS: v.claimLease(),
		allocationGeneration: 1, sourceRevision: v.scope.revision, revision: 2, sequence: 1,
	}
}

func (v mgCaptureFixtureValues) renewResult() mgCaptureResult {
	out := v.claimResult()
	out.leaseMS, out.revision, out.sequence = v.renewLease(), 3, 2
	return out
}

func (v mgCaptureFixtureValues) checkpointResult(in mgCaptureCheckpointRequest) mgCaptureResult {
	out := v.renewResult()
	out.capturedStartMS, out.capturedEndMS, out.gapCount = in.capturedStartMS, in.capturedEndMS, in.gapCount
	out.producerSucceeded = 1
	if in.boundaryReached {
		out.boundaryReached = 1
	}
	out.revision, out.sequence = 4, 3
	return out
}

func mgCaptureFake(v mgCaptureFixtureValues) *mgPersonalFakeRoots {
	return mgRecordingFake(v.mgRecordingFixtureValues)
}

func mgCaptureDefinitionsUnion() []stmt.Definition {
	defs := mgPersonalRootDefinitions()
	defs = append(defs, mgCaptureDefinitions()...)
	return append(defs, mgOwnerDefinition())
}

func mgCaptureTurn(t *testing.T, w *executor, purpose CommandID) *commandTurn {
	t.Helper()
	defs := mgCaptureDefinitionsUnion()
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	recipe := turnRecipe{
		limit:    Work{1000, 1000000, 1000, 8 << 20, 25 * time.Millisecond},
		setup:    Work{8, 100, 8, 4096, time.Millisecond},
		terminal: Work{8, 100, 8, 4096, time.Millisecond},
	}
	tx, err := w.begin(t.Context(), purpose, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	var steps []statementStep
	for _, d := range defs {
		steps = append(steps, statementStep{d.ID, Work{8, 10000, 32, 65536, time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !tx.done {
			_ = turn.closeRows()
			_, _ = tx.rollback(nil)
		}
	})
	return turn
}

func mgCaptureClaimReference(t *testing.T, id CommandIdentity, r mgCaptureClaimRequest) ReceiptReference {
	t.Helper()
	c, err := newMGCaptureClaimCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: id, RequestDigest: c.digest}
}

func mgCaptureRenewReference(t *testing.T, id CommandIdentity, r mgCaptureRenewRequest) ReceiptReference {
	t.Helper()
	c, err := newMGCaptureRenewCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: id, RequestDigest: c.digest}
}

func mgCaptureCheckpointReference(t *testing.T, id CommandIdentity, r mgCaptureCheckpointRequest) ReceiptReference {
	t.Helper()
	c, err := newMGCaptureCheckpointCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: id, RequestDigest: c.digest}
}

func claimMGCapture(t *testing.T, w *executor, v mgCaptureFixtureValues, fake *mgPersonalFakeRoots, identity CommandIdentity) mgCaptureAcceptance {
	t.Helper()
	request := v.claimRequest()
	turn := mgCaptureTurn(t, w, MGCaptureClaim)
	out, err := acceptMGCaptureClaim(t.Context(), turn, fake, v.proof, t.Context(), mgCaptureClaimReference(t, identity, request), request)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	mgPersonalCommit(t, turn)
	ackMGCaptureRow(t, w, out.result.id, out.result.sequence)
	ackMGRecording(t, w, v.root, identity.Sequence)
	return out
}

func renewMGCapture(t *testing.T, w *executor, v mgCaptureFixtureValues, fake *mgPersonalFakeRoots, identity CommandIdentity) mgCaptureAcceptance {
	t.Helper()
	request := v.renewRequest()
	turn := mgCaptureTurn(t, w, MGRenewCapture)
	out, err := acceptMGCaptureRenew(t.Context(), turn, fake, v.proof, t.Context(), mgCaptureRenewReference(t, identity, request), request)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	mgPersonalCommit(t, turn)
	ackMGCaptureRow(t, w, out.result.id, out.result.sequence)
	ackMGRecording(t, w, v.root, identity.Sequence)
	return out
}

func checkpointMGCapture(t *testing.T, w *executor, v mgCaptureFixtureValues, fake *mgPersonalFakeRoots, identity CommandIdentity) mgCaptureAcceptance {
	t.Helper()
	request := v.checkpointRequest()
	turn := mgCaptureTurn(t, w, MGCheckpointCapture)
	out, err := acceptMGCaptureCheckpoint(t.Context(), turn, fake, v.proof, t.Context(), mgCaptureCheckpointReference(t, identity, request), request)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	mgPersonalCommit(t, turn)
	ackMGCaptureRow(t, w, out.result.id, out.result.sequence)
	ackMGRecording(t, w, v.root, identity.Sequence)
	return out
}

// The scheduler acknowledges one capture command at the recording row before
// the next; the fixture models that external ACK exactly.
func ackMGCaptureRow(t *testing.T, w *executor, id string, sequence int64) {
	t.Helper()
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_recordings SET acknowledged_command_sequence=? WHERE id=? AND last_command_sequence=?`, sequence, id, sequence); err != nil {
		t.Fatal(err)
	}
}

func mgCaptureCounts(t *testing.T, w *executor, root string) (receipts, sequence int64) {
	t.Helper()
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM user_mutation_command_receipts),(SELECT last_command_sequence FROM user_mutation_command_owners WHERE root_id=?)`, root).Scan(&receipts, &sequence); err != nil {
		t.Fatal(err)
	}
	return
}
