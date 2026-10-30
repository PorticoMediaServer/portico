package downloads

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
	"time"
)

func readingKind(kind string) bool { return kind == "ebook" || kind == "comic" }

// Installed once with the router's actual Reading authority/source service.
func (s *Service) SetReading(service *reading.Service) {
	s.accessMu.Lock()
	s.reading = service
	s.accessMu.Unlock()
}
func (s *Service) readingService() *reading.Service {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	return s.reading
}
func (s *Service) publicationForRequest(ctx context.Context, p identity.Principal, r Request) (*reading.OfflineDocument, error) {
	if r.MediaID == "" {
		return nil, nil
	}
	snap, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return nil, err
	}
	tx := snap.Tx()
	found, err := resolveVersionSource(ctx, tx, r.MediaID, r.VersionID)
	if err != nil {
		snap.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if !readingKind(found.Kind) {
		snap.Rollback()
		return nil, nil
	}
	if r.VersionID == "" || len(r.MediaIDs) > 0 || r.NextAfterMediaID != "" || (r.Quality != "" && string(r.Quality) != QualityOriginal) {
		snap.Rollback()
		return nil, ErrInput
	}
	if err = s.visibleItem(ctx, tx, p, r.MediaID); err != nil {
		snap.Rollback()
		return nil, err
	}
	allowed, err := ProfileAllowsDownloads(tx, p.Viewer)
	snap.Rollback()
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, identity.ErrForbidden
	}
	reader := s.readingService()
	if reader == nil {
		return nil, ErrInput
	}
	doc, err := reader.OfflineDocument(ctx, p, r.MediaID, r.VersionID)
	if err != nil {
		return nil, err
	}
	// Keep the escaped descriptor plus existing bundle metadata within the
	// unchanged page response budget. Larger descriptors fail before admission.
	raw, err := json.Marshal(doc)
	if err == nil {
		_, err = reading.ParseOfflineDocument(string(raw))
	}
	if err != nil || len(raw) > 160<<10 {
		return nil, ErrInput
	}
	return &doc, nil
}
func (s *Service) checkPublicationTx(ctx context.Context, tx *sql.Tx, p identity.Principal, found source, doc *reading.OfflineDocument) error {
	if !readingKind(found.Kind) {
		return nil
	}
	reader := s.readingService()
	if reader == nil || doc == nil || doc.ItemID != found.Item || doc.SourceID != found.Asset || doc.Size != found.Size {
		return ErrInput
	}
	selected, err := reader.SelectOfflineTx(ctx, tx, p, found.Item, found.Asset)
	if err != nil {
		return err
	}
	if selected.Revision != doc.Revision || selected.Format != doc.Format || selected.ModifiedNS != found.ModifiedNS {
		return reading.ErrChanged
	}
	return nil
}

// ValidatePublicationReceipt requires both the cryptographic envelope and the
// current authoritative receipt row, current profile entitlement and content access.
func (s *Service) ValidatePublicationReceipt(ctx context.Context, p identity.Principal, serverID string, envelope Receipt) (ReceiptClaims, error) {
	keys, err := s.ReceiptKeys(ctx)
	if err != nil {
		return ReceiptClaims{}, err
	}
	claims, err := VerifyReceipt(keys, envelope, s.now())
	if err != nil {
		return claims, err
	}
	if claims.Publication == nil || claims.Viewer.Authority != p.Authority || claims.Viewer.AccountID != p.AccountID || claims.Viewer.ProfileID != p.ProfileID || claims.Viewer.ServerID != serverID {
		return claims, identity.ErrForbidden
	}
	snap, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return claims, err
	}
	defer snap.Rollback()
	return claims, s.validatePublicationReceiptTx(ctx, snap.Tx(), p, serverID, claims)
}

// The only callback handed to the Reading accepting writer captures claims
// verified by this service, not an unsigned caller DTO or a later current ref.
func (s *Service) PublicationReceiptValidator(ctx context.Context, p identity.Principal, serverID string, envelope Receipt) (ReceiptClaims, func(context.Context, *sql.Tx, identity.Principal) error, error) {
	claims, err := s.ValidatePublicationReceipt(ctx, p, serverID, envelope)
	if err != nil {
		return claims, nil, err
	}
	binding := *claims.Publication
	original := claims
	original.Publication = &binding
	validate := func(ctx context.Context, tx *sql.Tx, actor identity.Principal) error {
		return s.validatePublicationReceiptTx(ctx, tx, actor, serverID, original)
	}
	return claims, validate, nil
}
func (s *Service) validatePublicationReceiptTx(ctx context.Context, tx *sql.Tx, p identity.Principal, serverID string, claims ReceiptClaims) error {
	if claims.Publication == nil || claims.Viewer.Authority != p.Authority || claims.Viewer.AccountID != p.AccountID || claims.Viewer.ProfileID != p.ProfileID || claims.Viewer.ServerID != serverID {
		return identity.ErrForbidden
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, claims.ExpiresAt)
	if err != nil || !expiresAt.After(s.now()) {
		return identity.ErrForbidden
	}
	var item, preparation, digest string
	var bytes, expires, revoked int64
	err = tx.QueryRowContext(ctx, `SELECT item_public,preparation_id,artifact_digest,artifact_bytes,expires_ms,revoked_ms FROM download_receipts WHERE id=? AND profile_key=?`, claims.ReceiptID, operations.ViewerKey(p)).Scan(&item, &preparation, &digest, &bytes, &expires, &revoked)
	if err != nil {
		return identity.ErrForbidden
	}
	if revoked != 0 || item != claims.ItemID || expires < s.millis() || preparation != claims.PreparationID || digest != claims.Artifact.SHA256 || bytes != claims.Artifact.Bytes {
		return identity.ErrForbidden
	}
	allowed, err := ProfileAllowsDownloads(tx, p.Viewer)
	if err != nil {
		return err
	}
	if !allowed {
		return identity.ErrForbidden
	}
	if err = s.visibleItem(ctx, tx, p, claims.ItemID); err != nil {
		return err
	}
	doc, err := publicationTx(ctx, tx, preparation)
	if err != nil {
		return err
	}
	if doc == nil || doc.ItemID != claims.ItemID || doc.SourceID != claims.Publication.SourceID || doc.Revision != claims.Publication.Revision || doc.Format != claims.Publication.Format || doc.Digest() != claims.Publication.DocumentSHA256 {
		return identity.ErrForbidden
	}
	return nil
}
func publicationJSON(doc *reading.OfflineDocument) string {
	if doc == nil {
		return ""
	}
	raw, _ := json.Marshal(doc)
	return string(raw)
}

// Keep the literal validated bytes. Decoding is never authority to remarshal
// an older descriptor under today's struct or silently remint its receipt digest.
type storedPublication struct {
	*reading.OfflineDocument
	raw string
}

func (d *storedPublication) Digest() string {
	sum := sha256.Sum256([]byte(d.raw))
	return hex.EncodeToString(sum[:])
}
func publicationTx(ctx context.Context, tx *sql.Tx, id string) (*storedPublication, error) {
	var raw, title string
	if err := tx.QueryRowContext(ctx, `SELECT publication_json,title_json FROM download_preparations WHERE id=?`, id).Scan(&raw, &title); err != nil {
		return nil, err
	}
	if raw == "" {
		// A retired/legacy Reading preparation is not an ordinary media receipt.
		// Never reconstruct or silently downgrade its absent signed descriptor.
		var stored Title
		if json.Unmarshal([]byte(title), &stored) == nil && readingKind(stored.Kind) {
			return nil, ErrUnavailable
		}
		return nil, nil
	}
	doc, err := reading.ParseOfflineDocument(raw)
	if err != nil {
		return nil, err
	}
	return &storedPublication{&doc, raw}, nil
}

func (s *Service) VerifyPublicationObservation(ctx context.Context, current ReceiptClaims, envelope Receipt, started, observed int64) error {
	keys, err := s.ReceiptKeys(ctx)
	if err != nil {
		return err
	}
	original, err := VerifyReceipt(keys, envelope, time.UnixMilli(observed))
	if err != nil {
		return err
	}
	if original.ReceiptID != current.ReceiptID || original.PreparationID != current.PreparationID || original.ItemID != current.ItemID || original.Viewer != current.Viewer || original.Artifact != current.Artifact || original.Publication == nil || current.Publication == nil || *original.Publication != *current.Publication {
		return identity.ErrForbidden
	}
	issued, err := time.Parse(time.RFC3339Nano, original.IssuedAt)
	if err != nil || started < issued.UnixMilli() {
		return ErrInput
	}
	return nil
}
