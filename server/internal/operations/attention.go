package operations

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/telemetry"
)

// AttentionFacts gathers the database half of the needs-attention list. Every
// query is capped and indexed, and the caller adds the facts that are not in
// the database — certificate expiry, volume space, dependency readiness —
// before composing the list with telemetry.Attention.
//
// The cap on each list is deliberate: an owner with sixty broken sources needs
// to be told that sources are broken, not handed sixty rows.
const attentionRowCap = 10

func (s *Store) AttentionFacts(ctx context.Context, auth Authorize) (out telemetry.AttentionFacts, e error) {
	out.Alerts = []telemetry.OpenAlert{}
	out.UnavailableSources = []telemetry.NamedRecord{}
	out.FailedScans = []telemetry.NamedRecord{}
	out.PausedScans = []telemetry.NamedRecord{}
	e = s.snapshot(ctx, auth, "", func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id,code,severity FROM console_alerts WHERE status='open' ORDER BY last_ms DESC LIMIT ?`, attentionRowCap)
		if err != nil {
			return err
		}
		for rows.Next() {
			var a telemetry.OpenAlert
			if err = rows.Scan(&a.ID, &a.Code, &a.Severity); err != nil {
				rows.Close()
				return err
			}
			out.Alerts = append(out.Alerts, a)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		// A source is unavailable when the owner disabled it or when the server
		// could not confirm its root. Both stop playback from it.
		if out.UnavailableSources, err = namedRecords(ctx, tx, `SELECT s.id,COALESCE(NULLIF(s.name,''),lib.name,''),
 CASE WHEN s.enabled=0 THEN 'the source is turned off' ELSE 'the server reported '||s.health END
 FROM library_sources s LEFT JOIN libraries lib ON lib.id=s.library_id
 WHERE s.enabled=0 OR s.health IN ('offline','root_changed','removing') ORDER BY s.id LIMIT ?`); err != nil {
			return err
		}
		for _, lane := range []struct {
			state  string
			target *[]telemetry.NamedRecord
		}{{"failed", &out.FailedScans}, {"paused", &out.PausedScans}} {
			records, err := namedRecords(ctx, tx, `SELECT o.id,COALESCE(NULLIF(lib.name,''),o.resource,''),o.error_code
 FROM console_operations o LEFT JOIN libraries lib ON lib.id=o.resource
 WHERE o.kind='library-scan' AND o.state=`+quoted(lane.state)+` ORDER BY o.updated_ms DESC LIMIT ?`)
			if err != nil {
				return err
			}
			*lane.target = records
		}
		// Declined conversions are published by the delivery service into the
		// runtime evidence lane. Until it does, this count is honestly zero.
		return tx.QueryRowContext(ctx, `SELECT count(*) FROM console_records WHERE lane='runtime' AND component='playback' AND code IN ('conversion-declined','transcoding-disabled') AND time_ms>=?`, s.now()-days(1)).Scan(&out.ConversionsDeclined)
	})
	return
}

// quoted is used only for the fixed lane names above, never for caller input.
func quoted(v string) string { return "'" + v + "'" }

// AllowServerScope authorizes a read that belongs to the server itself rather
// than to a signed-in viewer, such as the delivery service asking the settings
// registry what policy to apply. It grants nothing to a request.
func AllowServerScope(context.Context, *sql.Tx, string) error { return nil }

func namedRecords(ctx context.Context, tx *sql.Tx, query string) ([]telemetry.NamedRecord, error) {
	out := []telemetry.NamedRecord{}
	rows, e := tx.QueryContext(ctx, query, attentionRowCap)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var v telemetry.NamedRecord
		if e = rows.Scan(&v.ID, &v.Name, &v.Detail); e != nil {
			return out, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
