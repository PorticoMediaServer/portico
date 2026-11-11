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
