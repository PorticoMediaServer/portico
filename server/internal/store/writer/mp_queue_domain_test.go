package writer

import (
	"context"
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
	d, err := mpQueueResultCommitment(mpQueueEdit, e, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*mpQueueEffect){func(v *mpQueueEffect) { v.changed = false }, func(v *mpQueueEffect) { v.custody++ }, func(v *mpQueueEffect) { v.entry = "snap-6" }, func(v *mpQueueEffect) { v.reason = "expired" }, func(v *mpQueueEffect) { v.done = true }} {
		copy := e
		mutate(&copy)
		got, err := mpQueueResultCommitment(mpQueueEdit, copy, 10)
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
