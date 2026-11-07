package writer

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func basicReceiptTestDefinitions(command []stmt.Definition) []stmt.Definition {
	defs := append(command, basicReceiptDefinitions()...)
	for _, d := range scanReceiptDefinitions() {
		switch d.ID {
		case scanReceiptOwner, scanReceiptAck, scanReceiptReclaimKey:
			defs = append(defs, d)
		}
	}
	return defs
}

func TestBasicReceiptDraftDefinitions(t *testing.T) {
	defs := basicReceiptDefinitions()
	for _, d := range defs {
		if d.ID == basicReceiptHeaderLengths {
			want := `SELECT octet_length(source_id),octet_length(source_incarnation),octet_length(source_revision),octet_length(next_cursor),octet_length(next_copy_key),octet_length(next_copy_key2),octet_length(next_copy_key3) FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?`
			if d.SQL != want || !reflect.DeepEqual(d.Params, []stmt.Param{{Name: "run_id", Type: stmt.Text}, {Name: "command_incarnation", Type: stmt.Blob}, {Name: "command_sequence", Type: stmt.Integer}}) ||
				!reflect.DeepEqual(d.Plan, stmt.Plan{Hot: true, PrimaryKeys: []string{"scan_command_receipts"}, Seeks: []string{"run_id=? AND command_incarnation=? AND command_sequence=?"}}) {
				t.Fatal("HN exact receipt octet specification drifted", d)
			}
			continue // ONLY Capture's exact HN rule may admit this native point.
		}
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
	}
	if _, err := stmt.Describe(defs); err != nil {
		t.Fatal(err)
	}
	if _, err := stmt.New(defs); !errors.Is(err, stmt.ErrUnqualified) {
		t.Fatal("draft definitions acquired executable authority", err)
	}
}

func TestBasicReceiptByteEnvelopeAndZeroErrors(t *testing.T) {
	q := BasicReceiptRequest{Purpose: BasicPurposeStage, Scan: ScanReceiptRequest{RunID: "run", LibraryID: 7, LibraryKey: "library"}}
	base, err := basicReceiptRequiredBytes(q, [7]int64{})
	if err != nil {
		t.Fatal(err)
	}
	// Actual UTF-8 and embedded NUL byte counts, not code points or a guessed
	// revision format. The shared codec independently validates UTF-8.
	lengths := [7]int64{int64(len("source")), int64(len("incarnation")), int64(len("観測\x00revision")), 0, int64(len("actual-key")), 0, 0}
	full, err := basicReceiptRequiredBytes(q, lengths)
	if err != nil || full-base != 8*uint64(lengths[0]+lengths[1]+lengths[2]+lengths[4]) {
		t.Fatal(base, full, err)
	}
	for _, bad := range [][7]int64{{-1, 1, 1, 0}, {1, 1, maxCanonicalResultBytes, 0}} {
		if n, err := basicReceiptRequiredBytes(q, bad); n != 0 || !errors.Is(err, ErrCap) {
			t.Fatal(n, err)
		}
	}
	if got, err := lookupBasic(t.Context(), nil, q); err == nil || !reflect.DeepEqual(got, basicReceiptLookup{}) {
		t.Fatal(got, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := acknowledgeBasic(cancelled, nil, q, basicReceiptLookup{owner: receiptOwnerFacts{exists: true, lastSequence: 19}}); got != 0 || !errors.Is(err, context.Canceled) {
		t.Fatal(got, err)
	}
}

// Native tests intentionally require the REAL Capture->stmt.New path. At this
// draft base HN's exact header_lengths rule is absent, so Capture blocks these
// tests. No copy-stub registry, raw execution of the octet point or skip/waiver
// substitutes for that dependency. Run on GitHub once HN is integrated.
func TestBasicReceiptActualStageSealHistoryAndFloorOnlyACK(t *testing.T) {
	w, source, original, identity := basicControlFixture(t)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET family_mask=511 WHERE id=51`); err != nil {
		t.Fatal(err)
	}
	// Exercise an actual saved native TEXT revision, including its NUL and
	// multibyte bytes, through the producer and the shared canonical codec.
	source.objectRevision = "観測\x00revision"
	for _, query := range []string{
		`UPDATE inventory_objects SET revision=? WHERE id='basic-object'`,
		`UPDATE catalog_info_input_witnesses SET object_revision=? WHERE change_id=51`,
	} {
		if _, err := w.db.ExecContext(t.Context(), query, source.objectRevision); err != nil {
			t.Fatal(err)
		}
	}
	stage, err := newBasicStageCommand(identity, basicStageRequest{original: original, purpose: BasicPurposeStage, operation: basicStageFactInput, fact: source, changeRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	defs := basicReceiptTestDefinitions(basicControlCommandDefinitions())
	registry := nativeInventoryFixtureRegistry(t, w, defs, "basic_receipts_test.go", "basic_handlers_test.go", "basic_control_test.go", "basic_stage_test.go")
	turn := basicHandlerTurn(t, w, BasicStage, defs, registry)
	staged, err := applyBasicStage(t.Context(), turn, stage)
	if err != nil {
		t.Fatal(err)
	}
	basicHandlerCommit(t, turn, BasicPurposeStage, original.run.leaseExpiresMS)
	q, err := stage.receiptRequest()
	if err != nil {
		t.Fatal(err)
	}
	turn = basicHandlerTurn(t, w, BasicStage, defs, registry)
	lookup, err := lookupBasic(t.Context(), turn, q)
	if err != nil || lookup.decision != receiptReplay || !reflect.DeepEqual(lookup.result, staged) || lookup.result.Outcome != (ReceiptOutcome{}) {
		t.Fatal(lookup, staged, err)
	}
	if ack, err := acknowledgeBasic(t.Context(), turn, q, lookup); err != nil || ack != identity.Sequence {
		t.Fatal(ack, err)
	}
	// ACK does not consume a fresh lease sample or delete its header/members.
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	var headers, floor int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM scan_command_receipts WHERE run_id='basic-run'),acknowledged_command_sequence FROM inventory_runs WHERE job_id='basic-run'`).Scan(&headers, &floor); err != nil || headers != 1 || floor != identity.Sequence {
		t.Fatal(headers, floor, err)
	}
	original.run.revision, original.run.lastSequence, original.run.acknowledgedSequence = staged.AcceptedRunRevision, identity.Sequence, identity.Sequence
	identity.Sequence++
	identity.OperationID[0]++
	seal, err := newBasicSealCommand(identity, basicSealRequest{original: original, purpose: BasicPurposeSeal, operation: basicSealStreamInput, source: source, changeRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	turn = basicHandlerTurn(t, w, BasicSeal, defs, registry)
	sealed, err := applyBasicSeal(t.Context(), turn, seal)
	if err != nil {
		t.Fatal(err)
	}
	basicHandlerCommit(t, turn, BasicPurposeSeal, original.run.leaseExpiresMS)
	sealQuery, err := seal.receiptRequest()
	if err != nil {
		t.Fatal(err)
	}
	// Historical truth survives actual fresh-gate changes and loss of the
	// live change. These fixture mutations are not reconstructed result data.
	for _, query := range []string{
		`UPDATE inventory_runs SET job_revision=9223372036854775807,job_lease_expires_ms=1 WHERE job_id='basic-run'`,
		`UPDATE library_sources SET generation=generation+1,enabled=0 WHERE id='basic-source'`,
		`DELETE FROM catalog_info_new_stream_facts WHERE change_id=51`,
		`DELETE FROM catalog_info_input_families WHERE change_id=51`,
		`DELETE FROM catalog_info_input_witnesses WHERE change_id=51`,
		`DELETE FROM catalog_info_changes WHERE id=51`,
	} {
		if _, err := w.db.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	turn = basicHandlerTurn(t, w, BasicSeal, defs, registry)
	lookup, err = lookupBasic(t.Context(), turn, sealQuery)
	if err != nil || lookup.decision != receiptReplay || !reflect.DeepEqual(lookup.result, sealed) {
		t.Fatal(lookup, sealed, err)
	}
	if ack, err := acknowledgeBasic(t.Context(), turn, sealQuery, lookup); err != nil || ack != identity.Sequence {
		t.Fatal(ack, err)
	}
	// Corrupt the already ACKed body, then remove its steps. Floor FIRST must
	// still return retired with zero projection, rather than decode old data.
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE scan_command_receipts SET next_copy_id=5 WHERE run_id='basic-run' AND command_sequence=?`, identity.Sequence); err != nil {
		t.Fatal(err)
	}
	delete(turn.steps, basicReceiptHeaderLengths)
	delete(turn.steps, basicReceiptHeader)
	delete(turn.steps, scanReceiptReclaimKey)
	retired, err := lookupBasic(t.Context(), turn, sealQuery)
	if err != nil || retired.decision != receiptRetired || !reflect.DeepEqual(retired.result, BasicResult{}) {
		t.Fatal(retired, err)
	}
}

func TestBasicReceiptThreeNativeControls(t *testing.T) {
	for _, control := range []string{"original_digest", "continuation", "extra_member"} {
		t.Run(control, func(t *testing.T) {
			w, source, original, identity := basicControlFixture(t)
			if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET family_mask=511 WHERE id=51`); err != nil {
				t.Fatal(err)
			}
			command, err := newBasicStageCommand(identity, basicStageRequest{original: original, purpose: BasicPurposeStage, operation: basicStageFactInput, fact: source, changeRevision: 1})
			if err != nil {
				t.Fatal(err)
			}
			defs := basicReceiptTestDefinitions(basicStageCommandDefinitions(command.request))
			registry := nativeInventoryFixtureRegistry(t, w, defs, "basic_receipts_test.go", "basic_handlers_test.go", "basic_control_test.go", "basic_stage_test.go")
			turn := basicHandlerTurn(t, w, BasicStage, defs, registry)
			if _, err := applyBasicStage(t.Context(), turn, command); err != nil {
				t.Fatal(err)
			}
			basicHandlerCommit(t, turn, BasicPurposeStage, original.run.leaseExpiresMS)
			q, err := command.receiptRequest()
			if err != nil {
				t.Fatal(err)
			}
			want := ErrResolveRequired
			switch control {
			case "original_digest":
				q.Scan.Receipt.RequestDigest[0]++
				want = ErrConflict
			case "continuation":
				if _, err := w.db.ExecContext(t.Context(), `UPDATE scan_command_receipts SET next_copy_id=1 WHERE run_id='basic-run'`); err != nil {
					t.Fatal(err)
				}
			case "extra_member":
				for _, query := range []string{
					`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)`,
					`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(31,zeroblob(16),7,1)`,
					`INSERT INTO scan_command_results(run_id,command_incarnation,command_sequence,input_ordinal,child_kind,child_ordinal,object_id,library_id,subject_id,asset_id,accepted_asset_revision) SELECT run_id,command_incarnation,command_sequence,0,0,0,'basic-object',7,31,41,17 FROM scan_command_receipts WHERE run_id='basic-run'`,
				} {
					if _, err := w.db.ExecContext(t.Context(), query); err != nil {
						t.Fatal(err)
					}
				}
			}
			turn = basicHandlerTurn(t, w, BasicStage, defs, registry)
			got, err := lookupBasic(t.Context(), turn, q)
			if !errors.Is(err, want) || !reflect.DeepEqual(got, basicReceiptLookup{}) {
				t.Fatal(got, err)
			}
		})
	}
}
