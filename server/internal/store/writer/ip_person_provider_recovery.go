package writer

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Exact owner-key recovery is a read component under CURRENT disclosure and
// admitted memory. It does not retry BEGIN, issue a capability or return Applied.
// BEGIN's original sequence continues to return Retired after ACK/reclamation.
// No latest-build selection, ID tail, scan, fallback generation or new owner.
type ipPersonRecoveryRequest struct {
	ownerID     string
	incarnation [16]byte
	personID    int64
	token       string
}
type ipPersonRecoveryResult struct {
	exists, terminal                                                                  bool
	ownerGeneration                                                                   int64
	buildID, evidenceID, algorithm                                                    string
	generation, revision, state, cursor, rows, bytes, stagedRows, stagedBytes, charge int64
	commitment                                                                        [32]byte
	sealOwner                                                                         string
	sealIncarnation                                                                   [16]byte
	sealSequence                                                                      int64
}

func ipPersonRecover(ctx context.Context, t *commandTurn, r ipPersonRecoveryRequest, b *ipPersonDecode) (out ipPersonRecoveryResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !ipPersonText(r.ownerID, 128, true) || r.incarnation == [16]byte{} || r.personID <= 0 || !ipPersonText(r.token, 256, true) {
		return out, ErrInvalid
	}
	key := []any{r.ownerID, r.incarnation[:], r.personID, r.token}
	var n int64
	if err = ipPersonScan(ctx, t, ipPPRecoveryOctets, key, &n); err != nil {
		return out, err
	}
	if err = b.reserve(n); err != nil {
		return out, err
	}
	var bid, eid, algorithm, sealOwner sql.NullString
	var generation, revision, state, cursor, rows, bytes, stagedRows, stagedBytes, charge, ownerGeneration, sealSequence sql.NullInt64
	var commitment, sealIncarnation []byte
	var terminal int64
	err = ipPersonScan(ctx, t, ipPPRecovery, key, &out.ownerGeneration, &terminal, &bid, &eid, &algorithm, &generation, &revision, &state, &cursor, &rows, &bytes, &stagedRows, &stagedBytes, &charge, &ownerGeneration, &commitment, &sealOwner, &sealIncarnation, &sealSequence)
	if err != nil {
		return out, err
	}
	out.terminal = terminal == 1
	if !bid.Valid {
		if n != 0 || eid.Valid || generation.Valid || out.ownerGeneration <= 0 {
			return out, ErrResolveRequired
		}
		// Terminal absence is a retained tombstone, never permission to begin again.
		return out, nil
	}
	if !eid.Valid || !algorithm.Valid || !generation.Valid || !revision.Valid || !state.Valid || !cursor.Valid || !rows.Valid || !bytes.Valid || !stagedRows.Valid || !stagedBytes.Valid || !charge.Valid || !ownerGeneration.Valid || generation.Int64 != out.ownerGeneration || ownerGeneration.Int64 != out.ownerGeneration || len(commitment) != 32 || algorithm.String != ipPersonManifest || n != int64(len(bid.String)+len(eid.String)+len(algorithm.String)+len(sealOwner.String)) || !ipPersonText(bid.String, 128, true) || !ipPersonText(eid.String, 128, true) || revision.Int64 <= 0 || rows.Int64 < 1 || rows.Int64 > ipPersonMaxChildren+1 || bytes.Int64 < 1 || bytes.Int64 > ipPersonMaxBytes || stagedRows.Int64 < 1 || stagedRows.Int64 > rows.Int64 || cursor.Int64 != stagedRows.Int64 || stagedBytes.Int64 < 1 || stagedBytes.Int64 > bytes.Int64 || charge.Int64 != stagedBytes.Int64 {
		return out, ErrResolveRequired
	}
	if sealOwner.Valid != sealSequence.Valid || sealOwner.Valid != (len(sealIncarnation) > 0) || sealOwner.Valid && (len(sealIncarnation) != 16 || sealSequence.Int64 <= 0 || sealOwner.String != r.ownerID) || state.Int64 == 2 && !sealOwner.Valid {
		return out, ErrResolveRequired
	}
	out.exists = true
	out.buildID = bid.String
	out.evidenceID = eid.String
	out.algorithm = algorithm.String
	out.generation = generation.Int64
	out.revision = revision.Int64
	out.state = state.Int64
	out.cursor = cursor.Int64
	out.rows = rows.Int64
	out.bytes = bytes.Int64
	out.stagedRows = stagedRows.Int64
	out.stagedBytes = stagedBytes.Int64
	out.charge = charge.Int64
	copy(out.commitment[:], commitment)
	if sealOwner.Valid {
		out.sealOwner = sealOwner.String
		copy(out.sealIncarnation[:], sealIncarnation)
		out.sealSequence = sealSequence.Int64
	}
	return out, nil
}

const (
	ipPPRecoveryOctets stmt.ID = "ip.person.recovery_octets"
	ipPPRecovery       stmt.ID = "ip.person.recovery"
)

func ipPersonRecoveryDefinitions() []stmt.Definition {
	return []stmt.Definition{
		ipPersonDefinition(ipPPRecoveryOctets, stmt.Read, `SELECT coalesce(octet_length(b.id),0)+coalesce(octet_length(b.evidence_id),0)+coalesce(octet_length(b.expected_algorithm),0)+coalesce(octet_length(b.seal_owner),0) FROM metadata_person_operations o JOIN catalog_people p ON p.id=o.person_id LEFT JOIN metadata_person_builds b INDEXED BY metadata_person_build_owner ON b.owner_id=o.id AND b.owner_incarnation=o.command_incarnation WHERE o.id=? AND o.command_incarnation=? AND o.person_id=? AND p.token=?`, "tbit"),
		ipPersonDefinition(ipPPRecovery, stmt.Read, `SELECT o.generation,(o.phase='done'),b.id,b.evidence_id,b.expected_algorithm,b.generation,b.content_revision,CASE WHEN b.id IS NULL THEN NULL WHEN b.state='building' THEN 1 WHEN b.state='ready' THEN 2 WHEN b.state='retiring' THEN 3 ELSE 4 END,b.cursor,b.expected_rows,b.expected_bytes,b.staged_rows,b.staged_bytes,b.materialized_charge,b.owner_generation,b.expected_commitment,b.seal_owner,b.seal_incarnation,b.seal_sequence FROM metadata_person_operations o JOIN catalog_people p ON p.id=o.person_id LEFT JOIN metadata_person_builds b INDEXED BY metadata_person_build_owner ON b.owner_id=o.id AND b.owner_incarnation=o.command_incarnation AND b.provider=o.provider AND b.provider_id=o.provider_id AND b.subject_kind='person' WHERE o.id=? AND o.command_incarnation=? AND o.person_id=? AND p.token=?`, "tbit"),
	}
}
