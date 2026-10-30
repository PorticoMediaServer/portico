package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// capturePlayHistorySnapshot reads event-time facts in insertPlay's existing
// transaction. Its nullable results are retained once; subsequent history reads
// must not replace them with current catalogue or device metadata.
func capturePlayHistorySnapshot(tx *sql.Tx, v identity.Viewer, playbackID string, entityID int64) (year sql.NullInt64, app sql.NullString, err error) {
	if err = tx.QueryRow(`SELECT NULLIF(year,0) FROM catalog_entities WHERE id=?`, entityID).Scan(&year); err != nil {
		return
	}
	if year.Valid && year.Int64 <= 0 {
		year = sql.NullInt64{}
	}
	// The registered app is tied to the authenticated session's real family and
	// device. Platform, device name, user agent and a guessed product name are
	// never substitutes. A retired access token can still identify its family;
	// this lookup grants no authority and does not extend its validity.
	err = tx.QueryRow(`SELECT NULLIF(trim(d.app),'') FROM playback_sessions ps
 JOIN authorization_family_tokens t ON t.token_hash=ps.session_hash
 JOIN authorization_session_families f ON f.id=t.family_id
 JOIN identity_device_families df ON df.family_id=f.id
 JOIN identity_devices d ON d.id=df.device_id AND d.authority=f.authority AND d.account_id=f.account_id
 WHERE ps.id=? AND ps.item_id=? AND ps.account_id=? AND ps.profile_id=?
 AND f.server_id=? AND f.authority=? AND f.account_id=? AND f.profile_id=? AND f.revoked=0`,
		playbackID, entityID, v.AccountID, v.ProfileID, v.ServerID, v.Authority, v.AccountID, v.ProfileID).Scan(&app)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
		app = sql.NullString{}
	}
	if app.Valid && !safeHistoryApp(app.String) {
		app = sql.NullString{}
	}
	return
}

func safeHistoryApp(app string) bool {
	return app != "" && len(app) <= 64 && !strings.Contains(app, "://") &&
		!strings.HasPrefix(app, "/") && !strings.Contains(app, "\\") &&
		!strings.ContainsFunc(app, unicode.IsControl)
}

// readPlayHistoryActive is present observed status, not an event-time aggregate
// at ReportContext.until. Only the bounded selected history page is examined.
// Missing/ended/expired or differently owned/profiled sessions are false. A
// genuinely leased paused session remains present; completed is not liveness.
func readPlayHistoryActive(ctx context.Context, tx *sql.Tx, historyIDs []int64, observedMs int64) (map[int64]bool, error) {
	out := make(map[int64]bool, len(historyIDs))
	if len(historyIDs) > PlayHistoryMax || observedMs < 0 {
		return nil, ErrCursor
	}
	for _, id := range historyIDs {
		if id <= 0 {
			return nil, ErrCursor
		}
		out[id] = false
	}
	if len(historyIDs) == 0 {
		return out, nil
	}
	raw, _ := json.Marshal(historyIDs)
	rows, err := tx.QueryContext(ctx, `SELECT h.id,EXISTS(
 SELECT 1 FROM playback_v1_sessions v WHERE v.media_session_id=h.playback_id
 AND v.authority=h.authority AND v.account_id=h.account_id AND v.profile_id=h.profile_id
 AND v.item_id=h.item_id AND v.ended_ms=0 AND v.lease_expires_ms>?)
 FROM json_each(?) selected CROSS JOIN play_history h ON h.id=selected.value`, observedMs, string(raw))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var active bool
		if err = rows.Scan(&id, &active); err != nil {
			return nil, err
		}
		out[id] = active
	}
	return out, rows.Err()
}
