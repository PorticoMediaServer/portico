package writer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func uoBirthSessionCount(t *testing.T, w *executor, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := w.db.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// MP-09 seam fixture: MP has already created the logical session and its
// independent physical occurrence. UO only freezes the complete first response
// and the exact born identities; it never selects a source or presentation.
func uoBirthSessionFixture(t *testing.T) (*executor, *usOwnerAuthorityProof, uoRootReference, uoBirthSessionSource, playbackv1.SessionView) {
	t.Helper()
	w, p, reading := uoPublicReadingFixture(t, true)
	q := uoRootReference{root: reading.root, kind: 710, identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, request: RequestDigest{3}}
	wc008NativeExec(t, w, `INSERT INTO user_mutation_command_owners(root_id,command_incarnation) VALUES(?,?)`, q.root, q.identity.Incarnation[:])
	wc008NativeExec(t, w, `INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'uo-session-library','/uo-session')`)
	wc008NativeExec(t, w, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)`)
	wc008NativeExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(9001,?,7,1)`, make([]byte, 16))
	wc008NativeExec(t, w, `INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES(9001,'uo-session-asset',7,'uo-session.mkv',100,1,'matroska','h264','aac',1920,1080,1)`)
	now := w.now().UnixMilli()
	source := uoBirthSessionSource{
		root: q.root, session: "ps_uo_session_original", physical: "media_uo_original",
		device: p.witness.device, startKey: strings.Repeat("a", 64), startBody: strings.Repeat("b", 64),
		logical: [16]byte{9}, physicalIncarnation: [16]byte{10}, physicalGeneration: 1,
		createdMS: now, deadlineMS: now + 60000, tokenExpiresMS: p.tokenExpiry.UnixMilli(), appliedMS: now,
	}
	leaseMS := now + 120000
	view := playbackv1.SessionView{
		Control:   &playbackv1.SessionControl{Revision: "4", State: "playing"},
		ID:        source.session,
		Revision:  "1",
		Kind:      "vod",
		Role:      "local",
		State:     "playing",
		ItemID:    "original-item",
		VersionID: "original-version",
		Queue:     &playbackv1.QueueRef{QueueID: "original-queue", EntryID: "original-entry"},
		Lease:     playbackv1.Lease{ExpiresAt: time.UnixMilli(leaseMS).UTC().Format(time.RFC3339Nano), ReportEveryMs: 10000},
		Presentation: playbackv1.Presentation{
			Generation:      1,
			Mode:            "direct",
			URL:             "http://original.invalid/presentation",
			Engine:          "native",
			StartPositionMs: 0,
			Decision:        playbackv1.Decisions{Video: &playbackv1.Decision{Action: "direct", Reasons: []string{}}},
			Quality:         &playbackv1.Quality{Mode: playbackv1.QualityOriginal},
			Container:       "matroska",
		},
		ObservedPlayback: &playback.ObservedPlayback{Video: &playback.ObservedVideo{Width: 1920, Height: 1080, FrameRateHz: 23.976}},
		PlaybackInfo:     &playback.PlaybackInfo{Route: "original", Network: "home", Basis: "committed_delivery_plan", Losses: []playback.PlaybackLoss{}},
	}
	return w, p, q, source, view
}

func uoBirthSessionTurn(t *testing.T, w *executor, p *usOwnerAuthorityProof) (*commandTurn, *usCurrentSessionAuthorization) {
	t.Helper()
	defs := append(usCurrentSessionDefinitions(), uoRootDefinitions()...)
	defs = append(defs, uoBirthSessionDefinitions()...)
	turn := wc008NativeTurn(t, w, 710, defs)
	a, err := authorizeUSCurrentSession(t.Context(), turn, p, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return turn, a
}

// Rows are inserted on the SAME writer connection as the open turn: the pool
// owns one writer, so a direct w.db call here would wait on this frame.
func uoBirthSessionRows(t *testing.T, turn *commandTurn, p *usOwnerAuthorityProof, source uoBirthSessionSource, view playbackv1.SessionView) {
	t.Helper()
	i := p.witness.issuer
	leaseMS, ok := uoBirthSessionLeaseMS(view)
	if !ok {
		t.Fatal("fixture lease")
	}
	query := `INSERT INTO playback_v1_sessions(id,device_id,account_id,profile_id,authority,start_key,start_digest,kind,role,state,item_id,version_id,queue_id,entry_id,request,revision,generation,media_session_id,media_generation,presentation,lease_expires_ms,created_ms,updated_ms,mp_command_incarnation,mp_user_root_id,mp_owner_member_incarnation,mp_owner_profile_lifetime) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,1,?,?,?,?,?,?,?,?,?,?)`
	_, err := turn.transaction.conn.ExecContext(t.Context(), query, source.session, source.device, i.account, i.profile, "local", source.startKey, source.startBody, "vod", "local", view.State, int64(9001), view.VersionID, view.Queue.QueueID, view.Queue.EntryID, `{}`, source.physical, source.physicalGeneration, `{}`, leaseMS, source.createdMS, source.createdMS, source.logical[:], source.root, i.memberIncarnation, i.profileLifetime)
	if err != nil {
		t.Fatal(err)
	}
	query = `INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration,mp_command_incarnation,mp_user_root_id,mp_owner_member_incarnation,mp_owner_profile_lifetime) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	if _, err = turn.transaction.conn.ExecContext(t.Context(), query, source.physical, "hash", i.account, i.profile, int64(9001), "uo-session-asset", source.physicalGeneration, "playing", "grant", "opaque", "2099-01-01T00:00:00Z", "request", 1, source.physicalIncarnation[:], source.root, i.memberIncarnation, i.profileLifetime); err != nil {
		t.Fatal(err)
	}
}

func uoBirthSessionSave(t *testing.T, turn *commandTurn, lookup *uoBirthSessionLookup, source uoBirthSessionSource, view playbackv1.SessionView) ResultDigest {
	t.Helper()
	digest, err := persistUOBirthSession(t.Context(), turn, lookup, source, view)
	if err != nil || digest == (ResultDigest{}) {
		t.Fatal(digest, err)
	}
	return digest
}

func TestUOBirthSession(t *testing.T) {
	t.Run("originalFullWireAfterMutationAndDeletion", func(t *testing.T) {
		w, p, q, source, view := uoBirthSessionFixture(t)
		turn, a := uoBirthSessionTurn(t, w, p)
		lookup, err := lookupUOBirthSession(t.Context(), turn, a, q)
		if err != nil || lookup.root.decision != receiptFresh {
			t.Fatal(lookup, err)
		}
		uoBirthSessionRows(t, turn, p, source, view)
		uoBirthSessionSave(t, turn, lookup, source, view)
		want := lookup.original
		if !bytes.Contains(want.wire, []byte("http://original.invalid/presentation")) || !bytes.Contains(want.wire, []byte(`"deliveredBitrateKbps"`)) && !bytes.Contains(want.wire, []byte("observedPlayback")) {
			t.Fatal("sealed original dropped presentation/observation fields", string(want.wire))
		}
		uoPublicReadingCommit(t, turn)
		for _, query := range []struct {
			text string
			args []any
		}{
			{`SELECT mp_command_last_sequence+mp_command_acknowledged_sequence FROM playback_v1_sessions WHERE id=?`, []any{source.session}},
			{`SELECT mp_command_last_sequence+mp_command_acknowledged_sequence FROM playback_sessions WHERE id=?`, []any{source.physical}},
			{`SELECT count(*) FROM mp_session_command_receipts`, nil},
			{`SELECT count(*) FROM mp_physical_command_receipts`, nil},
			{`SELECT receipt_variable_bytes-(SELECT sum(variable_bytes) FROM user_mutation_command_receipts) FROM user_mutation_command_owners`, nil},
		} {
			if n := uoBirthSessionCount(t, w, query.text, query.args...); n != 0 {
				t.Fatal(query.text, n)
			}
		}
		// Both rows drift and are then deleted; replay must still return the exact
		// first wire because it never reconstructs the current view.
		wc008NativeExec(t, w, `UPDATE playback_v1_sessions SET state='ended',revision=9,device_id='moved',presentation='{"mutated":true}',lease_expires_ms=1`)
		wc008NativeExec(t, w, `UPDATE playback_sessions SET state='ended',generation=7`)
		wc008NativeExec(t, w, `DELETE FROM playback_v1_sessions`)
		wc008NativeExec(t, w, `DELETE FROM playback_sessions`)
		turn, a = uoBirthSessionTurn(t, w, p)
		replay, err := lookupUOBirthSession(t.Context(), turn, a, q)
		if err != nil || replay.root.decision != receiptReplay || !replay.original.equal(want) || !bytes.Equal(replay.original.wire, want.wire) {
			t.Fatal(replay, err)
		}
		decoded, err := decodeUOBirthSessionView(replay.original.wire)
		if err != nil || decoded.ID != view.ID || decoded.Presentation.URL != view.Presentation.URL || decoded.Queue == nil || decoded.Queue.EntryID != view.Queue.EntryID {
			t.Fatal("restored wire lost complete view fields", decoded, err)
		}
		uoPublicReadingCommit(t, turn)
		if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM playback_v1_sessions`); n != 0 {
			t.Fatal("replay reconstructed the session", n)
		}
	})

	t.Run("distinctLogicalAndPhysicalLifetimes", func(t *testing.T) {
		for _, mode := range []string{"physical-generation", "physical-incarnation", "logical-link", "logical-counter", "logical-root", "device", "lease", "state", "logical-account", "physical-account", "logical-profile", "physical-profile", "logical-member", "physical-member", "logical-lifetime", "physical-lifetime", "borrowed-logical", "borrowed-physical"} {
			t.Run(mode, func(t *testing.T) {
				w, p, q, source, view := uoBirthSessionFixture(t)
				turn, a := uoBirthSessionTurn(t, w, p)
				lookup, err := lookupUOBirthSession(t.Context(), turn, a, q)
				if err != nil {
					t.Fatal(err)
				}
				uoBirthSessionRows(t, turn, p, source, view)
				exec := func(query string, args ...any) {
					t.Helper()
					if _, err := turn.transaction.conn.ExecContext(t.Context(), query, args...); err != nil {
						t.Fatal(err)
					}
				}
				switch mode {
				case "physical-generation":
					exec(`UPDATE playback_sessions SET generation=2`)
				case "physical-incarnation":
					exec(`UPDATE playback_sessions SET mp_command_incarnation=zeroblob(16)`)
				case "logical-link":
					exec(`UPDATE playback_v1_sessions SET media_generation=2`)
				case "logical-counter":
					exec(`UPDATE playback_v1_sessions SET mp_command_last_sequence=1`)
				case "logical-root":
					exec(`UPDATE playback_v1_sessions SET mp_user_root_id='zyxwvutsrqponmlkjihgfe'`)
				case "device":
					exec(`UPDATE playback_v1_sessions SET device_id='foreign'`)
				case "lease":
					exec(`UPDATE playback_v1_sessions SET lease_expires_ms=1`)
				case "state":
					exec(`UPDATE playback_v1_sessions SET state='paused'`)
				case "logical-account":
					exec(`UPDATE playback_v1_sessions SET account_id='foreign'`)
				case "physical-account":
					exec(`UPDATE playback_sessions SET account_id='foreign'`)
				case "logical-profile":
					exec(`UPDATE playback_v1_sessions SET profile_id='foreign'`)
				case "physical-profile":
					exec(`UPDATE playback_sessions SET profile_id='foreign'`)
				case "logical-member":
					exec(`UPDATE playback_v1_sessions SET mp_owner_member_incarnation=?`, strings.Repeat("f", 32))
				case "physical-member":
					exec(`UPDATE playback_sessions SET mp_owner_member_incarnation=?`, strings.Repeat("f", 32))
				case "logical-lifetime":
					exec(`UPDATE playback_v1_sessions SET mp_owner_profile_lifetime=?`, strings.Repeat("f", 32))
				case "physical-lifetime":
					exec(`UPDATE playback_sessions SET mp_owner_profile_lifetime=?`, strings.Repeat("f", 32))
				case "borrowed-logical":
					source.logical = q.identity.Incarnation
				case "borrowed-physical":
					source.physicalIncarnation = q.identity.Incarnation
				}
				if _, err := persistUOBirthSession(t.Context(), turn, lookup, source, view); err == nil {
					t.Fatal("mismatched lifetime bound")
				}
				if _, err := turn.transaction.commit(); err == nil {
					t.Fatal("mismatched birth committed")
				}
				if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM user_mutation_command_receipts`); n != 0 {
					t.Fatal(n)
				}
			})
		}
	})

	t.Run("fenceDeviceAndIdentity", func(t *testing.T) {
		for _, mode := range []string{"fence-device", "fence-identity"} {
			t.Run(mode, func(t *testing.T) {
				w, p, q, source, view := uoBirthSessionFixture(t)
				if mode == "fence-device" {
					// Rows, the sealed original and the source all name this device;
					// only the frozen US witness (fence) device stays the fixture one.
					source.device = "fence-foreign"
				}
				turn, a := uoBirthSessionTurn(t, w, p)
				lookup, err := lookupUOBirthSession(t.Context(), turn, a, q)
				if err != nil {
					t.Fatal(err)
				}
				uoBirthSessionRows(t, turn, p, source, view)
				if mode == "fence-identity" {
					lookup.root.binding.identity.account = "foreign"
				}
				if mode == "fence-device" {
					// The exact native statements still MATCH the foreign device, so
					// a refusal below cannot come from the SQL branch; it proves the
					// explicit source-vs-witness device comparison is the gate.
					leaseMS, ok := uoBirthSessionLeaseMS(view)
					if !ok {
						t.Fatal("fixture lease")
					}
					i := p.witness.issuer
					if n, err := wc008DraftInteger(t.Context(), turn, uoBirthSessionLogical, source.session, source.device, i.account, i.profile, source.startKey, source.startBody, view.State, source.physical, source.physicalGeneration, leaseMS, source.createdMS, source.logical[:], source.root, i.memberIncarnation, i.profileLifetime); err != nil || n != 1 {
						t.Fatal("logical native branch", n, err)
					}
					if n, err := wc008DraftInteger(t.Context(), turn, uoBirthSessionPhysical, source.physical, source.physicalGeneration, source.physicalIncarnation[:], source.root, i.account, i.profile, i.memberIncarnation, i.profileLifetime); err != nil || n != 1 {
						t.Fatal("physical native branch", n, err)
					}
				}
				if _, err = persistUOBirthSession(t.Context(), turn, lookup, source, view); err == nil {
					t.Fatal("fence mismatch accepted")
				}
				if _, err = turn.transaction.commit(); err == nil {
					t.Fatal("fence mismatch committed")
				}
				if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM user_mutation_command_receipts`); n != 0 {
					t.Fatal(n)
				}
			})
		}
	})

	t.Run("retiredAcknowledgedFloor", func(t *testing.T) {
		for _, floor := range []struct {
			name               string
			last, acknowledged int64
		}{{"at-floor", 1, 1}, {"below-floor", 2, 2}} {
			t.Run(floor.name, func(t *testing.T) {
				w, p, q, source, view := uoBirthSessionFixture(t)
				turn, a := uoBirthSessionTurn(t, w, p)
				lookup, err := lookupUOBirthSession(t.Context(), turn, a, q)
				if err != nil {
					t.Fatal(err)
				}
				uoBirthSessionRows(t, turn, p, source, view)
				uoBirthSessionSave(t, turn, lookup, source, view)
				uoPublicReadingCommit(t, turn)

				// Sequence at or below the acknowledged floor is retired custody,
				// not a replay: no header/payload is read and no original is restored.
				wc008NativeExec(t, w, `UPDATE user_mutation_command_owners SET last_command_sequence=?,acknowledged_command_sequence=?`, floor.last, floor.acknowledged)
				turn, a = uoBirthSessionTurn(t, w, p)
				retired, err := lookupUOBirthSession(t.Context(), turn, a, q)
				if err != nil || retired.root.decision != receiptRetired || !retired.original.equal(uoBirthSessionResult{}) {
					t.Fatal(retired, err)
				}
				uoPublicReadingCommit(t, turn)

				// Nothing may be finalized over a retired original.
				turn, a = uoBirthSessionTurn(t, w, p)
				retired, err = lookupUOBirthSession(t.Context(), turn, a, q)
				if err != nil || retired.root.decision != receiptRetired {
					t.Fatal(retired, err)
				}
				if _, err = persistUOBirthSession(t.Context(), turn, retired, source, view); !errors.Is(err, ErrInvalid) {
					t.Fatal("retired original persisted", err)
				}
				_, _ = turn.transaction.rollback(nil)
				if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM user_mutation_command_receipts`); n != 1 {
					t.Fatal("retired persist wrote a receipt", n)
				}
				if n := uoPublicReadingCount(t, w, `SELECT receipt_rows FROM user_mutation_command_owners`); n != 1 {
					t.Fatal("retired persist moved the owner counters", n)
				}
			})
		}
	})

	t.Run("crossKindPersistRefused", func(t *testing.T) {
		t.Run("startInSessionTurn", func(t *testing.T) {
			w, p, q, source, view := uoBirthSessionFixture(t)
			turn, a := uoBirthSessionTurn(t, w, p)
			lookup, err := lookupUOBirthSession(t.Context(), turn, a, q)
			if err != nil {
				t.Fatal(err)
			}
			uoBirthSessionRows(t, turn, p, source, view)
			now := w.now().UnixMilli()
			start := &uoBirthStartLookup{turn: turn, root: lookup.root}
			startResult := uoBirthStartResult{root: q.root, preparation: "cross-kind-preparation", device: p.witness.device, startKey: strings.Repeat("a", 64), startBody: strings.Repeat("b", 64), item: "cross-item", asset: "cross-asset", worker: [16]byte{9}, createdMS: now, deadlineMS: now + 60000, tokenExpiresMS: p.tokenExpiry.UnixMilli(), revision: 1, retryMS: 500, appliedMS: now}
			// A complete 700 original presented to the 710 finalizer: the closed
			// state holder is per purpose, so only a 710 result may be persisted.
			if _, err = persistUOBirthStart(t.Context(), turn, start, startResult); !errors.Is(err, ErrInvalid) {
				t.Fatal("700 result persisted in a 710 turn", err)
			}
			_, _ = turn.transaction.rollback(nil)
			if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM user_mutation_command_receipts`); n != 0 {
				t.Fatal(n)
			}
		})

		t.Run("sessionInStartTurn", func(t *testing.T) {
			w, p, q, source, view := uoBirthSessionFixture(t)
			startReference := q
			startReference.kind = 700
			defs := append(usCurrentSessionDefinitions(), uoRootDefinitions()...)
			defs = append(defs, uoBirthStartDefinitions()...)
			turn := wc008NativeTurn(t, w, 700, defs)
			a, err := authorizeUSCurrentSession(t.Context(), turn, p, t.Context())
			if err != nil {
				t.Fatal(err)
			}
			start, err := lookupUOBirthStart(t.Context(), turn, a, startReference)
			if err != nil || start.root.decision != receiptFresh {
				t.Fatal(start, err)
			}
			session := &uoBirthSessionLookup{turn: turn, root: start.root}
			if _, err = persistUOBirthSession(t.Context(), turn, session, source, view); !errors.Is(err, ErrInvalid) {
				t.Fatal("710 result persisted in a 700 turn", err)
			}
			_, _ = turn.transaction.rollback(nil)
			if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM user_mutation_command_receipts`); n != 0 {
				t.Fatal(n)
			}
		})
	})

	t.Run("duplicateAndConflict", func(t *testing.T) {
		w, p, q, source, view := uoBirthSessionFixture(t)
		turn, a := uoBirthSessionTurn(t, w, p)
		lookup, err := lookupUOBirthSession(t.Context(), turn, a, q)
		if err != nil {
			t.Fatal(err)
		}
		uoBirthSessionRows(t, turn, p, source, view)
		uoBirthSessionSave(t, turn, lookup, source, view)
		uoPublicReadingCommit(t, turn)

		for _, mode := range []string{"operation", "request", "sequence"} {
			t.Run(mode, func(t *testing.T) {
				turn, a := uoBirthSessionTurn(t, w, p)
				next := q
				if mode == "operation" {
					next.identity.OperationID[0]++
				} else if mode == "request" {
					next.request[0]++
				} else {
					next.identity.Sequence++
					next.identity.OperationID[0]++
				}
				out, err := lookupUOBirthSession(t.Context(), turn, a, next)
				if mode == "sequence" {
					// A successor has no acknowledged predecessor, so missing retry
					// custody is Resolve-required rather than a synthesized birth.
					if !errors.Is(err, ErrResolveRequired) || out != nil {
						t.Fatal("successor synthesized retry custody", out, err)
					}
				} else if !errors.Is(err, ErrConflict) || out != nil {
					t.Fatal(out, err)
				}
				if _, err = turn.transaction.commit(); err == nil {
					t.Fatal("conflict committed")
				}
				if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM user_mutation_command_receipts`); n != 1 {
					t.Fatal(n)
				}
			})
		}
	})

	t.Run("canceledAndPrefixRollback", func(t *testing.T) {
		w, p, q, _, _ := uoBirthSessionFixture(t)
		turn, a := uoBirthSessionTurn(t, w, p)
		cancelCtx, cancel := context.WithCancel(t.Context())
		cancel()
		if out, err := lookupUOBirthSession(cancelCtx, turn, a, q); err == nil || out != nil {
			t.Fatal(out, err)
		}
		_, _ = turn.transaction.rollback(nil)

		for _, mode := range []string{"header-prefix", "unsaved", "final-source", "final-authority"} {
			t.Run(mode, func(t *testing.T) {
				w, p, q, source, view := uoBirthSessionFixture(t)
				turn, a := uoBirthSessionTurn(t, w, p)
				lookup, err := lookupUOBirthSession(t.Context(), turn, a, q)
				if err != nil {
					t.Fatal(err)
				}
				uoBirthSessionRows(t, turn, p, source, view)
				switch mode {
				case "header-prefix":
					delete(turn.steps, uoRootOwnerAdvanceNull)
					if _, err = persistUOBirthSession(t.Context(), turn, lookup, source, view); err == nil {
						t.Fatal("prefix failure ignored")
					}
				default:
					if _, err = persistUOBirthSession(t.Context(), turn, lookup, source, view); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "unsaved" {
					lookup.saved = false
				}
				if mode == "final-source" || mode == "final-authority" {
					query := `UPDATE playback_v1_sessions SET start_digest='ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff'`
					if mode == "final-authority" {
						query = `UPDATE identity_devices SET retired_ms=1`
					}
					if _, err = turn.transaction.conn.ExecContext(t.Context(), query); err != nil {
						t.Fatal(err)
					}
				}
				// A nonempty receipt/owner/sealed prefix must roll back completely.
				if _, err = turn.transaction.commit(); err == nil {
					t.Fatal("partial/stale birth committed")
				}
				if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM user_mutation_command_receipts`); n != 0 {
					t.Fatal(n)
				}
				if n := uoPublicReadingCount(t, w, `SELECT receipt_rows+receipt_variable_bytes FROM user_mutation_command_owners`); n != 0 {
					t.Fatal(n)
				}
			})
		}
	})

	t.Run("protectedResultDenial", func(t *testing.T) {
		w, p, q, source, view := uoBirthSessionFixture(t)
		turn, a := uoBirthSessionTurn(t, w, p)
		lookup, err := lookupUOBirthSession(t.Context(), turn, a, q)
		if err != nil {
			t.Fatal(err)
		}
		uoBirthSessionRows(t, turn, p, source, view)
		uoBirthSessionSave(t, turn, lookup, source, view)
		uoPublicReadingCommit(t, turn)

		turn, a = uoBirthSessionTurn(t, w, p)
		if _, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE identity_security_fences SET denied=1`); err != nil {
			t.Fatal(err)
		}
		// Remove the payload step: only the independent pre-lookup current fence
		// may produce the expected denial.
		delete(turn.steps, uoRootReceiptPayload)
		out, err := lookupUOBirthSession(t.Context(), turn, a, q)
		if !errors.Is(err, ErrDisclosureDenied) || out != nil || errors.Is(err, sql.ErrNoRows) {
			t.Fatal(out, err)
		}
		if _, err = turn.transaction.commit(); err == nil {
			t.Fatal("denied replay committed")
		}
		if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM user_mutation_command_receipts`); n != 1 {
			t.Fatal(n)
		}
	})

	t.Run("unknownCommit", func(t *testing.T) {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprint(after), func(t *testing.T) {
				w, p, q, source, view := uoBirthSessionFixture(t)
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
				turn, a := uoBirthSessionTurn(t, w, p)
				lookup, err := lookupUOBirthSession(t.Context(), turn, a, q)
				if err != nil {
					t.Fatal(err)
				}
				uoBirthSessionRows(t, turn, p, source, view)
				uoBirthSessionSave(t, turn, lookup, source, view)
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
				for _, table := range []string{"playback_v1_sessions", "playback_sessions", "user_mutation_command_receipts"} {
					var n int
					if err = reader.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != want {
						t.Fatal(table, n, want, err)
					}
				}
				if _, err = w.begin(t.Context(), 710, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
					t.Fatal("ambiguous executor reused", err)
				}
			})
		}
	})

	t.Run("negativeControls", func(t *testing.T) {
		t.Run("eofAndMalformedWire", func(t *testing.T) {
			_, _, q, source, view := uoBirthSessionFixture(t)
			wire, err := sealUOBirthSessionView(view)
			if err != nil {
				t.Fatal(err)
			}
			v := uoBirthSessionResult{source: source, wire: wire}
			encoded, digest, err := encodeUORootCanonical(q.identity, q.request, v)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := decodeUOBirthSession(q.identity, q.request, q.root, encoded, digest)
			if err != nil || !decoded.equal(v) {
				t.Fatal(decoded, err)
			}
			for _, payload := range [][]byte{append(append([]byte{}, encoded...), 0), encoded[:len(encoded)-1], encoded[:len(encoded)-8*5-1]} {
				if _, err = decodeUOBirthSession(q.identity, q.request, q.root, payload, digest); !errors.Is(err, ErrResolveRequired) {
					t.Fatal("EOF class", err)
				}
			}
			changed := append([]byte{}, wire...)
			changed[len(changed)/2] = 0
			if _, err = decodeUOBirthSessionView(changed); !errors.Is(err, ErrResolveRequired) {
				t.Fatal("tampered wire", err)
			}
			valueChanged := append([]byte{}, wire...)
			valueChanged[len(valueChanged)/2] ^= 1
			if _, err = decodeUOBirthSession(q.identity, q.request, q.root, valueChanged, digest); !errors.Is(err, ErrResolveRequired) {
				t.Fatal("value-tampered wire passed its digest", err)
			}
			unknown := append(append([]byte{}, wire[:len(wire)-1]...), []byte(`,"unknown":1}`)...)
			if _, err = decodeUOBirthSessionView(unknown); !errors.Is(err, ErrResolveRequired) {
				t.Fatal("unknown wire field", err)
			}
			q.request[0]++
			if _, err = decodeUOBirthSession(q.identity, q.request, q.root, encoded, digest); !errors.Is(err, ErrResolveRequired) {
				t.Fatal("changed request accepted", err)
			}
		})

		t.Run("wrongRootAndDigest", func(t *testing.T) {
			w, p, q, source, view := uoBirthSessionFixture(t)
			wire, _ := sealUOBirthSessionView(view)
			v := uoBirthSessionResult{source: source, wire: wire}
			encoded, digest, err := encodeUORootCanonical(q.identity, q.request, v)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = decodeUOBirthSession(q.identity, q.request, "zyxwvutsrqponmlkjihgfe", encoded, digest); !errors.Is(err, ErrResolveRequired) {
				t.Fatal("wrong root accepted", err)
			}
			turn, a := uoBirthSessionTurn(t, w, p)
			lookup, err := lookupUOBirthSession(t.Context(), turn, a, q)
			if err != nil {
				t.Fatal(err)
			}
			uoBirthSessionRows(t, turn, p, source, view)
			bad := source
			bad.root = "zyxwvutsrqponmlkjihgfe"
			if _, err = persistUOBirthSession(t.Context(), turn, lookup, bad, view); !errors.Is(err, ErrConflict) {
				t.Fatal("foreign result root", err)
			}
			_, _ = turn.transaction.rollback(nil)

			turn, a = uoBirthSessionTurn(t, w, p)
			lookup, err = lookupUOBirthSession(t.Context(), turn, a, q)
			if err != nil {
				t.Fatal(err)
			}
			uoBirthSessionRows(t, turn, p, source, view)
			uoBirthSessionSave(t, turn, lookup, source, view)
			uoPublicReadingCommit(t, turn)
			wc008NativeExec(t, w, `UPDATE user_mutation_command_receipts SET result_digest=zeroblob(32)`)
			turn, a = uoBirthSessionTurn(t, w, p)
			if out, err := lookupUOBirthSession(t.Context(), turn, a, q); !errors.Is(err, ErrResolveRequired) || out != nil {
				t.Fatal("changed digest", out, err)
			}
		})

		t.Run("goneRootClass", func(t *testing.T) {
			for _, mode := range []string{"owner-missing", "root-missing", "foreign-root", "draining", "changed-incarnation"} {
				t.Run(mode, func(t *testing.T) {
					w, p, q, _, _ := uoBirthSessionFixture(t)
					switch mode {
					case "owner-missing":
						wc008NativeExec(t, w, `DELETE FROM user_mutation_command_owners`)
					case "root-missing":
						wc008NativeExec(t, w, `DELETE FROM user_mutation_command_owners`)
						wc008NativeExec(t, w, `DELETE FROM user_mutation_roots`)
					case "foreign-root":
						wc008NativeExec(t, w, `UPDATE user_mutation_roots SET account_id='foreign'`)
					case "draining":
						wc008NativeExec(t, w, `UPDATE user_mutation_roots SET state='draining'`)
					case "changed-incarnation":
						q.identity.Incarnation[0]++
					}
					turn, a := uoBirthSessionTurn(t, w, p)
					out, err := lookupUOBirthSession(t.Context(), turn, a, q)
					if err == nil || out != nil {
						t.Fatal(out, err)
					}
					if mode == "root-missing" || mode == "foreign-root" || mode == "draining" {
						if !errors.Is(err, ErrUORootGone) || errors.Is(err, sql.ErrNoRows) {
							t.Fatal("root failure class", err)
						}
					}
					if mode == "owner-missing" && (!errors.Is(err, ErrResolveRequired) || !errors.Is(err, ErrOwnerUnavailable) || errors.Is(err, sql.ErrNoRows)) {
						t.Fatal("missing owner escaped typed mapping", err)
					}
					_, _ = turn.transaction.rollback(nil)
				})
			}
		})

		t.Run("drainingBetweenPersistAndCommit", func(t *testing.T) {
			w, p, q, source, view := uoBirthSessionFixture(t)
			turn, a := uoBirthSessionTurn(t, w, p)
			lookup, err := lookupUOBirthSession(t.Context(), turn, a, q)
			if err != nil {
				t.Fatal(err)
			}
			uoBirthSessionRows(t, turn, p, source, view)
			uoBirthSessionSave(t, turn, lookup, source, view)
			if _, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE user_mutation_roots SET state='draining'`); err != nil {
				t.Fatal(err)
			}
			if _, err = turn.transaction.commit(); !errors.Is(err, ErrUORootGone) {
				t.Fatal("Fresh survived drain", err)
			}
			if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM user_mutation_command_receipts`); n != 0 {
				t.Fatal(n)
			}
		})

		t.Run("otherPurposeProofRefused", func(t *testing.T) {
			w, p, _, _, _ := uoBirthSessionFixture(t)
			for _, kind := range []CommandID{600, 700, 770, 771} {
				t.Run(fmt.Sprint(kind), func(t *testing.T) {
					turn, a := uoPublicReadingTurn(t, w, p, kind)
					proof, err := bindUSCurrentUORoot(t.Context(), turn, a)
					if kind == 600 || kind == 700 {
						if err != nil || proof == nil {
							t.Fatal(proof, err)
						}
					} else if !errors.Is(err, ErrUORootCurrentFenceUnavailable) || proof != nil {
						t.Fatal("other purpose minted the birth proof", proof, err)
					}
					_, _ = turn.transaction.rollback(nil)
				})
			}
			turn, a := uoBirthSessionTurn(t, w, p)
			if out, err := lookupUOBirthSession(t.Context(), turn, a, uoRootReference{root: "abcdefghijklmnopqrstuv", kind: 700, identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, request: RequestDigest{3}}); !errors.Is(err, ErrInvalid) || out != nil {
				t.Fatal("710 lookup accepted 700", out, err)
			}
			_, _ = turn.transaction.rollback(nil)
		})

		t.Run("otherBirthPurposesStayClosed", func(t *testing.T) {
			w, _, _ := uoPublicReadingFixture(t, false)
			for _, kind := range []CommandID{740, 750, 761} {
				t.Run(fmt.Sprint(kind), func(t *testing.T) {
					// The shared closed switch lists these as birth purposes but gives
					// each no state holder; adding 710 must not open them. The turn's
					// own kind is required to reach that switch. A non-nil but
					// unusable binding keeps the assertion honest: without the
					// default arm the later binding.check would answer
					// ErrUORootCurrentFenceUnavailable instead of ErrInvalid.
					defs := append(usCurrentSessionDefinitions(), uoRootDefinitions()...)
					turn := wc008NativeTurn(t, w, kind, defs)
					q := uoRootReference{root: "abcdefghijklmnopqrstuv", kind: kind, identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, request: RequestDigest{3}}
					out, err := lookupUORootCommand(t.Context(), turn, &uoRootBinding{}, q)
					if !errors.Is(err, ErrInvalid) || errors.Is(err, ErrUORootCurrentFenceUnavailable) || out != (uoRootLookup{}) {
						t.Fatal("birth purpose default", out, err)
					}
					_, _ = turn.transaction.rollback(nil)
				})
			}
		})
	})

	t.Run("codecAndPlans", func(t *testing.T) {
		w, p, q, source, view := uoBirthSessionFixture(t)
		for _, d := range uoBirthSessionDefinitions() {
			if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
				t.Fatal(d.ID, issues)
			}
		}
		wc008NativePrepare(t, w, uoBirthSessionDefinitions())
		wire, err := sealUOBirthSessionView(view)
		if err != nil {
			t.Fatal(err)
		}
		v := uoBirthSessionResult{source: source, wire: wire}
		encoded, digest, err := encodeUORootCanonical(q.identity, q.request, v)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeUOBirthSession(q.identity, q.request, q.root, encoded, digest)
		if err != nil || !decoded.equal(v) {
			t.Fatal(decoded, err)
		}
		turn, a := uoBirthSessionTurn(t, w, p)
		lookup, err := lookupUOBirthSession(t.Context(), turn, a, q)
		if err != nil || lookup.root.decision != receiptFresh {
			t.Fatal(lookup, err)
		}
		if _, err = persistUORootOperation(t.Context(), turn, lookup.root, uoOperationResult{status: 1, target: "fake session", appliedMS: 1}); err == nil {
			t.Fatal("generic scalar relabeled as a session birth")
		}
		_, _ = turn.transaction.rollback(nil)
	})
}

// The composed capture is deliberately unrecorded: it proves the component
// SQL shape on this fixture and claims no installed/golden admission.
func TestUOBirthSessionComposedCapture(t *testing.T) {
	w, p, q, source, view := uoBirthSessionFixture(t)
	leaseMS, ok := uoBirthSessionLeaseMS(view)
	if !ok {
		t.Fatal("fixture lease")
	}
	defs := append(usCurrentSessionDefinitions(), uoRootDefinitions()...)
	born := uoBirthSessionDefinitions()
	i := p.witness.issuer
	born[0].Cohorts = []stmt.Cohort{{Name: "exact_logical_session_birth", Bindings: []any{source.session, source.device, i.account, i.profile, source.startKey, source.startBody, view.State, source.physical, source.physicalGeneration, leaseMS, source.createdMS, source.logical[:], q.root, i.memberIncarnation, i.profileLifetime}}}
	born[1].Cohorts = []stmt.Cohort{{Name: "exact_physical_session_birth", Bindings: []any{source.physical, source.physicalGeneration, source.physicalIncarnation[:], q.root, i.account, i.profile, i.memberIncarnation, i.profileLifetime}}}
	defs = append(defs, born...)
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("uo_birth_session_test.go")
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = cachedCapture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: w.config, SHA256: hex.EncodeToString(hash[:]), Statistics: plan.Unanalyzed, Implementations: "UO-24 composed canonical-F current-session/root/logical+physical session birth component; unrecorded, no command or resource qualification"}, policy)
	if err != nil {
		t.Fatal(err)
	}
}
