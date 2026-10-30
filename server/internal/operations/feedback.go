package operations

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/apiwire"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"strconv"
)

// resolveReportItem maps a report's public item id to the integer
// console_reports stores (0 when there is none). An unknown id stores 0: the
// readers blank unreachable media context through auth, so a dangling
// reference was never observable and 0 reads the same way.
func resolveReportItem(ctx context.Context, tx *sql.Tx, public string) (int64, error) {
	if public == "" {
		return 0, nil
	}
	entity, err := entityid.Resolve(ctx, tx, public)
	if errors.Is(err, entityid.ErrNotFound) {
		return 0, nil
	}
	return entity, err
}

type Notification struct {
	ID        string           `json:"id"`
	Sequence  int64            `json:"-"`
	CreatedAt apiwire.Instant  `json:"createdAt"`
	ReadAt    *apiwire.Instant `json:"readAt"`
	Code      string           `json:"code"`
	TargetID  string           `json:"targetId,omitempty"`
	Message   string           `json:"message"`
}
type Inbox struct {
	Page[Notification]
	Unread int `json:"unread"`
}

func notifyTx(tx *sql.Tx, now int64, scope, code, target, dedupe string) error {
	settings, e := readSettings(tx)
	if e != nil {
		return e
	}
	_, e = tx.Exec(`INSERT OR IGNORE INTO console_notifications(id,scope,created_ms,code,target_id,expires_ms,dedupe) VALUES(?,?,?,?,?,?,?)`, identity.Token(), scope, now, code, target, now+days(settings.Effective.NotificationDays), dedupe)
	if e != nil {
		return e
	}
	// Explicit per-inbox rotation; a single noisy profile cannot evict another.
	_, e = tx.Exec(`DELETE FROM console_notifications WHERE scope=? AND sequence NOT IN(SELECT sequence FROM console_notifications WHERE scope=? ORDER BY sequence DESC LIMIT 1000)`, scope, scope)
	return e
}
func (s *Store) Inbox(ctx context.Context, p identity.Principal, auth Authorize, cursor string, limit int) (out Inbox, e error) {
	out.Items = []Notification{}
	before, e := Cursor(cursor)
	if e != nil || limit < 1 || limit > 40 {
		return out, ErrInvalid
	}
	scope := ViewerKey(p)
	e = s.snapshot(ctx, auth, "", func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT id,sequence,created_ms,read_ms,code,target_id FROM console_notifications WHERE scope=? AND sequence<? AND expires_ms>? ORDER BY sequence DESC LIMIT ?`, scope, before, s.now(), limit+1)
		if err != nil {
			return err
		}
		for rows.Next() {
			var n Notification
			if err = rows.Scan(&n.ID, &n.Sequence, &n.CreatedAt, &n.ReadAt, &n.Code, &n.TargetID); err != nil {
				rows.Close()
				return err
			}
			n.Message = "Your feedback has an update."
			if n.Code == "feedback-received" {
				n.Message = "New viewer feedback is ready for review."
			}
			out.Items = append(out.Items, n)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(out.Items) > limit {
			out.Items = out.Items[:limit]
			out.NextCursor = strconv.FormatInt(out.Items[len(out.Items)-1].Sequence, 10)
		}
		for i := range out.Items {
			n := &out.Items[i]
			if n.Code == "owner-alert" {
				n.Message = "A server operation needs owner attention."
				var found int
				if p.Authority != "local" || p.Role != "owner" || tx.QueryRow(`SELECT 1 FROM console_alerts WHERE id=?`, n.TargetID).Scan(&found) != nil {
					n.TargetID = ""
				}
				continue
			}
			var item, reportScope string
			err = tx.QueryRow(`SELECT COALESCE((SELECT pid(public_id) FROM catalog_entities WHERE id=console_reports.item_id),''),scope FROM console_reports WHERE id=? AND expires_ms>?`, n.TargetID, s.now()).Scan(&item, &reportScope)
			if err != nil || reportScope != scope && (p.Authority != "local" || p.Role != "owner") || item != "" && auth(ctx, tx, item) != nil {
				n.TargetID = ""
				n.Message = "A previous report has a status update."
			}
		}
		return tx.QueryRow(`SELECT count(*) FROM console_notifications WHERE scope=? AND read_ms IS NULL AND expires_ms>?`, scope, s.now()).Scan(&out.Unread)
	})
	return
}
func (s *Store) ReadNotice(ctx context.Context, p identity.Principal, auth Authorize, id string) error {
	if !validID(id) {
		return ErrInvalid
	}
	return s.transaction(ctx, auth, "", func(tx *sql.Tx) error {
		r, e := tx.Exec(`UPDATE console_notifications SET read_ms=COALESCE(read_ms,?) WHERE id=? AND scope=? AND expires_ms>?`, s.now(), id, ViewerKey(p), s.now())
		if e != nil {
			return e
		}
		n, _ := r.RowsAffected()
		if n != 1 {
			return sql.ErrNoRows
		}
		return nil
	})
}

// Notify is the local registration seam for other packages. The producer must
// authorize and supply the exact viewer owner in its transaction. No free-form
// media payload or delivery-to-Hosted option exists.
func Notify(tx *sql.Tx, now int64, p identity.Principal, code, target, dedupe string) error {
	if code != "feedback-updated" && code != "feedback-received" {
		return ErrInvalid
	}
	if !validID(target) || len(dedupe) > 200 {
		return ErrInvalid
	}
	return notifyTx(tx, now, ViewerKey(p), code, target, dedupe)
}

// Owner notifications go only to the active Direct Sign-In recovery owner.
func ownerScopes(tx *sql.Tx) ([]string, error) {
	rows, e := tx.Query(`SELECT a.id,a.profile_id FROM accounts a JOIN direct_memberships m ON m.account_id=a.id WHERE m.role='owner' AND m.disabled=0`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	owners := []string{}
	for rows.Next() {
		var account, profile string
		if e = rows.Scan(&account, &profile); e != nil {
			return nil, e
		}
		owners = append(owners, ViewerKey(identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: account, ProfileID: profile}}))
	}
	return owners, rows.Err()
}
