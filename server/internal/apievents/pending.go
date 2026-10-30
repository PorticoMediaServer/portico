package apievents

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/entityid"
)

// Flush publishes bounded, coalesced hints. It runs only after a committed
// change wakes the feed. The source revision and the hint are one transaction.
func Flush(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT audience,type,resource_kind,resource_id,revision,data FROM api_change_pending ORDER BY audience,type,resource_kind,resource_id LIMIT ?`, limit)
	if err != nil {
		return 0, err
	}
	type hint struct{ audience, kind, resource, id, revision, data string }
	var hints []hint
	for rows.Next() {
		var h hint
		if err = rows.Scan(&h.audience, &h.kind, &h.resource, &h.id, &h.revision, &h.data); err != nil {
			break
		}
		hints = append(hints, h)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, h := range hints {
		audience := h.audience
		if strings.HasPrefix(audience, "personal:") {
			key := strings.TrimPrefix(audience, "personal:")
			raw, e := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(key, "viewer:"))
			var parts []string
			if e == nil && strings.HasPrefix(key, "viewer:") && json.Unmarshal(raw, &parts) == nil && len(parts) == 3 {
				audience = ProfileAudience(parts[0], parts[1], parts[2])
			} else {
				audience = "legacy:" + key
			}
		}
		var data any
		if json.Valid([]byte(h.data)) {
			data = json.RawMessage(publicTitles(h.data))
		}
		if err = Append(tx, audience, h.kind, h.resource, h.id, h.revision, data); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM api_change_pending WHERE audience=? AND type=? AND resource_kind=? AND resource_id=?`, h.audience, h.kind, h.resource, h.id); err != nil {
			return 0, err
		}
	}
	return len(hints), nil
}

// publicTitles turns a personal hint's titles path (hex public ids, the title
// first and then its parent and grandparent, as the schema's triggers write
// it) into the public ids a client knows them by. Anything it cannot read is
// left out, which means "any title".
func publicTitles(data string) string {
	if !strings.Contains(data, `"titles"`) {
		return data
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(data), &fields) != nil {
		return data
	}
	var path string
	if json.Unmarshal(fields["titles"], &path) != nil {
		return data
	}
	delete(fields, "titles")
	ids := []string{}
	for _, part := range strings.Split(path, "/") {
		raw, err := hex.DecodeString(part)
		if err != nil || len(raw) != entityid.Size {
			ids = nil
			break
		}
		ids = append(ids, entityid.Encode(raw))
	}
	if len(ids) > 0 {
		fields["titles"], _ = json.Marshal(ids)
	}
	out, err := json.Marshal(fields)
	if err != nil {
		return data
	}
	return string(out)
}
