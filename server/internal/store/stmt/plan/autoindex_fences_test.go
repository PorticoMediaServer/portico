package plan_test

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// Regression snapshots of RD's actual joined fence SQL, not serving adapters.
// No held read package is imported or executed. RD retains its own product
// statements, seeds, semantic oracles and output/admission qualification.
const autoindexPrincipalFence = `SELECT a.id,p.id,m.incarnation,p.lifetime,a.epoch,m.role,m.allowed_libraries,p.allowed_libraries,t.expires_at,f.authorization_horizon,f.authority_generation,m.revision,COALESCE(pr.maximum_age,-1),COALESCE(pr.allow_unrated,1),COALESCE(pr.blocked_labels,'[]'),lim.account_id,p.revision,COALESCE(pr.revision,0)
FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
CROSS JOIN identity_devices AS d INDEXED BY sqlite_autoindex_identity_devices_1 ON d.id=f.device_id
CROSS JOIN accounts AS a INDEXED BY sqlite_autoindex_accounts_1 ON a.id=f.account_id
CROSS JOIN direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 ON m.account_id=a.id
CROSS JOIN direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_2 ON p.id=f.profile_id AND p.account_id=a.id
LEFT JOIN profile_restrictions AS pr INDEXED BY sqlite_autoindex_profile_restrictions_1 ON pr.profile_id=p.id
LEFT JOIN access_limits AS lim INDEXED BY sqlite_autoindex_access_limits_1 ON lim.account_id=a.id
WHERE t.token_hash=? AND t.retired=0 AND t.generation=f.current_generation AND f.revoked=0 AND f.pin_gate=0
AND f.authority='local' AND d.authority='local' AND d.account_id=a.id AND a.epoch=f.epoch
AND f.authority_generation=? AND m.disabled=0 AND p.deleted=0`
const autoindexLibraryFence = `SELECT l.id,l.data_revision,l.data_order_revision,l.data_metadata_revision,COALESCE(v.revision,0),l.age_input_revision,l.age_published_revision,l.age_pending_operations
FROM libraries AS l INDEXED BY sqlite_autoindex_libraries_1
LEFT JOIN viewer_revisions AS v INDEXED BY sqlite_autoindex_viewer_revisions_1 ON v.library_id=l.id AND v.profile_id=?
WHERE l.id=?`

func autoindexFenceDefinitions() []stmt.Definition {
	key := func(table, name, origin string, columns ...string) stmt.AutoIndexBinding {
		pin := stmt.AutoIndexBinding{Table: table, Name: name, Origin: origin}
		for _, col := range columns {
			pin.Columns = append(pin.Columns, stmt.IndexColumn{Name: col, Collation: "BINARY"})
		}
		return pin
	}
	principal := []stmt.AutoIndexBinding{
		key("authorization_family_tokens", "sqlite_autoindex_authorization_family_tokens_1", "pk", "token_hash"),
		key("authorization_session_families", "sqlite_autoindex_authorization_session_families_1", "pk", "id"),
		key("identity_devices", "sqlite_autoindex_identity_devices_1", "pk", "id"),
		key("accounts", "sqlite_autoindex_accounts_1", "pk", "id"),
		key("direct_memberships", "sqlite_autoindex_direct_memberships_1", "pk", "account_id"),
		key("direct_profiles", "sqlite_autoindex_direct_profiles_2", "u", "account_id", "id"),
		key("profile_restrictions", "sqlite_autoindex_profile_restrictions_1", "pk", "profile_id"),
		key("access_limits", "sqlite_autoindex_access_limits_1", "pk", "account_id"),
	}
	library := []stmt.AutoIndexBinding{
		key("libraries", "sqlite_autoindex_libraries_1", "pk", "id"),
		key("viewer_revisions", "sqlite_autoindex_viewer_revisions_1", "pk", "profile_id", "library_id"),
	}
	defs := []stmt.Definition{
		{ID: "auto.rd.principal", SQL: autoindexPrincipalFence, Params: []stmt.Param{{Name: "token", Type: stmt.Text}, {Name: "generation", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{"token", int64(1)}}, {Name: "missing", Bindings: []any{"absent", int64(1)}}}, Plan: stmt.Plan{Hot: true, AutoIndexes: principal, Seeks: []string{"token_hash=?", "account_id=? AND id=?"}}},
		{ID: "auto.rd.library", SQL: autoindexLibraryFence, Params: []stmt.Param{{Name: "profile", Type: stmt.Text}, {Name: "library", Type: stmt.Text}}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{"p", "movies"}}, {Name: "missing", Bindings: []any{"p", "absent"}}}, Plan: stmt.Plan{Hot: true, AutoIndexes: library, Seeks: []string{"id=?", "profile_id=? AND library_id=?"}}},
	}
	for i := range defs {
		defs[i].Owner = "plan-autoindex-fence-regression"
		defs[i].CallSites = []string{"autoindex_fences_test.go"}
		defs[i].Kind = stmt.Read
		for _, pin := range defs[i].Plan.AutoIndexes {
			defs[i].Plan.Indexes = append(defs[i].Plan.Indexes, pin.Name)
		}
	}
	return defs
}

func TestPlanAutoindexRealJoinedFences(t *testing.T) {
	type snapshot struct {
		Environment   *plan.Environment    `json:"environment,omitempty"`
		Fixture       plan.Fixture         `json:"fixture"`
		Connection    sqliteconn.Settings  `json:"connection"`
		SchemaSHA     string               `json:"schemaSha256"`
		PolicySHA     string               `json:"policySha256"`
		StatisticsSHA string               `json:"statisticsSha256"`
		Manifest      []stmt.Entry         `json:"manifest"`
		Plans         []plan.StatementPlan `json:"plans"`
	}
	var snapshots []snapshot
	// Actual canonical tables with only 1–2 identity/library rows. In
	// particular no larger population is introduced to influence ANALYZE.
	seed := []string{
		`INSERT INTO identity_state(singleton,sign_in_kind) VALUES(1,'local')`,
		`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('a','alice',x'00','p'),('b','bob',x'00','q')`,
		`INSERT INTO direct_memberships(account_id,role,allowed_libraries) VALUES('a','member','["movies"]'),('b','member','["other"]')`,
		`INSERT INTO direct_profiles(id,account_id,name) VALUES('p','a','Alice'),('q','b','Bob')`,
		`INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('d','a','i','fixture','test','test','1','2026-11-05T00:00:00Z','2026-11-05T00:00:00Z')`,
		`INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,device_id,command_member_incarnation,command_profile_lifetime,command_incarnation)
 SELECT 'f','s','a','p','local','member',1,'2026-11-07T00:00:00Z',1,'d',m.incarnation,p.lifetime,X'01010101010101010101010101010101'
 FROM direct_memberships m JOIN direct_profiles p ON p.account_id=m.account_id
 WHERE m.account_id='a' AND p.id='p'`,
		`INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES('token','f',1,'2026-11-07T00:00:00Z')`,
		`INSERT INTO libraries(id,name,kind,root) VALUES('movies','Movies','movie','/media'),('other','Other','movie','/media')`,
	}
	for _, analyzed := range []bool{false, true} {
		writer, _, f, _ := fixture(t)
		if _, err := writer.ExecContext(t.Context(), "DROP TABLE items"); err != nil {
			t.Fatal(err)
		}
		if err := schema.InitializeEmpty(t.Context(), writer); err != nil {
			t.Fatal(err)
		}
		hash := sha256.New()
		for _, q := range seed {
			if _, err := writer.ExecContext(t.Context(), q); err != nil {
				t.Fatal(err)
			}
			hash.Write([]byte(q))
		}
		f.Statistics = plan.Unanalyzed
		if analyzed {
			if _, err := writer.ExecContext(t.Context(), "ANALYZE"); err != nil {
				t.Fatal(err)
			}
			f.Statistics = plan.Analyzed
		}
		f.SHA256 = hex.EncodeToString(hash.Sum(nil))
		f.Implementations = "actual canonical MAIN PK/UNIQUE constraints; RD principal/library SQL-shape regression; tiny 1–2 rows; optional LEFT tables empty; no serving authorization qualification"
		var sequence int64
		var name, path string
		if err := writer.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
			t.Fatal(err)
		}
		f.ConnectionPolicy.Role = sqliteconn.Reader
		f.ConnectionPolicy.CacheKiB = 1024
		f.ConnectionPolicy.ReadLimits = sqliteconn.ReadLimits{LengthBytes: 65536, Columns: 128}
		db, _, err := sqliteconn.Open(t.Context(), path, f.ConnectionPolicy)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		reader, err := db.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { reader.Close() })
		data, err := schema.ReadContract("INT-INDEX-POLICY.json")
		if err != nil {
			t.Fatal(err)
		}
		policy, err := plan.ReadIndexPolicy(data)
		if err != nil {
			t.Fatal(err)
		}
		defs := autoindexFenceDefinitions()
		catalogue, err := stmt.Describe(defs)
		if err != nil {
			t.Fatal(err)
		}
		golden, err := plan.Capture(t.Context(), reader, catalogue, f, policy)
		if err != nil {
			t.Fatal(err)
		}
		registry, err := stmt.New(defs, golden.Receipt())
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range defs {
			s, err := registry.Lookup(d.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range d.Cohorts {
				bound, err := s.Bind(c.Bindings...)
				if err != nil {
					t.Fatal(err)
				}
				rows, err := bound.QueryContext(t.Context(), reader)
				if err != nil {
					t.Fatal(err)
				}
				columns, err := rows.Columns()
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for rows.Next() {
					values := make([]any, len(columns))
					targets := make([]any, len(values))
					for i := range values {
						targets[i] = &values[i]
					}
					if err := rows.Scan(targets...); err != nil {
						t.Fatal(err)
					}
					count++
					if d.ID == "auto.rd.principal" && (len(values) != 18 || values[0] != "a" || values[1] != "p" || values[15] != nil) {
						t.Fatal("principal/LEFT regression", values)
					}
					if d.ID == "auto.rd.library" && (len(values) != 8 || values[0] != "movies" || values[4] != int64(0)) {
						t.Fatal("library/LEFT regression", values)
					}
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				want := 0
				if c.Name == "present" {
					want = 1
				}
				if count != want {
					t.Fatal(d.ID, c.Name, count, want)
				}
			}
		}
		snapshots = append(snapshots, snapshot{&golden.Environment, f, golden.Environment.Connection, golden.Environment.SchemaSHA256, golden.Environment.IndexPolicySHA256, golden.Environment.StatisticsSHA256, golden.Manifest, golden.Plans})
		t.Logf("%s: 2 real joined fences/4 cohorts; actual fixed Reader %+v", f.Statistics, golden.Environment.Connection)
	}
	data, err := json.MarshalIndent(snapshots, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "autoindex-fences.json.gz")
	if *recordAutoindexPlans {
		var buffer bytes.Buffer
		w := gzip.NewWriter(&buffer)
		if _, err := w.Write(append(data, '\n')); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, buffer.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	expected, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := plan.CompareJSON(expected, data); err != nil {
		t.Fatal(err)
	}
}
