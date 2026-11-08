package writer

import (
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// P1 draft: compose the Basic Accept profile on the SAME writer runtime that
// owns the issued scan driver. This is the producer seam; no product wiring.
func basicProducerFixture(t *testing.T) (*scanDriverFixture, *ScanRunLease, ScanRunReference, *Writer) {
	t.Helper()
	f, lease, last := basicSourceIssuerFixture(t)
	r := f.driver.writer.effective
	bounds := basicAcceptBounds{}
	acceptDefs := basicFinalDefinitions(bounds)
	var all []stmt.Definition
	seen := map[stmt.ID]bool{}
	for _, entry := range r.registry.Manifest() {
		all = append(all, entry.Definition)
		seen[entry.Definition.ID] = true
	}
	for _, d := range acceptDefs {
		if seen[d.ID] {
			continue
		}
		all = append(all, d)
		seen[d.ID] = true
	}
	conn, err := r.executor.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// The driver fixture acquires writer turns at NORMAL, not the startup FULL
	// policy, so capture against the actual connection policy.
	actual, err := sqliteconn.Assert(t.Context(), conn, r.executor.config)
	if err != nil {
		cfg := r.executor.config
		cfg.Synchronous = sqliteconn.SyncMode(actual.Synchronous)
		if _, retry := sqliteconn.Assert(t.Context(), conn, cfg); retry != nil {
			t.Fatal(retry)
		}
		r.executor.config = cfg
	}
	golden := captureEffectiveFixture(t, conn, r.executor.config, all, "basic_producer_test.go")
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	accept := effectivePlan{
		recipe:            turnRecipe{limit: Work{4096, 1000000, 4096, 32 << 20, 25 * time.Millisecond}, setup: Work{16, 500, 16, 65536, time.Millisecond}, terminal: Work{16, 500, 16, 65536, 8 * time.Millisecond}},
		headerSync:        Work{WALBytes: 32, Hold: 5 * time.Millisecond},
		basicAcceptBounds: bounds,
		basicMemory:       commandMemoryBudget{4 << 20, 4 << 20},
	}
	accept.need[governor.MemoryBytes] = 8 << 20
	accept.need[governor.CPUMilli] = 100
	accept.need[governor.WALBytes] = int64(accept.recipe.limit.WALBytes)
	accept.need[governor.IOBytes] = 64 << 20
	accept.io = []governor.DeviceIO{{DeviceID: "protocol-fixture", Class: governor.Seeking, Outstanding: 1, Bytes: 64 << 20}}
	accept.recipe.limit.Rows, accept.recipe.limit.VMOps, accept.recipe.limit.Pages = 1<<20, 1<<32, 1<<20
	for _, d := range acceptDefs {
		work := Work{Rows: basicFinalRows(bounds, d.ID), VMOps: 160000, Pages: 16, Hold: 25 * time.Microsecond}
		if d.Kind == stmt.Write {
			work.WALBytes = 65536
		}
		accept.steps = append(accept.steps, statementStep{d.ID, work})
	}
	accept.registration = golden.Receipt()
	if err := composeBasicAcceptProfile(t.Context(), r, accept); err != nil {
		t.Fatal(err)
	}
	return f, lease, last, f.driver.writer
}

// The prepared original is funded in an earlier turn, so the dispatch deadline
// is clipped to it and the issued driver binds the frame. The body then reads
// native rows this draft fixture does not seed; binding the frame (not the body
// outcome) is what this slice proves.
func TestBasicProducerPreparedAcceptBindsTheIssuedFrame(t *testing.T) {
	f, lease, last, w := basicProducerFixture(t)
	d := f.driver
	identity := basicBindingIdentity(last)
	p, original, err := prepareBasicCommandOriginal(t.Context(), d, lease, last, BasicPurposeAccept, identity)
	if err != nil {
		t.Fatal(err)
	}
	if !p.expected.Deadline.Before(w.effective.executor.now().Add(MaxCommandSafetyHold)) {
		t.Fatal("preparation deadline is not earlier than the safety ceiling")
	}
	source := basicStreamFactStage{
		changeID: 51, inputRevision: 7, libraryID: original.scope.libraryID, assetID: 41, expectedEpoch: 17,
		assetToken: "producer-asset", runID: original.scope.runID, sourceID: original.source.id, incarnation: original.source.incarnation,
		objectID: "producer-object", objectRevision: "producer-object-revision", sourceGeneration: original.source.generation,
		policyRevision: original.run.policyRevision, witnessFingerprint: [32]byte{1}, factRevision: 31, size: 42, modifiedNS: -11,
		detailVersion: 1, fingerprint: "producer-probe",
	}
	analysis, err := prepareBasicObjectAnalysis(source, original.scope.libraryKey, "producer-probe-v1", `["probe","local_metadata","subtitles"]`, `["probe","local_metadata","subtitles"]`, false)
	if err != nil {
		t.Fatal(err)
	}
	nfo, err := prepareBasicNFOSet(source, original.scope.libraryKey, nil, nil, nil, basicNFOStatus{status: "observed"}, true)
	if err != nil {
		t.Fatal(err)
	}
	command, err := newBasicAcceptCommand(identity, basicAcceptRequest{
		original: original, purpose: BasicPurposeAccept, operation: basicAcceptFirstOrdinaryLeaf,
		activation: basicActivationEffects{source: source, changeRevision: 13, familyMask: 511, shapeID: 61, invalidationRevision: 1, fileSizeRevision: 1},
		entityID:   31, duration: 100, height: 1080, analysis: *analysis, nfo: nfo,
	})
	if err != nil {
		t.Fatal(err)
	}
	q, err := command.receiptRequest()
	if err != nil {
		t.Fatal(err)
	}
	work, err := d.prepareWork(t.Context(), BasicAccept, q.Scan, 1, p)
	if err != nil {
		t.Fatal(err)
	}
	in := basicInvocation{request: q, accept: command, work: work}
	_, _, runErr := w.effective.runBasic(t.Context(), w, d.sourceDisclosure(last.Scan), in, false)
	if work.submission != scanFrameBound {
		var ae *governor.AdmissionError
		if errors.As(runErr, &ae) {
			t.Fatalf("prepared frame was not bound: %v cause=%v refusal=%v", runErr, ae.Cause, ae.Err)
		}
		t.Fatalf("prepared frame was not bound: %v (%#v)", runErr, runErr)
	}
	// The body is expected to refuse on the unseeded native rows; it must not
	// be the pre-frame guard that rejected the command.
	if runErr == nil {
		t.Fatal("unseeded body unexpectedly applied")
	}
}
