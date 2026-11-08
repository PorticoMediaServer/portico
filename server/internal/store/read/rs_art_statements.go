package read

import (
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strconv"
)

const rsArtSourceSQL = `SELECT e.id,e.kind,e.retired,e.admission_own_age,e.metadata_incarnation,e.source_revision,e.identity_revision,e.metadata_revision,e.artwork_incarnation,e.artwork_revision,e.admission_revision,e.show_id,e.season_id,e.album_id,e.book_id,sh.id,sh.kind,sh.retired,sh.admission_own_age,sh.metadata_incarnation,sh.source_revision,sh.identity_revision,sh.metadata_revision,sh.artwork_incarnation,sh.artwork_revision,sh.admission_revision,sh.show_id,sh.season_id,sh.album_id,sh.book_id,se.id,se.kind,se.retired,se.admission_own_age,se.metadata_incarnation,se.source_revision,se.identity_revision,se.metadata_revision,se.artwork_incarnation,se.artwork_revision,se.admission_revision,se.show_id,se.season_id,se.album_id,se.book_id,al.id,al.kind,al.retired,al.admission_own_age,al.metadata_incarnation,al.source_revision,al.identity_revision,al.metadata_revision,al.artwork_incarnation,al.artwork_revision,al.admission_revision,al.show_id,al.season_id,al.album_id,al.book_id,bk.id,bk.kind,bk.retired,bk.admission_own_age,bk.metadata_incarnation,bk.source_revision,bk.identity_revision,bk.metadata_revision,bk.artwork_incarnation,bk.artwork_revision,bk.admission_revision,bk.show_id,bk.season_id,bk.album_id,bk.book_id
FROM catalog_entities AS e NOT INDEXED
LEFT JOIN catalog_entities AS sh NOT INDEXED ON sh.id=e.show_id AND sh.library_id=e.library_id AND sh.kind=? AND sh.retired=0
LEFT JOIN catalog_entities AS se NOT INDEXED ON se.id=e.season_id AND se.library_id=e.library_id AND se.kind=? AND se.retired=0 AND se.show_id=e.show_id
LEFT JOIN catalog_entities AS al NOT INDEXED ON al.id=e.album_id AND al.library_id=e.library_id AND al.kind=? AND al.retired=0
LEFT JOIN catalog_entities AS bk NOT INDEXED ON bk.id=e.book_id AND bk.library_id=e.library_id AND bk.kind=? AND bk.retired=0
WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0`
const rsArtPointSQL = `SELECT a.entity_id,own.kind,a.role,a.choice,a.digest,own.public_id,a.revision,sb.digest,sb.mime,sb.width,sb.height,sb.bytes,lb.digest,lb.mime,lb.width,lb.height,lb.bytes,sb.status,lb.status,a.source_fence,a.source_object_id,a.source_object_revision,o.id,o.revision,o.retired,o.root_incarnation,ls.incarnation,ls.library_id,cl.library_id,asset.available,asset.size,asset.modified_ns,o.size,o.modified_ns,s.retiring
FROM artwork_selections AS a NOT INDEXED
LEFT JOIN catalog_entities AS own NOT INDEXED ON own.id=a.entity_id
LEFT JOIN catalog_libraries AS cl NOT INDEXED ON cl.id=own.library_id
LEFT JOIN artwork_sources AS s INDEXED BY sqlite_autoindex_artwork_sources_1 ON s.digest=a.digest
LEFT JOIN artwork_blobs AS sb INDEXED BY sqlite_autoindex_artwork_blobs_1 ON sb.digest=s.small_blob
LEFT JOIN artwork_blobs AS lb INDEXED BY sqlite_autoindex_artwork_blobs_1 ON lb.digest=s.large_blob
LEFT JOIN inventory_objects AS o INDEXED BY sqlite_autoindex_inventory_objects_1 ON o.id=a.source_object_id
LEFT JOIN library_sources AS ls INDEXED BY sqlite_autoindex_library_sources_1 ON ls.id=o.source_id
LEFT JOIN catalog_assets AS asset INDEXED BY sqlite_autoindex_catalog_assets_1 ON asset.token=o.asset_id AND asset.library_id=own.library_id
WHERE a.kind='entity' AND a.entity_id=? AND a.role=? ORDER BY a.choice LIMIT 1`
const rsArtOctetsSQL = `SELECT octet_length(a.role),octet_length(a.choice),octet_length(a.digest),octet_length(own.public_id),octet_length(sb.digest),octet_length(sb.mime),octet_length(lb.digest),octet_length(lb.mime),octet_length(sb.status),octet_length(lb.status),octet_length(a.source_fence),octet_length(a.source_object_id),octet_length(a.source_object_revision),octet_length(o.id),octet_length(o.revision),octet_length(o.root_incarnation),octet_length(ls.incarnation),octet_length(ls.library_id),octet_length(cl.library_id)
FROM artwork_selections AS a NOT INDEXED
LEFT JOIN catalog_entities AS own NOT INDEXED ON own.id=a.entity_id
LEFT JOIN catalog_libraries AS cl NOT INDEXED ON cl.id=own.library_id
LEFT JOIN artwork_sources AS s INDEXED BY sqlite_autoindex_artwork_sources_1 ON s.digest=a.digest
LEFT JOIN artwork_blobs AS sb INDEXED BY sqlite_autoindex_artwork_blobs_1 ON sb.digest=s.small_blob
LEFT JOIN artwork_blobs AS lb INDEXED BY sqlite_autoindex_artwork_blobs_1 ON lb.digest=s.large_blob
LEFT JOIN inventory_objects AS o INDEXED BY sqlite_autoindex_inventory_objects_1 ON o.id=a.source_object_id
LEFT JOIN library_sources AS ls INDEXED BY sqlite_autoindex_library_sources_1 ON ls.id=o.source_id
WHERE a.kind='entity' AND a.entity_id=? AND a.role=? ORDER BY a.choice LIMIT 1`
const rsArtVerifySQL = `SELECT a.revision
FROM artwork_selections AS a INDEXED BY artwork_selections_digest
CROSS JOIN catalog_entities AS own NOT INDEXED ON own.id=a.entity_id
CROSS JOIN artwork_sources AS s INDEXED BY sqlite_autoindex_artwork_sources_1 ON s.digest=a.digest
CROSS JOIN artwork_blobs AS sb INDEXED BY sqlite_autoindex_artwork_blobs_1 ON sb.digest=s.small_blob
CROSS JOIN artwork_blobs AS lb INDEXED BY sqlite_autoindex_artwork_blobs_1 ON lb.digest=s.large_blob
WHERE a.kind='entity' AND a.entity_id=? AND own.kind=? AND a.role=? AND a.choice=? AND a.digest=? AND own.public_id=? AND a.revision=? AND sb.digest=? AND sb.mime=? AND sb.width=? AND sb.height=? AND sb.bytes=? AND lb.digest=? AND lb.mime=? AND lb.width=? AND lb.height=? AND lb.bytes=? AND sb.status=? AND lb.status=? AND a.source_fence=? AND a.source_object_id IS NULL`
const rsArtVerifyLocalSQL = `SELECT a.revision
FROM artwork_selections AS a INDEXED BY artwork_selections_digest
CROSS JOIN catalog_entities AS own NOT INDEXED ON own.id=a.entity_id
CROSS JOIN catalog_libraries AS cl NOT INDEXED ON cl.id=own.library_id
CROSS JOIN artwork_sources AS s INDEXED BY sqlite_autoindex_artwork_sources_1 ON s.digest=a.digest
CROSS JOIN artwork_blobs AS sb INDEXED BY sqlite_autoindex_artwork_blobs_1 ON sb.digest=s.small_blob
CROSS JOIN artwork_blobs AS lb INDEXED BY sqlite_autoindex_artwork_blobs_1 ON lb.digest=s.large_blob
CROSS JOIN inventory_objects AS o INDEXED BY sqlite_autoindex_inventory_objects_1 ON o.id=a.source_object_id
CROSS JOIN library_sources AS ls INDEXED BY sqlite_autoindex_library_sources_1 ON ls.id=o.source_id
CROSS JOIN catalog_assets AS asset INDEXED BY sqlite_autoindex_catalog_assets_1 ON asset.token=o.asset_id AND asset.library_id=own.library_id
WHERE a.kind='entity' AND a.entity_id=? AND own.kind=? AND a.role=? AND a.choice=? AND a.digest=? AND own.public_id=? AND a.revision=? AND sb.digest=? AND sb.mime=? AND sb.width=? AND sb.height=? AND sb.bytes=? AND lb.digest=? AND lb.mime=? AND lb.width=? AND lb.height=? AND lb.bytes=? AND sb.status=? AND lb.status=? AND a.source_fence=? AND a.source_object_id=? AND a.source_object_revision=? AND o.id=? AND o.revision=? AND o.retired=? AND o.root_incarnation=? AND ls.incarnation=? AND ls.library_id=? AND cl.library_id=? AND asset.available=? AND asset.size=? AND asset.modified_ns=? AND o.size=? AND o.modified_ns=?`

// An explicit locked choice without a digest is a first-class empty selection.
// It has no artwork_sources row to join, so its exact final witness is the
// WITHOUT ROWID selection key itself: NULL cannot evade source equality.
const rsArtVerifyEmptySQL = `SELECT a.revision
FROM artwork_selections AS a
CROSS JOIN catalog_entities AS own NOT INDEXED ON own.id=a.entity_id
WHERE a.kind='entity' AND a.entity_id=? AND own.kind=? AND a.role=? AND a.choice='locked' AND a.digest IS NULL AND own.public_id=? AND a.revision=? AND a.source_fence=? AND a.source_object_id IS NULL`

var rsArtVariableColumns = []int{2, 3, 4, 5, 7, 8, 12, 13, 17, 18, 19, 20, 21, 22, 23, 25, 26, 27, 28}

func init() {
	registerRSBytePoint("rs.art.point", rsBytePoint{lengths: "rs.art.octets", columns: rsArtVariableColumns})
}

// ArtworkDefinitions supplies inert statements; compose with HarnessDefinitions.
func ArtworkDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	var out []stmt.Definition
	for _, d := range []struct{ name, sql string }{{"source", rsArtSourceSQL}, {"point", rsArtPointSQL}, {"octets", rsArtOctetsSQL}, {"verify", rsArtVerifySQL}, {"verify_local", rsArtVerifyLocalSQL}, {"verify_empty", rsArtVerifyEmptySQL}} {
		params := []stmt.Param{i("owner"), s("role")}
		present := []any{int64(1103), "poster"}
		empty := []any{int64(999999), "poster"}
		if d.name == "source" {
			params = []stmt.Param{i("show_kind"), i("season_kind"), i("album_kind"), i("book_kind"), i("item"), i("library"), i("kind")}
			present = rsArtSourceArgs(1104, 1, 4)
			empty = rsArtSourceArgs(999999, 1, 4)
		}
		if d.name == "verify_empty" {
			params = []stmt.Param{i("owner"), i("kind"), s("role"), {Name: "public_id", Type: stmt.Blob}, i("revision"), s("fence")}
			present = []any{int64(1109), int64(9), "banner", []byte{128, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 9}, int64(1), "accepted-fence"}
			empty = []any{int64(999999), int64(9), "banner", []byte{128, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 9}, int64(1), "accepted-fence"}
		}
		if d.name == "verify" || d.name == "verify_local" {
			params = nil
			for n := 0; n < 34; n++ {
				p := i("field" + strconv.Itoa(n))
				p.Nullable = false
				for _, col := range rsArtVariableColumns {
					if col == n {
						p.Type = stmt.Text
					}
				}
				if n == 5 {
					p.Type = stmt.Blob
				}
				params = append(params, p)
			}
			present = []any{int64(1103), int64(3), "poster", "locked", rsArtFixtureDigest(2), []byte{128, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 3}, int64(1), rsArtFixtureDigest(2), "image/jpeg", int64(600), int64(900), int64(1234), rsArtFixtureDigest(2), "image/jpeg", int64(600), int64(900), int64(1234), "ready", "ready", "accepted-fence", nil, nil, nil, nil, nil, nil, nil, nil, "movies", nil, nil, nil, nil, nil}
			if d.name == "verify" {
				params = params[:20]
				present = present[:20]
			} else {
				present[3] = "matched"
				present[4] = rsArtFixtureDigest(1)
				present[7] = rsArtFixtureDigest(1)
				present[12] = rsArtFixtureDigest(1)
				present[20] = "art-object"
				present[21] = "r1"
				present[22] = "art-object"
				present[23] = "r1"
				present[24] = int64(0)
				present[25] = "root-a"
				present[26] = "root-a"
				present[27] = "movies"
				present[29] = int64(1)
				present[30] = int64(1000)
				present[31] = int64(1)
				present[32] = int64(1000)
				present[33] = int64(1)
			}
			empty = append([]any(nil), present...)
			empty[0] = int64(999999)
		}
		out = append(out, stmt.Definition{ID: readStatementID("rs.art." + d.name), SQL: d.sql, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_art.go:ForegroundReader"}, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: present}, {Name: "empty", Bindings: empty}}, Plan: rsArtPlan(d.name)})
	}
	return out
}
func rsArtFixtureDigest(n int) string { return fmt.Sprintf("%064x", n+1000) }
func rsArtSourceArgs(item, library, kind int64) []any {
	sh, se, al, bk := int64(0), int64(0), int64(0), int64(0)
	switch kind {
	case 3:
		sh = 2
	case 4:
		sh = 2
		se = 3
	case 7:
		al = 6
	case 9:
		bk = 8
	}
	return []any{sh, se, al, bk, item, library, kind}
}
func rsArtPlan(name string) stmt.Plan {
	p := stmt.Plan{Hot: true, PrimaryKeys: []string{"a", "own", "cl"}, Indexes: []string{"sqlite_autoindex_artwork_sources_1", "sqlite_autoindex_artwork_blobs_1", "sqlite_autoindex_inventory_objects_1", "sqlite_autoindex_library_sources_1", "sqlite_autoindex_catalog_assets_1"}, AutoIndexes: []stmt.AutoIndexBinding{
		rsArtKey("artwork_sources", "sqlite_autoindex_artwork_sources_1", "pk", "digest"), rsArtKey("artwork_blobs", "sqlite_autoindex_artwork_blobs_1", "pk", "digest"), rsArtKey("inventory_objects", "sqlite_autoindex_inventory_objects_1", "pk", "id"), rsArtKey("library_sources", "sqlite_autoindex_library_sources_1", "pk", "id"), rsArtKey("catalog_assets", "sqlite_autoindex_catalog_assets_1", "u", "token")}}
	if name == "source" {
		return stmt.Plan{Hot: true, PrimaryKeys: []string{"e", "sh", "se", "al", "bk"}}
	}
	if name == "verify_empty" {
		return stmt.Plan{Hot: true, PrimaryKeys: []string{"a", "own"}}
	}
	if name == "verify" {
		p.PrimaryKeys = []string{"a", "own"}
		p.Indexes = p.Indexes[:2]
		p.AutoIndexes = p.AutoIndexes[:2]
	}
	if name == "octets" {
		p.Indexes = p.Indexes[:4]
		p.AutoIndexes = p.AutoIndexes[:4]
	}
	if name == "verify" || name == "verify_local" {
		p.PrimaryKeys = p.PrimaryKeys[1:]
		p.Indexes = append(p.Indexes, "artwork_selections_digest")
	}
	return p
}
func rsArtKey(table, name, origin, column string) stmt.AutoIndexBinding {
	return stmt.AutoIndexBinding{Table: table, Name: name, Origin: origin, Columns: []stmt.IndexColumn{{Name: column, Collation: "BINARY"}}}
}
