package writer

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func sealHistorySnapshot(t *testing.T, w *executor) (*governor.Snapshots, *governor.Authority, governor.Request) {
	t.Helper()
	var seq int
	var name, path string
	if err := w.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	cfg := governor.Config{Now: time.Now, MaxGrantsPerClass: 8}
	cfg.Partitions[governor.Background] = governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 4 << 20, governor.CPUMilli: 1000}
	g, a, err := governor.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	reader := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal, ReadLimits: sqliteconn.ReadLimits{LengthBytes: 4 << 20, Columns: 128}}
	connector, err := sqliteconn.Connector(path, reader)
	if err != nil {
		t.Fatal(err)
	}
	defs := append(enrichmentSealHistoryDefinitions(), enrichmentSealProofLengthDefinitions()...)
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	s, err := governor.NewSnapshots(connector, reader, g, a, registry)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	r := governor.Request{Key: governor.JobKey{Domain: uint16(EvidenceSeal), ID: "seal-history-component"}, Owner: 17, Class: governor.Background,
		Need: governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 2 << 20, governor.CPUMilli: 1000}, Budget: governor.UnitBudget{RawRows: 8}, Deadline: time.Now().Add(time.Second)}
	return s, a, r
}

func sealHistoryNativeReceipt(t *testing.T) (*executor, EnrichmentReceiptRequest, sealResultFacts) {
	t.Helper()
	w, scope, payload := evidenceProducerFixture(t)
	m, err := prepareEvidenceSealManifest(t.Context(), payload, enrichmentControlCapture{})
	if err != nil {
		t.Fatal(err)
	}
	p := evidenceStagePrefix{root: evidenceSealRoot{buildID: "history-original-build", evidenceID: payload.header.ID(), parser: payload.header.input.ParserVersion, buildGeneration: 89, subjectID: payload.header.input.SubjectID, rows: int64(1 + payload.CreditCount())}, manifest: m, expires: "2100-11-05T13:00:00Z"}
	turn := sealEffectTurnPurpose(t, w, evidenceProducerDefinitions(), EvidenceStage)
	p, err = createEvidenceBuild(t.Context(), turn, scope, p, payload)
	if err == nil {
		p, err = stageEvidenceCredits(t.Context(), turn, scope, p, payload.Credits())
	}
	if err != nil {
		t.Fatal(err)
	}
	actual, err := advanceEvidenceSeal(t.Context(), turn, scope, p.root)
	if err != nil {
		t.Fatal(err)
	}
	content := sealReceiptContent{payload.header.input, m, 8876032}
	result, digest, err := projectEvidenceSealResult(scope, p.root, actual, content)
	if err != nil {
		t.Fatal(err)
	}
	if err = writeEvidenceSealReceipt(t.Context(), turn, scope, p.root, actual, content, digest); err != nil {
		t.Fatal(err)
	}
	if err = accountEvidenceSealReference(t.Context(), turn, scope, p.root, actual, m, p.charge, p.providerCharge, p.providerOther); err != nil {
		t.Fatal(err)
	}
	// Harness owner publication only; this does not supply production entry,
	// lease/authority, native proof, common dispatcher or resource qualification.
	if _, err = turn.transaction.conn.ExecContext(t.Context(), "UPDATE metadata_provider_operations SET last_command_sequence=? WHERE id=?", scope.identity.Sequence, scope.ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	return w, EnrichmentReceiptRequest{ProviderOperationID: scope.ownerID, LibraryID: 7, EntityID: scope.entityID, Receipt: ReceiptReference{scope.identity, scope.request}}, result.facts
}

func TestSealHistorySnapshotReplaysSavedResult(t *testing.T) {
	w, request, want := sealHistoryNativeReceipt(t)
	s, a, r := sealHistorySnapshot(t, w)
	var saved enrichmentSealHistory
	_, err := s.ReadForecasted(t.Context(), r, time.Second, governor.SnapshotForecast{NativeRowBytes: 64<<10 + 256, MaxQueries: 4}, func(scope *governor.ReadScope) error {
		var err error
		saved, err = readEnrichmentSealHistory(t.Context(), scope, EvidenceSeal, request)
		return err
	})
	if err != nil || saved.decision != receiptReplay || saved.saved.facts != want || a.Usage() != [4]governor.Amounts{} {
		t.Fatal("saved native replay/retirement", saved, err, a.Usage())
	}
	header := draftMovieStageHeader(t)
	if saved.saved.facts.ObservationDigest != hex.EncodeToString(header.input.ObservationDigest[:]) {
		t.Fatal("native digest representation drift")
	}
}

func TestSealHistoryFloorSkipsRetainedPayload(t *testing.T) {
	w, request, _ := sealHistoryNativeReceipt(t)
	for _, q := range []string{"UPDATE metadata_provider_operations SET acknowledged_command_sequence=last_command_sequence WHERE id='operation-one'", "UPDATE enrichment_command_receipts SET result_digest=zeroblob(32) WHERE command_kind='evidence_seal'"} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	s, a, r := sealHistorySnapshot(t, w)
	var got enrichmentSealHistory
	_, err := s.ReadForecasted(t.Context(), r, time.Second, governor.SnapshotForecast{NativeRowBytes: 512, MaxQueries: 2}, func(scope *governor.ReadScope) error {
		var err error
		got, err = readEnrichmentSealHistory(t.Context(), scope, EvidenceSeal, request)
		return err
	})
	if err != nil || got.decision != receiptRetired || got.saved != (savedSealReceipt{}) || a.Usage() != [4]governor.Amounts{} {
		t.Fatal("floor decoded/served retained corrupted proof", got, err, a.Usage())
	}
}

func TestSealHistoryRefusesPayloadBudgetAndCorruption(t *testing.T) {
	for _, oversize := range []bool{false, true} {
		t.Run(map[bool]string{false: "corrupt_digest", true: "before_payload_copy"}[oversize], func(t *testing.T) {
			w, request, _ := sealHistoryNativeReceipt(t)
			if oversize {
				if _, err := w.db.ExecContext(t.Context(), "UPDATE enrichment_command_receipts SET after_item=? WHERE command_kind='evidence_seal'", strings.Repeat("x", 4096)); err != nil {
					t.Fatal(err)
				}
			} else if _, err := w.db.ExecContext(t.Context(), "UPDATE enrichment_command_receipts SET result_digest=zeroblob(32) WHERE command_kind='evidence_seal'"); err != nil {
				t.Fatal(err)
			}
			s, a, r := sealHistorySnapshot(t, w)
			var got enrichmentSealHistory
			forecast := governor.SnapshotForecast{NativeRowBytes: 64<<10 + 256, MaxQueries: 4}
			if oversize {
				forecast.NativeRowBytes, forecast.MaxQueries = 512, 3
			}
			_, err := s.ReadForecasted(t.Context(), r, time.Second, forecast, func(scope *governor.ReadScope) error {
				var err error
				got, err = readEnrichmentSealHistory(context.Background(), scope, EvidenceSeal, request)
				return err
			})
			if !errors.Is(err, ErrResolveRequired) || got != (enrichmentSealHistory{}) || a.Usage() != [4]governor.Amounts{} {
				t.Fatal("bad native proof exposed partial result/leaked custody", got, err, a.Usage())
			}
			if oversize && !errors.Is(err, governor.ErrSnapshotBudget) {
				t.Fatal("payload copied without byte refusal", err)
			}
		})
	}
}
