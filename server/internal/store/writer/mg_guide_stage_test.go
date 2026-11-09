package writer

import (
	"errors"
	"strings"
	"testing"
)

func TestMGGuideNativeStagedProgressReopenAndNoRecount(t *testing.T) {
	w, b, stages := mgGuideBirthFixture(t, false)
	before := mgGuideBeginCommitted(t, w, b)
	if before.progress.staged != ([3]int64{}) || before.progress.sequence != 1 || before.acknowledged != 1 {
		t.Fatal("begin not durable", before)
	}
	var saved []mgGuideReceipt
	for _, stage := range stages {
		receipt, after, result := mgGuideStageCommitted(t, w, before, stage)
		family := int(stage.family) - 1
		if result.operation.progress.staged[family] != before.progress.staged[family]+stage.count {
			t.Fatal("stage total", stage.family, result.operation.progress)
		}
		if after.acknowledged != after.progress.sequence || before.progress.sequence+1 != after.progress.sequence {
			t.Fatal("durable stage progress", before, after)
		}
		if after.progress.staged[family] != receipt.staged[family] || after.progress.bytes != receipt.bytes || after.progress.chain != receipt.chain {
			t.Fatal("receipt did not bind native totals", receipt, after)
		}
		saved = append(saved, receipt)
		before = after
	}
	if before.progress.staged != before.progress.manifest.expected || before.progress.chain != before.progress.manifest.expectedChain {
		t.Fatal("complete imported rows", before.progress)
	}
	if before.progress.sequence != 4 || before.progress.revision != 4 || before.progress.bytes <= 0 {
		t.Fatal("terminal staging progress", before.progress)
	}
	for _, table := range []string{"live_channel_versions", "live_programmes", "live_channel_choices"} {
		var n int64
		if err := w.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table+" WHERE generation_id='new'").Scan(&n); err != nil || n != 1 {
			t.Fatal(table, n, err)
		}
	}
	var head string
	if err := w.db.QueryRowContext(t.Context(), `SELECT active_generation FROM live_sources`).Scan(&head); err != nil || head != "old" {
		t.Fatal("staging moved the serving head", head, err)
	}

	// A completed family cannot be extended, and the original receipt replay has
	// zero new effects and no recount.
	turn := mgGuideTestTurn(t, w, MGGuideStageChannels, mgGuideDefinitions())
	extra, err := freezeMGGuideChannels([]mgGuideChannel{{id: "extra", name: "Extra", position: 1, locator: []byte("x")}})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := mgStageGuideNative(t.Context(), turn, before, mgGuideIdentityAfter(before), extra); !errors.Is(err, ErrConflict) || out != (mgGuideNativeResult{}) {
		t.Fatal("over-count stage", out, err)
	}
	original := saved[0]
	replay, err := mgSaveGuideReceipt(t.Context(), turn, mgGuideNativeResult{operation: before, head: mgGuideHead{source: "source", generation: "old", state: "active", revision: 7}, appliedMS: original.appliedMS}, original.key.identity, original.key.requestDigest, original.key.kind, original.projection)
	if err != nil || replay != original {
		t.Fatal("original stage replay", replay, err)
	}
	var changes int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	if _, err := mgSaveGuideReceipt(t.Context(), turn, mgGuideNativeResult{operation: before}, original.key.identity, original.key.requestDigest, original.key.kind, original.projection); err != nil {
		t.Fatal(err)
	}
	var afterChanges int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&afterChanges); err != nil {
		t.Fatal(err)
	}
	if afterChanges != changes {
		t.Fatal("receipt replay made effects", changes, afterChanges)
	}
	// Same request and identity at an already accepted sequence is a conflict,
	// never a second native stage or counter recount.
	if out, err := mgStageGuideNative(t.Context(), turn, before, original.key.identity, stages[0]); !errors.Is(err, ErrConflict) || out != (mgGuideNativeResult{}) {
		t.Fatal("reused sequence staged", out, err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var rows, byteCount int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*),coalesce(sum(length(channel_id)),0) FROM live_channel_versions WHERE generation_id='new'`).Scan(&rows, &byteCount); err != nil || rows != 1 {
		t.Fatal("reused sequence recounted", rows, err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT staged_bytes FROM live_operations WHERE request_id='guide-request'`).Scan(&byteCount); err != nil || byteCount != before.progress.bytes {
		t.Fatal("staged bytes drifted", byteCount, err)
	}
}

func TestMGGuideNativeStageRejectsDuplicateHoleAndOverCount(t *testing.T) {
	for _, test := range []string{"duplicate", "hole", "over-count", "foreign-incarnation", "family-purpose", "returning-retarget", "late-cas"} {
		t.Run(test, func(t *testing.T) {
			w, b, stages := mgGuideBirthFixture(t, true)
			if test == "duplicate" {
				b.manifest.expected[0] = 2
				b.manifest.expectedChain = mgGuideChainFor(b.manifest, nil, 2)
			}
			before := mgGuideBeginCommitted(t, w, b)
			defs := mgGuideDefinitions()
			stage := stages[0]
			identity := mgGuideIdentityAfter(before)
			purpose := MGGuideStageChannels
			switch test {
			case "foreign-incarnation":
				identity.Incarnation[0]++
			case "family-purpose":
				purpose = MGGuideStageProgrammes
			case "duplicate":
				var err error
				stage, err = freezeMGGuideChannels([]mgGuideChannel{{id: "duplicate", name: "One", position: 0, locator: []byte("one")}, {id: "duplicate", name: "Two", position: 1, locator: []byte("two")}})
				if err != nil {
					t.Fatal(err)
				}
			case "hole":
				identity.Sequence++
				identity.OperationID[0]++
			case "over-count":
				var err error
				stage, err = freezeMGGuideChannels([]mgGuideChannel{{id: "one", name: "One", position: 0, locator: []byte("one")}, {id: "two", name: "Two", position: 1, locator: []byte("two")}})
				if err != nil {
					t.Fatal(err)
				}
			case "returning-retarget":
				for j := range defs {
					if defs[j].ID == mgGuideInsertChannel {
						defs[j].SQL = strings.Replace(defs[j].SQL, "RETURNING generation_id,channel_id,", "RETURNING generation_id,'other',", 1)
					}
				}
			case "late-cas":
				for j := range defs {
					if defs[j].ID == mgGuideAdvanceStage {
						defs[j].SQL = strings.Replace(defs[j].SQL, "request_id=?", "request_id=? AND result_revision=-1", 1)
					}
				}
			}
			turn := mgGuideTestTurn(t, w, purpose, defs)
			out, err := mgStageGuideNative(t.Context(), turn, before, identity, stage)
			if err == nil || out != (mgGuideNativeResult{}) {
				t.Fatal("failed stage exposed a result", out, err)
			}
			var prefix int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM live_channel_versions WHERE generation_id='new'`).Scan(&prefix); err != nil {
				t.Fatal(err)
			}
			wantPrefix := int64(0)
			if test == "duplicate" || test == "returning-retarget" || test == "late-cas" {
				wantPrefix = 1
			}
			if prefix != wantPrefix {
				t.Fatal("control did not exercise the expected prefix", test, prefix, err)
			}
			mgGuideRollback(t, turn)
			var rows, sequence, revision int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM live_channel_versions WHERE generation_id='new'),last_command_sequence,stage_revision FROM live_operations WHERE request_id='guide-request'`).Scan(&rows, &sequence, &revision); err != nil {
				t.Fatal(err)
			}
			if rows != 0 || sequence != before.progress.sequence || revision != before.progress.revision {
				t.Fatal("failed stage escaped the frame", rows, sequence, revision)
			}
		})
	}
}

func TestMGGuideStagePayloadOwnershipAndBounds(t *testing.T) {
	locator := []byte("sealed")
	logo := "logo"
	rows := []mgGuideChannel{{id: "channel", name: "Channel", locator: locator, logo: &logo}}
	frozen, err := freezeMGGuideChannels(rows)
	if err != nil {
		t.Fatal(err)
	}
	locator[0] = 'X'
	logo = "different"
	rows[0].name = "changed"
	if string(frozen.rows[0][7].([]byte)) != "sealed" || frozen.rows[0][2] != "Channel" || frozen.rows[0][16] != "logo" {
		t.Fatal("caller changed an owned stage", frozen)
	}
	for _, bad := range [][]mgGuideChannel{
		nil,
		make([]mgGuideChannel, 9),
		{{id: "bad", name: string([]byte{255}), locator: []byte("x")}},
		{{id: "bad", name: "Large", locator: make([]byte, mgGuideStageBytes+1)}},
	} {
		if out, err := freezeMGGuideChannels(bad); err == nil || out.count != 0 {
			t.Fatal("unbounded or malformed stage", out.count, err)
		}
	}
	for _, bad := range [][]mgGuideProgramme{nil, make([]mgGuideProgramme, 9), {{id: "p", channel: "c", start: "b", end: "a"}}, {{id: "", channel: "c", start: "a", end: "b"}}} {
		if out, err := freezeMGGuideProgrammes(bad); err == nil || out.count != 0 {
			t.Fatal("unbounded or malformed programme stage", out.count, err)
		}
	}
	for _, bad := range [][]mgGuideChoice{nil, make([]mgGuideChoice, 9), {{key: ""}}} {
		if out, err := freezeMGGuideChoices(bad); err == nil || out.count != 0 {
			t.Fatal("unbounded or malformed choice stage", out.count, err)
		}
	}
}
