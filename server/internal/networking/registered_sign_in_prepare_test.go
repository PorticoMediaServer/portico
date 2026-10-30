package networking

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/serverreturn"
)

func TestRegisteredSignInContextBindsActualNameFreshChallengeAndInstalledRegistration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, claims, ctx, origin := managedHTTPSFixture(t)
	dir := filepath.Join(f.dir, "prepare-keys")
	if err := privateDirectory(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "key1.ed25519"), f.localKey, 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	keys := &ProtectedKeys{f.db, root}
	t.Cleanup(func() { keys.Close() })
	h := NewRouteIdentityHandler(keys, claims.runner, nil, nil)
	v, err := f.store.InstalledIntent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var generation int64
	if err = f.db.QueryRow(`SELECT generation FROM networking_remote_state`).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	claim, ce := strconv.ParseInt(v.ClaimGeneration, 10, 64)
	credential, ke := strconv.ParseInt(v.CredentialGeneration, 10, 64)
	if ce != nil || ke != nil {
		t.Fatal("invalid genuine installed fixture tuple")
	}
	q := RegisteredSignInPrepareInput{ServerID: v.ServerID, RegistrationID: serverreturn.RegistrationID(v.ServerID, v.OperationID, claim, credential, generation, origin), RegistrationOrigin: origin, HostedOrigin: "https://hosted.example", ClaimOperationID: v.OperationID, Challenge: strings.Repeat("C", 43), Nonce: base64.RawURLEncoding.EncodeToString(make([]byte, 32)), InstallationID: "d297a1dc-3adc-4b1c-a7f2-fbd4640f69e8"}
	hash := sha256.Sum256([]byte(q.Challenge))
	expires := time.Now().Add(4 * time.Minute).UnixMilli()
	name := "Actual <home> & TV\u2028Room"
	if _, err = f.db.Exec(`INSERT INTO hosted_identity_challenges(challenge_hash,installation_id,expires_ms) VALUES(?,?,?)`, base64.RawURLEncoding.EncodeToString(hash[:]), q.InstallationID, expires); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`INSERT INTO configuration(key,value) VALUES('name',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, name); err != nil {
		t.Fatal(err)
	}
	out, payload, signature, err := h.SignRegisteredSignInPrepare(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(payload)
	sig, se := base64.RawURLEncoding.Strict().DecodeString(signature)
	if err != nil || se != nil || !ed25519.Verify(f.localKey.Public().(ed25519.PublicKey), raw, sig) {
		t.Fatal("actual key did not sign context")
	}
	var parsed RegisteredSignInPrepare
	if json.Unmarshal(raw, &parsed) != nil || parsed != out || out.ServerName != name || out.ExpiresAt != time.UnixMilli(expires).UTC().Format(time.RFC3339Nano) || out.RegistrationID != q.RegistrationID || out.RegistrationOrigin != origin || out.Kind != "portico.registered-sign-in-prepare" || out.Version != "1" {
		t.Fatalf("context lost actual source facts: canonical=%v name=%v expiry=%v registration=%v origin=%v kind=%v version=%v", parsed == out, out.ServerName == name, out.ExpiresAt == time.UnixMilli(expires).UTC().Format(time.RFC3339Nano), out.RegistrationID == q.RegistrationID, out.RegistrationOrigin == origin, out.Kind == "portico.registered-sign-in-prepare", out.Version == "1")
	}
	if !strings.Contains(string(raw), `\u003c`) || !strings.Contains(string(raw), `\u2028`) {
		t.Fatal("noncanonical Go JSON")
	}
	var properties map[string]json.RawMessage
	if json.Unmarshal(raw, &properties) != nil || len(properties) != 15 {
		t.Fatal("unexpected signed context fields")
	}
	for label, change := range map[string]func(*RegisteredSignInPrepareInput){
		"different server":          func(q *RegisteredSignInPrepareInput) { q.ServerID += "other" },
		"different operation":       func(q *RegisteredSignInPrepareInput) { q.ClaimOperationID += "other" },
		"registration substitution": func(q *RegisteredSignInPrepareInput) { q.RegistrationID += "other" },
		"origin substitution":       func(q *RegisteredSignInPrepareInput) { q.RegistrationOrigin = "https://other.example" },
		"insecure origin":           func(q *RegisteredSignInPrepareInput) { q.RegistrationOrigin = "http://localhost" },
		"different installation":    func(q *RegisteredSignInPrepareInput) { q.InstallationID = strings.Repeat("i", 43) },
		"unknown challenge":         func(q *RegisteredSignInPrepareInput) { q.Challenge = strings.Repeat("D", 43) },
		"noncanonical nonce":        func(q *RegisteredSignInPrepareInput) { q.Nonce += "\n" },
	} {
		t.Run(label, func(t *testing.T) {
			next := q
			change(&next)
			if _, payload, signature, err := h.SignRegisteredSignInPrepare(ctx, next); err == nil || payload != "" || signature != "" {
				t.Fatal("invalid context signed")
			}
		})
	}
	for _, mutation := range []struct{ change, restore string }{
		{`UPDATE networking_remote_state SET generation=generation+1`, `UPDATE networking_remote_state SET generation=generation-1`},
		{`UPDATE hosted_identity_challenges SET expires_ms=1`, `UPDATE hosted_identity_challenges SET expires_ms=` + strconv.FormatInt(expires, 10)},
		{`UPDATE identity_state SET sign_in_kind='local'`, `UPDATE identity_state SET sign_in_kind='portico'`},
	} {
		if _, err = f.db.Exec(mutation.change); err != nil {
			t.Fatal(err)
		}
		if _, payload, signature, err = h.SignRegisteredSignInPrepare(ctx, q); err == nil || payload != "" || signature != "" {
			t.Fatal("stale context signed")
		}
		if _, err = f.db.Exec(mutation.restore); err != nil {
			t.Fatal(err)
		}
	}
}
