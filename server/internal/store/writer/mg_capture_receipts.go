package writer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	mgCaptureReceiptRead          stmt.ID = "mg.capture.receipt.read"
	mgCaptureReceiptOperationRead stmt.ID = "mg.capture.receipt.operation"
	mgCaptureReceiptInsert        stmt.ID = "mg.capture.receipt.insert"
)
const mgCaptureReceiptHeaderColumns = `recording_id,command_incarnation,command_sequence,operation_id,request_digest,result_digest,applied_ms,command_kind,source_id,owner_key,authority,account_id,profile_id,member_incarnation,profile_lifetime,accepted_revision,accepted_acknowledged_sequence`

func mgCaptureReceiptDefinitions() []stmt.Definition {
	header := []stmt.Param{
		{Name: "recording", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "sequence", Type: stmt.Integer}, {Name: "operation", Type: stmt.Blob}, {Name: "request_digest", Type: stmt.Blob}, {Name: "result_digest", Type: stmt.Blob}, {Name: "applied", Type: stmt.Integer}, {Name: "kind", Type: stmt.Text}, {Name: "source", Type: stmt.Text}, {Name: "owner", Type: stmt.Text}, {Name: "authority", Type: stmt.Text}, {Name: "account", Type: stmt.Text}, {Name: "profile", Type: stmt.Text}, {Name: "member_lifetime", Type: stmt.Text}, {Name: "profile_lifetime", Type: stmt.Text}, {Name: "revision", Type: stmt.Integer}, {Name: "ack", Type: stmt.Integer},
	}
	params := append(header, mgCaptureSavedParams()...)
	columns := mgCaptureReceiptHeaderColumns + `,` + mgCaptureReceiptResultColumns
	return []stmt.Definition{
		{ID: mgCaptureReceiptRead, Kind: stmt.Read, SQL: `SELECT ` + columns + ` FROM dvr_capture_command_receipts WHERE recording_id=? AND command_incarnation=? AND command_sequence=?`, Params: header[:3]},
		{ID: mgCaptureReceiptOperationRead, Kind: stmt.Read, SQL: `SELECT ` + columns + ` FROM dvr_capture_command_receipts INDEXED BY sqlite_autoindex_dvr_capture_command_receipts_2 WHERE recording_id=? AND command_incarnation=? AND operation_id=?`, Params: []stmt.Param{header[0], header[1], header[3]}},
		{ID: mgCaptureReceiptInsert, Kind: stmt.Write, SQL: `INSERT INTO dvr_capture_command_receipts(` + columns + `) VALUES(` + strings.TrimSuffix(strings.Repeat("?,", len(params)), ",") + `)`, Params: params},
	}
}

type mgCaptureReceipt struct {
	command                                                                                 mgCaptureCommand
	resultDigest                                                                            mgCaptureDigest
	appliedMS                                                                               int64
	sourceID, ownerKey, authority, accountID, profileID, memberIncarnation, profileLifetime string
	revision, ackSequence                                                                   int64
	result                                                                                  mgCaptureSavedResult
}

func mgCaptureReceiptFor(f mgCaptureFacts) mgCaptureReceipt {
	s := f.recording
	r := mgCaptureReceipt{command: f.command, appliedMS: s.updatedMS, sourceID: s.sourceID, ownerKey: s.ownerKey, authority: s.authority, accountID: s.accountID, profileID: s.profileID, memberIncarnation: s.memberIncarnation, profileLifetime: s.profileLifetime, revision: s.revision, ackSequence: s.ackSequence, result: s.mgCaptureSavedResult}
	r.resultDigest = r.digest()
	return r
}

// Closed v1 binary encoding: fixed field order, explicit nullable tags, big
// endian integers and byte-length-prefixed UTF-8. Only this typed DVR result
// enters the digest; no arbitrary JSON or generic domain receipt codec exists.
func (r mgCaptureReceipt) digest() mgCaptureDigest {
	h := sha256.New()
	putText := func(v string) {
		_, _ = h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(v))))
		_, _ = h.Write([]byte(v))
	}
	putInt := func(v int64) { _, _ = h.Write(binary.BigEndian.AppendUint64(nil, uint64(v))) }
	putID := func(v mgCaptureOptionalID) {
		if v.valid {
			_, _ = h.Write([]byte{1})
			_, _ = h.Write(v.id[:])
		} else {
			_, _ = h.Write([]byte{0})
		}
	}
	putNullable := func(v mgCaptureNullableText) {
		if v.valid {
			_, _ = h.Write([]byte{1})
			putText(v.text)
		} else {
			_, _ = h.Write([]byte{0})
		}
	}
	putText("portico.mg.capture.result.v1")
	putText(r.command.recordingID)
	_, _ = h.Write(r.command.incarnation[:])
	putInt(r.command.sequence)
	_, _ = h.Write(r.command.operation[:])
	_, _ = h.Write(r.command.requestDigest[:])
	putText(string(r.command.kind))
	for _, s := range []string{r.sourceID, r.ownerKey, r.authority, r.accountID, r.profileID, r.memberIncarnation, r.profileLifetime} {
		putText(s)
	}
	putInt(r.appliedMS)
	putInt(r.revision)
	putInt(r.ackSequence)
	v := r.result
	putText(v.state)
	putText(v.reason)
	putInt(v.claimGeneration)
	putInt(v.cancelGeneration)
	putText(v.token)
	putText(v.ownerInstance)
	putInt(v.leaseMS)
	putText(v.allocationID)
	putInt(v.allocationGeneration)
	putText(v.allocationToken)
	putID(v.launch)
	putInt(v.writerRetiredSequence)
	putInt(v.physicalExitSequence)
	putID(v.exitLaunch)
	putInt(v.exitClaimGeneration)
	putInt(v.exitCancelGeneration)
	putInt(v.sharedReadySequence)
	putInt(v.sourceRevision)
	putText(v.sourceFence)
	putInt(v.itemID)
	putText(v.artifactID)
	putText(v.artifactDigest)
	putInt(v.bytes)
	putInt(v.capturedStartMS)
	putInt(v.capturedEndMS)
	putInt(v.gapCount)
	putInt(v.producerSucceeded)
	putInt(v.boundaryReached)
	putNullable(v.leaderID)
	putInt(v.shareClaimGeneration)
	putID(v.shareLaunch)
	putNullable(v.guidePin)
	var digest mgCaptureDigest
	copy(digest[:], h.Sum(nil))
	return digest
}
func (r mgCaptureReceipt) valid() bool {
	if !r.command.valid() || r.appliedMS < 0 || r.revision <= 0 || r.ackSequence != r.command.sequence-1 || !r.result.valid(r.command.sequence) || len(r.memberIncarnation) != 32 || !mgCaptureText(r.memberIncarnation, true) || len(r.profileLifetime) != 32 || !mgCaptureText(r.profileLifetime, true) {
		return false
	}
	for _, s := range []string{r.sourceID, r.ownerKey, r.authority} {
		if !mgCaptureText(s, true) {
			return false
		}
	}
	for _, s := range []string{r.accountID, r.profileID} {
		if !mgCaptureText(s, false) {
			return false
		}
	}
	return r.resultDigest == r.digest()
}
func (r mgCaptureReceipt) values() []any {
	c := r.command
	return append([]any{c.recordingID, c.incarnation[:], c.sequence, c.operation[:], c.requestDigest[:], r.resultDigest[:], r.appliedMS, string(c.kind), r.sourceID, r.ownerKey, r.authority, r.accountID, r.profileID, r.memberIncarnation, r.profileLifetime, r.revision, r.ackSequence}, mgCaptureSavedValues(r.result)...)
}
func mgReadCaptureReceiptRow(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (mgCaptureReceipt, error) {
	var r mgCaptureReceipt
	step, err := mgCaptureStep(t, id)
	if err != nil {
		return r, err
	}
	row, err := t.queryRow(ctx, step, args...)
	if err != nil {
		return r, err
	}
	var kind string
	targets := append([]any{&r.command.recordingID, &r.command.incarnation, &r.command.sequence, &r.command.operation, &r.command.requestDigest, &r.resultDigest, &r.appliedMS, &kind, &r.sourceID, &r.ownerKey, &r.authority, &r.accountID, &r.profileID, &r.memberIncarnation, &r.profileLifetime, &r.revision, &r.ackSequence}, mgCaptureSavedTargets(&r.result)...)
	if err := row.Scan(targets...); err != nil {
		return mgCaptureReceipt{}, err
	}
	r.command.kind = mgCaptureKind(kind)
	if !r.valid() {
		return mgCaptureReceipt{}, ErrConflict
	}
	return r, nil
}

// The parent checks retirement floors and fresh disclosure BEFORE delivery.
// History survives live-owner deletion. A missing receipt permits no fresh
// effects by itself; parent must resolve the live namespace/ACK/floor first.
func mgLookupCaptureReceipt(ctx context.Context, t *commandTurn, c mgCaptureCommand) (out mgCaptureReceipt, found bool, err error) {
	defer func() {
		out, err = finishCommand(ctx, commandLifetime(t), out, err)
		if err != nil {
			found = false
		}
	}()
	if !c.valid() {
		return out, false, ErrInvalid
	}
	out, err = mgReadCaptureReceiptRow(ctx, t, mgCaptureReceiptRead, c.recordingID, c.incarnation[:], c.sequence)
	if errors.Is(err, sql.ErrNoRows) {
		_, opErr := mgReadCaptureReceiptRow(ctx, t, mgCaptureReceiptOperationRead, c.recordingID, c.incarnation[:], c.operation[:])
		if errors.Is(opErr, sql.ErrNoRows) {
			return mgCaptureReceipt{}, false, nil
		}
		if opErr != nil {
			return mgCaptureReceipt{}, false, opErr
		}
		return mgCaptureReceipt{}, false, ErrConflict
	}
	if err != nil {
		return mgCaptureReceipt{}, false, err
	}
	if out.command != c {
		return mgCaptureReceipt{}, false, ErrConflict
	}
	return out, true, nil
}

// Save only the actual same-turn post-effect facts, and read back the immutable
// scalar receipt. The caller rolls back the WHOLE command on any error; this
// helper never commits, ACKs, or interprets a conflict as replayed success.
func mgSaveCaptureReceipt(ctx context.Context, t *commandTurn, f mgCaptureFacts) (out mgCaptureReceipt, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	s, c := f.recording, f.command
	if !s.valid() || !c.valid() || c.recordingID != s.id || c.incarnation != s.incarnation || c.sequence != s.lastSequence || s.ackSequence != c.sequence-1 {
		return out, ErrInvalid
	}
	if err = mgCaptureMatch(ctx, t, s); err != nil {
		return out, err
	}
	out = mgCaptureReceiptFor(f)
	if !out.valid() {
		return out, ErrInvalid
	}
	if _, found, e := mgLookupCaptureReceipt(ctx, t, c); e != nil {
		return out, e
	} else if found {
		return out, ErrConflict
	}
	step, err := mgCaptureStep(t, mgCaptureReceiptInsert)
	if err != nil {
		return out, err
	}
	result, err := t.exec(ctx, step, out.values()...)
	if err != nil {
		return out, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return out, err
	}
	if n != 1 {
		return out, ErrConflict
	}
	got, found, err := mgLookupCaptureReceipt(ctx, t, c)
	if err != nil {
		return out, err
	}
	if !found || got != out {
		return out, ErrConflict
	}
	return got, nil
}
