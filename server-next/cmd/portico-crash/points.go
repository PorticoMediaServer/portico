package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

var pointItems = flag.Int("point-items", 12, "with -every-point: size of the library the import is killed in")

var errServerDied = errors.New("the server died")

// runEveryPoint is the other half of bar 9: rather than at random moments, the server is
// killed at each point where a write can die, one after another. Built with -tags crashpoints,
// a process given PORTICO_CRASH_AT=N kills itself at its Nth change to the database (a row about
// to be inserted, updated or deleted, or a commit). So a fixed sequence of people's writes is run
// for N = 1, 2, 3 and so on until it finishes without dying, each time from the same starting
// database, and after each kill the same promises are checked as after a random one. Then the
// import is killed at each of its points in the same way, and must finish afterwards with
// exactly the library it was asked for.
func runEveryPoint() error {
	ctx := context.Background()
	h := &harness{seederDone: true}
	if err := os.RemoveAll(*work); err != nil {
		return err
	}
	var err error
	if err := os.MkdirAll(*work, 0o700); err != nil {
		return err
	}
	if h.logs, err = os.Create(filepath.Join(*work, "processes.log")); err != nil {
		return err
	}
	port, err := freePort()
	if err != nil {
		return err
	}
	h.base = fmt.Sprintf("http://127.0.0.1:%d", port)

	// empty: the libraries and the owner. library: the same with a small import done.
	empty, library := filepath.Join(*work, "empty"), filepath.Join(*work, "library")
	if err := h.prepare(ctx, empty, library); err != nil {
		return fmt.Errorf("preparing: %w", err)
	}
	pool, err := h.poolFrom(library)
	if err != nil {
		return fmt.Errorf("preparing: %w", err)
	}

	report := map[string]any{}
	people, err := h.everyPersonPoint(ctx, library, pool)
	if err != nil {
		return err
	}
	report["peoplesWritePoints"] = people
	imports, err := h.everyImportPoint(ctx, empty)
	if err != nil {
		return err
	}
	report["importPoints"], report["importItems"] = imports, *pointItems
	raw, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(raw))
	return nil
}

// prepare makes the two starting databases: libraries and an owner, then that plus an import.
func (h *harness) prepare(ctx context.Context, empty, library string) error {
	h.data, h.dbPath = empty, filepath.Join(empty, "main.db")
	if err := os.MkdirAll(empty, 0o700); err != nil {
		return err
	}
	d, err := db.Open(ctx, h.dbPath)
	if err != nil {
		return err
	}
	for _, spec := range seed.Libraries {
		if _, err := d.CreateLibrary(ctx, spec.Name, spec.Kind, "en", []string{spec.Path}); err != nil {
			return err
		}
	}
	d.Close()
	if err := h.start(); err != nil {
		return err
	}
	if err := h.setup(); err != nil {
		return err
	}
	h.stop()
	if err := copyDir(empty, library); err != nil {
		return err
	}
	if d, err = db.Open(ctx, filepath.Join(library, "main.db")); err != nil {
		return err
	}
	defer d.Close()
	_, err = seed.Import(ctx, d, seed.Config{Seed: 1, Items: *pointItems, BatchFiles: 40}, nil)
	return err
}

// poolFrom lists the films the people's writes go to.
func (h *harness) poolFrom(library string) ([]string, error) {
	h.data, h.dbPath = library, filepath.Join(library, "main.db")
	if err := h.start(); err != nil {
		return nil, err
	}
	defer h.stop()
	p, err := h.person("point-pool")
	if err != nil {
		return nil, err
	}
	var libraries api.Page[api.Library]
	if status, err := call("GET", h.base+"/v1/libraries", p.token, nil, &libraries); status != 200 {
		return nil, fmt.Errorf("listing libraries answered %d: %v", status, err)
	}
	var page api.ItemPage
	if status, err := call("GET", h.base+"/v1/items?kind=movie&library="+libraries.Items[0].ID, p.token, nil, &page); status != 200 || len(page.Items) == 0 {
		return nil, fmt.Errorf("listing films answered %d with %d films: %v", status, len(page.Items), err)
	}
	var pool []string
	for _, it := range page.Items {
		pool = append(pool, it.ID)
	}
	return pool, nil
}

// stop ends the server cleanly, so the database it leaves is a starting point.
func (h *harness) stop() {
	h.server.Process.Signal(syscall.SIGTERM)
	<-h.serverExited
}

// everyPersonPoint kills the server at each point of a fixed sequence of people's writes and
// returns how many points the sequence has.
func (h *harness) everyPersonPoint(ctx context.Context, library string, pool []string) (int, error) {
	for n := 1; ; n++ {
		h.data = filepath.Join(*work, "run")
		h.dbPath = filepath.Join(h.data, "main.db")
		os.RemoveAll(h.data)
		if err := copyDir(library, h.data); err != nil {
			return 0, err
		}
		h.people, h.crashAt = newLedger(), n
		err := h.start()
		if err == nil {
			var finished bool
			if finished, err = h.personMix(pool); err != nil {
				return 0, fmt.Errorf("point %d: %w", n, err)
			}
			if finished {
				h.killAll()
				if n == 1 {
					return 0, fmt.Errorf("the server never died: build it and this tool with -tags crashpoints")
				}
				log.Printf("people's writes: all %d points tried", n-1)
				return n - 1, nil
			}
			select {
			case <-h.serverExited:
			case <-time.After(10 * time.Second):
				return 0, fmt.Errorf("point %d: a request went unanswered but the server is still running", n)
			}
		} else if !errors.Is(err, errServerDied) {
			return 0, fmt.Errorf("point %d: %w", n, err)
		}
		// Killed at point n. The database opens and is sound, the restarted server serves, and
		// what it said was saved is there and nothing it refused.
		if err := check(ctx, h.dbPath); err != nil {
			return 0, fmt.Errorf("point %d: %w", n, err)
		}
		h.crashAt = 0
		if err := h.start(); err != nil {
			return 0, fmt.Errorf("point %d, after restart: %w", n, err)
		}
		p, err := h.person("point-check")
		if err == nil {
			err = h.people.verify(p, h)
		}
		h.killAll()
		if err != nil {
			return 0, fmt.Errorf("point %d, after restart: %w", n, err)
		}
	}
}

// personMix is the fixed sequence: sign in and choose a profile, progress on four films,
// create a profile with an Idempotency-Key, sign in on a second device, two more progress
// reports. It stops at the first request left unanswered, which is the server dying, and says
// whether it got to the end. A refusal is a failure: nothing in the sequence should be refused.
func (h *harness) personMix(pool []string) (finished bool, err error) {
	p, err := h.signInOnce("point-driver")
	if err != nil {
		if errors.Is(err, errNoAnswer) {
			return false, nil
		}
		return false, err
	}
	progress := func(i int) (bool, error) {
		id, position := pool[i%len(pool)], h.people.nextPosition()
		status, err := call("PUT", h.base+"/v1/items/"+id+"/state", p.token, map[string]int64{"positionMs": position}, nil)
		h.people.wrote(id, position, outcomeOf(status, err))
		return answered(status, err)
	}
	for i := range 4 {
		if ok, err := progress(i); !ok {
			return false, err
		}
	}
	h.people.createProfile(h.base, p.token, "Point", "point-profile")
	if last := h.people.profiles[len(h.people.profiles)-1]; last.outcome != saved {
		return false, refusedErr(last.outcome, "creating a profile")
	}
	var in api.SignInResponse
	status, err := call("POST", h.base+"/v1/auth/sign-in", "", api.SignInRequest{Username: "owner", Password: password, Device: device("point-second")}, &in)
	h.people.signedIn("point-second", in.Session.RefreshToken, outcomeOf(status, err))
	if ok, err := answered(status, err); !ok {
		return false, err
	}
	for i := 4; i < 6; i++ {
		if ok, err := progress(i); !ok {
			return false, err
		}
	}
	return true, nil
}

var errNoAnswer = errors.New("no answer")

// answered is true for a 2xx, false with no error when no answer came (the kill), and an
// error for a refusal.
func answered(status int, err error) (bool, error) {
	return status >= 200 && status < 300, refusedErr(outcomeOf(status, err), fmt.Sprint("a request answered ", status, ": ", err))
}

func refusedErr(o outcome, what string) error {
	if o == refused {
		return errors.New(what + " was refused")
	}
	return nil
}

// everyImportPoint kills the import at each of its points, starting from libraries with
// nothing in them, and returns how many points it has.
func (h *harness) everyImportPoint(ctx context.Context, empty string) (int, error) {
	path := filepath.Join(*work, "import", "main.db")
	for n := 1; ; n++ {
		os.RemoveAll(filepath.Dir(path))
		if err := copyDir(empty, filepath.Dir(path)); err != nil {
			return 0, err
		}
		killed, err := importOnce(path, n)
		if err != nil {
			return 0, fmt.Errorf("import point %d: %w", n, err)
		}
		if !killed {
			if n == 1 {
				return 0, fmt.Errorf("the import never died: build this tool with -tags crashpoints")
			}
			log.Printf("import: all %d points tried", n-1)
			return n - 1, nil
		}
		if err := check(ctx, path); err != nil {
			return 0, fmt.Errorf("import point %d: %w", n, err)
		}
		if _, err := importOnce(path, 0); err != nil {
			return 0, fmt.Errorf("import point %d: finishing the import failed: %w", n, err)
		}
		if err := libraryExact(ctx, path); err != nil {
			return 0, fmt.Errorf("import point %d: %w", n, err)
		}
	}
}

// importOnce runs the import in its own process, killed at point crashAt unless it is 0, and
// says whether it was killed. An import that fails by itself is an error.
func importOnce(path string, crashAt int) (killed bool, err error) {
	self, _ := os.Executable()
	cmd := command(self, "-import", path, "-items", fmt.Sprint(*pointItems), "-pause", "0s")
	cmd.Env = append(os.Environ(), fmt.Sprintf("PORTICO_CRASH_AT=%d", crashAt))
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.Sys().(syscall.WaitStatus).Signaled() {
		return true, nil
	}
	return false, err
}

// libraryExact checks that the import, run again, changes nothing, and that each library holds
// exactly the playable items asked for: no title lost and none made twice.
func libraryExact(ctx context.Context, path string) error {
	self, _ := os.Executable()
	out, err := command(self, "-import", path, "-items", fmt.Sprint(*pointItems), "-pause", "0s").Output()
	if err != nil {
		return err
	}
	var again seed.Totals
	if err := json.Unmarshal(out, &again); err != nil {
		return err
	}
	if again.RowsWritten != 0 || again.ItemsNew != 0 {
		return fmt.Errorf("an unchanged import afterwards wrote %d rows and made %d items", again.RowsWritten, again.ItemsNew)
	}
	d, err := db.Open(ctx, path)
	if err != nil {
		return err
	}
	defer d.Close()
	libraries, err := d.AllLibraries(ctx)
	if err != nil {
		return err
	}
	owner, cfg := db.Viewer{UserID: 1, ProfileID: 1}, seed.Config{Items: *pointItems}
	for _, lib := range libraries {
		page, err := d.Items(ctx, owner, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{seed.LeafKind(lib.Kind)}, Limit: 1, WithTotal: true})
		if err != nil {
			return err
		}
		if want := cfg.Leaves(lib.Kind); page.Total != want {
			return fmt.Errorf("%s holds %d playable items, want %d", lib.Name, page.Total, want)
		}
	}
	return nil
}

func copyDir(from, to string) error {
	if err := os.MkdirAll(to, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(from)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		in, err := os.Open(filepath.Join(from, e.Name()))
		if err != nil {
			return err
		}
		out, err := os.Create(filepath.Join(to, e.Name()))
		if err == nil {
			_, err = io.Copy(out, in)
			err = errors.Join(err, out.Close())
		}
		in.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
