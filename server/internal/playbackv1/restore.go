package playbackv1

import (
	"context"
	"database/sql"
	"time"
)

// EndReasonRestored is why every session of a restored state ended.
const EndReasonRestored = "restored"

// RetireRestored runs once, when the server first starts on a restored
// database (internal/backup): no playback of the restored state may resume. Every live v1 session ends with reason "restored" (so its device's
// next timeline report or read says so), its channel state ends, prepared next
// tracks are canceled, and every media grant still live is revoked: its session
// stops and its grant hash is replaced, so no URL minted before the backup
// serves another byte. Nothing here needs the service's in-memory state: the
// restored server starts from these rows.
func RetireRestored(ctx context.Context, tx *sql.Tx, at time.Time) error {
	now := at.UnixMilli()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE playback_v1_sessions SET ended_ms=?,end_reason=?,message='',revision=revision+1,updated_ms=? WHERE ended_ms=0`, []any{now, EndReasonRestored, now}},
		{`UPDATE playback_channel_sessions SET ended_ms=? WHERE ended_ms=0`, []any{now}},
		{`UPDATE playback_v1_prepared SET state='canceled',cancel_reason=? WHERE state='prepared'`, []any{EndReasonRestored}},
		{`UPDATE playback_sessions SET state='stopped',grant_hash=lower(hex(randomblob(32))),grant_token='' WHERE state NOT IN('stopped','ended','failed')`, nil},
	} {
		if _, err := tx.ExecContext(ctx, q.sql, q.args...); err != nil {
			return err
		}
	}
	return nil
}
