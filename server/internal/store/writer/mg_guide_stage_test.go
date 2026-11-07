package writer

import (
	"strings"
	"testing"
)

func mgGuideStageFixture(t *testing.T) (*executor, mgGuideNative, []mgGuideFrozenStage) {
	t.Helper()
	w, before := mgGuideNativeFixture(t, false, "active")
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
	stages := []mgGuideFrozenStage{channel, programme, choice}
	p := mgGuideProgress{manifest: mgGuideManifest{source: "source", generation: "new", previousGeneration: "old", expectedRevision: 7, expected: [3]int64{1, 1, 1}}, revision: 1, sequence: 1}
	p.chain = mgGuideInitialChain(p.manifest)
	chain := p.chain
	for j, s := range stages {
		chain = mgGuideNextChain(chain, mgGuideStageDelta{s.family, "source", "new", int64(j + 2), 0, s.count, s.bytes + s.count*12, s.payload})
	}
	p.manifest.expectedChain = chain
	before.progress = p
	before.acknowledged = 1
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_generations SET channel_count=1,programme_count=1 WHERE id='new';`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_operations SET expected_choice_count=1,last_command_sequence=1,acknowledged_command_sequence=1,stage_revision=1,staged_channel_count=0,staged_programme_count=0,staged_choice_count=0,staged_bytes=0,manifest_digest=?,stage_digest=?`, p.manifest.expectedChain[:], p.chain[:]); err != nil {
		t.Fatal(err)
	}
	return w, before, stages
}
func TestMGGuideNativeStagesThenSeal(t *testing.T) {
	w, before, stages := mgGuideStageFixture(t)
	turn := mgGuideNativeTurn(t, w)
	for _, stage := range stages {
		out, err := mgStageGuideNative(t.Context(), turn, before, mgGuideIdentityAfter(before), stage)
		if err != nil || out.operation.progress.staged[int(stage.family)-1] != stage.count {
			t.Fatal("stage", stage.family, out, err)
		}
		before = out.operation
		// Component fixture only: exact receipt/ACK composition is independently tested.
		if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE live_operations SET acknowledged_command_sequence=last_command_sequence`); err != nil {
			t.Fatal(err)
		}
		before.acknowledged = before.progress.sequence
	}
	if before.progress.chain != before.progress.manifest.expectedChain {
		t.Fatal("native row commitment", before.progress)
	}
	sealed, err := mgSealGuideNative(t.Context(), turn, before, mgGuideIdentityAfter(before))
	if err != nil || !sealed.operation.progress.sealed {
		t.Fatal(sealed, err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"live_channel_versions", "live_programmes", "live_channel_choices"} {
		var n int64
		if err := w.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table+" WHERE generation_id='new'").Scan(&n); err != nil || n != 1 {
			t.Fatal(table, n, err)
		}
	}
	var head string
	if err := w.db.QueryRowContext(t.Context(), `SELECT active_generation FROM live_sources`).Scan(&head); err != nil || head != "old" {
		t.Fatal("staging moved serving head", head, err)
	}
}
func TestMGGuideNativeStageRollbackAndReturningBinding(t *testing.T) {
	for _, test := range []string{"duplicate", "returning-retarget", "late-cas"} {
		t.Run(test, func(t *testing.T) {
			w, before, stages := mgGuideStageFixture(t)
			defs := append(mgGuidePublicationDefinitions(), mgGuideStageDefinitions()...)
			stage := stages[0]
			switch test {
			case "duplicate":
				var err error
				stage, err = freezeMGGuideChannels([]mgGuideChannel{{id: "duplicate", name: "One", position: 0, locator: []byte("one")}, {id: "duplicate", name: "Two", position: 1, locator: []byte("two")}})
				if err != nil {
					t.Fatal(err)
				}
				before.progress.manifest.expected[0] = 2
				before.progress.chain = mgGuideInitialChain(before.progress.manifest)
				if _, err := w.db.ExecContext(t.Context(), `UPDATE live_operations SET stage_digest=?`, before.progress.chain[:]); err != nil {
					t.Fatal(err)
				}
				if _, err := w.db.ExecContext(t.Context(), `UPDATE live_generations SET channel_count=2 WHERE id='new'`); err != nil {
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
			turn := mgGuideNativeTurn(t, w, defs)
			out, err := mgStageGuideNative(t.Context(), turn, before, mgGuideIdentityAfter(before), stage)
			if err == nil || out != (mgGuideNativeResult{}) {
				t.Fatal("failed stage exposed result", out, err)
			}
			var prefix int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM live_channel_versions WHERE generation_id='new'`).Scan(&prefix); err != nil || prefix != 1 {
				t.Fatal("control did not exercise a partial prefix", prefix, err)
			}
			if err := turn.closeRows(); err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			var count, sequence int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM live_channel_versions WHERE generation_id='new'`).Scan(&count); err != nil || count != 0 {
				t.Fatal("partial insert escaped", count, err)
			}
			if err := w.db.QueryRowContext(t.Context(), `SELECT last_command_sequence FROM live_operations`).Scan(&sequence); err != nil || sequence != 1 {
				t.Fatal("partial sequence escaped", sequence, err)
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
		t.Fatal("caller changed owned stage", frozen)
	}
	for _, bad := range [][]mgGuideChannel{nil, make([]mgGuideChannel, 9), {{id: "bad", name: string([]byte{255}), locator: []byte("x")}}, {{id: "bad", name: "Large", locator: make([]byte, mgGuideStageBytes+1)}}} {
		if out, err := freezeMGGuideChannels(bad); err == nil || out.count != 0 {
			t.Fatal("unbounded/malformed stage", out.count, err)
		}
	}
}
