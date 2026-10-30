package networking

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// The §7.3 demo-state copy check for the networking half of §6 plain state
// (O3, be/backups). A non-release server never opens the claim control, so
// starting it on a copy doesn't run these two migrations; this runs them
// against a copy of a real state folder, with no network activity. Skipped
// unless PORTICO_STATE_COPY names such a copy (never the live folder).
func TestPlainStateMigrationsOnAStateCopy(t *testing.T) {
	dir := os.Getenv("PORTICO_STATE_COPY")
	if dir == "" {
		t.Skip("set PORTICO_STATE_COPY to a copy of a state folder")
	}
	db, err := persistence.Open(filepath.Join(dir, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	var sealedBefore, activeBefore int
	_ = db.QueryRow(`SELECT count(*) FROM networking_claim_credentials WHERE length(nonce)>0`).Scan(&sealedBefore)
	_ = db.QueryRow(`SELECT count(*) FROM networking_certificate_state WHERE active_id<>''`).Scan(&activeBefore)
	t.Logf("before: %d sealed claim credentials, %d active certificates", sealedBefore, activeBefore)
	if _, err = OpenClaimCipher(ctx, db, dir); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(storagepaths.Path(dir, "networking-claim-credentials.key")); !os.IsNotExist(err) {
		t.Fatalf("the claim credential key file is still there: %v", err)
	}
	var sealed int
	if err = db.QueryRow(`SELECT count(*) FROM networking_claim_credentials WHERE length(nonce)>0`).Scan(&sealed); err != nil || sealed != 0 {
		t.Fatalf("%d claim credentials still sealed (%v)", sealed, err)
	}
	var plain int
	if err = db.QueryRow(`SELECT count(*) FROM networking_claim_credentials WHERE length(nonce)=0 AND length(ciphertext)>0`).Scan(&plain); err != nil {
		t.Fatal(err)
	}
	if plain != sealedBefore {
		t.Fatalf("%d plain credentials after, %d sealed before", plain, sealedBefore)
	}
	key, err := ensureTLSKey(ctx, db, dir)
	if err != nil || key == nil {
		t.Fatalf("TLS key: %v", err)
	}
	if _, err = os.Stat(storagepaths.Path(dir, TLSKeyFileName)); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(storagepaths.Path(dir, tlsSealFileName)); !os.IsNotExist(err) {
		t.Fatalf("the TLS seal file is still there: %v", err)
	}
	var sealedMaterial int
	if err = db.QueryRow(`SELECT count(*) FROM networking_certificates WHERE length(key_cipher)>0`).Scan(&sealedMaterial); err != nil || sealedMaterial != 0 {
		t.Fatalf("%d certificate rows still sealed (%v)", sealedMaterial, err)
	}
	var activeAfter int
	_ = db.QueryRow(`SELECT count(*) FROM networking_certificate_state WHERE active_id<>''`).Scan(&activeAfter)
	// Continuity: when one live certificate was active, its key is the one
	// adopted into the PEM file, so remote access keeps working without a
	// reissue.
	rows, err := db.Query(`SELECT c.chain FROM networking_certificates c JOIN networking_certificate_state s ON s.scope_id=c.scope_id AND s.active_id=c.request_id WHERE length(c.chain)>0`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var chain []byte
		if err = rows.Scan(&chain); err != nil {
			t.Fatal(err)
		}
		block, _ := pem.Decode(chain)
		if block == nil {
			t.Fatal("active chain is not PEM")
		}
		leaf, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := x509.MarshalPKIXPublicKey(leaf.PublicKey)
		got, _ := x509.MarshalPKIXPublicKey(key.Public())
		if !bytes.Equal(want, got) {
			t.Errorf("the active certificate (expires %s) doesn't match the adopted TLS key: remote access would need a reissue", leaf.NotAfter)
		} else {
			t.Logf("the active certificate (expires %s) matches the adopted TLS key", leaf.NotAfter)
		}
	}
	t.Logf("after: %d plain claim credentials, %d active certificates, TLS key in %s", plain, activeAfter, TLSKeyFileName)
}
