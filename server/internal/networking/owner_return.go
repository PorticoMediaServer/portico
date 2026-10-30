package networking

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/serverreturn"
)

// cancelOwnerReturn recognizes the captured operation before considering the
// older central-claim cancellation path. Once captured, a missing callback or
// stale guard refuses; it can never fall through to CancelClaim.
func (h *ClaimHandler) cancelOwnerReturn(ctx context.Context, expected Intent, actor ClaimRequestOwner) (bool, error) {
	g, err := h.store.tx(ctx)
	if err != nil {
		return false, err
	}
	defer g.Rollback()
	var captured bool
	if err = g.Tx().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_owner_completions WHERE operation_id=?)`, expected.OperationID).Scan(&captured); err != nil || !captured {
		return false, err
	}
	if actor.CancelOwnerReturnTx == nil {
		return true, ErrUnavailable
	}
	if _, err = h.store.positive(ctx, g.Tx(), expected); err != nil {
		return true, err
	}
	if err = actor.CancelOwnerReturnTx(ctx, g.Tx(), expected.OperationID); err != nil {
		return true, err
	}
	// The callback rechecked the exact captured owner and installed operation
	// in this writer, then retired its family. Only the lifecycle may now commit.
	return true, commitClaimTx(ctx, g.Commit)
}

// ownerReturnRouteTx checks real currently serving TLS and local route state.
// The derived ID is only a comparison fence. First binding must still obtain
// its ID from the actual installed-server-authenticated Hosted resolver.
func (h *ClaimHandler) ownerReturnRouteTx(ctx context.Context, tx *sql.Tx, v Intent) (serverreturn.Registration, int64, error) {
	var none serverreturn.Registration
	origin, reason, err := h.managedHTTPSTx(ctx, tx)
	if err != nil {
		return none, 0, err
	}
	if origin == "" {
		return none, 0, stepFailure("owner return route: "+reason, ErrUnavailable)
	}
	var generation int64
	if err = tx.QueryRowContext(ctx, `SELECT generation FROM networking_remote_state WHERE singleton=1`).Scan(&generation); err != nil {
		return none, 0, err
	}
	claim, ce := strconv.ParseInt(v.ClaimGeneration, 10, 64)
	credential, ke := strconv.ParseInt(v.CredentialGeneration, 10, 64)
	if ce != nil || ke != nil || claim < 1 || credential < 1 || generation < 1 {
		return none, 0, ErrStale
	}
	return serverreturn.Registration{ID: serverreturn.RegistrationID(v.ServerID, v.OperationID, claim, credential, generation, origin), Origin: origin}, generation, nil
}

// This preflight releases its read-only snapshot before the resolver request.
// The caller later opens the claim store's gated writer, rechecks the route and
// only then invokes the registration-binding writer callback.
func (h *ClaimHandler) ownerReturnRoute(ctx context.Context, v Intent) (serverreturn.Registration, int64, error) {
	tx, release, err := dbwork.BeginRead(ctx, h.store.db)
	if err != nil {
		return serverreturn.Registration{}, 0, err
	}
	defer release()
	return h.ownerReturnRouteTx(ctx, tx, v)
}

// BindOwnerReturn resolves and freezes a genuine HTTPS registration only
// after the captured owner guard and actual local installation/ACK/TLS/route.
// A terminal bind callback commits its own denial without asking the family it
// just retired to authorize that denial. No new registration is published.
func (h *ClaimHandler) BindOwnerReturn(parent context.Context, guard ClaimRequestGuard, bind func(context.Context, *sql.Tx, serverreturn.Registration, int64) (bool, error)) (registration serverreturn.Registration, generation int64, err error) {
	if h == nil || h.store == nil || h.runner == nil || h.certificates == nil || guard == nil || bind == nil {
		return registration, 0, ErrUnavailable
	}
	transport, ok := h.transport.(*HTTPTransport)
	if !ok {
		return registration, 0, ErrUnavailable
	}
	err = h.runner.Do(parent, func(ctx context.Context) error {
		ctx, e := guardedClaimRequest(ctx, h.store, guard)
		if e != nil {
			return e
		}
		v, e := h.store.InstalledIntent(ctx)
		if e != nil {
			return e
		}
		if v.Purpose != "switch" || v.CurrentOwnerAccountID != "" || !v.InstallationAcknowledged {
			return ErrStale
		}
		want, gen, e := h.ownerReturnRoute(ctx, v)
		if e != nil {
			return e
		}
		var actual serverreturn.Registration
		if e = transport.CallServer(ctx, v, ResolveReturnRegistration, serverreturn.RegistrationRequest{Origin: want.Origin}, &actual); e != nil {
			return e
		}
		if actual != want {
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
		current, currentGen, e := h.ownerReturnRouteTx(ctx, g.Tx(), v)
		if e != nil {
			return e
		}
		if current != actual || currentGen != gen {
			return ErrStale
		}
		bound, e := bind(ctx, g.Tx(), actual, gen)
		if e != nil {
			return e
		}
		if !bound {
			if e = commitClaimTx(ctx, g.Commit); e != nil {
				return e
			}
			return ErrStale
		}
		if e = commitClaimDatabaseTx(ctx, g); e != nil {
			return e
		}
		registration, generation = actual, gen
		return nil
	})
	if err != nil {
		return serverreturn.Registration{}, 0, err
	}
	return
}

// OwnerReturnActions contains only private closures captured by the identity
// edge. No browser proof, URL or signed identity can construct these actions.
// Begin consumes durably; CheckAttempt is bounded to that one executing call;
// Verify spends nothing; Complete atomically admits and retires the old family.
type OwnerReturnActions struct {
	Guard          ClaimRequestGuard
	BeginTx        func(context.Context, *sql.Tx, serverreturn.Registration, int64) error
	CheckAttemptTx ClaimRequestGuard
	Verify         func(context.Context, json.RawMessage) error
	CompleteTx     func(context.Context, *sql.Tx) error
	AbandonTx      func(context.Context, *sql.Tx) error
}

// RunOwnerReturn is the sole positive owner-exchange orchestration. It never
// signs a new claim/assertion, retries a consumed exchange, or renews authority.
func (h *ClaimHandler) RunOwnerReturn(parent context.Context, q serverreturn.OwnerExchange, a OwnerReturnActions) (err error) {
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
		if v.Purpose != "switch" || v.CurrentOwnerAccountID != "" || !v.InstallationAcknowledged {
			return ErrStale
		}
		g, e := h.store.tx(ctx)
		if e != nil {
			return e
		}
		registration, generation, e := h.ownerReturnRouteTx(ctx, g.Tx(), v)
		if e == nil && registration.ID != q.RegistrationID {
			e = ErrStale
		}
		if e == nil {
			e = a.BeginTx(ctx, g.Tx(), registration, generation)
		}
		attemptGuard := func(ctx context.Context, tx *sql.Tx) error {
			if e := a.CheckAttemptTx(ctx, tx); e != nil {
				return e
			}
			current, gen, e := h.ownerReturnRouteTx(ctx, tx, v)
			if e != nil {
				return e
			}
			if current != registration || gen != generation {
				return ErrStale
			}
			return nil
		}
		attemptCtx, ce := guardedClaimRequest(ctx, h.store, attemptGuard)
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
		if e = transport.exchangeOwnerReturn(attemptCtx, v, q, &grant); e != nil {
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
		current, currentGeneration, e := h.ownerReturnRouteTx(attemptCtx, g.Tx(), v)
		if e != nil {
			return e
		}
		if current != registration || currentGeneration != generation {
			return ErrStale
		}
		// Complete checked all captured/current rows in this writer and retired
		// the initiating family. Requiring its positive guard now would roll
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

func (t *HTTPTransport) exchangeOwnerReturn(ctx context.Context, v Intent, q serverreturn.OwnerExchange, out *json.RawMessage) error {
	if !q.Valid() || v.Purpose != "switch" || v.CurrentOwnerAccountID != "" || v.Stage != Installed || !v.InstallationAcknowledged {
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
