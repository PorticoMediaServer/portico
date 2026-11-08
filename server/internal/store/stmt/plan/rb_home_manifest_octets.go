package plan

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const rbHomeManifestListSQL = "SELECT octet_length(value) FROM preference_list_items WHERE authority=? AND account_id=? AND profile_id=? AND scope=? AND device_key=? AND key=? AND position=?"
const rbHomeManifestLibrarySQL = "SELECT octet_length(name),octet_length(kind) FROM libraries INDEXED BY sqlite_autoindex_libraries_1 WHERE id=?"

func init() {
	for _, list := range []bool{true, false} {
		list := list
		name, sqlText := "library.bytes", rbHomeManifestLibrarySQL
		if list {
			name, sqlText = "list.bytes", rbHomeManifestListSQL
		}
		pointOctets.add(octetShape{id: stmt.ID("read.browse.rb.home.manifest." + name), sql: sqlText, mismatch: "exact Home manifest native byte point required",
			definition: func(d stmt.Definition) bool { return rbHomeManifestOctetDefinition(d, list) },
			audit: func(ctx context.Context, c *sql.Conn) error {
				if list {
					return auditClosedOctetKey(ctx, c, "preference_list_items", "", []string{"authority", "account_id", "profile_id", "scope", "device_key", "key", "position"})
				}
				return auditClosedOctetKey(ctx, c, "libraries", "sqlite_autoindex_libraries_1", []string{"id"})
			},
			point: func(eqp Rows) error {
				want := "SEARCH libraries USING INDEX sqlite_autoindex_libraries_1 (id=?)"
				if list {
					want = "SEARCH preference_list_items USING PRIMARY KEY (authority=? AND account_id=? AND profile_id=? AND scope=? AND device_key=? AND key=? AND position=?)"
				}
				if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != want {
					return fmt.Errorf("Home manifest bytes: exact complete PK SEARCH required")
				}
				return nil
			},
		})
	}
}
func rbHomeManifestOctetDefinition(d stmt.Definition, list bool) bool {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	name, sqlText := "library.bytes", rbHomeManifestLibrarySQL
	params := []stmt.Param{text("library")}
	proof := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_libraries_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "libraries", Name: "sqlite_autoindex_libraries_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}, Seeks: []string{"id=?"}}
	if list {
		name, sqlText = "list.bytes", rbHomeManifestListSQL
		params = []stmt.Param{text("authority"), text("account"), text("profile"), text("scope"), text("device"), text("key"), {Name: "position", Type: stmt.Integer}}
		proof = stmt.Plan{Hot: true, PrimaryKeys: []string{"preference_list_items"}, Seeks: []string{"authority=? AND account_id=? AND profile_id=? AND scope=? AND device_key=? AND key=? AND position=?"}}
	}
	return d.ID == stmt.ID("read.browse.rb.home.manifest."+name) && d.SQL == sqlText && d.Kind == stmt.Read && d.Owner == "store/read" && reflect.DeepEqual(d.Params, params) && reflect.DeepEqual(d.Plan, proof)
}
