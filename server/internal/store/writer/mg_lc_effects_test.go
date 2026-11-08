package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestMGLCDescriptorRange(t *testing.T) {
	// Prospective 800-range metadata only: the parent aggregates descriptors and
	// the accepting dispatcher/public commands remain owned by MG-22/23.
	ds := mgLCDescriptors()
	if len(ds) != 8 {
		t.Fatal("closed descriptor inventory", len(ds))
	}
	for index, d := range ds {
		if d.ID != MGLCBegin+CommandID(index) || d.Durability != Normal || d.Name == "" {
			t.Fatal("descriptor range", index, d)
		}
	}
}

func TestMGLCNativeAccountingVerifySealAndPublish(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {

		w, b := mgLCFixture(t)
		p := mgLCEntriesFixture(t, w, b, true)
		expectedBytes := int64(len(mgLCCandidateBytes(b.generation, mgLCCandidateFixture())))
		rows, err := w.db.QueryContext(t.Context(), `SELECT `+mgLCEntryColumns+` FROM lc_entries WHERE generation_id=? ORDER BY start_ms`, b.generation)
		if err != nil {
			t.Fatal(err)
		}
		chain := mgLCInitialDigest(b)
		var entries int64
		for rows.Next() {
			var e mgLCEntry
			if err := rows.Scan(&e.generation, &e.occurrence, &e.channel, &e.start, &e.end, &e.item, &e.asset, &e.library, &e.source, &e.title, &e.rule, &e.block, &e.offset, &e.slate); err != nil {
				t.Fatal(err)
			}
			expectedBytes += int64(len(e.canonicalBytes()))
			chain = mgLCNextDigest(chain, e)
			entries++
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(err)
		}
		if p.candidates != 1 || p.entries != entries || entries != 2 || p.bytes != expectedBytes || p.entryDigest != chain || p.verified != 0 || p.verifyDigest != mgLCInitialDigest(b) {
			t.Fatal("actual inserted-row accounting", p, expectedBytes)
		}
		turn := mgLCTurn(t, w)
		if _, err := publishMGLC(t.Context(), turn, b, mgLCNext(b, p), p); !errors.Is(err, ErrConflict) {
			t.Fatal("unverified publication", err)
		}
		if _, err := sealMGLC(t.Context(), turn, b, mgLCNext(b, p), p); !errors.Is(err, ErrConflict) {
			t.Fatal("unverified seal", err)
		}
		_, _ = turn.transaction.rollback(nil)
		p = mgLCVerifiedFixture(t, w, b, p)
		if p.verified != 2 || p.verifyAfter != 31000 || p.verifyEnd != 61000 || p.verifyDigest != p.entryDigest || p.sealed {
			t.Fatal("native prefix", p)
		}
		turn = mgLCTurn(t, w)
		c := mgLCNext(b, p)
		next, err := sealMGLC(t.Context(), turn, b, c, p)
		if err != nil {
			t.Fatal(err)
		}
		s := mgLCTestSave(t, turn, b, c, mgLCSealKind, next)
		mgLCCommit(t, turn)
		p = mgLCTestAck(t, w, b, s, next)
		var head string
		if err := w.db.QueryRowContext(t.Context(), `SELECT active_generation FROM lc_channels WHERE id=?`, b.channel).Scan(&head); err != nil || head != b.base {
			t.Fatal("old head changed before complete publish", head, err)
		}
		turn = mgLCTurn(t, w)
		c = mgLCNext(b, p)
		published, err := publishMGLC(t.Context(), turn, b, c, p)
		if err != nil {
			t.Fatal(err)
		}
		saved := mgLCTestSave(t, turn, b, c, mgLCPublishKind, published)
		mgLCCommit(t, turn)
		if !published.sealed || published.status != "published" || published.lease != "" || published.leaseUntil != 0 || published.published <= 0 {
			t.Fatal(published)
		}
		var old, pins int
		if err := w.db.QueryRowContext(t.Context(), `SELECT active_generation,(SELECT count(*) FROM lc_entries WHERE generation_id='lc-old'),(SELECT count(*) FROM lc_playback_refs WHERE generation_id='lc-old') FROM lc_channels WHERE id=?`, b.channel).Scan(&head, &old, &pins); err != nil || head != b.generation || old != 1 || pins != 1 {
			t.Fatal(head, old, pins, err)
		}
		// Current source/head/lease changes cannot replace the ORIGINAL saved result.
		if _, err := w.db.ExecContext(t.Context(), `UPDATE lc_channels SET revision=8,enabled=0,active_generation='lc-old' WHERE id='lc-channel'`); err != nil {
			t.Fatal(err)
		}
		turn = mgLCTurn(t, w)
		decision, replayed, err := lookupMGLCReceipt(t.Context(), turn, b, c, mgLCPublishKind)
		if err != nil || decision != receiptReplay || replayed != saved {
			t.Fatal("original replay", decision, replayed, err)
		}
		_, _ = turn.transaction.rollback(nil)
		// ACK too has no current head/catalogue/lease precondition.
		mgLCTestAck(t, w, b, saved, published)
		turn = mgLCTurn(t, w)
		decision, _, err = lookupMGLCReceipt(t.Context(), turn, b, c, mgLCPublishKind)
		if err != nil || decision != receiptRetired {
			t.Fatal(decision, err)
		}

	})
}
func TestMGLCVerificationRejectsStoredDamage(t *testing.T) {
	for name, q := range map[string]string{
		"negative-extra": `INSERT INTO lc_entries VALUES('lc-new','negative','lc-channel',-100,-1,0,'','','','Invalid','rule','default',0,'no-eligible-media')`,
		"missing-slot":   `DELETE FROM lc_entries WHERE generation_id='lc-new' AND occurrence_id='first'`,
		"changed-body":   `UPDATE lc_entries SET title='Different same-size' WHERE generation_id='lc-new' AND occurrence_id='first'`,
		"timeline-gap":   `UPDATE lc_entries SET start_ms=1001 WHERE generation_id='lc-new' AND occurrence_id='first'`,
		"extra-tail":     `INSERT INTO lc_entries VALUES('lc-new','extra','lc-channel',61000,62000,0,'','','','Unavailable program','rule','default',0,'no-eligible-media')`,
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {

				w, b := mgLCFixture(t)
				p := mgLCEntriesFixture(t, w, b, true)
				if _, err := w.db.ExecContext(t.Context(), q); err != nil {
					t.Fatal(err)
				}
				rejected := false
				for n := 0; n < 3; n++ {
					turn := mgLCTurn(t, w)
					c := mgLCNext(b, p)
					next, err := verifyMGLCEntries(t.Context(), turn, b, c, p)
					if err != nil {
						if !errors.Is(err, ErrConflict) {
							t.Fatal(err)
						}
						_, _ = turn.transaction.rollback(nil)
						rejected = true
						break
					}
					s := mgLCTestSave(t, turn, b, c, mgLCVerifyKind, next)
					mgLCCommit(t, turn)
					p = mgLCTestAck(t, w, b, s, next)
				}
				if !rejected {
					t.Fatal("damaged native schedule verified")
				}
				turn := mgLCTurn(t, w)
				if _, err := sealMGLC(t.Context(), turn, b, mgLCNext(b, p), p); !errors.Is(err, ErrConflict) {
					t.Fatal("damaged schedule sealed", err)
				}

			})
		})
	}
}
func TestMGLCPublicationFences(t *testing.T) {
	for name, q := range map[string]string{
		"config":             `UPDATE lc_channels SET revision=8 WHERE id='lc-channel'`,
		"same-revision-base": `UPDATE lc_channels SET active_generation='' WHERE id='lc-channel'`,
		"lease-token":        `UPDATE lc_generations SET lease_token='successor' WHERE id='lc-new'`,
		"lease-deadline":     `UPDATE lc_generations SET lease_until_ms=lease_until_ms+1 WHERE id='lc-new'`,
		"catalogue":          `UPDATE libraries SET data_revision=12 WHERE id='lc-lib'`,
		"library-retired":    `UPDATE catalog_libraries SET retired=1 WHERE id=101`,
		"history":            `UPDATE lc_generations SET play_history_fence=1 WHERE id='lc-new'`,
		"origin":             `UPDATE libraries SET playback_origin_incarnation=zeroblob(16) WHERE id='lc-lib'`,
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {

				w, b := mgLCFixture(t)
				p := mgLCSealedFixture(t, w, b, true)
				if _, err := w.db.ExecContext(t.Context(), q); err != nil {
					t.Fatal(err)
				}
				turn := mgLCTurn(t, w)
				if got, err := publishMGLC(t.Context(), turn, b, mgLCNext(b, p), p); !errors.Is(err, ErrConflict) || got != (mgLCProgress{}) {
					t.Fatal(got, err)
				}

			})
		})
	}
}
func TestMGLCSequenceAckAndReceiptIdentity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {

		w, b := mgLCFixture(t)
		turn := mgLCTurn(t, w)
		birth := mgLCNext(b, mgLCProgress{})
		p, err := beginMGLC(t.Context(), turn, b, birth)
		if err != nil {
			t.Fatal(err)
		}
		s := mgLCTestSave(t, turn, b, birth, mgLCBeginKind, p)
		mgLCCommit(t, turn)
		turn = mgLCTurn(t, w)
		c := mgLCNext(b, p)
		if _, err := finishMGLCCandidates(t.Context(), turn, b, c, p); !errors.Is(err, ErrConflict) {
			t.Fatal("unACKed predecessor accepted", err)
		}
		wrong := s.digest
		wrong[0] ^= 1
		if _, err := acknowledgeMGLC(t.Context(), turn, b, birth, mgLCBeginKind, wrong); !errors.Is(err, ErrConflict) {
			t.Fatal("wrong result ACK", err)
		}
		for _, mutate := range []func(*mgLCCommand){func(c *mgLCCommand) { c.request[0] ^= 1 }, func(c *mgLCCommand) { c.identity.OperationID[0] ^= 1 }, func(c *mgLCCommand) { c.identity.Sequence++ }} {
			bad := birth
			mutate(&bad)
			if _, _, err := lookupMGLCReceipt(t.Context(), turn, b, bad, mgLCBeginKind); !errors.Is(err, ErrConflict) {
				t.Fatal("changed duplicate accepted", err)
			}
		}
		_, _ = turn.transaction.rollback(nil)
		p = mgLCTestAck(t, w, b, s, p)
		turn = mgLCTurn(t, w)
		bad := mgLCNext(b, p)
		bad.identity.Sequence++
		if _, err := finishMGLCCandidates(t.Context(), turn, b, bad, p); !errors.Is(err, ErrConflict) {
			t.Fatal("sequence gap accepted", err)
		}
		c = mgLCNext(b, p)
		c.identity.OperationID = birth.identity.OperationID
		if _, err := finishMGLCCandidates(t.Context(), turn, b, c, p); !errors.Is(err, ErrConflict) {
			t.Fatal("operation reused", err)
		}

	})
}
func TestMGLCPartialEffectsRequireWholeRollback(t *testing.T) {
	t.Run("publish", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {

			w, b := mgLCFixture(t)
			p := mgLCSealedFixture(t, w, b, true)
			turn := mgLCTurn(t, w)
			// The real native head UPDATE succeeds; the owner/sequence UPDATE then fails.
			step := turn.steps[mgLCAdvance]
			step.work.Rows = math.MaxUint64
			turn.steps[mgLCAdvance] = step
			if got, err := publishMGLC(t.Context(), turn, b, mgLCNext(b, p), p); !errors.Is(err, ErrQuantum) || got != (mgLCProgress{}) {
				t.Fatal(got, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			var head, status string
			if err := w.db.QueryRowContext(t.Context(), `SELECT active_generation,(SELECT status FROM lc_generations WHERE id='lc-new') FROM lc_channels WHERE id='lc-channel'`).Scan(&head, &status); err != nil || head != b.base || status != "pending" {
				t.Fatal(head, status, err)
			}

		})
	})
	t.Run("receipt", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {

			w, b := mgLCFixture(t)
			p := mgLCBeginFixture(t, w, b)
			turn := mgLCTurn(t, w)
			c := mgLCNext(b, p)
			next, err := stageMGLCCandidates(t.Context(), turn, b, c, p, []mgLCCandidate{mgLCCandidateFixture()}, "id:101", 1, 0)
			if err != nil {
				t.Fatal(err)
			}
			// The candidate and owner advance precede an actual typed receipt CHECK
			// failure. This must roll back the whole frame, never just the receipt.
			if got, err := saveMGLCReceipt(t.Context(), turn, b, c, mgLCPublishKind, next); err == nil || got != (mgLCSaved{}) {
				t.Fatal(got, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			var count, last, bytes int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT candidate_count,last_command_sequence,staged_bytes FROM lc_generations WHERE id='lc-new'`).Scan(&count, &last, &bytes); err != nil || count != 0 || last != 1 || bytes != 0 {
				t.Fatal(count, last, bytes, err)
			}

		})
	})
}
func TestMGLCBoundsCancellationSlateAndExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {

		w, b := mgLCFixture(t)
		window := b
		window.end = window.start + 169*60*60*1000
		if !window.valid() {
			t.Fatal("DST civil window")
		}
		tooMany := b
		tooMany.libraries = append(append([]mgLCLibraryWitness{}, b.libraries...), make([]mgLCLibraryWitness, 8)...)
		if tooMany.valid() {
			t.Fatal("unqualified component limit not explicit")
		}
		turn := mgLCTurn(t, w)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := beginMGLC(ctx, turn, b, mgLCNext(b, mgLCProgress{})); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		_, _ = turn.transaction.rollback(nil)
		p := mgLCBeginFixture(t, w, b)
		turn = mgLCTurn(t, w)
		candidate := mgLCCandidateFixture()
		candidate.title = strings.Repeat("x", mgLCBytes)
		if _, err := stageMGLCCandidates(t.Context(), turn, b, mgLCNext(b, p), p, []mgLCCandidate{candidate}, "next", 1, 0); !errors.Is(err, ErrCap) {
			t.Fatal(err)
		}
		_, _ = turn.transaction.rollback(nil)
		// Expiry is a logical fence only; it neither releases the old reference nor
		// permits publication. Synthetic clock jump deliberately fences this writer.
		turn = mgLCTurn(t, w)
		w.now = func() time.Time { return time.UnixMilli(b.leaseUntil) }
		if _, err := finishMGLCCandidates(t.Context(), turn, b, mgLCNext(b, p), p); !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
		if _, err := turn.transaction.rollback(nil); !errors.Is(err, ErrOverrun) {
			t.Fatal(err)
		}

	})
}

func TestMGLCBoundedVerificationPages(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {

		w, b := mgLCFixture(t)
		b.end = 21000
		p := mgLCStartFixture(t, w, b, false)
		for index := 0; index < 20; {
			var slots []mgLCSlot
			for len(slots) < 8 && index < 20 {
				start := int64(1000 + 1000*index)
				slots = append(slots, mgLCSlot{occurrence: fmt.Sprintf("slot-%02d", index), rule: "rule", block: "default", start: start, end: start + 1000})
				index++
			}
			turn := mgLCTurn(t, w)
			c := mgLCNext(b, p)
			next, err := stageMGLCEntries(t.Context(), turn, b, c, p, slots)
			if err != nil {
				t.Fatal(err)
			}
			s := mgLCTestSave(t, turn, b, c, mgLCEntriesKind, next)
			mgLCCommit(t, turn)
			p = mgLCTestAck(t, w, b, s, next)
		}
		for _, want := range []int64{8, 16, 20, 20} {
			turn := mgLCTurn(t, w)
			c := mgLCNext(b, p)
			next, err := verifyMGLCEntries(t.Context(), turn, b, c, p)
			if err != nil || next.verified != want {
				t.Fatal(next, err)
			}
			s := mgLCTestSave(t, turn, b, c, mgLCVerifyKind, next)
			mgLCCommit(t, turn)
			p = mgLCTestAck(t, w, b, s, next)
		}
		if p.phase != mgLCVerifiedPhase || p.verifyDigest != p.entryDigest || p.bytes != 20*int64(len((mgLCEntry{generation: b.generation, occurrence: "slot-00", channel: b.channel, start: 1000, end: 2000, title: "Unavailable program", rule: "rule", block: "default", slate: "no-eligible-media"}).canonicalBytes())) {
			t.Fatal(p)
		}

	})
}
func TestMGLCVerificationBytePrefix(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {

		w, b := mgLCFixture(t)
		p := mgLCBeginFixture(t, w, b)
		candidate := mgLCCandidateFixture()
		candidate.title = strings.Repeat("x", 140000)
		turn := mgLCTurn(t, w)
		c := mgLCNext(b, p)
		next, err := stageMGLCCandidates(t.Context(), turn, b, c, p, []mgLCCandidate{candidate}, "id:101", 1, 0)
		if err != nil {
			t.Fatal(err)
		}
		s := mgLCTestSave(t, turn, b, c, mgLCCandidatesKind, next)
		mgLCCommit(t, turn)
		p = mgLCTestAck(t, w, b, s, next)
		turn = mgLCTurn(t, w)
		c = mgLCNext(b, p)
		next, err = finishMGLCCandidates(t.Context(), turn, b, c, p)
		if err != nil {
			t.Fatal(err)
		}
		s = mgLCTestSave(t, turn, b, c, mgLCCandidatesKind, next)
		mgLCCommit(t, turn)
		p = mgLCTestAck(t, w, b, s, next)
		// Two individually legal media rows exceed one 256KiB staging quantum.
		slots := []mgLCSlot{{"wide-1", "rule", "default", 101, 1000, 31000}, {"wide-2", "rule", "default", 101, 31000, 61000}}
		turn = mgLCTurn(t, w)
		c = mgLCNext(b, p)
		if got, err := stageMGLCEntries(t.Context(), turn, b, c, p, slots); !errors.Is(err, ErrCap) || got != (mgLCProgress{}) {
			t.Fatal(got, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		for _, slot := range slots {
			turn = mgLCTurn(t, w)
			c = mgLCNext(b, p)
			next, err = stageMGLCEntries(t.Context(), turn, b, c, p, []mgLCSlot{slot})
			if err != nil {
				t.Fatal(err)
			}
			s = mgLCTestSave(t, turn, b, c, mgLCEntriesKind, next)
			mgLCCommit(t, turn)
			p = mgLCTestAck(t, w, b, s, next)
		}
		for _, want := range []int64{1, 2, 2} {
			turn = mgLCTurn(t, w)
			c = mgLCNext(b, p)
			next, err = verifyMGLCEntries(t.Context(), turn, b, c, p)
			if err != nil || next.verified != want {
				t.Fatal(next, err)
			}
			s = mgLCTestSave(t, turn, b, c, mgLCVerifyKind, next)
			mgLCCommit(t, turn)
			p = mgLCTestAck(t, w, b, s, next)
		}
		if p.phase != mgLCVerifiedPhase || p.verifyDigest != p.entryDigest {
			t.Fatal(p)
		}

	})
}

// Independent native point controls use the same occurrence ID in two
// generations and distinct occurrences in one generation. Neither half of the
// full PK may be dropped or replaced by the schedule's start-time range.
func TestMGLCEntryLengthPointsUseFullPK(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {

		w, b := mgLCFixture(t)
		mgLCEntriesFixture(t, w, b, true)
		candidate := mgLCCandidateFixture()
		media := mgLCEntry{generation: b.generation, occurrence: "first", channel: b.channel, start: 1000, end: 31000, item: candidate.item, asset: candidate.asset, library: candidate.library, source: candidate.source, title: candidate.title, rule: "rule", block: "default"}
		slate := mgLCEntry{generation: b.generation, occurrence: "second", channel: b.channel, start: 31000, end: 61000, title: "Unavailable program", rule: "rule", block: "default", slate: "no-eligible-media"}
		old := mgLCEntry{generation: b.base, occurrence: "first", channel: b.channel, start: 61000, end: 62000, title: strings.Repeat("old", 100), rule: "rule", block: "default", slate: "no-eligible-media"}
		if _, err := w.db.ExecContext(t.Context(), `INSERT INTO lc_entries(`+mgLCEntryColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, old.values()...); err != nil {
			t.Fatal(err)
		}
		turn := mgLCTurn(t, w)
		for _, entry := range []mgLCEntry{media, slate, old} {
			row, err := mgLCRow(t.Context(), turn, mgLCEntryLengthsPoint, entry.generation, entry.occurrence)
			if err != nil {
				t.Fatal(err)
			}
			var lengths [10]int64
			if err := row.Scan(&lengths[0], &lengths[1], &lengths[2], &lengths[3], &lengths[4], &lengths[5], &lengths[6], &lengths[7], &lengths[8], &lengths[9]); err != nil {
				t.Fatal(err)
			}
			// Compare native columns to independent canonical row encoding,
			// without using the effect's checked addition helper.
			size := int64(113)
			for _, n := range lengths {
				if n < 0 {
					t.Fatal("negative native length", n)
				}
				size += n
			}
			if size != int64(len(entry.canonicalBytes())) {
				t.Fatal("wrong full-PK length", entry.generation, entry.occurrence, size, err)
			}
		}
		for _, key := range [][2]string{{b.generation, "missing"}, {"missing", "first"}} {
			row, err := mgLCRow(t.Context(), turn, mgLCEntryLengthsPoint, key[0], key[1])
			if err != nil {
				t.Fatal(err)
			}
			var lengths [10]int64
			if err := row.Scan(&lengths[0], &lengths[1], &lengths[2], &lengths[3], &lengths[4], &lengths[5], &lengths[6], &lengths[7], &lengths[8], &lengths[9]); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("partial key accepted", key, lengths, err)
			}
		}

	})
}
