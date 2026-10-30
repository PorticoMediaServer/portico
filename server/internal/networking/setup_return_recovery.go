package networking

import (
	"context"
	"encoding/json"
	"time"
)

// resumeSetupReturnBinding resumes only the current installed setup's original
// unexpired capture. It never creates a claim, renews consent or issues a local
// owner. Route readiness may lag installation, so recovery keeps the original
// finite deadline while the real HTTPS resolver becomes available.
func (h *ClaimHandler) resumeSetupReturnBinding(parent context.Context, restore func(string) ClaimRequestGuard) {
	if h.setupReturnResolver == nil || restore == nil {
		return
	}
	var raw, operation string
	var generation int64
	err := h.store.db.QueryRowContext(parent, `SELECT w.watch,i.operation_id,i.local_generation
 FROM networking_web_claims w JOIN networking_claim_intents i ON i.operation_id=w.operation_id
 JOIN networking_claim_identity k ON k.singleton=1 AND k.active_operation_id=i.operation_id AND k.installed_operation_id=i.operation_id
 JOIN identity_setup_completions c ON c.operation_id=i.operation_id
 WHERE i.purpose='setup' AND i.stage='installed' AND i.installation_acknowledged=1
 AND w.watch<>'' AND c.phase='prepared' AND c.expires_ms>? LIMIT 1`, time.Now().UnixMilli()).Scan(&raw, &operation, &generation)
	if err != nil {
		return
	}
	var note webClaimResume
	if json.Unmarshal([]byte(raw), &note) != nil || note.Owner == "" || note.Expected.OperationID != operation || note.Expected.LocalGeneration != generation {
		return
	}
	guard := restore(note.Owner)
	if guard == nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()
	_ = h.runner.Do(ctx, func(ctx context.Context) error {
		guarded, err := guardedClaimRequest(ctx, h.store, guard)
		if err != nil {
			return err
		}
		current, err := h.store.InstalledIntent(guarded)
		if err != nil {
			return err
		}
		if current.OperationID != operation || current.LocalGeneration != generation || current.Purpose != "setup" || !current.InstallationAcknowledged {
			return ErrStale
		}
		return h.setupReturnResolver(guarded, note.Owner, operation)
	})
}
