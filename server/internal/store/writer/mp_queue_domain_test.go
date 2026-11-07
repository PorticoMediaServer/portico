package writer

import (
	"context"
	"database/sql"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func mpTestLayout(n int64) mpQueueLayout {
	return mpQueueLayout{head: mpQueueHead{id: "queue", ownerKind: "device", owner: "device", authority: "local", account: "account", profile: "profile", revision: 3, custody: 2, repeat: "off", entry: "snap-0"}, segments: []mpQueueSegment{{id: "segment", key: "h", snapshot: "snap", count: n}}, snapshots: map[string]mpQueueSnapshot{"snap": {id: "snap", queue: "queue", count: n, built: n, state: "ready", kind: "items"}}, removals: map[string][]int64{}}
}
func mpTestEntries(t *testing.T, l mpQueueLayout) []string {
	t.Helper()
	if err := l.validate(); err != nil {
		t.Fatal(err)
	}
	var out []string
	for i := int64(0); i < l.total(); i++ {
		entry, err := l.entryAt(i)
		if err != nil {
			t.Fatal(err)
		}
		pos, err := l.position(entry)
		if err != nil || pos != i {
			t.Fatalf("inverse %s: %d %v", entry, pos, err)
		}
		out = append(out, entry)
	}
	return out
}
func TestMPQueueChunkCodec(t *testing.T) {
	keys := []string{"item", "item", "音楽"}
	b, err := mpEncodeChunk(keys)
	if err != nil {
		t.Fatal(err)
	}
	out, err := mpDecodeChunk(b)
	if err != nil || !reflect.DeepEqual(out, keys) {
		t.Fatal(out, err)
	}
	b[1] = 'x'
	if out[0] != "item" {
		t.Fatal("decoded values alias input")
	}
	for _, bad := range [][]byte{nil, {0}, {0, 1}, {2, 'x'}, {1, 0}, {2, 'a', 0, 'b', 'c', 0}} {
		if _, err := mpDecodeChunk(bad); err == nil {
			t.Fatalf("admitted malformed chunk %v", bad)
		}
	}
	for _, keys := range [][]string{nil, {""}, {"a\x00b"}, {strings.Repeat("x", 129)}, make([]string, mpChunkKeys+1)} {
		if _, err := mpEncodeChunk(keys); err == nil {
			t.Fatal("admitted bad chunk")
		}
	}
}
func TestMPQueueRangeEditsAndImmutableOccurrences(t *testing.T) {
	old := mpTestLayout(5)
	next := old.clone()
	if err := next.removeEntry("snap-1"); err != nil {
		t.Fatal(err)
	}
	s := mpQueueSnapshot{id: "added", queue: "queue", count: 2, built: 2, state: "ready", kind: "items"}
	if err := next.appendSnapshot(s, "inserted", "tail", 2); err != nil {
		t.Fatal(err)
	}
	want := []string{"snap-0", "snap-2", "added-0", "added-1", "snap-3", "snap-4"}
	if got := mpTestEntries(t, next); !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	if len(old.removals) != 0 || len(old.segments) != 1 {
		t.Fatal("edited caller state")
	}
	if _, err := next.position("snap-1"); err == nil {
		t.Fatal("tombstone resurrected")
	}
	// Boundary insert uses a distinct sort key before the retained segment;
	// native UNIQUE(queue_id,sort_key) never sees a temporary key collision.
	before := old.clone()
	if err := before.appendSnapshot(s, "front", "unused", 0); err != nil {
		t.Fatal(err)
	}
	if before.segments[1].key != old.segments[0].key {
		t.Fatal("before insertion rekeys existing segment")
	}
	mpTestEntries(t, before)
}
func TestMPQueueShuffleSpansAndLapTombstones(t *testing.T) {
	l := mpTestLayout(17)
	if err := l.removeEntry("snap-2"); err != nil {
		t.Fatal(err)
	}
	l.head.entry = "snap-5"
	l.head.position = 4
	sh, changed, err := mpSetShuffle(l, true, 971)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	entries := mpTestEntries(t, sh)
	if entries[0] != "snap-5" || len(entries) != 16 {
		t.Fatal(entries)
	}
	if err := sh.removeEntry(entries[3]); err != nil {
		t.Fatal(err)
	}
	s := mpQueueSnapshot{id: "tail", queue: "queue", count: 2, built: 2, state: "ready", kind: "items"}
	if err := sh.appendSnapshot(s, "tailSeg", "unused", 1); err != nil {
		t.Fatal(err)
	}
	after := mpTestEntries(t, sh)
	if after[1] != "tail-0" || after[2] != "tail-1" {
		t.Fatal(after)
	}
	sh.head.repeat = "all"
	sh.head.position = sh.total() - 1
	next, _, err := mpPlanAdvance(sh, mpAdvanceInput{reason: "completion"})
	if err != nil {
		t.Fatal(err)
	}
	next, err = mpRebuildLap(sh, next)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range mpTestEntries(t, next) {
		if entry == "snap-2" || entry == entries[3] {
			t.Fatal("new lap selected removed occurrence", entry)
		}
	}
	plain, _, err := mpSetShuffle(sh, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	mpTestEntries(t, plain)
	mutant := sh.clone()
	mutant.spans[0].count++
	if mutant.validate() == nil {
		t.Fatal("duplicated span length survived")
	}
}
func TestMPQueueSelectedOrderGolden(t *testing.T) {
	l := mpTestLayout(8)
	l.head.entry = "snap-3"
	l.head.position = 3
	sh, _, err := mpSetShuffle(l, true, 42)
	if err != nil {
		t.Fatal(err)
	}
	// Frozen public algorithm vector, obtained independently by the existing
	// four-round specification (not by calling legacy implementation).
	var got []int64
	for i := int64(0); i < 8; i++ {
		got = append(got, sh.baseSource(i))
	}
	want := []int64{3, 6, 5, 1, 0, 7, 4, 2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selected order %v, want %v", got, want)
	}
}
func TestMPQueueLayoutBounds(t *testing.T) {
	l := mpTestLayout(mpMaxQueueKeys)
	if err := l.validate(); err != nil {
		t.Fatal(err)
	}
	s := mpQueueSnapshot{id: "overflow", queue: "queue", count: 1, built: 1, state: "ready"}
	if err := l.appendSnapshot(s, "seg2", "split", l.total()); err == nil {
		t.Fatal("exceeded queue cap")
	}
	bad := mpTestLayout(3)
	bad.removals["snap"] = []int64{1, 1}
	bad.segments[0].removed = 2
	if bad.validate() == nil {
		t.Fatal("duplicate tombstone admitted")
	}
	bad = mpTestLayout(3)
	bad.segments = append(bad.segments, mpQueueSegment{id: "overlap", key: "z", snapshot: "snap", first: 1, count: 1})
	if bad.validate() == nil {
		t.Fatal("overlapping immutable ranges admitted")
	}
	if _, _, err := mpParseEntry("snap-01"); err == nil {
		t.Fatal("noncanonical occurrence admitted")
	}
}
func TestMPQueueOriginalResultCommitment(t *testing.T) {
	e := mpQueueEffect{queue: "queue", entry: "snap-5", revision: 4, custody: 2, position: 3, changed: true}
	d, err := mpQueueEffectCommitment(mpQueueEdit, e, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*mpQueueEffect){func(v *mpQueueEffect) { v.changed = false }, func(v *mpQueueEffect) { v.custody++ }, func(v *mpQueueEffect) { v.entry = "snap-6" }, func(v *mpQueueEffect) { v.reason = "expired" }, func(v *mpQueueEffect) { v.done = true }} {
		copy := e
		mutate(&copy)
		got, err := mpQueueEffectCommitment(mpQueueEdit, copy, 10)
		if err != nil || got == d {
			t.Fatal("result field absent from commitment", err)
		}
	}
	if _, err := mpResultBytes(CommandID(754), e); err == nil {
		t.Fatal("borrowed parent's repeat purpose")
	}
	if mpQueueRootPurpose(mpCommitNext) || mpNextRootPurpose(mpQueueEdit) || mpQueueRootPurpose(mpQueueBegin) || mpNextRootPurpose(mpPrepareNext) {
		t.Fatal("per-root native result codec accepted another root or a birth")
	}
}

func TestMPQueueShuffledMoveRetainsOccurrence(t *testing.T) {
	l := mpTestLayout(8)
	sh, _, err := mpSetShuffle(l, true, 42)
	if err != nil {
		t.Fatal(err)
	}
	before := mpTestEntries(t, sh)
	moved := before[2]
	anchor := before[5]
	changed, err := sh.moveShuffled(moved, anchor, false)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	after := mpTestEntries(t, sh)
	if after[5] != moved || len(sh.removals) != 0 || len(sh.snapshots) != 1 {
		t.Fatal("shuffled move changed occurrence identity", after)
	}
	if sh.head.entry != l.head.entry || sh.head.position != 0 {
		t.Fatal("move displaced current entry")
	}
}

func TestMPQueueCurrentOwnerAndOriginalLifetime(t *testing.T) {
	h := mpTestLayout(1).head
	a := mpActor{family: "family", device: "other-device", account: h.account, profile: h.profile}
	if mpOwns(a, h, false, false) {
		t.Fatal("other device borrowed narrowed custody")
	}
	h.custody = 0
	if !mpOwns(a, h, false, false) {
		t.Fatal("same profile lost legacy unnarrowed controller scope")
	}
	a.profile = "new-lifetime-profile"
	if mpOwns(a, h, false, false) {
		t.Fatal("device/account substituted for current profile")
	}
}

func TestMPNativeBindingVectorsAreTyped(t *testing.T) {
	defs := append(mpQueueDefinitions(), mpNextDefinitions()...)
	defs = append(defs, mpQueueSaveDefinitions()...)
	byID := map[string]stmt.Definition{}
	for _, d := range defs {
		byID[string(d.ID)] = d
	}
	vectors := map[string][]any{
		"mp.queue.publish":     {"device", int64(2), int64(0), int64(0), "entry", "session", int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(2), "queue", "device", "account", "profile", int64(1), int64(0)},
		"mp.next.insert":       {"next", "queue", "device", "key", "digest", "session", int64(1), "playing", int64(3), int64(1), "entry", int64(11), "media", int64(60000), int64(1000), "family", "current", int64(0), int64(0), int64(2), "object-revision", int64(4), "predecessor-media", int64(5)},
		"mp.queue.save_cursor": {int64(48), int64(45), "building", int64(1000), "profile", "key", "queue", "order", int64(0)},
	}
	for id, args := range vectors {
		d := byID[id]
		if len(args) != len(d.Params) {
			t.Fatal(id, "argument count")
		}
		for i, p := range d.Params {
			switch p.Type {
			case stmt.Text:
				if _, ok := args[i].(string); !ok {
					t.Fatal(id, i)
				}
			case stmt.Integer:
				if _, ok := args[i].(int64); !ok {
					t.Fatal(id, i)
				}
			default:
				t.Fatal(id, i, "unexpected vector type")
			}
		}
	}
}
func TestMPDefinitionsAreInertAndWellTyped(t *testing.T) {
	defs := append(mpQueueDefinitions(), mpNextDefinitions()...)
	defs = append(defs, mpReadingDefinitions()...)
	defs = append(defs, mpQueueSaveDefinitions()...)
	defs = append(defs, mpQueueResultDefinitions()...)
	defs = append(defs, mpQueueCleanupDefinitions()...)
	defs = append(defs, mpNextResultDefinitions()...)
	for _, d := range defs {
		t.Run(string(d.ID), func(t *testing.T) {
			r, err := stmt.Describe([]stmt.Definition{d})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = r.Lookup(d.ID); !errors.Is(err, stmt.ErrUnqualified) {
				t.Fatal("draft became executable", err)
			}
		})
	}
}
func TestMPRejectsBorrowedCommandPurpose(t *testing.T) {
	tx := &transaction{ctx: context.Background(), ownerWindow: ownerWindow{commandID: InventoryAccept}}
	turn := &commandTurn{transaction: tx}
	if err := mpPurpose(turn, mpQueueStage); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tx.ctx = ctx
	tx.ownerWindow.commandID = mpQueueStage
	if err := mpPurpose(turn, mpQueueStage); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestMPBoundariesUseWTurnAndFinish(t *testing.T) {
	for _, name := range []string{"mp_queue_domain_commands.go", "mp_queue_domain_cleanup.go", "mp_queue_domain_save.go", "mp_queue_domain_receipts.go", "mp_next.go", "mp_next_receipts.go", "mp_next_selection.go", "mp_reading.go"} {
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ctx := false
			for _, p := range fn.Type.Params.List {
				if s, ok := p.Type.(*ast.SelectorExpr); ok && s.Sel.Name == "Context" {
					ctx = true
				}
			}
			if !ctx {
				continue
			}
			// Native wrappers returning error alone delegate to guarded mpExec;
			// all releases of typed domain values own the final shared finish.
			if fn.Type.Results == nil || len(fn.Type.Results.List) < 2 {
				continue
			}
			guarded := false
			for _, s := range fn.Body.List {
				if d, ok := s.(*ast.DeferStmt); ok && guardedFinishDefer(fn, d) {
					guarded = true
				}
			}
			if !guarded {
				t.Errorf("%s:%s lacks shared finish", name, fn.Name.Name)
			}
		}
	}
}

func mpTestQueueSavedReceipt() mpQueueSavedResult {
	return mpQueueSavedResult{kind: mpQueueEdit, identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, requestDigest: RequestDigest{3}, queue: "queue", appliedMS: 19, effect: mpQueueEffect{queue: "queue", entry: "snap-0", session: "logical", revision: 4, custody: 2, position: 0, lap: 1, count: 77, cursor: 5, changed: true, done: true}, header: mpQueueReceiptHeader{repeat: "off", retirement: "live", shuffle: true, seed: 42, domain: 2, first: 0, total: 2, remaining: 1, automatic: 7, updated: 19}, segments: []mpQueueReceiptSegment{{id: "seg", snapshot: "snap", kind: "items", label: "original", sourceRevision: "source-old", state: "ready", count: 2}}, retireMedia: []mpReceiptRetireMedia{{media: "media", generation: 3, requestID: "stop / original-音", pending: true}}}
}
func TestMPQueueFReceiptCompleteDigestAndNoDTOFallback(t *testing.T) {
	v := mpTestQueueSavedReceipt()
	original, err := mpQueueSavedCommitment(v)
	if err != nil {
		t.Fatal(err)
	}
	mutations := []func(*mpQueueSavedResult){
		func(v *mpQueueSavedResult) { v.identity.OperationID[0]++ }, func(v *mpQueueSavedResult) { v.requestDigest[0]++ }, func(v *mpQueueSavedResult) { v.header.automatic++ }, func(v *mpQueueSavedResult) { v.header.updated++ }, func(v *mpQueueSavedResult) { v.header.seed++ }, func(v *mpQueueSavedResult) { v.header.domain++ }, func(v *mpQueueSavedResult) { v.header.first++ }, func(v *mpQueueSavedResult) { v.header.repeat = "all" }, func(v *mpQueueSavedResult) { v.header.retirement = "draining" }, func(v *mpQueueSavedResult) { v.header.shuffle = false }, func(v *mpQueueSavedResult) { v.header.total++ }, func(v *mpQueueSavedResult) { v.header.remaining = 0 }, func(v *mpQueueSavedResult) { v.header.duration = sql.NullInt64{Valid: true} }, func(v *mpQueueSavedResult) { v.effect.count++ }, func(v *mpQueueSavedResult) { v.effect.done = false }, func(v *mpQueueSavedResult) { v.effect.reason = "segments" }, func(v *mpQueueSavedResult) { v.segments[0].sourceRevision = "new" }, func(v *mpQueueSavedResult) { v.retireMedia[0].requestID = "different" }, func(v *mpQueueSavedResult) { v.retireMedia[0].pending = false },
	}
	for i, change := range mutations {
		next := v
		next.segments = append([]mpQueueReceiptSegment(nil), v.segments...)
		next.retireMedia = append([]mpReceiptRetireMedia(nil), v.retireMedia...)
		change(&next)
		got, err := mpQueueSavedCommitment(next)
		if err != nil || got == original {
			t.Fatal("native field omitted from queue digest", i, err)
		}
	}
	missing := v
	missing.header = mpQueueReceiptHeader{}
	if _, err = mpQueueSavedCommitment(missing); err == nil {
		t.Fatal("missing original header silently reconstructed")
	}
}
func mpTestNextSavedReceipt() mpNextSavedResult {
	return mpNextSavedResult{token: "next", identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, requestDigest: RequestDigest{3}, kind: mpCommitNext, appliedMS: 19, effect: mpQueueEffect{queue: "queue", token: "next", entry: "snap-1", session: "logical-next", revision: 5, custody: 2, position: 1, lap: 3, changed: true, done: true}, header: mpNextReceiptHeader{state: "committed", media: "media-next", presentationURL: "/original-presentation", sessionGeneration: 1, sessionRevision: 1, mediaGeneration: 8, expires: 1000, physicalPending: true}, retireMedia: []mpReceiptRetireMedia{{media: "media-old", generation: 7, requestID: "exact old request", pending: true}}}
}
func TestMPNextFReceiptDigestPreservesOriginalResults(t *testing.T) {
	v := mpTestNextSavedReceipt()
	original, err := mpNextSavedCommitment(v)
	if err != nil {
		t.Fatal(err)
	}
	for i, change := range []func(*mpNextSavedResult){func(v *mpNextSavedResult) { v.header.presentationURL = "/today" }, func(v *mpNextSavedResult) { v.header.sessionGeneration++ }, func(v *mpNextSavedResult) { v.header.sessionRevision++ }, func(v *mpNextSavedResult) { v.header.mediaGeneration++ }, func(v *mpNextSavedResult) { v.header.expires++ }, func(v *mpNextSavedResult) { v.header.state = "canceled" }, func(v *mpNextSavedResult) { v.effect.lap++ }, func(v *mpNextSavedResult) { v.effect.changed = false }, func(v *mpNextSavedResult) { v.header.physicalPending = false }, func(v *mpNextSavedResult) { v.retireMedia[0].requestID = "changed" }} {
		next := v
		next.retireMedia = append([]mpReceiptRetireMedia(nil), v.retireMedia...)
		change(&next)
		got, err := mpNextSavedCommitment(next)
		if err != nil || got == original {
			t.Fatal("native next field omitted", i, err)
		}
	}
	missing := v
	missing.header = mpNextReceiptHeader{}
	if _, err = mpNextSavedCommitment(missing); err == nil {
		t.Fatal("missing original session projection silently reconstructed")
	}
	// Mutable native exit debt can finish after the immutable receipt has been
	// saved or ACKed. The original proof, URL, requestID and pending bit survive.
	originalChild := v.retireMedia[0]
	debt := originalChild
	debt.pending = false
	again, err := mpNextSavedCommitment(v)
	if err != nil || again != original || !v.retireMedia[0].pending || v.retireMedia[0].requestID != "exact old request" {
		t.Fatal("physical debt mutated original replay", err)
	}
	_ = debt
}
func TestMPFWorkerBindingsAndOwnerTables(t *testing.T) {
	for _, defs := range [][]stmt.Definition{mpQueueResultDefinitions(), mpNextResultDefinitions()} {
		if _, err := stmt.Describe(defs); err != nil {
			t.Fatal(err)
		}
		for _, d := range defs {
			if strings.Count(d.SQL, "?") != len(d.Params) {
				t.Fatal(d.ID, "arity")
			}
			if strings.Contains(d.SQL, "receipt_segments") || strings.Contains(d.SQL, "receipt_retire_media") {
				t.Fatal(d.ID, "obsolete child")
			}
		}
	}
}

func TestMPQueueNextWorkerPayloadControls(t *testing.T) {
	h := mpPrepReceiptHeader{identity: CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1}, request: RequestDigest{3}, resultDigest: ResultDigest{4}, kind: mpQueueSave, appliedMS: 100}
	v := mpWMediaPayload{header: h, version: 1, count: 1, payload: []byte("original")}
	o := mpMediaWorkerBefore{incarnation: h.identity.Incarnation, revision: 1}
	a := mpMediaWorkerAdmission{maxRows: 1, maxBytes: 1000, reclaimDueMS: 101}
	n, err := o.admits("queue", v, a)
	if err != nil || n != 5+96+8 {
		t.Fatal(n, err)
	}
	for _, mutate := range []func(*mpMediaWorkerBefore, *mpMediaWorkerAdmission, *mpWMediaPayload){
		func(o *mpMediaWorkerBefore, a *mpMediaWorkerAdmission, v *mpWMediaPayload) { o.incarnation[0]++ },
		func(o *mpMediaWorkerBefore, a *mpMediaWorkerAdmission, v *mpWMediaPayload) { o.last = 1 },
		func(o *mpMediaWorkerBefore, a *mpMediaWorkerAdmission, v *mpWMediaPayload) { a.maxBytes = n - 1 },
		func(o *mpMediaWorkerBefore, a *mpMediaWorkerAdmission, v *mpWMediaPayload) { a.maxRows = 0 },
		func(o *mpMediaWorkerBefore, a *mpMediaWorkerAdmission, v *mpWMediaPayload) { a.reclaimDueMS = 99 },
		func(o *mpMediaWorkerBefore, a *mpMediaWorkerAdmission, v *mpWMediaPayload) {
			v.payload = make([]byte, 65537)
		},
	} {
		oo, aa, vv := o, a, v
		mutate(&oo, &aa, &vv)
		if _, err = oo.admits("queue", vv, aa); err == nil {
			t.Fatal("invalid admission")
		}
	}
	for _, kind := range []CommandID{750, 761, 770, 771} {
		v.header.kind = kind
		if _, err = storeMPQueueWorkerPayload(t.Context(), nil, "queue", o, a, v); err == nil {
			t.Fatal("borrowed queue purpose", kind)
		}
		if _, err = storeMPNextWorkerPayload(t.Context(), nil, "next", o, a, v); err == nil {
			t.Fatal("borrowed next purpose", kind)
		}
	}
	for _, group := range [][]stmt.Definition{mpQueueWorkerPayloadDefinitions(), mpNextWorkerPayloadDefinitions()} {
		for _, d := range group {
			if strings.HasSuffix(string(d.ID), ".advance") {
				for _, fence := range []string{"mp_owner_revision=?", "mp_command_last_sequence=?", "mp_command_acknowledged_sequence=?", "mp_receipt_rows=?", "mp_receipt_variable_bytes=?", "mp_owner_revision=mp_owner_revision+1", "mp_receipt_reclaim_due_ms"} {
					if !strings.Contains(d.SQL, fence) {
						t.Fatal(d.ID, fence)
					}
				}
			}
		}
	}
	b := mpQueueWorkerBirth{userIncarnation: [16]byte{7}, root: strings.Repeat("r", 22), member: strings.Repeat("m", 32), profileLifetime: strings.Repeat("p", 32)}
	id, err := mpFreshQueueWorkerIncarnation(b)
	if err != nil || id == b.userIncarnation || id == ([16]byte{}) {
		t.Fatal(id, err)
	}
	b.root = ""
	if _, err = mpFreshQueueWorkerIncarnation(b); err == nil {
		t.Fatal("missing historical root")
	}
}
