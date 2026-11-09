package writer

import (
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// Two-channel directory used by the reorder replay/disclosure regressions.
func mgLCControlTwoChannelDirectory(t *testing.T, w *executor) mgLCControlWitness {
	t.Helper()
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO lc_channels(id,revision,config_json,enabled,position,name,state) VALUES('lc-second',3,'{"id":"lc-second"}',1,1,'Second','healthy')`); err != nil {
		t.Fatal(err)
	}
	return mgLCControlOrderWitness(t, w)
}

func mgLCControlTwoChannelOrder(witness mgLCControlWitness) []mgLCControlOrder {
	return []mgLCControlOrder{
		{channel: witness.directory[1], config: mgLCControlConfigFor("lc-second", "Second", 1, 0)},
		{channel: witness.directory[0], config: mgLCControlConfigFor("lc-channel", "Channel", 1, 1)},
	}
}

// P1-1: a lost-response retry of a reorder must return its saved result, and a
// fresh reader must be able to decode the committed body.
func TestMGLCControlReorderReplayReturnsSaved(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, _, proof := mgLCControlFixture(t)
		witness := mgLCControlTwoChannelDirectory(t, w)
		order := mgLCControlTwoChannelOrder(witness)
		in := mgLCControlInput{order: order}
		saved := mgLCControlApply(t, w, proof, MGReorderLCChannels, witness, in, 30)
		var body string
		reader := mgGuideFreshReader(t, mgGuideDatabasePath(t, w))
		if err := reader.QueryRowContext(t.Context(), `SELECT result_json FROM lc_receipts WHERE request_id=?`, mgLCControlRequestKey(mgLCControlCommandFor(mgLCControlReorderKind, witness, in, 30))).Scan(&body); err != nil {
			t.Fatal(err)
		}
		raw, err := hex.DecodeString(body)
		if err != nil {
			t.Fatal(err)
		}
		command := mgLCControlCommandFor(mgLCControlReorderKind, witness, in, 30)
		decoded, err := decodeMGLCControlResult(raw, command, mgLCControlReorderKind)
		if err != nil || !reflect.DeepEqual(decoded, saved) {
			t.Fatal("lost COMMIT reorder decode", decoded, saved, err)
		}
		turn, budget := mgLCControlAuthorityTurn(t, w, MGReorderLCChannels)
		before := mgLCControlChanges(t, turn)
		replay, decision, err := mgLCControlDispatch(t.Context(), turn, proof, t.Context(), budget, MGReorderLCChannels, witness, command, in)
		if err != nil || decision != receiptReplay || !reflect.DeepEqual(replay, saved) {
			t.Fatal("reorder replay", replay, decision, err)
		}
		if after := mgLCControlChanges(t, turn); after != before {
			t.Fatal("reorder replay made effects", before, after)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
	})
}

// P1-2: a reorder original spans every member; retirement of any member since
// the reorder must deny disclosure of the saved snapshot.
func TestMGLCControlRetiredDirectoryMemberDeniesReorderReplay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, _, proof := mgLCControlFixture(t)
		witness := mgLCControlTwoChannelDirectory(t, w)
		order := mgLCControlTwoChannelOrder(witness)
		in := mgLCControlInput{order: order}
		_ = mgLCControlApply(t, w, proof, MGReorderLCChannels, witness, in, 31)
		retire := mgLCControlWitness{issuer: mgLCControlIssuer(), channel: mgLCControlReadWitness(t, w, "lc-second")}
		_ = mgLCControlApply(t, w, proof, MGRetireLCChannel, retire, mgLCControlInput{}, 32)
		turn, budget := mgLCControlAuthorityTurn(t, w, MGReorderLCChannels)
		before := mgLCControlChanges(t, turn)
		got, decision, err := mgLCControlDispatch(t.Context(), turn, proof, t.Context(), budget, MGReorderLCChannels, witness,
			mgLCControlCommandFor(mgLCControlReorderKind, witness, in, 31), in)
		if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(got, mgLCControlSaved{}) {
			t.Fatal("retired directory member replay", got, decision, err)
		}
		if after := mgLCControlChanges(t, turn); after != before {
			t.Fatal("retired directory member replay wrote", before, after)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
	})
}

// P1-2: a missing or unprovable channel row is never replay evidence, even
// when it fails closed inside the native scan as ErrConflict.
func TestMGLCControlUnprovableChannelRowNeverReplays(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cases := []struct {
			name   string
			mutate func(*testing.T, *executor)
		}{
			{"missing", func(t *testing.T, w *executor) {
				if _, err := w.db.ExecContext(t.Context(), `DELETE FROM lc_channels WHERE id='control-new'`); err != nil {
					t.Fatal(err)
				}
			}},
			{"invalid-row", func(t *testing.T, w *executor) {
				if _, err := w.db.ExecContext(t.Context(), `UPDATE lc_channels SET config_json=? WHERE id='control-new'`, strings.Repeat("x", mgLCControlConfigBytes+1)); err != nil {
					t.Fatal(err)
				}
			}},
		}
		for _, tc := range cases {
			w, _, proof := mgLCControlFixture(t)
			birth := mgLCControlWitness{issuer: mgLCControlIssuer(), channel: mgLCControlChannel{id: "control-new"}}
			config := mgLCControlConfigFor("control-new", "New", 1, 0)
			in := mgLCControlInput{config: &config}
			_ = mgLCControlApply(t, w, proof, MGSaveLCConfiguration, birth, in, 33)
			tc.mutate(t, w)
			turn, budget := mgLCControlAuthorityTurn(t, w, MGSaveLCConfiguration)
			before := mgLCControlChanges(t, turn)
			got, decision, err := mgLCControlDispatch(t.Context(), turn, proof, t.Context(), budget, MGSaveLCConfiguration, birth,
				mgLCControlCommandFor(mgLCControlSaveKind, birth, in, 33), in)
			if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(got, mgLCControlSaved{}) {
				t.Fatal(tc.name, "unprovable channel row replay", got, decision, err)
			}
			if after := mgLCControlChanges(t, turn); after != before {
				t.Fatal(tc.name, "unprovable channel row replay wrote", before, after)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
		}
	})
}

// P1-3: no control frame may disclose a saved original or perform an effect
// without current US owner authority. The old `issuer == witness.issuer` check
// only compared two caller-supplied values.
func TestMGLCControlAuthorityRefusesBeforeDisclosureAndEffect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			forge  func(*mgActorProof)
			mutate string
		}{
			{"forged-proof", func(p *mgActorProof) { p.native.tokenExpiry = p.native.tokenExpiry.Add(time.Hour) }, ""},
			{"stale-member", nil, `UPDATE direct_memberships SET incarnation='cccccccccccccccccccccccccccccccc'`},
			{"revoked-family", nil, `UPDATE authorization_session_families SET revoked=1`},
			{"revoked-token", nil, `UPDATE authorization_family_tokens SET retired=1`},
			{"viewer-not-owner", func(p *mgActorProof) { p.kind = mgActorViewer }, ""},
		} {
			// Fresh: the denial precedes the effect and the receipt.
			w, b, proof := mgLCControlFixture(t)
			witness := mgLCControlSaveWitness(t, w, b.channel)
			config := mgLCControlConfigFor(b.channel, "Authority", 1, 0)
			in := mgLCControlInput{config: &config}
			if tc.forge != nil {
				tc.forge(proof)
			}
			if tc.mutate != "" {
				if _, err := w.db.ExecContext(t.Context(), tc.mutate); err != nil {
					t.Fatal(err)
				}
			}
			turn, budget := mgLCControlAuthorityTurn(t, w, MGSaveLCConfiguration)
			before := mgLCControlChanges(t, turn)
			receipts := mgLCControlTurnReceiptCount(t, turn)
			got, _, err := mgLCControlDispatch(t.Context(), turn, proof, t.Context(), budget, MGSaveLCConfiguration, witness,
				mgLCControlCommandFor(mgLCControlSaveKind, witness, in, 40), in)
			if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(got, mgLCControlSaved{}) {
				t.Fatal(tc.name, "fresh authority", got, err)
			}
			if after := mgLCControlChanges(t, turn); after != before {
				t.Fatal(tc.name, "fresh authority wrote", before, after)
			}
			if mgLCControlTurnReceiptCount(t, turn) != receipts {
				t.Fatal(tc.name, "fresh authority saved a receipt")
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}

			// Replay: a valid original exists, but stale authority may not disclose it.
			w, b, proof = mgLCControlFixture(t)
			witness = mgLCControlSaveWitness(t, w, b.channel)
			in = mgLCControlInput{config: &config}
			_ = mgLCControlApply(t, w, proof, MGSaveLCConfiguration, witness, in, 41)
			if tc.forge != nil {
				tc.forge(proof)
			}
			if tc.mutate != "" {
				if _, err := w.db.ExecContext(t.Context(), tc.mutate); err != nil {
					t.Fatal(err)
				}
			}
			turn, budget = mgLCControlAuthorityTurn(t, w, MGSaveLCConfiguration)
			before = mgLCControlChanges(t, turn)
			got, _, err = mgLCControlDispatch(t.Context(), turn, proof, t.Context(), budget, MGSaveLCConfiguration, witness,
				mgLCControlCommandFor(mgLCControlSaveKind, witness, in, 41), in)
			if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(got, mgLCControlSaved{}) {
				t.Fatal(tc.name, "replay authority disclosed", got, err)
			}
			if after := mgLCControlChanges(t, turn); after != before {
				t.Fatal(tc.name, "replay authority wrote", before, after)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
		}
	})
}

// The control turn owns the writer's only connection, so receipt presence must
// be observed on that same turn rather than the outer pool.
func mgLCControlTurnReceiptCount(t *testing.T, turn *commandTurn) int64 {
	t.Helper()
	var n int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM lc_receipts`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Reorder is positional: the display name must equal the existing channel.
func TestMGLCControlReorderRefusesRenamedChannel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, _, proof := mgLCControlFixture(t)
		witness := mgLCControlTwoChannelDirectory(t, w)
		order := mgLCControlTwoChannelOrder(witness)
		order[0].config.name = "Renamed"
		if err := mgLCControlRefuse(t, w, proof, MGReorderLCChannels, witness, mgLCControlInput{order: order}, 34); !errors.Is(err, ErrInvalid) {
			t.Fatal("renamed reorder", err)
		}
		var first int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT position FROM lc_channels WHERE id='lc-second'`).Scan(&first); err != nil || first != 1 {
			t.Fatal("renamed reorder moved positions", first, err)
		}
	})
}

// The encoded reorder result is capped before the first native write, so a
// directory that only overflows after encoding performs no partial reorder.
func TestMGLCControlReorderPreflightsEncodedResultCap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, _, proof := mgLCControlFixture(t)
		config := strings.Repeat("c", 4050)
		if _, err := w.db.ExecContext(t.Context(), `UPDATE lc_channels SET config_json=? WHERE id='lc-channel'`, config); err != nil {
			t.Fatal(err)
		}
		for n := 1; n < 64; n++ {
			name := strings.Repeat("n", 18) + "a" + string(rune('a'+n/26)) + string(rune('a'+n%26))
			name = name[:20]
			id := "cap-" + string(rune('a'+n/26)) + string(rune('a'+n%26))
			if _, err := w.db.ExecContext(t.Context(), `INSERT INTO lc_channels(id,revision,config_json,enabled,position,name,state) VALUES(?,1,?,1,?,?,'ready')`, id, config, n, name); err != nil {
				t.Fatal(err)
			}
		}
		witness := mgLCControlOrderWitness(t, w)
		if len(witness.directory) != 64 {
			t.Fatal("directory size", len(witness.directory))
		}
		order := make([]mgLCControlOrder, 0, len(witness.directory))
		for n, ch := range witness.directory {
			order = append(order, mgLCControlOrder{channel: ch, config: mgLCControlConfig{canonical: config, name: ch.name, enabled: 1, position: int64(n)}})
		}
		in := mgLCControlInput{order: order}
		kind, _ := mgLCControlKindFor(MGReorderLCChannels)
		planned, ok := mgLCControlPlannedResult(kind, witness, in)
		if !ok || len(mgLCControlResultBytes(mgLCControlSaved{kind: kind, result: planned})) <= mgLCBytes {
			t.Fatal("fixture does not exceed the encoded cap", ok)
		}
		refusal := &Refusal{}
		if err := mgLCControlRefuse(t, w, proof, MGReorderLCChannels, witness, in, 35); !errors.As(err, &refusal) || refusal.Code != RefusalCapacity {
			t.Fatal("encoded result cap", err)
		}
		var revision, position int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT revision,position FROM lc_channels WHERE id='lc-channel'`).Scan(&revision, &position); err != nil || revision != 7 || position != 0 {
			t.Fatal("cap refusal wrote a reorder", revision, position, err)
		}
	})
}

// A pending alignment RETURNING mismatch leaves the whole reorder to rollback.
func TestMGLCControlReorderPendingAlignMismatchRollsBack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, _, proof := mgLCControlFixture(t)
		witness := mgLCControlTwoChannelDirectory(t, w)
		mgLCControlInsertPending(t, w, "lc-pending-second", "lc-second", 3, "", "lease-second")
		// BEFORE UPDATE: the align statement's RETURNING sees the perturbed lease.
		if _, err := w.db.ExecContext(t.Context(), `CREATE TRIGGER control_align_mismatch BEFORE UPDATE ON lc_generations BEGIN UPDATE lc_generations SET lease_token='wrong' WHERE id=new.id; END`); err != nil {
			t.Fatal(err)
		}
		order := mgLCControlTwoChannelOrder(witness)
		in := mgLCControlInput{order: order}
		turn, budget := mgLCControlAuthorityTurn(t, w, MGReorderLCChannels)
		got, _, err := mgLCControlDispatch(t.Context(), turn, proof, t.Context(), budget, MGReorderLCChannels, witness,
			mgLCControlCommandFor(mgLCControlReorderKind, witness, in, 36), in)
		if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(got, mgLCControlSaved{}) {
			t.Fatal("reorder align mismatch", got, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		var status, lease string
		if err := w.db.QueryRowContext(t.Context(), `SELECT status,lease_token FROM lc_generations WHERE id='lc-pending-second'`).Scan(&status, &lease); err != nil || status != "pending" || lease != "lease-second" {
			t.Fatal("align mismatch escaped rollback", status, lease, err)
		}
		var position int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT position FROM lc_channels WHERE id='lc-second'`).Scan(&position); err != nil || position != 1 {
			t.Fatal("align mismatch moved positions", position, err)
		}
	})
}

// A pending build at another revision refuses save and retire too, not just
// regeneration, and never retargets the pending job.
func TestMGLCControlPendingAtDifferentRevisionRefusesRetireAndSave(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, b, proof := mgLCControlFixture(t)
		mgLCControlInsertPending(t, w, "lc-pending", b.channel, 8, b.base, "lease-pending")
		witness := mgLCControlSaveWitness(t, w, b.channel)
		config := mgLCControlConfigFor(b.channel, "Save", 1, 0)
		if err := mgLCControlRefuse(t, w, proof, MGSaveLCConfiguration, witness, mgLCControlInput{config: &config}, 37); !errors.Is(err, ErrConflict) {
			t.Fatal("save with pending at another revision", err)
		}
		if err := mgLCControlRefuse(t, w, proof, MGRetireLCChannel, witness, mgLCControlInput{}, 38); !errors.Is(err, ErrConflict) {
			t.Fatal("retire with pending at another revision", err)
		}
		var status string
		var revision, configRevision int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT status,config_revision FROM lc_generations WHERE id='lc-pending'`).Scan(&status, &configRevision); err != nil || status != "pending" || configRevision != 8 {
			t.Fatal("pending was retargeted", status, configRevision, err)
		}
		if err := w.db.QueryRowContext(t.Context(), `SELECT revision FROM lc_channels WHERE id=?`, b.channel).Scan(&revision); err != nil || revision != 7 {
			t.Fatal("refused control moved the channel", revision, err)
		}
	})
}

// A retired channel id is never a birth target again.
func TestMGLCControlRetiredChannelIDCannotBeReborn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, b, proof := mgLCControlFixture(t)
		if _, err := w.db.ExecContext(t.Context(), `DELETE FROM lc_playback_refs WHERE playback_id='active-player'`); err != nil {
			t.Fatal(err)
		}
		witness := mgLCControlSaveWitness(t, w, b.channel)
		_ = mgLCControlApply(t, w, proof, MGRetireLCChannel, witness, mgLCControlInput{}, 39)
		birth := mgLCControlWitness{issuer: mgLCControlIssuer(), channel: mgLCControlChannel{id: b.channel}}
		config := mgLCControlConfigFor(b.channel, "Reborn", 1, 0)
		if err := mgLCControlRefuse(t, w, proof, MGSaveLCConfiguration, birth, mgLCControlInput{config: &config}, 42); !errors.Is(err, ErrConflict) {
			t.Fatal("retired channel reborn", err)
		}
		var rows, removed int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT count(*),max(removed) FROM lc_channels WHERE id=?`, b.channel).Scan(&rows, &removed); err != nil || rows != 1 || removed != 1 {
			t.Fatal("retired channel rebirth wrote", rows, removed, err)
		}
	})
}
