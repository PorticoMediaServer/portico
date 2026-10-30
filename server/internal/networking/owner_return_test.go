package networking

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/serverreturn"
)

// Real current TLS/route/credential/store/lifecycle boundaries are used here.
// Identity callbacks are explicit test trace closures; their normalized writer
// and signed admission tests live in identity/hosted, not this transport test.
func TestOwnerReturnUsesRealRegistrationAndOneConsumedInstalledExchange(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, mode := range []string{"success", "lost response", "route changes during exchange", "verification refused", "wrong registration"} {
		t.Run(mode, func(t *testing.T) {
			f, h, ctx, origin := managedHTTPSFixture(t)
			if _, err := f.db.Exec(`UPDATE networking_claim_intents SET purpose='switch',current_owner_account_id='' WHERE stage='installed'`); err != nil {
				t.Fatal(err)
			}
			phase := "installed"
			guard := func(context.Context, *sql.Tx) error {
				if phase != "installed" {
					return ErrClaimOwnerRequired
				}
				return nil
			}
			var registration serverreturn.Registration
			var generation int64
			v, err := f.store.InstalledIntent(ctx)
			if err != nil {
				t.Fatal(err)
			}
			read, err := f.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			want, generation, err := h.ownerReturnRouteTx(ctx, read, v)
			read.Rollback()
			if err != nil {
				t.Fatal(err)
			}
			resolves, exchanges, begins, verified, completed, abandoned := 0, 0, 0, 0, 0, 0
			transport := h.transport.(*HTTPTransport)
			transport.client.Transport = claimRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.Method != "POST" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || r.Header.Get("Cookie") != "" {
					t.Fatal("installed exchange credential boundary")
				}
				switch r.URL.Path {
				case "/v1/servers/" + v.ServerID + "/return-registration":
					resolves++
					var q serverreturn.RegistrationRequest
					if json.NewDecoder(r.Body).Decode(&q) != nil || q.Origin != origin {
						t.Fatal("resolver origin inferred/substituted")
					}
					raw, _ := json.Marshal(want)
					return reply(200, string(raw)), nil
				case "/v1/server-returns/exchange":
					exchanges++
					if phase != "consumed" || begins != 1 {
						t.Fatal("outbound preceded consumed attempt")
					}
					raw, _ := io.ReadAll(r.Body)
					var q serverreturn.OwnerExchange
					if json.Unmarshal(raw, &q) != nil || q.RegistrationID != registration.ID || strings.Contains(string(raw), `"code"`) {
						t.Fatal("owner/normal transport mixed")
					}
					if mode == "lost response" {
						return nil, errors.New("connection lost after request")
					}
					if mode == "route changes during exchange" {
						if _, err = f.db.Exec(`UPDATE networking_remote_state SET generation=generation+1`); err != nil {
							return nil, err
						}
					}
					return reply(200, `{"assertion":{"payload":"trace-only"}}`), nil
				default:
					t.Fatal("unexpected owner control request path")
					return nil, ErrInvalid
				}
			})
			registration, gen, err := h.BindOwnerReturn(ctx, guard, func(ctx context.Context, tx *sql.Tx, r serverreturn.Registration, g int64) (bool, error) {
				if r != want || g != generation {
					t.Fatal("not actual resolver registration")
				}
				return true, nil
			})
			if err != nil || registration != want || gen != generation || resolves != 1 {
				t.Fatal("actual registration bind", err)
			}
			q := serverreturn.OwnerExchange{TransactionID: "owner_transaction", State: strings.Repeat("s", 43), Verifier: strings.Repeat("v", 43), RegistrationID: registration.ID, InstallationID: "d297a1dc-3adc-4b1c-a7f2-fbd4640f69e8", ServerChallenge: strings.Repeat("c", 32), Purpose: "switch"}
			if mode == "wrong registration" {
				q.RegistrationID = "ret_wrong"
			}
			actions := OwnerReturnActions{Guard: guard,
				BeginTx: func(context.Context, *sql.Tx, serverreturn.Registration, int64) error {
					begins++
					phase = "consumed"
					return nil
				},
				CheckAttemptTx: func(context.Context, *sql.Tx) error {
					if phase != "consumed" {
						return ErrClaimOwnerRequired
					}
					return nil
				},
				Verify: func(context.Context, json.RawMessage) error {
					verified++
					if mode == "verification refused" {
						return ErrInvalid
					}
					return nil
				},
				CompleteTx: func(context.Context, *sql.Tx) error { completed++; phase = "completed"; return nil },
				AbandonTx:  func(context.Context, *sql.Tx) error { abandoned++; phase = "terminal"; return nil },
			}
			err = h.RunOwnerReturn(ctx, q, actions)
			if mode == "success" {
				if err != nil || completed != 1 || verified != 1 || abandoned != 0 || exchanges != 1 {
					t.Fatal("owner exchange progression", err, completed, verified, abandoned, exchanges)
				}
			} else {
				if err == nil || completed != 0 {
					t.Fatal("failed owner exchange admitted", mode, err)
				}
				if mode == "wrong registration" {
					if begins != 0 || exchanges != 0 || abandoned != 0 {
						t.Fatal("bad binding consumed or sent")
					}
				} else if abandoned != 1 || exchanges != 1 {
					t.Fatal("failed consumed exchange not terminal", abandoned, exchanges)
				}
			}
			if mode != "wrong registration" {
				if err = h.RunOwnerReturn(ctx, q, actions); err == nil || exchanges != 1 {
					t.Fatal("terminal attempt resent installed exchange", err, exchanges)
				}
			}
		})
	}
}
