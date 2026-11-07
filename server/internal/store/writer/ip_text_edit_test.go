package writer

import (
	"errors"
	"fmt"
	fieldmeta "github.com/PorticoMediaServer/portico/server/internal/store/metadata"
	"testing"
)

func TestIPTextRestoreNativeProjectionHistoryAndHistoricalReplay(t *testing.T) {
	w, base := ipTextFixture(t)
	q := base.request
	c, err := newIPTextEditCommand(base.identity, q, fieldmeta.TextEdit{Action: fieldmeta.RestoreAutomatic})
	if err != nil {
		t.Fatal(err)
	}
	turn := ipTextTurn(t, w, IPFieldRestore)
	got, err := applyIPTextLock(t.Context(), turn, c, 1234)
	if err != nil || !got.value.Changed || got.value.Locked || got.value.Value != "Automatic é" {
		t.Fatal(got, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	var projected, before, after string
	var locked, effective, history int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT e.overview,f.locked,r.effective_changed,r.history_id,h.before_value,h.after_value FROM catalog_entities e JOIN metadata_owner_fields f ON f.entity_id=e.id JOIN metadata_edit_receipts r ON r.owner_id=11 JOIN metadata_owner_history_fields h ON h.history_id=r.history_id WHERE e.id=101`).Scan(&projected, &locked, &effective, &history, &before, &after); err != nil || projected != "Automatic é" || locked != 0 || effective != 1 || history <= 0 || before != "" || after != projected {
		t.Fatal(projected, locked, effective, history, before, after, err)
	}
	if _, err = w.db.ExecContext(t.Context(), `UPDATE catalog_entities SET overview='later',revision=999 WHERE id=101`); err != nil {
		t.Fatal(err)
	}
	replay := ipTextTurn(t, w, IPFieldRestore)
	same, err := applyIPTextLock(t.Context(), replay, c, 9999)
	if err != nil || same.decision != receiptReplay || same.value != got.value || same.digest != got.digest {
		t.Fatal(same, err)
	}
}

func TestIPTextEditNativeCanonicalSortAndRollback(t *testing.T) {
	for _, fault := range []bool{false, true} {
		t.Run(map[bool]string{false: "accept", true: "terminal_failure"}[fault], func(t *testing.T) {
			w, base := ipTextFixture(t)
			if _, err := w.db.ExecContext(t.Context(), `INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at,value_present,automatic_present) VALUES('item',101,'title','Title','Title',0,'prior','prior',1,1)`); err != nil {
				t.Fatal(err)
			}
			q := base.request
			q.Field = fieldmeta.Title
			q.Before = fieldmeta.TextState{Effective: fieldmeta.Text{"Title", true}, Automatic: fieldmeta.Text{"Title", true}}
			q.Projection = ipTextProjection{Title: "Title", SortKey: "title", SortHead: "t", SortKeyKeepArticles: "title", SortHeadKeepArticles: "t"}
			c, err := newIPTextEditCommand(base.identity, q, fieldmeta.TextEdit{Action: fieldmeta.SetText, Value: "The Voyage"})
			if err != nil {
				t.Fatal(err)
			}
			if fault {
				if _, err = w.db.ExecContext(t.Context(), `CREATE TRIGGER ip_edit_fail BEFORE INSERT ON metadata_edit_receipts BEGIN SELECT RAISE(ABORT,'receipt');END`); err != nil {
					t.Fatal(err)
				}
			}
			turn := ipTextTurn(t, w, IPFieldEdit)
			got, err := applyIPTextLock(t.Context(), turn, c, 1234)
			if fault {
				if err == nil || got != (ipTextLockNative{}) {
					t.Fatal(got, err)
				}
				_, _ = turn.transaction.rollback(err)
			} else {
				if err != nil || !got.value.Locked || got.value.Value != "The Voyage" {
					t.Fatal(got, err)
				}
				if _, err = turn.transaction.commit(); err != nil {
					t.Fatal(err)
				}
			}
			var title, key, keep string
			var rev, last, history int64
			if err = w.db.QueryRowContext(t.Context(), `SELECT title,sort_key,sort_key_keep_articles,revision,(SELECT last_command_sequence FROM metadata_edit_owners WHERE id=11),(SELECT count(*) FROM metadata_owner_history_fields) FROM catalog_entities WHERE id=101`).Scan(&title, &key, &keep, &rev, &last, &history); err != nil {
				t.Fatal(err)
			}
			if fault {
				if title != "Title" || key != "title" || keep != "title" || rev != 23 || last != 0 || history != 0 {
					t.Fatal(title, key, keep, rev, last, history)
				}
			} else {
				if title != "The Voyage" || key != "voyage" || keep != "the voyage" || rev != 24 || last != 1 || history != 1 {
					t.Fatal(title, key, keep, rev, last, history)
				}
			}
		})
	}
}
func TestIPTextEditRejectsProjectionDriftAndCachedAfterMutation(t *testing.T) {
	w, base := ipTextFixture(t)
	c, err := newIPTextEditCommand(base.identity, base.request, fieldmeta.TextEdit{Action: fieldmeta.SetText, Value: "Edited"})
	if err != nil {
		t.Fatal(err)
	}
	c.after.State.Effective.Value = "forged"
	got, err := applyIPTextLock(t.Context(), ipTextTurn(t, w, IPFieldEdit), c, 1234)
	if !errors.Is(err, ErrInvalid) || got != (ipTextLockNative{}) {
		t.Fatal(got, err)
	}
}

func TestIPTextMutationIndependentResultWire(t *testing.T) {
	var i CommandIdentity
	var request RequestDigest
	for n := range i.Incarnation {
		i.Incarnation[n] = byte(n)
		i.OperationID[n] = byte(n + 16)
	}
	i.Sequence = 31
	for n := range request {
		request[n] = byte(n + 32)
	}
	v := ipTextMutationSavedResult{IPFieldRestore, ipTextLockResult{11, 13, 17, 19, 23, "item", "description", "", "Adaé", true, true, true, true, EvidenceInteger{29, true}}, true}
	got, err := canonicalResultDigest(i, request, v)
	// 233 bytes independently encoded using struct.pack/hashlib from the
	// written purpose, effective-change flag and common field-result payload.
	if err != nil || fmt.Sprintf("%x", got) != "7d8a90847e069e02f5198fd4716b7903f07c86813ae39a9464a4fcc665689dc8" {
		t.Fatal(got, err)
	}
}
