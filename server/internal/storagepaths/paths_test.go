package storagepaths

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlatformDefaults(t *testing.T) {
	cases := []struct {
		o               Options
		data, transcode string
	}{
		{Options{Platform: "linux", Home: "/home/owner"}, "/home/owner/.local/share/portico", "/home/owner/.local/share/portico/Transcode"},
		{Options{Platform: "linux", Service: true}, "/var/lib/portico", "/var/lib/portico/Transcode"},
		{Options{Platform: "darwin", Home: "/Users/owner"}, "/Users/owner/Library/Application Support/Portico", "/Users/owner/Library/Application Support/Portico/Transcode"},
		{Options{Platform: "windows", LocalAppData: `C:\Users\owner\AppData\Local`}, `C:\Users\owner\AppData\Local\Portico`, `C:\Users\owner\AppData\Local\Portico\Transcode`},
		{Options{Platform: "windows", Service: true}, `C:\ProgramData\Portico`, `C:\ProgramData\Portico\Transcode`},
		{Options{Container: true, Mounts: []Mount{{"/transcode", "ext4"}}}, "/config", "/transcode"},
		{Options{Container: true}, "/config", "/config/Transcode"},
	}
	for _, c := range cases {
		got, err := Resolve(c.o)
		if err != nil || got.Data != c.data || got.Transcode != c.transcode {
			t.Errorf("%+v => %+v %v", c.o, got, err)
		}
	}
	for _, bad := range []string{".", "/", "relative/folder"} {
		if _, err := Resolve(Options{Platform: "linux", Home: "/home/u", DataDirectory: bad}); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
func testLayout(t *testing.T) Layout {
	t.Helper()
	l := At(filepath.Join(t.TempDir(), "data"))
	l.Transcode = filepath.Join(filepath.Dir(l.Data), "transcode")
	if err := l.Prepare(); err != nil {
		t.Fatal(err)
	}
	return l
}
func TestTempResetNeverFollowsLinksOrDeletesParents(t *testing.T) {
	l := testLayout(t)
	outside := filepath.Join(filepath.Dir(l.Data), "keep")
	if err := os.WriteFile(outside, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(l.Temp, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(l.Temp, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(l.Temp, "nested", "old"), []byte("work"), 0600)
	if err := l.ResetTemp(); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(outside); err != nil || string(raw) != "original" {
		t.Fatal("escaped Temp", err)
	}
	entries, _ := os.ReadDir(l.Temp)
	if len(entries) != 1 || entries[0].Name() != ownershipMarker {
		t.Fatal(entries)
	}
	if err := os.Remove(filepath.Join(l.Temp, ownershipMarker)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(l.Temp); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(l.Data), l.Temp); err != nil {
		t.Fatal(err)
	}
	if err := l.ResetTemp(); err == nil {
		t.Fatal("accepted linked Temp")
	}
	if err := l.EmptyOwned(".."); err == nil {
		t.Fatal("accepted parent")
	}
	if err := (At("/")).ResetTemp(); err == nil {
		t.Fatal("accepted root")
	}
}
func TestOldLayoutReadOnlyRefusal(t *testing.T) {
	parent := t.TempDir()
	l := At(filepath.Join(parent, "old"))
	os.Mkdir(l.Data, 0700)
	old := filepath.Join(l.Data, "server.sqlite")
	os.WriteFile(old, []byte("untouched"), 0600)
	if err := l.Prepare(); err == nil {
		t.Fatal("old layout accepted")
	}
	raw, _ := os.ReadFile(old)
	entries, _ := os.ReadDir(l.Data)
	if string(raw) != "untouched" || len(entries) != 1 {
		t.Fatal("old data modified")
	}
	linked := At(filepath.Join(parent, "linked"))
	os.Symlink(l.Data, linked.Data)
	if err := linked.Prepare(); err == nil {
		t.Fatal("linked data accepted")
	}
}
func TestMountInfoAndUsageNotice(t *testing.T) {
	mounts := ParseMountInfo(strings.NewReader("1 0 8:1 / / rw - ext4 /dev/sda rw\n2 1 0:3 / /config rw - fuse.shfs shfs rw\n3 1 0:4 / /transcode rw shared:2 - tmpfs tmpfs rw\n4 1 0:5 / /a\\040b rw - nfs host:/x rw\n"))
	if !IsMount("/transcode", mounts) || IsMount("/transcode/sub", mounts) || Filesystem("/a b/c", mounts) != "nfs" {
		t.Fatal(mounts)
	}
	for _, kind := range []string{"fuse.shfs", "nfs", "nfs4", "cifs", "smb3"} {
		if !SharedFilesystem(kind) {
			t.Fatal(kind)
		}
	}
	l := testLayout(t)
	l.TranscodeFallback = true
	os.WriteFile(filepath.Join(l.Cache, "derived"), []byte("123"), 0600)
	outside := filepath.Join(filepath.Dir(l.Data), "secret")
	os.WriteFile(outside, []byte(strings.Repeat("s", 90)), 0600)
	os.Symlink(outside, filepath.Join(l.Cache, "link"))
	out, err := l.Usage(context.Background(), []Mount{{l.Data, "cifs"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Notices) != 2 || len(out.Folders) != 8 {
		t.Fatal(out)
	}
	for _, f := range out.Folders {
		if f.Name == "Cache" && f.Bytes != 3 {
			t.Fatal(f)
		}
	}
	if err = l.RequestCacheEmpty(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(l.Cache, "derived")); err != nil {
		t.Fatal("deleted active cache")
	}
	if err = l.ApplyCacheEmpty(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(l.Cache, "derived")); !os.IsNotExist(err) {
		t.Fatal("cache remains", err)
	}
}
func TestRecordingsOutsideDataOnly(t *testing.T) {
	l := testLayout(t)
	for _, p := range []string{"", l.Data, filepath.Join(l.Data, "recordings"), "relative"} {
		if l.ValidateRecordings(p) == nil {
			t.Fatalf("accepted %s", p)
		}
	}
	if err := l.ValidateRecordings(filepath.Join(filepath.Dir(l.Data), "Recordings")); err != nil {
		t.Fatal(err)
	}
}

func TestEveryLegacyCategoryIsRefusedWithoutMutation(t *testing.T) {
	for _, name := range []string{"artwork", "local-artwork", "subtitles", "channel-logos", "guide-images", "analysis-artifacts", "title-dataset", "remote-sources", "managed-storage", "mounts", "run", "live-source-locks", "logs", "backups", "restore-staged", "networking-keys", "keys", "supervisor", "hls", "recordings"} {
		t.Run(name, func(t *testing.T) {
			data := filepath.Join(t.TempDir(), "data")
			old := filepath.Join(data, name)
			if err := os.MkdirAll(old, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(old, "keep"), []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := At(data).Prepare(); err == nil {
				t.Fatal("old layout accepted")
			}
			if raw, err := os.ReadFile(filepath.Join(old, "keep")); err != nil || string(raw) != "original" {
				t.Fatal("old data changed", err)
			}
			entries, _ := os.ReadDir(data)
			if len(entries) != 1 {
				t.Fatal("created new layout over old", entries)
			}
		})
	}
}

func TestNativeVolumeRootIsNeverADataOrSpillFolder(t *testing.T) {
	directory := t.TempDir()
	root := filepath.VolumeName(directory) + string(os.PathSeparator)
	if !filesystemRoot(root) || filesystemRoot(directory) {
		t.Fatal(root, directory)
	}
	if err := At(root).ResetTemp(); err == nil {
		t.Fatal("volume root accepted for Temp cleanup")
	}
	if err := At(root).Prepare(); err == nil {
		t.Fatal("volume root accepted as data")
	}
	if _, _, err := TranscodeDirectories(root, root, nil); err == nil {
		t.Fatal("volume root accepted for spill")
	}
}

// A pre-layout invitation key must never be silently replaced by a fresh key,
// which would invalidate pending invitations without moving the old data.
func TestLegacyInvitationKeyIsRefusedWithoutMutation(t *testing.T) {
	data := t.TempDir()
	old := filepath.Join(data, "invitation-link.key")
	before := []byte(strings.Repeat("k", 32))
	if err := os.WriteFile(old, before, 0600); err != nil {
		t.Fatal(err)
	}
	if err := At(data).Prepare(); err == nil {
		t.Fatal("legacy invitation key accepted")
	}
	after, err := os.ReadFile(old)
	if err != nil || string(after) != string(before) {
		t.Fatal("legacy invitation key changed", err)
	}
	entries, err := os.ReadDir(data)
	if err != nil || len(entries) != 1 {
		t.Fatal("new layout created over legacy key", entries, err)
	}
	if got := Path(data, "invitation-link.key"); got != filepath.Join(data, "Keys", "invitation-link.key") {
		t.Fatal("invitation key mapping", got)
	}
}
