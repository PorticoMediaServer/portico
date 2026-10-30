package networking

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type endpointTestTransport struct {
	Transport
	call func(context.Context, Intent, any, any) error
}

func (t endpointTestTransport) CallServer(ctx context.Context, v Intent, op ServerOperation, in, out any) error {
	if op != RegisterEndpoint {
		return ErrInvalid
	}
	return t.call(ctx, v, in, out)
}
func TestClaimEndpointPendingProofAndOwnerFence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, mode := range []string{"success", "wrong-origin", "revoked-owner", "stale-claim", "quiesced"} {
		t.Run(mode, func(t *testing.T) {
			f, h, tr := handlerFixture(t)
			gate, runner := lifecycleRunner(t)
			h.runner = runner
			status := handlerApprove(t, f, h, handlerPrepare(t, f, h))
			for range 3 {
				status, _ = handlerCall(t, h, "POST", "/v1/networking/claim/continue", struct {
					Expected ClaimExpected `json:"expected"`
				}{*status.Operation})
			}
			if !status.InstallationAcknowledged {
				t.Fatal("not installed")
			}
			var incarnation string
			if err := f.db.QueryRow(`SELECT key_incarnation FROM networking_server_identities`).Scan(&incarnation); err != nil {
				t.Fatal(err)
			}
			keyDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(keyDir, incarnation+".ed25519"), f.localKey, 0600); err != nil {
				t.Fatal(err)
			}
			keyRoot, err := os.OpenRoot(keyDir)
			if err != nil {
				t.Fatal(err)
			}
			defer keyRoot.Close()
			h.signer = &ProtectedKeys{db: f.db, root: keyRoot}
			h.transport = endpointTestTransport{tr, func(ctx context.Context, v Intent, in, out any) error {
				encoded, _ := json.Marshal(in)
				var request struct {
					BaseURL        string `json:"baseUrl"`
					RegistrationID string `json:"registrationId"`
				}
				_ = json.Unmarshal(encoded, &request)
				q := endpointChallenge{"portico.server.endpoint.v1", v.ServerID, v.AccountID, v.OperationID, v.ClaimGeneration, v.CredentialGeneration, request.BaseURL, request.RegistrationID, "fresh_nonce", time.Now().UTC().Add(30 * time.Second)}
				switch mode {
				case "wrong-origin":
					q.BaseURL = "https://other.example.com"
				case "revoked-owner":
					_, _ = f.db.Exec(`UPDATE request_family SET active=0`)
				case "stale-claim":
					q.CredentialGeneration = "999"
				case "quiesced":
					gate.BeginQuiesce()
				}
				raw, _ := json.Marshal(q)
				r := httptest.NewRequest("POST", "/v1/networking/endpoint-proof", bytes.NewReader(raw))
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				h.endpointProof(w, r)
				if mode != "success" {
					if w.Code == 200 {
						t.Fatal("invalid proof accepted")
					}
					return ErrStale
				}
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
				var proof struct {
					Payload   string `json:"payload"`
					Signature string `json:"signature"`
				}
				_ = json.Unmarshal(w.Body.Bytes(), &proof)
				sig, _ := base64.RawURLEncoding.DecodeString(proof.Signature)
				if proof.Payload != base64.RawURLEncoding.EncodeToString(raw) || !ed25519.Verify(f.localKey.Public().(ed25519.PublicKey), raw, sig) {
					t.Fatal("invalid proof")
				}
				*out.(*EndpointReceipt) = EndpointReceipt{request.BaseURL, time.Now().UTC()}
				return nil
			}}
			body, _ := json.Marshal(struct {
				Expected ClaimExpected `json:"expected"`
				BaseURL  string        `json:"baseUrl"`
			}{*status.Operation, "https://demo.example.com"})
			r := httptest.NewRequest("POST", "/v1/networking/claim/endpoint", bytes.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", "https://web.example")
			r.Header.Set("Authorization", "Bearer current")
			w := httptest.NewRecorder()
			h.endpointRegister(w, r)
			if (w.Code == http.StatusOK) != (mode == "success") {
				t.Fatal(w.Code, w.Body.String())
			}
			if len(h.endpoints.pending) != 0 {
				t.Fatal("pending capability retained")
			}
		})
	}
}
func TestClaimEndpointProofWithoutPendingDenied(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	_, h, _ := handlerFixture(t)
	q := endpointChallenge{Kind: "portico.server.endpoint.v1", RegistrationID: "unknown", ExpiresAt: time.Now().UTC().Add(time.Second)}
	raw, _ := json.Marshal(q)
	r := httptest.NewRequest("POST", "/v1/networking/endpoint-proof", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.endpointProof(w, r)
	if w.Code == 200 {
		t.Fatal("unprompted signer")
	}
}
