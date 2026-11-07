package writer

import (
	"context"
	"errors"
	"testing"
)

// Synthetic NORMAL custody only. This test adapter attests no real US authority.
type uoSavedTestSecurity struct {
	deny  bool
	bytes int64
}

func (s *uoSavedTestSecurity) checkSaved(_ context.Context, _ *commandTurn, q uoSavedInput, _ string) error {
	if s.deny {
		return ErrConflict
	}
	return nil
}
func (s *uoSavedTestSecurity) checkSavedItem(context.Context, *commandTurn, uoSavedAddition) error {
	return nil
}
func (s *uoSavedTestSecurity) reserveSavedBytes(_ context.Context, _ *commandTurn, n int64) error {
	s.bytes += n
	return nil
}
func uoSavedTestTurn(t *testing.T, w *executor) *commandTurn {
	return wc008NativeTurn(t, w, SetPersonalFlags, uoSavedDefinitions())
}
func uoSavedTestCreate(t *testing.T, w *executor, s *uoSavedTestSecurity, id string) {
	t.Helper()
	n := "Collection"
	turn := uoSavedTestTurn(t, w)
	v, e := applyUOSaved(t.Context(), turn, uoSavedInput{action: uoSavedCreate, id: id, kind: "collection", name: &n, owner: "owner", authority: "local", account: "account", profile: "profile", created: "2026-11-07T00:00:00Z", security: s})
	if e != nil || v.revision != 1 {
		t.Fatal(v, e)
	}
	uoFlagsCommit(t, turn)
}
func TestUOSavedNativeMembershipUndoOrderTombstone(t *testing.T) {
	w, f := uoFlagsFixture(t)
	s := &uoSavedTestSecurity{}
	uoSavedTestCreate(t, w, s, "saved-one")
	turn := uoSavedTestTurn(t, w)
	q := uoSavedInput{action: uoSavedEntries, id: "saved-one", revision: 1, security: s, additions: []uoSavedAddition{{subject: f.subjectID, library: f.libraryID, revision: 1, admission: 1, public: f.publicID, id: "entry-one"}}}
	v, e := applyUOSaved(t.Context(), turn, q)
	if e != nil || v.revision != 2 || len(v.outcomes) != 1 || v.outcomes[0].status != "added" {
		t.Fatal(v, e)
	}
	uoFlagsCommit(t, turn)
	var key string
	if e = w.db.QueryRowContext(t.Context(), `SELECT order_key FROM saved_resource_entries WHERE id='entry-one'`).Scan(&key); e != nil {
		t.Fatal(e)
	}
	turn = uoSavedTestTurn(t, w)
	v, e = applyUOSaved(t.Context(), turn, uoSavedInput{action: uoSavedEntries, id: q.id, revision: 2, security: s, owner: "owner", token: "undo-one", nowMS: 1000, removals: []string{"missing", "entry-one"}})
	if e != nil || v.undoUntil != 31000 || v.outcomes[0].status != "not_found" {
		t.Fatal(v, e)
	}
	uoFlagsCommit(t, turn)
	turn = uoSavedTestTurn(t, w)
	v, e = applyUOSaved(t.Context(), turn, uoSavedInput{action: uoSavedUndo, id: q.id, revision: 3, security: s, owner: "owner", token: "undo-one", nowMS: 31000})
	if e != nil || v.outcomes[0].id != "entry-one" {
		t.Fatal(v, e)
	}
	uoFlagsCommit(t, turn)
	var restored string
	w.db.QueryRowContext(t.Context(), `SELECT order_key FROM saved_resource_entries WHERE id='entry-one'`).Scan(&restored)
	if restored != key {
		t.Fatal(restored, key)
	}
	wc008NativeExec(t, w, `INSERT INTO saved_pins(owner_key,kind,resource_id,pinned) VALUES('other-user','collection','saved-one',1)`)
	turn = uoSavedTestTurn(t, w)
	v, e = applyUOSaved(t.Context(), turn, uoSavedInput{action: uoSavedDelete, id: q.id, revision: 4, security: s})
	if e != nil || !v.deleted {
		t.Fatal(v, e)
	}
	uoFlagsCommit(t, turn)
	var pins, entries int
	w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM saved_pins`).Scan(&pins)
	w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM saved_resource_entries`).Scan(&entries)
	if pins != 1 || entries != 1 {
		t.Fatal("tombstone fanned out", pins, entries)
	}
	turn = uoSavedTestTurn(t, w)
	c, e := applyUOSavedCleanup(t.Context(), turn, uoSavedCleanupInput{resource: q.id, entry: "entry-one", security: s})
	if e != nil || c.rows != 1 || c.bytes != int64(len("saved-oneentry-one")+len(key)) {
		t.Fatal(c, e)
	}
	uoFlagsCommit(t, turn)
}
func TestUOSavedRefusalAndRollback(t *testing.T) {
	w, _ := uoFlagsFixture(t)
	s := &uoSavedTestSecurity{}
	uoSavedTestCreate(t, w, s, "saved-one")
	for _, q := range []uoSavedInput{{action: 99, id: "saved-one", revision: 1}, {action: uoSavedEdit, id: "saved-one", revision: 2}, {action: uoSavedReorder, id: "saved-one", revision: 1}, {action: uoSavedEdit, id: "saved-one", revision: 1}} {
		q.security = s
		turn := uoSavedTestTurn(t, w)
		_, e := applyUOSaved(t.Context(), turn, q)
		if e == nil {
			t.Fatal("accepted invalid", q)
		}
		uoPersonalRollback(t, turn)
	}
	n := "new"
	s.deny = true
	turn := uoSavedTestTurn(t, w)
	_, e := applyUOSaved(t.Context(), turn, uoSavedInput{action: uoSavedEdit, id: "saved-one", revision: 1, name: &n, security: s})
	if !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	uoPersonalRollback(t, turn)
	s.deny = false
	turn = uoSavedTestTurn(t, w)
	_, e = applyUOSaved(t.Context(), turn, uoSavedInput{action: uoSavedEdit, id: "saved-one", revision: 1, name: &n, security: s})
	if e != nil {
		t.Fatal(e)
	}
	uoPersonalRollback(t, turn)
	var name string
	w.db.QueryRowContext(t.Context(), `SELECT name FROM saved_resources WHERE id='saved-one'`).Scan(&name)
	if name != "Collection" {
		t.Fatal(name)
	}
}
func TestUOSavedReorderModesAndSecurityCustody(t *testing.T) {
	w, f := uoFlagsFixture(t)
	s := &uoSavedTestSecurity{}
	uoSavedTestCreate(t, w, s, "saved-one")
	wc008NativeExec(t, w, `INSERT INTO saved_resource_entries(id,resource_id,item_id,order_key) VALUES('a','saved-one',?,'B'),('b','saved-one',NULL,'M'),('c','saved-one',NULL,'a')`, f.subjectID)
	run := func(ids []string, anchor *string, rev int64) {
		t.Helper()
		turn := uoSavedTestTurn(t, w)
		_, e := applyUOSaved(t.Context(), turn, uoSavedInput{action: uoSavedReorder, id: "saved-one", revision: rev, security: s, order: ids, after: anchor})
		if e != nil {
			t.Fatal(e)
		}
		uoFlagsCommit(t, turn)
	}
	run([]string{"c", "a"}, nil, 1)
	var a, c string
	w.db.QueryRowContext(t.Context(), `SELECT order_key FROM saved_resource_entries WHERE id='a'`).Scan(&a)
	w.db.QueryRowContext(t.Context(), `SELECT order_key FROM saved_resource_entries WHERE id='c'`).Scan(&c)
	if a != "a" || c != "B" {
		t.Fatal(a, c)
	}
	front := ""
	run([]string{"a"}, &front, 2)
	w.db.QueryRowContext(t.Context(), `SELECT order_key FROM saved_resource_entries WHERE id='a'`).Scan(&a)
	if a >= "B" {
		t.Fatal(a)
	}
	anchor := "b"
	run([]string{"c"}, &anchor, 3)
	w.db.QueryRowContext(t.Context(), `SELECT order_key FROM saved_resource_entries WHERE id='c'`).Scan(&c)
	if c <= "M" {
		t.Fatal(c)
	}
	visibility := "server"
	turn := uoSavedTestTurn(t, w)
	_, e := applyUOSaved(t.Context(), turn, uoSavedInput{action: uoSavedEdit, id: "saved-one", revision: 4, security: s, visibility: &visibility})
	if e == nil {
		t.Fatal("NORMAL accepted visibility")
	}
	uoPersonalRollback(t, turn)
}

type uoSavedTestView struct{}

func (uoSavedTestView) validateSavedView(context.Context, *commandTurn, uoSavedView) (string, error) {
	return `{"kind":"movie","sort":"title"}`, nil
}
func TestUOSavedViewBoundary(t *testing.T) {
	w, _ := uoFlagsFixture(t)
	s := &uoSavedTestSecurity{}
	n := "View"
	q := uoSavedInput{action: uoSavedCreate, id: "view-one", kind: "view", owner: "owner", authority: "local", account: "account", profile: "profile", created: "2026-11-07T00:00:00Z", name: &n, view: &uoSavedView{kind: "movie", sort: "title"}, security: s}
	turn := uoSavedTestTurn(t, w)
	if _, e := applyUOSaved(t.Context(), turn, q); e == nil {
		t.Fatal("missing RB witness accepted")
	}
	uoPersonalRollback(t, turn)
	q.validator = uoSavedTestView{}
	turn = uoSavedTestTurn(t, w)
	if _, e := applyUOSaved(t.Context(), turn, q); e != nil {
		t.Fatal(e)
	}
	uoFlagsCommit(t, turn)
	turn = uoSavedTestTurn(t, w)
	if _, e := applyUOSaved(t.Context(), turn, uoSavedInput{action: uoSavedReorder, id: q.id, revision: 1, security: s, order: []string{"x"}}); e == nil {
		t.Fatal("view accepted order")
	}
	uoPersonalRollback(t, turn)
}
func TestUOSavedPublicLimits(t *testing.T) {
	w, _ := uoFlagsFixture(t)
	s := &uoSavedTestSecurity{}
	uoSavedTestCreate(t, w, s, "saved-one")
	for _, q := range []uoSavedInput{{action: uoSavedEntries, additions: make([]uoSavedAddition, 101)}, {action: uoSavedEntries, removals: make([]string, 101)}, {action: uoSavedReorder, order: make([]string, 201)}} {
		q.id = "saved-one"
		q.revision = 1
		q.security = s
		turn := uoSavedTestTurn(t, w)
		if _, e := applyUOSaved(t.Context(), turn, q); e == nil {
			t.Fatal("oversize accepted")
		}
		uoPersonalRollback(t, turn)
	}
}
