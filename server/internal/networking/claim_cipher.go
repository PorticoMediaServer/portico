package networking

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

// ClaimCipher stores installed outbound-claim credentials as plain columns.
// Folder permissions are the protection, as in Plex. The type stays so the
// store's shape does not churn; it holds no key.
type ClaimCipher struct{}

// OpenClaimCipher opens the plaintext credential store, migrating any rows
// the old file key sealed. It never refuses: there is no key to lose.
func OpenClaimCipher(ctx context.Context, db *sql.DB, stateDir string) (*ClaimCipher, error) {
	if db == nil || stateDir == "" {
		return nil, ErrInvalid
	}
	if err := migrateClaimCredentials(ctx, db, stateDir); err != nil {
		log.Printf("Sealed claim migration deferred: %v", err)
	}
	return &ClaimCipher{}, nil
}

type credentialAAD struct {
	Kind                 string `json:"kind"`
	OperationID          string `json:"operationId"`
	ServerID             string `json:"serverId"`
	AccountID            string `json:"accountId"`
	PublicKey            string `json:"publicKey"`
	LocalGeneration      int64  `json:"localGeneration,string"`
	ClaimGeneration      string `json:"claimGeneration"`
	CredentialGeneration string `json:"credentialGeneration"`
}

func aad(v Intent) ([]byte, error) {
	if e := validateIntent(v, v.OperationID); e != nil {
		return nil, e
	}
	if v.ClaimGeneration == "" {
		return nil, ErrInvalid
	}
	return json.Marshal(credentialAAD{Kind: "portico.networking.claim-credential.v1", OperationID: v.OperationID, ServerID: v.ServerID, AccountID: v.AccountID, PublicKey: base64.RawURLEncoding.EncodeToString(v.PublicKey), LocalGeneration: v.LocalGeneration, ClaimGeneration: v.ClaimGeneration, CredentialGeneration: v.CredentialGeneration})
}

func (c *ClaimCipher) seal(v Intent, secret *Secret) ([]byte, []byte, error) {
	if c == nil {
		return nil, nil, ErrUnavailable
	}
	if err := validateIntent(v, v.OperationID); err != nil {
		return nil, nil, err
	}
	var raw []byte
	if err := secret.Use(func(value []byte) error {
		raw = append([]byte(nil), value...)
		return nil
	}); err != nil {
		return nil, nil, err
	}
	// An empty nonce marks the plaintext format.
	return []byte{}, raw, nil
}

func (c *ClaimCipher) open(v Intent, _, encrypted []byte) (*Secret, error) {
	if c == nil {
		return nil, ErrUnavailable
	}
	if err := validateIntent(v, v.OperationID); err != nil {
		return nil, err
	}
	secret, err := NewSecret(encrypted)
	if err != nil {
		return nil, ErrUnavailable
	}
	return secret, nil
}

// migrateClaimCredentials is the one-time upgrade from the file-key era:
// sealed credential rows move to plaintext and the key file is removed.
// Anything it cannot read is left for the next start; it never blocks.
func migrateClaimCredentials(ctx context.Context, db *sql.DB, stateDir string) error {
	raw, err := os.ReadFile(storagepaths.Path(stateDir, "networking-claim-credentials.key"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || len(raw) != 32 {
		return err
	}
	defer clear(raw)
	block, err := aes.NewCipher(raw)
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	gated, err := dbwork.Begin(ctx, db, dbwork.ClassInteractive)
	if err != nil {
		return err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	var marker int
	if err = tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='networking_claim_credentials'`).Scan(&marker); err != nil || marker == 0 {
		return err
	}
	rows, err := tx.Query(`SELECT operation_id,nonce,ciphertext FROM networking_claim_credentials`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type credential struct {
		operation        string
		nonce, encrypted []byte
	}
	var pending []credential
	for rows.Next() {
		var cred credential
		if err = rows.Scan(&cred.operation, &cred.nonce, &cred.encrypted); err != nil {
			return err
		}
		pending = append(pending, cred)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for _, cred := range pending {
		if len(cred.nonce) == 0 {
			continue
		}
		stored, err := scanIntent(tx.QueryRow(`SELECT `+intentColumns+` FROM networking_claim_intents WHERE operation_id=?`, cred.operation))
		if err != nil {
			return err
		}
		additional, err := aad(stored.Intent)
		if err != nil {
			return err
		}
		plain, err := aead.Open(nil, cred.nonce, cred.encrypted, additional)
		if err != nil {
			return err
		}
		if _, err = NewSecret(plain); err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE networking_claim_credentials SET nonce=?,ciphertext=? WHERE operation_id=?`, []byte{}, plain, cred.operation); err != nil {
			return err
		}
	}
	if err = gated.Commit(); err != nil {
		return err
	}
	// Best effort: a leftover file is inert and migrates again next start.
	_ = os.Remove(storagepaths.Path(stateDir, "networking-claim-credentials.key"))
	return nil
}
