package mediaexec

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// With LIBVA_DRIVERS_PATH unset a media process is pointed at the first of the usual driver
// folders that exists; a folder the owner set is passed on as it is and never replaced.
func TestVADriversFolderDefaultsToTheFirstThatExists(t *testing.T) {
	missing, present, later := filepath.Join(t.TempDir(), "none"), t.TempDir(), t.TempDir()
	saved := vaDriverFolders[runtime.GOARCH]
	vaDriverFolders[runtime.GOARCH] = []string{missing, present, later}
	defer func() { vaDriverFolders[runtime.GOARCH] = saved }()

	value := func() string {
		for _, kv := range hardwareEnvironment() {
			if kv[0] == "LIBVA_DRIVERS_PATH" {
				return kv[1]
			}
		}
		return ""
	}
	os.Unsetenv("LIBVA_DRIVERS_PATH")
	if got := value(); got != present {
		t.Fatalf("the default driver folder: %q, want %q", got, present)
	}
	if !slices.Contains(environment(), "LIBVA_DRIVERS_PATH="+present) {
		t.Fatal("a job without a sandbox is not pointed at the same folder")
	}
	t.Setenv("LIBVA_DRIVERS_PATH", "/opt/owner/dri")
	if got := value(); got != "/opt/owner/dri" {
		t.Fatalf("the owner's folder was replaced: %q", got)
	}
	vaDriverFolders[runtime.GOARCH] = []string{missing}
	os.Unsetenv("LIBVA_DRIVERS_PATH")
	if got := value(); got != "" {
		t.Fatalf("no usual folder exists, yet the job is pointed at %q", got)
	}
}

// The folders the owner names are readable inside a hardware job's sandbox, read-only; a
// relative entry and the whole file system are not.
func TestAnOwnerNamedDriverFolderIsReadableInTheHardwareSandbox(t *testing.T) {
	args := LinuxHardwareSandboxArgs([][2]string{{"LIBVA_DRIVERS_PATH", "/opt/intel/dri:drivers:/:/srv/va/../va"}, {"LIBVA_DRIVER_NAME", "iHD"}})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--ro-bind-try /opt/intel/dri /opt/intel/dri") {
		t.Fatal("the owner's driver folder is not mounted", args)
	}
	for _, refused := range []string{"--ro-bind-try drivers ", "--ro-bind-try / /", "--ro-bind-try /srv/va", "--bind /opt", "--dev-bind-try /opt"} {
		if strings.Contains(joined+" ", refused) {
			t.Fatalf("mounted %q: %v", refused, args)
		}
	}
}

// A driver the owner unpacked by hand needs its own libraries as well: for a hardware job the
// folders of the server's own LD_LIBRARY_PATH are mounted read-only and come last in the
// job's library path. A software job gets neither.
func TestAHardwareJobFindsTheLibrariesOfAnOwnerUnpackedDriver(t *testing.T) {
	tool, err := resolveExecutable("true")
	if err != nil {
		t.Skip("no true(1) on this host")
	}
	t.Setenv("LD_LIBRARY_PATH", "/opt/intel/lib:relative")
	for _, hardware := range []bool{true, false} {
		argv, err := linuxArgv("/usr/bin/bwrap", Job{Executable: tool, Hardware: hardware}, false)
		if err != nil {
			t.Fatal(err)
		}
		joined, path := strings.Join(argv, " "), ""
		for i := 0; i+2 < len(argv); i++ {
			if argv[i] == "--setenv" && argv[i+1] == "LD_LIBRARY_PATH" {
				if path != "" {
					t.Fatal("the library path is set twice", argv)
				}
				path = argv[i+2]
			}
		}
		mounted := strings.Contains(joined, "--ro-bind-try /opt/intel/lib /opt/intel/lib")
		if hardware && (!mounted || !strings.HasSuffix(path, ":/opt/intel/lib")) {
			t.Fatalf("a hardware job: mounted %v, library path %q", mounted, path)
		}
		if !hardware && strings.Contains(joined, "/opt/intel/lib") {
			t.Fatalf("a software job was given the owner's library folder: %v", argv)
		}
		if strings.Contains(path, "relative") {
			t.Fatalf("a relative folder reached the library path: %q", path)
		}
	}
}

func withDataFolder(t *testing.T, folder string) {
	t.Helper()
	configured.Lock()
	saved := configured.dataFolder
	configured.dataFolder = resolvedFolder(folder)
	configured.Unlock()
	t.Cleanup(func() {
		configured.Lock()
		configured.dataFolder = saved
		configured.Unlock()
	})
}

// A folder the owner names that is or contains the server's data folder is not shown to
// hardware jobs, and the owner is told why; a folder inside the data folder, and any other,
// still is.
func TestAFolderHoldingTheDataFolderIsNotShownToHardwareJobs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())
	data, drivers := filepath.Join(home, "portico"), filepath.Join(home, "drivers")
	inside := filepath.Join(data, "drivers")
	for _, dir := range []string{inside, drivers} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(home, "link-to-home")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	withDataFolder(t, data)
	shown, skipped := sortOwnerFolders(strings.Join([]string{home, data, link, drivers, inside}, ":"))
	if !slices.Equal(shown, []string{drivers, inside}) {
		t.Fatalf("shown to hardware jobs: %v", shown)
	}
	if len(skipped) != 3 || skipped[0][0] != home || skipped[1][0] != data || skipped[2][0] != link || !strings.Contains(skipped[0][1], "data folder") {
		t.Fatalf("skipped: %v", skipped)
	}
	args := strings.Join(LinuxHardwareSandboxArgs([][2]string{{"LD_LIBRARY_PATH", home + ":" + drivers}}), " ") + " "
	if strings.Contains(args, "--ro-bind-try "+home+" ") || !strings.Contains(args, "--ro-bind-try "+drivers+" ") {
		t.Fatalf("the sandbox: %v", args)
	}
	if got := HardwareLibraryFolders([][2]string{{"LD_LIBRARY_PATH", home + ":" + drivers}}); !slices.Equal(got, []string{drivers}) {
		t.Fatalf("the job's library path: %v", got)
	}
}

// A folder that is or contains the temporary folder is not shown either: it would be mounted
// over the job's own private one.
func TestAFolderHoldingTheTemporaryFolderIsNotShownToHardwareJobs(t *testing.T) {
	root := t.TempDir()
	temporary, drivers := filepath.Join(root, "tmp"), filepath.Join(root, "drivers")
	for _, dir := range []string{temporary, drivers} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TMPDIR", temporary)
	withDataFolder(t, "")
	shown, skipped := sortOwnerFolders(strings.Join([]string{temporary, root, drivers}, ":"))
	if !slices.Equal(shown, []string{drivers}) {
		t.Fatalf("shown to hardware jobs: %v", shown)
	}
	if len(skipped) != 2 || skipped[0][0] != temporary || skipped[1][0] != root || !strings.Contains(skipped[1][1], "temporary folder") {
		t.Fatalf("skipped: %v", skipped)
	}
}

// A hardware job whose input comes over the decoder's private bridge has its sandbox built by
// a helper process (decoder.RunHelper), from the hardware environment the server hands it
// (HardwareEnvironment). That helper is a fresh process: Configure was never called in it, so
// it does not know the data folder. The folder the guard skips in the server must therefore
// already be gone from what the server hands over. (Review desk A's test for #2813.)
func TestReviewATheBridgeHelperIsNotHandedAFolderHoldingTheDataFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())
	data, drivers := filepath.Join(home, "portico"), filepath.Join(home, "drivers")
	for _, dir := range []string{data, drivers} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("LD_LIBRARY_PATH", home+":"+drivers)
	withDataFolder(t, data)
	handed := HardwareEnvironment() // in the server, which knows the data folder

	// In the helper: no data folder configured.
	withDataFolder(t, "")
	args := strings.Join(LinuxHardwareSandboxArgs(handed), " ") + " "
	if strings.Contains(args, "--ro-bind-try "+home+" ") {
		t.Fatalf("the bridge helper mounts %s, which holds the data folder %s, for a hardware job:\n%s", home, data, args)
	}
	if !strings.Contains(args, "--ro-bind-try "+drivers+" ") {
		t.Fatalf("the driver folder is no longer shown: %s", args)
	}
}
