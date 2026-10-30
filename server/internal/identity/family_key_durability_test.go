package identity

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// The setup-hash HMAC key lives in the configuration table, not in a key
// file. Hashing that is not encryption stays; there is nothing to lose.
func TestSetupHashKeyStoredInConfiguration(t *testing.T) {
	dir := t.TempDir()
	db, err := persistence.Open(filepath.Join(dir, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := New(db, dir)
	if err != nil {
		t.Fatal(err)
	}
	stored := persistence.Get(db, "setup_hash_key")
	if len(stored) != 64 {
		t.Fatalf("setup hash key not stored: %q", stored)
	}
	if len(s.setupHashKey) != 32 {
		t.Fatal("setup hash key not loaded")
	}
	if _, e := os.Stat(storagepaths.Path(dir, "authorization-replay.key")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("key file created")
	}
	// A restart keeps the same key: code hashes stay stable.
	again, err := New(db, dir)
	if err != nil {
		t.Fatal(err)
	}
	if persistence.Get(db, "setup_hash_key") != stored || string(again.setupHashKey) != string(s.setupHashKey) {
		t.Fatal("setup hash key changed across restart")
	}
}

// Rows sealed under the old file key migrate to plaintext on first start,
// and the key file goes away. TOTP secrets are durable: without this pass
// every second factor would break at upgrade.
func TestSealedFactorsMigrateToPlain(t *testing.T) {
	dir := t.TempDir()
	db, err := persistence.Open(filepath.Join(dir, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('account','owner',X'00','profile',1)`); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal("TOPSECRET")
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	sealed := base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, raw, []byte("mfa-secret:account")))
	if _, err = db.Exec(`INSERT INTO identity_account_factors(account_id,secret,confirmed,last_step,enrolled_at) VALUES('account',?,0,0,'2026-01-01T00:00:00Z')`, sealed); err != nil {
		t.Fatal(err)
	}
	if err = writePathFixture(storagepaths.Path(dir, "authorization-replay.key"), key, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New(db, dir)
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	if err = db.QueryRow(`SELECT secret FROM identity_account_factors WHERE account_id='account'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	secret, err := s.openFactorSecret("account", stored)
	if err != nil || secret != "TOPSECRET" {
		t.Fatalf("migrated factor unreadable: %q %v", secret, err)
	}
	if _, err = os.Stat(storagepaths.Path(dir, "authorization-replay.key")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("key file left behind")
	}
}
