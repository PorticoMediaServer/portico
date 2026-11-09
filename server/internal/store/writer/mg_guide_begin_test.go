package writer

import (
	"errors"
	"fmt"
	"testing"
)

func TestMGGuideNativeBirthHeaderAndNoResurrection(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		t.Run(fmt.Sprint(fresh), func(t *testing.T) {
			w, b, _ := mgGuideBirthFixture(t, fresh)
			turn := mgGuideTestTurn(t, w, MGGuideBegin, mgGuideDefinitions())
			result, err := mgBeginGuideNative(t.Context(), turn, b)
			if err != nil {
				t.Fatal(err)
			}
			m := b.manifest
			if result.operation.status != "staging" || result.operation.progress.sequence != 1 || result.operation.progress.revision != 1 ||
				result.operation.progress.staged != ([3]int64{}) || result.operation.acknowledged != 0 || result.operation.progress.sealed ||
				result.operation.progress.chain != mgGuideInitialChain(m) || result.operation.progress.manifest.expectedChain != m.expectedChain ||
				result.operation.createOperation != b.identity.OperationID || result.operation.incarnation != b.identity.Incarnation ||
				result.operation.birthDigest != [32]byte(b.requestDigest) || result.operation.name != b.name || result.operation.tuner != b.tuners {
				t.Fatal("birth header", result.operation)
			}
			if fresh {
				if result.head != (mgGuideHead{source: m.source}) {
					t.Fatal("fresh birth invented a head", result.head)
				}
			} else if result.head != (mgGuideHead{source: "source", generation: "old", state: "active", revision: 7}) {
				t.Fatal("birth displaced the serving head", result.head)
			}
			saved, err := mgSaveGuideReceipt(t.Context(), turn, result, b.identity, b.requestDigest, mgGuideReceiptBegin, mgGuideProjectionFixture())
			if err != nil || saved.key.kind != mgGuideReceiptBegin || saved.revision != 1 || saved.staged != ([3]int64{}) {
				t.Fatal(saved, err)
			}
			acked, err := mgACKGuideReceipt(t.Context(), turn, result.operation, saved.key, saved.resultDigest)
			if err != nil || acked.acknowledged != 1 {
				t.Fatal(acked, err)
			}
			mgGuideCommit(t, turn)

			var sources int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM live_sources WHERE id=?`, m.source).Scan(&sources); err != nil {
				t.Fatal(err)
			}
			if fresh && sources != 0 {
				t.Fatal("staging published a serving source", sources)
			}
			reopen := mgGuideTestTurn(t, w, MGGuideBegin, mgGuideDefinitions())
			after, err := mgReadGuideNative(t.Context(), reopen, b.request)
			if err != nil || after != acked {
				t.Fatal("durable staging header", after, err)
			}
			mgGuideRollback(t, reopen)

			// Neither a new generation nor a new namespace may reuse the request ID.
			rebirth := b
			rebirth.manifest.generation = "another-generation"
			rebirth.identity.Incarnation[0]++
			turn = mgGuideTestTurn(t, w, MGGuideBegin, mgGuideDefinitions())
			if out, err := mgBeginGuideNative(t.Context(), turn, rebirth); !errors.Is(err, ErrConflict) || out != (mgGuideNativeResult{}) {
				t.Fatal("request recycled", out, err)
			}
			mgGuideRollback(t, turn)
		})
	}
}

func TestMGGuideNativeBirthRefusals(t *testing.T) {
	for _, test := range []string{"zero-channels", "stale-head", "purpose", "capacity"} {
		t.Run(test, func(t *testing.T) {
			w, b, _ := mgGuideBirthFixture(t, true)
			purpose := MGGuideBegin
			switch test {
			case "zero-channels":
				b.manifest.expected[0] = 0
			case "stale-head":
				b.manifest.expectedRevision = 7
				b.manifest.previousGeneration = "old"
			case "purpose":
				purpose = MGGuideStageChannels
			case "capacity":
				for j := 0; j < 64; j++ {
					source, generation := fmt.Sprintf("capacity-source-%d", j), fmt.Sprintf("capacity-generation-%d", j)
					for _, q := range []struct {
						sql  string
						args []any
					}{
						{`INSERT INTO live_source_identities(id) VALUES(?)`, []any{source}},
						{`INSERT INTO live_generations(id,source_id,name,tuner_count,channel_count,programme_count,created_at) VALUES(?,?,'Capacity',1,1,0,'now')`, []any{generation, source}},
						{`INSERT INTO live_sources(id,name,revision,state,tuner_count,active_generation,published_at) VALUES(?,'Capacity',1,'active',1,?,'now')`, []any{source, generation}},
					} {
						if _, err := w.db.ExecContext(t.Context(), q.sql, q.args...); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			turn := mgGuideTestTurn(t, w, purpose, mgGuideDefinitions())
			if out, err := mgBeginGuideNative(t.Context(), turn, b); err == nil || out != (mgGuideNativeResult{}) {
				t.Fatal("invalid birth", out, err)
			}
			mgGuideRollback(t, turn)
			var identities, generations, operations int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM live_source_identities WHERE id='fresh-source'),(SELECT count(*) FROM live_generations WHERE source_id='fresh-source'),(SELECT count(*) FROM live_operations WHERE request_id='guide-request')`).Scan(&identities, &generations, &operations); err != nil {
				t.Fatal(err)
			}
			if identities != 0 || generations != 0 || operations != 0 {
				t.Fatal("failed birth prefix escaped", identities, generations, operations)
			}
		})
	}
}

func TestMGGuideNativeTimePurposeFence(t *testing.T) {
	w, _ := testCanonicalExecutor(t)
	turn := mgGuideTestTurn(t, w, MGConfigureTuner, mgGuideDefinitions())
	if value, err := mgGuideNativeTime(t.Context(), turn); !errors.Is(err, ErrInvalid) || value != 0 {
		t.Fatal("foreign command borrowed the guide clock", value, err)
	}
	mgGuideRollback(t, turn)
	turn = mgGuideTestTurn(t, w, MGGuideBegin, mgGuideDefinitions())
	if value, err := mgGuideNativeTime(t.Context(), turn); err != nil || value <= 0 {
		t.Fatal("guide component clock", value, err)
	}
}
