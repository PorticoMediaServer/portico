package writer

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func usRestrictionsReceiptCommit(t *testing.T, turn *commandTurn) {
	t.Helper()
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	physical, err := turn.transaction.commit()
	if err != nil || physical.state != committed || turn.transaction.settings.Synchronous != 2 {
		t.Fatal("FULL physical commit failed", physical, err)
	}
}

func usRestrictionsReceiptSaved(t *testing.T, c *usRestrictionsCommand, revision int64) (usRestrictionsSavedResult, ResultDigest) {
	t.Helper()
	v := usRestrictionsSavedFixture()
	v.ownerID, v.account, v.memberIncarnation, v.profile, v.profileLifetime = c.request.ownerID, c.request.account, c.request.memberIncarnation, c.request.profile, c.request.profileLifetime
	v.revision = revision
	digest, err := usRestrictionsResultDigest(c.identity, c.digest, v)
	if err != nil {
		t.Fatal(err)
	}
	return v, digest
}

func TestUSRestrictionsReceiptReplayAfterSourceDrift(t *testing.T) {
	w, _, r, proof, input := usRestrictionsFixture(t, true, nil)
	q, err := usRestrictionsReceiptForCommand(input)
	if err != nil {
		t.Fatal(err)
	}
	v, digest := usRestrictionsReceiptSaved(t, input, 2)
	turn, a, _ := usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	if fresh, err := lookupUSRestrictions(t.Context(), turn, a, q); err != nil || fresh.decision != receiptFresh || fresh.owner.count != 0 {
		t.Fatal(fresh, err)
	}
	charged, err := storeUSRestrictionsResult(t.Context(), turn, a, q, v, digest)
	if err != nil || charged <= 0 {
		t.Fatal(charged, err)
	}
	usRestrictionsReceiptCommit(t, turn)
	// Real current-source drift after the original save: a different document,
	// revision and a retired profile. Replay must not read or reapply it.
	wc008NativeExec(t, w, `UPDATE profile_restrictions SET rating_system='',maximum_age_rating='',maximum_age=-1,blocked_labels='[]',allow_dvr=1,revision=revision+1 WHERE profile_id=?`, input.request.profile)
	wc008NativeExec(t, w, `UPDATE direct_profiles SET deleted=1 WHERE id=?`, input.request.profile)
	turn, a, _ = usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	for _, id := range []stmt.ID{usRestrictionsScopeCheck, usRestrictionsTargetFacts, usRestrictionsCurrentRow, usRestrictionsUpdate, usRestrictionsInsert} {
		delete(turn.steps, id)
	}
	var before, after int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	replay, err := lookupUSRestrictions(t.Context(), turn, a, q)
	if err != nil || replay.decision != receiptReplay || replay.digest != digest || !reflect.DeepEqual(replay.result, v) {
		t.Fatal(replay, err)
	}
	if err := turn.transaction.conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&after); err != nil || after != before {
		t.Fatal("replay made new effects", before, after, err)
	}
}

func TestUSRestrictionsReceiptChangedDigestConflicts(t *testing.T) {
	w, _, r, proof, input := usRestrictionsFixture(t, true, nil)
	q, err := usRestrictionsReceiptForCommand(input)
	if err != nil {
		t.Fatal(err)
	}
	v, digest := usRestrictionsReceiptSaved(t, input, 2)
	turn, a, _ := usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	if _, err = storeUSRestrictionsResult(t.Context(), turn, a, q, v, digest); err != nil {
		t.Fatal(err)
	}
	usRestrictionsReceiptCommit(t, turn)
	turn, a, _ = usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	changed := q
	changed.reference.RequestDigest = RequestDigest{9}
	if out, err := lookupUSRestrictions(t.Context(), turn, a, changed); !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, usRestrictionsReceiptLookup{}) {
		t.Fatal(out, err)
	}
}

func TestUSRestrictionsReceiptFloorOnlyACK(t *testing.T) {
	w, _, r, proof, input := usRestrictionsFixture(t, true, nil)
	q, err := usRestrictionsReceiptForCommand(input)
	if err != nil {
		t.Fatal(err)
	}
	v, digest := usRestrictionsReceiptSaved(t, input, 2)
	turn, a, _ := usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	charged, err := storeUSRestrictionsResult(t.Context(), turn, a, q, v, digest)
	if err != nil || charged <= 0 {
		t.Fatal(charged, err)
	}
	usRestrictionsReceiptCommit(t, turn)
	turn, a, _ = usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	ack, err := acknowledgeUSRestrictionsAfterAuthority(t.Context(), turn, a, q)
	if err != nil || ack != 1 {
		t.Fatal(ack, err)
	}
	usRestrictionsReceiptCommit(t, turn)
	var acked, count, bytes int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT acknowledged_sequence,retained_count,retained_variable_bytes FROM security_command_owners`).Scan(&acked, &count, &bytes); err != nil || acked != 1 || count != 1 || bytes != charged {
		t.Fatal("ACK refunded or deleted retained bytes", acked, count, bytes, charged, err)
	}
	if usRestrictionsQuery(t, w, `SELECT count(*) FROM security_command_receipts`) != 1 ||
		usRestrictionsQuery(t, w, `SELECT count(*) FROM security_restriction_results`) != 1 ||
		usRestrictionsQuery(t, w, `SELECT count(*) FROM security_restriction_result_labels`) != 2 {
		t.Fatal("ACK physically removed the cohort")
	}
	// The floor precedes all cohort decoding: even a drained child cohort must
	// classify retired, never Applied or an incomplete-result error.
	wc008NativeExec(t, w, `DELETE FROM security_restriction_result_labels`)
	turn, a, _ = usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	retired, err := lookupUSRestrictions(t.Context(), turn, a, q)
	if err != nil || retired.decision != receiptRetired || retired.result.ownerID != "" {
		t.Fatal(retired, err)
	}
	if again, err := acknowledgeUSRestrictionsAfterAuthority(t.Context(), turn, a, q); err != nil || again != 1 {
		t.Fatal("floor ACK was not idempotent", again, err)
	}
}

func TestUSRestrictionsReceiptPartialCohortIsResolveRequired(t *testing.T) {
	w, _, r, proof, input := usRestrictionsFixture(t, true, nil)
	q, err := usRestrictionsReceiptForCommand(input)
	if err != nil {
		t.Fatal(err)
	}
	v, digest := usRestrictionsReceiptSaved(t, input, 2)
	turn, a, _ := usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	if _, err = storeUSRestrictionsResult(t.Context(), turn, a, q, v, digest); err != nil {
		t.Fatal(err)
	}
	usRestrictionsReceiptCommit(t, turn)
	wc008NativeExec(t, w, `DELETE FROM security_restriction_result_labels WHERE ordinal=1`)
	turn, a, _ = usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	if out, err := lookupUSRestrictions(t.Context(), turn, a, q); !errors.Is(err, ErrResolveRequired) || !reflect.DeepEqual(out, usRestrictionsReceiptLookup{}) {
		t.Fatal("partial cohort escaped as Applied", out, err)
	}
}

func TestUSRestrictionsReceiptPrefixRollsBack(t *testing.T) {
	w, _, r, proof, input := usRestrictionsFixture(t, true, nil)
	q, err := usRestrictionsReceiptForCommand(input)
	if err != nil {
		t.Fatal(err)
	}
	v, digest := usRestrictionsReceiptSaved(t, input, 2)
	turn, a, _ := usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	// Omit only the final owner-advance step: the header, typed row and both
	// ordered children are already a nonempty prefix in this SAME frame.
	delete(turn.steps, usRestrictionsOwnerAdvance)
	if charged, err := storeUSRestrictionsResult(t.Context(), turn, a, q, v, digest); err == nil || charged != 0 {
		t.Fatal("partial receipt escaped", charged, err)
	} else if _, rbErr := turn.transaction.rollback(err); !errors.Is(rbErr, err) {
		t.Fatal(rbErr)
	}
	if usRestrictionsQuery(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 ||
		usRestrictionsQuery(t, w, `SELECT count(*) FROM security_restriction_results`) != 0 ||
		usRestrictionsQuery(t, w, `SELECT count(*) FROM security_restriction_result_labels`) != 0 ||
		usRestrictionsQuery(t, w, `SELECT retained_count FROM security_command_owners`) != 0 ||
		usRestrictionsQuery(t, w, `SELECT retained_variable_bytes FROM security_command_owners`) != 0 {
		t.Fatal("receipt prefix or owner debt survived rollback")
	}
}

// A foreign installation must match the missing-owner denial before any payload
// statement, so it cannot even learn whether the owner exists.
func TestUSRestrictionsReceiptForeignIssuerMatchesMissingOwner(t *testing.T) {
	w, _, r, _, input := usRestrictionsFixture(t, true, nil)
	q, err := usRestrictionsReceiptForCommand(input)
	if err != nil {
		t.Fatal(err)
	}
	other := usPolicyOtherIssuer(t, w)
	payload := []stmt.ID{usRestrictionsReceiptBinding, usRestrictionsReceiptHeader, usRestrictionsReceiptOctets, usRestrictionsReceiptResult, usRestrictionsReceiptResultSize, usRestrictionsReceiptLabelSize, usRestrictionsReceiptLabels}
	foreignTurn, foreignAuth, _ := usRestrictionsTurn(t, w, r, other, input.kind, 0)
	for _, id := range payload {
		delete(foreignTurn.steps, id)
	}
	foreignOut, foreignErr := lookupUSRestrictions(t.Context(), foreignTurn, foreignAuth, q)
	if !errors.Is(foreignErr, ErrOwnerUnavailable) || !reflect.DeepEqual(foreignOut, usRestrictionsReceiptLookup{}) {
		t.Fatal(foreignOut, foreignErr)
	}
	_, _ = foreignTurn.transaction.rollback(foreignErr)
	missingTurn, missingAuth, _ := usRestrictionsTurn(t, w, r, other, input.kind, 0)
	for _, id := range payload {
		delete(missingTurn.steps, id)
	}
	missing := q
	missing.ownerID = "no-such-owner"
	missingOut, missingErr := lookupUSRestrictions(t.Context(), missingTurn, missingAuth, missing)
	if !errors.Is(missingErr, ErrOwnerUnavailable) || !reflect.DeepEqual(missingOut, usRestrictionsReceiptLookup{}) {
		t.Fatal(missingOut, missingErr)
	}
	if foreignErr.Error() != missingErr.Error() {
		t.Fatal("foreign issuer leaked a different denial", foreignErr, missingErr)
	}
	if strings.Contains(foreignErr.Error(), "us_restrictions_statement") {
		t.Fatal("payload statement ran for a foreign issuer", foreignErr)
	}
}

// The same operation at the next retained sequence is a typed conflict before
// the INSERT, never a raw UNIQUE violation or an overwrite.
func TestUSRestrictionsReceiptSameOperationNextSequenceConflicts(t *testing.T) {
	w, _, r, proof, input := usRestrictionsFixture(t, true, nil)
	q, err := usRestrictionsReceiptForCommand(input)
	if err != nil {
		t.Fatal(err)
	}
	v, digest := usRestrictionsReceiptSaved(t, input, 2)
	turn, a, _ := usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	if _, err = storeUSRestrictionsResult(t.Context(), turn, a, q, v, digest); err != nil {
		t.Fatal(err)
	}
	usRestrictionsReceiptCommit(t, turn)
	turn, a, _ = usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	if ack, err := acknowledgeUSRestrictionsAfterAuthority(t.Context(), turn, a, q); err != nil || ack != 1 {
		t.Fatal(ack, err)
	}
	usRestrictionsReceiptCommit(t, turn)
	identity := input.identity
	identity.Sequence = 2
	next, err := prepareUSRestrictions(USProfileRestrictions, identity, input.request)
	if err != nil {
		t.Fatal(err)
	}
	nextValue, nextDigest := usRestrictionsReceiptSaved(t, next, 3)
	nextRef, err := usRestrictionsReceiptForCommand(next)
	if err != nil {
		t.Fatal(err)
	}
	turn, a, _ = usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	if _, err = storeUSRestrictionsResult(t.Context(), turn, a, nextRef, nextValue, nextDigest); !errors.Is(err, ErrConflict) {
		t.Fatal("reused operation was not a typed conflict", err)
	} else if strings.Contains(err.Error(), "UNIQUE") {
		t.Fatal("raw SQLite uniqueness error escaped", err)
	}
	_, _ = turn.transaction.rollback(err)
	if usRestrictionsQuery(t, w, `SELECT count(*) FROM security_command_receipts`) != 1 {
		t.Fatal("second sequence overwrote or added a receipt")
	}
}

// (b) A reclaimed sequence is never writable again: the floor is durable and
// physical reclaim (US-26) may have removed the original cohort.
func TestUSRestrictionsReceiptReclaimedSequenceRefused(t *testing.T) {
	w, _, r, proof, input := usRestrictionsFixture(t, true, nil)
	q, err := usRestrictionsReceiptForCommand(input)
	if err != nil {
		t.Fatal(err)
	}
	v, digest := usRestrictionsReceiptSaved(t, input, 2)
	turn, a, _ := usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	if _, err = storeUSRestrictionsResult(t.Context(), turn, a, q, v, digest); err != nil {
		t.Fatal(err)
	}
	usRestrictionsReceiptCommit(t, turn)
	turn, a, _ = usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	if ack, err := acknowledgeUSRestrictionsAfterAuthority(t.Context(), turn, a, q); err != nil || ack != 1 {
		t.Fatal(ack, err)
	}
	usRestrictionsReceiptCommit(t, turn)
	// Simulate US-26's physical reclaim after the durable floor.
	wc008NativeExec(t, w, `DELETE FROM security_restriction_result_labels`)
	wc008NativeExec(t, w, `DELETE FROM security_restriction_results`)
	wc008NativeExec(t, w, `DELETE FROM security_command_receipts`)
	wc008NativeExec(t, w, `UPDATE security_command_owners SET reclaimed_sequence=1`)
	turn, a, _ = usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	if charged, err := storeUSRestrictionsResult(t.Context(), turn, a, q, v, digest); !errors.Is(err, ErrConflict) || charged != 0 {
		t.Fatal("reclaimed sequence was writable", charged, err)
	}
	_, _ = turn.transaction.rollback(nil)
	if usRestrictionsQuery(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 ||
		usRestrictionsQuery(t, w, `SELECT last_sequence FROM security_command_owners`) != 1 ||
		usRestrictionsQuery(t, w, `SELECT retained_count FROM security_command_owners`) != 1 {
		t.Fatal("refused re-store disturbed the reclaimed owner")
	}
}
