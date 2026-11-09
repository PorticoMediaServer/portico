package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func uoBirthJobFixture(t *testing.T) (*executor, *usOwnerAuthorityProof, uoRootReference, uoBirthJobSource, uoBirthJobResult) {
	t.Helper()
	w, p, reading := uoPublicReadingFixture(t, true)
	public := [16]byte{3, 128}
	// read/mp_prepared.go source pins: the job's native item is a live catalogue
	// entity with this stored public id, kind and library.
	wc008NativeExec(t, w, `INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'library-one','/fixture')`)
	wc008NativeExec(t, w, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)`)
	wc008NativeExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(2111,?,7,1)`, public[:])
	wc008NativeExec(t, w, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) VALUES(2111,?,7,1,'Title','title','t','title','t',0,?,?)`, public[:], public[:], public[:])
	q := uoRootReference{root: reading.root, kind: 740, identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, request: RequestDigest{3}}
	wc008NativeExec(t, w, `INSERT INTO user_mutation_command_owners(root_id,command_incarnation) VALUES(?,?)`, q.root, q.identity.Incarnation[:])
	now := w.now().UnixMilli()
	src := uoBirthJobSource{item: 2111, public: public, library: 7, key: "library-one", kind: 1, sourceRevision: strings.Repeat("c", 64)}
	v := uoBirthJobResult{root: q.root, worker: [16]byte{9}, id: "job-original", itemID: entityid.Encode(public[:]), sourceID: "original-asset", profileID: "portable-720p-high-v3", targetID: "prepared-library", state: "queued", phase: "waiting", generation: 0, bytes: 0, errorCode: "", revision: 1, createdMS: now, updatedMS: now, versionID: "", appliedMS: now}
	return w, p, q, src, v
}

func uoBirthJobTurn(t *testing.T, w *executor, p *usOwnerAuthorityProof) (*commandTurn, *usCurrentSessionAuthorization) {
	t.Helper()
	defs := append(usCurrentSessionDefinitions(), uoRootDefinitions()...)
	defs = append(defs, uoBirthJobDefinitions()...)
	turn := wc008NativeTurn(t, w, 740, defs)
	a, err := authorizeUSCurrentSession(t.Context(), turn, p, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return turn, a
}

func uoBirthJobRow(t *testing.T, turn *commandTurn, p *usOwnerAuthorityProof, src uoBirthJobSource, v uoBirthJobResult) {
	t.Helper()
	i := p.witness.issuer
	_, err := turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO prepared_media_jobs(id,item_id,asset_id,library_id,profile_id,target_id,selection_json,source_revision,principal_json,state,phase,generation,bytes,error_code,revision,created_ms,updated_ms,version_id,mp_command_incarnation,mp_user_root_id,mp_owner_member_incarnation,mp_owner_profile_lifetime) VALUES(?,?,?,?,?,?,'{}',?,'{}',?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		v.id, src.item, v.sourceID, src.key, v.profileID, v.targetID, src.sourceRevision, v.state, v.phase, v.generation, v.bytes, v.errorCode, v.revision, v.createdMS, v.updatedMS, v.versionID, v.worker[:], v.root, i.memberIncarnation, i.profileLifetime)
	if err != nil {
		t.Fatal(err)
	}
}

func TestUOBirthJob(t *testing.T) {
	t.Run("cross-profile-actor-recipe-and-exact-saved-original", func(t *testing.T) {
		w, p, q, src, v := uoBirthJobFixture(t)
		i := p.witness.issuer
		// The original actor is separate from the encoding recipe: a secondary
		// member profile requests an owner-quality recipe.
		if i.profile == v.profileID || i.profileLifetime == "" {
			t.Fatal("fixture collapsed actor profile into recipe")
		}
		turn, a := uoBirthJobTurn(t, w, p)
		lookup, err := lookupUOBirthJob(t.Context(), turn, a, q)
		if err != nil || lookup.root.decision != receiptFresh {
			t.Fatal(lookup, err)
		}
		uoBirthJobRow(t, turn, p, src, v)
		digest, err := persistUOBirthJob(t.Context(), turn, lookup, src, v)
		if err != nil || digest == [32]byte{} {
			t.Fatal(digest, err)
		}
		uoPublicReadingCommit(t, turn)
		var member, lifetime, recipe string
		if err = w.db.QueryRowContext(t.Context(), `SELECT mp_owner_member_incarnation,mp_owner_profile_lifetime,profile_id FROM prepared_media_jobs`).Scan(&member, &lifetime, &recipe); err != nil || member != i.memberIncarnation || lifetime != i.profileLifetime || recipe != v.profileID {
			t.Fatal("actor/recipe binding", member, lifetime, recipe, err)
		}
		// Independent worker incarnation/zero counters; the user sequence and the
		// worker receipt family are never borrowed by this birth.
		if n := uoPublicReadingCount(t, w, `SELECT mp_command_last_sequence+mp_command_acknowledged_sequence+mp_owner_revision-1+mp_receipt_rows+mp_receipt_variable_bytes FROM prepared_media_jobs`); n != 0 {
			t.Fatal("borrowed worker/user sequence", n)
		}
		if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM mp_start_command_receipts`); n != 0 {
			t.Fatal("worker receipt on user birth", n)
		}
		if n := uoPublicReadingCount(t, w, `SELECT receipt_variable_bytes-(SELECT sum(variable_bytes) FROM user_mutation_command_receipts) FROM user_mutation_command_owners`); n != 0 {
			t.Fatal("root byte ledger", n)
		}
		// The complete original survives row deletion and every source/recipe/
		// execution change. No mutable head or job scan reconstructs it.
		wc008NativeExec(t, w, `UPDATE prepared_media_jobs SET item_id=9999, asset_id='changed-source', profile_id='changed-recipe', target_id='changed-target', state='failed', phase='changed', generation=7, bytes=11, error_code='changed', revision=9, created_ms=1, updated_ms=2, version_id='changed-version'`)
		wc008NativeExec(t, w, `DELETE FROM prepared_media_jobs`)
		wc008NativeExec(t, w, `UPDATE user_mutation_roots SET state='draining'`)
		turn, a = uoBirthJobTurn(t, w, p)
		replay, err := lookupUOBirthJob(t.Context(), turn, a, q)
		if err != nil || replay.root.decision != receiptReplay || replay.original != v {
			t.Fatal("saved original", replay, err)
		}
		uoPublicReadingCommit(t, turn)
		if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM prepared_media_jobs`); n != 0 {
			t.Fatal("retry recreated job", n)
		}
		wc008NativeExec(t, w, `UPDATE user_mutation_command_owners SET acknowledged_command_sequence=last_command_sequence`)
		turn, a = uoBirthJobTurn(t, w, p)
		retired, err := lookupUOBirthJob(t.Context(), turn, a, q)
		if err != nil || retired.root.decision != receiptRetired || retired.original != (uoBirthJobResult{}) {
			t.Fatal(retired, err)
		}
		uoPublicReadingCommit(t, turn)
		// A retired owner is custody, never a Fresh original. A second lookup
		// still restores no original, and finalization is refused before it can
		// write a receipt or move the owner counters.
		turn, a = uoBirthJobTurn(t, w, p)
		retired, err = lookupUOBirthJob(t.Context(), turn, a, q)
		if err != nil || retired.root.decision != receiptRetired || retired.original != (uoBirthJobResult{}) {
			t.Fatal(retired, err)
		}
		if _, err = persistUOBirthJob(t.Context(), turn, retired, src, v); !errors.Is(err, ErrInvalid) {
			t.Fatal("retired original persisted", err)
		}
		_, _ = turn.transaction.rollback(nil)
		if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM user_mutation_command_receipts`); n != 1 {
			t.Fatal("retired persist wrote a receipt", n)
		}
		if n := uoPublicReadingCount(t, w, `SELECT receipt_rows FROM user_mutation_command_owners`); n != 1 {
			t.Fatal("retired persist moved owner counters", n)
		}
	})

	t.Run("changed-source-recipe-and-duplicate-conflict", func(t *testing.T) {
		w, p, q, src, v := uoBirthJobFixture(t)
		turn, a := uoBirthJobTurn(t, w, p)
		lookup, err := lookupUOBirthJob(t.Context(), turn, a, q)
		if err != nil {
			t.Fatal(err)
		}
		uoBirthJobRow(t, turn, p, src, v)
		if _, err = persistUOBirthJob(t.Context(), turn, lookup, src, v); err != nil {
			t.Fatal(err)
		}
		if _, err = turn.transaction.commit(); err != nil {
			t.Fatal(err)
		}
		// A second Fresh birth (same worker identity, ack floor moved) that claims
		// a different source, recipe or execution cannot reuse row or receipt.
		wc008NativeExec(t, w, `UPDATE user_mutation_command_owners SET acknowledged_command_sequence=last_command_sequence`)
		other := v
		other.id, other.worker = "job-second", [16]byte{10}
		other.sourceID, other.profileID, other.targetID = "changed-source", "changed-recipe", "changed-target"
		otherSeq := q
		otherSeq.identity.Sequence = 2
		otherSeq.request = RequestDigest{4}
		// The same operation id is a duplicate operation: the native row exists,
		// but the receipt UNIQUE must refuse a second original.
		otherSeq.identity.OperationID = q.identity.OperationID
		turn, a = uoBirthJobTurn(t, w, p)
		second, err := lookupUOBirthJob(t.Context(), turn, a, otherSeq)
		if err != nil || second.root.decision != receiptFresh {
			t.Fatal(second, err)
		}
		uoBirthJobRow(t, turn, p, src, other)
		if _, err = persistUOBirthJob(t.Context(), turn, second, src, other); !errors.Is(err, ErrConflict) {
			t.Fatal("duplicate operation accepted", err)
		}
		if _, err = turn.transaction.commit(); err == nil {
			t.Fatal("duplicate committed")
		}
		if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM prepared_media_jobs`); n != 1 {
			t.Fatal(n)
		}
		if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM user_mutation_command_receipts`); n != 1 {
			t.Fatal(n)
		}
	})

	t.Run("no-owner-synthesis-and-root-binding", func(t *testing.T) {
		for _, mode := range []string{"owner-missing", "root-missing", "foreign-root", "account", "profile", "draining", "changed-incarnation"} {
			t.Run(mode, func(t *testing.T) {
				w, p, q, _, _ := uoBirthJobFixture(t)
				switch mode {
				case "owner-missing":
					wc008NativeExec(t, w, `DELETE FROM user_mutation_command_owners`)
				case "root-missing":
					wc008NativeExec(t, w, `DELETE FROM user_mutation_command_owners`)
					wc008NativeExec(t, w, `DELETE FROM user_mutation_roots`)
				case "foreign-root":
					wc008NativeExec(t, w, `UPDATE user_mutation_roots SET account_id='foreign'`)
				case "account":
					wc008NativeExec(t, w, `UPDATE user_mutation_roots SET account_id='foreign'`)
				case "profile":
					wc008NativeExec(t, w, `UPDATE user_mutation_roots SET profile_id='foreign'`)
				case "draining":
					wc008NativeExec(t, w, `UPDATE user_mutation_roots SET state='draining'`)
				case "changed-incarnation":
					q.identity.Incarnation[0]++
				}
				turn, a := uoBirthJobTurn(t, w, p)
				out, err := lookupUOBirthJob(t.Context(), turn, a, q)
				if err == nil || out != nil {
					t.Fatal(out, err)
				}
				if mode == "root-missing" || mode == "foreign-root" || mode == "account" || mode == "profile" || mode == "draining" {
					if !errors.Is(err, ErrUORootGone) || errors.Is(err, sql.ErrNoRows) {
						t.Fatal("root failure class", err)
					}
				}
				if mode == "owner-missing" && (!errors.Is(err, ErrResolveRequired) || !errors.Is(err, ErrOwnerUnavailable) || errors.Is(err, sql.ErrNoRows)) {
					t.Fatal("missing owner escaped typed mapping", err)
				}
				_, _ = turn.transaction.rollback(nil)
				if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM prepared_media_jobs`); n != 0 {
					t.Fatal(n)
				}
			})
		}
	})

	t.Run("cross-kind-persist-refused", func(t *testing.T) {
		t.Run("start-in-job-turn", func(t *testing.T) {
			w, p, q, src, v := uoBirthJobFixture(t)
			turn, a := uoBirthJobTurn(t, w, p)
			lookup, err := lookupUOBirthJob(t.Context(), turn, a, q)
			if err != nil {
				t.Fatal(err)
			}
			uoBirthJobRow(t, turn, p, src, v)
			now := w.now().UnixMilli()
			start := &uoBirthStartLookup{turn: turn, root: lookup.root}
			startValue := uoBirthStartResult{root: q.root, preparation: "cross-kind-preparation", device: p.witness.device, startKey: strings.Repeat("a", 64), startBody: strings.Repeat("b", 64), item: "cross-item", asset: "cross-asset", worker: [16]byte{9}, createdMS: now, deadlineMS: now + 60000, tokenExpiresMS: p.tokenExpiry.UnixMilli(), revision: 1, retryMS: 500, appliedMS: now}
			// A complete 700 original presented to the 740 finalizer: the closed
			// state holder is per purpose, so only a 740 result may be persisted.
			if _, err = persistUOBirthStart(t.Context(), turn, start, startValue); !errors.Is(err, ErrInvalid) {
				t.Fatal("700 result persisted in a 740 turn", err)
			}
			_, _ = turn.transaction.rollback(nil)
			if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM user_mutation_command_receipts`); n != 0 {
				t.Fatal(n)
			}
			if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM prepared_media_jobs`); n != 0 {
				t.Fatal(n)
			}
		})

		t.Run("job-in-start-turn", func(t *testing.T) {
			w, p, q, src, v := uoBirthJobFixture(t)
			q.kind = 700
			turn, a := uoBirthStartTurn(t, w, p)
			start, err := lookupUOBirthStart(t.Context(), turn, a, q)
			if err != nil || start.root.decision != receiptFresh {
				t.Fatal(start, err)
			}
			job := &uoBirthJobLookup{turn: turn, root: start.root}
			// A complete 740 original presented to the 700 finalizer is refused
			// for the same reason in the other direction.
			if _, err = persistUOBirthJob(t.Context(), turn, job, src, v); !errors.Is(err, ErrInvalid) {
				t.Fatal("740 result persisted in a 700 turn", err)
			}
			_, _ = turn.transaction.rollback(nil)
			if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM user_mutation_command_receipts`); n != 0 {
				t.Fatal(n)
			}
			if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM prepared_media_jobs`); n != 0 {
				t.Fatal(n)
			}
		})
	})

	// A prepared job binds no device: neither uoBirthJobSource nor
	// uoBirthJobResult carries one and the 740 statement has no device column,
	// so there is no job analogue of UO-24's fence-device mismatch case.
	t.Run("wrong-native-or-unsaved-rolls-back", func(t *testing.T) {
		for _, mode := range []string{"worker", "borrowed-worker", "member", "lifetime", "item", "public", "library-key", "library-native", "library-pair-foreign", "library-retired", "kind", "source-revision", "source", "recipe", "target", "state", "phase", "generation", "bytes", "error", "revision", "created", "updated", "version", "native-root", "result-root", "unsaved", "header-prefix", "final-source", "final-authority", "final-header", "final-ledger"} {
			t.Run(mode, func(t *testing.T) {
				w, p, q, src, v := uoBirthJobFixture(t)
				turn, a := uoBirthJobTurn(t, w, p)
				lookup, err := lookupUOBirthJob(t.Context(), turn, a, q)
				if err != nil {
					t.Fatal(err)
				}
				uoBirthJobRow(t, turn, p, src, v)
				switch mode {
				case "worker":
					v.worker[0]++
				case "borrowed-worker":
					v.worker = q.identity.Incarnation
				case "member":
					_, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE prepared_media_jobs SET mp_owner_member_incarnation=?`, strings.Repeat("f", 32))
				case "lifetime":
					_, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE prepared_media_jobs SET mp_owner_profile_lifetime=?`, strings.Repeat("f", 32))
				case "item":
					src.item++
				case "public":
					src.public[0]++
				case "library-key":
					src.key = "foreign-library"
				case "library-native":
					src.library++
				case "library-pair-foreign":
					// The caller changes BOTH the native library and the key to a
					// self-consistent pair, and the item really lives in that
					// library. The key is live but belongs to library 9, so the
					// pair still disagrees with catalog_libraries for the item's
					// library 8. Both old predicates (e.library_id and
					// j.library_id) pass; only the catalog_libraries bridge can
					// refuse the birth.
					moved := [16]byte{4, 128}
					src.item, src.public, src.library, src.key = 2112, moved, 8, "library-two"
					v.itemID = entityid.Encode(moved[:])
					_, err = turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO catalog_libraries(id,library_id,root) VALUES(8,'library-nine','/fixture-two'),(9,'library-two','/fixture-three')`)
					if err == nil {
						_, err = turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(2112,?,8,1)`, moved[:])
					}
					if err == nil {
						_, err = turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) VALUES(2112,?,8,1,'Title','title','t','title','t',0,?,?)`, moved[:], moved[:], moved[:])
					}
					if err == nil {
						_, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE prepared_media_jobs SET item_id=2112,library_id=?`, src.key)
					}
				case "library-retired":
					// The native id and key are the registered pair, but the
					// library row is retired: the bridge's retired=0 arm must
					// close the birth.
					_, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE catalog_libraries SET retired=1 WHERE id=7`)
				case "kind":
					src.kind++
				case "source-revision":
					src.sourceRevision = strings.Repeat("d", 64)
				case "source":
					v.sourceID = "foreign-source"
				case "recipe":
					v.profileID = "foreign-recipe"
				case "target":
					v.targetID = "foreign-target"
				case "state":
					v.state = "succeeded"
				case "phase":
					v.phase = "foreign-phase"
				case "generation":
					v.generation = 1
				case "bytes":
					v.bytes = 1
				case "error":
					v.errorCode = "foreign-error"
				case "revision":
					v.revision = 2
				case "created":
					v.createdMS++
					v.updatedMS++
					v.appliedMS++
				case "updated":
					v.updatedMS++
				case "version":
					v.versionID = "foreign-version"
				case "native-root":
					_, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE prepared_media_jobs SET mp_user_root_id='zyxwvutsrqponmlkjihgfe'`)
				case "result-root":
					v.root = "zyxwvutsrqponmlkjihgfe"
				case "header-prefix":
					delete(turn.steps, uoRootOwnerAdvanceNull)
				}
				if err != nil {
					t.Fatal(err)
				}
				if mode != "unsaved" {
					_, err = persistUOBirthJob(t.Context(), turn, lookup, src, v)
					if mode == "final-source" || mode == "final-authority" || mode == "final-header" || mode == "final-ledger" {
						if err != nil {
							t.Fatal(err)
						}
						sql := `UPDATE prepared_media_jobs SET asset_id='ffffffffffffffffffffffffffffffff'`
						switch mode {
						case "final-authority":
							sql = `UPDATE identity_devices SET retired_ms=1`
						case "final-header":
							sql = `DELETE FROM user_mutation_command_receipts`
						case "final-ledger":
							sql = `UPDATE user_mutation_command_owners SET receipt_rows=0,receipt_variable_bytes=0`
						}
						if _, err = turn.transaction.conn.ExecContext(t.Context(), sql); err != nil {
							t.Fatal(err)
						}
					} else if err == nil {
						t.Fatal("bad native birth accepted")
					}
				}
				if _, err = turn.transaction.commit(); err == nil {
					t.Fatal("partial/stale birth committed")
				}
				if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM prepared_media_jobs`); n != 0 {
					t.Fatal(n)
				}
				if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM user_mutation_command_receipts`); n != 0 {
					t.Fatal(n)
				}
			})
		}
	})

	t.Run("quota-cancel-and-prefix-rollback", func(t *testing.T) {
		// Owner/control CAS: the in-memory owner snapshot is stale by the time the
		// receipt+advance run, so the CAS misses after a nonempty receipt prefix.
		w, p, q, src, v := uoBirthJobFixture(t)
		turn, a := uoBirthJobTurn(t, w, p)
		lookup, err := lookupUOBirthJob(t.Context(), turn, a, q)
		if err != nil {
			t.Fatal(err)
		}
		uoBirthJobRow(t, turn, p, src, v)
		if _, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE user_mutation_command_owners SET receipt_rows=1,receipt_variable_bytes=118`); err != nil {
			t.Fatal(err)
		}
		if _, err = persistUOBirthJob(t.Context(), turn, lookup, src, v); err == nil {
			t.Fatal("stale owner counter CAS accepted")
		}
		if _, err = turn.transaction.commit(); err == nil {
			t.Fatal("nonzero prefix committed")
		}
		if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM user_mutation_command_receipts`); n != 0 {
			t.Fatal("receipt prefix survived rollback", n)
		}
		// Cancellation before effects leaves neither the born row nor a receipt.
		wc008NativeExec(t, w, `UPDATE user_mutation_command_owners SET receipt_rows=0,receipt_variable_bytes=0`)
		turn, a = uoBirthJobTurn(t, w, p)
		lookup, err = lookupUOBirthJob(t.Context(), turn, a, q)
		if err != nil {
			t.Fatal(err)
		}
		uoBirthJobRow(t, turn, p, src, v)
		turn.transaction.cancel()
		if _, err = persistUOBirthJob(t.Context(), turn, lookup, src, v); err == nil {
			t.Fatal("cancelled turn persisted")
		}
		if _, err = turn.transaction.commit(); err == nil {
			t.Fatal("cancelled turn committed")
		}
		if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM prepared_media_jobs`); n != 0 {
			t.Fatal(n)
		}
	})

	t.Run("current-denied-replay-and-digest-conflict", func(t *testing.T) {
		for _, mode := range []string{"digest", "current"} {
			t.Run(mode, func(t *testing.T) {
				w, p, q, src, v := uoBirthJobFixture(t)
				turn, a := uoBirthJobTurn(t, w, p)
				lookup, err := lookupUOBirthJob(t.Context(), turn, a, q)
				if err != nil {
					t.Fatal(err)
				}
				uoBirthJobRow(t, turn, p, src, v)
				if _, err = persistUOBirthJob(t.Context(), turn, lookup, src, v); err != nil {
					t.Fatal(err)
				}
				uoPublicReadingCommit(t, turn)
				turn, a = uoBirthJobTurn(t, w, p)
				want := ErrConflict
				if mode == "digest" {
					q.request[0]++
				} else {
					if _, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE identity_security_fences SET denied=1`); err != nil {
						t.Fatal(err)
					}
					delete(turn.steps, uoRootReceiptPayload)
					want = ErrDisclosureDenied
				}
				out, err := lookupUOBirthJob(t.Context(), turn, a, q)
				if !errors.Is(err, want) || out != nil {
					t.Fatal(out, err)
				}
				if _, err = turn.transaction.commit(); err == nil {
					t.Fatal("denied replay committed")
				}
				if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM user_mutation_command_receipts`); n != 1 {
					t.Fatal(n)
				}
			})
		}
	})

	t.Run("closed-root-path-and-proof", func(t *testing.T) {
		w, p, q, _, _ := uoBirthJobFixture(t)
		turn, a := uoBirthJobTurn(t, w, p)
		forged := uoRootBinding{identity: uoRootSessionIdentity(a)}
		if err := forged.check(t.Context(), turn); !errors.Is(err, ErrUORootCurrentFenceUnavailable) {
			t.Fatal("NORMAL binding fell back to cancellation only", err)
		}
		proof, err := bindUSCurrentUORoot(t.Context(), turn, a)
		if err != nil {
			t.Fatal(err)
		}
		binding, err := bindUORoot(turn, nil, proof)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = lookupUORootCommand(t.Context(), turn, &binding, q); !errors.Is(err, ErrInvalid) {
			t.Fatal("generic birth lookup bypass", err)
		}
		_, _ = turn.transaction.rollback(nil)
		turn, a = uoBirthJobTurn(t, w, p)
		proof, err = bindUSCurrentUORoot(t.Context(), turn, a)
		if err != nil {
			t.Fatal(err)
		}
		proof.identity.account = "foreign"
		if _, err = bindUORoot(turn, nil, proof); err == nil {
			t.Fatal("copied/forged proof identity")
		}
		_, _ = turn.transaction.rollback(nil)
		turn, a = uoBirthJobTurn(t, w, p)
		lookup, err := lookupUOBirthJob(t.Context(), turn, a, q)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = persistUORootOperation(t.Context(), turn, lookup.root, uoOperationResult{status: 1, target: "fake birth", appliedMS: 1}); err == nil {
			t.Fatal("generic scalar relabeled as birth")
		}
		_, _ = turn.transaction.rollback(nil)
		other, _ := uoBirthJobTurn(t, w, p)
		if err = a.recheck(t.Context(), other); err == nil {
			t.Fatal("old-frame current proof reused")
		}
	})

	t.Run("root-changes-before-payload-restore", func(t *testing.T) {
		w, p, q, src, v := uoBirthJobFixture(t)
		turn, a := uoBirthJobTurn(t, w, p)
		lookup, err := lookupUOBirthJob(t.Context(), turn, a, q)
		if err != nil {
			t.Fatal(err)
		}
		uoBirthJobRow(t, turn, p, src, v)
		if _, err = persistUOBirthJob(t.Context(), turn, lookup, src, v); err != nil {
			t.Fatal(err)
		}
		uoPublicReadingCommit(t, turn)
		turn, a = uoBirthJobTurn(t, w, p)
		replay, err := lookupUOBirthJob(t.Context(), turn, a, q)
		if err != nil || replay.root.decision != receiptReplay {
			t.Fatal(replay, err)
		}
		if _, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE user_mutation_roots SET account_id='foreign'`); err != nil {
			t.Fatal(err)
		}
		payload, digest, err := readUORootPayload(t.Context(), turn, replay.root)
		if !errors.Is(err, ErrUORootGone) || len(payload) != 0 || digest != (ResultDigest{}) {
			t.Fatal("stale root released original", len(payload), digest, err)
		}
		if _, err = turn.transaction.commit(); err == nil {
			t.Fatal("stale root committed")
		}
	})

	t.Run("codec-and-plans", func(t *testing.T) {
		_, _, q, src, v := uoBirthJobFixture(t)
		encoded, digest, err := encodeUORootCanonical(q.identity, q.request, v)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeUOBirthJob(q.identity, q.request, q.root, encoded, digest)
		if err != nil || decoded != v {
			t.Fatal(decoded, err)
		}
		for _, payload := range [][]byte{append(append([]byte{}, encoded...), 0), encoded[:len(encoded)-1], encoded[:len(encoded)-6*8-1]} {
			if _, err = decodeUOBirthJob(q.identity, q.request, q.root, payload, digest); !errors.Is(err, ErrResolveRequired) {
				t.Fatal("malformed original class", err)
			}
		}
		q.request[0]++
		if _, err = decodeUOBirthJob(q.identity, q.request, q.root, encoded, digest); err == nil {
			t.Fatal("changed request accepted")
		}
		if !src.valid() {
			t.Fatal("source seam shape")
		}
		for _, d := range uoBirthJobDefinitions() {
			if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
				t.Fatal(issues)
			}
		}
		w, p, _, _, _ := uoBirthJobFixture(t)
		wc008NativePrepare(t, w, uoBirthJobDefinitions())
		turn, a := uoBirthJobTurn(t, w, p)
		q2 := uoRootReference{root: "abcdefghijklmnopqrstuv", kind: 740, identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, request: RequestDigest{3}}
		cancelCtx, cancel := context.WithCancel(t.Context())
		cancel()
		if out, err := lookupUOBirthJob(cancelCtx, turn, a, q2); err == nil || out != nil {
			t.Fatal(out, err)
		}
	})

	t.Run("draining-before-commit", func(t *testing.T) {
		w, p, q, src, v := uoBirthJobFixture(t)
		turn, a := uoBirthJobTurn(t, w, p)
		lookup, err := lookupUOBirthJob(t.Context(), turn, a, q)
		if err != nil {
			t.Fatal(err)
		}
		uoBirthJobRow(t, turn, p, src, v)
		if _, err = persistUOBirthJob(t.Context(), turn, lookup, src, v); err != nil {
			t.Fatal(err)
		}
		if _, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE user_mutation_roots SET state='draining'`); err != nil {
			t.Fatal(err)
		}
		if _, err = turn.transaction.commit(); !errors.Is(err, ErrUORootGone) {
			t.Fatal("Fresh survived drain", err)
		}
		if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM user_mutation_command_receipts`); n != 0 {
			t.Fatal(n)
		}
		if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM prepared_media_jobs`); n != 0 {
			t.Fatal(n)
		}
	})

	t.Run("unknown-commit-never-applied", func(t *testing.T) {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprint(after), func(t *testing.T) {
				w, p, q, src, v := uoBirthJobFixture(t)
				var seq int
				var name, path string
				if err := w.db.QueryRowContext(t.Context(), `PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
					t.Fatal(err)
				}
				if err := w.db.Close(); err != nil {
					t.Fatal(err)
				}
				base, err := sqliteconn.Connector(path, w.config)
				if err != nil {
					t.Fatal(err)
				}
				installFaultPool(w, &faultConnector{Connector: base, afterCommit: after, replyError: true})
				turn, a := uoBirthJobTurn(t, w, p)
				lookup, err := lookupUOBirthJob(t.Context(), turn, a, q)
				if err != nil {
					t.Fatal(err)
				}
				uoBirthJobRow(t, turn, p, src, v)
				if _, err = persistUOBirthJob(t.Context(), turn, lookup, src, v); err != nil {
					t.Fatal(err)
				}
				result, err := turn.transaction.commit()
				if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) || !w.ambiguous {
					t.Fatal("unknown treated as applied", result, err)
				}
				reader, _, err := sqliteconn.Open(t.Context(), path, sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}, sqliteconn.ExistingFile)
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				want := 0
				if after {
					want = 1
				}
				for _, table := range []string{"prepared_media_jobs", "user_mutation_command_receipts"} {
					var n int
					if err = reader.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != want {
						t.Fatal(table, n, want, err)
					}
				}
				if _, err = w.begin(t.Context(), 740, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
					t.Fatal("ambiguous executor reused", err)
				}
			})
		}
	})

	t.Run("root-bridge-purpose-is-closed", func(t *testing.T) {
		w, p, _ := uoPublicReadingFixture(t, false)
		turn, a := uoPublicReadingTurn(t, w, p, 770)
		if proof, err := bindUSCurrentUORoot(t.Context(), turn, a); !errors.Is(err, ErrUORootCurrentFenceUnavailable) || proof != nil {
			t.Fatal("public reading borrowed the prepared-job birth fence", proof, err)
		}
		_, _ = turn.transaction.rollback(nil)

		// The 740 gate accepts only its own purpose. A 700, 710, 750 or 761
		// reference in a turn of that kind is refused before the root bridge or
		// any root lookup, and the shared birth switch gives none of them a
		// state holder it could release.
		for _, kind := range []CommandID{700, 710, 750, 761} {
			t.Run("job-gate-"+fmt.Sprint(kind), func(t *testing.T) {
				w, _, _ := uoPublicReadingFixture(t, false)
				turn := wc008NativeTurn(t, w, kind, uoBirthJobDefinitions())
				q := uoRootReference{root: "abcdefghijklmnopqrstuv", kind: kind, identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, request: RequestDigest{3}}
				out, err := lookupUOBirthJob(t.Context(), turn, nil, q)
				if !errors.Is(err, ErrInvalid) || out != nil {
					t.Fatal("740 gate accepted another birth purpose", out, err)
				}
				root, err := lookupUORootCommand(t.Context(), turn, &uoRootBinding{}, q)
				if !errors.Is(err, ErrInvalid) || errors.Is(err, ErrUORootCurrentFenceUnavailable) || root != (uoRootLookup{}) {
					t.Fatal("birth purpose default", root, err)
				}
				_, _ = turn.transaction.rollback(nil)
			})
		}

		// The 700 gate refuses a 740 reference in a 700 turn.
		t.Run("start-gate-740", func(t *testing.T) {
			w, p, _, _ := uoBirthStartFixture(t)
			q := uoRootReference{root: "abcdefghijklmnopqrstuv", kind: 740, identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, request: RequestDigest{3}}
			turn, a := uoBirthStartTurn(t, w, p)
			out, err := lookupUOBirthStart(t.Context(), turn, a, q)
			if !errors.Is(err, ErrInvalid) || out != nil {
				t.Fatal("700 gate accepted a 740 reference", out, err)
			}
			_, _ = turn.transaction.rollback(nil)
		})
	})

	t.Run("composed-capture-unrecorded", uoBirthJobComposedCapture)
}
