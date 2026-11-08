package writer

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// One existing governor-backed frame; numeric inputs here are component-test
// forecasts, never a production recipe or a replacement grant constructor.
func effectiveSourceNativeTurn(t *testing.T, w *executor, budget commandMemoryBudget, purposes ...CommandID) (*commandTurn, *governor.Authority) {
	t.Helper()
	purpose := EffectiveStage
	if len(purposes) > 1 {
		t.Fatal("multiple source proof fixture purposes")
	}
	if len(purposes) == 1 {
		purpose = purposes[0]
	}
	if !effectiveSourceProofPurpose(purpose) {
		t.Fatal("unsupported source proof fixture purpose")
	}
	defs := append(enrichmentEffectiveSourcePinDefinitions(), enrichmentEffectiveSourceControlDefinitions()...)
	// The actual preceding component stage/seal frame selected NORMAL.
	// Assert that held policy for Capture; never repair it to match w.config.
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	policy := w.config
	policy.Synchronous = sqliteconn.Normal
	registry := nativeFixtureRegistry(t, conn, policy, defs, "enrichment_effective_source_native_test.go", "enrichment_effective_source_pin_statements.go", "enrichment_effective_source_controls.go")
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	recipe := turnRecipe{limit: Work{4096, 1000000, 4096, 32 << 20, 25 * time.Millisecond}, setup: Work{16, 500, 16, 65536, time.Millisecond}, terminal: Work{16, 500, 16, 65536, time.Millisecond}}
	g, a, wal, request := governedFixture(t, w.now, governedFixtureProfile{recipe, 16 << 20})
	request.Key.Domain = uint16(purpose)
	request.Key.ID = "operation-one"
	request.Class = governor.Background
	request.Footprint = []governor.Scope{{Library: 7, Domain: uint16(purpose)}}
	if err := refreshWriterWAL(walPathForEffectiveTest(t, w), wal, w.now); err != nil {
		t.Fatal(err)
	}
	decision := g.Try(request)
	if decision.Grant == nil {
		t.Fatal(decision.Refusal)
	}
	custody, err := bindGovernedTurn(purpose, recipe.limit, g, a, wal, decision.Grant, request)
	if err != nil {
		t.Fatal(errors.Join(err, a.Retired(decision.Grant)))
	}
	custody.effectiveSourceMemory = budget
	tx, err := w.begin(t.Context(), purpose, recipe, custody)
	if err != nil {
		if errors.Is(err, ErrBusy) {
			err = errors.Join(err, custody.retired())
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	var steps []statementStep
	for _, def := range defs {
		steps = append(steps, statementStep{def.ID, Work{16, 2000, 16, 65536, 100 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	return turn, a
}

func walPathForEffectiveTest(t *testing.T, w *executor) string {
	t.Helper()
	var seq int64
	var name, path string
	if err := w.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEffectiveSourceNativePointsRefuseLegacyAndUnfundedProof(t *testing.T) {
	for _, unfunded := range []bool{false, true} {
		name := "legacy_body_has_no_original_proof"
		if unfunded {
			name = "no_effective_memory_partition"
		}
		t.Run(name, func(t *testing.T) {
			w, _, request, identity := effectiveBeginWholeFixture(t)
			refs := effectiveSourceReferenceInput()
			refs.source.owner = request.ownerID
			refs.source.receipt.Identity.Incarnation = identity.Incarnation
			refs.source.receipt.Identity.Sequence = request.owner.acknowledgedSequence
			input, err := newEffectiveStageBeginFromSeals(identity, request, refs)
			if err != nil {
				t.Fatal(err)
			}
			budget := commandMemoryBudget{8 << 20, 8 << 20}
			if unfunded {
				budget = commandMemoryBudget{}
			}
			turn, a := effectiveSourceNativeTurn(t, w, budget)
			proof, err := verifyEffectiveSource(t.Context(), turn, input)
			if proof != nil {
				t.Fatal("legacy body or absent partition minted native authority")
			}
			if unfunded {
				var refusal *Refusal
				if !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified {
					t.Fatal("missing same-grant partition did not refuse", err)
				}
			} else if !errors.Is(err, ErrResolveRequired) {
				t.Fatal("legacy body without immutable seal proof did not require resolution", err)
			}
			_, _ = turn.transaction.rollback(err)
			if a.Usage() != ([4]governor.Amounts{}) {
				t.Fatal("native proof refusal leaked physical custody")
			}
		})
	}
}

func TestEffectiveSourceStatementInventoryIncludesBothOriginalReceipts(t *testing.T) {
	defs := append(enrichmentEffectiveSourcePinDefinitions(), enrichmentEffectiveSourceControlDefinitions()...)
	seen := map[stmt.ID]bool{}
	uses := 0
	for _, def := range defs {
		if seen[def.ID] {
			t.Fatal("duplicate native source point", def.ID)
		}
		seen[def.ID] = true
		uses += commandStatementMaxUses(EffectiveStage, def.ID)
	}
	if len(seen) != 8 || uses != 10 {
		t.Fatal("both distinct original seals are not fully reserved", len(seen), uses)
	}
}

// This projection freezes only the verifier's source inputs. It deliberately
// confers no current catalogue/asset/disclosure permission and does not bypass
// newEffectiveStageBeginFromSeals in the later accepting BEGIN harness.
func effectiveNativeLineageProjection(t *testing.T, f nativeEnrichmentSeals) *effectiveStageBeginCommand {
	t.Helper()
	refs := effectiveBeginSourceReferences{
		evidence: effectiveBeginSealReference{owner: f.evidenceFacts.ProviderOperationID, purpose: EvidenceSeal, version: 1, receipt: f.evidenceReceipt, result: f.evidenceResult},
		source:   effectiveBeginSealReference{owner: f.sourceFacts.ProviderOperationID, purpose: CreditSourceSeal, version: 1, receipt: f.sourceReceipt, result: f.sourceResult},
	}
	c := &effectiveStageBeginCommand{identity: f.scope.identity, valid: true, sourceReferences: &refs}
	c.request.variant = "begin"
	c.request.ownerID = f.scope.ownerID
	c.request.evidenceID = f.evidenceFacts.EvidenceID
	c.request.publicationID = f.source.publicationID
	c.request.entity.id = f.scope.entityID
	c.request.owner.provider = f.scope.provider
	c.request.owner.acknowledgedSequence = f.scope.ack
	c.request.producer.Evidence = effectiveBeginEvidence{SubjectID: f.evidenceFacts.SubjectID, Provider: f.evidenceFacts.Provider, Kind: f.evidenceFacts.SubjectKind, ID: f.evidenceFacts.ProviderID, Language: f.evidenceFacts.Language, Region: f.evidenceFacts.Region, Shape: f.evidenceFacts.RequestShape, Digest: f.evidenceFacts.ObservationDigest, Sealed: 1}
	c.request.header.sourceManifest = f.sourceFacts.Commitment
	var err error
	c.digest, err = canonicalRequestDigest(EffectiveStage, effectiveNativeSourceRequestVersion, c.identity, effectiveNativeSourceRequest{"begin_native_seal_refs_v1", c.request, refs})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestEffectiveSourceNativeFactoryReadsActualStagedSealLineage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, f := nativeEnrichmentSealsFixture(t)
		input := effectiveNativeLineageProjection(t, f)
		turn, a := effectiveSourceNativeTurn(t, w, commandMemoryBudget{8 << 20, 8 << 20})
		before := turn.transaction.quantum.Remaining()
		proof, err := verifyEffectiveSource(t.Context(), turn, input)
		if err != nil || proof == nil {
			t.Fatal("actual canonical sealed cohort did not verify", err)
		}
		if proof.evidence.saved.resultDigest != f.evidenceResult || proof.source.saved.resultDigest != f.sourceResult {
			t.Fatal("verifier replaced original canonical seal results")
		}
		after := turn.transaction.quantum.Remaining()
		if before.VMOps-after.VMOps != 10*2000 {
			t.Fatal("native proof did not charge its complete10-use prefix", before, after)
		}
		if err := checkEffectiveVerifiedSource(t.Context(), turn, input, proof); err != nil {
			t.Fatal(err)
		}
		copied := *input
		if err := checkEffectiveVerifiedSource(t.Context(), turn, &copied, proof); !errors.Is(err, ErrInvalid) {
			t.Fatal("different command borrowed native proof", err)
		}
		if err := checkEffectiveVerifiedSource(t.Context(), &commandTurn{transaction: turn.transaction}, input, proof); !errors.Is(err, ErrInvalid) {
			t.Fatal("different turn borrowed native proof", err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		if err := checkEffectiveVerifiedSource(t.Context(), turn, input, proof); err == nil {
			t.Fatal("retired turn retained source authority")
		}
		if a.Usage() != ([4]governor.Amounts{}) {
			t.Fatal("native verification retained physical grant")
		}
	})
}

func TestEffectiveSourceNativeFactoryRejectsChangedOriginalOrCustody(t *testing.T) {
	for _, mode := range []string{"original_commitment", "source_revision", "actual_body_pin"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w, f := nativeEnrichmentSealsFixture(t)
				input := effectiveNativeLineageProjection(t, f)
				switch mode {
				case "original_commitment":
					input.sourceReferences.source.result[0] ^= 1
				case "source_revision":
					if _, err := w.db.ExecContext(t.Context(), "UPDATE metadata_provider_operations SET source_content_revision=source_content_revision+1 WHERE id=?", f.scope.ownerID); err != nil {
						t.Fatal(err)
					}
				case "actual_body_pin":
					input.sourceReferences.evidence.receipt.Identity.Sequence++
				}
				turn, _ := effectiveSourceNativeTurn(t, w, commandMemoryBudget{8 << 20, 8 << 20})
				proof, err := verifyEffectiveSource(t.Context(), turn, input)
				if err == nil || proof != nil {
					t.Fatal("changed native original/custody minted proof", mode, err)
				}
			})
		})
	}
}

func TestEffectiveSourceControlDefinitionsFitClosedGrammar(t *testing.T) {
	for _, def := range enrichmentEffectiveSourceControlDefinitions() {
		if issues := plan.LintSQL(def.SQL); len(issues) != 0 {
			t.Fatal(def.ID, issues)
		}
	}
}
