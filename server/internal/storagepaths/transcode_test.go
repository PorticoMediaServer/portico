package storagepaths

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMemoryTranscodeDirectoryIsPrivateAndStable(t *testing.T) {
	disk := t.TempDir()
	mounts := []Mount{{"/", "ext4"}, {"/dev/shm", "tmpfs"}}
	got := MemoryTranscodeDirectory(disk, mounts)
	if runtime.GOOS != "linux" {
		if got != "" {
			t.Fatal("non-Linux platform offered memory", got)
		}
		return
	}
	if filepath.Dir(got) != "/dev/shm" || filepath.Base(got) == "portico-hls" {
		t.Fatal("not an installation-specific child", got)
	}
	if other := MemoryTranscodeDirectory(t.TempDir(), mounts); other == got {
		t.Fatal("different disk roots share RAM output")
	}
	alias := filepath.Join(t.TempDir(), "disk")
	if err := os.Symlink(disk, alias); err != nil {
		t.Fatal(err)
	}
	if MemoryTranscodeDirectory(alias, mounts) != got {
		t.Fatal("canonical disk identity is unstable")
	}
	mounts = append(mounts, Mount{"/memory-transcode", "tmpfs"})
	if preferred := MemoryTranscodeDirectory(disk, mounts); filepath.Dir(preferred) != "/memory-transcode" {
		t.Fatal("dedicated bounded mount not preferred", preferred)
	}
	if MemoryTranscodeDirectory(disk, []Mount{{"/", "ext4"}}) != "" {
		t.Fatal("disk offered as memory")
	}
}

func TestTranscodeDirectoriesPlacementAndVerification(t *testing.T) {
	mounts := []Mount{{"/", "ext4"}, {"/ram", "tmpfs"}, {"/unbounded", "ramfs"}}
	for _, tc := range []struct {
		requested, fallback, disk, memory string
		fail                              bool
	}{
		{"/disk", "/fallback", "/disk", "", false},
		{"/ram/output", "/disk", "/disk", "/ram/output", false},
		{"/unbounded/output", "/disk", "/disk", "", false},
		{"/ram/output", "/ram/spill", "", "", true},
		{"/ram/output", "", "", "", true},
	} {
		disk, memory, err := TranscodeDirectories(tc.requested, tc.fallback, mounts)
		wantMemory := tc.memory
		if runtime.GOOS != "linux" {
			wantMemory = ""
		}
		if (err != nil) != tc.fail || disk != tc.disk || memory != wantMemory {
			t.Fatalf("%+v: %q %q %v", tc, disk, memory, err)
		}
	}
	if _, _, err := TranscodeDirectories("/unknown", "/disk", nil); err == nil {
		t.Fatal("unknown filesystem accepted")
	}
}

func TestTranscodeDirectoriesNeverTreatLinkedRAMAsDisk(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux tmpfs placement")
	}
	base := t.TempDir()
	alias := filepath.Join(base, "looks-like-disk")
	if err := os.Symlink("/dev/shm", alias); err != nil {
		t.Fatal(err)
	}
	fallback := filepath.Join(base, "disk")
	mounts, err := CurrentMounts()
	if err != nil {
		t.Fatal(err)
	}
	got, memory, err := TranscodeDirectories(filepath.Join(alias, "output"), fallback, mounts)
	if err != nil || got != fallback || memory != "" {
		t.Fatal("linked RAM was treated as disk", got, memory, err)
	}
	if _, _, err := TranscodeDirectories(filepath.Join(alias, "output"), filepath.Join(alias, "fallback"), mounts); err == nil {
		t.Fatal("a linked RAM fallback was accepted")
	}
}

func TestContainerConversionFolderStaysWithinDiskRoots(t *testing.T) {
	layout := Layout{Container: true, Data: "/config", Transcode: "/transcode"}
	for _, path := range []string{"", "/transcode", "/transcode/jobs", "/config/Transcode", "/config/Transcode/jobs"} {
		if !layout.AllowsTranscodeDirectory(path) {
			t.Fatal("refused a supported container conversion folder", path)
		}
	}
	for _, path := range []string{"relative", "/transcode-other", "/config/Keys", "/transcode/../config", "/media"} {
		if layout.AllowsTranscodeDirectory(path) {
			t.Fatal("accepted output outside the container conversion roots", path)
		}
	}
	if !(Layout{}).AllowsTranscodeDirectory("/owner/disk") {
		t.Fatal("native-host disk folder was not configurable")
	}
}
