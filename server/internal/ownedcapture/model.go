package ownedcapture

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
)

func framed(domain string, v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(append([]byte(domain+"\x00"), b...))
	return hex.EncodeToString(sum[:])
}
func captureVersion(id string, b Binding, object mediaartifact.Object) (mediasource.Version, error) {
	if !captureID.MatchString(id) || !b.valid() {
		return mediasource.Version{}, ErrInput
	}
	return mediasource.NewVersion(mediasource.Evidence{Kind: mediasource.ContentSHA256,
		Scope: framed("portico.owned-capture.scope.v1", b), Object: id, Revision: object.Digest, Size: object.Size})
}

const sourceColumns = `s.capture_id,s.source_id,s.config_generation,s.object_id,s.object_revision,s.asset_id,s.acquisition_id,s.reservation_id,s.policy_witness,s.authority_witness,s.expected_size,s.created_ms,s.expires_ms,l.state,l.pending_digest`

type sourceRow struct {
	id                     string
	b                      Binding
	size, created, expires int64
	state, digest          string
}

func readSource(row interface{ Scan(...any) error }) (sourceRow, error) {
	var r sourceRow
	err := row.Scan(&r.id, &r.b.SourceID, &r.b.ConfigGeneration, &r.b.ObjectID, &r.b.ObjectRevision, &r.b.AssetID, &r.b.AcquisitionID, &r.b.ReservationID, &r.b.PolicyWitness, &r.b.AuthorityWitness, &r.size, &r.created, &r.expires, &r.state, &r.digest)
	if err == nil && (!r.b.valid() || !captureID.MatchString(r.id) || r.size <= 0 || r.expires <= r.created) {
		err = ErrInput
	}
	return r, err
}
func sourceTx(ctx context.Context, tx *sql.Tx, id string) (sourceRow, error) {
	return readSource(tx.QueryRowContext(ctx, `SELECT `+sourceColumns+` FROM owned_capture_sources s JOIN owned_capture_lifecycle l ON l.capture_id=s.capture_id WHERE s.capture_id=?`, id))
}
func (s *Service) resolveTx(ctx context.Context, tx *sql.Tx, id string, b Binding) (Receipt, error) {
	var out Receipt
	if s.life.Err() != nil || tx == nil || !captureID.MatchString(id) || !b.valid() {
		return out, ErrInput
	}
	source, err := sourceTx(ctx, tx, id)
	if err != nil {
		return out, err
	}
	if source.b != b || source.state != "ready" || source.expires <= time.Now().UnixMilli() {
		return out, ErrCurrent
	}
	out.ID, out.Binding, out.ExpiresAt = id, b, time.UnixMilli(source.expires).UTC()
	var version string
	err = tx.QueryRowContext(ctx, `SELECT artifact_id,digest,size,version_id FROM owned_capture_receipts WHERE capture_id=?`, id).Scan(&out.ArtifactID, &out.Object.Digest, &out.Object.Size, &version)
	if err != nil {
		return Receipt{}, err
	}
	if !captureID.MatchString(out.ArtifactID) {
		return Receipt{}, ErrCurrent
	}
	out.Version, err = captureVersion(id, b, out.Object)
	if err != nil || out.Version.ID() != version || out.Object.Size != source.size {
		return Receipt{}, ErrCurrent
	}
	if err = s.producer.CheckTx(ctx, tx, b); err != nil {
		return Receipt{}, err
	}
	return out, ctx.Err()
}

// ResolveTx accepts only the requested immutable capture and original bindings.
// It never discovers a latest receipt or turns a digest into source authority.
func (s *Service) ResolveTx(ctx context.Context, tx *sql.Tx, id string, b Binding) (Receipt, error) {
	return s.resolveTx(ctx, tx, id, b)
}
func sameReceipt(a, b Receipt) bool {
	return a.ID == b.ID && a.ArtifactID == b.ArtifactID && a.Binding == b.Binding && a.Object == b.Object && a.Version.ID() == b.Version.ID() && a.ExpiresAt.Equal(b.ExpiresAt)
}
