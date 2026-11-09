package writer

import (
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func mgGuideDefinitions() []stmt.Definition {
	defs := append(mgGuideNativeDefinitions(), mgGuideBeginDefinitions()...)
	defs = append(defs, mgGuideStageDefinitions()...)
	return append(defs, mgGuideReceiptDefinitions()...)
}

// One synthetic component frame carrying every closed guide effect. This larger
// test-only ledger is not an admission profile for a production command.
func mgGuideTestTurn(t *testing.T, w *executor, purpose CommandID, defs []stmt.Definition) *commandTurn {
	t.Helper()
	// Virtual component clock, not a latency/admission/qualification result.
	clock := time.Now().Add(time.Hour)
	w.now = func() time.Time { return clock }
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	recipe := turnRecipe{limit: Work{4096, 1000000, 4096, 32 << 20, 25 * time.Millisecond}, setup: Work{8, 100, 8, 4096, time.Millisecond}, terminal: Work{8, 100, 8, 4096, time.Millisecond}}
	tx, err := w.begin(t.Context(), purpose, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	var steps []statementStep
	for _, d := range defs {
		steps = append(steps, statementStep{d.ID, Work{16, 10000, 64, 65536, time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = turn.closeRows() })
	return turn
}

func mgGuideCommit(t *testing.T, turn *commandTurn) {
	t.Helper()
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}

func mgGuideRollback(t *testing.T, turn *commandTurn) {
	t.Helper()
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

func mgGuideProjectionFixture() mgGuideReceiptProjection {
	return mgGuideReceiptProjection{capacityKnown: 1, planningEstimate: 999, tunerCountMode: "discovered", deliveryValidation: "frozen original parser metadata", acquisition: "upload", skippedChannels: 2, skippedProgrammes: 3, adjustedProgrammes: 4, exampleCount: 2, examples: [10]string{"original channel diagnostic", "original programme diagnostic"}}
}

func mgGuideIdentityAfter(before mgGuideNative) CommandIdentity {
	return CommandIdentity{Incarnation: before.incarnation, Sequence: before.progress.sequence + 1, OperationID: [16]byte{9, byte(before.progress.sequence + 1)}}
}

// Immutable whole-import commitment over the exact ordered frozen stages.
func mgGuideChainFor(m mgGuideManifest, stages []mgGuideFrozenStage, firstSequence int64) [32]byte {
	chain := mgGuideInitialChain(m)
	var ordinals [3]int64
	for i, s := range stages {
		family := int(s.family) - 1
		delta := mgGuideStageDelta{s.family, m.source, m.generation, firstSequence + int64(i), ordinals[family], s.count, s.bytes + s.count*(9+int64(len(m.generation))), s.payload}
		chain = mgGuideNextChain(chain, delta)
		ordinals[family] += s.count
	}
	return chain
}

func mgGuideFrozenFixture(t *testing.T) []mgGuideFrozenStage {
	t.Helper()
	channel, err := freezeMGGuideChannels([]mgGuideChannel{{id: "channel", provider: "provider", name: "Channel", position: 0, locator: []byte("sealed locator"), availableStart: "2026-11-07T00:00:00Z", availableEnd: "2026-11-08T00:00:00Z"}})
	if err != nil {
		t.Fatal(err)
	}
	programme, err := freezeMGGuideProgrammes([]mgGuideProgramme{{id: "programme", channel: "channel", provider: "provider", title: "Programme", start: "2026-11-07T00:00:00Z", end: "2026-11-07T01:00:00Z", lineage: "lineage"}})
	if err != nil {
		t.Fatal(err)
	}
	choice, err := freezeMGGuideChoices([]mgGuideChoice{{key: "provider", name: "Channel", group: "Group"}})
	if err != nil {
		t.Fatal(err)
	}
	return []mgGuideFrozenStage{channel, programme, choice}
}

// mgGuideBirthFixture seeds the committed prior state. non-fresh means the
// source already serves generation "old" at revision 7; fresh means the source
// identity is created by the birth itself.
func mgGuideBirthFixture(t *testing.T, fresh bool) (*executor, mgGuideBirth, []mgGuideFrozenStage) {
	t.Helper()
	w, _ := testCanonicalExecutor(t)
	stages := mgGuideFrozenFixture(t)
	if !fresh {
		for _, q := range []string{
			`INSERT INTO live_source_identities(id) VALUES('source')`,
			`INSERT INTO live_generations(id,source_id,name,tuner_count,channel_count,programme_count,created_at) VALUES('old','source','Old',1,1,1,'before')`,
			`INSERT INTO live_sources(id,name,revision,state,tuner_count,active_generation,published_at) VALUES('source','Old',7,'active',1,'old','before')`,
		} {
			if _, err := w.db.ExecContext(t.Context(), q); err != nil {
				t.Fatal(err)
			}
		}
	}
	m := mgGuideManifest{source: "source", generation: "new", expected: [3]int64{1, 1, 1}, expectedRevision: 7, previousGeneration: "old"}
	if fresh {
		m.source = "fresh-source"
		m.expectedRevision = 0
		m.previousGeneration = ""
	}
	m.expectedChain = mgGuideChainFor(m, stages, 2)
	b := mgGuideBirth{request: "guide-request", input: "guide-input", name: "Guide", tuners: 1, manifest: m,
		identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, requestDigest: RequestDigest{3}}
	return w, b, stages
}

func mgGuidePurposeFor(family mgGuideFamily) CommandID {
	switch family {
	case mgGuideProgrammes:
		return MGGuideStageProgrammes
	case mgGuideChoices:
		return MGGuideStageChoices
	}
	return MGGuideStageChannels
}

func mgGuideKindFor(family mgGuideFamily) mgGuideReceiptKind {
	switch family {
	case mgGuideProgrammes:
		return mgGuideReceiptProgrammes
	case mgGuideChoices:
		return mgGuideReceiptChoices
	}
	return mgGuideReceiptChannels
}

// mgGuideBeginCommitted creates the staging operation and commits its begin
// receipt and exact ACK, returning the native row read after the commit.
func mgGuideBeginCommitted(t *testing.T, w *executor, b mgGuideBirth) mgGuideNative {
	t.Helper()
	defs := mgGuideDefinitions()
	turn := mgGuideTestTurn(t, w, MGGuideBegin, defs)
	result, err := mgBeginGuideNative(t.Context(), turn, b)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := mgSaveGuideReceipt(t.Context(), turn, result, b.identity, b.requestDigest, mgGuideReceiptBegin, mgGuideProjectionFixture())
	if err != nil {
		t.Fatal(err)
	}
	acked, err := mgACKGuideReceipt(t.Context(), turn, result.operation, saved.key, saved.resultDigest)
	if err != nil {
		t.Fatal(err)
	}
	mgGuideCommit(t, turn)
	reopen := mgGuideTestTurn(t, w, MGGuideBegin, defs)
	after, err := mgReadGuideNative(t.Context(), reopen, b.request)
	if err != nil {
		t.Fatal(err)
	}
	mgGuideRollback(t, reopen)
	if after != acked {
		t.Fatal("committed begin did not read back", after, acked)
	}
	return after
}

// mgGuideStageCommitted stages one bounded batch, saves and ACKs its receipt,
// commits, and returns the durable receipt plus the post-reopen native row.
func mgGuideStageCommitted(t *testing.T, w *executor, before mgGuideNative, stage mgGuideFrozenStage) (mgGuideReceipt, mgGuideNative, mgGuideNativeResult) {
	t.Helper()
	defs := mgGuideDefinitions()
	identity := mgGuideIdentityAfter(before)
	turn := mgGuideTestTurn(t, w, mgGuidePurposeFor(stage.family), defs)
	result, err := mgStageGuideNative(t.Context(), turn, before, identity, stage)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := mgSaveGuideReceipt(t.Context(), turn, result, identity, RequestDigest{byte(identity.Sequence)}, mgGuideKindFor(stage.family), mgGuideProjectionFixture())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgACKGuideReceipt(t.Context(), turn, result.operation, saved.key, saved.resultDigest); err != nil {
		t.Fatal(err)
	}
	mgGuideCommit(t, turn)
	reopen := mgGuideTestTurn(t, w, MGGuideBegin, defs)
	after, err := mgReadGuideNative(t.Context(), reopen, before.request)
	if err != nil {
		t.Fatal(err)
	}
	mgGuideRollback(t, reopen)
	return saved, after, result
}
