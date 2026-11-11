package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// trialUpgrades is what a release or two after the first might do, for the tests here. The
// first step is only quick. The second adds a column to the largest table people browse,
// fills it in the background a batch of rows at a time, and then builds an index on it.
func trialUpgrades(batch int) []upgrade {
	return []upgrade{
		{what: "notes on titles", sql: `CREATE TABLE trial_notes (item_id INTEGER NOT NULL PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE, note TEXT NOT NULL) STRICT`},
		{what: "titles know their length", sql: `ALTER TABLE items ADD COLUMN title_length INTEGER`,
			background: func(tx *sql.Tx, after int64) (int64, bool, error) {
				var upTo sql.NullInt64
				if err := tx.QueryRow(`SELECT max(id) FROM (SELECT id FROM items WHERE id > ? ORDER BY id LIMIT ?)`, after, batch).Scan(&upTo); err != nil {
					return 0, false, err
				}
				if !upTo.Valid {
					_, err := tx.Exec(`CREATE INDEX items_by_title_length ON items (title_length)`)
					return after, true, err
				}
				_, err := tx.Exec(`UPDATE items SET title_length = length(title) WHERE id > ? AND id <= ?`, after, upTo.Int64)
				return upTo.Int64, false, err
			}},
	}
}

func useUpgrades(t *testing.T, steps []upgrade) {
	before := upgrades
	upgrades = steps
	t.Cleanup(func() { upgrades = before })
}

// firstReleaseDatabase makes a database as the first release would leave it, with some titles.
func firstReleaseDatabase(t *testing.T, path string, titles int) {
	t.Helper()
	useUpgrades(t, nil)
	d, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	err = d.write(context.Background(), User, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO libraries (id, public_id, name, kind, created_at, updated_at) VALUES (1, 'lib_1', 'Films', 'movies', 0, 0)`); err != nil {
			return err
		}
		for i := 1; i <= titles; i++ {
			title := strings.Repeat("a", i)
			if _, err := tx.Exec(`INSERT INTO items (id, public_id, library_id, kind, title, sort_title, added_at, updated_at) VALUES (?, ?, 1, 1, ?, ?, 0, 0)`,
				i, "itm_"+strconv.Itoa(i), title, title); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
}

// upgradeState is what the trial upgrade has left in a database file, read with a plain
// connection the way a restart would find it.
type upgradeState struct {
	version           int
	titles            int
	notesTable        bool
	lengthColumn      bool
	lengthIndex       bool
	backgroundPending bool
	backgroundAfter   int64
	wrongLengths      int // titles whose length is filled in when it should not be yet, or the reverse
}

func readUpgradeState(t *testing.T, path string) upgradeState {
	t.Helper()
	conn, err := open(path, "rw", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	scan := func(dest any, query string, args ...any) {
		t.Helper()
		if err := conn.QueryRow(query, args...).Scan(dest); err != nil {
			t.Fatalf("%s: %s: %v", path, query, err)
		}
	}
	var s upgradeState
	var ok string
	if scan(&ok, `PRAGMA integrity_check`); ok != "ok" {
		t.Fatalf("%s: integrity_check says %s", path, ok)
	}
	var broken int
	if scan(&broken, `SELECT count(*) FROM pragma_foreign_key_check`); broken != 0 {
		t.Fatalf("%s: %d broken references", path, broken)
	}
	scan(&s.version, `SELECT max(version) FROM schema_migrations`)
	scan(&s.titles, `SELECT count(*) FROM items`)
	scan(&s.notesTable, `SELECT count(*) FROM sqlite_master WHERE name = 'trial_notes'`)
	scan(&s.lengthColumn, `SELECT count(*) FROM pragma_table_info('items') WHERE name = 'title_length'`)
	scan(&s.lengthIndex, `SELECT count(*) FROM sqlite_master WHERE name = 'items_by_title_length'`)
	var after sql.NullInt64
	scan(&after, `SELECT max(background_after) FROM schema_migrations`)
	s.backgroundPending, s.backgroundAfter = after.Valid, after.Int64
	if s.lengthColumn {
		filledUpTo := int64(s.titles)
		if s.backgroundPending {
			filledUpTo = s.backgroundAfter
		}
		scan(&s.wrongLengths, `SELECT count(*) FROM items WHERE title_length IS NOT CASE WHEN id <= ? THEN length(title) END`, filledUpTo)
	}
	return s
}

// A person upgrades Portico and the server is killed part way: by a power cut, by an
// impatient restart, at any moment. Whenever that happens the database must be at a whole
// version, with the copy from before the upgrade whole too, and the next start must finish
// the upgrade with the same result as one that was never interrupted.
//
// The server here is this test binary run again as TestUpgradeChild, which kills its own
// process at the Nth moment an upgrade can be cut short; N goes up until a run finishes
// without reaching it.
func TestAnUpgradeKilledAtAnyPointResumes(t *testing.T) {
	const titles, batch = 30, 10
	original := filepath.Join(t.TempDir(), "main.db")
	firstReleaseDatabase(t, original, titles)

	run := func(path string, killAt int) (killedAt string) {
		t.Helper()
		child := exec.Command(os.Args[0], "-test.run=^TestUpgradeChild$", "-test.count=1")
		child.Env = append(os.Environ(), "PORTICO_UPGRADE_CHILD_DB="+path, "PORTICO_UPGRADE_CHILD_KILL="+strconv.Itoa(killAt),
			"PORTICO_UPGRADE_CHILD_BATCH="+strconv.Itoa(batch))
		out, _ := child.CombinedOutput()
		if point, err := os.ReadFile(path + ".killed"); err == nil {
			os.Remove(path + ".killed")
			return string(point)
		}
		if _, err := os.Stat(path + ".finished"); err != nil {
			t.Fatalf("the upgrade neither finished nor was killed (kill point %d):\n%s", killAt, out)
		}
		os.Remove(path + ".finished")
		return ""
	}
	finished := func(path string) {
		t.Helper()
		s := readUpgradeState(t, path)
		want := upgradeState{version: 3, titles: titles, notesTable: true, lengthColumn: true, lengthIndex: true}
		if s != want {
			t.Fatalf("after the upgrade finished the database is %+v, want %+v", s, want)
		}
	}

	seen := map[string]int{}
	for killAt := 1; ; killAt++ {
		path := filepath.Join(t.TempDir(), "main.db")
		copyFile(t, original, path)
		point := run(path, killAt)
		if point == "" {
			finished(path)
			break
		}
		seen[point]++

		s := readUpgradeState(t, path)
		whole := s.titles == titles && s.notesTable == (s.version >= 2) && s.lengthColumn == (s.version >= 3) && s.wrongLengths == 0 &&
			s.backgroundAfter%batch == 0 && s.lengthIndex == (s.version == 3 && !s.backgroundPending)
		if !whole {
			t.Fatalf("killed at point %d (%s), the database is not at a whole version: %+v", killAt, point, s)
		}
		// Once anything has changed, the copy from before is there and is the first release's database.
		copied := copyPath(path, firstVersion, 3)
		if _, err := os.Stat(copied); err == nil {
			if b := readUpgradeState(t, copied); b != (upgradeState{version: firstVersion, titles: titles}) {
				t.Fatalf("killed at point %d (%s), the copy from before the upgrade is %+v", killAt, point, b)
			}
		} else if s.version > firstVersion {
			t.Fatalf("killed at point %d (%s) at version %d with no copy from before the upgrade", killAt, point, s.version)
		}

		if again := run(path, 0); again != "" {
			t.Fatalf("the second start was killed at %s", again)
		}
		finished(path)
	}
	// Three batches of titles and one for the index, each cut short before and after its
	// commit; two steps likewise; the copy before it has its name.
	for point, times := range map[string]int{"copy written, not yet named": 1, "step applied, not yet committed": 2, "step committed": 2,
		"batch run, not yet committed": 4, "batch committed": 4} {
		if seen[point] < times {
			t.Errorf("killed %d times at %q, expected at least %d: %v", seen[point], point, times, seen)
		}
	}
}

// TestUpgradeChild is the server in TestAnUpgradeKilledAtAnyPointResumes. It does nothing
// when run by itself.
func TestUpgradeChild(t *testing.T) {
	path := os.Getenv("PORTICO_UPGRADE_CHILD_DB")
	if path == "" {
		t.Skip("run by TestAnUpgradeKilledAtAnyPointResumes")
	}
	killAt, _ := strconv.Atoi(os.Getenv("PORTICO_UPGRADE_CHILD_KILL"))
	batch, _ := strconv.Atoi(os.Getenv("PORTICO_UPGRADE_CHILD_BATCH"))
	upgrades = trialUpgrades(batch)
	points := 0
	upgradePoint = func(point string) {
		if points++; points == killAt {
			os.WriteFile(path+".killed", []byte(point), 0o644)
			self, _ := os.FindProcess(os.Getpid())
			self.Kill()
			select {}
		}
	}
	ctx := context.Background()
	d, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	// The server is in use while the background part runs: a person's write and a read.
	err = d.write(ctx, User, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO trial_notes (item_id, note) VALUES (1, 'seen') ON CONFLICT DO NOTHING`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var titles int
	if err := d.read.QueryRow(`SELECT count(*) FROM items`).Scan(&titles); err != nil {
		t.Fatal(err)
	}
	<-d.upgradesDone
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path+".finished", nil, 0o644)
}

// An older Portico pointed at a database a newer one has upgraded says so and leaves it alone.
func TestANewerDatabaseIsRefused(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "main.db")
	firstReleaseDatabase(t, path, 3)
	useUpgrades(t, trialUpgrades(10))
	d, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	<-d.upgradesDone
	d.Close()
	after := readUpgradeState(t, path)

	upgrades = nil // the first release again
	_, err = Open(ctx, path)
	if err == nil {
		t.Fatal("the first release opened a database two versions newer")
	}
	for _, want := range []string{"newer version of Portico", "version 3", "version 1", copyPath(path, 1, 3)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
	if now := readUpgradeState(t, path); now != after {
		t.Errorf("the refused database changed from %+v to %+v", after, now)
	}
	// The copy it points to is one the first release opens.
	d, err = Open(ctx, copyPath(path, 1, 3))
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
}

// A step that fails changes nothing: the database stays at the version before it, and a
// program with the step put right carries on from there.
func TestAFailedStepLeavesTheVersionBeforeIt(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "main.db")
	firstReleaseDatabase(t, path, 3)
	good := trialUpgrades(10)
	bad := slices.Clone(good)
	bad[1].sql += `; INSERT INTO no_such_table VALUES (1)`
	useUpgrades(t, bad)
	_, err := Open(ctx, path)
	if err == nil || !strings.Contains(err.Error(), "stays at version 2") {
		t.Fatalf("a failing step: %v", err)
	}
	if s := readUpgradeState(t, path); s != (upgradeState{version: 2, titles: 3, notesTable: true}) {
		t.Fatalf("after the failed step the database is %+v", s)
	}
	upgrades = good
	d, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	<-d.upgradesDone
	d.Close()
	if s := readUpgradeState(t, path); s != (upgradeState{version: 3, titles: 3, notesTable: true, lengthColumn: true, lengthIndex: true}) {
		t.Fatalf("after the step was put right the database is %+v", s)
	}
}

// Three releases, each with one step, installed one after the other, leave one copy of the
// database: the one from before the latest upgrade.
func TestOneCopyIsKeptFromBeforeTheLatestUpgrade(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "main.db")
	firstReleaseDatabase(t, path, 3)
	steps := []upgrade{
		{what: "one", sql: `CREATE TABLE trial_one (id INTEGER PRIMARY KEY) STRICT`},
		{what: "two", sql: `CREATE TABLE trial_two (id INTEGER PRIMARY KEY) STRICT`},
		{what: "three", sql: `CREATE TABLE trial_three (id INTEGER PRIMARY KEY) STRICT`},
	}
	for release := 1; release <= len(steps); release++ {
		useUpgrades(t, steps[:release])
		d, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		d.Close()
	}
	copies := upgradeCopies(path)
	if len(copies) != 1 || copies[0].from != 3 || copies[0].to != 4 {
		t.Fatalf("after three upgrades the copies kept are %+v, want the one from before the latest", copies)
	}
	if s := readUpgradeState(t, copies[0].path); s.version != 3 {
		t.Fatalf("the copy kept is version %d, want 3", s.version)
	}
}

// A release adds a column filled in the background; the next release's step takes the
// column away again. A person who installs both before the first has finished, or skips a
// release, must not be left with background work that can never finish: the earlier work is
// finished before the newer step is applied.
func TestEarlierBackgroundWorkIsFinishedBeforeANewerStep(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.db")
	firstReleaseDatabase(t, path, 30)
	filled := 0
	steps := trialUpgrades(10)
	fill := steps[1].background
	steps[1].background = func(tx *sql.Tx, after int64) (int64, bool, error) {
		filled++
		return fill(tx, after)
	}
	steps = append(steps, upgrade{what: "titles lose their length again", sql: `DROP INDEX items_by_title_length; ALTER TABLE items DROP COLUMN title_length`})
	useUpgrades(t, steps)
	d, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	if s := readUpgradeState(t, path); s != (upgradeState{version: 4, titles: 30, notesTable: true}) || filled != 4 {
		t.Fatalf("the database is %+v after %d background batches, want version 4 with nothing left to do after 4", s, filled)
	}
}

// A step can say its background part must be finished before the server answers anyone.
// Then no start returns with it half done: not the start that applies the step, and not a
// start after the work was cut short.
func TestAStepCanFinishItsBackgroundPartBeforeTheServerAnswers(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "main.db")
	firstReleaseDatabase(t, path, 30)
	steps := trialUpgrades(10)
	steps[1].beforeServing = true
	fill, cutShort := steps[1].background, true
	steps[1].background = func(tx *sql.Tx, after int64) (int64, bool, error) {
		if cutShort && after == 10 {
			return 0, false, errors.New("cut short")
		}
		return fill(tx, after)
	}
	useUpgrades(t, steps)
	if _, err := Open(ctx, path); err == nil || !strings.Contains(err.Error(), "cannot start until it is finished") {
		t.Fatalf("a start that could not finish the work: %v", err)
	}
	if s := readUpgradeState(t, path); s.version != 3 || !s.backgroundPending || s.backgroundAfter != 10 {
		t.Fatalf("after the start that was cut short the database is %+v, want version 3 with the first batch done", s)
	}
	cutShort = false
	d, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	var unfilled int
	if err := d.read.QueryRow(`SELECT count(*) FROM items WHERE title_length IS NOT length(title)`).Scan(&unfilled); err != nil || unfilled != 0 {
		t.Errorf("the server answers with %d titles not yet filled (%v)", unfilled, err)
	}
	d.Close()
}

// When the work that must be finished before serving fails, the refusal names the copy
// from before the upgrade, at the start that applied the step and at every start after.
func TestAFailureBeforeServingNamesTheCopyFromBeforeTheUpgrade(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "main.db")
	firstReleaseDatabase(t, path, 30)
	steps := trialUpgrades(10)
	steps[1].beforeServing = true
	steps[1].background = func(tx *sql.Tx, after int64) (int64, bool, error) { return 0, false, errors.New("disk I/O error") }
	useUpgrades(t, steps)
	for start := 1; start <= 2; start++ {
		_, err := Open(ctx, path)
		if err == nil || !strings.Contains(err.Error(), "cannot start until it is finished") || !strings.Contains(err.Error(), copyPath(path, 1, 3)) {
			t.Errorf("start %d: the refusal does not name the copy from before the upgrade: %v", start, err)
		}
	}
}

// Stopping the server while it finishes an upgrade before serving is not reported as a
// failure, keeps what was done, and the next start carries on.
func TestAStopDuringWorkBeforeServingIsNotAFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.db")
	firstReleaseDatabase(t, path, 30)
	steps := trialUpgrades(10)
	steps[1].beforeServing = true
	ctx, stop := context.WithCancel(context.Background())
	fill := steps[1].background
	steps[1].background = func(tx *sql.Tx, after int64) (int64, bool, error) {
		if after == 10 {
			stop()
		}
		return fill(tx, after)
	}
	useUpgrades(t, steps)
	_, err := Open(ctx, path)
	if err == nil || !strings.Contains(err.Error(), "was stopped") || strings.Contains(err.Error(), "failed") {
		t.Fatalf("a stop during the work: %v", err)
	}
	if s := readUpgradeState(t, path); s.version != 3 || !s.backgroundPending || s.backgroundAfter < 10 || s.wrongLengths != 0 {
		t.Fatalf("after the stop the database is %+v, want version 3 with at least the first batch kept", s)
	}
	d, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	if s := readUpgradeState(t, path); s.backgroundPending || !s.lengthIndex || s.wrongLengths != 0 {
		t.Fatalf("after the next start the database is %+v, want the work finished", s)
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	in, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestUpgradeAtSize is a measurement, not a check. It runs the trial upgrade through the
// real start-up on a large library's database and times what a person would wait for and
// what they would feel: the copy made first, the quick steps, how long the server takes to
// start, how long the background part takes, and how long people's writes and reads take
// while it runs.
//
// It changes the database it is given, so it runs only when PORTICO_UPGRADE_RUN_DB names a
// copy made for it. PORTICO_UPGRADE_RUN_OUT, if set, receives the numbers as JSON.
func TestUpgradeAtSize(t *testing.T) {
	path := os.Getenv("PORTICO_UPGRADE_RUN_DB")
	if path == "" {
		t.Skip("set PORTICO_UPGRADE_RUN_DB to a seeded database it may change")
	}
	ctx := context.Background()
	useUpgrades(t, trialUpgrades(2000))
	began := time.Now()
	var copied, quick time.Duration
	batches := 0
	upgradePoint = func(point string) {
		switch point {
		case "copy written, not yet named":
			copied = time.Since(began)
		case "step committed":
			quick = time.Since(began) - copied
		case "batch committed":
			batches++
		}
	}
	t.Cleanup(func() { upgradePoint = func(string) {} })
	before, _ := os.Stat(path)
	d, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	started := time.Since(began)

	// People using the server while the background part runs.
	var writes, reads []time.Duration
	var library, kind int64
	if err := d.read.QueryRow(`SELECT library_id, kind FROM items GROUP BY 1, 2 ORDER BY count(*) DESC LIMIT 1`).Scan(&library, &kind); err != nil {
		t.Fatal(err)
	}
	running := true
	for running {
		select {
		case <-d.upgradesDone:
			running = false
		case <-time.After(10 * time.Millisecond):
		}
		at := time.Now()
		err := d.write(ctx, User, func(tx *sql.Tx) error {
			_, err := tx.Exec(`UPDATE libraries SET revision = revision + 1 WHERE id = ?`, library)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		writes = append(writes, time.Since(at))
		at = time.Now()
		rows, err := d.read.Query(`SELECT id, title FROM items WHERE library_id = ? AND kind = ? ORDER BY sort_title, id LIMIT 50`, library, kind)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
		}
		rows.Close()
		reads = append(reads, time.Since(at))
	}
	background := time.Since(began) - started

	s := readUpgradeState(t, path)
	if s.version != 3 || s.backgroundPending || !s.lengthIndex || s.wrongLengths != 0 {
		t.Errorf("the upgrade did not finish: %+v", s)
	}
	backup, _ := os.Stat(copyPath(path, firstVersion, 3))
	ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
	p99 := func(times []time.Duration) float64 {
		slices.Sort(times)
		return ms(times[len(times)*99/100])
	}
	result := map[string]any{
		"titles": s.titles, "databaseBytes": before.Size(), "copyBytes": backup.Size(),
		"copyMs": ms(copied), "quickStepsMs": ms(quick), "serverStartsAfterMs": ms(started),
		"backgroundMs": ms(background), "backgroundBatches": batches,
		"writesDuring": len(writes), "writeP99Ms": p99(writes), "writeLongestMs": ms(slices.Max(writes)),
		"readP99Ms": p99(reads), "readLongestMs": ms(slices.Max(reads)),
	}
	for _, line := range []string{
		fmt.Sprintf("%d titles, database %d MB", s.titles, before.Size()>>20),
		fmt.Sprintf("copy made before the upgrade (%d MB)   %10.1f ms", backup.Size()>>20, ms(copied)),
		fmt.Sprintf("the two quick steps                   %10.1f ms", ms(quick)),
		fmt.Sprintf("server ready after                    %10.1f ms", ms(started)),
		fmt.Sprintf("background part (%d batches of 2,000 titles, then an index)   %10.1f ms", batches, ms(background)),
		fmt.Sprintf("a person's write meanwhile            p99 %.1f ms, longest %.1f ms (%d writes)", result["writeP99Ms"], result["writeLongestMs"], len(writes)),
		fmt.Sprintf("a page of titles meanwhile            p99 %.1f ms, longest %.1f ms", result["readP99Ms"], result["readLongestMs"]),
	} {
		t.Log(line)
	}
	if out := os.Getenv("PORTICO_UPGRADE_RUN_OUT"); out != "" {
		raw, _ := json.MarshalIndent(result, "", "  ")
		if err := os.WriteFile(out, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
