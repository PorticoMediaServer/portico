package read

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
)

const rsReadingAssetKeySQL = `SELECT token,size,modified_ns FROM catalog_assets WHERE id=? AND library_id=?`
const rsReadingAssetKeyOctetsSQL = `SELECT octet_length(token) FROM catalog_assets WHERE id=? AND library_id=?`
const rsReadingAssetSQL = `SELECT token,path,available,size,modified_ns FROM catalog_assets WHERE id=? AND library_id=?`
const rsReadingAssetOctetsSQL = `SELECT octet_length(token),octet_length(path) FROM catalog_assets WHERE id=? AND library_id=?`
const rsReadingLibrarySourceSQL = `SELECT root,configured_root,enabled,health,incarnation,generation FROM library_sources INDEXED BY sqlite_autoindex_library_sources_1 WHERE id=? AND library_id=?`
const rsReadingLibrarySourceOctetsSQL = `SELECT octet_length(root),octet_length(configured_root),octet_length(health),octet_length(incarnation) FROM library_sources INDEXED BY sqlite_autoindex_library_sources_1 WHERE id=? AND library_id=?`
const rsReadingInventoryBase = `SELECT 1 FROM inventory_objects AS o INDEXED BY inventory_objects_reading_asset_object CROSS JOIN library_sources AS s INDEXED BY library_sources_library ON s.id=o.source_id WHERE o.asset_id=? AND s.library_id=?`
const rsReadingInventoryReady = rsReadingInventoryBase + ` AND o.retired=0 AND o.state='available' AND o.unsupported_reason='' AND s.enabled=1 AND s.health NOT IN('root_changed','removing') AND o.root_incarnation=s.incarnation LIMIT 1`
const rsReadingSourceIDsSQL = `SELECT id FROM library_sources INDEXED BY library_sources_library WHERE library_id=? ORDER BY id LIMIT ?`
const rsReadingVerifySourceSQL = `SELECT 1 FROM library_sources INDEXED BY sqlite_autoindex_library_sources_1 WHERE id=? AND library_id=? AND root=? AND configured_root=? AND enabled=? AND health=? AND incarnation=? AND generation=?`
const rsReadingVerifyAssetSQL = `SELECT 1 FROM catalog_assets WHERE id=? AND library_id=? AND token=? AND path=? AND available=? AND size=? AND modified_ns=?`

func init() {
	registerRSBytePoint("rs.reading.asset_key", rsBytePoint{lengths: "rs.reading.asset_key_octets", columns: []int{0}})
	registerRSBytePoint("rs.reading.asset", rsBytePoint{lengths: "rs.reading.asset_octets", columns: []int{0, 1}})
	registerRSBytePoint("rs.reading.library_source", rsBytePoint{lengths: "rs.reading.library_source_octets", columns: []int{0, 1, 3, 4}})
}
func rsReadingInventoryDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, hit []any, point bool, p stmt.Plan) {
		miss := append([]any(nil), hit...)
		if len(params) > 0 && params[0].Type == stmt.Integer {
			miss[0] = int64(999999)
		} else {
			miss[0] = "missing"
		}
		cohort := "empty"
		if point {
			cohort = "present"
		}
		out = append(out, stmt.Definition{ID: readStatementID("rs.reading." + name), SQL: sql, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_reading.go:RSReadingReader.Facts"}, Params: params, Cohorts: []stmt.Cohort{{Name: cohort, Bindings: hit}, {Name: "absent", Bindings: miss}}, Plan: p})
	}
	pkAsset := stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}}
	pkSource := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_library_sources_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "library_sources", Name: "sqlite_autoindex_library_sources_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}}
	add("library", `SELECT kind FROM libraries INDEXED BY sqlite_autoindex_libraries_1 WHERE id=?`, []stmt.Param{text("library")}, []any{"movies"}, true, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_libraries_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "libraries", Name: "sqlite_autoindex_libraries_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}})
	add("publications", rsReadingPublicationsSQL, []stmt.Param{i("item"), i("limit")}, []any{int64(1101), int64(rsReadingPublicationCap + 1)}, true, stmt.Plan{Hot: true, PrimaryKeys: []string{"reading_publications"}})
	inventoryPlan := stmt.Plan{Hot: true, Indexes: []string{"inventory_objects_reading_asset_object", "library_sources_library"}}
	for _, p := range []struct{ name, sql string }{{"inventory_exists", rsReadingInventoryBase + ` LIMIT 1`}, {"inventory_ready", rsReadingInventoryReady}} {
		add(p.name, p.sql, []stmt.Param{text("token"), text("library")}, []any{"asset", "movies"}, true, inventoryPlan)
	}
	add("retired", `SELECT item_id FROM dvr_catalog_retirements WHERE item_id=?`, []stmt.Param{i("item")}, []any{int64(1101)}, false, stmt.Plan{Hot: true, PrimaryKeys: []string{"dvr_catalog_retirements"}})
	for _, p := range []struct{ name, sql string }{{"asset_key", rsReadingAssetKeySQL}, {"asset_key_octets", rsReadingAssetKeyOctetsSQL}, {"asset", rsReadingAssetSQL}, {"asset_octets", rsReadingAssetOctetsSQL}} {
		add(p.name, p.sql, []stmt.Param{i("asset"), i("library")}, []any{int64(1), int64(1)}, true, pkAsset)
	}
	for _, p := range []struct{ name, sql string }{{"library_source", rsReadingLibrarySourceSQL}, {"library_source_octets", rsReadingLibrarySourceOctetsSQL}} {
		add(p.name, p.sql, []stmt.Param{text("source"), text("library")}, []any{"source", "movies"}, true, pkSource)
	}
	add("source_ids", rsReadingSourceIDsSQL, []stmt.Param{text("library"), i("limit")}, []any{"movies", int64(rsReadingPublicationCap + 1)}, true, stmt.Plan{Hot: true, Indexes: []string{"library_sources_library"}})
	add("verify_source", rsReadingVerifySourceSQL, []stmt.Param{text("source"), text("library"), text("root"), text("configured"), i("enabled"), text("health"), text("incarnation"), i("generation")}, []any{"source", "movies", "/media", "/media", int64(1), "ready", "inc", int64(1)}, true, pkSource)
	add("verify_asset", rsReadingVerifyAssetSQL, []stmt.Param{i("asset"), i("library"), text("token"), text("path"), i("available"), i("size"), i("mtime")}, []any{int64(1), int64(1), "asset", "/media/title.mkv", int64(1), int64(1000), int64(1)}, true, pkAsset)
	add("local", rsReadingLocalSQL, []stmt.Param{text("token"), text("library"), i("size"), i("mtime")}, []any{"asset", "movies", int64(1000), int64(1)}, true, inventoryPlan)
	return out
}

type rsReadingInventory struct {
	ids     [][]any
	sources map[string][]any
	assets  map[int64][]any
	states  map[int64][2]bool
	retired bool
}

func rsReadingRootMatch(path, root string) bool {
	root = strings.TrimRight(root, "/\\")
	return strings.HasPrefix(path, root+"/") || strings.HasPrefix(path, root+"\\")
}
func (w *rsReadingInventory) available(q browseQuery, asset int64, library string) (bool, error) {
	exists, err := q("rs.reading.inventory_exists", w.assets[asset][0], library)
	if err != nil {
		return false, err
	}
	ready, err := q("rs.reading.inventory_ready", w.assets[asset][0], library)
	if err != nil {
		return false, err
	}
	w.states[asset] = [2]bool{len(exists) > 0, len(ready) > 0}
	if w.retired {
		return false, nil
	}
	if len(exists) > 0 {
		return len(ready) > 0, nil
	}
	a := w.assets[asset]
	if a[2].(int64) == 0 {
		return false, nil
	}
	matches, goodMatch, anyGood := false, true, false
	for _, s := range w.sources {
		good := s[2].(int64) != 0 && s[3] != "root_changed" && s[3] != "removing"
		anyGood = anyGood || good
		if rsReadingRootMatch(a[1].(string), s[0].(string)) || rsReadingRootMatch(a[1].(string), s[1].(string)) {
			matches = true
			goodMatch = goodMatch && good
		}
	}
	if matches {
		return goodMatch, nil
	}
	return len(w.sources) == 0 || anyGood, nil
}
func (w *rsReadingInventory) verify(q browseQuery, in ReadingFactsInput, library string) error {
	ids, err := q("rs.reading.source_ids", library, int64(rsReadingPublicationCap+1))
	if err != nil {
		return err
	}
	if !equalSourceRows(ids, w.ids) {
		return ErrStale
	}
	gone, err := q("rs.reading.retired", in.Item)
	if err != nil {
		return err
	}
	if (len(gone) > 0) != w.retired {
		return ErrStale
	}
	for id, s := range w.sources {
		rows, err := q("rs.reading.verify_source", id, library, s[0], s[1], s[2], s[3], s[4], s[5])
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			return ErrStale
		}
	}
	for id, a := range w.assets {
		rows, err := q("rs.reading.verify_asset", id, in.View.Library, a[0], a[1], a[2], a[3], a[4])
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			return ErrStale
		}
		state := w.states[id]
		exists, err := q("rs.reading.inventory_exists", a[0], library)
		if err != nil {
			return err
		}
		ready, err := q("rs.reading.inventory_ready", a[0], library)
		if err != nil {
			return err
		}
		if state != [2]bool{len(exists) > 0, len(ready) > 0} {
			return ErrStale
		}
	}
	return nil
}
func equalSourceRows(a, b [][]any) bool {
	if len(a) != len(b) {
		return false
	}
	for n := range a {
		if !equalSource(a[n], b[n]) {
			return false
		}
	}
	return true
}
func rsReadingOctets(q browseQuery, name string, args ...any) (int64, error) {
	rows, err := q(name, args...)
	if err != nil {
		return 0, err
	}
	if len(rows) != 1 {
		return 0, ErrStale
	}
	return rsPointBytes(rows[0])
}
func rsReadingProspectiveBytes(total, bytes, limit int64) (int64, error) {
	if bytes < 0 || bytes > limit-total {
		return 0, governor.ErrSnapshotBudget
	}
	return total + bytes, nil
}
