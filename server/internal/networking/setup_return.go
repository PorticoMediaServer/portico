package networking

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/serverreturn"
)

// InstallSetupReturn records, after the captured setup guard and the actual local
// installation and its acknowledgement, that the setup can finish now (O-163). Owner
// admission no longer waits for a certificate or an HTTPS return registration; that
// registration attaches later and serves only ordinary sign-ins. Nothing is published
// and Hosted is not called.
func (h *ClaimHandler) InstallSetupReturn(parent context.Context, guard ClaimRequestGuard, mark func(context.Context, *sql.Tx) error) error {
	if h == nil || h.store == nil || h.runner == nil || guard == nil || mark == nil {
		return ErrUnavailable
	}
	return h.runner.Do(parent, func(ctx context.Context) error {
		ctx, e := guardedClaimRequest(ctx, h.store, guard)
		if e != nil {
			return e
		}
		v, e := h.store.InstalledIntent(ctx)
		if e != nil {
			return e
		}
		if v.Purpose != "setup" || v.CurrentOwnerAccountID != "" || !v.InstallationAcknowledged {
			return ErrStale
		}
		g, e := h.store.tx(ctx)
		if e != nil {
			return e
		}
		defer g.Rollback()
		if _, e = h.store.positive(ctx, g.Tx(), v); e != nil {
			return e
		}
		if e = mark(ctx, g.Tx()); e != nil {
			return e
		}
		return commitClaimDatabaseTx(ctx, g)
	})
}

// SetupReturnActions contains only private closures captured by the identity
// edge. No browser proof, URL or signed identity can construct these actions.
// Begin consumes durably; CheckAttempt is bounded to that one executing call;
// Verify spends nothing; Complete atomically admits and retires the old family.
type SetupReturnActions struct {
	Guard          ClaimRequestGuard
	BeginTx        func(context.Context, *sql.Tx) error
	CheckAttemptTx ClaimRequestGuard
	Verify         func(context.Context, json.RawMessage) error
	CompleteTx     func(context.Context, *sql.Tx) error
	AbandonTx      func(context.Context, *sql.Tx) error
}

// RunSetupReturn is the sole positive owner-exchange orchestration. It never
// signs a new claim/assertion, retries a consumed exchange, or renews authority.
func (h *ClaimHandler) RunSetupReturn(parent context.Context, q serverreturn.SetupExchange, a SetupReturnActions) (err error) {
	if h == nil || h.store == nil || h.runner == nil || h.certificates == nil || !q.Valid() || a.Guard == nil || a.BeginTx == nil || a.CheckAttemptTx == nil || a.Verify == nil || a.CompleteTx == nil || a.AbandonTx == nil {
		return ErrInvalid
	}
	transport, ok := h.transport.(*HTTPTransport)
	if !ok {
		return ErrUnavailable
	}
	consumed, completed := false, false
	err = h.runner.Do(parent, func(ctx context.Context) error {
		ctx, e := guardedClaimRequest(ctx, h.store, a.Guard)
		if e != nil {
			return e
		}
		v, e := h.store.InstalledIntent(ctx)
		if e != nil {
			return e
		}
		if v.Purpose != "setup" || v.CurrentOwnerAccountID != "" || !v.InstallationAcknowledged {
			return ErrStale
		}
		g, e := h.store.tx(ctx)
		if e != nil {
			return e
		}
		// The installed claim and credential generations are fenced by the identity
		// writer (CheckPorticoReturnTx); setup binds no route or registration (O-163).
		if _, e = h.store.positive(ctx, g.Tx(), v); e == nil {
			e = a.BeginTx(ctx, g.Tx())
		}
		attemptCtx, ce := guardedClaimRequest(ctx, h.store, a.CheckAttemptTx)
		if e == nil {
			e = ce
		}
		if e == nil {
			e = commitClaimDatabaseTx(attemptCtx, g)
		}
		g.Rollback()
		if e != nil {
			return e
		}
		consumed = true
		var grant json.RawMessage
		if e = transport.exchangeSetupReturn(attemptCtx, v, q, &grant); e != nil {
			return e
		}
		defer clear(grant)
		if e = a.Verify(attemptCtx, grant); e != nil {
			return e
		}
		g, e = h.store.tx(attemptCtx)
		if e != nil {
			return e
		}
		defer g.Rollback()
		if e = a.CompleteTx(attemptCtx, g.Tx()); e != nil {
			return e
		}
		// Complete checked all captured/current rows in this writer and completed
		// the pending setup authority. Requiring its positive guard now would roll
		// back the intended retirement. The root lifecycle lease still fences
		// the atomic commit; no callback can commit outside this writer.
		if e = commitClaimTx(attemptCtx, g.Commit); e != nil {
			return e
		}
		completed = true
		return nil
	})
	if err != nil && consumed && !completed {
		// This is deny-only cleanup. The durable consumed phase already blocks
		// re-entry if cancellation/crash prevents cleanup from committing.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer cancel()
		_ = h.runner.Do(ctx, func(ctx context.Context) error {
			g, e := h.store.tx(ctx)
			if e != nil {
				return e
			}
			defer g.Rollback()
			if e = a.AbandonTx(ctx, g.Tx()); e != nil {
				return e
			}
			return commitClaimTx(ctx, g.Commit)
		})
	}
	return err
}

func (t *HTTPTransport) exchangeSetupReturn(ctx context.Context, v Intent, q serverreturn.SetupExchange, out *json.RawMessage) error {
	if !q.Valid() || v.Purpose != "setup" || v.CurrentOwnerAccountID != "" || v.Stage != Installed || !v.InstallationAcknowledged {
		return ErrInvalid
	}
	secret, e := t.credentials.InstalledCredential(ctx, v)
	if e != nil {
		return e
	}
	defer secret.Clear()
	if e = t.controlRequest(ctx, http.MethodPost, "/v1/server-returns/exchange", q, out, secret, claimHTTPBound); e != nil {
		return e
	}
	return t.credentials.Current(ctx, v)
}
