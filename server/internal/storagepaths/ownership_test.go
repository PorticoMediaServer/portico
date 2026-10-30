package storagepaths

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestExistingUnownedDisposableScopesAreNeverAdopted(t *testing.T) {
	for _, scope := range []string{"Temp", "Cache"} {
		t.Run(scope, func(t *testing.T) {
			l := At(filepath.Join(t.TempDir(), "unrelated"))
			dir := filepath.Join(l.Data, scope)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			keep := filepath.Join(dir, "keep")
			if err := os.WriteFile(keep, []byte("unrelated contents"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := l.Prepare(); err == nil {
				t.Fatal("adopted an unowned destructive scope")
			}
			if err := l.EmptyOwned(scope); err == nil {
				t.Fatal("emptied an unowned scope")
			}
			if raw, err := os.ReadFile(keep); err != nil || string(raw) != "unrelated contents" {
				t.Fatal("unrelated contents changed", err)
			}
			entries, err := os.ReadDir(l.Data)
			if err != nil || len(entries) != 1 {
				t.Fatal("refusal modified data folder", entries, err)
			}
			if _, err := os.Stat(filepath.Join(dir, ownershipMarker)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("minted ownership over preexisting data", err)
			}
		})
	}
}

func TestOwnedScopeSurvivesReopenButMissingOrCopiedOwnershipRefusesDeletion(t *testing.T) {
	l := testLayout(t)
	if err := l.Prepare(); err != nil {
		t.Fatal("did not retain ownership", err)
	}
	if err := os.Remove(filepath.Join(l.Temp, ownershipMarker)); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(l.Temp, "keep")
	if err := os.WriteFile(keep, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := l.ResetTemp(); err == nil {
		t.Fatal("missing ownership accepted")
	}
	if err := l.Prepare(); err == nil {
		t.Fatal("ownership reminted over existing contents")
	}
	if raw, err := os.ReadFile(keep); err != nil || string(raw) != "original" {
		t.Fatal(err)
	}
	other := At(filepath.Join(t.TempDir(), "other"))
	if err := os.MkdirAll(other.Temp, 0700); err != nil {
		t.Fatal(err)
	}
	marker, err := os.ReadFile(filepath.Join(l.Cache, ownershipMarker))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other.Temp, ownershipMarker), marker, 0600); err != nil {
		t.Fatal(err)
	}
	if err := other.ResetTemp(); err == nil {
		t.Fatal("copied ownership accepted")
	}
}

func TestCleanupRefusesUnknownMountsAndEveryDescendantVolume(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Temp")
	for _, mounts := range [][]Mount{nil, {{dir, "apfs"}}, {{filepath.Join(dir, "volume"), "apfs"}}, {{filepath.Join(dir, "same-device-bind"), "ext4"}}} {
		if err := refuseMountsWithin(dir, mounts); !errors.Is(err, ErrCleanupUnavailable) {
			t.Fatal("cleanup admitted a mount or unknown table", mounts, err)
		}
	}
	if err := refuseMountsWithin(dir, []Mount{{filepath.VolumeName(dir) + string(os.PathSeparator), "ext4"}, {dir + "-sibling", "apfs"}}); err != nil {
		t.Fatal(err)
	}
}

func TestRAMMappedTranscodeHasVerifiedDiskSpill(t *testing.T) {
	mounts := []Mount{{"/", "ext4"}, {"/transcode", "tmpfs"}, {"/dev/shm", "tmpfs"}}
	l, err := Resolve(Options{Container: true, Mounts: mounts})
	if err != nil || l.Transcode != "/transcode" || l.TranscodeFallback {
		t.Fatal(l, err)
	}
	got, memory, err := TranscodeDirectories("/transcode", "/config/Transcode", mounts)
	wantMemory := ""
	if runtime.GOOS == "linux" {
		wantMemory = "/transcode"
	}
	if err != nil || got != "/config/Transcode" || memory != wantMemory {
		t.Fatal(got, err)
	}
	if _, _, err := TranscodeDirectories("/transcode", "/dev/shm/spill", mounts); err == nil {
		t.Fatal("RAM spill accepted for live FFmpeg output")
	}
	for _, unknown := range [][]Mount{nil, {{"/unrelated", "ext4"}}} {
		if _, _, err := TranscodeDirectories("/transcode", "/config/Transcode", unknown); err == nil {
			t.Fatal("unknown filesystem accepted as disk", unknown)
		}
	}
}

func TestWholeDataFolderMoveAndCopyRetainScopeOwnership(t *testing.T) {
	l := testLayout(t)
	for _, dir := range []string{l.Temp, l.Cache, l.Metadata, l.Keys} {
		if err := os.WriteFile(filepath.Join(dir, "original"), []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	moved := At(filepath.Join(filepath.Dir(l.Data), "renamed-data"))
	if err := os.Rename(l.Data, moved.Data); err != nil {
		t.Fatal(err)
	}
	if err := moved.Prepare(); err != nil {
		t.Fatal("whole-folder rename refused", err)
	}
	copied := At(filepath.Join(t.TempDir(), "copied-data"))
	if err := os.CopyFS(copied.Data, os.DirFS(moved.Data)); err != nil {
		t.Fatal(err)
	}
	if err := copied.Prepare(); err != nil {
		t.Fatal("whole-folder copy refused", err)
	}
	if err := copied.ResetTemp(); err != nil {
		t.Fatal(err)
	}
	if err := copied.RequestCacheEmpty(); err != nil {
		t.Fatal(err)
	}
	if err := copied.ApplyCacheEmpty(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{copied.Metadata, copied.Keys, moved.Temp, moved.Cache} {
		if raw, err := os.ReadFile(filepath.Join(dir, "original")); err != nil || string(raw) != "keep" {
			t.Fatal("move/copy cleanup changed retained bytes or original", dir, err)
		}
	}
	other := testLayout(t)
	marker, err := os.ReadFile(filepath.Join(moved.Cache, ownershipMarker))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other.Cache, ownershipMarker), marker, 0600); err != nil {
		t.Fatal(err)
	}
	if err := other.EmptyOwned("Cache"); err == nil {
		t.Fatal("scope copied alone became owned by another layout")
	}
}
