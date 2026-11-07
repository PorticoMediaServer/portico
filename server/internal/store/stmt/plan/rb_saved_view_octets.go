package plan

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const rbSavedViewOwnerOctetsSQL = "SELECT octet_length(r.name),octet_length(r.summary),octet_length(r.definition) FROM saved_resources AS r INDEXED BY sqlite_autoindex_saved_resources_1 WHERE r.id=? AND r.kind='view' AND r.deleted=0 AND r.owner_authority=? AND r.owner_account=? AND r.owner_profile=?"
const rbSavedViewSharedOctetsSQL = `SELECT octet_length(r.name),octet_length(r.summary),octet_length(r.definition) FROM saved_resources AS r INDEXED BY sqlite_autoindex_saved_resources_1
 LEFT JOIN saved_resource_shares AS s INDEXED BY sqlite_autoindex_saved_resource_shares_1 ON s.resource_id=r.id AND s.authority=? AND s.account_id=? AND s.profile_id=?
 WHERE r.id=? AND r.kind='view' AND r.deleted=0 AND s.role IN('viewer','editor')`

func init() {
	for _, owner := range []bool{true, false} {
		owner := owner
		kind, sqlText := "shared", rbSavedViewSharedOctetsSQL
		if owner {
			kind, sqlText = "owner", rbSavedViewOwnerOctetsSQL
		}
		id := stmt.ID("read.browse.rb.saved.view.resource." + kind + ".bytes")
		pointOctets.add(octetShape{id: id, sql: sqlText, mismatch: "exact saved-view owner/shared native octet point required",
			definition: func(d stmt.Definition) bool { return rbSavedViewOctetDefinition(d, owner) },
			audit: func(ctx context.Context, c *sql.Conn) error {
				if e := auditClosedOctetKey(ctx, c, "saved_resources", "sqlite_autoindex_saved_resources_1", []string{"id"}); e != nil {
					return e
				}
				if !owner {
					return auditClosedOctetKey(ctx, c, "saved_resource_shares", "sqlite_autoindex_saved_resource_shares_1", []string{"resource_id", "authority", "account_id", "profile_id"})
				}
				return nil
			},
			point: func(eqp Rows) error {
				want := 1
				if !owner {
					want = 2
				}
				if len(eqp.Values) != want {
					return fmt.Errorf("saved-view octets: exact complete PK points required")
				}
				resource, share := false, owner
				for _, row := range eqp.Values {
					if len(row) != 4 {
						return fmt.Errorf("saved-view octets: invalid EQP")
					}
					detail, ok := row[3].(string)
					if !ok {
						return fmt.Errorf("saved-view octets: invalid EQP")
					}
					if detail == "SEARCH r USING INDEX sqlite_autoindex_saved_resources_1 (id=?)" {
						resource = true
						continue
					}
					if !owner && strings.HasPrefix(detail, "SEARCH s USING INDEX sqlite_autoindex_saved_resource_shares_1 (resource_id=? AND authority=? AND account_id=? AND profile_id=?)") {
						share = true
						continue
					}
					return fmt.Errorf("saved-view octets: unexpected access %s", detail)
				}
				if !resource || !share {
					return fmt.Errorf("saved-view octets: missing complete PK")
				}
				return nil
			},
		})
	}
}
func rbSavedViewOctetDefinition(d stmt.Definition, owner bool) bool {
	kind, sqlText := "shared", rbSavedViewSharedOctetsSQL
	if owner {
		kind, sqlText = "owner", rbSavedViewOwnerOctetsSQL
	}
	if d.ID != stmt.ID("read.browse.rb.saved.view.resource."+kind+".bytes") || d.SQL != sqlText || d.Kind != stmt.Read || d.Owner != "store/read" {
		return false
	}
	p := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	params := []stmt.Param{p("authority"), p("share_account"), p("share_profile"), p("id")}
	key := func(table, name string, cols ...string) stmt.AutoIndexBinding {
		b := stmt.AutoIndexBinding{Table: table, Name: name, Origin: "pk"}
		for _, c := range cols {
			b.Columns = append(b.Columns, stmt.IndexColumn{Name: c, Collation: "BINARY"})
		}
		return b
	}
	r := key("saved_resources", "sqlite_autoindex_saved_resources_1", "id")
	s := key("saved_resource_shares", "sqlite_autoindex_saved_resource_shares_1", "resource_id", "authority", "account_id", "profile_id")
	proof := stmt.Plan{Hot: true, Indexes: []string{r.Name, s.Name}, AutoIndexes: []stmt.AutoIndexBinding{r, s}, Seeks: []string{"id=?", "resource_id=? AND authority=? AND account_id=? AND profile_id=?"}}
	if owner {
		params = []stmt.Param{p("id"), p("owner_authority"), p("owner_account"), p("owner_profile")}
		proof = stmt.Plan{Hot: true, Indexes: []string{r.Name}, AutoIndexes: []stmt.AutoIndexBinding{r}, Seeks: []string{"id=?"}}
	}
	return reflect.DeepEqual(d.Params, params) && reflect.DeepEqual(d.Plan, proof)
}
