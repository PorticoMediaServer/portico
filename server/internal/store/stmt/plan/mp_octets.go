package plan

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// MP's three durable heads publish only after a same-snapshot octet preflight.
// Each point selects scalar octet_length() values for the exact payload
// projection, then QueryExactBytes repeats both points in the admitted business
// snapshot. These literals are compiled recipe data, never caller SQL, an
// allowance list or a separate resource ledger.
const mpQueueOctetsID stmt.ID = "read.foreground.mp.queue_head_octets"
const mpSessionOctetsID stmt.ID = "read.foreground.mp.session_head_octets"
const mpPreparedOctetsID stmt.ID = "read.foreground.mp.prepared_job_octets"

const mpQueueOctetsSQL = `SELECT octet_length(q.id),octet_length(q.current_entry_id),octet_length(q.repeat),octet_length(q.session_id),octet_length(q.owner_kind),octet_length(q.owner_id),octet_length(q.authority),octet_length(q.account_id),octet_length(q.profile_id),octet_length(f.id),octet_length(f.device_id) FROM queues_v1 AS q INDEXED BY sqlite_autoindex_queues_v1_1
CROSS JOIN authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1 ON t.token_hash=?
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
WHERE q.id=? AND t.retired=0 AND t.generation=f.current_generation
AND f.revoked=0 AND f.pin_gate=0 AND f.authority='local'
AND q.owner_kind='device' AND q.owner_id=f.device_id AND q.authority=f.authority AND q.account_id=f.account_id AND q.profile_id=f.profile_id`

const mpSessionOctetsSQL = `SELECT octet_length(s.id),octet_length(s.kind),octet_length(s.role),octet_length(s.state),octet_length(s.version_id),octet_length(s.queue_id),octet_length(s.entry_id),octet_length(s.media_session_id),octet_length(s.end_reason),octet_length(s.device_id),octet_length(f.id),octet_length(e.sort_key),octet_length(e.public_id),octet_length(e.title),octet_length(s.account_id),octet_length(s.profile_id),octet_length(s.authority) FROM playback_v1_sessions AS s INDEXED BY sqlite_autoindex_playback_v1_sessions_1
CROSS JOIN authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1 ON t.token_hash=?
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
CROSS JOIN catalog_entities AS e ON e.id=s.item_id
WHERE s.id=? AND s.item_id=? AND e.library_id=? AND e.kind=? AND e.retired=0
AND t.retired=0 AND t.generation=f.current_generation AND f.revoked=0 AND f.pin_gate=0 AND f.authority='local'
AND s.device_id=f.device_id AND s.account_id=f.account_id AND s.profile_id=f.profile_id AND s.authority=f.authority
AND s.role='local' AND s.kind IN('vod','audio') AND s.group_id='' AND s.transfer_id='' AND s.channel_id=''`

const mpPreparedOctetsSQL = `SELECT octet_length(j.id),octet_length(j.state),octet_length(j.phase),octet_length(j.error_code),octet_length(j.version_id),octet_length(j.asset_id),octet_length(j.target_id),octet_length(j.profile_id),octet_length(j.source_revision),octet_length(e.sort_key),octet_length(e.public_id),octet_length(e.title),octet_length(j.library_id) FROM prepared_media_jobs AS j INDEXED BY sqlite_autoindex_prepared_media_jobs_1
CROSS JOIN catalog_entities AS e ON e.id=j.item_id
WHERE j.id=? AND j.item_id=? AND e.library_id=? AND e.kind=? AND j.library_id=? AND e.retired=0`

type mpOctetPoint struct {
	id       stmt.ID
	sql      string
	params   []stmt.Param
	indexes  []string
	autos    []stmt.AutoIndexBinding
	primary  []string
	searches []string
	entity   bool
}

func mpQueueIndex() stmt.AutoIndexBinding {
	return stmt.AutoIndexBinding{Table: "queues_v1", Name: "sqlite_autoindex_queues_v1_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}
}

func mpTokenIndex() stmt.AutoIndexBinding {
	return stmt.AutoIndexBinding{Table: "authorization_family_tokens", Name: "sqlite_autoindex_authorization_family_tokens_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "token_hash", Collation: "BINARY"}}}
}

func mpFamilyIndex() stmt.AutoIndexBinding {
	return stmt.AutoIndexBinding{Table: "authorization_session_families", Name: "sqlite_autoindex_authorization_session_families_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}
}

var mpOctetPoints = []mpOctetPoint{
	{
		id:      mpQueueOctetsID,
		sql:     mpQueueOctetsSQL,
		params:  []stmt.Param{{Name: "token", Type: stmt.Text}, {Name: "queue", Type: stmt.Text}},
		indexes: []string{"sqlite_autoindex_queues_v1_1", "sqlite_autoindex_authorization_family_tokens_1", "sqlite_autoindex_authorization_session_families_1"},
		autos:   []stmt.AutoIndexBinding{mpQueueIndex(), mpTokenIndex(), mpFamilyIndex()},
		searches: []string{
			"SEARCH q USING INDEX sqlite_autoindex_queues_v1_1 (id=?)",
			"SEARCH t USING INDEX sqlite_autoindex_authorization_family_tokens_1 (token_hash=?)",
			"SEARCH f USING INDEX sqlite_autoindex_authorization_session_families_1 (id=?)",
		},
	},
	{
		id:      mpSessionOctetsID,
		sql:     mpSessionOctetsSQL,
		params:  []stmt.Param{{Name: "token", Type: stmt.Text}, {Name: "session", Type: stmt.Text}, {Name: "item", Type: stmt.Integer}, {Name: "library", Type: stmt.Integer}, {Name: "kind", Type: stmt.Integer}},
		indexes: []string{"sqlite_autoindex_playback_v1_sessions_1", "sqlite_autoindex_authorization_family_tokens_1", "sqlite_autoindex_authorization_session_families_1"},
		autos:   []stmt.AutoIndexBinding{{Table: "playback_v1_sessions", Name: "sqlite_autoindex_playback_v1_sessions_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}, mpTokenIndex(), mpFamilyIndex()},
		primary: []string{"e"},
		searches: []string{
			"SEARCH s USING INDEX sqlite_autoindex_playback_v1_sessions_1 (id=?)",
			"SEARCH t USING INDEX sqlite_autoindex_authorization_family_tokens_1 (token_hash=?)",
			"SEARCH f USING INDEX sqlite_autoindex_authorization_session_families_1 (id=?)",
			"SEARCH e USING INTEGER PRIMARY KEY (rowid=?)",
		},
		entity: true,
	},
	{
		id:      mpPreparedOctetsID,
		sql:     mpPreparedOctetsSQL,
		params:  []stmt.Param{{Name: "job", Type: stmt.Text}, {Name: "item", Type: stmt.Integer}, {Name: "library", Type: stmt.Integer}, {Name: "kind", Type: stmt.Integer}, {Name: "library_name", Type: stmt.Text}},
		indexes: []string{"sqlite_autoindex_prepared_media_jobs_1"},
		autos:   []stmt.AutoIndexBinding{{Table: "prepared_media_jobs", Name: "sqlite_autoindex_prepared_media_jobs_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}},
		primary: []string{"e"},
		searches: []string{
			"SEARCH j USING INDEX sqlite_autoindex_prepared_media_jobs_1 (id=?)",
			"SEARCH e USING INTEGER PRIMARY KEY (rowid=?)",
		},
		entity: true,
	},
}

func mpOctetPointFor(id stmt.ID) (mpOctetPoint, bool) {
	for _, p := range mpOctetPoints {
		if p.id == id {
			return p, true
		}
	}
	return mpOctetPoint{}, false
}

func mpOctetDefinition(d stmt.Definition) bool {
	p, ok := mpOctetPointFor(d.ID)
	if !ok || d.SQL != p.sql || d.Kind != stmt.Read || !d.Plan.Hot ||
		!slices.Equal(d.Params, p.params) || !slices.Equal(d.Plan.Indexes, p.indexes) ||
		!reflect.DeepEqual(d.Plan.AutoIndexes, p.autos) || !slices.Equal(d.Plan.PrimaryKeys, p.primary) ||
		len(d.Plan.Seeks) != 0 || len(d.Plan.Waivers) != 0 || len(d.Plan.AllowedScans) != 0 ||
		d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" || d.Plan.NativeRead != nil {
		return false
	}
	for _, param := range d.Params {
		if param.Nullable {
			return false
		}
	}
	return true
}

func init() {
	for _, p := range mpOctetPoints {
		point := p
		pointOctets.add(octetShape{
			id: point.id, sql: point.sql,
			mismatch:   "MP head byte preflight requires the exact closed point definition",
			definition: mpOctetDefinition,
			audit: func(ctx context.Context, c *sql.Conn) error {
				return auditMPOctetPoint(ctx, c, point)
			},
			point: func(rows Rows) error { return checkMPOctetPoint(point, rows) },
		})
	}
}

func auditMPOctetPoint(ctx context.Context, conn *sql.Conn, p mpOctetPoint) error {
	// Reuse the shared native builtin guard: exactly one unshadowed one-argument
	// octet_length, as the other merged octet registries require.
	if err := auditBuiltinOctetLength(ctx, conn); err != nil {
		return err
	}
	for _, auto := range p.autos {
		if err := auditMPAutoindex(ctx, conn, auto); err != nil {
			return err
		}
	}
	if p.entity {
		info, err := query(ctx, conn, "PRAGMA main.table_xinfo(catalog_entities)")
		if err != nil {
			return err
		}
		found := false
		for _, row := range info.Values {
			if len(row) != 7 {
				return fmt.Errorf("MP preflight: invalid catalog entity columns")
			}
			if row[1] == "id" {
				found = row[2] == "INTEGER" && row[5] == int64(1) && row[6] == int64(0)
			}
		}
		if !found {
			return fmt.Errorf("MP preflight: exact integer catalog identity required")
		}
	}
	return nil
}

func auditMPAutoindex(ctx context.Context, conn *sql.Conn, b stmt.AutoIndexBinding) error {
	indexes, err := query(ctx, conn, "PRAGMA main.index_list("+b.Table+")")
	if err != nil {
		return err
	}
	found := false
	for _, row := range indexes.Values {
		if len(row) != 5 {
			return fmt.Errorf("MP preflight: unexpected native index metadata")
		}
		if row[1] == b.Name {
			if row[2] != int64(1) || row[3] != "pk" || row[4] != int64(0) {
				return fmt.Errorf("MP preflight: exact nonpartial primary key required")
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("MP preflight: pinned primary key missing")
	}
	info, err := query(ctx, conn, "PRAGMA main.index_xinfo("+b.Name+")")
	if err != nil {
		return err
	}
	if len(info.Values) != len(b.Columns)+1 {
		return fmt.Errorf("MP preflight: exact full primary key required")
	}
	for i, col := range b.Columns {
		row := info.Values[i]
		if len(row) != 6 || row[0] != int64(i) || row[2] != col.Name || row[3] != int64(0) || row[4] != col.Collation || row[5] != int64(1) {
			return fmt.Errorf("MP preflight: exact BINARY primary key required")
		}
	}
	tail := info.Values[len(b.Columns)]
	if len(tail) != 6 || tail[1] != int64(-1) || tail[5] != int64(0) {
		return fmt.Errorf("MP preflight: native rowid tail required")
	}
	return nil
}

func checkMPOctetPoint(p mpOctetPoint, eqp Rows) error {
	details := make([]string, 0, len(eqp.Values))
	for _, row := range eqp.Values {
		if len(row) != 4 {
			return fmt.Errorf("MP preflight: unexpected native EQP shape")
		}
		detail, ok := row[3].(string)
		if !ok {
			return fmt.Errorf("MP preflight: invalid native EQP detail")
		}
		details = append(details, detail)
	}
	for _, want := range p.searches {
		found := false
		for _, detail := range details {
			if strings.HasPrefix(detail, want) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("MP preflight: exact indexed point SEARCH required: %s", want)
		}
	}
	return nil
}
