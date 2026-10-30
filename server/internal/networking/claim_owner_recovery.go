package networking

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// ArmPreinstallCancellation saves the exact original-key deny-only proof
// BEFORE a first Local-to-Portico FINALIZE can leave the process. Its pending
// outbox row is dormant while the positive intent is current: delivery requires
// the separate atomic transition to CancelPending. A successful local Install
// deletes it in the same transaction, so this proof can never undo that install.
func (s *SQLiteStore) ArmPreinstallCancellation(ctx context.Context, expected Intent, item Cancellation) (Cancellation, error) {
	if expected.Purpose != "switch" || expected.CurrentOwnerAccountID != "" || item.ClaimGeneration != "" || !sameBinding(expected.Binding, item.Binding) || validateCancellation(s.audience, item) != nil {
		return Cancellation{}, ErrInvalid
	}
	g, err := s.tx(ctx)
	if err != nil {
		return Cancellation{}, err
	}
	defer g.Rollback()
	tx := g.Tx()
	v, err := s.positive(ctx, tx, expected)
	if err != nil {
		return Cancellation{}, err
	}
	if v.Stage != Approved && v.Stage != Finalizing {
		return Cancellation{}, ErrStale
	}
	old, err := s.cancellationTx(ctx, tx, v.Intent)
	if err == nil {
		item = old // Exact first bytes survive remount/retry, never re-sign.
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Cancellation{}, err
	} else if _, err = tx.ExecContext(ctx, `INSERT INTO networking_claim_cancellations(operation_id,request_id,mode,claim_generation,payload,signature,state,created_at) VALUES(?,?,'intent','',?,?,'pending',?)`, v.OperationID, item.RequestID, item.Proof.Payload, item.Proof.Signature, s.now().UTC().Format(time.RFC3339Nano)); err != nil {
		return Cancellation{}, err
	}
	if _, err = s.finish(ctx, g, v.Intent, true); err != nil {
		return Cancellation{}, err
	}
	return item, nil
}

// ReconcilePreinstallCancellation only drains the durable deny-only intent.
// It never signs a replacement proof or asks Hosted to commit a claim.
func (c *Coordinator) ReconcilePreinstallCancellation(parent context.Context) (Intent, error) {
	if _, err := claimAuthority(parent); err != nil {
		return Intent{}, err
	}
	store, ok := c.store.(*SQLiteStore)
	if !ok {
		return Intent{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, c.timeout)
	defer cancel()
	v, item, err := store.QueuePreinstallCancellation(ctx, c.operation)
	if err != nil {
		return Intent{}, err
	}
	ctx, err = store.preinstallCancellationContext(ctx, v, item)
	if err != nil {
		return Intent{}, err
	}
	return c.deliverCancellation(ctx, v)
}

// preinstallCancellationContext is private and grants only use of the already
// durable signed cancellation. It never validates positive claim progression or
// creates a fresh proof after the initiating owner/challenge has expired.
func (s *SQLiteStore) preinstallCancellationContext(ctx context.Context, v Intent, item Cancellation) (context.Context, error) {
	return guardedClaimRequest(ctx, s, func(ctx context.Context, tx *sql.Tx) error {
		id, err := readIdentity(ctx, tx)
		if err != nil {
			return err
		}
		current, err := loadIntentTx(ctx, tx, v.OperationID)
		if err != nil {
			return err
		}
		if !sameBinding(current.Binding, v.Binding) || current.Purpose != "switch" || current.CurrentOwnerAccountID != "" || id.installed.Valid || id.server != v.ServerID || !bytes.Equal(id.key, v.PublicKey) {
			return ErrStale
		}
		switch current.Stage {
		case Approved, Finalizing, Retrieving:
			if id.generation != v.LocalGeneration || !id.pending.Valid || id.pending.String != v.OperationID || id.active.Valid {
				return ErrStale
			}
		case CancelPending:
			if id.generation != v.LocalGeneration+1 || !id.active.Valid || id.active.String != v.OperationID || id.pending.Valid {
				return ErrStale
			}
		case Cancelled:
			// Only the exact acknowledgement writer may reach this state. A
			// successor pointer or generation fences even this deny-only guard.
			if id.generation != v.LocalGeneration+1 || id.active.Valid || id.pending.Valid {
				return ErrStale
			}
		default:
			return ErrStale
		}
		state := "pending"
		if current.Stage == Cancelled {
			state = "acknowledged"
		}
		var exact bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM networking_claim_cancellations WHERE operation_id=? AND request_id=? AND payload=? AND signature=? AND state=?)`, v.OperationID, item.RequestID, item.Proof.Payload, item.Proof.Signature, state).Scan(&exact); err != nil {
			return err
		}
		if !exact {
			return ErrStale
		}
		return ctx.Err()
	})
}

// QueuePreinstallCancellation and Install compete for the same writer. This
// method cannot queue a cancel after Install won, even with retained proof.
func (s *SQLiteStore) QueuePreinstallCancellation(ctx context.Context, operation string) (Intent, Cancellation, error) {
	if _, err := claimAuthority(ctx); err != nil {
		return Intent{}, Cancellation{}, err
	}
	g, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return Intent{}, Cancellation{}, err
	}
	v, err := loadIntentTx(ctx, g.Tx(), operation)
	if err != nil {
		g.Rollback()
		return Intent{}, Cancellation{}, err
	}
	item, err := s.cancellationTx(ctx, g.Tx(), v.Intent)
	g.Rollback()
	if err != nil {
		return Intent{}, Cancellation{}, err
	}
	scoped, err := s.preinstallCancellationContext(ctx, v.Intent, item)
	if err != nil {
		return Intent{}, Cancellation{}, err
	}
	return s.BeginCancel(scoped, v.Binding, item)
}

// queuedPreinstallCancellation is the sole Local networking exception for this
// recovery path. An armed row alone is insufficient: cancellation must already
// have won the local writer and fenced installation. No fresh proof or positive
// claim endpoint is authorized by this check.
func (s *SQLiteStore) queuedPreinstallCancellation(ctx context.Context, payload, signature []byte) (bool, error) {
	g, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return false, err
	}
	defer g.Rollback()
	tx := g.Tx()
	var operation string
	err = tx.QueryRowContext(ctx, `SELECT operation_id FROM networking_claim_cancellations WHERE payload=? AND signature=? AND mode='intent' AND claim_generation='' AND state='pending'`, payload, signature).Scan(&operation)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	v, err := loadIntentTx(ctx, tx, operation)
	if err != nil {
		return false, err
	}
	id, err := readIdentity(ctx, tx)
	if err != nil {
		return false, err
	}
	if v.Purpose != "switch" || v.CurrentOwnerAccountID != "" || v.Stage != CancelPending || id.installed.Valid || id.pending.Valid || !id.active.Valid || id.active.String != operation || id.server != v.ServerID || !bytes.Equal(id.key, v.PublicKey) || id.generation != v.LocalGeneration+1 {
		return false, nil
	}
	_, err = s.cancellationTx(ctx, tx, v.Intent)
	return err == nil, err
}

// reconcileWebClaim abandons positive progression only after the saved owner
// guard/deadline ended, or an explicit closed decision. It never cancels an
// installed claim. A transient remote error with live authority leaves the
// same operation available for an explicit retry.
func (h *ClaimHandler) reconcileWebClaim(parent context.Context, operation string, guard ClaimRequestGuard, closed bool) error {
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()
	return h.runner.Do(ctx, func(ctx context.Context) error {
		v, err := h.store.Load(ctx, operation)
		if err != nil || v.Stage == Installed || v.Stage == Cancelled {
			return err
		}
		abandoned := closed || guard == nil || (!v.ApprovalExpiresAt.IsZero() && !h.store.now().Before(v.ApprovalExpiresAt))
		if !abandoned {
			guarded, err := guardedClaimRequest(ctx, h.store, guard)
			if err != nil {
				return err
			}
			err = checkClaimRequest(guarded)
			if errors.Is(err, ErrClaimOwnerRequired) || errors.Is(err, ErrStale) {
				abandoned = true
			} else if err != nil {
				return err
			}
		}
		if v.Stage != CancelPending && !abandoned {
			return nil
		}
		if v.Purpose == "switch" && v.CurrentOwnerAccountID == "" && (v.Stage == Finalizing || v.Stage == Retrieving || v.Stage == CancelPending) {
			coordinator, err := NewCoordinator(operation, h.store.audience, h.store, h.transport, h.signer)
			if err != nil {
				return err
			}
			_, err = coordinator.ReconcilePreinstallCancellation(ctx)
			return err
		}
		_, _, err = h.store.WithdrawReplacement(ctx, v)
		return err
	})
}

// RunWebClaimRecovery resumes saved waits and retries only durable deny-only
// cancellations. It neither starts a new claim nor renews owner consent.
func (h *ClaimHandler) wakeClaimRecovery() {
	if h == nil {
		return
	}
	select {
	case h.recoveryWake <- struct{}{}:
	default:
	}
}

func (h *ClaimHandler) RunWebClaimRecovery(ctx context.Context, guard func(string) ClaimRequestGuard) {
	if h == nil || guard == nil {
		return
	}
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	if !timer.Stop() {
		<-timer.C
	}
	for {
		h.resumeSetupReturnBinding(ctx, guard)
		h.ResumeWebClaims(ctx, guard)
		// A crash or failed pending POST can leave a complete typed capture with
		// no watch/resume note. Expiry itself authorizes only its exact denial;
		// it never reconstructs a positive owner guard or sends a fresh claim.
		rows, err := h.store.db.QueryContext(ctx, `SELECT c.operation_id FROM identity_owner_completions c JOIN networking_claim_intents i ON i.operation_id=c.operation_id WHERE c.phase='prepared' AND c.expires_ms<=? AND i.purpose='switch' AND i.current_owner_account_id='' AND i.stage IN('prepared','approved','finalizing','retrieving')`, time.Now().UnixMilli())
		if err == nil {
			var expired []string
			for rows.Next() {
				var operation string
				if err = rows.Scan(&operation); err != nil {
					break
				}
				expired = append(expired, operation)
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
			if err == nil {
				for _, operation := range expired {
					if err = h.reconcileWebClaim(ctx, operation, nil, true); err != nil && ctx.Err() == nil {
						log.Printf("Expired owner claim could not be reconciled: %v", err)
					}
				}
			}
		}
		rows, err = h.store.db.QueryContext(ctx, `SELECT i.operation_id FROM networking_claim_intents i JOIN networking_claim_cancellations c ON c.operation_id=i.operation_id WHERE i.purpose='switch' AND i.current_owner_account_id='' AND i.stage='cancel_pending' AND c.state='pending' AND c.mode='intent'`)
		if err == nil {
			var operations []string
			for rows.Next() {
				var operation string
				if err = rows.Scan(&operation); err != nil {
					break
				}
				operations = append(operations, operation)
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
			for _, operation := range operations {
				if err = h.reconcileWebClaim(ctx, operation, nil, false); err != nil && ctx.Err() == nil {
					log.Printf("Pending owner claim cancellation could not be delivered: %v", err)
				}
			}
		}
		// A server with no pending approval or queued denial has no recovery
		// work. Stay asleep until an actual claim action/watcher wakes it;
		// permanently polling empty tables would consume the idle SQL budget.
		var pending bool
		err = h.store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM networking_web_claims w JOIN networking_claim_intents i ON i.operation_id=w.operation_id WHERE w.watch<>'' AND i.stage IN('prepared','approved','finalizing','retrieving')) OR EXISTS(SELECT 1 FROM networking_claim_intents i JOIN networking_claim_cancellations c ON c.operation_id=i.operation_id WHERE i.purpose='switch' AND i.current_owner_account_id='' AND i.stage='cancel_pending' AND c.state='pending' AND c.mode='intent') OR EXISTS(SELECT 1 FROM identity_owner_completions c JOIN networking_claim_intents i ON i.operation_id=c.operation_id WHERE c.phase='prepared' AND i.stage IN('prepared','approved','finalizing','retrieving')) OR EXISTS(SELECT 1 FROM identity_setup_completions c JOIN networking_claim_identity k ON k.singleton=1 AND k.active_operation_id=c.operation_id AND k.installed_operation_id=c.operation_id JOIN networking_web_claims w ON w.operation_id=c.operation_id WHERE c.phase='prepared' AND c.expires_ms>? AND w.watch<>'')`, time.Now().UnixMilli()).Scan(&pending)
		if err == nil && !pending {
			select {
			case <-ctx.Done():
				return
			case <-h.recoveryWake:
				continue
			}
		}
		timer.Reset(30 * time.Second)
		select {
		case <-ctx.Done():
			return
		case <-h.recoveryWake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
		}
	}
}
