package httpapi

import (
	"context"
	"database/sql"
	"net/http"
	"reflect"
	"sort"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/apiwire"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// adminEventLimit is the drawn short list's bound. The route never returns
// more, and has no cursor, filters, settings, export or write action.
const adminEventLimit = 8

// adminSafeEventCodes is the closed, reviewed vocabulary an administrator's TV
// Troubleshooting page may show. Each entry is an exact action code the server
// records in its own audit trail, and each is emitted only by a server-wide
// owner or administrator maintenance path:
//
//	alert.acknowledge        owner console alert acknowledgement
//	downloads.settings       owner's server-wide download settings
//	job.cancel/pause/...     administrator ingestion-job control (owner console too)
//	optimization.*           owner prepared-version request, control and deletion
//	schedule.apply           owner console schedule edit
//	settings.apply           owner server settings save
//
// Personal download work is deliberately absent. prepare, pause, resume, cancel,
// retry, remove and revoke are emitted for one viewer's own downloads, and the
// profile-scoped revoke and cache release for one profile, so a global code and
// time would expose private activity even after the actor and target are
// dropped; an action-only audit row carries no per-row proof that would let an
// administrator see it. Membership is not inferred from an action's spelling: an
// action outside this list is omitted. The projection drops the audit row's
// actor and target, so no account, profile, item, asset, path, token, title or
// URL travels with it.
var adminSafeEventCodes = []string{
	"alert.acknowledge",
	"downloads.settings",
	"job.cancel",
	"job.pause",
	"job.resume",
	"job.retry",
	"optimization.cancel",
	"optimization.delete",
	"optimization.request",
	"optimization.retry",
	"schedule.apply",
	"settings.apply",
}

// adminEventCode is the wire type, so the generated schema publishes the same
// closed vocabulary rather than an open string.
type adminEventCode string

func (adminEventCode) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": adminSafeEventCodes}
}

// adminEvent is one eligible recorded maintenance event. It carries only when
// it happened and which reviewed code it is; the sentence is the catalogue's,
// with no parameters filled in from the record.
type adminEvent struct {
	At   apiwire.Instant `json:"at"`
	Code adminEventCode  `json:"code"`
}

type adminEvents struct {
	Items []adminEvent `json:"items"`
	Limit int          `json:"limit"`
}

// JSONSchema publishes the bound the handler enforces: no more than the eight
// eligible events, and a limit that is always exactly eight.
func (adminEvents) JSONSchema() map[string]any {
	type wire adminEvents
	shape, err := apikit.Schema(reflect.TypeFor[wire]())
	if err != nil {
		panic(err)
	}
	props := shape["properties"].(map[string]any)
	props["limit"].(map[string]any)["const"] = adminEventLimit
	items := props["items"].(map[string]any)
	items["maxItems"] = adminEventLimit
	return shape
}

// registerAdminAccessEvents publishes the administrator's bounded, coded
// Troubleshooting list. It is deliberately not a log reader: the owner's
// get_admin_logs and the servicelog ring keep full messages, and no route name,
// device or limit widens that. The caller must still be an administrator in a
// fresh authority snapshot both before and after the read.
func registerAdminAccessEvents(registry *apikit.Registry, d Dependencies) {
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "get_admin_access_events", Method: "GET", Path: "/v1/admin/access/events", Summary: "Read the latest eligible maintenance events for an administrator", Access: apikit.Admin, Requires: apikit.ScopeAdmin, AdminScope: "access", Lane: apikit.Default, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"unauthorized", "not_permitted", "internal"}}, func(ctx context.Context, r *http.Request, _ noBody) (adminEvents, error) {
		principal, err := d.administrator(r)
		if err != nil {
			return adminEvents{}, err
		}
		out := adminEvents{Items: []adminEvent{}, Limit: adminEventLimit}
		if d.DB != nil {
			if err := readAdminEvents(ctx, d.DB, &out); err != nil {
				return adminEvents{}, &apierror.Error{Code: "internal", Cause: err}
			}
		}
		// The read runs outside the authority snapshot that admitted it. Prove
		// the same principal and administrator capability again before the data
		// leaves, so a revoked, demoted or replaced session receives nothing read
		// under its old proof.
		again, err := d.administrator(r)
		if err != nil {
			return adminEvents{}, err
		}
		if again.AccountID != principal.AccountID || again.ProfileID != principal.ProfileID ||
			again.Authority != principal.Authority || again.Role != principal.Role ||
			again.Epoch != principal.Epoch || again.Hash != principal.Hash {
			return adminEvents{}, identity.ErrForbidden
		}
		return out, nil
	})
}

type adminEventCandidate struct {
	sequence int64
	at       int64
	code     adminEventCode
}

// readAdminEvents finds the newest eligible records without walking history.
// console_audit is ordered by its sequence, not by action, so a single
// "action IN (...) ORDER BY sequence LIMIT 8" could scan an unbounded number of
// unsupported rows. Instead each code in the closed vocabulary takes its own
// newest eight through the (action, sequence) index — at most 12×8 candidates —
// and the global newest eight are merged from those heads. The eight newest eligible rows must be in
// their own code's newest eight, so the result is exact rather than a sample.
// Ordering is the audit trail's own sequence, which is unique and therefore also
// the stable tie-break when two rows share a millisecond.
//
// All the seeks share one read snapshot, so an audit append that lands between
// two of them cannot mix an older and a newer state into one alleged latest set.
// The snapshot is rolled back before the caller re-proves authority.
func readAdminEvents(ctx context.Context, db *sql.DB, out *adminEvents) error {
	gated, err := dbwork.BeginSnapshot(ctx, db)
	if err != nil {
		return err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	candidates := make([]adminEventCandidate, 0, len(adminSafeEventCodes)*out.Limit)
	for _, code := range adminSafeEventCodes {
		rows, err := tx.QueryContext(ctx, `SELECT sequence,time_ms FROM console_audit WHERE action=? ORDER BY sequence DESC LIMIT ?`, code, out.Limit)
		if err != nil {
			return err
		}
		for rows.Next() {
			var candidate adminEventCandidate
			if err := rows.Scan(&candidate.sequence, &candidate.at); err != nil {
				rows.Close()
				return err
			}
			candidate.code = adminEventCode(code)
			candidates = append(candidates, candidate)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].sequence > candidates[j].sequence })
	if len(candidates) > out.Limit {
		candidates = candidates[:out.Limit]
	}
	for _, candidate := range candidates {
		out.Items = append(out.Items, adminEvent{At: apiwire.Instant(candidate.at), Code: candidate.code})
	}
	return nil
}
