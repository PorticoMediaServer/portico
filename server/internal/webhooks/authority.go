package webhooks

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

var errRetired = errors.New("webhook sending authority retired")

type sendAdmission struct {
	webhook    string
	generation int64
	cancel     context.CancelFunc
}

// RetireOwnerTx belongs to the accepting ownership transfer, not a later UI or
// Hosted callback. Configuration and history remain protected and reviewable.
func (s *Store) RetireOwnerTx(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,secret FROM webhooks ORDER BY id LIMIT ?`, MaxWebhooks+1)
	if err != nil {
		return err
	}
	retired := map[string]string{}
	for rows.Next() {
		var id, secret string
		if err = rows.Scan(&id, &secret); err != nil {
			break
		}
		retired[id] = secretDigest(secret)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	if len(retired) > MaxWebhooks {
		return ErrCapacity
	}
	for id, digest := range retired {
		if _, err = tx.ExecContext(ctx, `UPDATE webhooks SET retired_secret_digest=? WHERE id=?`, digest, id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE webhooks SET enabled=0,owner_review_required=1,owner_reviewed=0,owner_secret_replaced=0,authority_generation=authority_generation+1,revision=revision+1,updated_ms=?`, s.now()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE webhook_deliveries SET state='failed',finished_ms=?,error='owner_changed' WHERE state='pending'`, s.now()); err != nil {
		return err
	}
	for id := range retired {
		if err = pruneDeliveryLog(ctx, tx, id); err != nil {
			return err
		}
	}
	// Only committed transfers cancel requests. The commit callback runs while
	// the same writer gate still excludes a new outbound admission.
	return dbwork.AfterCommit(tx, func() { s.cancelSends("", 0) })
}

func (s *Store) cancelSends(webhook string, generation int64) {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	for _, admission := range s.active {
		if webhook == "" || admission.webhook == webhook && admission.generation != generation {
			admission.cancel()
		}
	}
}

// admitSend serializes final send admission with ownership/configuration
// retirement. A cached queue row alone never authorizes an HTTP request.
func (s *Store) admitSend(ctx context.Context, q queued) (context.Context, func(), error) {
	life, cancel := context.WithCancel(ctx)
	a := &sendAdmission{webhook: q.webhook, generation: q.generation, cancel: cancel}
	registered := false
	release := func() {
		cancel()
		s.activeMu.Lock()
		if s.active[q.id] == a {
			delete(s.active, q.id)
		}
		s.activeMu.Unlock()
	}
	err := dbwork.WithWriteTx(ctx, s.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		var accepted bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM webhook_deliveries d JOIN webhooks w ON w.id=d.webhook_id WHERE d.id=? AND d.webhook_id=? AND d.state='pending' AND d.authority_generation=? AND w.authority_generation=d.authority_generation AND w.owner_review_required=0 AND (w.enabled=1 OR d.type=?))`, q.id, q.webhook, q.generation, Test).Scan(&accepted); err != nil {
			return err
		}
		if !accepted {
			return errRetired
		}
		s.activeMu.Lock()
		defer s.activeMu.Unlock()
		if s.active[q.id] != nil {
			return errRetired
		}
		if s.active == nil {
			s.active = map[string]*sendAdmission{}
		}
		s.active[q.id] = a
		registered = true
		return nil
	})
	if err != nil || !registered {
		release()
		if err == nil {
			err = errRetired
		}
		return nil, nil, err
	}
	return life, release, nil
}

func secretDigest(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func currentDeliveryTx(ctx context.Context, tx *sql.Tx, q queued) (bool, error) {
	var accepted bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM webhook_deliveries d JOIN webhooks w ON w.id=d.webhook_id WHERE d.id=? AND d.state='pending' AND d.authority_generation=? AND w.authority_generation=d.authority_generation AND w.owner_review_required=0 AND (w.enabled=1 OR d.type=?))`, q.id, q.generation, Test).Scan(&accepted)
	return accepted, err
}

func pruneDeliveryLog(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM webhook_deliveries WHERE webhook_id=? AND state<>'pending' AND id NOT IN(SELECT id FROM webhook_deliveries WHERE webhook_id=? AND state<>'pending' ORDER BY created_ms DESC,id DESC LIMIT ?)`, id, id, LogKept)
	return err
}
