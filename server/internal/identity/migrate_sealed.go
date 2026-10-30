package identity

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// migrateSealedToPlain is the one-time upgrade from the file-key era: rows
// sealed under authorization-replay.key move to plain database columns, and
// the key file is removed. Anything it cannot read is left for the next
// start; it never blocks startup. Short-lived rows whose authentication data
// cannot be reconstructed (quick-connect create ciphers, refresh receipts,
// topshelf capabilities) are not migrated: they expire within minutes, and
// clients retry them in the clear.
func (s *Service) migrateSealedToPlain(state string) error {
	raw, err := os.ReadFile(storagepaths.Path(state, "authorization-replay.key"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || len(raw) != 32 {
		return nil
	}
	defer clear(raw)
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil
	}
	ctx := context.Background()
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if err != nil {
		return nil
	}
	tx := gated.Tx()
	defer gated.Rollback()
	// A row that doesn't open under the key never stops the others (O3 review):
	// a second factor that can't be read is kept sealed, logged, and keeps the
	// key file so a later start can try again; a short-lived row that can't be
	// read (a renewal response, a quick-connect grant, an onboarding receipt)
	// is dropped, because it expires within minutes and its client retries.
	keep := false
	for _, pass := range []struct {
		name string
		run  func(*sql.Tx, cipher.AEAD) (bool, error)
	}{
		{"factors", migrateSealedFactors},
		{"renewals", migrateSealedRenewals},
		{"quick-connect", migrateSealedQuickConnect},
		{"onboarding", migrateSealedOnboarding},
	} {
		left, err := pass.run(tx, aead)
		if err != nil {
			log.Printf("Sealed identity migration (%s) deferred: %v", pass.name, err)
			return nil
		}
		keep = keep || left
	}
	if err = gated.Commit(); err != nil {
		return nil
	}
	if keep {
		log.Printf("Some second-factor secrets couldn't be read with authorization-replay.key; the file is kept so a later start can migrate them")
		return nil
	}
	// Best effort: a leftover file is inert and migrates again next start.
	_ = os.Remove(storagepaths.Path(state, "authorization-replay.key"))
	return nil
}

func hasTable(tx *sql.Tx, name string) bool {
	var count int
	if err := tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&count); err != nil {
		return false
	}
	return count == 1
}

// openSealedBlob opens one value in the old nonce-prefixed sealSetup format.
func openSealedBlob(aead cipher.AEAD, sealed []byte, aad string, value any) error {
	n := aead.NonceSize()
	if len(sealed) < n+16 || len(sealed) > 64<<10 {
		return errors.New("identity: sealed value is not decryptable")
	}
	plain, err := aead.Open(nil, sealed[:n], sealed[n:], []byte(aad))
	if err != nil {
		return err
	}
	defer clear(plain)
	return json.Unmarshal(plain, value)
}

// plainJSON reports whether raw is already a plaintext JSON value.
func plainJSON(raw []byte) bool {
	if len(raw) == 0 || len(raw) > 64<<10 {
		return false
	}
	var probe any
	return json.Unmarshal(raw, &probe) == nil
}

// migrateSealedFactors moves TOTP secrets to plaintext. These are durable:
// without this pass every second factor breaks at upgrade. It reports whether
// a secret was left sealed (it couldn't be opened).
func migrateSealedFactors(tx *sql.Tx, aead cipher.AEAD) (bool, error) {
	if !hasTable(tx, "identity_account_factors") {
		return false, nil
	}
	rows, err := tx.Query(`SELECT account_id,secret FROM identity_account_factors`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	type factor struct{ account, stored string }
	var pending []factor
	for rows.Next() {
		var f factor
		if err = rows.Scan(&f.account, &f.stored); err != nil {
			return false, err
		}
		pending = append(pending, f)
	}
	if err = rows.Err(); err != nil {
		return false, err
	}
	rows.Close()
	left := false
	for _, f := range pending {
		decoded, err := base64.RawURLEncoding.DecodeString(f.stored)
		if err != nil {
			log.Printf("Sealed second factor of account %s is unreadable (%v); left as it is", f.account, err)
			left = true
			continue
		}
		if plainJSON(decoded) {
			continue
		}
		var secret string
		if err = openSealedBlob(aead, decoded, "mfa-secret:"+f.account, &secret); err != nil {
			log.Printf("Sealed second factor of account %s doesn't open with the key (%v); left sealed", f.account, err)
			left = true
			continue
		}
		plain, err := json.Marshal(secret)
		if err != nil {
			return false, err
		}
		if _, err = tx.Exec(`UPDATE identity_account_factors SET secret=? WHERE account_id=?`, base64.RawURLEncoding.EncodeToString(plain), f.account); err != nil {
			return false, err
		}
	}
	return left, nil
}

// migrateSealedRenewals moves outstanding renewal responses to plaintext. An
// empty nonce marks the plaintext format.
func migrateSealedRenewals(tx *sql.Tx, aead cipher.AEAD) (bool, error) {
	if !hasTable(tx, "authorization_family_renewals") {
		return false, nil
	}
	rows, err := tx.Query(`SELECT family_id,request_id,predecessor_hash,controller_id,controller_epoch,result_generation,expires_at,authorization_horizon,nonce,ciphertext FROM authorization_family_renewals`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	type renewal struct {
		family, request, prior, controller, epoch, expires, horizon string
		generation                                                  int64
		nonce, ciphertext                                           []byte
	}
	var pending []renewal
	for rows.Next() {
		var r renewal
		if err = rows.Scan(&r.family, &r.request, &r.prior, &r.controller, &r.epoch, &r.generation, &r.expires, &r.horizon, &r.nonce, &r.ciphertext); err != nil {
			return false, err
		}
		pending = append(pending, r)
	}
	if err = rows.Err(); err != nil {
		return false, err
	}
	for _, r := range pending {
		if len(r.nonce) == 0 {
			continue
		}
		plain, err := aead.Open(nil, r.nonce, r.ciphertext, renewalAAD(r.family, r.prior, RenewalBinding{ControllerID: r.controller, ControllerEpoch: r.epoch, RenewalRequestID: r.request}, r.generation, r.expires, r.horizon))
		if err != nil || len(plain) != 43 {
			// A renewal response lives minutes; one that can't be read is dropped.
			if _, err = tx.Exec(`DELETE FROM authorization_family_renewals WHERE family_id=? AND request_id=?`, r.family, r.request); err != nil {
				return false, err
			}
			continue
		}
		if _, err = tx.Exec(`UPDATE authorization_family_renewals SET nonce=?,ciphertext=? WHERE family_id=? AND request_id=?`, []byte{}, plain, r.family, r.request); err != nil {
			return false, err
		}
	}
	return false, nil
}

// migrateSealedQuickConnect moves approval and grant envelopes to plaintext.
// Create ciphers cannot be reconstructed (their request id is hashed), and
// expire within ten minutes.
func migrateSealedQuickConnect(tx *sql.Tx, aead cipher.AEAD) (bool, error) {
	if !hasTable(tx, "quick_connect_v1") {
		return false, nil
	}
	rows, err := tx.Query(`SELECT id,status,grant_cipher FROM quick_connect_v1 WHERE grant_cipher IS NOT NULL`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	type grant struct {
		id, status string
		sealed     []byte
	}
	var pending []grant
	for rows.Next() {
		var g grant
		if err = rows.Scan(&g.id, &g.status, &g.sealed); err != nil {
			return false, err
		}
		pending = append(pending, g)
	}
	if err = rows.Err(); err != nil {
		return false, err
	}
	for _, g := range pending {
		if plainJSON(g.sealed) {
			continue
		}
		var aad string
		switch g.status {
		case "approved":
			aad = "quick-approval:" + g.id
		case "consumed":
			aad = "quick-grant:" + g.id
		default:
			continue
		}
		var value any
		if g.status == "approved" {
			value = &quickApproval{}
		} else {
			value = &Envelope{}
		}
		if err = openSealedBlob(aead, g.sealed, aad, value); err != nil {
			// A quick-connect grant lives minutes; one that can't be read is dropped.
			if _, err = tx.Exec(`DELETE FROM quick_connect_v1 WHERE id=?`, g.id); err != nil {
				return false, err
			}
			continue
		}
		plain, err := json.Marshal(value)
		if err != nil {
			return false, err
		}
		if _, err = tx.Exec(`UPDATE quick_connect_v1 SET grant_cipher=? WHERE id=?`, plain, g.id); err != nil {
			return false, err
		}
	}
	return false, nil
}

// migrateSealedOnboarding moves the in-flight setup receipt to plaintext.
func migrateSealedOnboarding(tx *sql.Tx, aead cipher.AEAD) (bool, error) {
	if !hasTable(tx, "onboarding_state_v1") {
		return false, nil
	}
	var operation string
	var receipt []byte
	if err := tx.QueryRow(`SELECT operation_id,receipt FROM onboarding_state_v1 WHERE singleton=1 AND receipt IS NOT NULL`).Scan(&operation, &receipt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if plainJSON(receipt) {
		return false, nil
	}
	var out Envelope
	if err := openSealedBlob(aead, receipt, "initialize:"+operation, &out); err != nil {
		// The setup receipt only answers a replayed setup request; setup is
		// long done. One that can't be read is cleared.
		_, err = tx.Exec(`UPDATE onboarding_state_v1 SET receipt=NULL WHERE singleton=1`)
		return false, err
	}
	plain, err := json.Marshal(out)
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(`UPDATE onboarding_state_v1 SET receipt=? WHERE singleton=1`, plain)
	return false, err
}
