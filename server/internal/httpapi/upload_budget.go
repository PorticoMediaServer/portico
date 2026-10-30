package httpapi

import (
	"context"
	"database/sql"
	"time"
)

// The upload budget (Plex's "Internet upload speed"): new remote streams share
// uploadBudgetPercent of the owner's stated upload capacity with the remote
// streams already playing, and a new stream's video ceiling is what is left.
// Streams already playing are never re-planned. When even the floor does not
// fit, the stream still plays at the floor and the owner is alerted: a refusal
// would be a failure the viewer cannot explain.
const (
	uploadBudgetPercent = 80
	// uploadAudioAllowanceKbps is kept out of the video ceiling for the stream's
	// own audio.
	uploadAudioAllowanceKbps = 192
	// uploadFloorKbps is the lowest video ceiling the budget ever sets, about 480p.
	uploadFloorKbps = 1200
)

// uploadBudgetCeiling returns the video ceiling in bits per second for a new
// remote stream and whether the budget is exhausted. It reads inside the
// admission transaction; replan is the v1 session being re-planned, whose own
// share is not counted against it.
func uploadBudgetCeiling(ctx context.Context, tx *sql.Tx, capacityKbps int, replan string, now time.Time) (int, bool, error) {
	var used int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(MAX(0,CAST(COALESCE(json_extract(presentation,'$.bitrateKbps'),0) AS INTEGER))),0)
		FROM playback_v1_sessions WHERE ended_ms=0 AND lease_expires_ms>? AND location IN ('remote','cellular') AND id<>?`,
		now.UnixMilli(), replan).Scan(&used); err != nil {
		return 0, false, err
	}
	return uploadCeiling(capacityKbps, used)
}

func uploadCeiling(capacityKbps int, usedKbps int64) (int, bool, error) {
	remaining := int64(capacityKbps)*uploadBudgetPercent/100 - usedKbps - uploadAudioAllowanceKbps
	if remaining < uploadFloorKbps {
		return uploadFloorKbps * 1000, true, nil
	}
	return int(remaining) * 1000, false, nil
}

// narrowBitrate is the tighter of two ceilings, where 0 means none.
func narrowBitrate(a, b int) int {
	switch {
	case a <= 0:
		return b
	case b <= 0 || a < b:
		return a
	default:
		return b
	}
}

// alertUploadBudget raises the owner alert while new remote streams are being
// held at the floor and clears it once one fits again. It changes state only.
func (d Dependencies) alertUploadBudget(ctx context.Context, exhausted bool) {
	if d.connectionPolicy == nil || d.Console == nil || d.connectionPolicy.uploadAlert.Load() == exhausted {
		return
	}
	d.connectionPolicy.mu.Lock()
	defer d.connectionPolicy.mu.Unlock()
	if d.connectionPolicy.uploadAlert.Load() == exhausted {
		return
	}
	if err := d.Console.Alert(ctx, "upload-budget-exhausted", "warning", exhausted, map[string]string{"reason": "upload-budget-exhausted", "time": time.Now().UTC().Format(time.RFC3339Nano)}); err == nil {
		d.connectionPolicy.uploadAlert.Store(exhausted)
	}
}
