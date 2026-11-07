package writer

import (
	"context"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
	"testing"
)

func mpDraftActor() mpPrepActor {
	return mpPrepActor{server: "server", authority: "local", account: "account", profile: "profile", device: "device", family: "family", grant: "grant", sessionHash: strings.Repeat("a", 64), epoch: 1, tokenGeneration: 1, horizonMS: 10000}
}
func mpDraftSource() mpPrepSource {
	return mpPrepSource{library: "library", source: "source", asset: "asset", object: "object", revision: "r1", root: "root", incarnation: "incarnation", configuration: "config", policy: "policy", authority: "authority", libraryGeneration: 1, configurationGeneration: 1, size: 10, modifiedNS: 20}
}
func mpDraftIDs() mpStartPrepStableIDs {
	return mpStartPrepStableIDs{session: "session", legacySession: "media", snapshot: "snapshot", legacyGeneration: 1}
}
func mpDraftRecipe() mpTranscodeRecipe {
	return mpTranscodeRecipe{profile: "h264", target: "screen", selection: "{\"part\":0}", sourceRevision: strings.Repeat("b", 64), transformation: strings.Repeat("c", 64)}
}
func mpDraftIdentity() CommandIdentity {
	return CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}
}

// Tiny preparation-only checks, without database fixtures or external work.
func TestMPPrepDraftShape(t *testing.T) {
	seen := map[stmt.ID]bool{}
	for _, defs := range [][]stmt.Definition{mpStartPrepDefinitions(), mpTranscodeDefinitions(), mpPrepReceiptDefinitions()} {
		if _, err := stmt.Describe(defs); err != nil {
			t.Fatal(err)
		}
		for _, d := range defs {
			if len(d.Params) != strings.Count(d.SQL, "?") {
				t.Errorf("%s: placeholders=%d params=%d", d.ID, strings.Count(d.SQL, "?"), len(d.Params))
			}
			if !strings.HasPrefix(string(d.ID), "mp.") {
				t.Fatal(d.ID)
			}
			if seen[d.ID] && !strings.HasPrefix(string(d.ID), "mp.prep.") {
				t.Fatal("duplicate", d.ID)
			}
			seen[d.ID] = true
		}
	}
}
func TestMPPrepDraftRequestWitnesses(t *testing.T) {
	q := mpStartPrepBegin{rootIncarnation: [16]byte{4}, id: "prep", item: "item", keyDigest: strings.Repeat("d", 64), bodyDigest: strings.Repeat("e", 64), nowMS: 100, deadlineMS: 9000, tokenExpiresMS: 10000, actor: mpDraftActor(), source: mpDraftSource(), results: mpDraftIDs()}
	baseline, err := mpPrepDraftRequestDigest(MPStartPrepBegin, mpDraftIdentity(), q)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*mpStartPrepBegin){func(q *mpStartPrepBegin) { q.actor.epoch++ }, func(q *mpStartPrepBegin) { q.source.configurationGeneration++ }, func(q *mpStartPrepBegin) { q.source.size++ }, func(q *mpStartPrepBegin) { q.bodyDigest = strings.Repeat("f", 64) }, func(q *mpStartPrepBegin) { q.queue.custody++ }, func(q *mpStartPrepBegin) { q.results.session = "different" }, func(q *mpStartPrepBegin) { q.replaces = "previous"; q.replacesGeneration = 2 }} {
		v := q
		change(&v)
		d, err := mpPrepDraftRequestDigest(MPStartPrepBegin, mpDraftIdentity(), v)
		if err != nil || d == baseline {
			t.Fatal("unbound input", err)
		}
	}
	if _, err := mpPrepDraftRequestDigest(MPStartPrepClaim, mpDraftIdentity(), q); !errors.Is(err, ErrInvalid) {
		t.Fatal("wrong command schema", err)
	}
}
func TestMPPrepDraftInputBounds(t *testing.T) {
	if !mpDraftActor().valid(100) || !mpDraftSource().valid() || !mpDraftRecipe().valid() || !mpDraftIDs().valid() {
		t.Fatal("valid input")
	}
	if mpDraftActor().valid(10000) || mpPrepTexts("bad\x00id") || mpPrepTexts(strings.Repeat("x", 4097)) || (mpPrepQueue{id: "queue"}).valid() {
		t.Fatal("invalid input admitted")
	}
}
func TestMPPrepDraftMatchingPurposeAndFinish(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	turn := &commandTurn{transaction: &transaction{ctx: ctx, descriptor: Descriptor{ID: MPStartPrepBegin}}}
	out, err := applyMPTranscodeCreate(ctx, turn, mpTranscodeCreate{})
	if !errors.Is(err, ErrInvalid) || !errors.Is(err, context.Canceled) || out != (mpTranscodeEffect{}) {
		t.Fatal(out, err)
	}
	turn.transaction.descriptor.ID = MPTranscodeCreate
	start, e := applyMPStartPrepBegin(ctx, turn, mpStartPrepBegin{})
	if !errors.Is(e, ErrInvalid) || !errors.Is(e, context.Canceled) || start != (mpStartPrepEffect{}) {
		t.Fatal(start, e)
	}
}
func TestMPPrepDraftReceiptCodec(t *testing.T) {
	h := mpPrepReceiptHeader{identity: mpDraftIdentity(), request: RequestDigest{3}, kind: MPStartPrepClaim, appliedMS: 100}
	v := mpStartPrepReceipt{header: h, result: mpStartPrepEffect{rootIncarnation: [16]byte{4}, preparation: "prep", sealed: true, state: "preparing", execution: "exec", revision: 2, deadlineMS: 9000, ids: mpDraftIDs(), cleanupPending: true}}
	d, err := mpStartPrepResultDigest(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*mpStartPrepReceipt){func(v *mpStartPrepReceipt) { v.result.sealed = !v.result.sealed }, func(v *mpStartPrepReceipt) { v.result.ids.session = "other" }, func(v *mpStartPrepReceipt) { v.result.stopExecution = "exec" }, func(v *mpStartPrepReceipt) { v.result.cleanupPending = false }, func(v *mpStartPrepReceipt) { v.header.appliedMS++ }, func(v *mpStartPrepReceipt) { v.header.identity.OperationID[0]++ }} {
		q := v
		change(&q)
		different, err := mpStartPrepResultDigest(q)
		if err == nil && different == d {
			t.Fatal("unbound receipt", err)
		}
	}
	h.kind = MPTranscodeClaim
	tr := mpTranscodeReceipt{header: h, result: mpTranscodeEffect{rootIncarnation: [16]byte{5}, phase: "probing source", sourceRevision: mpDraftRecipe().sourceRevision, job: "job", state: "running", execution: "exec", revision: 2, generation: 1, leaseOwner: 8, leaseGeneration: 1, leaseExpiresMS: 9000, attempts: 1, nextAttemptMS: -1, cleanupPending: true}}
	d, err = mpTranscodeResultDigest(tr)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*mpTranscodeReceipt){func(v *mpTranscodeReceipt) { v.result.phase = "other" }, func(v *mpTranscodeReceipt) { v.result.errorCode = "fault" }, func(v *mpTranscodeReceipt) { v.result.sourceRevision = strings.Repeat("f", 64) }, func(v *mpTranscodeReceipt) { v.result.leaseOwner++ }, func(v *mpTranscodeReceipt) { v.result.leaseGeneration++ }, func(v *mpTranscodeReceipt) { v.result.nextAttemptMS = 0 }, func(v *mpTranscodeReceipt) { v.result.cleanupCursor++ }, func(v *mpTranscodeReceipt) { v.result.cleanupPending = false }} {
		q := tr
		change(&q)
		different, err := mpTranscodeResultDigest(q)
		if err == nil && different == d {
			t.Fatal("unbound job receipt", err)
		}
	}
}
