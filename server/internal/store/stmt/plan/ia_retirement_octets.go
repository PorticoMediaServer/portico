package plan

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"reflect"
	"strings"
)

const iaRetirementSessionByteSQL = `SELECT octet_length(id),octet_length(session_hash),octet_length(account_id),octet_length(profile_id),octet_length(asset_id),octet_length(state),octet_length(grant_hash),octet_length(grant_token),octet_length(expires_at),octet_length(request_id),octet_length(mode),octet_length(mp_retire_request_id),octet_length(mp_exit_supervisor_incarnation),octet_length(mp_exit_request_id),octet_length(mp_execution_id),octet_length(mp_exit_digest),octet_length(mp_command_incarnation),octet_length(mp_user_root_id),octet_length(mp_owner_member_incarnation),octet_length(mp_owner_profile_lifetime),octet_length(mp_reclaim_operation_id),octet_length(mp_reclaim_request_digest),octet_length(mp_reclaim_result_digest),octet_length(mp_reclaim_result_payload),octet_length(mp_retirement_state) FROM playback_sessions INDEXED BY sqlite_autoindex_playback_sessions_1 WHERE id=? AND generation=? AND mp_revision=? AND mp_execution_id=?`

var iaRetirementSessionByteTypes = map[string]string{"id": "TEXT", "session_hash": "TEXT", "account_id": "TEXT", "profile_id": "TEXT", "asset_id": "TEXT", "state": "TEXT", "grant_hash": "TEXT", "grant_token": "TEXT", "expires_at": "TEXT", "request_id": "TEXT", "mode": "TEXT", "mp_retire_request_id": "TEXT", "mp_exit_supervisor_incarnation": "BLOB", "mp_exit_request_id": "TEXT", "mp_execution_id": "TEXT", "mp_exit_digest": "TEXT", "mp_command_incarnation": "BLOB", "mp_user_root_id": "TEXT", "mp_owner_member_incarnation": "TEXT", "mp_owner_profile_lifetime": "TEXT", "mp_reclaim_operation_id": "BLOB", "mp_reclaim_request_digest": "BLOB", "mp_reclaim_result_digest": "BLOB", "mp_reclaim_result_payload": "BLOB", "mp_retirement_state": "TEXT"}

func init() {
	pointOctets.add(octetShape{"ia.retire.unit.session_bytes", iaRetirementSessionByteSQL, "IA retirement requires the exact complete session byte point", func(d stmt.Definition) bool {
		return d.ID == "ia.retire.unit.session_bytes" && d.SQL == iaRetirementSessionByteSQL && d.Kind == stmt.Read && reflect.DeepEqual(d.Params, []stmt.Param{{Name: "session", Type: stmt.Text}, {Name: "generation", Type: stmt.Integer}, {Name: "revision", Type: stmt.Integer}, {Name: "execution", Type: stmt.Text}}) && reflect.DeepEqual(d.Plan, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_sessions_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "playback_sessions", Name: "sqlite_autoindex_playback_sessions_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}})
	}, auditIARetirementSessionBytes, func(eqp Rows) error {
		if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != "SEARCH playback_sessions USING INDEX sqlite_autoindex_playback_sessions_1 (id=?)" {
			return fmt.Errorf("IA retirement: exact session SEARCH required")
		}
		return nil
	}})
}
func auditIARetirementSessionBytes(ctx context.Context, c *sql.Conn) error {
	f, err := query(ctx, c, "PRAGMA function_list")
	if err != nil {
		return err
	}
	native := 0
	for _, r := range f.Values {
		if len(r) != 6 {
			return fmt.Errorf("IA retirement: native functions")
		}
		n, ok := r[0].(string)
		if ok && strings.EqualFold(n, "octet_length") {
			if r[1] != int64(1) || r[4] != int64(1) {
				return fmt.Errorf("IA retirement: shadowed byte function")
			}
			native++
		}
	}
	if native != 1 {
		return fmt.Errorf("IA retirement: native octet_length missing")
	}
	cols, err := query(ctx, c, "PRAGMA main.table_xinfo(playback_sessions)")
	if err != nil {
		return err
	}
	seen := 0
	keys := 0
	for _, r := range cols.Values {
		if len(r) != 7 {
			return fmt.Errorf("IA retirement: native columns")
		}
		name, ok := r[1].(string)
		if !ok {
			return fmt.Errorf("IA retirement: native column")
		}
		if r[6] == int64(0) && (r[2] == "TEXT" || r[2] == "BLOB") {
			want, ok := iaRetirementSessionByteTypes[name]
			if !ok || r[2] != want {
				return fmt.Errorf("IA retirement: omitted variable column %s", name)
			}
			seen++
		}
		if r[5] != int64(0) {
			if name != "id" || r[5] != int64(1) || r[2] != "TEXT" || r[6] != int64(0) {
				return fmt.Errorf("IA retirement: native session key")
			}
			keys++
		}
	}
	if seen != len(iaRetirementSessionByteTypes) || keys != 1 {
		return fmt.Errorf("IA retirement: incomplete variable-byte inventory")
	}
	return nil
}
