package writer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func mgGuideHeadFixture(source, generation, state string, revision int64) mgGuideHead {
	return mgGuideHead{source: source, generation: generation, state: state, revision: revision}
}

// A complete guide seals and publishes under one point CAS on the observed
// config revision and the previous published generation. The seals move no
// serving head; the publication does, exactly once, preserving the source's
// enabled/disabled state and every older immutable generation.
func TestMGGuideNativeSealAndPublishCommitted(t *testing.T) {
	for _, state := range []string{"active", "disabled"} {
		t.Run(state, func(t *testing.T) {
			w, b, stages := mgGuideBirthFixture(t, false)
			if _, err := w.db.ExecContext(t.Context(), `UPDATE live_sources SET state=? WHERE id='source'`, state); err != nil {
				t.Fatal(err)
			}
			complete := mgGuideCompleteCommitted(t, w, b, stages)
			path := mgGuideDatabasePath(t, w)
			seal, sealed := mgGuideSealCommitted(t, w, complete)
			if !sealed.progress.sealed || sealed.status != "staging" ||
				sealed.progress.sequence != complete.progress.sequence+1 || sealed.progress.revision != complete.progress.revision+1 {
				t.Fatal("seal did not advance exactly one sealed revision", sealed)
			}
			if seal.key.kind != mgGuideReceiptSeal || seal.sealed != 1 || seal.staged != sealed.progress.staged ||
				seal.chain != sealed.progress.chain || seal.bytes != sealed.progress.bytes {
				t.Fatal("seal receipt did not bind the accepted totals", seal)
			}
			// The seal alone is not a publication: the source still serves "old".
			reader := mgGuideFreshReader(t, path)
			var head, served string
			if err := reader.QueryRowContext(t.Context(), `SELECT active_generation,state FROM live_sources WHERE id='source'`).Scan(&head, &served); err != nil {
				t.Fatal(err)
			}
			if head != "old" || served != state {
				t.Fatal("seal moved the serving head or state", head, served)
			}
			publish, result := mgGuidePublishCommitted(t, w, sealed, false)
			if result.head != mgGuideHeadFixture("source", "new", state, 8) ||
				result.operation.status != "published" || result.operation.resultRevision != 8 ||
				result.operation.resultState != state || result.operation.publishedAt == "" {
				t.Fatal("publication result", result)
			}
			if publish.key.kind != mgGuideReceiptPublish || publish.sealed != 1 || publish.sourceRevision != 8 ||
				publish.sourceState != state || publish.generation != "new" ||
				publish.publishedAt != result.operation.publishedAt || publish.staged != sealed.progress.staged {
				t.Fatal("publication receipt did not bind the original result", publish)
			}
			// Durable state is observed through a separate read-only connection.
			reader = mgGuideFreshReader(t, path)
			var durableName, durableState, generation, published string
			var revision, tuners int64
			if err := reader.QueryRowContext(t.Context(), `SELECT name,revision,state,tuner_count,active_generation,published_at FROM live_sources WHERE id='source'`).Scan(&durableName, &revision, &durableState, &tuners, &generation, &published); err != nil {
				t.Fatal(err)
			}
			if durableName != "Guide" || revision != 8 || durableState != state || generation != "new" || tuners != 1 || published != result.operation.publishedAt {
				t.Fatal("durable head", durableName, revision, durableState, generation, tuners, published)
			}
			var old, programmes, receipts int64
			if err := reader.QueryRowContext(t.Context(), `SELECT count(*) FROM live_generations WHERE id='old'`).Scan(&old); err != nil || old != 1 {
				t.Fatal("old immutable generation lost", old, err)
			}
			if err := reader.QueryRowContext(t.Context(), `SELECT staged_programme_count FROM live_operations WHERE request_id='guide-request'`).Scan(&programmes); err != nil || programmes != 1 {
				t.Fatal("accepted totals drifted", programmes, err)
			}
			if err := reader.QueryRowContext(t.Context(), `SELECT count(*) FROM live_operation_command_receipts`).Scan(&receipts); err != nil || receipts != 6 {
				t.Fatal("original results were not co-committed", receipts, err)
			}
		})
	}
}

// A refresh re-publishes the same configuration: the head moves to the new
// generation while the config revision is deliberately retained.
func TestMGGuideNativeRefreshKeepsConfigRevision(t *testing.T) {
	w, b, stages := mgGuideBirthFixture(t, false)
	complete := mgGuideCompleteCommitted(t, w, b, stages)
	_, sealed := mgGuideSealCommitted(t, w, complete)
	publish, result := mgGuidePublishCommitted(t, w, sealed, true)
	if result.head != mgGuideHeadFixture("source", "new", "active", 7) ||
		result.operation.resultRevision != 7 || publish.sourceRevision != 7 {
		t.Fatal("refresh changed the config revision", result, publish)
	}
}

// A first publication creates the retained source row from exact native
// RETURNING equality; no source exists before it.
func TestMGGuideNativeFirstPublicationCreatesSource(t *testing.T) {
	w, b, stages := mgGuideBirthFixture(t, true)
	complete := mgGuideCompleteCommitted(t, w, b, stages)
	path := mgGuideDatabasePath(t, w)
	if n := mgGuideSourceCount(t, path, "fresh-source"); n != 0 {
		t.Fatal("first publication already had a source row", n)
	}
	_, sealed := mgGuideSealCommitted(t, w, complete)
	publish, result := mgGuidePublishCommitted(t, w, sealed, false)
	if result.head != mgGuideHeadFixture("fresh-source", "new", "active", 1) ||
		publish.sourceRevision != 1 || publish.sourceState != "active" {
		t.Fatal("first publication", result, publish)
	}
	reader := mgGuideFreshReader(t, path)
	var name, state, generation, published string
	var revision, tuners int64
	if err := reader.QueryRowContext(t.Context(), `SELECT name,revision,state,tuner_count,active_generation,published_at FROM live_sources WHERE id='fresh-source'`).Scan(&name, &revision, &state, &tuners, &generation, &published); err != nil {
		t.Fatal(err)
	}
	if name != "Guide" || revision != 1 || state != "active" || tuners != 1 || generation != "new" ||
		published != result.operation.publishedAt {
		t.Fatal("durable first source", name, revision, state, tuners, generation, published)
	}
}

// One channel with no programmes is a complete guide: it seals and publishes.
func TestMGGuideNativeZeroProgrammeGuide(t *testing.T) {
	w, b, stages := mgGuideBirthFixture(t, false)
	b.manifest.expected = [3]int64{1, 0, 1}
	stages = []mgGuideFrozenStage{stages[0], stages[2]}
	b.manifest.expectedChain = mgGuideChainFor(b.manifest, stages, 2)
	complete := mgGuideCompleteCommitted(t, w, b, stages)
	if complete.progress.staged != ([3]int64{1, 0, 1}) {
		t.Fatal("zero-programme staging totals", complete.progress)
	}
	_, sealed := mgGuideSealCommitted(t, w, complete)
	if !sealed.progress.sealed {
		t.Fatal("zero-programme guide did not seal", sealed)
	}
	_, result := mgGuidePublishCommitted(t, w, sealed, false)
	if result.head != mgGuideHeadFixture("source", "new", "active", 8) || result.operation.progress.staged[1] != 0 {
		t.Fatal("zero-programme guide did not publish", result)
	}
}

func mgGuideSourceCount(t *testing.T, path, source string) int64 {
	t.Helper()
	reader := mgGuideFreshReader(t, path)
	var n int64
	if err := reader.QueryRowContext(t.Context(), `SELECT count(*) FROM live_sources WHERE id=?`, source).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func mgGuideHeadGeneration(t *testing.T, path, source string) string {
	t.Helper()
	reader := mgGuideFreshReader(t, path)
	var head string
	if err := reader.QueryRowContext(t.Context(), `SELECT active_generation FROM live_sources WHERE id=?`, source).Scan(&head); err != nil {
		t.Fatal(err)
	}
	return head
}

// Every refusal must return the zero result and leave no durable effect.
func mgGuideRefusedPublication(t *testing.T, w *executor, before mgGuideNative, identity CommandIdentity, refresh bool, want error) {
	t.Helper()
	defs := mgGuidePublicationTestDefinitions()
	turn := mgGuideTestTurn(t, w, MGGuidePublish, defs)
	out, err := mgPublishGuideNative(t.Context(), turn, before, identity, refresh)
	if !errors.Is(err, want) || out != (mgGuideNativeResult{}) {
		t.Fatal("publication refusal", err, out)
	}
	mgGuideRollback(t, turn)
}

func TestMGGuideNativeSealAndPublishFences(t *testing.T) {
	t.Run("unsealed-publication", func(t *testing.T) {
		w, b, stages := mgGuideBirthFixture(t, false)
		complete := mgGuideCompleteCommitted(t, w, b, stages)
		path := mgGuideDatabasePath(t, w)
		mgGuideRefusedPublication(t, w, complete, mgGuideIdentityAfter(complete), false, ErrConflict)
		reader := mgGuideFreshReader(t, path)
		var status string
		var sealed int64
		if err := reader.QueryRowContext(t.Context(), `SELECT status,sealed FROM live_operations WHERE request_id='guide-request'`).Scan(&status, &sealed); err != nil {
			t.Fatal(err)
		}
		if status != "staging" || sealed != 0 || mgGuideHeadGeneration(t, path, "source") != "old" {
			t.Fatal("unsealed publication escaped", status, sealed)
		}
	})
	t.Run("append-after-seal", func(t *testing.T) {
		w, b, stages := mgGuideBirthFixture(t, false)
		complete := mgGuideCompleteCommitted(t, w, b, stages)
		path := mgGuideDatabasePath(t, w)
		_, sealed := mgGuideSealCommitted(t, w, complete)
		extra, err := freezeMGGuideChannels([]mgGuideChannel{{id: "late", name: "Late", position: 1, locator: []byte("late")}})
		if err != nil {
			t.Fatal(err)
		}
		turn := mgGuideTestTurn(t, w, MGGuideStageChannels, mgGuideDefinitions())
		out, err := mgStageGuideNative(t.Context(), turn, sealed, mgGuideIdentityAfter(sealed), extra)
		if !errors.Is(err, ErrConflict) || out != (mgGuideNativeResult{}) {
			t.Fatal("sealed generation accepted an append", out, err)
		}
		mgGuideRollback(t, turn)
		reader := mgGuideFreshReader(t, path)
		var rows, sequence int64
		if err := reader.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM live_channel_versions WHERE generation_id='new'),last_command_sequence FROM live_operations WHERE request_id='guide-request'`).Scan(&rows, &sequence); err != nil {
			t.Fatal(err)
		}
		if rows != 1 || sequence != sealed.progress.sequence {
			t.Fatal("append after seal escaped", rows, sequence)
		}
	})
	t.Run("zero-channel-header", func(t *testing.T) {
		w, before := mgGuideZeroChannelFixture(t)
		defs := mgGuidePublicationTestDefinitions()
		turn := mgGuideTestTurn(t, w, MGGuideSeal, defs)
		out, err := mgSealGuideNative(t.Context(), turn, before, mgGuideIdentityAfter(before))
		if !errors.Is(err, ErrInvalid) || out != (mgGuideNativeResult{}) {
			t.Fatal("zero-channel guide sealed", out, err)
		}
		mgGuideRollback(t, turn)
		path := mgGuideDatabasePath(t, w)
		reader := mgGuideFreshReader(t, path)
		var sealed int64
		if err := reader.QueryRowContext(t.Context(), `SELECT sealed FROM live_operations WHERE request_id='forged-request'`).Scan(&sealed); err != nil || sealed != 0 {
			t.Fatal("zero-channel seal escaped", sealed, err)
		}
	})
	t.Run("seal-digest-mismatch", func(t *testing.T) {
		// The row claims complete counts but its stage digest is not the terminal
		// manifest commitment; the seal must refuse it.
		expected := [3]int64{1, 0, 1}
		manifest := mgGuideInitialChain(mgGuideManifest{source: "source", generation: "forged", expected: expected, expectedRevision: 7, previousGeneration: "old"})
		chain := manifest
		chain[0] ^= 0xff
		w, before := mgGuideForgedHeaderFixture(t, expected, expected, manifest, chain)
		defs := mgGuidePublicationTestDefinitions()
		turn := mgGuideTestTurn(t, w, MGGuideSeal, defs)
		out, err := mgSealGuideNative(t.Context(), turn, before, mgGuideIdentityAfter(before))
		if !errors.Is(err, ErrConflict) || out != (mgGuideNativeResult{}) {
			t.Fatal("uncommitted digest sealed", out, err)
		}
		mgGuideRollback(t, turn)
	})
	t.Run("same-revision-competing-refresh", func(t *testing.T) {
		w, b, stages := mgGuideBirthFixture(t, false)
		first := mgGuideCompleteCommitted(t, w, b, stages)
		second := b
		second.request = "guide-request-2"
		second.manifest.generation = "new2"
		second.manifest.expectedChain = mgGuideChainFor(second.manifest, stages, 2)
		other := mgGuideCompleteCommitted(t, w, second, stages)
		if other.progress.manifest.generation != "new2" {
			t.Fatal("second guide birth", other.progress)
		}
		path := mgGuideDatabasePath(t, w)
		_, sealedA := mgGuideSealCommitted(t, w, first)
		_, sealedB := mgGuideSealCommitted(t, w, other)
		_, published := mgGuidePublishCommitted(t, w, sealedA, true)
		if published.head != mgGuideHeadFixture("source", "new", "active", 7) {
			t.Fatal("first refresh", published.head)
		}
		// The competing refresh observed the same config revision but a now-stale
		// previous generation, so it must be refused.
		mgGuideRefusedPublication(t, w, sealedB, mgGuideIdentityAfter(sealedB), true, ErrConflict)
		if head := mgGuideHeadGeneration(t, path, "source"); head != "new" {
			t.Fatal("competing refresh moved the head", head)
		}
	})
	t.Run("stale-config-revision", func(t *testing.T) {
		w, b, stages := mgGuideBirthFixture(t, false)
		complete := mgGuideCompleteCommitted(t, w, b, stages)
		_, sealed := mgGuideSealCommitted(t, w, complete)
		if _, err := w.db.ExecContext(t.Context(), `UPDATE live_sources SET revision=revision+1 WHERE id='source'`); err != nil {
			t.Fatal(err)
		}
		mgGuideRefusedPublication(t, w, sealed, mgGuideIdentityAfter(sealed), false, ErrConflict)
	})
	t.Run("retired-source", func(t *testing.T) {
		w, b, stages := mgGuideBirthFixture(t, false)
		complete := mgGuideCompleteCommitted(t, w, b, stages)
		_, sealed := mgGuideSealCommitted(t, w, complete)
		if _, err := w.db.ExecContext(t.Context(), `UPDATE live_source_identities SET has_live_source_removals=1,removal_request_id='removed',removal_revision=8,removal_removed_at='now' WHERE id='source'`); err != nil {
			t.Fatal(err)
		}
		mgGuideRefusedPublication(t, w, sealed, mgGuideIdentityAfter(sealed), false, ErrDisclosureDenied)
	})
	t.Run("forged-issuer", func(t *testing.T) {
		for _, name := range []string{"skipped-sequence", "foreign-incarnation", "empty-operation", "unacked-predecessor"} {
			t.Run(name, func(t *testing.T) {
				w, b, stages := mgGuideBirthFixture(t, false)
				complete := mgGuideCompleteCommitted(t, w, b, stages)
				_, sealed := mgGuideSealCommitted(t, w, complete)
				identity := mgGuideIdentityAfter(sealed)
				before := sealed
				switch name {
				case "skipped-sequence":
					identity.Sequence++
				case "foreign-incarnation":
					identity.Incarnation[0]++
				case "empty-operation":
					identity.OperationID = [16]byte{}
				case "unacked-predecessor":
					before.acknowledged--
				}
				mgGuideRefusedPublication(t, w, before, identity, false, ErrConflict)
			})
		}
	})
	t.Run("forged-native-row", func(t *testing.T) {
		w, b, stages := mgGuideBirthFixture(t, false)
		complete := mgGuideCompleteCommitted(t, w, b, stages)
		_, sealed := mgGuideSealCommitted(t, w, complete)
		forged := sealed
		forged.progress.bytes++
		mgGuideRefusedPublication(t, w, forged, mgGuideIdentityAfter(forged), false, ErrConflict)
	})
	t.Run("sealed-twice", func(t *testing.T) {
		w, b, stages := mgGuideBirthFixture(t, false)
		complete := mgGuideCompleteCommitted(t, w, b, stages)
		_, sealed := mgGuideSealCommitted(t, w, complete)
		defs := mgGuidePublicationTestDefinitions()
		turn := mgGuideTestTurn(t, w, MGGuideSeal, defs)
		out, err := mgSealGuideNative(t.Context(), turn, sealed, mgGuideIdentityAfter(sealed))
		if !errors.Is(err, ErrConflict) || out != (mgGuideNativeResult{}) {
			t.Fatal("sealed operation sealed twice", out, err)
		}
		mgGuideRollback(t, turn)
	})
	t.Run("refresh-invents-source", func(t *testing.T) {
		w, b, stages := mgGuideBirthFixture(t, true)
		complete := mgGuideCompleteCommitted(t, w, b, stages)
		_, sealed := mgGuideSealCommitted(t, w, complete)
		path := mgGuideDatabasePath(t, w)
		mgGuideRefusedPublication(t, w, sealed, mgGuideIdentityAfter(sealed), true, ErrConflict)
		if n := mgGuideSourceCount(t, path, "fresh-source"); n != 0 {
			t.Fatal("refresh invented a source", n)
		}
	})
	t.Run("wrong-purpose", func(t *testing.T) {
		w, b, stages := mgGuideBirthFixture(t, false)
		complete := mgGuideCompleteCommitted(t, w, b, stages)
		turn := mgGuideTestTurn(t, w, MGGuideStageChannels, mgGuideDefinitions())
		out, err := mgSealGuideNative(t.Context(), turn, complete, mgGuideIdentityAfter(complete))
		if !errors.Is(err, ErrInvalid) || out != (mgGuideNativeResult{}) {
			t.Fatal("seal borrowed a staging purpose", out, err)
		}
		mgGuideRollback(t, turn)
	})
}

// mgGuideForgedHeaderFixture stores a native operation header that the bounded
// begin/stage commands can never produce. The seal must refuse it from the exact
// native row rather than trust the stored counts.
func mgGuideForgedHeaderFixture(t *testing.T, expected, staged [3]int64, manifest, chain [32]byte) (*executor, mgGuideNative) {
	t.Helper()
	w, _ := testCanonicalExecutor(t)
	zero := `INSERT INTO live_source_identities(id) VALUES('source')`
	queries := []string{
		zero,
		`INSERT INTO live_generations(id,source_id,name,tuner_count,channel_count,programme_count,created_at) VALUES('forged','source','Forged',1,?,?,'before')`,
		`INSERT INTO live_generations(id,source_id,name,tuner_count,channel_count,programme_count,created_at) VALUES('old','source','Old',1,1,1,'before')`,
		`INSERT INTO live_sources(id,name,revision,state,tuner_count,active_generation,published_at) VALUES('source','Old',7,'active',1,'old','before')`,
	}
	for i, q := range queries {
		var err error
		if i == 1 {
			_, err = w.db.ExecContext(t.Context(), q, expected[0], expected[1])
		} else {
			_, err = w.db.ExecContext(t.Context(), q)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	m := mgGuideManifest{source: "source", generation: "forged", expected: expected, expectedRevision: 7, previousGeneration: "old"}
	m.expectedChain = manifest
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO live_operations(request_id,input_hash,source_id,generation_id,expected_revision,status,expected_generation,expected_choice_count,command_incarnation,create_operation_id,birth_request_digest,last_command_sequence,acknowledged_command_sequence,stage_revision,staged_channel_count,staged_programme_count,staged_choice_count,staged_bytes,manifest_digest,stage_digest) VALUES('forged-request','forged-input','source','forged',7,'staging','old',?,?,?,?,2,2,2,?,?,?,100,?,?)`, expected[2], make([]byte, 16), make([]byte, 16), make([]byte, 32), staged[0], staged[1], staged[2], manifest[:], chain[:]); err != nil {
		t.Fatal(err)
	}
	return w, mgGuideNative{
		request: "forged-request", input: "forged-input", name: "Forged", createdAt: "before", status: "staging", tuner: 1,
		progress:        mgGuideProgress{manifest: m, staged: staged, bytes: 100, revision: 2, sequence: 2, chain: chain},
		incarnation:     [16]byte{},
		createOperation: [16]byte{},
		acknowledged:    2,
	}
}

// mgGuideZeroChannelFixture stores a forged native header whose generation has
// no channels. It cannot be born through the bounded begin command, so the seal
// must refuse it rather than accept a zero-channel guide.
func mgGuideZeroChannelFixture(t *testing.T) (*executor, mgGuideNative) {
	t.Helper()
	m := mgGuideManifest{source: "source", generation: "forged"}
	m.expectedChain = mgGuideInitialChain(m)
	return mgGuideForgedHeaderFixture(t, [3]int64{}, [3]int64{}, m.expectedChain, m.expectedChain)
}

// A failure after the source head moved (or after the first source row was
// inserted) must roll the whole frame back: nothing may leak through a crash at
// the head flip.
func TestMGGuidePublicationLateFailureRollsBackHead(t *testing.T) {
	for _, test := range []struct {
		name   string
		target stmt.ID
		fresh  bool
	}{
		{"head-then-operation", mgGuidePublishOperation, false},
		{"source-then-operation", mgGuidePublishSource, false},
		{"inserted-source-then-operation", mgGuidePublishOperation, true},
		{"returning-retarget", mgGuidePublishNewSource, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			w, b, stages := mgGuideBirthFixture(t, test.fresh)
			complete := mgGuideCompleteCommitted(t, w, b, stages)
			_, sealed := mgGuideSealCommitted(t, w, complete)
			path := mgGuideDatabasePath(t, w)
			defs := mgGuidePublicationTestDefinitions()
			for j := range defs {
				switch {
				case defs[j].ID == test.target && test.target == mgGuidePublishOperation:
					defs[j].SQL = strings.Replace(defs[j].SQL, "request_id=?", "request_id=? AND result_revision=-1", 1)
				case defs[j].ID == test.target && test.target == mgGuidePublishSource:
					defs[j].SQL = strings.Replace(defs[j].SQL, "id=?", "id=? AND revision=-1", 1)
				case defs[j].ID == test.target && test.target == mgGuidePublishNewSource:
					defs[j].SQL = strings.Replace(defs[j].SQL, "RETURNING id,", "RETURNING 'other',", 1)
				}
			}
			identity := mgGuideIdentityAfter(sealed)
			turn := mgGuideTestTurn(t, w, MGGuidePublish, defs)
			out, err := mgPublishGuideNative(t.Context(), turn, sealed, identity, false)
			if !errors.Is(err, ErrConflict) || out != (mgGuideNativeResult{}) {
				t.Fatal("late failure released a result", out, err)
			}
			mgGuideRollback(t, turn)
			reader := mgGuideFreshReader(t, path)
			var status string
			var sealedN, publishReceipts int64
			if err := reader.QueryRowContext(t.Context(), `SELECT status,sealed FROM live_operations WHERE request_id=?`, b.request).Scan(&status, &sealedN); err != nil {
				t.Fatal(err)
			}
			if err := reader.QueryRowContext(t.Context(), `SELECT count(*) FROM live_operation_command_receipts WHERE request_id=? AND command_sequence>?`, b.request, sealed.progress.sequence).Scan(&publishReceipts); err != nil {
				t.Fatal(err)
			}
			if status != "staging" || sealedN != 1 || publishReceipts != 0 {
				t.Fatal("partial publication escaped", status, sealedN, publishReceipts)
			}
			if test.fresh {
				if n := mgGuideSourceCount(t, path, "fresh-source"); n != 0 {
					t.Fatal("inserted source escaped rollback", n)
				}
			} else if head := mgGuideHeadGeneration(t, path, "source"); head != "old" {
				t.Fatal("head escaped rollback", head)
			}
		})
	}
}

func TestMGGuidePublicationCancelledLifetime(t *testing.T) {
	w, b, stages := mgGuideBirthFixture(t, false)
	complete := mgGuideCompleteCommitted(t, w, b, stages)
	_, sealed := mgGuideSealCommitted(t, w, complete)
	defs := mgGuidePublicationTestDefinitions()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	turn := mgGuideTestTurn(t, w, MGGuidePublish, defs)
	out, err := mgPublishGuideNative(ctx, turn, sealed, mgGuideIdentityAfter(sealed), false)
	if !errors.Is(err, context.Canceled) || out != (mgGuideNativeResult{}) {
		t.Fatal("cancelled publication", out, err)
	}
	mgGuideRollback(t, turn)
	sealTurn := mgGuideTestTurn(t, w, MGGuideSeal, defs)
	if out, err := mgSealGuideNative(ctx, sealTurn, complete, mgGuideIdentityAfter(complete)); !errors.Is(err, context.Canceled) || out != (mgGuideNativeResult{}) {
		t.Fatal("cancelled seal", out, err)
	}
	mgGuideRollback(t, sealTurn)
	path := mgGuideDatabasePath(t, w)
	if head := mgGuideHeadGeneration(t, path, "source"); head != "old" {
		t.Fatal("cancelled publication moved the head", head)
	}
}

// Original saved results win after ACK and after a retirement floor is installed;
// a floor blocks new lineage and never rewrites the stored original.
func TestMGGuidePublicationReplayAfterACKAndRetirement(t *testing.T) {
	w, b, stages := mgGuideBirthFixture(t, false)
	complete := mgGuideCompleteCommitted(t, w, b, stages)
	seal, sealed := mgGuideSealCommitted(t, w, complete)
	publish, published := mgGuidePublishCommitted(t, w, sealed, false)
	defs := mgGuidePublicationTestDefinitions()
	turn := mgGuideTestTurn(t, w, MGGuidePublish, defs)
	var changes int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	replay, err := mgSaveGuideReceipt(t.Context(), turn, published, publish.key.identity, publish.key.requestDigest, mgGuideReceiptPublish, publish.projection)
	if err != nil || replay != publish {
		t.Fatal("publication replay after ACK", replay, err)
	}
	replaySeal, err := mgSaveGuideReceipt(t.Context(), turn, mgGuideNativeResult{}, seal.key.identity, seal.key.requestDigest, mgGuideReceiptSeal, seal.projection)
	if err != nil || replaySeal != seal {
		t.Fatal("seal replay after publication", replaySeal, err)
	}
	var after int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || after != changes {
		t.Fatal("replay made effects", changes, after, err)
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO live_operation_retirements(request_id,command_incarnation,create_operation_id,birth_request_digest,source_id,final_sequence,final_result_digest,retired_ms,last_command_sequence,acknowledged_command_sequence) VALUES(?,?,?,?,?,?,?,123,?,?)`, b.request, complete.incarnation[:], complete.createOperation[:], complete.birthDigest[:], "source", publish.key.identity.Sequence, publish.resultDigest[:], publish.key.identity.Sequence, publish.key.identity.Sequence); err != nil {
		t.Fatal(err)
	}
	if replay, err := mgSaveGuideReceipt(t.Context(), turn, published, publish.key.identity, publish.key.requestDigest, mgGuideReceiptPublish, publish.projection); err != nil || replay != publish {
		t.Fatal("original publication replay after floor", replay, err)
	}
	next := publish.key.identity
	next.Sequence++
	next.OperationID[0]++
	lineage := published
	lineage.operation.progress.sequence = next.Sequence
	if out, err := mgSaveGuideReceipt(t.Context(), turn, lineage, next, RequestDigest{byte(next.Sequence)}, mgGuideReceiptPublish, publish.projection); !errors.Is(err, ErrConflict) || out != (mgGuideReceipt{}) {
		t.Fatal("retired request accepted new lineage", out, err)
	}
	mgGuideRollback(t, turn)
}

// A lost (unknown) commit is never success in memory: durability is confirmed
// only by a fresh read-only connection, and the replay of the original receipt
// stays effect-free.
func TestMGGuidePublicationLostCommitObservedFresh(t *testing.T) {
	w, b, stages := mgGuideBirthFixture(t, false)
	complete := mgGuideCompleteCommitted(t, w, b, stages)
	path := mgGuideDatabasePath(t, w)
	_, sealed := mgGuideSealCommitted(t, w, complete)
	publish, published := mgGuidePublishCommitted(t, w, sealed, false)
	// The caller's response is discarded; only the independent reader confirms.
	reader := mgGuideFreshReader(t, path)
	var digest []byte
	var sequence, receiptRevision int64
	if err := reader.QueryRowContext(t.Context(), `SELECT result_digest,command_sequence,accepted_revision FROM live_operation_command_receipts WHERE request_id=? AND command_kind='publish'`, b.request).Scan(&digest, &sequence, &receiptRevision); err != nil {
		t.Fatal(err)
	}
	if len(digest) != 32 || sequence != publish.key.identity.Sequence || receiptRevision != 8 {
		t.Fatal("fresh receipt read", len(digest), sequence, receiptRevision)
	}
	var want [32]byte
	copy(want[:], digest)
	if want != publish.resultDigest || published.operation.status != "published" {
		t.Fatal("fresh digest disagrees with the original result", want, publish.resultDigest)
	}
	defs := mgGuidePublicationTestDefinitions()
	turn := mgGuideTestTurn(t, w, MGGuidePublish, defs)
	var changes int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	replay, err := mgSaveGuideReceipt(t.Context(), turn, published, publish.key.identity, publish.key.requestDigest, mgGuideReceiptPublish, publish.projection)
	if err != nil || replay != publish {
		t.Fatal("lost response replay", replay, err)
	}
	var after int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || after != changes {
		t.Fatal("lost response replay made effects", changes, after, err)
	}
	mgGuideRollback(t, turn)
}
