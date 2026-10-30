package downloads

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// ReceiptKind and ReceiptVersion identify the signed document. A verifier that
// does not recognize both refuses the receipt rather than guessing its meaning.
const (
	ReceiptKind    = "portico.download-receipt"
	ReceiptVersion = "1"
)

// ReceiptViewer is the exact viewer a receipt is good for. A receipt is not
// transferable: it names the authority, account and profile, and a client that
// signed in as somebody else must ask for its own.
type ReceiptViewer struct {
	Authority string `json:"authority"`
	AccountID string `json:"accountId"`
	ProfileID string `json:"profileId"`
	ServerID  string `json:"serverId"`
}

// ReceiptArtifact binds the receipt to specific bytes. A client verifies the
// file it holds against sha256 before playing it offline.
type ReceiptArtifact struct {
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

// ReceiptClaims is the signed document itself.
type ReceiptPublication struct {
	SourceID       string `json:"sourceId"`
	Revision       string `json:"revision"`
	Format         string `json:"format"`
	DocumentSHA256 string `json:"documentSha256"`
}

func (ReceiptPublication) JSONSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"sourceId", "revision", "format", "documentSha256"},
		"properties": map[string]any{
			"sourceId":       map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "pattern": "^[A-Za-z0-9_-]+$"},
			"revision":       map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"},
			"documentSha256": map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"},
			"format":         map[string]any{"type": "string", "enum": []string{"epub", "pdf", "cbz", "cb7", "cbr"}},
		}}
}

type ReceiptClaims struct {
	Publication   *ReceiptPublication `json:"publication,omitempty"`
	Kind          string              `json:"kind"`
	Version       string              `json:"version"`
	ReceiptID     string              `json:"receiptId"`
	KeyID         string              `json:"keyId"`
	Viewer        ReceiptViewer       `json:"viewer"`
	ItemID        string              `json:"itemId"`
	PreparationID string              `json:"preparationId"`
	Quality       string              `json:"quality"`
	QualityLabel  string              `json:"qualityLabel"`
	Artifact      ReceiptArtifact     `json:"artifact"`
	IssuedAt      string              `json:"issuedAt"`
	ExpiresAt     string              `json:"expiresAt"`
}

// Receipt is what a client stores. payload carries the exact signed bytes, so a
// verifier never has to reproduce this server's JSON formatting to check the
// signature; claims is the same document decoded, for convenience.
type Receipt struct {
	ReceiptID string        `json:"receiptId"`
	Algorithm string        `json:"algorithm"`
	KeyID     string        `json:"keyId"`
	Payload   string        `json:"payload"`
	Signature string        `json:"signature"`
	Claims    ReceiptClaims `json:"claims"`
	Revision  int64         `json:"revision"`
}

// ReceiptKey is a published verification key. The private half never leaves the
// server; a client pins the key it saw and refuses a receipt signed by another.
type ReceiptKey struct {
	ID        string `json:"id"`
	Algorithm string `json:"algorithm"`
	PublicKey string `json:"publicKey"`
	CreatedAt string `json:"createdAt"`
	RetiredAt string `json:"retiredAt,omitempty"`
}

// ReceiptOutcome is one entry's answer from issue or revalidate. TitleOnServer
// accompanies a receipt: false tells the device that the title has left this
// server's library, so it can say so beside a copy that still plays.
type ReceiptOutcome struct {
	PreparationID string   `json:"preparationId,omitempty"`
	ReceiptID     string   `json:"receiptId,omitempty"`
	Outcome       string   `json:"outcome"`
	Code          string   `json:"code,omitempty"`
	Receipt       *Receipt `json:"receipt,omitempty"`
	TitleOnServer *bool    `json:"titleOnServer,omitempty"`
	// Viewing is where the viewer stands with the title on the server now, sent
	// with a renewal so a copy on a device resumes from where another device
	// left off, not from where this one last was.
	Viewing *ReceiptViewing `json:"viewing,omitempty"`
}

// ReceiptViewing is a title's resume point and watched state.
type ReceiptViewing struct {
	PositionSeconds float64 `json:"positionSeconds"`
	Watched         bool    `json:"watched"`
}

// Outcomes. "issued" and "renewed" carry a receipt. "refused" with
// unknown_receipt or unknown_preparation means this server has no record of
// what was asked about and says nothing about the copy; every other refusal
// means the viewer may no longer play it.
const (
	OutcomeIssued  = "issued"
	OutcomeRenewed = "renewed"
	OutcomeRefused = "refused"
)

// Revocation is one entry of the revocation list a client polls.
type Revocation struct {
	ReceiptID string `json:"receiptId"`
	ItemID    string `json:"itemId"`
	ProfileID string `json:"profileId"`
	Reason    string `json:"reason"`
	RevokedAt string `json:"revokedAt"`
	Sequence  int64  `json:"sequence"`
}

// RevocationPage is a cursor page of revocations plus the instant it describes.
type RevocationPage struct {
	Items      []Revocation `json:"items"`
	NextCursor string       `json:"nextCursor"`
	AsOf       string       `json:"asOf"`
}

// signingKey returns the active receipt key, creating one on first use. Key
// creation is part of the caller's transaction, so two concurrent first issues
// cannot install two keys.
func signingKey(tx *sql.Tx, now int64) (string, ed25519.PrivateKey, error) {
	var id string
	var private []byte
	e := tx.QueryRow(`SELECT id,private_key FROM download_receipt_keys WHERE retired_ms=0 ORDER BY created_ms DESC LIMIT 1`).Scan(&id, &private)
	if e == nil {
		if len(private) != ed25519.PrivateKeySize {
			return "", nil, ErrReceipt
		}
		return id, ed25519.PrivateKey(private), nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return "", nil, e
	}
	public, secret, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		return "", nil, e
	}
	id = identity.Digest(identity.Token())[:32]
	if _, e = tx.Exec(`INSERT INTO download_receipt_keys(id,algorithm,private_key,public_key,created_ms) VALUES(?,'ed25519',?,?,?)`, id, []byte(secret), []byte(public), now); e != nil {
		return "", nil, e
	}
	return id, secret, nil
}

// ReceiptKeys publishes every verification key this server has used, so a
// client can still verify a receipt signed before a rotation while refusing to
// accept a new one from a retired key.
func (s *Service) ReceiptKeys(ctx context.Context) ([]ReceiptKey, error) {
	out := []ReceiptKey{}
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassForegroundTransfer)
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if _, _, e = signingKey(tx, s.millis()); e != nil {
		return out, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT id,algorithm,public_key,created_ms,retired_ms FROM download_receipt_keys ORDER BY created_ms DESC LIMIT 32`)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var key ReceiptKey
		var public []byte
		var created, retired int64
		if e = rows.Scan(&key.ID, &key.Algorithm, &public, &created, &retired); e != nil {
			rows.Close()
			return out, e
		}
		key.PublicKey, key.CreatedAt, key.RetiredAt = base64.RawURLEncoding.EncodeToString(public), stamp(created), stamp(retired)
		out = append(out, key)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	return out, gated.Commit()
}

// sign renders and signs one receipt.
func sign(keyID string, secret ed25519.PrivateKey, claims ReceiptClaims, revision int64) (Receipt, error) {
	raw, e := json.Marshal(claims)
	if e != nil {
		return Receipt{}, e
	}
	return Receipt{
		ReceiptID: claims.ReceiptID,
		Algorithm: "ed25519",
		KeyID:     keyID,
		Payload:   base64.RawURLEncoding.EncodeToString(raw),
		Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(secret, raw)),
		Claims:    claims,
		Revision:  revision,
	}, nil
}

// VerifyReceipt checks an envelope against this server's published keys. It is
// the same check a client performs offline, exposed so the server's own tests
// and a third-party client can agree on what "valid" means.
func VerifyReceipt(keys []ReceiptKey, envelope Receipt, now time.Time) (ReceiptClaims, error) {
	var claims ReceiptClaims
	raw, e := base64.RawURLEncoding.Strict().DecodeString(envelope.Payload)
	if e != nil || len(raw) > 8<<10 {
		return claims, ErrReceipt
	}
	signature, e := base64.RawURLEncoding.Strict().DecodeString(envelope.Signature)
	if e != nil || len(signature) != ed25519.SignatureSize {
		return claims, ErrReceipt
	}
	if e = json.Unmarshal(raw, &claims); e != nil {
		return claims, ErrReceipt
	}
	if claims.Kind != ReceiptKind || claims.Version != ReceiptVersion || claims.KeyID != envelope.KeyID {
		return claims, ErrReceipt
	}
	for _, key := range keys {
		if key.ID != envelope.KeyID || key.Algorithm != "ed25519" {
			continue
		}
		public, err := base64.RawURLEncoding.Strict().DecodeString(key.PublicKey)
		if err != nil || len(public) != ed25519.PublicKeySize {
			return claims, ErrReceipt
		}
		if !ed25519.Verify(ed25519.PublicKey(public), raw, signature) {
			return claims, ErrReceipt
		}
		expires, err := time.Parse(time.RFC3339, claims.ExpiresAt)
		if err != nil || !now.Before(expires) {
			return claims, ErrReceipt
		}
		return claims, nil
	}
	return claims, ErrReceipt
}

// IssueReceipts hands out offline authorization for ready preparations. Each
// entry answers on its own, because one deleted item in a season must not cost
// a client the receipts for the rest of it.
func (s *Service) IssueReceipts(ctx context.Context, p identity.Principal, serverID, operationID string, preparations []string) ([]ReceiptOutcome, error) {
	out := []ReceiptOutcome{}
	if !validID.MatchString(operationID) || len(preparations) == 0 || len(preparations) > MaxBatchTargets {
		return out, ErrInput
	}
	for _, id := range preparations {
		if !validID.MatchString(id) {
			return out, ErrInput
		}
	}
	gated2, e := dbwork.Begin(ctx, s.db, dbwork.ClassForegroundTransfer)
	if e != nil {
		return out, e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	now := s.millis()
	key := operations.ViewerKey(p)
	scope := "downloads-receipts:" + key
	raw, digest, e := operations.Receipt(tx, scope, operationID, []any{preparations}, now)
	if e != nil {
		return out, e
	}
	allowed, e := ProfileAllowsDownloads(tx, p.Viewer)
	if e != nil {
		return out, e
	}
	if !allowed {
		return out, ErrPolicy
	}
	if raw != "" {
		if e = json.Unmarshal([]byte(raw), &out); e != nil {
			return out, e
		}
		if e = s.checkReceiptReplay(ctx, tx, p, out, nil, now); e != nil {
			return out, e
		}
		return out, gated2.Commit()
	}
	keyID, secret, e := signingKey(tx, now)
	if e != nil {
		return out, e
	}
	for _, id := range preparations {
		var state, item, library, quality, artifactDigest string
		var size int64
		var present bool
		err := tx.QueryRowContext(ctx, `SELECT state,item_public,library_id,quality,artifact_digest,bytes_total,NOT (`+titleGone+`) FROM download_preparations WHERE id=? AND profile_key=?`, id, key).Scan(&state, &item, &library, &quality, &artifactDigest, &size, &present)
		if errors.Is(err, sql.ErrNoRows) {
			out = append(out, ReceiptOutcome{PreparationID: id, Outcome: OutcomeRefused, Code: ReasonUnknownPreparation})
			continue
		}
		if err != nil {
			return out, err
		}
		if !present {
			out = append(out, ReceiptOutcome{PreparationID: id, Outcome: OutcomeRefused, Code: ReasonItemDeleted})
			continue
		}
		if state != StateReady || !validDigest.MatchString(artifactDigest) || size <= 0 {
			out = append(out, ReceiptOutcome{PreparationID: id, Outcome: OutcomeRefused, Code: ReasonFailed})
			continue
		}
		if err = s.visibleItem(ctx, tx, p, item); err != nil {
			// A catalogue change still publishing is a retry, not a refusal.
			if !errors.Is(err, identity.ErrContentRestricted) {
				return out, err
			}
			out = append(out, ReceiptOutcome{PreparationID: id, Outcome: OutcomeRefused, Code: ReasonItemDeleted})
			continue
		}
		receiptID, receipt, err := s.storeReceipt(ctx, tx, p, serverID, keyID, secret, id, item, library, quality, artifactDigest, size, now)
		if err != nil {
			return out, err
		}
		onServer := true
		out = append(out, ReceiptOutcome{PreparationID: id, ReceiptID: receiptID, Outcome: OutcomeIssued, Receipt: receipt, TitleOnServer: &onServer})
	}
	if e = operations.SaveReceipt(tx, scope, operationID, digest, out, now); e != nil {
		return out, e
	}
	return out, gated2.Commit()
}

func (s *Service) storeReceipt(ctx context.Context, tx *sql.Tx, p identity.Principal, serverID, keyID string, secret ed25519.PrivateKey, preparation, item, library, quality, artifactDigest string, size, now int64) (string, *Receipt, error) {
	id := identity.Digest(identity.Token())
	expires := now + int64(ReceiptTTL/time.Millisecond)
	var sequence int64
	if _, e := tx.ExecContext(ctx, `UPDATE download_receipt_sequence SET value=value+1 WHERE singleton=1`); e != nil {
		return "", nil, e
	}
	if e := tx.QueryRowContext(ctx, `SELECT value FROM download_receipt_sequence WHERE singleton=1`).Scan(&sequence); e != nil {
		return "", nil, e
	}
	entity, e := entityid.Resolve(ctx, tx, item)
	if e != nil {
		return "", nil, e
	}
	doc, e := publicationTx(ctx, tx, preparation)
	if e != nil {
		return "", nil, e
	}
	publicationRequired := 0
	if doc != nil {
		publicationRequired = 1
	}
	if _, e := tx.ExecContext(ctx, `INSERT INTO download_receipts(id,profile_key,authority,account_id,profile_id,item_id,item_public,library_id,preparation_id,quality,artifact_digest,artifact_bytes,key_id,issued_ms,expires_ms,sequence,publication_required) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, operations.ViewerKey(p), p.Authority, p.AccountID, p.ProfileID, entity, item, library, preparation, quality, artifactDigest, size, keyID, now, expires, sequence, publicationRequired); e != nil {
		return "", nil, e
	}
	claims := ReceiptClaims{
		Kind: ReceiptKind, Version: ReceiptVersion, ReceiptID: id, KeyID: keyID,
		Viewer:        ReceiptViewer{Authority: p.Authority, AccountID: p.AccountID, ProfileID: p.ProfileID, ServerID: serverID},
		ItemID:        item,
		PreparationID: preparation,
		Quality:       quality,
		QualityLabel:  qualityLabel(quality),
		Artifact:      ReceiptArtifact{SHA256: artifactDigest, Bytes: size},
		IssuedAt:      stamp(now), ExpiresAt: stamp(expires),
	}
	if doc != nil {
		claims.Publication = &ReceiptPublication{doc.SourceID, doc.Revision, doc.Format, doc.Digest()}
	}
	receipt, e := sign(keyID, secret, claims, 1)
	if e != nil {
		return "", nil, e
	}
	return id, &receipt, nil
}

// Revalidate renews receipts a client presents when it regains contact.
//
// A receipt answers to one question: may this viewer still play this title? It
// is refused, and withdrawn, when the account was switched off, the profile
// lost downloads, or the viewer may no longer see the title or its library.
// It is renewed in every other case, and that deliberately includes the ones
// where the server itself has tidied up: the claim ran out its retention, the
// file was replaced, the converted copy was deleted, the title or its whole
// library was removed. None of those is the owner taking anything away from
// the viewer, so none of them reaches a copy already on a device. A title that
// is gone is reported with titleOnServer false.
//
// The server answers from its own rows rather than from the envelope the client
// sent, because the envelope is exactly what a stale client would be holding.
func (s *Service) Revalidate(ctx context.Context, p identity.Principal, serverID, operationID string, receiptIDs []string, guard LibraryGuard) ([]ReceiptOutcome, error) {
	out := []ReceiptOutcome{}
	if !validID.MatchString(operationID) || len(receiptIDs) == 0 || len(receiptIDs) > MaxBatchTargets {
		return out, ErrInput
	}
	for _, id := range receiptIDs {
		if !validID.MatchString(id) {
			return out, ErrInput
		}
	}
	gated3, e := dbwork.Begin(ctx, s.db, dbwork.ClassForegroundTransfer)
	if e != nil {
		return out, e
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	now := s.millis()
	key := operations.ViewerKey(p)
	scope := "downloads-revalidate:" + key
	raw, digest, e := operations.Receipt(tx, scope, operationID, []any{receiptIDs}, now)
	if e != nil {
		return out, e
	}
	allowed, e := ProfileAllowsDownloads(tx, p.Viewer)
	if e != nil {
		return out, e
	}
	if raw != "" {
		// A replayed request is not a second chance at a renewal the profile
		// has since lost the right to.
		if !allowed {
			return out, ErrPolicy
		}
		if e = json.Unmarshal([]byte(raw), &out); e != nil {
			return out, e
		}
		if e = s.checkReceiptReplay(ctx, tx, p, out, guard, now); e != nil {
			return out, e
		}
		return out, gated3.Commit()
	}
	disabled, e := AccountDisabled(tx, p.Viewer)
	if e != nil {
		return out, e
	}
	keyID, secret, e := signingKey(tx, now)
	if e != nil {
		return out, e
	}
	for _, id := range receiptIDs {
		var item, library, preparation, quality, artifactDigest string
		var size, revoked, revision int64
		var publicationRequired bool
		err := tx.QueryRowContext(ctx, `SELECT item_public,library_id,preparation_id,quality,artifact_digest,artifact_bytes,revoked_ms,revision,publication_required FROM download_receipts WHERE id=? AND profile_key=?`, id, key).Scan(&item, &library, &preparation, &quality, &artifactDigest, &size, &revoked, &revision, &publicationRequired)
		if errors.Is(err, sql.ErrNoRows) {
			out = append(out, ReceiptOutcome{ReceiptID: id, Outcome: OutcomeRefused, Code: ReasonUnknownReceipt})
			continue
		}
		if err != nil {
			return out, err
		}
		if revoked > 0 {
			out = append(out, ReceiptOutcome{ReceiptID: id, Outcome: OutcomeRefused, Code: ReasonRevoked})
			continue
		}
		refuse := ""
		switch {
		case disabled:
			refuse = ReasonAccountDisabled
		case !allowed:
			refuse = ReasonNotAllowed
		}
		onServer := false
		if refuse == "" {
			var current sql.NullString
			if err = tx.QueryRowContext(ctx, `SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?)`, item).Scan(&current); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return out, err
			}
			if onServer = err == nil; onServer {
				// A title that moved to another library answers to that one.
				library = current.String
				// Renewal never withdraws a copy because a catalogue edit is
				// still publishing: that answers retry. Only a published refusal
				// does.
				if err = s.visibleItem(ctx, tx, p, item); errors.Is(err, identity.ErrContentRestricted) {
					refuse = ReasonItemDeleted
				} else if err != nil {
					return out, err
				}
			}
		}
		if refuse == "" && library != "" {
			// The title's library is still on this server (or the title is):
			// the viewer must still be let into it. A library that is gone
			// altogether took its titles with it, which is the server tidying,
			// not access being withdrawn.
			var exists bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM libraries WHERE id=?)`, library).Scan(&exists); err != nil {
				return out, err
			}
			if exists {
				if err = s.checkLibrary(ctx, tx, p, library); err == nil && guard != nil {
					err = guard(tx, library)
				}
				if permissionDenied(err) {
					refuse = ReasonItemDeleted
				} else if err != nil {
					return out, err
				}
			}
		}
		if refuse != "" {
			if err = revokeReceipt(tx, id, refuse, now); err != nil {
				return out, err
			}
			out = append(out, ReceiptOutcome{ReceiptID: id, Outcome: OutcomeRefused, Code: refuse})
			continue
		}
		doc, err := publicationTx(ctx, tx, preparation)
		if errors.Is(err, sql.ErrNoRows) {
			// The receipt records whether its signed claims need a publication,
			// even after both the preparation and catalogue title are gone.
			if publicationRequired {
				err = ErrUnavailable
			} else {
				doc, err = nil, nil
			}
		}
		if errors.Is(err, ErrUnavailable) {
			if err = revokeReceipt(tx, id, ReasonRevoked, now); err != nil {
				return out, err
			}
			out = append(out, ReceiptOutcome{ReceiptID: id, Outcome: OutcomeRefused, Code: ReasonRevoked})
			continue
		}
		if err != nil {
			return out, err
		}
		expires := now + int64(ReceiptTTL/time.Millisecond)
		if _, err = tx.ExecContext(ctx, `UPDATE download_receipts SET issued_ms=?,expires_ms=?,key_id=?,library_id=?,revision=revision+1 WHERE id=?`, now, expires, keyID, library, id); err != nil {
			return out, err
		}
		claims := ReceiptClaims{
			Kind: ReceiptKind, Version: ReceiptVersion, ReceiptID: id, KeyID: keyID,
			Viewer:        ReceiptViewer{Authority: p.Authority, AccountID: p.AccountID, ProfileID: p.ProfileID, ServerID: serverID},
			ItemID:        item,
			PreparationID: preparation,
			Quality:       quality,
			QualityLabel:  qualityLabel(quality),
			Artifact:      ReceiptArtifact{SHA256: artifactDigest, Bytes: size},
			IssuedAt:      stamp(now), ExpiresAt: stamp(expires),
		}
		if doc != nil {
			claims.Publication = &ReceiptPublication{doc.SourceID, doc.Revision, doc.Format, doc.Digest()}
		}
		renewed, err := sign(keyID, secret, claims, revision+1)
		if err != nil {
			return out, err
		}
		present := onServer
		outcome := ReceiptOutcome{ReceiptID: id, PreparationID: preparation, Outcome: OutcomeRenewed, Receipt: &renewed, TitleOnServer: &present}
		if onServer {
			entity, err := entityid.Resolve(ctx, tx, item)
			if err != nil {
				return out, err
			}
			position, watched, err := readCurrentProgress(ctx, tx, identity.PersonalKey(p.Viewer), entity)
			if err != nil {
				return out, err
			}
			outcome.Viewing = &ReceiptViewing{PositionSeconds: position, Watched: watched}
		}
		out = append(out, outcome)
	}
	if e = operations.SaveReceipt(tx, scope, operationID, digest, out, now); e != nil {
		return out, e
	}
	return out, gated3.Commit()
}

// Revoke withdraws offline authorization. A viewer may revoke their own
// receipts — the action behind "remove this download from my other device" —
// and an owner may revoke any.
func (s *Service) Revoke(ctx context.Context, p identity.Principal, operationID, reason string, receiptIDs []string, anyViewer bool) ([]ReceiptOutcome, error) {
	out := []ReceiptOutcome{}
	if !validID.MatchString(operationID) || len(receiptIDs) == 0 || len(receiptIDs) > MaxBatchTargets {
		return out, ErrInput
	}
	if reason == "" {
		reason = ReasonRevoked
	}
	// The reason is published back to clients, which accept only the reasons
	// this server documents.
	if !publishedReasons[reason] {
		return out, ErrInput
	}
	gated4, e := dbwork.Begin(ctx, s.db, dbwork.ClassForegroundTransfer)
	if e != nil {
		return out, e
	}
	tx := gated4.Tx()
	defer gated4.Rollback()
	now := s.millis()
	scope := "downloads-revoke:" + operations.ViewerKey(p)
	raw, digest, e := operations.Receipt(tx, scope, operationID, []any{receiptIDs, reason}, now)
	if e != nil {
		return out, e
	}
	if raw != "" {
		return out, json.Unmarshal([]byte(raw), &out)
	}
	for _, id := range receiptIDs {
		if !validID.MatchString(id) {
			return out, ErrInput
		}
		query := `SELECT id FROM download_receipts WHERE id=? AND profile_key=?`
		args := []any{id, operations.ViewerKey(p)}
		if anyViewer {
			query, args = `SELECT id FROM download_receipts WHERE id=?`, []any{id}
		}
		var found string
		err := tx.QueryRowContext(ctx, query, args...).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			out = append(out, ReceiptOutcome{ReceiptID: id, Outcome: OutcomeRefused, Code: ReasonUnknownReceipt})
			continue
		}
		if err != nil {
			return out, err
		}
		if err = revokeReceipt(tx, id, reason, now); err != nil {
			return out, err
		}
		out = append(out, ReceiptOutcome{ReceiptID: id, Outcome: OutcomeRefused, Code: ReasonRevoked})
	}
	if e = operations.Audit(tx, now, operations.AccountKey(p), "downloads.revoke", reason, int64(len(out))); e != nil {
		return out, e
	}
	if e = operations.SaveReceipt(tx, scope, operationID, digest, out, now); e != nil {
		return out, e
	}
	return out, gated4.Commit()
}

func revokeReceipt(tx *sql.Tx, id, reason string, now int64) error {
	var sequence int64
	if _, e := tx.Exec(`UPDATE download_receipt_sequence SET value=value+1 WHERE singleton=1`); e != nil {
		return e
	}
	if e := tx.QueryRow(`SELECT value FROM download_receipt_sequence WHERE singleton=1`).Scan(&sequence); e != nil {
		return e
	}
	_, e := tx.Exec(`UPDATE download_receipts SET revoked_ms=?,revoked_reason=?,sequence=?,revision=revision+1 WHERE id=? AND revoked_ms=0`, now, reason, sequence, id)
	return e
}

// Revocations pages the revocation list. A client that was offline polls from
// the sequence it last saw; the list is server-wide for an owner and scoped to
// the viewer otherwise, because a revocation names a viewer's own receipt.
func (s *Service) Revocations(ctx context.Context, p identity.Principal, cursor string, limit int, anyViewer bool) (RevocationPage, error) {
	out := RevocationPage{Items: []Revocation{}, AsOf: stamp(s.millis())}
	if limit < 1 || limit > 500 {
		limit = 200
	}
	after := int64(0)
	if cursor != "" {
		n, e := strconv.ParseInt(cursor, 10, 64)
		if e != nil || n < 0 {
			return out, ErrInput
		}
		after = n
	}
	gated5, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return out, e
	}
	tx := gated5.Tx()
	defer gated5.Rollback()
	query := `SELECT id,item_public,profile_id,revoked_reason,revoked_ms,sequence FROM download_receipts WHERE revoked_ms>0 AND sequence>?`
	args := []any{after}
	if !anyViewer {
		query += ` AND profile_key=?`
		args = append(args, operations.ViewerKey(p))
	}
	query += ` ORDER BY sequence LIMIT ?`
	args = append(args, limit+1)
	rows, e := tx.QueryContext(ctx, query, args...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var entry Revocation
		var revoked int64
		if e = rows.Scan(&entry.ReceiptID, &entry.ItemID, &entry.ProfileID, &entry.Reason, &revoked, &entry.Sequence); e != nil {
			rows.Close()
			return out, e
		}
		entry.RevokedAt = stamp(revoked)
		out.Items = append(out.Items, entry)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = strconv.FormatInt(out.Items[limit-1].Sequence, 10)
	}
	return out, gated5.Commit()
}

// Cached signatures never bypass the current receipt and access rows.
func (s *Service) checkReceiptReplay(ctx context.Context, tx *sql.Tx, p identity.Principal, out []ReceiptOutcome, guard LibraryGuard, now int64) error {
	for i := range out {
		entry := &out[i]
		if entry.Receipt == nil {
			continue
		}
		var item, library string
		var revoked int64
		err := tx.QueryRowContext(ctx, `SELECT item_public,library_id,revoked_ms FROM download_receipts WHERE id=? AND profile_key=?`, entry.ReceiptID, operations.ViewerKey(p)).Scan(&item, &library, &revoked)
		code := ""
		if errors.Is(err, sql.ErrNoRows) {
			code = ReasonUnknownReceipt
		} else if err != nil {
			return err
		} else if revoked != 0 {
			code = ReasonRevoked
		}
		if code == "" {
			var present bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entities WHERE public_id=pid_blob(?))`, item).Scan(&present); err != nil {
				return err
			}
			if present {
				err = s.visibleItem(ctx, tx, p, item)
			} else {
				var exists bool
				if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM libraries WHERE id=?)`, library).Scan(&exists); err != nil {
					return err
				}
				if exists {
					err = s.checkLibrary(ctx, tx, p, library)
				}
			}
			if err == nil && guard != nil {
				var current string
				e := tx.QueryRowContext(ctx, `SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?)`, item).Scan(&current)
				if e == nil {
					library = current
				} else if !errors.Is(e, sql.ErrNoRows) {
					return e
				}
				var exists bool
				if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM libraries WHERE id=?)`, library).Scan(&exists); e != nil {
					return e
				}
				if exists {
					err = guard(tx, library)
				}
			}
			if permissionDenied(err) {
				code = ReasonItemDeleted
			} else if err != nil {
				return err
			}
		}
		if code != "" {
			if code != ReasonUnknownReceipt && revoked == 0 {
				if err = revokeReceipt(tx, entry.ReceiptID, code, now); err != nil {
					return err
				}
			}
			*entry = ReceiptOutcome{PreparationID: entry.PreparationID, ReceiptID: entry.ReceiptID, Outcome: OutcomeRefused, Code: code}
		}
	}
	return nil
}

func permissionDenied(err error) bool {
	return errors.Is(err, identity.ErrContentRestricted) || errors.Is(err, identity.ErrNotVisible) || errors.Is(err, identity.ErrUnauthorized) || errors.Is(err, identity.ErrForbidden)
}
