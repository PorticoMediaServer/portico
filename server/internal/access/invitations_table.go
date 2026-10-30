package access

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type invitationTableCursor struct {
	Query   string `json:"q"`
	State   string `json:"state"`
	Created int64  `json:"created"`
	ID      string `json:"id"`
}

// InvitationTablePage is the existing People list's counted projection. Counts
// are computed with the page before LIMIT, never from a partial client page.
type InvitationTablePage struct {
	InvitationPage
	TotalCount   int64 `json:"totalCount"`
	MatchedCount int64 `json:"matchedCount"`
}

// InvitationTable supplies the existing invitation list's table projection.
// Counts, visible search, ordering and the page share one reauthorized read.
// The existing HTTP list projects these counts with the same page.
func (s *Store) InvitationTable(ctx context.Context, auth Authorize, actor identity.Principal, q InvitationQuery, search, sort, direction string) (out InvitationPage, total, matched int64, err error) {
	limit, err := pageLimit(q.Limit)
	if err != nil {
		return out, 0, 0, err
	}
	if len(search) > 160 || !safeText(search, 160) {
		return out, 0, 0, invalid("q")
	}
	search = strings.ToLower(strings.TrimSpace(search))
	if sort == "" {
		sort = "sent"
	}
	if direction == "" {
		direction = "desc"
	}
	if sort != "sent" && sort != "status" {
		return out, 0, 0, invalid("sort")
	}
	if direction != "asc" && direction != "desc" {
		return out, 0, 0, invalid("direction")
	}
	switch q.State {
	case "", "pending", "accepted", "revoked", "expired":
	default:
		return out, 0, 0, invalid("state")
	}
	// The cursor never contains private search text or notes. Its scope includes
	// the exact actor and tier, so a role change cannot reuse a richer search.
	canonical, _ := json.Marshal([]string{actor.AccountID, actor.Role, strconv.Itoa(actor.Epoch), search, q.State, sort, direction})
	hash := sha256.Sum256(canonical)
	fingerprint := hex.EncodeToString(hash[:])
	var cursor invitationTableCursor
	if q.Cursor != "" {
		if len(q.Cursor) > 2048 {
			return out, 0, 0, invalid("cursor")
		}
		raw, e := base64.RawURLEncoding.Strict().DecodeString(q.Cursor)
		if e != nil || base64.RawURLEncoding.EncodeToString(raw) != q.Cursor || json.Unmarshal(raw, &cursor) != nil {
			return out, 0, 0, invalid("cursor")
		}
		encoded, e := json.Marshal(cursor)
		if e != nil || !bytes.Equal(encoded, raw) || cursor.Query != fingerprint || !validID(cursor.ID) || cursor.Created < 1 {
			return out, 0, 0, invalid("cursor")
		}
		switch cursor.State {
		case "pending", "accepted", "revoked", "expired":
		default:
			return out, 0, 0, invalid("cursor")
		}
	}
	out.Limit, out.Items = limit, []Invitation{}
	err = s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		live, e := Role(tx, actor.AccountID)
		if e != nil {
			return e
		}
		if actor.Authority != "local" || actor.Grant != "" || actor.Role != live || !identity.Administrative(live) {
			return identity.ErrForbidden
		}
		// Redact before search and LIMIT. A peer invitation's private note must
		// affect neither the visible rows nor their count for an administrator.
		cte := `WITH visible AS (SELECT id,CASE WHEN ?='owner' OR (?='admin' AND role='member') THEN notes ELSE NULL END AS notes,role,allowed_libraries,CASE WHEN state='pending' AND expires_ms<=? THEN 'expired' ELSE state END AS state,created_ms,expires_ms,accepted_ms,account_id,revision,bound_username,max_streams_mode,max_streams,allow_remote,access_tokens,max_content_rating FROM access_invitations) `
		baseArgs := []any{live, live, s.now()}
		if e = tx.QueryRowContext(ctx, cte+`SELECT count(*) FROM visible`, baseArgs...).Scan(&total); e != nil {
			return e
		}
		where := []string{"1=1"}
		args := append([]any{}, baseArgs...)
		if q.State != "" {
			where = append(where, "state=?")
			args = append(args, q.State)
		}
		for _, word := range strings.Fields(search) {
			where = append(where, `(instr(lower(COALESCE(notes,'')),?)>0 OR instr(lower(bound_username),?)>0)`)
			args = append(args, word, word)
		}
		predicate := strings.Join(where, " AND ")
		if e = tx.QueryRowContext(ctx, cte+`SELECT count(*) FROM visible WHERE `+predicate, args...).Scan(&matched); e != nil {
			return e
		}
		order := "created_ms " + direction + ",id " + direction
		if sort == "status" {
			order = "state " + direction + "," + order
		}
		if cursor.ID != "" {
			op := ">"
			if direction == "desc" {
				op = "<"
			}
			if sort == "status" {
				predicate += " AND (state,created_ms,id)" + op + "(?,?,?)"
				args = append(args, cursor.State, cursor.Created, cursor.ID)
			} else {
				predicate += " AND (created_ms,id)" + op + "(?,?)"
				args = append(args, cursor.Created, cursor.ID)
			}
		}
		args = append(args, limit+1)
		rows, e := tx.QueryContext(ctx, cte+`SELECT `+invitationColumns+` FROM visible WHERE `+predicate+` ORDER BY `+order+` LIMIT ?`, args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		var last invitationTableCursor
		for rows.Next() {
			v, created, e := invitationRowCreated(rows)
			if e != nil {
				return e
			}
			if len(out.Items) == limit {
				raw, _ := json.Marshal(last)
				out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
				break
			}
			out.Items = append(out.Items, v)
			// Preserve the exact millisecond tie-break rather than round-tripping
			// the seconds-only display timestamp used by the older wire record.
			last = invitationTableCursor{Query: fingerprint, State: v.State, Created: created, ID: v.ID}
		}
		if e = rows.Err(); e != nil {
			return e
		}
		return nil
	})
	return
}
