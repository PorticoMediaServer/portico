package administration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
)

type LiveSourceChoice struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Group    string `json:"group"`
	Channels int64  `json:"channels"`
}
type LiveSourceChoicePage struct {
	SourceID   string             `json:"sourceId"`
	Generation string             `json:"generation"`
	Kind       string             `json:"kind"`
	Total      int64              `json:"total"`
	Items      []LiveSourceChoice `json:"items"`
	NextCursor string             `json:"nextCursor"`
}

// SourceChoices includes excluded channels: the owner can select them again
// without clearing the list and importing the whole line-up first.
func (s *Service) SourceChoices(ctx context.Context, auth Authorize, source, kind, cursor, query string, limit int) (LiveSourceChoicePage, error) {
	out := LiveSourceChoicePage{SourceID: source, Kind: kind, Items: []LiveSourceChoice{}}
	size, err := pageSize(limit)
	if err != nil || !safeText(query, 256) || (kind != "groups" && kind != "channels") {
		return out, ErrInput
	}
	err = s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		if _, _, e := liveSourceExists(ctx, tx, source); e != nil {
			return e
		}
		if e := tx.QueryRowContext(ctx, `SELECT active_generation FROM live_sources WHERE id=?`, source).Scan(&out.Generation); e != nil {
			return e
		}
		scope := digestOf([]string{source, out.Generation, kind, query})
		afterID, e := decodeChoiceCursor(scope, cursor)
		if e != nil {
			return e
		}
		var rows *sql.Rows
		if kind == "groups" {
			if e = tx.QueryRowContext(ctx, `SELECT count(DISTINCT group_name) FROM live_channel_choices WHERE generation_id=? AND group_name<>'' AND instr(lower(group_name),lower(?))>0`, out.Generation, query).Scan(&out.Total); e != nil {
				return e
			}
			rows, e = tx.QueryContext(ctx, `SELECT group_name,group_name,'',count(*) FROM live_channel_choices WHERE generation_id=? AND group_name>? AND instr(lower(group_name),lower(?))>0 GROUP BY group_name ORDER BY group_name LIMIT ?`, out.Generation, afterID, query, size+1)
		} else {
			if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM live_channel_choices WHERE generation_id=? AND instr(lower(name||' '||group_name),lower(?))>0`, out.Generation, query).Scan(&out.Total); e != nil {
				return e
			}
			rows, e = tx.QueryContext(ctx, `SELECT channel_key,name,group_name,0 FROM live_channel_choices WHERE generation_id=? AND channel_key>? AND instr(lower(name||' '||group_name),lower(?))>0 ORDER BY channel_key LIMIT ?`, out.Generation, afterID, query, size+1)
		}
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var v LiveSourceChoice
			if e = rows.Scan(&v.ID, &v.Name, &v.Group, &v.Channels); e != nil {
				return e
			}
			if len(out.Items) == size {
				out.NextCursor = encodeChoiceCursor(scope, out.Items[size-1].ID)
				break
			}
			out.Items = append(out.Items, v)
		}
		return rows.Err()
	})
	return out, err
}

func encodeChoiceCursor(scope, last string) string {
	prefix, _ := hex.DecodeString(scope)
	return base64.RawURLEncoding.EncodeToString(append(prefix, []byte(last)...))
}
func decodeChoiceCursor(scope, token string) (string, error) {
	if token == "" {
		return "", nil
	}
	if len(token) > 512 {
		return "", ErrCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	prefix, _ := hex.DecodeString(scope)
	if err != nil || len(raw) < 33 || len(raw) > 288 || !bytes.Equal(raw[:32], prefix) || !safeText(string(raw[32:]), 256) {
		return "", ErrCursor
	}
	return string(raw[32:]), nil
}
