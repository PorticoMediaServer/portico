package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	adminops "github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/archive7z"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/audiofacts"
	"github.com/PorticoMediaServer/portico/server/internal/backup"
	"github.com/PorticoMediaServer/portico/server/internal/buildinfo"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/dlna"
	"github.com/PorticoMediaServer/portico/server/internal/driveoauth"
	"github.com/PorticoMediaServer/portico/server/internal/enrichment"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/downloads"
	"github.com/PorticoMediaServer/portico/server/internal/eventfeed"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/hostlimits"
	"github.com/PorticoMediaServer/portico/server/internal/httpapi"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/ingestion"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels/dvr"
	librarychannels "github.com/PorticoMediaServer/portico/server/internal/livechannels/library"
	"github.com/PorticoMediaServer/portico/server/internal/localmetadata"
	"github.com/PorticoMediaServer/portico/server/internal/lyrics"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/medialimits"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackruntime"
	"github.com/PorticoMediaServer/portico/server/internal/preparedmedia"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
	"github.com/PorticoMediaServer/portico/server/internal/recordingaccess"
	"github.com/PorticoMediaServer/portico/server/internal/recordingmedia"
	"github.com/PorticoMediaServer/portico/server/internal/recordingroot"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/remotesources"
	"github.com/PorticoMediaServer/portico/server/internal/scanevents"
	"github.com/PorticoMediaServer/portico/server/internal/servicelog"
	"github.com/PorticoMediaServer/portico/server/internal/sourceaccess"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
	"github.com/PorticoMediaServer/portico/server/internal/subtitlevideo"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"github.com/PorticoMediaServer/portico/server/internal/telemetry"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
	"github.com/PorticoMediaServer/portico/server/internal/workpolicy"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// newDriveOAuth keeps every OAuth listener and sweep inside the server's
// supervised lifetime; Store.Close waits for the sweep's done signal.
func newDriveOAuth() *driveoauth.Store {
	store := driveoauth.New(8)
	store.SetSpawn(func(work func()) { supervise.Go("driveoauth.session", work) })
	return store
}

func main() {
	// The bounded archive worker is the installed executable's hidden mode;
	// it applies its limits before parsing and never opens server state.
	if len(os.Args) >= 2 && os.Args[1] == archive7z.Mode {
		os.Exit(archive7z.Main(os.Args[2:], os.Stdin, os.Stdout))
	}
	// The media process shim (mediaexec) replaces itself with the sandboxed
	// tool; it must not start anything else first.
	if handled, e := mediaexec.RunHelper(os.Args[1:]); handled {
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
		}
		os.Exit(1)
	}
	if handled, e := decoder.RunHelper(os.Args[1:]); handled {
		if e != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == reading.PDFTextHelperArg {
		// The isolated PDF text worker: hard child limits, one private input
		// path and JSON stdout. It never starts the server.
		os.Exit(reading.RunPDFTextHelper(os.Args[2]))
	}
	if len(os.Args) == 2 && os.Args[1] == "--health-check" {
		os.Exit(healthCheck())
	}
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		_ = json.NewEncoder(os.Stdout).Encode(buildinfo.Info())
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--portico-rclone-guardian" {
		if mounts.Guardian(os.Stdin) != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--portico-rclone-native" {
		if mounts.NativeHelper(os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--portico-storage-helper" {
		if storage.Helper(os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--portico-subtitle-helper" {
		if subtitles.Helper(os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		return
	}
	if handled, err := serviceCommand(); handled {
		if err != nil {
			log.Print(err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 {
		fmt.Fprintln(os.Stderr, "Unrecognized command or argument.")
		os.Exit(2)
	}
	if os.Getenv(supervise.WorkerEnvironment) != "1" {
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		executable, err := os.Executable()
		if err == nil {
			err = supervise.Process(ctx, executable, os.Args[1:])
		}
		if err != nil {
			log.Print(err)
			os.Exit(1)
		}
		return
	}
	if e := run(); e != nil {
		if errors.Is(e, errRestartRequested) {
			log.Print("Restart requested")
			os.Exit(supervise.RestartExitCode)
		}
		if !errors.Is(e, context.Canceled) {
			serveEarlyStartupFailure(e)
		}
		log.Fatal(e)
	}
}
func env(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}

// defaultBind accepts IPv4 and IPv6 on hosts supporting a dual-stack socket.
// HTTP and TLS share this listener; route authorization applies to both.
const defaultBind = ":32500"

// stateDirFreshThisProcess reports whether this process created the state
// directory's database file. ONB-02 item 4: a foreign-schema warning about a
// directory this process just created can only describe objects this same
// build installed, so housekeeping downgrades it.
var stateDirFreshThisProcess bool

// errRestartRequested asks the supervising parent to start a fresh worker.
var errRestartRequested = errors.New("restart requested: staged restore applies on next start")

// restartRequested is set when a restore is staged and answered, and read when
// the HTTP server has drained.
var restartRequested atomic.Bool

// firstPrivateLANAddress returns the machine's first private LAN IPv4 address
// (10/8, 172.16/12, 192.168/16), or "" when none is configured. ONB-02 item 1
// names it in the first-run setup block so a phone or TV on the same network
// can reach the server.
func firstPrivateLANAddress() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		var ip net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		default:
			continue
		}
		ip = ip.To4()
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			continue
		}
		if ip.IsPrivate() {
			return ip.String()
		}
	}
	return ""
}

// logSetupBlock prints the friendly first-run block while setupRequired is
// true: the LAN URL every start, plus localhost when bound to loopback.
func logSetupBlock(port int, loopback bool) {
	if lan := firstPrivateLANAddress(); lan != "" {
		log.Printf("Portico is ready to set up.\nOpen http://%s:%d in a browser on this network.", lan, port)
	} else {
		log.Printf("Portico is ready to set up.\nOpen http://localhost:%d in a browser on this computer.", port)
	}
	if loopback {
		log.Printf("Open http://localhost:%d to set up Portico.", port)
	}
}
func run() error {
	return runWithInstanceLock(acquireInstanceLock)
}

func runWithInstanceLock(takeLock func(string) (*instanceLock, error)) (runErr error) {
	setupHosts, setupHostsErr := httpapi.ParseSetupHosts(os.Getenv("PORTICO_SETUP_HOSTS"))
	if setupHostsErr != nil {
		return setupHostsErr
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if os.Getenv(supervise.WorkerEnvironment) == "1" {
		supervise.WatchParent(cancel, os.Stdin)
	}
	// Before anything opens a socket or a file: lift the descriptor limit to what
	// the kernel will allow, and derive a conservative Go soft memory limit from
	// physical/cgroup memory unless the owner provided GOMEMLIMIT. This leaves
	// room for SQLite and the operating system; it is not a hard RSS ceiling.
	hostlimits.Apply()
	// On Windows this puts the server in a job object that kills every child when
	// the last handle closes — which is when this process ends, however it ends.
	// Children join the job automatically, so ffmpeg, the helpers and the
	// helpers' own rclone are all covered by this one call. Unix already lets a
	// parent reach its descendants and uses process groups per child instead.
	if err := childprocess.AdoptTree(); err != nil {
		log.Printf("warn: Child processes are not bound to this server's lifetime (%v); a crash may leave media processes running", err)
	}
	layout, e := startupLayout()
	if e != nil {
		return e
	}
	if e = layout.Prepare(); e != nil {
		return e
	}
	state := layout.Data
	startupLog := servicelog.New(servicelog.Options{Directory: storagepaths.Path(state, "logs")})
	installMessageLogOutput(startupLog)
	defer startupLog.Close()
	defer func() {
		if errors.Is(runErr, errRestartRequested) {
			log.Print("Portico is restarting")
		} else if runErr != nil {
			log.Printf("error: [server] Portico stopped: %v", runErr)
		}
	}()
	rcloneStatus := rcloneprogram.Inspect(ctx)
	if rcloneStatus.Available {
		log.Printf("rclone version %s", rcloneStatus.Version)
	} else {
		log.Print("Can't start rclone")
	}
	cache := layout.Cache
	// New state folders start private. An existing folder is never
	// re-tightened here: the console warns about loose permissions and the
	// owner fixes them with an explicit action.
	bind := env("PORTICO_BIND", defaultBind)
	host, _, e := net.SplitHostPort(bind)
	if e != nil {
		return e
	}
	ip := net.ParseIP(host)
	if host == "" {
		ip = net.IPv6unspecified
	}
	// The socket is bound before anything expensive happens, so a client that
	// arrives during startup gets an honest "starting" answer with phase progress
	// instead of a connection refusal, and a supervisor's health check cannot
	// race the database open.
	reporter := newStartupReporter()
	handlerSwitch := newSwitchableHandler(reporter)
	reporter.start("listener", "Bind the network listener")
	listener, e := net.Listen("tcp", bind)
	if e != nil {
		reporter.finish("listener", e)
		return e
	}
	reporter.finish("listener", nil)
	ip = listener.Addr().(*net.TCPAddr).IP
	defer listener.Close()
	// One http.Server serves this socket for the whole life of the process,
	// from bind onwards: the startup reporter first, the real handler once it
	// exists, and the recovery page if startup fails. The connection ceiling and
	// the TLS classifier are in place from the first connection; the owner's
	// saved limit and the certificates are applied to them in place later.
	transport := consoleConnections(listener, connectionBudget(hostlimits.EffectiveMemoryBytes()))
	direct, e := networking.NewDeferredDirectListener(transport)
	if e != nil {
		_ = transport.Close()
		return e
	}
	server := &http.Server{Addr: bind, Handler: transport.protectConsole(handlerSwitch), ConnContext: transport.context, ConnState: transport.state, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10, HTTP2: serverHTTP2Policy()}
	served := make(chan struct{})
	var serveErr error
	supervise.Go("cmd.http-serve", func() { defer close(served); serveErr = server.Serve(direct) })
	defer func() { _ = server.Close(); <-served }()
	defer func() {
		if runErr == nil || errors.Is(runErr, errRestartRequested) || ctx.Err() != nil {
			return
		}
		reporter.fail(runErr)
		handlerSwitch.set(reporter)
		log.Printf("Portico could not start. A recovery page is available on %s; fix the cause and restart the service: %v", listener.Addr(), runErr)
		select {
		case <-ctx.Done():
		case <-served:
			runErr = errors.Join(runErr, serveErr)
		}
		runErr = errors.Join(runErr, ctx.Err())
	}()

	// One process owns one state directory. The lock is taken now — after the
	// listener is bound, so a second instance's refusal is visible, and before
	// anything opens the database or the journal, so a second instance never
	// writes a byte.
	doneInstance := reporter.step("instance", "Take the single-instance lock")
	instance, lockErr := takeLock(storagepaths.Path(state, "server.lock"))
	if errors.Is(lockErr, errInstanceLockHeld) {
		doneInstance(lockErr)
		return fmt.Errorf("another Portico server is already using %s", state)
	}
	if lockErr != nil {
		failure := &instanceLockFailure{state: state, cause: lockErr}
		doneInstance(failure)
		return failure
	}
	defer instance.Close()
	doneInstance(nil)
	if e = resetStartupWork(layout, true); e != nil {
		return e
	}
	if e = layout.ActivateTemp(); e != nil {
		return e
	}

	// A staged restore applies now, before the authority or the database is
	// opened: the current database moves into a pre-restore backup, the staged
	// copy moves into place, and verification runs the ordinary migrations. A
	// failure moves the pre-restore copy back and the server still starts.
	doneRestore := reporter.step("restore", "Apply a staged restore")
	if err := backup.CleanupPartials(state); err != nil {
		log.Printf("warn: Backup staging cleanup: %v", err)
	}
	if err := backup.ApplyStaged(context.Background(), state, verifyRestoredState(state)); err != nil {
		if backup.RestoreUnresolved(state) {
			// Files may already have moved: starting now could serve a
			// half-restored folder or a new empty database. Nothing has been
			// deleted, and the next start resumes the restore.
			return fmt.Errorf("A restore didn't finish (%v). Portico won't start on a half-restored state folder; nothing has been deleted, and the previous data is kept under %s. Fix the cause (for example free disk space or folder permissions), then start Portico again: it resumes the restore.", err, storagepaths.Path(state, backup.BackupsDir))
		}
		log.Printf("warn: Staged restore did not apply (%v); continuing on the current state", err)
	}
	doneRestore(nil)
	// A staged restore may replace an invalid current config. Validate the
	// resulting file only after ApplyStaged has had that opportunity; restored
	// files are also checked before the restore is committed.
	if e = validateStateConfiguration(state); e != nil {
		return e
	}
	// Every FFmpeg and ffprobe this server starts goes through mediaexec. Now
	// that this process owns the state directory, stop what a crashed previous
	// run left encoding (BE-MEDIA-06) and turn on the limits and the ledger.
	if reaped, mediaErr := mediaexec.Configure(mediaexec.Options{StateDir: state, Libraries: append(filepath.SplitList(os.Getenv("PORTICO_DECODER_LIBRARIES")), filepath.SplitList(os.Getenv("PORTICO_LINEAR_DECODER_LIBRARIES"))...)}); mediaErr != nil {
		log.Printf("warn: Media process limits and the orphan ledger are unavailable (%v); media still runs sandboxed where the platform allows", mediaErr)
	} else if reaped > 0 {
		log.Printf("Stopped %d media process group(s) a previous run of this server left running", reaped)
	}
	doneAuthority := reporter.step("authority", "Open the current server authority")
	claimRunner, e := openCurrentAuthority(state)
	doneAuthority(e)
	if e != nil {
		return e
	}
	doneDatabase := reporter.step("database", "Open and install the database")
	if _, err := os.Stat(storagepaths.Path(state, "server.sqlite")); os.IsNotExist(err) {
		stateDirFreshThisProcess = true
	}
	db, e := persistence.Open(storagepaths.Path(state, "server.sqlite"))
	doneDatabase(e)
	if e != nil {
		return e
	}
	defer db.Close()
	backupPolicy := backup.NewPolicyStore(db, state)
	configurationStore := configurationStoreForState(db, state)
	if e = configurationStore.InitializeConfiguration(ctx, layout.Config); e != nil {
		return e
	}
	// Resolve one verified pair. Qualification overlaps independent startup work,
	// but is joined below before constructing the first playback service.
	doneQualification := reporter.step("toolchain", "Verify and qualify the media tools")
	startupMedia, e := operations.StartupMediaSettings(context.Background(), db)
	if e != nil {
		return e
	}
	medialimits.ConfigureSubtitles(startupMedia.SubtitleTrackLimit, startupMedia.SubtitleFileLimitMiB)
	decoder.SetBackgroundConversionLimit(startupMedia.MaxBackgroundSessions)
	ffmpegTool, ffprobeTool := decoder.ResolveTools()
	defaultFFmpegTool, defaultFFprobeTool := ffmpegTool, ffprobeTool
	if startupMedia.FFmpegPath != "" {
		ffmpegTool = startupMedia.FFmpegPath
		ffprobeTool = decoder.CompanionProbe(ffmpegTool, ffprobeTool)
	}
	qualificationCtx, qualificationCancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer qualificationCancel()
	type toolchainResult struct {
		facts decoder.ToolchainFacts
		err   error
	}
	toolchainReady := make(chan toolchainResult, 1)
	supervise.Go("cmd.toolchain-qualification", func() {
		facts, err := decoder.ProbeToolchain(qualificationCtx, ffmpegTool)
		if err == nil && startupMedia.FFmpegPath != "" && len(facts.MissingRequired()) > 0 {
			err = fmt.Errorf("configured FFmpeg lacks required media features: %v", facts.MissingRequired())
		}
		toolchainReady <- toolchainResult{facts, err}
	})

	// A copied state folder rebases Portico's own absolute paths on first
	// start (spec §3): storage stays absolute and the move repairs it.
	if e = persistence.RebaseStatePaths(context.Background(), db, state); e != nil {
		return e
	}
	workAdmission := workpolicy.Service{DB: db}
	adminOperations := adminops.NewAt(db, state)
	adminOperations.ConfigurationCommitted = func(ctx context.Context) error {
		err := configurationStore.FlushConfiguration(ctx)
		if configurationStore.ConfigChanged != nil {
			configurationStore.ConfigChanged(ctx)
		}
		return err
	}
	adminOperations.ApplyBackgroundPolicy(ctx)
	adminOperations.LogoFetch = func(ctx context.Context, locator string) ([]byte, error) {
		return livechannels.ReadSource(ctx, locator, remotemedia.Policy{ReadTimeout: 10 * time.Second}, adminops.LogoUploadBytes)
	}
	adminOperations.Fetch = adminops.FetchReleaseFeed
	// One bounded barrier for every long-lived loop, registered here so it runs
	// before the database closes and after the HTTP server has stopped accepting.
	drain := &drainBarrier{}
	defer drain.wait()
	databasePath := storagepaths.Path(state, "server.sqlite")
	watchdog := dbwork.NewWatchdog(db)
	watchdog.Reopen = func() (*sql.DB, error) { return dbwork.OpenHandle(databasePath, dbwork.DefaultPolicy()) }
	doneClaims := reporter.step("networking-claims", "Verify the networking claim schema")
	e = persistence.VerifyNetworkingClaims(context.Background(), db)
	doneClaims(e)
	if e != nil {
		return e
	}
	doneKeys := reporter.step("claim-keys", "Open the networking claim keys")
	claimKeys, e := networking.OpenCurrentKeys(context.Background(), db, state, claimRunner)
	doneKeys(e)
	if e != nil {
		return e
	}
	defer claimKeys.Close()
	doneLive := reporter.step("live-channels", "Open live channel storage")
	liveSources, e := initializeLiveChannels(db, state)
	doneLive(e)
	if e != nil {
		return e
	}
	doneIdentity := reporter.step("identity", "Load the server identity")
	ident, e := identity.New(db, state)
	doneIdentity(e)
	if e != nil {
		return e
	}
	container := false
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			container = true
		}
	}
	if e = ident.ConfigureResetFile(layout.Data, layout.Keys, container); e != nil {
		return e
	}
	wireNativeServerIdentity(ident)
	// A restore that has just been applied signs everyone out, before anything
	// can be served from the restored sessions. The copy of the previous
	// database the restore set aside is scrubbed of its sessions too.
	if e = signOutAfterRestore(context.Background(), ident, state); e != nil {
		return e
	}
	// A devtrust build on a throwaway e2e state directory seeds its test owner
	// (identity.SeedDevE2EOwner); every other build and state does nothing.
	if e = ident.SeedDevE2EOwner(context.Background()); e != nil {
		return e
	}
	helperBinary, e := os.Executable()
	if e != nil {
		return e
	}
	// Integration merge: lane C's configureLocalMedia (15040d6: source guard only
	// on add/change, managed mount root exempt) plus lane B's owner background
	// priority (b54b6c1) applied to the same storage supervisor.
	storeIO, managed, e := configureLocalMedia(db, state, helperBinary, adminOperations)
	if e != nil {
		return e
	}
	backgroundPriority, e := storedBackgroundPriority(context.Background(), db)
	if e != nil {
		return e
	}
	e = storeIO.Supervisor.SetBackgroundTaskPriority(backgroundPriority)
	if e != nil {
		return e
	}
	sources, e := remotesources.New(db, state, managed)
	if e != nil {
		return e
	}
	storeIO.Remote = sources
	storeIO.Guard = managed.Guard
	storeIO.MountedRoot = managed.RootFor
	readPreparations, e := readprep.New(readprep.DefaultConfig())
	if e != nil {
		return e
	}
	defer readPreparations.Close()
	cat := catalog.New(db)
	cat.UseReadPreparations(readPreparations)
	facetReads, e := catalog.NewFacetRuntime(db, readPreparations, catalog.CheckFacetWindowAuthority, 128<<20)
	if e != nil {
		return e
	}
	genreReads, e := catalog.NewGenreRuntime(db, readPreparations, catalog.CheckFacetWindowAuthority, facetReads)
	if e != nil {
		return e
	}
	fileInfoRuntime, e := catalog.NewFileInfoRuntime(db, readPreparations, catalog.CheckFileInfoAuthority)
	if e != nil {
		return e
	}
	cat.UseGenreRuntime(genreReads)
	cat.SetStorage(storeIO)
	// This is a barrier, not a background best-effort capability update. No
	// playback service is constructed until qualification completes or times out.
	overrideRejected := false
	select {
	case result := <-toolchainReady:
		if result.err != nil {
			if startupMedia.FFmpegPath != "" {
				overrideRejected = true
				log.Printf("Configured FFmpeg could not be qualified; using bundled tools: %v", result.err)
			}
			log.Printf("Media toolchain qualification unavailable: %v", result.err)
		} else {
			decoder.ConfigureToolchain(result.facts)
			playback.SetConversionOutputs(decoder.AvailableConversionOutputs(&result.facts))
			log.Printf("Media toolchain: %s ass=%t zscale=%t", ffmpegTool, result.facts.Filters["ass"], result.facts.Filters["zscale"])
			if missing := result.facts.MissingRequired(); len(missing) > 0 {
				log.Printf("Media toolchain attention: missing %v", missing)
			}
		}
	case <-qualificationCtx.Done():
		if startupMedia.FFmpegPath != "" {
			overrideRejected = true
			log.Printf("Configured FFmpeg qualification timed out; using bundled tools: %v", qualificationCtx.Err())
		}
		log.Printf("Media toolchain qualification unavailable: %v", qualificationCtx.Err())
	}
	qualificationCancel()
	if overrideRejected {
		ffmpegTool, ffprobeTool = defaultFFmpegTool, defaultFFprobeTool
		startupMedia.FFmpegPath = ""
		operations.UseBundledFFmpeg()
		retry, retryCancel := context.WithTimeout(context.Background(), 40*time.Second)
		facts, err := decoder.ProbeToolchain(retry, ffmpegTool)
		retryCancel()
		if err == nil {
			decoder.ConfigureToolchain(facts)
			playback.SetConversionOutputs(decoder.AvailableConversionOutputs(&facts))
		} else {
			log.Printf("Media toolchain qualification unavailable: %v", err)
		}
	}
	if startupMedia.FFmpegPath != "" {
		if e := decoder.ConfigureResolvedTools(ffmpegTool, ffprobeTool); e != nil {
			return e
		}
	}
	doneQualification(nil)
	scanner := ingestion.New(db, cat, assets.Probe{Binary: ffprobeTool, Supervisor: storeIO.Supervisor})
	scanner.Admission = workAdmission.Admit
	scanner.SetStorage(storeIO)
	localMeta, e := localmetadata.New(storagepaths.Path(state, "local-artwork"), ffmpegTool, storeIO)
	if e != nil {
		return e
	}
	localMeta.ArtworkLimit = func(ctx context.Context) int64 { return metadata.LocalArtworkLimit(ctx, db) }
	scanner.LocalMetadata = localMeta
	lyricProvider, e := lyrics.NewLRCLIB(env("PORTICO_LYRICS_PROVIDER_ORIGIN", "https://lrclib.net"))
	if e != nil {
		return e
	}
	lyricService := &lyrics.Service{DB: db, Local: storeIO, Provider: lyricProvider}
	lyricFetch := &lyrics.Bulk{Service: lyricService}
	player := playback.New(db)
	player.PersonalProgress = cat.AcceptPlaybackProgressEvidence
	player.StorageGuard = managed.Guard
	remote := playback.NewRemote(db, storeIO, assets.Probe{Binary: ffprobeTool, Supervisor: storeIO.Supervisor})
	player.ConfigureRemote(remote)
	analysisRoots := sourceaccess.New(storeIO, managed)
	defer analysisRoots.Close()
	remote.AnalysisRoot = func(ctx context.Context, library, path string) (*storage.RootLease, error) {
		sources, err := cat.LibrarySources(ctx, library)
		if err != nil {
			return nil, err
		}
		var selected catalog.LibrarySource
		relative := ""
		for _, source := range sources {
			if !source.Enabled || source.Kind != "local" {
				continue
			}
			rel, err := filepath.Rel(source.ResolvedPath, path)
			if err == nil && filepath.IsLocal(rel) && rel != "." {
				if selected.ID != "" {
					return nil, sourceaccess.ErrAuthority
				}
				selected, relative = source, rel
			}
		}
		if selected.ID == "" || !selected.IdentityConfirmed {
			return nil, sourceaccess.ErrAuthority
		}
		validate := func(check context.Context) error {
			current, err := cat.LibrarySource(check, selected.ID)
			if err != nil {
				return err
			}
			if !current.Enabled || current.LibraryID != library || current.Kind != "local" || current.ResolvedPath != selected.ResolvedPath || current.RootIdentity != selected.RootIdentity || current.Incarnation != selected.Incarnation || current.Generation != selected.Generation {
				return sourceaccess.ErrAuthority
			}
			return nil
		}
		return analysisRoots.Borrow(ctx, sourceaccess.Authority{OwnerID: "strm-analysis:" + identity.Token(), RootID: selected.ID, RootPath: selected.ResolvedPath, RelativePath: relative, Revision: selected.Incarnation + ":" + strconv.FormatInt(selected.Generation, 10), Lifetime: ctx, Validate: validate})
	}
	scanner.AnalyzeSTRM = remote.AnalyzeDescriptor
	hostedOrigin, hostedRoot, hostedRootID, e := hosted.DefaultConfig(os.Getenv("PORTICO_HOSTED_ORIGIN"), os.Getenv("PORTICO_HOSTED_PUBLIC_KEY"), os.Getenv("PORTICO_HOSTED_KEY_ID"))
	if e != nil {
		return e
	}
	control, e := hosted.New(db, ident, hostedOrigin, hostedRoot, hostedRootID)
	if e != nil {
		return e
	}
	subtitleAuthority := httpapi.Dependencies{DB: db, Identity: ident, Hosted: control, Catalog: cat}
	doneSubtitles := reporter.step("subtitles", "Open subtitle storage")
	subtitleProvider := subtitles.ProviderFromEnvironment()
	subtitleService, e := subtitles.New(subtitles.Options{DB: db, Directory: storagepaths.Path(state, "subtitles"), Storage: storeIO, HelperBinary: helperBinary, FFmpeg: ffmpegTool, Authorize: subtitleAuthority.AuthorizeSubtitles, Provider: subtitleProvider})
	if e != nil {
		return e
	}
	defer subtitleService.Close()
	doneSubtitles(nil)
	player.Subtitles = subtitleService
	subtitleRenderer, e := subtitlevideo.New(subtitlevideo.Options{DB: db, Storage: storeIO, Mounts: managed, Directory: filepath.Join(cache, "subtitle-video"), FFmpeg: ffmpegTool, FFprobe: ffprobeTool, Libraries: filepath.SplitList(os.Getenv("PORTICO_DECODER_LIBRARIES"))})
	if e != nil {
		return e
	}
	defer subtitleRenderer.Close()
	subtitleRenderer.RemoteStorage = subtitlevideo.RemoteStorageAdapter{Match: sources.Handles, Selected: sources.OpenSubtitlePlaybackInput, Analysis: sources.OpenMediaAnalysisInput, AnalysisInvocation: func(ctx context.Context) (bool, bool) {
		invocation, ok := mediaanalysis.ReadInvocationFromContext(ctx)
		return ok, ok && invocation.Facts().Consent.Remote
	}, Acquire: func(ctx context.Context, path, purpose string) (subtitlevideo.RemoteObject, error) {
		return sources.Acquire(ctx, path, purpose)
	}}
	doneAnalysis := reporter.step("analysis", "Open media analysis storage")
	analysisService, e := initializeAnalysis(db, state, subtitleRenderer, ffmpegTool, storeIO.Supervisor)
	doneAnalysis(e)
	if e != nil {
		return e
	}
	defer analysisService.Close()
	adminOperations.SonicRuntimeAvailable = analysisService.SonicRuntimeAvailable
	analysisService.Admission = workAdmission.Admit
	scanner.DeepAnalysis = analysisService
	donePrepared := reporter.step("prepared-media", "Open prepared media storage")
	preparedVersions, e := preparedmedia.New(preparedmedia.Options{Sandbox: env("PORTICO_PREPARED_SANDBOX", "bwrap"), DB: db, Directory: filepath.Join(cache, "prepared-media"), FFmpeg: ffmpegTool, FFprobe: ffprobeTool, Libraries: filepath.SplitList(os.Getenv("PORTICO_DECODER_LIBRARIES")), Supervisor: storeIO.Supervisor, OpenInput: subtitleRenderer.OpenMediaInput, Authorize: subtitleAuthority.AuthorizePreparedMedia, Continue: subtitleAuthority.ContinuePreparedMedia})
	if e != nil {
		return e
	}
	defer preparedVersions.Close()
	donePrepared(nil)
	player.Prepared = preparedVersions
	// Offline downloads read original bytes, in place, through the same isolated
	// media process playback uses, and prepared bytes through the producer that
	// owns them. It produces nothing itself and copies nothing.
	offlineDownloads, e := downloads.New(downloads.Options{DB: db, DeliveryConfiguration: player.DeliveryConfiguration, OpenSource: func(ctx context.Context, path string, size, modified int64) (io.ReadSeekCloser, error) {
		return storeIO.OpenPlayback(ctx, path, size, modified)
	}, Artifacts: preparedVersions, Optimizer: preparedVersions, Plays: cat.AcceptPlaybackProgressAt})
	if e != nil {
		return e
	}
	offlineDownloads.Start()
	defer offlineDownloads.Close()
	subtitleRenderer.DiscInputs = player.OpenDiscSubtitleInput
	subtitleRenderer.DiscAvailable = player.DiscSubtitleAvailable
	subtitleService.Extractor = subtitleRenderer
	subtitleService.SourceInputs = subtitleRenderer
	scanner.Subtitles = subtitleService
	player.ConfigureSubtitleDelivery(subtitleService)
	remote.SubtitleInputs = subtitleRenderer
	remote.SubtitleInspector = subtitleRenderer
	// The Portico web app and the shared Cast receiver page may call this server like its own
	// web app. Any other browser origin may call it with a token (an API key, a personal access
	// token or a session); nothing needs configuring for either.
	origins := []string{"https://web.getportico.tv", "https://cast.getportico.tv"}
	claimHandler, e := initializeCurrentClaimControl(db, state, ident, claimRunner, claimKeys, control, origins)
	if e != nil {
		return e
	}
	if claimHandler != nil {
		if e = claimHandler.ConfigureRemote(context.Background(), bind, control.NetworkChanged); e != nil {
			return e
		}
	}
	preparedVersions.Start(ctx)
	generatedChannels, e := librarychannels.New(db)
	if e != nil {
		return errors.New("Library Channel storage could not be initialized")
	}
	operatorRecordingsRoot := os.Getenv("PORTICO_RECORDINGS_DIR")
	recordingsRoot := ""
	doneRecording := reporter.step("recording", "Open recording storage")
	recordingPolicy := recordingaccess.Policy{Cached: control, Schema: recordingaccess.NewSchema()}
	recordingStore, e := dvr.New(db, liveSources, recordingPolicy.Durable)
	if e != nil {
		return e
	}
	if e = recordingStore.ConfigureRetainedCaptures(recordingPolicy.RetainedCapture); e != nil {
		return e
	}

	var recorder *recordingmedia.Driver
	e = recordingroot.Activate(ctx, db, adminOperations, ident, state, operatorRecordingsRoot, func(root string) (recordingroot.StartupBinding, error) {
		prepared, err := recordingmedia.New(root, ffmpegTool, ffprobeTool, filepath.SplitList(os.Getenv("PORTICO_LINEAR_DECODER_LIBRARIES")))
		if err != nil {
			return recordingroot.StartupBinding{}, err
		}
		return recordingroot.StartupBinding{
			Rollback: func() { _ = prepared.Close() },
			Verify:   prepared.ValidateRootBinding,
			Publish: func() {
				recorder = prepared
				recordingsRoot = root
				storeIO.RecordingRoot = root
				storeIO.RecordingLayout = recordingStore.IsRecordingLayoutPath
			},
		}, nil
	})
	if e != nil {
		// Keep the public recovery refusal generic; the local operator needs
		// the underlying cause to distinguish authority, storage and admission.
		log.Printf("Private recording storage startup failed: %v", e)
		return errors.New("private recording storage configuration could not be accepted")
	}
	recordingLocks, e := livechannels.NewPhysicalLocks(storagepaths.Path(state, "live-source-locks"))
	if e != nil {
		_ = recorder.Close()
		return e
	}
	if e = recordingStore.ConfigureCapture(recorder, recordingLocks); e != nil {
		_ = recorder.Close()
		return e
	}
	doneRecording(nil)
	recordingCtx, stopRecordings := context.WithCancel(ctx)
	recordingsDone := make(chan struct{})
	supervise.Go("cmd.recordings", func() {
		defer close(recordingsDone)
		supervise.Loop(recordingCtx, "cmd.recordings", recordingStore.Run)
	})
	defer func() {
		stopRecordings()
		select {
		case <-recordingsDone:
			_ = recorder.Close()
		case <-time.After(15 * time.Second):
			log.Print("Recording shutdown awaits physical reader retirement; durable recovery will reconcile remaining work")
		}
	}()
	donePlaybackRuntime := reporter.step("playback-runtime", "Start the playback runtime")
	currentPlayback, e := playbackruntime.New(db, ident, storeIO)
	donePlaybackRuntime(e)
	supervise.Supervise(ctx, "cmd.subtitles", subtitleService.Run)
	if e != nil {
		return e
	}
	if e = currentPlayback.ConfigureChannels(playbackruntime.LinearConfig{DB: db, Live: liveSources, Library: generatedChannels, Policy: recordingaccess.Policy{Cached: control}, CacheDirectory: filepath.Join(cache, "linear-media"), LockDirectory: storagepaths.Path(state, "live-source-locks"), FFmpeg: ffmpegTool, FFprobe: ffprobeTool, DecoderLibraries: filepath.SplitList(os.Getenv("PORTICO_LINEAR_DECODER_LIBRARIES")), ReadBufferLimits: func(ctx context.Context) (time.Duration, int64, error) {
		snapshot, err := dbwork.BeginSnapshot(ctx, db)
		if err != nil {
			return 0, 0, err
		}
		defer snapshot.Rollback()
		defaults, _, err := livechannels.RuntimeConfigurationTx(ctx, snapshot.Tx(), "")
		if err != nil {
			return 0, 0, err
		}
		return livechannels.TimeshiftLimits(defaults.TimeshiftMinutes)
	}}); e != nil {
		return e
	}
	// Channels on v1 sessions open a Library Channels title the way every v1
	// media read does (spec §18.6, B5).
	if currentPlayback.Linear != nil {
		currentPlayback.Linear.UseMediaInputs(func(ctx context.Context, item, asset string) (subtitles.RenderInput, error) {
			return subtitleRenderer.OpenSubtitleInput(ctx, item, asset, "")
		})
	}
	if e = currentPlayback.Start(ctx); e != nil {
		return e
	}
	defer func() {
		stop, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if err := currentPlayback.Shutdown(stop); err != nil {
			log.Printf("Playback shutdown incomplete: %v", err)
		}
	}()
	// Delivery seams: owner settings default until the administration registry
	// is wired to them, viewer preferences come from the preference registry, and
	// the hardware detector probes this host's ffmpeg once per identity.
	// The registry is the authority for transcoding policy; the console store
	// that publishes it is created below, so the adapter binds to it late.
	deliverySettings := &httpapi.RegistryDeliverySettings{}
	doneDecoder := reporter.step("decoder", "Resolve the media decoder")
	ffmpegPath, ffmpegErr := exec.LookPath(ffmpegTool)
	detector := &decoder.HardwareDetector{}
	if libraries, libErr := decoder.ResolveLibraries(ffmpegPath, ffprobeTool, filepath.SplitList(os.Getenv("PORTICO_LINEAR_DECODER_LIBRARIES"))); libErr == nil {
		detector.Libraries = libraries
	}
	if ffmpegErr != nil {
		ffmpegPath = ""
	}
	doneDecoder(nil)
	player.ConfigureDelivery(deliverySettings, func(tx *sql.Tx, v identity.Viewer, deviceClass string) (playback.DeliveryPreferenceReader, error) {
		values, sources, _, err := operations.EffectivePreferences(tx, v, deviceClass)
		return playback.WithPreferenceSources(values, sources), err
	}, detector, ffmpegPath)
	// Probe encoders once at startup, off the request path, so the first session
	// never waits on ffmpeg. Sessions that arrive earlier take software until then.
	player.ConfigureProbe(assets.Probe{Binary: ffprobeTool, Supervisor: storeIO.Supervisor})
	supervise.Go("cmd.hardware-report", func() { player.HardwareReport(context.Background()) })
	appliedConversionSetting := startupMedia.TemporaryDirectory
	if !layout.AllowsTranscodeDirectory(appliedConversionSetting) {
		log.Printf("Configured conversion folder is outside the container conversion roots; using the default disk folder")
		appliedConversionSetting = ""
	}
	conversionDirectory := layout.Transcode
	if appliedConversionSetting != "" {
		conversionDirectory = operations.ConversionDirectory(layout.Transcode, appliedConversionSetting)
	}
	// The configured folder is always the disk target. A memory preference is
	// independent of that folder and can always fall back or spill here.
	conversionDirectory, _, conversionErr := storagepaths.TranscodeDirectories(conversionDirectory, filepath.Join(layout.Data, "Transcode"), startupMounts())
	if conversionErr != nil {
		log.Printf("Media conversion unavailable: %v", conversionErr)
	}
	layout.Transcode = conversionDirectory
	var hls *playback.HLS
	if conversionErr == nil {
		hls, e = playback.NewHLS(ctx, db, conversionDirectory, ffmpegTool)
	} else {
		e = conversionErr
	}
	if e == nil {
		hls.SourceStorage = storeIO
		hls.WindowInputs = subtitleRenderer
		hls.ConfigureSettings(deliverySettings)
		memoryDirectory := storagepaths.MemoryTranscodeDirectory(conversionDirectory, startupMounts())
		if memoryDirectory != "" {
			if err := hls.ConfigureMemory(memoryDirectory); err != nil {
				log.Printf("Memory conversion storage unavailable; using disk: %v", err)
			} else {
				log.Printf("Conversion storage: disk %s, memory candidate %s", conversionDirectory, memoryDirectory)
			}
		}
		hls.ConfigureMemberCeiling(memberCeilingReader())
		hls.ConfigureProbe(ffprobeTool)
		player.ConfigureHLS(hls)
		supervise.Supervise(ctx, "cmd.hls", hls.Run)
		// Converters are cancelled and then waited for: an ffmpeg child that
		// outlives the process holds its source file open across a restart.
		hlsDrained := make(chan struct{})
		converter := hls
		drain.add("hls-producers", func() {
			supervise.Go("cmd.hls-drain", func() {
				defer close(hlsDrained)
				stop, done := context.WithTimeout(context.Background(), drainBudget)
				defer done()
				if err := converter.Shutdown(stop); err != nil {
					log.Printf("warn: Media conversion shutdown incomplete: %v", err)
				}
			})
		}, hlsDrained)
	} else {
		log.Print("Media conversion unavailable; compatible direct playback remains available")
	}
	meta := metadata.New(db, os.Getenv("PORTICO_TMDB_READ_ACCESS_TOKEN"))
	meta.ConfigurationCommitted = configurationStore.FlushConfiguration
	meta.Admission = workAdmission.Admit
	meta.SubtitleCredentials = subtitleProvider.SetCredentials
	meta.SubtitleStatus = func() metadata.OpenSubtitlesStatus {
		return metadata.OpenSubtitlesStatus(subtitleProvider.OwnerStatus())
	}
	meta.SubtitleCheck = func(ctx context.Context) metadata.OpenSubtitlesStatus {
		return metadata.OpenSubtitlesStatus(subtitleProvider.Check(ctx))
	}
	if e = meta.ConfigureAcoustID(os.Getenv("PORTICO_ACOUSTID_CLIENT_KEY")); e != nil {
		return e
	}
	cat.OwnerMetadataRevision = meta.OwnerTextRevision
	meta.LocalArtwork = localMeta.Open
	meta.LocalArtworkUsage = localMeta.MeasuredArtworkUsage
	if e = meta.SetArtworkDirectory(storagepaths.Path(state, "artwork")); e != nil {
		return e
	}
	meta.EpisodeThumbnails = episodeThumbnailRuntime(meta, sources, ident, storeIO, subtitleRenderer)
	// Bind the same configured metadata service before the scanner can call it.
	// Generation remains closed until its retained source runtime is composed.
	scanner.EpisodeThumbnails = meta.ScanEpisodeThumbnails
	facetLife, stopFacets := context.WithCancel(ctx)
	facetsDone := make(chan struct{})
	supervise.Go("cmd.facet-bases", func() {
		defer close(facetsDone)
		if err := facetReads.Run(facetLife); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("facet base maintenance stopped: %v", err)
		}
	})
	defer func() { stopFacets(); <-facetsDone }()
	supervise.Supervise(ctx, "cmd.remote-sources", sources.Run)
	supervise.Supervise(ctx, "cmd.source-scans", func(life context.Context) { runSourceAdvancedScans(life, scanner, sources) })
	supervise.Supervise(ctx, "cmd.library-channels", generatedChannels.Run)
	supervise.Supervise(ctx, "cmd.live-refresh", func(life context.Context) { liveSources.RunRefresh(life, livechannels.SourceFetcher{}) })
	supervise.Supervise(ctx, "cmd.live-logo-import", adminOperations.RunLogoImports)
	supervise.Supervise(ctx, "cmd.guide-image-import", adminOperations.RunGuideImageImports)
	supervise.Supervise(ctx, "cmd.managed-mounts", managed.Run)
	scanDone := make(chan struct{})
	retentionDone := make(chan struct{})
	supervise.Go("cmd.scanner", func() {
		defer close(scanDone)
		supervise.Loop(ctx, "cmd.scanner", scanner.Run)
	})
	supervise.Go("cmd.analysis-retention", func() {
		defer close(retentionDone)
		supervise.Loop(ctx, "cmd.analysis-retention", analysisService.RunRetention)
	})
	segmentsDone := make(chan struct{})
	supervise.Go("cmd.segment-detection", func() {
		defer close(segmentsDone)
		supervise.Loop(ctx, "cmd.segment-detection", analysisService.RunSegments)
	})
	drain.add("scanner", cancel, scanDone)
	drain.add("analysis-retention", nil, retentionDone)
	drain.add("segment-detection", nil, segmentsDone)
	// The Portico title dataset: a check on first need, then weekly at this
	// install's own offset, for the domains this server has libraries for, and
	// works matched as they gain provider ids. The hourly wake also paces a
	// failed check's backoff. Off until a release is signed (enrichment.Published*).
	titles := enrichment.New(db, enrichment.ConfigFromEnvironment(storagepaths.Path(state, "title-dataset")))
	if titles.Enabled() {
		supervise.Supervise(ctx, "cmd.title-dataset", func(life context.Context) {
			wake := worker.NewSignal()
			// server_settings: the owner's switch takes effect at once.
			unregister := dbwork.WakeOnTables(wake, "catalog_external_ids", "catalog_libraries", "server_settings")
			defer unregister()
			worker.Run(life, "cmd.title-dataset", wake, func(ctx context.Context) time.Duration {
				more, err := titles.Step(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia), 500)
				if err != nil {
					log.Printf("Title dataset: %v", err)
					return time.Hour
				}
				if more {
					return time.Second
				}
				return time.Hour
			})
		})
	}
	// Ratings are classified after publication by this bounded background
	// worker. Restricted reads treat a pending spelling as unrated immediately.
	supervise.Supervise(ctx, "cmd.rating-classification", func(life context.Context) {
		wake := worker.NewSignal()
		unregister := dbwork.WakeOnTables(wake, "catalog_item_attributes")
		defer unregister()
		worker.Run(life, "cmd.rating-classification", wake, func(ctx context.Context) time.Duration {
			more, err := cat.ClassifyPendingRatings(ctx)
			if err != nil {
				log.Printf("Content rating classification: %v", err)
				return 15 * time.Second
			}
			if more {
				return time.Millisecond
			}
			return 0
		})
	})
	supervise.Supervise(ctx, "cmd.automatic-collections", func(life context.Context) {
		wake := worker.NewSignal()
		unregister := dbwork.WakeOnTables(wake, "catalog_automatic_collections")
		defer unregister()
		worker.Run(life, "cmd.automatic-collections", wake, func(ctx context.Context) time.Duration {
			if err := cat.PruneRemovedTitles(ctx); err != nil {
				log.Printf("Removed titles: %v", err)
				return 15 * time.Second
			}
			more, err := cat.SyncAutomaticCollections(ctx)
			if err != nil {
				log.Printf("Automatic collections: %v", err)
				return 15 * time.Second
			}
			if more {
				return time.Millisecond
			}
			return time.Hour
		})
	})

	// Audio facts for client-side decoding (spec §18.7): a background pass over
	// the library's audio assets, woken by catalog changes, one measurement at a
	// time; a play measures a missing one itself (bounded).
	supervise.Supervise(ctx, "cmd.audio-facts", func(life context.Context) {
		wake := worker.NewSignal()
		unregister := dbwork.WakeOnTables(wake, "catalog_assets", "catalog_asset_links", "audio_render_fact_requests")
		defer unregister()
		backfill := &audiofacts.Backfill{DB: db, Measure: subtitleRenderer.MeasureAudio}
		worker.Run(life, "cmd.audio-facts", wake, backfill.Step)
	})
	// Author/series navigation identities are derived from book and tag
	// publications. Keeping the batch here makes audiobook reads read-only.
	// Only books and book_files commits can name a new author or series (every
	// audiobook ingest writes book_files); another library's scan does not wake
	// it (B85). The step runs a pass at startup and after such a change.
	supervise.Supervise(ctx, "cmd.listening-groups", func(life context.Context) {
		wake, changed := worker.NewSignal(), worker.NewSignal()
		unregisterWake := dbwork.WakeOnTables(wake, "books", "book_files")
		defer unregisterWake()
		unregisterChanged := dbwork.WakeOnTables(changed, "books", "book_files")
		defer unregisterChanged()
		worker.Run(life, "cmd.listening-groups", wake, cat.ListeningGroupStep(changed))
	})
	supervise.Supervise(ctx, "cmd.metadata", meta.Run)
	supervise.Supervise(ctx, "cmd.artwork", meta.RunArtwork)
	supervise.Supervise(ctx, "cmd.profile-purge", ident.RunProfilePurge)
	supervise.Supervise(ctx, "cmd.hosted-control", control.Run)
	setupCtx, stopSetup := context.WithCancel(ctx)
	setupDone := make(chan struct{})
	supervise.Go("cmd.setup-maintenance", func() {
		defer close(setupDone)
		supervise.Loop(setupCtx, "cmd.setup-maintenance", ident.RunSetupMaintenance)
	})
	drain.add("setup-maintenance", stopSetup, setupDone)
	// Four retention sweeps that used to run every minute for the life of the
	// process. Every one of them only has something to remove after a write, so
	// they are woken by commits: a server nobody is using runs none of them and
	// asks the database nothing, and a busy one runs them sooner than a minute
	// rather than later.
	supervise.Supervise(ctx, "cmd.minute-cleanup", func(ctx context.Context) {
		wake, unregister := minuteCleanupWake()
		defer unregister()
		lastArtwork := time.Time{}
		lastMissing := time.Time{}
		worker.Run(ctx, "cmd.minute-cleanup", wake, func(ctx context.Context) time.Duration {
			// Missing titles follow the owner's rule (immediately, after a number
			// of days, never). A short, bounded pass; a long list is worked off
			// over several passes so nothing else waits on it.
			if time.Since(lastMissing) >= 30*time.Second {
				lastMissing = time.Now()
				pass, cancel := context.WithTimeout(dbwork.WithClass(ctx, dbwork.ClassMaintenance), 2*time.Second)
				_, _ = cat.EmptyMissingDue(pass)
				cancel()
			}
			_ = player.Cleanup()
			_ = cat.CleanupPersonalReceipts()
			_ = cat.CleanupBulkJobs(dbwork.WithClass(ctx, dbwork.ClassMaintenance), 250*time.Millisecond)
			_ = lyricService.Cleanup(ctx)
			if time.Since(lastArtwork) >= metadata.ArtCleanupInterval {
				lastArtwork = time.Now()
				_ = meta.CleanupArtwork(ctx)
			}
			return 0
		})
	})
	doneConsole := reporter.step("console", "Open the operations console")
	console, scheduler, e := initializeConsole(db, scanner, player, hls, cat, lyricFetch, state, configurationStore)
	if console != nil && e == nil {
		console.FFmpegValidation = operations.FFmpegValidator(ffmpegTool)
		_ = console.FFmpegValidation(ctx, startupMedia.FFmpegPath)
		console.AppliedTemporaryDirectory = &appliedConversionSetting
		console.TranscodeDirectoryAllowed = layout.AllowsTranscodeDirectory
		console.AppliedFFmpegPath = &startupMedia.FFmpegPath
		console.RecordingRoot, e = recordingroot.Runtime(adminOperations, recordingStore, recordingsRoot, operatorRecordingsRoot != "")
		if e != nil {
			return errors.New("recording folder admission could not be initialized")
		}
	}
	doneConsole(e)
	if e != nil {
		return e
	}
	// Plain state (spec §6): loose permissions warn, never refuse. The check
	// runs beside startup so it never delays listening.
	supervise.Go("cmd.state-permissions", func() {
		exposed, err := backup.CheckPermissions(state)
		if err != nil {
			log.Printf("State permissions check: %v", err)
			return
		}
		if err = console.Alert(context.Background(), "state-permissions", "warning", exposed); err != nil {
			log.Printf("State permissions check: %v", err)
		} else if exposed {
			log.Print("State permissions: other accounts on this computer can read your Portico data, including sign-in secrets; the console offers a fix")
		}
	})
	deliverySettings.Console = console
	deliverySettings.Refresh(ctx)
	if e = scheduler.Register(preparedVersions.Adapter()); e != nil {
		return e
	}
	supervise.Supervise(ctx, "cmd.job-scheduler", scheduler.Run)
	supervise.Supervise(ctx, "cmd.lyric-fetch", lyricFetch.Run)
	measurements := operations.NewMeasurements(db, state)
	measurements.ActiveTranscodes = func(ctx context.Context) (int, error) {
		status, err := player.DeliveryDiagnostics(ctx)
		if err != nil {
			return 0, err
		}
		if status.ActiveConversionSessions == nil {
			return 0, nil
		}
		return *status.ActiveConversionSessions, nil
	}
	_, bindPort, _ := net.SplitHostPort(bind)
	bindPortNumber, _ := strconv.Atoi(bindPort)
	doneAdministration := reporter.step("administration", "Open administration and access")
	administration, closeAdministration := initializeAdministration(ctx, db, console, ident, state, bindPortNumber, startupLog)
	defer closeAdministration()
	if err := administration.Access.LoadInvitationKey(layout.Keys); err != nil {
		doneAdministration(err)
		return fmt.Errorf("invitation key: %w", err)
	}
	doneAdministration(nil)
	// The owner's preferred LAN interface is read from the console once per
	// route observation; a read error keeps the last value (automatic until read).
	if manager := claimHandler.Remote(); manager != nil && console != nil {
		var advertisedMu sync.Mutex
		var lastAdvertised string
		manager.SetAdvertisedInterface(func(ctx context.Context) string {
			document, err := console.RuntimeSettings(ctx, operations.AllowServerScope)
			advertisedMu.Lock()
			defer advertisedMu.Unlock()
			if err == nil {
				lastAdvertised = document.Effective.AdvertisedInterface
			}
			return lastAdvertised
		})
	}
	// The owner's certificate for a custom domain. The TLS handshake asks for
	// its source on every connection, so the settings are cached: read at most
	// every 30 seconds, and at once after any settings save (SettingsApplied
	// invalidates). A read error keeps the last values. The same instance serves
	// handshakes and the connectivity report.
	customCertificate := networking.NewCustomCertificate()
	invalidateCustomSource := func() {}
	if console != nil {
		var customMu sync.Mutex
		var customRead time.Time
		var lastPath, lastKey, lastDomain string
		invalidateCustomSource = func() {
			customMu.Lock()
			customRead = time.Time{}
			customMu.Unlock()
		}
		customCertificate.SetSource(func(ctx context.Context) (string, string, string) {
			customMu.Lock()
			defer customMu.Unlock()
			if !customRead.IsZero() && time.Since(customRead) < 30*time.Second {
				return lastPath, lastKey, lastDomain
			}
			if ctx == nil {
				ctx = context.Background()
			}
			document, err := console.RuntimeSettings(ctx, operations.AllowServerScope)
			customRead = time.Now()
			if err == nil {
				lastPath, lastKey, lastDomain = document.Effective.CustomCertificatePath, document.Effective.CustomCertificateKeyPath, document.Effective.CustomCertificateDomain
				if !document.Effective.OwnAddressEnabled {
					lastPath, lastKey, lastDomain = "", "", ""
				}
			}
			return lastPath, lastKey, lastDomain
		})
	}
	doneRouter := reporter.step("router", "Build the request router")
	adminOperations.DVR = recordingStore
	events := eventfeed.New(db)
	backupService := backup.New(state, db, ident.ID(), buildinfo.Info()["version"], persistence.SchemaVersion())
	if e = backup.CleanupScratch(state); e != nil {
		return e
	}
	// A backup interrupted by the last stop left its artwork hold; its
	// scratch is gone now, so artwork clean-up may resume.
	if e = backupService.ReleaseInterruptedHold(ctx); e != nil {
		return e
	}
	backupPolicy.ConfigurationCommitted = configurationStore.FlushConfiguration
	backupService.SetPolicyStore(backupPolicy)
	// A backup holds no sessions: each database copy is scrubbed as it is written.
	backupService.SetCopyScrubber(identity.ScrubBackupCopy)
	requestRestart := func() {
		restartRequested.Store(true)
		// The staged reply must flush before leaving the way SIGTERM does.
		supervise.Go("cmd.restore-restart", func() {
			time.Sleep(2 * time.Second)
			cancel()
		})
	}
	if policy, e := backupPolicy.Settings(ctx); e != nil {
		return e
	} else if e = backup.CleanupDestinationPartials(state, policy.BackupFolder); e != nil {
		log.Printf("warn: Backup destination is unavailable; choose an available backup folder in Settings")
		_ = console.Alert(ctx, "backup-failed", "warning", true)
	}

	backupService.SetRestartFunc(requestRestart)
	backupService.SetRestoreAvailable(true)
	outboundWebhooks := startWebhooks(ctx, db, ident, backupService, adminOperations, console)
	// The backup schedule: every five minutes a window holding the backup
	// task may start one scheduled backup, then prune to the keep count.
	backupScheduler := backupService.Schedule(db, func(scheduleCtx context.Context) (int, error) {
		return adminOperations.BackupKeepCount(scheduleCtx), nil
	})
	backupScheduler.ReportFailure = func(ctx context.Context, failed bool) { _ = console.Alert(ctx, "backup-failed", "warning", failed) }
	supervise.Supervise(ctx, "cmd.backup-schedule", backupScheduler.Run)
	supervise.Supervise(ctx, "cmd.owner-notices", func(ctx context.Context) {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			administration.Logs.Prune()
			administration.Logs.PruneFiles()
			free, total, _ := telemetry.VolumeUsage(state)
			maintenanceCtx := dbwork.WithClass(ctx, dbwork.ClassMaintenance)
			if err := console.NotificationMaintenance(maintenanceCtx, free, total); err != nil && ctx.Err() == nil {
				log.Printf("warn: Owner notices: %v", err)
			}
			if document, err := console.RuntimeSettings(maintenanceCtx, operations.AllowServerScope); err == nil {
				if err := administration.ClientLogs.Prune(maintenanceCtx, document.Effective.LogRetentionDays()["client"]); err != nil && ctx.Err() == nil {
					log.Printf("warn: Client report retention: %v", err)
				}
				info := buildinfo.Info()
				current := adminops.UpdateBuild{Version: info["version"], BuildID: info["buildId"], SourceDigest: info["sourceDigest"], BuiltAt: info["builtAt"]}
				report := adminOperations.CachedUpdates(maintenanceCtx, current, document.Effective.UpdateChannel, document.Effective.UpdateFeedURL)
				if document.Effective.UpdateFeedURL == "" || report.CheckedAt != "" && report.Status != "unavailable" {
					_ = console.Alert(maintenanceCtx, "update-available", "info", report.UpdateAvailable)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
	events.LibraryVisible = func(tx *sql.Tx, p identity.Principal, library string) bool {
		return control.AllowedTx(p, library, tx) == nil
	}
	// Published access URLs come from the console settings (plus the custom
	// certificate domain); a settings save wakes the remote manager so the
	// changed list publishes at once — the route digest already covers the
	// observation, so it publishes exactly once.
	if manager := claimHandler.Remote(); manager != nil {
		manager.SetAccessURLs(accessURLsProvider(console, manager))
	}
	// Both storage pages read one maintained measurement; the data folder
	// walk is part of the same pass.
	adminOperations.DataUsage = func(ctx context.Context) (storagepaths.Usage, error) {
		var mounts []storagepaths.Mount
		if file, err := os.Open("/proc/self/mountinfo"); err == nil {
			mounts = storagepaths.ParseMountInfo(file)
			file.Close()
		}
		return layout.Usage(ctx, mounts)
	}
	apiDependencies := httpapi.Dependencies{TelemetryContext: ctx, RcloneProgram: &rcloneStatus, DriveOAuth: newDriveOAuth(), CacheDirectory: cache, StorageLayout: &layout, LocalArtworkUsage: localMeta.ArtworkUsage, ConnectionUsage: func() httpapi.ConnectionUsage {
		held, _ := transport.Held()
		ordinary, ordinaryLimit := transport.ordinaryUsage()
		return httpapi.ConnectionUsage{Ordinary: ordinary, Total: held, OrdinaryLimit: ordinaryLimit, ConsoleReserve: consoleConnectionReserve}
	}, Restart: requestRestart, ListeningPort: listener.Addr().(*net.TCPAddr).Port, LocalHTTPURL: networking.LoopbackHTTPURL(listener.Addr()), Events: events, Administration: adminOperations, Backups: backupService, Access: administration, Webhooks: outboundWebhooks, SettingsApplied: func(ctx context.Context) {
		control.NotifySettingsChanged(ctx)
		deliverySettings.Refresh(ctx)
		claimHandler.Remote().Wake()
		invalidateCustomSource()
	}, WebDirectory: os.Getenv("PORTICO_WEB_DIR"), Analysis: analysisService, Prepared: preparedVersions, Downloads: offlineDownloads, DVR: recordingStore, Subtitles: subtitleService, Lyrics: lyricService, LyricsBulk: lyricFetch, LyricsProbe: ffprobeTool, LibraryChannels: generatedChannels, Console: console, Scheduler: scheduler, Measurements: measurements, Networking: claimHandler, CustomCertificate: customCertificate, RouteIdentity: networking.NewRouteIdentityHandler(claimKeys, claimRunner, claimHandler.Certificates(), customCertificate), LiveChannels: liveSources, PlaybackRuntime: currentPlayback, AudioMedia: subtitleRenderer, Storage: storeIO, Mounts: managed, RemoteSources: sources, DB: db, Watchdog: watchdog, Identity: ident, Catalog: cat, FacetReads: facetReads, GenreReads: genreReads, FileInfoRuntime: fileInfoRuntime, Ingestion: scanner, Playback: player, Hosted: control, Metadata: meta, Origins: origins, SetupHosts: setupHosts}
	apiDependencies.PrepareBrowseSelections = true
	apiDependencies.PrepareReadingFacets = true
	apiDependencies.PrepareReadingBrowse = true
	// Private capture custody is independent of the still-closed remote disc
	// admission path. Its lifecycle reconciles exact owned receipts on restart.
	ownedCaptures, e := initializeOwnedCaptures(apiDependencies, cache)
	if e != nil {
		return e
	}
	apiDependencies.DiscCaptures = ownedCaptures
	var conditional playback.DiscConditionalSource
	if ownedCaptures != nil {
		defer ownedCaptures.Close()
		defer func() {
			stop, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = player.CloseDiscPlay(stop)
		}()
		ownedCaptures.Start(ctx)
		discConditional, err := remotesources.NewDiscConditionalSource(remotesources.CaptureProducerOptions{Sources: sources, Identities: ident, AuthorizeTx: apiDependencies.AuthorizeSubtitles})
		if err != nil {
			return err
		}
		conditional = discConditional
	}
	apiDependencies.DiscConditional = conditional
	readingService := httpapi.NewReadingService(apiDependencies)
	// The worker distinguishes bytes the artwork pipeline refused as an image
	// from transient acquisition failures, without the reading package
	// depending on the metadata package.
	readingService.AdoptCover = func(ctx context.Context, item, revision string, raw []byte, authorize func(*sql.Tx) error) error {
		err := meta.AdoptReadingCover(ctx, item, revision, raw, authorize)
		if errors.Is(err, metadata.ErrArtworkUpload) {
			return fmt.Errorf("%w: %v", reading.ErrCoverInvalid, err)
		}
		return err
	}
	mobiConverter, mobiErr := configuredReadingMobi(storeIO, cache)
	if mobiErr != nil {
		log.Printf("warn: MOBI/AZW3 reading conversion is unavailable; the configured bundle was not admitted")
	} else {
		readingService.Mobi = mobiConverter
	}
	pdfRaster, pdfRasterErr := configuredReadingPDFRaster(storeIO, cache)
	if pdfRasterErr != nil {
		log.Printf("warn: PDF raster rendering is unavailable; the configured bundle was not admitted")
	} else {
		readingService.PDFRaster = pdfRaster
	}
	cat.ConfigureReadingMobi(readingService.Mobi != nil)
	defer readingService.Close()
	apiDependencies.Reading = readingService
	// Unopened EPUB coverage: the same complete counter and text-count custody
	// as an open, run by the registered background worker under the existing
	// metadata admission and source/scene fences. Bounded indexed passes resume
	// from the adopted rows; failures stay unknown and are retried after a rest.
	textCounts := &reading.TextCountPreparation{DB: db, Prepare: readingService.PrepareTextCount, Index: readingService.PrepareTextIndex, PrimaryPublicationSQL: readingService.PrimaryPublicationSQL, Admission: workAdmission.Admit}
	textCountWake := worker.NewSignal()
	readingService.TextIndexChanged = textCountWake.Wake
	supervise.Supervise(ctx, "cmd.reading-text-counts", func(life context.Context) {
		unregister := dbwork.WakeOnTables(textCountWake, "reading_publications", "inventory_objects", "library_sources", "catalog_assets")
		defer unregister()
		worker.LowerPriority()
		worker.Run(life, "cmd.reading-text-counts", textCountWake, textCounts.Step)
	})
	// Unopened cover coverage: the same explicit EPUB cover item or first comic
	// page an open adopts, under the registered background source authority and
	// existing metadata admission. Determined outcomes are recorded per exact
	// observation so coverless files are not reopened on every pass, and owner
	// or other selected art is never replaced.
	covers := &reading.CoverPreparation{DB: db, Prepare: readingService.PrepareCover, PrimaryPublicationSQL: readingService.PrimaryPublicationSQL, Admission: workAdmission.Admit}
	supervise.Supervise(ctx, "cmd.reading-covers", func(life context.Context) {
		wake := worker.NewSignal()
		unregister := dbwork.WakeOnTables(wake, "reading_publications", "inventory_objects", "library_sources", "catalog_assets")
		defer unregister()
		worker.LowerPriority()
		worker.Run(life, "cmd.reading-covers", wake, covers.Step)
	})
	previousFamilyRevoked := ident.OnFamilyRevokedTx
	ident.OnFamilyRevokedTx = func(ctx context.Context, tx *sql.Tx, family identity.FamilyState) error {
		if previousFamilyRevoked != nil {
			if err := previousFamilyRevoked(ctx, tx, family); err != nil {
				return err
			}
		}
		return readingService.OnFamilyRevokedTx(ctx, tx, family)
	}
	apiDependencies.SelectionActions = httpapi.NewSelectionActions(apiDependencies)
	defer apiDependencies.SelectionActions.Close()
	supervise.Go("cmd.selection-actions", func() {
		for ctx.Err() == nil {
			if err := apiDependencies.SelectionActions.Run(ctx); err != nil && ctx.Err() == nil {
				log.Printf("Selection action worker paused: %v", err)
			} else {
				return
			}
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	})
	handler := httpapi.New(apiDependencies)
	backupService.SetBeforeSnapshot(func(ctx context.Context) error {
		flush, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return httpapi.FlushPlaybackObservations(flush, handler)
	})
	configCtx, stopConfig := context.WithCancel(ctx)
	configDone := make(chan struct{})
	supervise.Go("cmd.config-file", func() {
		defer close(configDone)
		console.ConfigFile.Watch(configCtx, time.Second, func() {
			if err := console.ImportConfiguration(configCtx); err != nil {
				log.Printf("Config file could not be applied: %v", err)
			}
		}, func(err error) { log.Printf("Config file could not be re-read: %v", err) })
	})
	defer func() { stopConfig(); <-configDone }()

	// The timeout and 5xx log lines name the registered route pattern, which
	// only the router can resolve for a request seen outside the mux.
	measurements.Pattern = httpapi.RoutePattern
	doneRouter(nil)
	if claimHandler != nil {
		supervise.Supervise(ctx, "networking.claim.web-recovery", func(ctx context.Context) {
			claimHandler.RunWebClaimRecovery(ctx, httpapi.NetworkingClaimResume(ident))
		})
	}
	supervise.Supervise(ctx, "cmd.api-events", events.Run)
	supervise.Supervise(ctx, "cmd.scan-events", func(ctx context.Context) { scanevents.Run(ctx, db, cat.InventoryStatus, scanner) })
	// The outermost wrapper: a handler panic is counted and attributed here, and
	// answered with an honest 500 when nothing has reached the client yet.
	handlerSwitch.set(supervise.HandlerMiddleware(measurements.Wrap(handler)))
	serverReadyHook()
	supervise.Go("cmd.started-notice", func() { announceStarted(ctx, outboundWebhooks) })
	supervise.Supervise(ctx, "cmd.visibility-rebuilder", cat.RunVisibilityRebuilder)
	supervise.Supervise(ctx, "cmd.compact-catalogue", func(life context.Context) {
		worker.LowerPriority()
		compactcatalog.NewWorker(db).Run(life)
	})
	supervise.Go("cmd.catalogue-retention", func() {
		worker.LowerPriority()
		compactcatalog.RunRetention(ctx, db)
	})
	connectionLimit := connectionBudget(hostlimits.EffectiveMemoryBytes())
	if console != nil {
		appliedConnectionLimit := 0
		if document, err := console.RuntimeSettings(ctx, operations.AllowServerScope); err == nil {
			appliedConnectionLimit = document.Requested.ConnectionLimit
			if appliedConnectionLimit > 0 {
				connectionLimit = appliedConnectionLimit
			}
		}
		console.AppliedConnectionLimit = &appliedConnectionLimit
	}
	transport.setOrdinaryLimit(connectionLimit)
	transport.setOwnerRequest(func(r *http.Request) bool {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			return false
		}
		p, err := ident.Authenticate(strings.TrimPrefix(header, "Bearer "))
		return err == nil && p.Role == "owner"
	})
	// The watchdog and the deferred database housekeeping both start only now, so
	// neither competes with the work of coming up.
	supervise.Supervise(ctx, "cmd.database-watchdog", watchdog.Run)
	supervise.Go("cmd.deferred-maintenance", func() {
		worker.LowerPriority()
		runDeferredMaintenance(dbwork.WithClass(ctx, dbwork.ClassMaintenance), db)
		// A changed episode name reader reads the sources the old one left with
		// a naming issue once more (NEW-26); a scan never re-reads them.
		if err := cat.ReparseEpisodicIssues(dbwork.WithClass(ctx, dbwork.ClassMaintenance)); err != nil && ctx.Err() == nil {
			log.Printf("Episode names couldn't all be read again; the rest resume on the next start: %v", err)
		}
	})
	supervise.Supervise(dbwork.WithClass(ctx, dbwork.ClassMaintenance), "cmd.database-housekeeping", func(life context.Context) {
		worker.LowerPriority()
		runDatabaseHousekeeping(life, db)
	})
	supervise.Go("hosted.storage", func() { control.ObserveStorage(ctx, state) })
	httpShutdownDone := make(chan struct{})
	supervise.Go("cmd.http-shutdown", func() {
		defer close(httpShutdownDone)
		<-ctx.Done()
		stop, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		// The Hosted notice has its own 2 s budget and runs beside the drain (A51).
		notified := make(chan struct{})
		supervise.Go("hosted.shutdown-notice", func() { defer close(notified); _ = control.NotifyShutdown(stop) })
		_ = server.Shutdown(stop)
		// The owner's in-flight Google sign-ins own loopback listeners and flights.
		apiDependencies.DriveOAuth.Close()
		flush, finishFlush := context.WithTimeout(context.Background(), 10*time.Second)
		if err := httpapi.FlushCredentialUse(flush, handler); err != nil {
			log.Printf("Credential usage could not be saved before shutdown: %v", err)
		}
		finishFlush()
		backupStop, finishBackup := context.WithTimeout(context.Background(), drainBudget)
		if err := backupService.Shutdown(backupStop); err != nil {
			log.Printf("Backup shutdown incomplete: %v", err)
		}
		finishBackup()
		observationStop, finishObservations := context.WithTimeout(context.Background(), 10*time.Second)
		if err := httpapi.ClosePlaybackObservations(observationStop, handler); err != nil {
			log.Printf("Playback observations could not be saved before shutdown: %v", err)
		}
		finishObservations()
		<-notified
	})
	certificates := claimHandler.Certificates()

	// A custom certificate serves even with no Hosted manager (unclaimed /
	// Direct Sign-In): the listener exists whenever either source can serve.
	{
		direct.UseCertificates(certificates, customCertificate)
		_, port, _ := net.SplitHostPort(listener.Addr().String())
		numericPort, _ := strconv.Atoi(port)
		if certificates != nil {
			certificates.SetListening(numericPort, true)
			defer certificates.SetListening(numericPort, false)
			certificateCtx, cancelCertificates := context.WithCancel(ctx)
			certificateDone := make(chan struct{})
			supervise.Go("cmd.certificates", func() {
				defer close(certificateDone)
				supervise.Loop(certificateCtx, "cmd.certificates", certificates.Run)
			})
			// Joined through the barrier, before the claim keys and the database
			// close, and bounded like everything else.
			drain.add("certificates", cancelCertificates, certificateDone)
		}
	}
	if manager := claimHandler.Remote(); manager != nil {
		networkCtx, stopNetwork := context.WithCancel(ctx)
		networkDone := make(chan struct{})
		supervise.Go("cmd.remote-network", func() {
			defer close(networkDone)
			supervise.Loop(networkCtx, "cmd.remote-network", manager.Run)
		})
		drain.add("remote-network", stopNetwork, networkDone)
	}
	if env("PORTICO_DISCOVERY", "1") != "0" {
		homeNetworks := func(ctx context.Context) []netip.Prefix {
			document, err := console.RuntimeSettings(ctx, operations.AllowServerScope)
			if err != nil {
				return nil
			}
			var networks []netip.Prefix
			for _, raw := range document.Effective.LANNetworks {
				if p, ok := operations.ParseNetwork(raw); ok {
					networks = append(networks, p)
				}
			}
			return networks
		}
		administration.Advertiser.Bind(ctx, func(life context.Context) {
			discovery := networking.NewDiscovery(claimKeys, claimRunner, ident.Name)
			discovery.SetHomeNetworks(homeNetworks)
			discovery.Run(life, listener.Addr().String())
		})
		discoveryDone := make(chan struct{})
		drain.add("discovery", func() {
			supervise.Go("cmd.discovery.stop", func() { administration.Advertiser.Close(); close(discoveryDone) })
		}, discoveryDone)
		// SSDP for the DLNA server; the owner's switch starts and stops it.
		administration.DLNA.Bind(ctx, func(life context.Context) {
			dlna.Advertise(life, dlna.Advertisement{Bind: listener.Addr().String(), UUID: dlna.DeviceUUID(ident.ID()),
				Server: runtime.GOOS + "/1.0 UPnP/1.0 Portico/" + buildinfo.Info()["version"], Home: homeNetworks})
		})
		dlnaDone := make(chan struct{})
		drain.add("dlna", func() {
			supervise.Go("cmd.dlna.stop", func() { administration.DLNA.Close(); close(dlnaDone) })
		}, dlnaDone)
	}

	log.Printf("Ordinary connection ceiling set to %d, plus 32 reserved for the console (HTTP/2 streams per connection: %d)", connectionLimit, server.HTTP2.MaxConcurrentStreams)
	log.Printf("Portico server listening on %s (HTTP and HTTPS share this port)", listener.Addr())
	// ONB-02 item 1: while setup is unfinished, every start names the address
	// to open. The port comes from the bound listener, not the configured
	// string, so a :0 or overridden bind still prints a reachable URL.
	if ident.SetupRequired() {
		if _, portStr, err := net.SplitHostPort(listener.Addr().String()); err == nil {
			if port, err := strconv.Atoi(portStr); err == nil {
				logSetupBlock(port, ip.IsLoopback())
			}
		}
	}
	<-served
	if e = serveErr; errors.Is(e, http.ErrServerClosed) {
		<-httpShutdownDone
		if restartRequested.Load() {
			return errRestartRequested
		}
		return nil
	}
	return e
}
