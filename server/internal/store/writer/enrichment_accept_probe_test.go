package writer

import (
	"errors"
	"testing"
	"testing/synctest"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Reviewer probe: a LATE failure (after seal, head flip, owner finish, origin,
// selection, retain) must leave nothing published.
func TestEnrichmentAcceptLateFailureRollsBackEverything(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, f, begin, image, result := acceptBeginFixture(t)
		cmd := acceptRequestFixture(t, e, f, begin, image, result)
		w, cap, q, a := acceptRuntime(t, e, begin, cmd)
		var pinsBefore int64
		if err := e.db.QueryRowContext(t.Context(), `SELECT proof_pin_count FROM enrichment_command_receipts WHERE provider_operation_id=? AND command_sequence=? AND command_kind='credit_source_seal'`, f.scope.ownerID, f.sourceReceipt.Identity.Sequence).Scan(&pinsBefore); err != nil {
			t.Fatal(err)
		}
		if _, err := e.db.ExecContext(t.Context(), `CREATE TRIGGER probe_late BEFORE INSERT ON enrichment_command_receipts WHEN NEW.command_kind='enrich_accept' BEGIN SELECT RAISE(ABORT,'late probe failure'); END`); err != nil {
			t.Fatal(err)
		}
		got, _, err := w.effective.runEnrichment(t.Context(), w, acceptInvocation(cmd, cap, q))
		if err == nil || got.Outcome.State == Applied || got.Value.accepted != nil || got.Digest != (ResultDigest{}) {
			t.Fatal("late failure published", got, err)
		}
		var sealed, headNull, receipts, pins, applied, current int64
		if err = e.db.QueryRowContext(t.Context(), `SELECT i.sealed,(SELECT credit_image_id IS NULL FROM catalog_entities WHERE id=101),(SELECT count(*) FROM enrichment_command_receipts WHERE command_kind='enrich_accept'),(SELECT proof_pin_count FROM enrichment_command_receipts WHERE provider_operation_id=? AND command_sequence=? AND command_kind='credit_source_seal'),(SELECT status='applied' FROM metadata_provider_operations WHERE id=?),(SELECT proof_current_slot IS NOT NULL FROM metadata_provider_operations WHERE id=?),(SELECT count(*) FROM credit_source_heads) FROM catalog_credit_images i WHERE i.id=?`, f.scope.ownerID, f.sourceReceipt.Identity.Sequence, begin.request.ownerID, begin.request.ownerID, image.id).Scan(&sealed, &headNull, &receipts, &pins, &applied, &current, new(int64)); err != nil {
			t.Fatal(err)
		}
		if sealed != 0 || headNull != 1 || receipts != 0 || pins != pinsBefore || applied != 0 || current != 0 {
			t.Fatal("partial publish after late failure", sealed, headNull, receipts, pins, applied, current)
		}
		var heads, beginReceipts int64
		_ = e.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM credit_source_heads),(SELECT count(*) FROM enrichment_command_receipts WHERE command_kind='effective_stage' AND command_sequence=?)`, begin.identity.Sequence).Scan(&heads, &beginReceipts)
		if heads != 0 || beginReceipts != 1 {
			t.Fatal("source head published or BEGIN dropped", heads, beginReceipts)
		}
		if a.Usage() != ([4]governor.Amounts{}) {
			t.Fatal("leaked grant", a.Usage())
		}
	})
}

// ACK is a CAS: before accept it refuses; after accept it advances once; a
// second ACK is an idempotent retired no-op, never a second advance.
func TestEnrichmentAcceptAckCASAndIdempotence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, f, begin, image, result := acceptBeginFixture(t)
		cmd := acceptRequestFixture(t, e, f, begin, image, result)
		w, cap, q, _ := acceptRuntime(t, e, begin, cmd)
		ackIn := func() *enrichmentInvocation {
			in := acceptInvocation(cmd, cap, q)
			in.action, in.payload = enrichmentAcknowledge, nil
			return in
		}
		_, res, err := w.effective.runEnrichment(t.Context(), w, ackIn())
		if err == nil || res.AcknowledgedThrough != 0 {
			t.Fatal("ack before accept advanced the floor", res, err)
		}
		if _, _, err = w.effective.runEnrichment(t.Context(), w, acceptInvocation(cmd, cap, q)); err != nil {
			t.Fatal(err)
		}
		var ackSeq int64
		read := func() int64 {
			if err := e.db.QueryRowContext(t.Context(), `SELECT acknowledged_command_sequence FROM metadata_provider_operations WHERE id=?`, begin.request.ownerID).Scan(&ackSeq); err != nil {
				t.Fatal(err)
			}
			return ackSeq
		}
		if read() != begin.identity.Sequence {
			t.Fatal("accept moved the floor before ACK", ackSeq)
		}
		_, res, err = w.effective.runEnrichment(t.Context(), w, ackIn())
		if err != nil || res.AcknowledgedThrough != cmd.identity.Sequence || read() != cmd.identity.Sequence {
			t.Fatal("first ack", res, err, ackSeq)
		}
		_, res, err = w.effective.runEnrichment(t.Context(), w, ackIn())
		if err != nil || res.AcknowledgedThrough != cmd.identity.Sequence || read() != cmd.identity.Sequence {
			t.Fatal("second ack not idempotent", res, err, ackSeq)
		}
		// A re-run of the accept after the floor moved is retired and applies nothing.
		again, _, err := w.effective.runEnrichment(t.Context(), w, acceptInvocation(cmd, cap, q))
		if err != nil || again.Outcome.State != ResultRetired {
			t.Fatal("post-ack re-run", again, err)
		}
	})
}

// A request-level foreign operation (different key) can never accept: the
// governed key, payload owner and disclosure all name the original operation.
func TestEnrichmentAcceptForeignOperationRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, f, begin, image, result := acceptBeginFixture(t)
		cmd := acceptRequestFixture(t, e, f, begin, image, result)
		w, cap, q, a := acceptRuntime(t, e, begin, cmd)
		foreign := q
		foreign.ProviderOperationID = "operation-foreign"
		in := acceptInvocation(cmd, cap, foreign)
		in.payload.scope.ownerID = foreign.ProviderOperationID
		got, _, err := w.effective.runEnrichment(t.Context(), w, in)
		if err == nil || got.Outcome.State == Applied || got.Value.accepted != nil {
			t.Fatal("foreign operation accepted", got, err)
		}
		if !errors.Is(err, ErrConflict) && !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrDisclosureDenied) {
			t.Log("refusal class", err)
		}
		var sealed int64
		if err = e.db.QueryRowContext(t.Context(), `SELECT sealed FROM catalog_credit_images WHERE id=?`, image.id).Scan(&sealed); err != nil || sealed != 0 {
			t.Fatal("foreign accept had effect", sealed, err)
		}
		if a.Usage() != ([4]governor.Amounts{}) {
			t.Fatal("leaked grant", a.Usage())
		}
	})
}

func acceptRequestWithReserved(t *testing.T, e *executor, begin *effectiveStageBeginCommand, image effectiveBeginImage, result EffectiveStageBeginResult, reserved int64) *enrichAcceptCommand {
	t.Helper()
	turn, steps := effectiveBeginWholeTurn(t, e)
	defer func() { _, _ = turn.transaction.rollback(nil) }()
	owner, err := effectiveBeginOwnerFacts(t.Context(), turn, steps[enrichmentEffectiveOwner], begin.request.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	entity, err := effectiveBeginEntityFacts(t.Context(), turn, steps[enrichmentEffectiveEntity], begin.request.entity.id)
	if err != nil {
		t.Fatal(err)
	}
	producer, err := effectiveBeginReadProducer(t.Context(), turn, begin.request.ownerID, entity, owner.provider, begin.request.evidenceID, begin.request.publicationID, begin.request.objectID)
	if err != nil {
		t.Fatal(err)
	}
	h := begin.request.header
	seed := effectiveBeginBuildSeed(h)
	id := CommandIdentity{Incarnation: begin.identity.Incarnation, Sequence: begin.identity.Sequence + 1, OperationID: [16]byte{231}}
	cmd, err := newEnrichAcceptCommand(id, begin, enrichAcceptRequest{variant: "accept_provider_credits_v1", beginIdentity: begin.identity, beginRequest: begin.digest, beginResult: result, owner: owner, entity: entity, producer: producer,
		image: enrichAcceptImage{id: image.id, revision: image.revision, header: h, stagedCount: h.expectedCount, stagedBytes: h.expectedBytes, seed: seed, chain: seed}, stageSlot: 0, reservedBytes: reserved})
	if err != nil {
		t.Fatal(err)
	}
	return cmd
}

// The frozen/charged reservation must cover BOTH original retained proofs. A
// reservation equal to the source proof alone (or total-1), even when the owner
// row carries exactly that value, must refuse with capacity and publish nothing.
func TestEnrichmentAcceptChargeMustCoverFullRetainedProof(t *testing.T) {
	for _, tc := range []struct {
		name string
		less func(ev, src int64) int64
	}{
		{"total minus one", func(ev, src int64) int64 { return ev + src - 1 }},
		{"source proof only", func(ev, src int64) int64 { return src }},
		{"evidence proof only", func(ev, src int64) int64 { return ev }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e, f, begin, image, result := acceptBeginFixture(t)
				reserved := tc.less(f.evidenceFacts.RetainedBytes, f.sourceFacts.RetainedBytes)
				cmd := acceptRequestWithReserved(t, e, begin, image, result, reserved)
				if _, err := e.db.ExecContext(t.Context(), `UPDATE metadata_provider_operations SET proof_reserved_bytes=? WHERE id=?`, reserved, begin.request.ownerID); err != nil {
					t.Fatal(err)
				}
				w, cap, q, a := acceptRuntime(t, e, begin, cmd)
				got, _, err := w.effective.runEnrichment(t.Context(), w, acceptInvocation(cmd, cap, q))
				var ref *Refusal
				if err == nil || got.Outcome.State == Applied || !errors.As(err, &ref) || ref.Code != RefusalCapacity {
					t.Fatal("under-charged accept not refused with capacity", got, err)
				}
				var sealed int64
				if err = e.db.QueryRowContext(t.Context(), `SELECT sealed FROM catalog_credit_images WHERE id=?`, image.id).Scan(&sealed); err != nil || sealed != 0 {
					t.Fatal("under-charged accept had effect", sealed, err)
				}
				if a.Usage() != ([4]governor.Amounts{}) {
					t.Fatal("leak", a.Usage())
				}
			})
		})
	}
}

// Outer receipt digest / identity must equal the frozen payload's own, even if
// the scope is made to agree with the outer request.
func TestEnrichmentAcceptPayloadMustMatchOuterReceipt(t *testing.T) {
	for _, tc := range []string{"digest", "operation id"} {
		t.Run(tc, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e, f, begin, image, result := acceptBeginFixture(t)
				cmd := acceptRequestFixture(t, e, f, begin, image, result)
				w, cap, q, _ := acceptRuntime(t, e, begin, cmd)
				q2 := q
				if tc == "digest" {
					q2.Receipt.RequestDigest[0] ^= 1
				} else {
					q2.Receipt.Identity.OperationID[0] ^= 1
				}
				in := acceptInvocation(cmd, cap, q2)
				in.payload.scope.identity, in.payload.scope.request = q2.Receipt.Identity, q2.Receipt.RequestDigest
				got, _, err := w.effective.runEnrichment(t.Context(), w, in)
				if err == nil || got.Outcome.State == Applied || got.Value.accepted != nil {
					t.Fatal("payload accepted under a different outer receipt", got, err)
				}
				var sealed int64
				if err = e.db.QueryRowContext(t.Context(), `SELECT sealed FROM catalog_credit_images WHERE id=?`, image.id).Scan(&sealed); err != nil || sealed != 0 {
					t.Fatal("effect", sealed, err)
				}
			})
		})
	}
}
