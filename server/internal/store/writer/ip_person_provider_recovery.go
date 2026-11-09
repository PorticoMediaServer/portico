package writer

import (
	"context"
	"database/sql"
	"errors"
)

// Exact owner-key recovery is a read component under CURRENT disclosure and an
// admitted turn. It does not retry BEGIN, issue a capability or return Applied.
// BEGIN's original sequence continues to return Retired after ACK/reclamation.
// The unique (owner_id,owner_incarnation) index is the only build selector: no
// latest-build, ID-tail, scan or fallback generation exists.
type ipPersonRecoveryRequest struct {
	ownerID     string
	incarnation [16]byte
	personID    int64
	token       string
}

type ipPersonRecoveryResult struct {
	exists, terminal                                                             bool
	ownerGeneration, ownerRevision                                               int64
	buildID, evidenceID, algorithm, state, owner                                 string
	ownerIncarnation                                                             [16]byte
	generation, cursor, rows, bytes, stagedRows, stagedBytes, charge, contentRev int64
	commitment                                                                   [32]byte
}

func ipPersonRecover(ctx context.Context, turn *commandTurn, r ipPersonRecoveryRequest) (out ipPersonRecoveryResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || len(r.ownerID) != 32 ||
		r.incarnation == [16]byte{} || r.personID <= 0 ||
		!ipPersonText(r.token, ipPersonTokenMax, true) {
		return out, ErrInvalid
	}
	row, err := wc008DraftQuery(ctx, turn, ipPersonRecoveryStmt, r.ownerID, r.incarnation[:], r.personID, r.token)
	if err != nil {
		return out, err
	}
	var phase string
	var buildID, evidenceID, algorithm, state, owner sql.NullString
	var commitment []byte
	var generation, cursor, rows, bytes, stagedRows, stagedBytes, charge, contentRev, ownerGeneration sql.NullInt64
	if err = row.Scan(&out.ownerGeneration, &phase, &out.ownerRevision, &buildID, &evidenceID,
		&generation, &state, &cursor, &rows, &bytes, &stagedRows, &stagedBytes, &charge,
		&contentRev, &algorithm, &commitment, &ownerGeneration); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, ErrResolveRequired
		}
		return out, err
	}
	if out.ownerGeneration <= 0 || out.ownerRevision <= 0 || !ipPersonPhase(phase) {
		return out, ErrResolveRequired
	}
	out.terminal = phase == "done"
	if !buildID.Valid {
		if evidenceID.Valid || generation.Valid || state.Valid || algorithm.Valid || len(commitment) != 0 ||
			owner.Valid || ownerGeneration.Valid {
			return out, ErrResolveRequired
		}
		// Terminal absence is a retained tombstone, never permission to begin again.
		return out, nil
	}
	if !evidenceID.Valid || !generation.Valid || !state.Valid || !cursor.Valid || !rows.Valid ||
		!bytes.Valid || !stagedRows.Valid || !stagedBytes.Valid || !charge.Valid || !contentRev.Valid ||
		!algorithm.Valid || len(commitment) != 32 || !ownerGeneration.Valid || !owner.Valid {
		return out, ErrResolveRequired
	}
	if generation.Int64 != out.ownerGeneration || ownerGeneration.Int64 != out.ownerGeneration ||
		cursor.Int64 < 0 || rows.Int64 <= 0 || bytes.Int64 <= 0 || stagedRows.Int64 < 0 ||
		stagedRows.Int64 > rows.Int64 || cursor.Int64 != stagedRows.Int64 || stagedBytes.Int64 < 0 ||
		stagedBytes.Int64 > bytes.Int64 || charge.Int64 < 0 || contentRev.Int64 <= 0 ||
		algorithm.String != ipPersonManifest ||
		!ipPersonText(buildID.String, ipPersonPointerMax, true) ||
		!ipPersonText(evidenceID.String, ipPersonPointerMax, true) ||
		!ipPersonText(owner.String, ipPersonPointerMax, true) {
		return out, ErrResolveRequired
	}
	switch state.String {
	case "building", "ready", "retiring", "done":
	default:
		return out, ErrResolveRequired
	}
	out.exists = true
	out.buildID = buildID.String
	out.evidenceID = evidenceID.String
	out.algorithm = algorithm.String
	out.state = state.String
	out.owner = owner.String
	out.ownerIncarnation = r.incarnation
	out.generation = generation.Int64
	out.cursor = cursor.Int64
	out.rows = rows.Int64
	out.bytes = bytes.Int64
	out.stagedRows = stagedRows.Int64
	out.stagedBytes = stagedBytes.Int64
	out.charge = charge.Int64
	out.contentRev = contentRev.Int64
	raw := commitment
	copy(out.commitment[:], raw)
	if out.owner != r.ownerID || out.buildID == "" || out.evidenceID == "" {
		return out, ErrResolveRequired
	}
	return out, nil
}
