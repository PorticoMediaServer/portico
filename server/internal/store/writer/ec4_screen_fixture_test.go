package writer

// This file builds the flat EC4 SCREENING metadata fixture only. It is not a
// production path, a media source, or a throughput claim: every member holds a
// dense 4 KiB seeded header and a sparse tail. Logical sizes look like movies;
// actual writes are headers. Nothing here imports writer internals, profiles,
// grants, cursors, or SQL.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/diskspace"
)

const (
	ec4ScreenSeed         uint64 = 0x4543345343524e31
	ec4ScreenMinFiles            = 1
	ec4ScreenMaxFiles            = 400000
	ec4ScreenHeaderBytes         = 4096
	ec4ScreenFreeHeadroom int64  = 4 << 30
)

// ec4ScreenFixture describes one generated flat directory. WrittenBytes counts
// only bytes actually written (headers); LogicalBytes sums the sparse sizes.
type ec4ScreenFixture struct {
	Root           string
	Files          int
	Seed           uint64
	LogicalBytes   int64
	WrittenBytes   int64
	ManifestSHA256 string
}

func ec4ScreenMix(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

// ec4ScreenMember returns a deterministic flat name and logical size. The
// zero-padded ordinal keeps names unique even when titles repeat.
func ec4ScreenMember(i int, seed uint64) (string, int64) {
	titles := [...]string{
		"The Arrival",
		"Amélie",
		"千と千尋の神隠し",
		"Léon",
		"A Quiet Place",
		"Blade Runner",
		"Spirited Away",
		"Cinema Paradiso",
		"Wings of Desire",
		"City of God",
		"Pan's Labyrinth",
		"The Umbrellas of Cherbourg",
		"Parasite",
		"Crouching Tiger, Hidden Dragon",
		"The Godfather",
		"An Autumn Tale",
	}
	exts := [...]string{".mkv", ".mp4", ".avi", ".m4v"}
	h := ec4ScreenMix(seed ^ uint64(i))
	title := titles[h%uint64(len(titles))]
	year := 1960 + int(h%66)
	name := fmt.Sprintf("%s (%d) [EC4-%09d]%s", title, year, i, exts[(h>>8)%4])
	size := int64(500<<20) + int64(h%(3500<<20))
	if h%5 == 0 {
		size = int64(4<<30) + int64(h%(16<<30))
	}
	return name, size
}

// ec4ScreenHeader fills dst with the seeded dense header for one member.
func ec4ScreenHeader(seed uint64, index int, dst []byte) {
	for j := range dst {
		dst[j] = byte(ec4ScreenMix(seed ^ uint64(index) ^ uint64(j)))
	}
}

// ec4MakeScreenFixture creates a new exclusive root and writes count flat
// members through one retained os.Root, keeping at most one member file open.
// It refuses to start without four GiB of headroom beyond the headers and
// honors t.Context cancellation. The returned manifest hashes names and sizes.
func ec4MakeScreenFixture(t *testing.T, root string, count int, seed uint64) (ec4ScreenFixture, error) {
	t.Helper()
	ctx := t.Context()
	fixture := ec4ScreenFixture{Root: root, Seed: seed}
	if count < ec4ScreenMinFiles || count > ec4ScreenMaxFiles {
		return fixture, fmt.Errorf("ec4 screen fixture: count %d outside %d..%d", count, ec4ScreenMinFiles, ec4ScreenMaxFiles)
	}
	if err := ctx.Err(); err != nil {
		return fixture, err
	}
	free, err := diskspace.Free(filepath.Dir(root))
	if err != nil {
		return fixture, fmt.Errorf("ec4 screen fixture: free space: %w", err)
	}
	need := int64(count)*ec4ScreenHeaderBytes + ec4ScreenFreeHeadroom
	if free < need {
		return fixture, fmt.Errorf("ec4 screen fixture: need %d bytes free, have %d", need, free)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		return fixture, fmt.Errorf("ec4 screen fixture: create root: %w", err)
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return fixture, fmt.Errorf("ec4 screen fixture: open root: %w", err)
	}
	defer dir.Close()

	hash := sha256.New()
	fmt.Fprintf(hash, "ec4-screen-flat-v1 %d %d\n", count, seed)
	header := make([]byte, ec4ScreenHeaderBytes)
	for i := 0; i < count; i++ {
		if err := ctx.Err(); err != nil {
			return fixture, err
		}
		name, size := ec4ScreenMember(i, seed)
		ec4ScreenHeader(seed, i, header)
		file, err := dir.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return fixture, fmt.Errorf("ec4 screen fixture: create %q: %w", name, err)
		}
		n, writeErr := file.Write(header)
		truncateErr := file.Truncate(size)
		closeErr := file.Close()
		if writeErr == nil && n != len(header) {
			writeErr = io.ErrShortWrite
		}
		if writeErr != nil {
			return fixture, fmt.Errorf("ec4 screen fixture: write %q: %w", name, writeErr)
		}
		if truncateErr != nil {
			return fixture, fmt.Errorf("ec4 screen fixture: truncate %q: %w", name, truncateErr)
		}
		if closeErr != nil {
			return fixture, fmt.Errorf("ec4 screen fixture: close %q: %w", name, closeErr)
		}
		fixture.Files++
		fixture.LogicalBytes += size
		fixture.WrittenBytes += int64(n)
		fmt.Fprintf(hash, "%s\t%d\n", name, size)
	}
	fixture.ManifestSHA256 = hex.EncodeToString(hash.Sum(nil))
	return fixture, nil
}

// TestEC4ScreenFixtureControl is the single tiny deterministic control. It is
// deliberately cached and small; no local scale counts are run here.
func TestEC4ScreenFixtureControl(t *testing.T) {
	const count = 16
	first, err := ec4MakeScreenFixture(t, filepath.Join(t.TempDir(), "library-a"), count, ec4ScreenSeed)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ec4MakeScreenFixture(t, filepath.Join(t.TempDir(), "library-b"), count, ec4ScreenSeed)
	if err != nil {
		t.Fatal(err)
	}
	if first.Files != count || second.Files != count {
		t.Fatalf("files = %d and %d, want %d", first.Files, second.Files, count)
	}
	if first.WrittenBytes != int64(count)*ec4ScreenHeaderBytes || second.WrittenBytes != first.WrittenBytes {
		t.Fatalf("written bytes = %d and %d", first.WrittenBytes, second.WrittenBytes)
	}
	if first.LogicalBytes != second.LogicalBytes {
		t.Fatalf("logical bytes = %d and %d", first.LogicalBytes, second.LogicalBytes)
	}
	if first.ManifestSHA256 != second.ManifestSHA256 {
		t.Fatalf("manifest changed: %s != %s", first.ManifestSHA256, second.ManifestSHA256)
	}
	if first.Seed != ec4ScreenSeed {
		t.Fatalf("seed = %#x", first.Seed)
	}
	entries, err := os.ReadDir(first.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != count {
		t.Fatalf("root entries = %d, want %d", len(entries), count)
	}

	name, size := ec4ScreenMember(0, ec4ScreenSeed)
	info, err := os.Stat(filepath.Join(first.Root, name))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != size || size <= ec4ScreenHeaderBytes {
		t.Fatalf("member size = %d, want %d (sparse tail)", info.Size(), size)
	}
	file, err := os.Open(filepath.Join(first.Root, name))
	if err != nil {
		t.Fatal(err)
	}
	got := make([]byte, ec4ScreenHeaderBytes)
	_, readErr := io.ReadFull(file, got)
	closeErr := file.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	want := make([]byte, ec4ScreenHeaderBytes)
	ec4ScreenHeader(ec4ScreenSeed, 0, want)
	if !bytes.Equal(got, want) {
		t.Fatal("dense header is not seeded")
	}

	lastName, lastSize := ec4ScreenMember(count-1, ec4ScreenSeed)
	lastInfo, err := os.Stat(filepath.Join(second.Root, lastName))
	if err != nil {
		t.Fatal(err)
	}
	if lastInfo.Size() != lastSize {
		t.Fatalf("last member size = %d, want %d", lastInfo.Size(), lastSize)
	}
}
