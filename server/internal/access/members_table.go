package access

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/streamlimits"
	"strings"
	"time"
)

// MemberRow is the table projection. Private notes and full permission/profile
// documents belong to the panel and are never search or sort inputs here.
type MemberRow struct {
	AccountID           string            `json:"accountId"`
	Username            string            `json:"username"`
	Email               string            `json:"email,omitempty"`
	Role                string            `json:"role"`
	Revision            int64             `json:"revision"`
	LimitsRevision      int64             `json:"limitsRevision"`
	Disabled            bool              `json:"disabled"`
	Status              string            `json:"status"`
	HasMoveCode         bool              `json:"hasMoveCode"`
	AllowedLibraries    []string          `json:"allowedLibraries"`
	LibraryNames        []string          `json:"libraryNames"`
	MaxStreams          int               `json:"maxStreams"`
	MaxStreamsMode      streamlimits.Mode `json:"maxStreamsMode"`
	ServerMaxStreams    *int              `json:"serverMaxStreams"`
	EffectiveMaxStreams *int              `json:"effectiveMaxStreams"`
	ProfileCount        int               `json:"profileCount"`
	ProfileNames        []string          `json:"profileNames"`
	LastActiveAt        string            `json:"lastActiveAt,omitempty"`
	LiveState           string            `json:"liveState"`
	Notes               *string           `json:"-"`
}
type memberTableCursor struct {
	Query string `json:"q"`
	Value string `json:"v"`
	ID    string `json:"id"`
}

func (s *Store) Members(ctx context.Context, auth Authorize, q MemberQuery) (out MemberPage, err error) {
	limit, err := pageLimit(q.Limit)
	if err != nil {
		return out, err
	}
	if len(q.Search) > 160 {
		return out, invalid("q")
	}
	q.Search = strings.TrimSpace(q.Search)
	if q.Role == "" {
		q.Role = "all"
	}
	if q.Status == "" {
		q.Status = "all"
	}
	if q.Sort == "" {
		q.Sort = "active"
	}
	if q.Direction == "" {
		q.Direction = "desc"
	}
	if q.Role != "all" && q.Role != "admin" && q.Role != "member" && q.Role != "owner" {
		return out, invalid("role")
	}
	if q.Direction != "asc" && q.Direction != "desc" {
		return out, invalid("direction")
	}
	where := []string{"1=1"}
	args := []any{}
	if q.Role == "admin" {
		where = append(where, "m.role IN('owner','admin')")
	} else if q.Role != "all" {
		where = append(where, "m.role=?")
		args = append(args, q.Role)
	}
	last := `COALESCE((SELECT max(last_seen) FROM identity_devices WHERE account_id=a.id AND authority='local' AND platform<>'api'),'')`
	live := `EXISTS(SELECT 1 FROM playback_v1_sessions v WHERE v.account_id=a.id AND v.ended_ms=0 AND v.lease_expires_ms>unixepoch('subsec')*1000)`
	profiles := `(SELECT count(*) FROM direct_profiles WHERE account_id=a.id AND deleted=0)`
	status := `CASE WHEN m.disabled_reason='left' THEN 'left' WHEN EXISTS(SELECT 1 FROM identity_move_waiting w WHERE w.account_id=a.id AND w.state='waiting') THEN 'waiting' WHEN m.disabled_reason='did_not_move' THEN 'did_not_move' WHEN m.disabled=1 THEN 'off' ELSE 'active' END`
	switch q.Status {
	case "all":
	case "now":
		where = append(where, live)
	case "month":
		where = append(where, last+">=?")
		args = append(args, time.UnixMilli(s.now()).UTC().AddDate(0, 0, -30).Format(time.RFC3339))
	case "idle":
		where = append(where, last+"<?")
		args = append(args, time.UnixMilli(s.now()).UTC().AddDate(0, 0, -90).Format(time.RFC3339))
	case "off":
		where = append(where, "m.disabled=1")
	case "waiting", "left", "did_not_move", "active":
		where = append(where, status+"=?")
		args = append(args, q.Status)
	default:
		return out, invalid("status")
	}
	for _, word := range strings.Fields(strings.ToLower(q.Search)) {
		where = append(where, `(instr(lower(a.username),?)>0 OR instr(lower(a.email),?)>0 OR EXISTS(SELECT 1 FROM direct_profiles p WHERE p.account_id=a.id AND p.deleted=0 AND instr(lower(p.name),?)>0) OR EXISTS(SELECT 1 FROM libraries l WHERE (json_array_length(m.allowed_libraries)=0 OR l.id IN(SELECT value FROM json_each(m.allowed_libraries))) AND instr(lower(l.name),?)>0))`)
		args = append(args, word, word, word, word)
	}
	sortExpr := ""
	switch q.Sort {
	case "person":
		sortExpr = "lower(a.username)"
	case "role":
		sortExpr = "m.role"
	case "access":
		sortExpr = "CAST(CASE WHEN json_array_length(m.allowed_libraries)=0 THEN (SELECT count(*) FROM libraries) ELSE json_array_length(m.allowed_libraries) END AS TEXT)"
	case "profiles":
		sortExpr = "CAST(" + profiles + " AS TEXT)"
	case "active":
		sortExpr = last
	default:
		return out, invalid("sort")
	}
	// Sort keys are scalar text with the account id as an unambiguous tie-break.
	// Numeric keys are padded so their lexical and numeric orders agree.
	if q.Sort == "profiles" {
		sortExpr = "printf('%010d'," + profiles + ")"
	}
	if q.Sort == "access" {
		sortExpr = "printf('%010d',CASE WHEN json_array_length(m.allowed_libraries)=0 THEN (SELECT count(*) FROM libraries) ELSE json_array_length(m.allowed_libraries) END)"
	}
	canonical, _ := json.Marshal([]string{q.Search, q.Role, q.Status, q.Sort, q.Direction})
	sum := sha256.Sum256(canonical)
	digest := hex.EncodeToString(sum[:])
	var cursor memberTableCursor
	if q.Cursor != "" {
		raw, e := base64.RawURLEncoding.Strict().DecodeString(q.Cursor)
		if e != nil || len(raw) > 1024 || json.Unmarshal(raw, &cursor) != nil || cursor.Query != digest || cursor.ID == "" {
			return out, invalid("cursor")
		}
	}
	out.Limit = limit
	out.Items = []MemberRow{}
	err = s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM accounts a JOIN direct_memberships m ON m.account_id=a.id`).Scan(&out.TotalCount); e != nil {
			return e
		}
		pred := strings.Join(where, " AND ")
		if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM accounts a JOIN direct_memberships m ON m.account_id=a.id WHERE `+pred, args...).Scan(&out.MatchedCount); e != nil {
			return e
		}
		pageArgs := append([]any{}, args...)
		if cursor.ID != "" {
			op := ">"
			if q.Direction == "desc" {
				op = "<"
			}
			pred += " AND (" + sortExpr + ",a.id)" + op + "(?,?)"
			pageArgs = append(pageArgs, cursor.Value, cursor.ID)
		}
		pageArgs = append(pageArgs, limit+1)
		query := `SELECT EXISTS(SELECT 1 FROM identity_move_codes c JOIN identity_move_waiting w ON w.account_id=c.account_id WHERE c.account_id=a.id AND c.expires_ms>unixepoch('subsec')*1000 AND w.state='waiting'),a.id,a.username,a.email,m.role,m.revision,COALESCE(l.revision,1),m.disabled,` + status + `,m.allowed_libraries,COALESCE(sl.stream_limit,0),COALESCE(sl.mode,'inherit'),stream_policy.per_account_cap,` + profiles + `,(SELECT json_group_array(name) FROM (SELECT name FROM direct_profiles WHERE account_id=a.id AND deleted=0 ORDER BY is_primary DESC,position,id)),` + last + `,CASE WHEN ` + live + ` THEN 'playing' ELSE 'idle' END,(SELECT json_group_array(name) FROM (SELECT name FROM libraries WHERE json_array_length(m.allowed_libraries)=0 OR id IN(SELECT value FROM json_each(m.allowed_libraries)) ORDER BY name,id)),` + sortExpr + ` FROM accounts a JOIN direct_memberships m ON m.account_id=a.id LEFT JOIN access_limits l ON l.account_id=a.id LEFT JOIN account_stream_limits sl ON sl.account_id=a.id JOIN playback_owner_policy stream_policy ON stream_policy.singleton=1 WHERE ` + pred + ` ORDER BY ` + sortExpr + ` ` + q.Direction + `,a.id ` + q.Direction + ` LIMIT ?`
		rows, e := tx.QueryContext(ctx, query, pageArgs...)
		if e != nil {
			return e
		}
		defer rows.Close()
		var lastCursor memberTableCursor
		for rows.Next() {
			var row MemberRow
			var libs, names, libraryNames, key string
			if e = rows.Scan(&row.HasMoveCode, &row.AccountID, &row.Username, &row.Email, &row.Role, &row.Revision, &row.LimitsRevision, &row.Disabled, &row.Status, &libs, &row.MaxStreams, &row.MaxStreamsMode, &row.ServerMaxStreams, &row.ProfileCount, &names, &row.LastActiveAt, &row.LiveState, &libraryNames, &key); e != nil {
				return e
			}
			var person *int
			if row.MaxStreamsMode == streamlimits.Limit {
				person = &row.MaxStreams
			}
			row.EffectiveMaxStreams = streamlimits.Effective(person, row.ServerMaxStreams)
			if e = json.Unmarshal([]byte(libs), &row.AllowedLibraries); e != nil {
				return e
			}
			if e = json.Unmarshal([]byte(names), &row.ProfileNames); e != nil {
				return e
			}
			if e = json.Unmarshal([]byte(libraryNames), &row.LibraryNames); e != nil {
				return e
			}
			if len(out.Items) == limit {
				raw, _ := json.Marshal(lastCursor)
				out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
				break
			}
			out.Items = append(out.Items, row)
			lastCursor = memberTableCursor{digest, key, row.AccountID}
		}
		if e = rows.Err(); e != nil {
			return fmt.Errorf("people page: %w", e)
		}
		return nil
	})
	return
}
