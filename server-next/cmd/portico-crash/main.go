// Command portico-crash kills the real server with SIGKILL at random moments while people's
// writes and an import run against a real database file, and after every kill checks what a
// person would notice: the server serves again, every write it said was saved is there, no
// write it refused shows up, a repeated "create profile" makes one profile, and the database
// passes SQLite's integrity and foreign-key checks. At the end the import is finished and run
// once more, and must find nothing to change and exactly the library it was asked for.
//
// The import is the seeder's, run in a second process (this command started again with
// -import) and killed at the same instant as the server. It pauses briefly after each batch:
// SQLite gives a waiting writer in another process no turn, so without the pause the server's
// writes would wait out their five seconds behind it. In the product the import runs inside
// the server, behind the one writer, and that question does not arise.
//
//	portico-crash -server ./portico-server -kills 200 -items 200000
//
// It exits non-zero on the first broken promise and prints a JSON summary on success.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

const password = "crash test password"

var (
	serverBin  = flag.String("server", "./portico-server", "server binary")
	importDB   = flag.String("import", "", "run only the import into this database file; used for the second process")
	pause      = flag.Duration("pause", 20*time.Millisecond, "with -import: wait this long after each batch")
	work       = flag.String("work", "crash-work", "folder for the database and the logs; emptied first")
	kills      = flag.Int("kills", 20, "how many times to kill the server")
	items      = flag.Int("items", 200_000, "size of the library the import works through")
	maxRun     = flag.Duration("max-run", 3*time.Second, "longest the server runs under load before it is killed")
	checkEvery = flag.Int("check-every", 5, "run the integrity check on the killed database every this many kills; the others are recovered by the server itself")
	writers    = flag.Int("writers", 4, "people writing at once")
	randomSeed = flag.Uint64("random", uint64(time.Now().UnixNano()), "seed for the kill times, printed so a failure can be repeated")
	everyPoint = flag.Bool("every-point", false, "instead of random kills, kill at every change point in turn (both binaries built with -tags crashpoints)")
)

func main() {
	flag.Parse()
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	if *importDB != "" {
		report, err := importOnly(*importDB)
		if err != nil {
			log.Fatal(err)
		}
		json.NewEncoder(os.Stdout).Encode(report)
		return
	}
	if *everyPoint {
		if err := runEveryPoint(); err != nil {
			log.Fatalf("FAILED: %v", err)
		}
		return
	}
	if err := run(); err != nil {
		log.Fatalf("FAILED: %v (rerun with -random %d)", err, *randomSeed)
	}
}

type harness struct {
	data, dbPath, base string
	server, seeder     *exec.Cmd
	serverExited       chan struct{}
	crashAt            int // with -every-point: PORTICO_CRASH_AT for the server
	seederDone         bool
	logs               *os.File
	rnd                *rand.Rand
	people             *ledger
	readyTimes         []time.Duration
}

func run() error {
	ctx := context.Background()
	h := &harness{data: filepath.Join(*work, "data"), rnd: rand.New(rand.NewPCG(*randomSeed, 0)), people: newLedger()}
	h.dbPath = filepath.Join(h.data, "main.db")
	if err := os.RemoveAll(*work); err != nil {
		return err
	}
	if err := os.MkdirAll(h.data, 0o700); err != nil {
		return err
	}
	var err error
	if h.logs, err = os.Create(filepath.Join(*work, "processes.log")); err != nil {
		return err
	}
	log.Printf("random seed %d, %d kills, %d items", *randomSeed, *kills, *items)

	// The libraries exist before anything is killed, so an import that starts again always
	// finds all four.
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

	port, err := freePort()
	if err != nil {
		return err
	}
	h.base = fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := h.start(); err != nil {
		return err
	}
	if err := h.setup(); err != nil {
		return err
	}
	pool, err := h.itemPool(200) // one page
	if err != nil {
		return err
	}
	log.Printf("writing to %d items", len(pool))

	for kill := 1; kill <= *kills; kill++ {
		p, err := h.person(fmt.Sprintf("crash-driver-%d", kill))
		if err != nil {
			return fmt.Errorf("kill %d, after restart: %w", kill, err)
		}
		if err := h.people.verify(p, h); err != nil {
			return fmt.Errorf("kill %d, after restart: %w", kill, err)
		}
		stop := h.load(p, pool, kill)
		time.Sleep(time.Duration(h.rnd.Int64N(int64(*maxRun))))
		h.killAll()
		stop()
		if kill%*checkEvery == 0 || kill == *kills {
			if err := check(ctx, h.dbPath); err != nil {
				return fmt.Errorf("kill %d: %w", kill, err)
			}
		}
		if err := h.start(); err != nil {
			return fmt.Errorf("kill %d: %w", kill, err)
		}
	}

	p, err := h.person("crash-driver-final")
	if err != nil {
		return err
	}
	h.people.everything = true
	if err := h.people.verify(p, h); err != nil {
		return fmt.Errorf("at the end: %w", err)
	}
	report, err := h.finishImport(p)
	if err != nil {
		return err
	}
	h.killAll()
	if err := check(ctx, h.dbPath); err != nil {
		return fmt.Errorf("at the end: %w", err)
	}
	slices.Sort(h.readyTimes)
	report["kills"], report["random"], report["items"] = *kills, *randomSeed, *items
	report["acknowledgedWrites"], report["refusedWrites"], report["unansweredWrites"] = h.people.counts()
	report["serveAgainMedianMs"] = h.readyTimes[len(h.readyTimes)/2].Milliseconds()
	report["serveAgainMaxMs"] = h.readyTimes[len(h.readyTimes)-1].Milliseconds()
	raw, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(raw))
	return nil
}

// importOnly imports the synthetic library into path and reports what it did.
func importOnly(path string) (seed.Totals, error) {
	ctx := context.Background()
	d, err := db.Open(ctx, path)
	if err != nil {
		return seed.Totals{}, err
	}
	defer d.Close()
	return seed.Import(ctx, d, seed.Config{Seed: 1, Items: *items, BatchFiles: 40}, func(seed.Totals) { time.Sleep(*pause) })
}

// importer is this command started again to run only the import.
func (h *harness) importer() *exec.Cmd {
	self, _ := os.Executable()
	return command(self, "-import", h.dbPath, "-items", fmt.Sprint(*items), "-pause", pause.String())
}

// command is the one place this tool starts a process: the server under test, or itself.
func command(name string, args ...string) *exec.Cmd { return exec.Command(name, args...) }

func freePort() (int, error) {
	for range 50 {
		port := 20000 + rand.IntN(40000)
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			l.Close()
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free port found")
}

// start starts the server and, unless it has finished, the import, and waits until the server
// answers. A server that does not answer within 30 seconds is a failure (bar 9 says 10).
func (h *harness) start() error {
	began := time.Now()
	h.server = command(*serverBin, "-data", h.data, "-listen", strings.TrimPrefix(h.base, "http://"))
	h.server.Stdout, h.server.Stderr = h.logs, h.logs
	if h.crashAt > 0 {
		h.server.Env = append(os.Environ(), fmt.Sprintf("PORTICO_CRASH_AT=%d", h.crashAt))
	}
	if err := h.server.Start(); err != nil {
		return err
	}
	exited, server := make(chan struct{}), h.server
	h.serverExited = exited
	go func() { server.Wait(); close(exited) }()
	for {
		var health api.Health
		if code, _ := call("GET", h.base+"/v1/health/ready", "", nil, &health); code == 200 {
			break
		}
		select {
		case <-exited:
			return errServerDied
		default:
		}
		if time.Since(began) > 30*time.Second {
			return fmt.Errorf("the server did not serve within 30 s of starting again")
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.readyTimes = append(h.readyTimes, time.Since(began))
	if !h.seederDone {
		h.seeder = h.importer()
		h.seeder.Stdout, h.seeder.Stderr = h.logs, h.logs
		if err := h.seeder.Start(); err != nil {
			return err
		}
	}
	return nil
}

// killAll kills the server and the import at once, as a power cut or an OOM kill would.
func (h *harness) killAll() {
	h.server.Process.Signal(syscall.SIGKILL)
	if h.seeder != nil {
		h.seeder.Process.Signal(syscall.SIGKILL)
		h.seeder.Wait()
	}
	<-h.serverExited
	// An import that ended by itself before the kill has finished the library.
	if h.seeder != nil && h.seeder.ProcessState.Success() {
		h.seederDone = true
		log.Print("the import finished during the kills")
	}
	h.seeder = nil
}

// check opens the database the way the server does, after a kill and before anything else,
// and runs SQLite's integrity and foreign-key checks.
func check(ctx context.Context, path string) error {
	d, err := db.Open(ctx, path)
	if err != nil {
		return fmt.Errorf("the database does not open: %w", err)
	}
	defer d.Close()
	return d.Check(ctx)
}

func (h *harness) setup() error {
	code, err := os.ReadFile(filepath.Join(h.data, "setup-code.txt"))
	if err != nil {
		return err
	}
	body := api.SetupOwnerRequest{SetupCode: strings.TrimSpace(string(code)), Username: "owner", Password: password,
		Device: device("crash-driver-setup")}
	if status, err := call("POST", h.base+"/v1/setup/owner", "", body, nil); status != 200 {
		return fmt.Errorf("setup answered %d: %v", status, err)
	}
	return nil
}

func device(name string) api.DeviceInfo {
	return api.DeviceInfo{InstallationID: strings.Repeat("0", max(0, 32-len(name))) + name, Name: name}
}

// person signs in as the owner on a new device and chooses the main profile.
type person struct {
	token, profileID string
}

func (h *harness) person(deviceName string) (p person, err error) {
	// Each try is a separate sign-in. One can be refused while the import holds the write
	// lock; that is the two-process setup, not something to stop on.
	for range 5 {
		if p, err = h.signInOnce(deviceName); err == nil {
			return p, nil
		}
		log.Printf("signing in again after: %v", err)
	}
	return p, err
}

func (h *harness) signInOnce(deviceName string) (person, error) {
	var in api.SignInResponse
	status, err := call("POST", h.base+"/v1/auth/sign-in", "", api.SignInRequest{Username: "owner", Password: password, Device: device(deviceName)}, &in)
	if status == 0 {
		return person{}, fmt.Errorf("sign-in: %w: %v", errNoAnswer, err)
	} else if status != 200 {
		return person{}, fmt.Errorf("sign-in answered %d: %v", status, err)
	}
	var chosen api.Session
	status, err = call("POST", h.base+"/v1/profiles/"+in.Profiles[0].ID+"/select", in.Session.AccessToken, api.ProfileSelect{}, &chosen)
	if status == 0 {
		return person{}, fmt.Errorf("choosing the main profile: %w: %v", errNoAnswer, err)
	} else if status != 200 {
		return person{}, fmt.Errorf("choosing the main profile answered %d: %v", status, err)
	}
	return person{token: chosen.AccessToken, profileID: in.Profiles[0].ID}, nil
}

// itemPool waits until the import has made n films and returns their ids.
func (h *harness) itemPool(n int) ([]string, error) {
	p, err := h.person("crash-driver-pool")
	if err != nil {
		return nil, err
	}
	var libraries api.Page[api.Library]
	if status, err := call("GET", h.base+"/v1/libraries", p.token, nil, &libraries); status != 200 || len(libraries.Items) == 0 {
		return nil, fmt.Errorf("listing libraries answered %d: %v", status, err)
	}
	films := libraries.Items[0].ID // the seeder's first library, imported first
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		var page api.ItemPage
		if status, err := call("GET", fmt.Sprintf("%s/v1/items?library=%s&kind=movie&limit=%d", h.base, films, n), p.token, nil, &page); status != 200 {
			return nil, fmt.Errorf("listing items answered %d: %v", status, err)
		}
		if len(page.Items) >= n {
			ids := make([]string, n)
			for i, it := range page.Items[:n] {
				ids[i] = it.ID
			}
			return ids, nil
		}
		time.Sleep(time.Second)
	}
	return nil, fmt.Errorf("the import did not make %d films in five minutes", n)
}

// load starts people writing until the returned function is called. Each writer owns a
// share of the items and writes them in turn, so the order of writes to one item is known.
// One writer also creates a profile with an Idempotency-Key, and one signs in on a new device.
func (h *harness) load(p person, pool []string, kill int) (stop func()) {
	done := make(chan struct{})
	var wg sync.WaitGroup
	for w := range *writers {
		wg.Go(func() {
			for n := 0; ; n++ {
				select {
				case <-done:
					return
				default:
				}
				id := pool[(n**writers+w)%len(pool)]
				position := h.people.nextPosition()
				status, err := call("PUT", h.base+"/v1/items/"+id+"/state", p.token, map[string]int64{"positionMs": position}, nil)
				h.people.wrote(id, position, outcomeOf(status, err))
				if outcomeOf(status, err) == refused && h.people.firstRefusal(status, err) {
					log.Printf("a write was refused with %d: %v", status, err)
				}
			}
		})
	}
	wg.Go(func() {
		h.people.createProfile(h.base, p.token, fmt.Sprintf("Crash %d", kill), fmt.Sprintf("crash-profile-%d", kill))
	})
	wg.Go(func() {
		name := fmt.Sprintf("crash-signin-%d", kill)
		var in api.SignInResponse
		status, err := call("POST", h.base+"/v1/auth/sign-in", "", api.SignInRequest{Username: "owner", Password: password, Device: device(name)}, &in)
		h.people.signedIn(name, in.Session.RefreshToken, outcomeOf(status, err))
	})
	return func() { close(done); wg.Wait() }
}

// finishImport lets the import run to the end with the server up, runs it once more, and
// checks that the second run changes nothing and the library holds exactly what was asked.
func (h *harness) finishImport(p person) (map[string]any, error) {
	report := map[string]any{}
	// The import started at the last restart runs to its end first.
	if h.seeder != nil {
		if err := h.seeder.Wait(); err != nil {
			return nil, fmt.Errorf("the import after the kills failed: %w", err)
		}
		h.seeder, h.seederDone = nil, true
	}
	for _, pass := range []string{"finish", "again"} {
		out, err := h.importer().Output()
		if err != nil {
			return nil, fmt.Errorf("the import after the kills failed: %w", err)
		}
		var result seed.Totals
		if err := json.Unmarshal(out, &result); err != nil {
			return nil, err
		}
		report[pass+"ImportFilesPerSec"] = int(result.FilesPerSec)
		if pass == "again" && (result.RowsWritten != 0 || result.ItemsNew != 0) {
			return nil, fmt.Errorf("an unchanged import after the kills wrote %d rows and made %d items", result.RowsWritten, result.ItemsNew)
		}
	}
	var libraries api.Page[api.Library]
	if status, err := call("GET", h.base+"/v1/libraries", p.token, nil, &libraries); status != 200 {
		return nil, fmt.Errorf("listing libraries answered %d: %v", status, err)
	}
	cfg := seed.Config{Items: *items}
	total := 0
	for _, lib := range libraries.Items {
		var page api.ItemPage
		url := fmt.Sprintf("%s/v1/items?library=%s&kind=%s&limit=1&total=true", h.base, lib.ID, seed.LeafKind(lib.Kind))
		if status, err := call("GET", url, p.token, nil, &page); status != 200 || page.Total == nil {
			return nil, fmt.Errorf("counting %s answered %d: %v", lib.Name, status, err)
		}
		if want := cfg.Leaves(lib.Kind); *page.Total != want {
			return nil, fmt.Errorf("%s holds %d playable items after the kills, want %d", lib.Name, *page.Total, want)
		}
		total += *page.Total
	}
	report["playableItems"] = total
	return report, nil
}
