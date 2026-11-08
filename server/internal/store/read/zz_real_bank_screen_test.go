package read

// SCREENING ONLY (scratch branch, never merged): the real canonical-movies-v1-search
// population image, read through the native full-search reader and checked by
// RD's exact oracle (#1701 Validate/Check/CheckWalk). Directional, not qualification.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

func realBankHashAndCopy(t *testing.T, src, dst string) (string, int64) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), in)
	if err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil)), n
}

func TestScreenRealBank(t *testing.T) {
	dir := os.Getenv("PORTICO_RD_POPULATION_DIR")
	if dir == "" {
		t.Skip("RD real-bank screen runs only with PORTICO_RD_POPULATION_DIR")
	}
	work := os.Getenv("PORTICO_RD_WORK_DIR")
	if work == "" {
		work = t.TempDir()
	}
	started := time.Now()
	path := filepath.Join(work, "population-copy.sqlite")
	sum, size := realBankHashAndCopy(t, filepath.Join(dir, "population.sqlite"), path)
	if want := os.Getenv("PORTICO_RD_IMAGE_SHA256"); want != "" && want != sum {
		t.Fatalf("image sha256 %s, pinned %s", sum, want)
	}
	copySeconds := time.Since(started)
	var facts struct {
		Facts struct {
			Version   string `json:"version"`
			Residents int    `json:"residents"`
			LibraryID int64  `json:"library_id"`
		} `json:"facts"`
	}
	raw, err := os.ReadFile(filepath.Join(dir, "population.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifestSum := fmt.Sprintf("%x", sha256.Sum256(raw))
	if want := os.Getenv("PORTICO_RD_MANIFEST_SHA256"); want != "" && want != manifestSum {
		t.Fatalf("manifest sha256 %s, pinned %s", manifestSum, want)
	}
	if err := json.Unmarshal(raw, &facts); err != nil {
		t.Fatal(err)
	}
	if facts.Facts.Version != "canonical-movies-v1-search" {
		t.Fatalf("population shape %q is not the search shape", facts.Facts.Version)
	}
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(); _ = db.Close() })

	started = time.Now()
	// The shared ForegroundOn journal inserts credit labels 1-4, which collide
	// with the search bank's own labels, so only the principal rows are seeded here.
	for _, q := range []string{
		`INSERT INTO identity_state(singleton,sign_in_kind) VALUES(1,'local')`,
		`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('a','alice',x'00','p')`,
		`INSERT INTO direct_memberships(account_id,role,allowed_libraries) VALUES('a','member','["qualification-movies"]')`,
		`INSERT INTO direct_profiles(id,account_id,name) VALUES('p','a','Alice')`,
		`INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('d','a','i','fixture','test','test','1','2026-11-05T00:00:00Z','2026-11-05T00:00:00Z')`,
		`INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,device_id,command_member_incarnation,command_profile_lifetime,command_incarnation) SELECT 'f','s','a','p','local','member',1,'2026-11-07T00:00:00Z',1,'d',m.incarnation,p.lifetime,x'00800000000000000000000000000001' FROM direct_memberships AS m JOIN direct_profiles AS p ON p.account_id=m.account_id WHERE m.account_id='a' AND p.id='p'`,
		`INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES('token','f',1,'2026-11-07T00:00:00Z')`,
	} {
		if _, err := conn.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	f := &browseFixture{path: path, conn: conn, input: BrowseInput{Database: "fixture-db-1", TokenHash: "token", Library: 7, Kind: 0, Limit: 20, Now: time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)}}
	seedSeconds := time.Since(started)

	started = time.Now()
	f = composeReadFixture(t, f, NativeFullSearchDefinitions(), readfixture.LiveConfig(time.Now), false, nativeFixtureHash(t))
	compose := time.Since(started)
	reader, err := NewNativeFullSearchReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	f.input.Kind = 0
	f.input.Limit = 20
	f.input.Library = facts.Facts.LibraryID

	base := int64(1_000_000_000)
	title := func(ordinal int) string {
		var s string
		if err := f.conn.QueryRowContext(t.Context(), `SELECT title FROM catalog_entities WHERE id=?`, base+int64(ordinal)).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	n := facts.Facts.Residents
	oracle, err := scrNewFullSearchOracle([]string{title(42), title(n/2 + 7), title(n)})
	if err != nil {
		t.Fatal(err)
	}
	started = time.Now()
	rows, err := f.conn.QueryContext(t.Context(), `SELECT id,title FROM catalog_entities WHERE retired=0`)
	if err != nil {
		t.Fatal(err)
	}
	observed := 0
	for rows.Next() {
		var id int64
		var tt string
		if err := rows.Scan(&id, &tt); err != nil {
			t.Fatal(err)
		}
		oracle.Observe(id, tt)
		observed++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	observe := time.Since(started)
	validate := oracle.Validate()
	if validate != nil {
		t.Error("mix does not hold on this population:", validate)
	}

	type termReport struct {
		Class, Term                 string
		Expected                    int64
		Polls, Hits, Distinct       int
		ReadyPolls                  int
		FirstNS, ReadyNS, MaxPollNS int64
		TotalNS                     int64
		Exhausted, Partial, Unavail bool
		CandSum                     int
		Error                       string
	}
	var reports []termReport
	for i, term := range oracle.Terms {
		r := termReport{Class: term.Class.String(), Term: term.Text, Expected: oracle.Expected(i)}
		var after *FullSearchCursor
		var pages []FullSearchPage
		seen := map[int64]bool{}
		walk := time.Now()
		for poll := 0; poll < 400; poll++ {
			begin := time.Now()
			page, err := nativePreviewRead(t, f, reader, term.Text, after, NativePreviewCandidates)
			took := time.Since(begin)
			r.Polls++
			r.MaxPollNS = max(r.MaxPollNS, took.Nanoseconds())
			if poll == 0 {
				r.FirstNS = took.Nanoseconds()
			}
			if err != nil {
				r.Error = err.Error()
				break
			}
			pages = append(pages, page)
			r.Hits += len(page.Hits)
			r.CandSum += page.NativeCandidates
			for _, h := range page.Hits {
				seen[h.ID] = true
			}
			if r.ReadyNS == 0 && (len(page.Hits) > 0 || page.Exhausted && !page.Partial) {
				r.ReadyNS, r.ReadyPolls = time.Since(walk).Nanoseconds(), r.Polls
			}
			r.Exhausted, r.Partial = page.Exhausted, page.Partial
			if page.Next == nil {
				break
			}
			after = page.Next
		}
		r.TotalNS, r.Distinct = time.Since(walk).Nanoseconds(), len(seen)
		if r.Error == "" {
			if cerr := oracle.CheckWalk(i, pages); cerr != nil {
				r.Error, r.Unavail = cerr.Error(), cerr == scrErrFullSearchBankAbsent
			}
		}
		if r.Error != "" {
			t.Errorf("%s %q: %s", r.Class, r.Term, r.Error)
		}
		reports = append(reports, r)
	}
	_ = search.ManifestEntity
	out := map[string]any{"scope": "SCREENING: real canonical-movies-v1-search image read through the native reader; directional only", "image_sha256": sum, "manifest_sha256": manifestSum, "image_bytes": size, "version": facts.Facts.Version, "residents": n, "observed_entities": observed, "library_id": facts.Facts.LibraryID, "copy_hash_ns": copySeconds.Nanoseconds(), "principal_seed_ns": seedSeconds.Nanoseconds(), "compose_capture_ns": compose.Nanoseconds(), "oracle_pass_ns": observe.Nanoseconds(), "validate_ok": validate == nil, "terms": reports}
	data, _ := json.MarshalIndent(out, "", " ")
	t.Logf("%s", data)
	if dirE := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); dirE != "" {
		_ = os.WriteFile(filepath.Join(dirE, fmt.Sprintf("real-bank-screen-%d.json", n)), data, 0o600)
	}
}
