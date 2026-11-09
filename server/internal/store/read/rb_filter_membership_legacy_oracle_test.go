package read

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

// rbMembershipLegacyVector is one query's frozen output from the public legacy
// compiler: catalog.ParseBrowseQuery -> CompileBrowseSelection. Freezing the
// SQL outside this package keeps the native tests from acquiring the legacy
// catalog initialization graph while still executing the real contract. The
// catalog package's TestRBFilterMembershipLegacyOracleVectorsMatchCompiler
// recompiles every vector against this file, so the freeze cannot drift.
type rbMembershipLegacyVector struct {
	Query string
	Where string
	Args  []any
}

func rbMembershipLegacyVectors(t *testing.T) []rbMembershipLegacyVector {
	t.Helper()
	data, err := os.ReadFile("testdata/rb-filter-membership-legacy-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Source  string
		Vectors []rbMembershipLegacyVector
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Source == "" || len(fixture.Vectors) != 21 {
		t.Fatalf("incomplete legacy browse oracle: %q %d", fixture.Source, len(fixture.Vectors))
	}
	return fixture.Vectors
}

// The legacy public compiler binds a public id through pid_blob and folds text
// through portico_fold_text. This native test binary deliberately does not
// import the persistence graph that registers them, so the oracle registers the
// exact public-id decode on a driver of its own and substitutes only the fold
// function name; every join, membership arm and argument stays byte-for-byte as
// compiled. Every frozen value is ASCII, where that fold is exactly lower().
//
// The registration stays off the package-level "sqlite" driver. Plan capture
// hashes pragma_function_list into environment.functionsSha256, so functions
// registered there would change every plan golden in this package, not only the
// oracle's rows. This driver is registered once, lazily, when a membership test
// first asks for it, and only the oracle's own connection is opened through it.
const rbMembershipOracleDriverName = "sqlite-rb-membership-legacy-oracle"

var (
	rbMembershipOracleRegister sync.Once
	rbMembershipOracleErr      error
)

func rbMembershipOracleDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	rbMembershipOracleRegister.Do(func() {
		connDriver := &sqlite.Driver{}
		if err := connDriver.RegisterDeterministicScalarFunction("pid_blob", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			public, ok := args[0].(string)
			if !ok {
				return nil, nil
			}
			raw, valid := entityid.Decode(public)
			if !valid {
				return nil, nil
			}
			return raw, nil
		}); err != nil {
			rbMembershipOracleErr = err
			return
		}
		if err := connDriver.RegisterDeterministicScalarFunction("pid", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			raw, ok := args[0].([]byte)
			if !ok || len(raw) != entityid.Size {
				return nil, nil
			}
			return entityid.Encode(raw), nil
		}); err != nil {
			rbMembershipOracleErr = err
			return
		}
		sql.Register(rbMembershipOracleDriverName, connDriver)
	})
	if rbMembershipOracleErr != nil {
		t.Fatalf("register membership oracle functions: %v", rbMembershipOracleErr)
	}
	uri := url.URL{Scheme: "file", Path: path, RawQuery: "mode=rw"}
	db, err := sql.Open(rbMembershipOracleDriverName, uri.String())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

func rbMembershipLegacyMatches(t *testing.T, db *sql.DB, in BrowseInput, where string, args []any) []int64 {
	t.Helper()
	key, direction := "sort_key", "ASC"
	if in.KeepArticles {
		key = "sort_key_keep_articles"
	}
	if in.Descending {
		direction = "DESC"
	}
	where = strings.ReplaceAll(where, "portico_fold_text(", "lower(")
	query := "SELECT e.entity_id FROM (SELECT id AS entity_id,library_id,kind,retired,sort_key,sort_key_keep_articles FROM catalog_entities) AS e WHERE e.library_id=? AND e.kind=? AND e.retired=0 AND (" + where + ") ORDER BY e." + key + " COLLATE NOCASE " + direction + ",e.entity_id " + direction
	rows, err := db.QueryContext(t.Context(), query, append([]any{in.Library, in.Kind}, args...)...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// rbMembershipSavedLegacySQL is the public savedCollection predicate's three
// arms, copied from server/internal/catalog/browse_group_scope.go with the
// unrestricted item predicate (the fixture viewer has no age or label policy).
// It is executed as plain SQL, never reimplemented in Go.
func rbMembershipSavedLegacySQL(resource string) (string, []any) {
	owner := rbMembershipTestOwnerKey()
	entries := `SELECT sre.item_id FROM saved_resources res JOIN saved_resource_entries sre ON sre.resource_id=res.id WHERE res.id IN(?) AND res.kind='collection' AND res.deleted=0 AND sre.item_id IS NOT NULL AND (res.owner_key=? OR res.visibility='server' OR EXISTS(SELECT 1 FROM saved_resource_shares share WHERE share.resource_id=res.id AND share.authority=? AND share.account_id=? AND share.profile_id=?))`
	direct := `e.entity_id IN(` + entries + `)`
	held := `e.entity_id IN(SELECT bei.entity_id FROM (` + entries + `) entry CROSS JOIN catalog_browse_memberships bei INDEXED BY catalog_browse_membership_item ON bei.item_id=entry.item_id WHERE 1)`
	parts := `e.entity_id IN(SELECT bei.item_id FROM (` + entries + `) entry CROSS JOIN catalog_browse_memberships bei ON bei.entity_id=entry.item_id)`
	where := "(" + direct + " OR " + held + " OR " + parts + ")"
	args := []any{}
	for i := 0; i < 3; i++ {
		args = append(args, resource, owner, "local", "a", "p")
	}
	return where, args
}

func rbMembershipListLegacySQL(token string) (string, []any) {
	where := `e.entity_id IN(SELECT bei.entity_id FROM catalog_browse_memberships bei JOIN catalog_playlist_entries entry ON entry.item_id=bei.item_id JOIN catalog_playlists list ON list.id=entry.playlist_id AND list.deleted=0 AND (list.owner_authority=? AND list.owner_account=? AND list.owner_profile=? OR EXISTS(SELECT 1 FROM playlist_shares share WHERE share.playlist_id=list.token AND share.authority=? AND share.account_id=? AND share.profile_id=?)) WHERE (1) AND (list.token) IN (?))`
	return where, []any{"local", "a", "p", "local", "a", "p", token}
}

// TestRBFilterMembershipLegacyPersonalOracle executes the legacy personal
// collection and playlist predicates as plain SQL over the same real-shape
// fixture.
func TestRBFilterMembershipLegacyPersonalOracle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbMembershipFixture(t)
		for _, tc := range []struct {
			kind  int64
			query string
			where string
			args  []any
		}{
			{1, `{"variant":"predicate","field":"savedCollection","operator":"equals","value":"saved-mine"}`, "", nil},
			{2, `{"variant":"predicate","field":"savedCollection","operator":"equals","value":"saved-shared"}`, "", nil},
			{1, `{"variant":"predicate","field":"list","operator":"equals","value":"list-mine"}`, "", nil},
		} {
			in := MembershipFilterInput{View: f.input, Query: json.RawMessage(tc.query)}
			in.View.Kind, in.View.Limit = tc.kind, 2
			where, args := tc.where, tc.args
			if where == "" {
				switch {
				case strings.Contains(tc.query, "saved-mine"):
					where, args = rbMembershipSavedLegacySQL("saved-mine")
				case strings.Contains(tc.query, "saved-shared"):
					where, args = rbMembershipSavedLegacySQL("saved-shared")
				default:
					where, args = rbMembershipListLegacySQL("list-mine")
				}
			}
			got := rbMembershipTraverse(t, f, in)
			want := rbMembershipOracle(t, f, in.View, where, args...)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("personal legacy oracle differs %s kind=%d: native %v legacy %v", tc.query, tc.kind, got, want)
			}
		}
	})
}
