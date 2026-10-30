package networking

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"log"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// TLSKeyFileName is the TLS private key's plain PEM file in the state folder.
// Folder permissions are the protection, as in Plex.
const TLSKeyFileName = "networking-tls.pem"

// tlsSealFileName is the removed seal key. It is only ever read once more, to
// migrate sealed material rows, and then deleted.
const tlsSealFileName = "networking-tls-seal.key"

// ensureTLSKey loads the server TLS key, generating and storing it (0600) on
// first use. When the old seal file is still around, the live sealed key is
// adopted for continuity and sealed rows move to keyless material.
// Called only during certificate-manager construction before backup admission.
func ensureTLSKey(ctx context.Context, db *sql.DB, stateDir string) (*ecdsa.PrivateKey, error) {
	path := storagepaths.Path(stateDir, TLSKeyFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if key, err := loadTLSKey(path); err == nil {
		migrateCertificateKeys(ctx, db, stateDir)
		return key, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	if adopted := adoptSealedActiveKey(ctx, db, stateDir); adopted != nil {
		key = adopted
	}
	if err = writeTLSKey(path, key); err != nil {
		// Another start won the race, or the file is already there: use it.
		if loaded, lerr := loadTLSKey(path); lerr == nil {
			migrateCertificateKeys(ctx, db, stateDir)
			return loaded, nil
		}
		return nil, err
	}
	migrateCertificateKeys(ctx, db, stateDir)
	return key, nil
}

// loadTLSKey parses the PEM key. Loose permissions never refuse: the state
// permissions warning covers them, and refusing would strand remote access.
func loadTLSKey(path string) (*ecdsa.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, errCertificateMaterial
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errCertificateMaterial
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, errCertificateMaterial
	}
	return key, nil
}

func writeTLSKey(path string, key *ecdsa.PrivateKey) error {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	defer clear(der)
	encoded := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = file.Write(encoded); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

// adoptSealedActiveKey returns the live sealed key when every active order
// shares exactly one, so the upgrade keeps serving it. Anything else (none,
// several, unreadable) takes the fresh key and reissues.
func adoptSealedActiveKey(ctx context.Context, db *sql.DB, stateDir string) *ecdsa.PrivateKey {
	aead, err := loadTLSSeal(stateDir)
	if err != nil || aead == nil {
		return nil
	}
	type active struct{ scope, request string }
	var actives []active
	rows, err := db.QueryContext(ctx, `SELECT scope_id,active_id FROM networking_certificate_state WHERE active_id<>''`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var a active
		if err = rows.Scan(&a.scope, &a.request); err != nil {
			return nil
		}
		actives = append(actives, a)
	}
	if err = rows.Err(); err != nil || len(actives) == 0 {
		return nil
	}
	var first []byte
	for _, a := range actives {
		key, err := openSealedMaterialKey(ctx, db, aead, a.scope, a.request)
		if err != nil {
			return nil
		}
		pub, err := x509.MarshalPKIXPublicKey(key.Public())
		if err != nil {
			return nil
		}
		if first == nil {
			first = pub
		} else if !bytes.Equal(first, pub) {
			return nil
		}
	}
	if first == nil {
		return nil
	}
	// Reopen the first active key to return it.
	key, err := openSealedMaterialKey(ctx, db, aead, actives[0].scope, actives[0].request)
	if err != nil {
		return nil
	}
	return key
}

// migrateCertificateKeys moves sealed material rows to the keyless format:
// kept rows (active or previous) keep only their chain and CSR, orphaned
// material is deleted, and dangling pending references are cleared so fresh
// orders issue under the file key. Only rows matching the file key validate
// afterwards; anything else retires through the ordinary invalid-material
// path. The seal file is removed; the migration is idempotent.
func migrateCertificateKeys(ctx context.Context, db *sql.DB, stateDir string) {
	aead, err := loadTLSSeal(stateDir)
	if err != nil || aead == nil {
		return
	}
	gated, err := dbwork.Begin(ctx, db, dbwork.ClassMaintenance)
	if err != nil {
		log.Printf("Sealed certificate migration deferred: %v", err)
		return
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if err = migrateCertificateKeysTx(ctx, tx); err != nil {
		log.Printf("Sealed certificate migration deferred: %v", err)
		return
	}
	if err = gated.Commit(); err != nil {
		return
	}
	// Best effort: a leftover file is inert and migrates again next start.
	_ = os.Remove(storagepaths.Path(stateDir, tlsSealFileName))
}

func migrateCertificateKeysTx(ctx context.Context, tx *sql.Tx) error {
	var states []struct{ scope, active, previous, pending string }
	rows, err := tx.QueryContext(ctx, `SELECT scope_id,active_id,previous_id,pending_id FROM networking_certificate_state`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var s struct{ scope, active, previous, pending string }
		if err = rows.Scan(&s.scope, &s.active, &s.previous, &s.pending); err != nil {
			rows.Close()
			return err
		}
		states = append(states, s)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	kept := map[string]bool{}
	for _, s := range states {
		kept[s.scope+"\x00"+s.active] = s.active != ""
		kept[s.scope+"\x00"+s.previous] = s.previous != ""
	}
	mats, err := tx.QueryContext(ctx, `SELECT scope_id,request_id FROM networking_certificates`)
	if err != nil {
		return err
	}
	defer mats.Close()
	type material struct{ scope, request string }
	var pending []material
	for mats.Next() {
		var m material
		if err = mats.Scan(&m.scope, &m.request); err != nil {
			return err
		}
		pending = append(pending, m)
	}
	if err = mats.Err(); err != nil {
		return err
	}
	for _, m := range pending {
		if !kept[m.scope+"\x00"+m.request] {
			if _, err = tx.ExecContext(ctx, `DELETE FROM networking_certificates WHERE scope_id=? AND request_id=?`, m.scope, m.request); err != nil {
				return err
			}
			continue
		}
		if _, err = tx.ExecContext(ctx, `UPDATE networking_certificates SET key_nonce=?,key_cipher=? WHERE scope_id=? AND request_id=?`, []byte{}, []byte{}, m.scope, m.request); err != nil {
			return err
		}
	}
	for _, s := range states {
		if s.pending == "" {
			continue
		}
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM networking_certificates WHERE scope_id=? AND request_id=?`, s.scope, s.pending).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if _, err = tx.ExecContext(ctx, `UPDATE networking_certificate_state SET pending_id='',next_attempt=0 WHERE scope_id=?`, s.scope); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

// loadTLSSeal reads the removed seal key for the one migration that needs it.
func loadTLSSeal(stateDir string) (cipher.AEAD, error) {
	raw, err := os.ReadFile(storagepaths.Path(stateDir, tlsSealFileName))
	if err != nil || len(raw) != 32 {
		return nil, errCertificateMaterial
	}
	defer clear(raw)
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, errCertificateMaterial
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errCertificateMaterial
	}
	return aead, nil
}

// openSealedMaterialKey decrypts one material row's key with the old seal.
func openSealedMaterialKey(ctx context.Context, db *sql.DB, aead cipher.AEAD, scope, request string) (*ecdsa.PrivateKey, error) {
	var nonce, sealed []byte
	if err := db.QueryRowContext(ctx, `SELECT key_nonce,key_cipher FROM networking_certificates WHERE scope_id=? AND request_id=?`, scope, request).Scan(&nonce, &sealed); err != nil {
		return nil, err
	}
	der, err := aead.Open(nil, nonce, sealed, certificateAAD(certificateScope{ID: scope}, request))
	if err != nil {
		return nil, err
	}
	defer clear(der)
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, errCertificateMaterial
	}
	return key, nil
}
