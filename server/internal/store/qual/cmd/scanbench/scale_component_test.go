package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/diskspace"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// These tests are metadata components, never playable-media or qualification
// fixtures. Logical movie sizes are realistic; 4 KiB/file is actually written
// and the rest is sparse. Do not use these files to claim media read throughput.
const scaleSeed uint64 = 0x504f525449434f34

func mixScale(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func scaleMember(i int, seed uint64) (string, int64) {
	titles := [...]string{"The Arrival", "Amélie", "千と千尋の神隠し", "A Quiet Place", "Léon", "Blade Runner", "The Godfather", "Se7en", "Spirited Away", "An Autumn Tale"}
	h := mixScale(seed ^ uint64(i))
	bucket := i / 128
	b := mixScale(seed ^ uint64(bucket))
	dirs := []string{"Movies", fmt.Sprintf("%c", 'A'+b%26)}
	if b&1 != 0 {
		dirs = append(dirs, fmt.Sprintf("Collection %02d", b%29))
	}
	if b&2 != 0 {
		dirs = append(dirs, "Restored Editions")
	}
	dirs = append(dirs, fmt.Sprintf("Shelf-%05d", bucket))
	title := fmt.Sprintf("%s (%d) [EC4-%09d]", titles[h%uint64(len(titles))], 1960+h%66, i)
	if h%11 == 0 {
		title += " - pt" + strconv.FormatUint(1+h%2, 10)
	}
	if h%7 == 0 {
		title += " [2160p HEVC]"
	}
	exts := [...]string{".mkv", ".mp4", ".avi", ".m4v"}
	size := int64(500<<20) + int64(h%(3500<<20))
	if h%5 == 0 {
		size = int64(4<<30) + int64(h%(24<<30))
	}
	if h%31 == 0 {
		size = int64(16<<20) + int64(h%(112<<20))
		title += " - Trailer"
	}
	return filepath.Join(append(dirs, title+exts[(h>>8)%4])...), size
}

func generateScale(ctx context.Context, root string, count int, seed uint64) (string, int64, error) {
	if count < 1 || count > 400000 {
		return "", 0, fmt.Errorf("invalid component count")
	}
	free, err := diskspace.Free(filepath.Dir(root))
	if err != nil {
		return "", 0, err
	}
	// Leave 4 GiB on this disposable runner; no development-host scale execution.
	if free < int64(count)*16384+(4<<30) {
		return "", 0, fmt.Errorf("insufficient component scratch headroom: %d", free)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		return "", 0, err
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return "", 0, err
	}
	defer dir.Close()
	hash := sha256.New()
	fmt.Fprintf(hash, "ec4-sparse-metadata-v1 %d %d\n", count, seed)
	payload := make([]byte, 4096)
	var total int64
	stamp := time.Unix(946684800, 0)
	for i := 0; i < count; i++ {
		if err := ctx.Err(); err != nil {
			return "", total, err
		}
		relative, size := scaleMember(i, seed)
		if i%128 == 0 {
			if err := dir.MkdirAll(filepath.Dir(relative), 0700); err != nil {
				return "", total, err
			}
		}
		file, err := dir.OpenFile(relative, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return "", total, err
		}
		for j := range payload {
			payload[j] = byte(mixScale(seed ^ uint64(i) ^ uint64(j)))
		}
		_, writeErr := file.Write(payload)
		truncateErr := file.Truncate(size)
		closeErr := file.Close()
		if writeErr != nil {
			return "", total, writeErr
		}
		if truncateErr != nil {
			return "", total, truncateErr
		}
		if closeErr != nil {
			return "", total, closeErr
		}
		if err := dir.Chtimes(relative, stamp, stamp); err != nil {
			return "", total, err
		}
		total += size
		fmt.Fprintf(hash, "%d %s %d %x\n", i, filepath.ToSlash(relative), size, sha256.Sum256(payload))
	}
	return hex.EncodeToString(hash.Sum(nil)), total, nil
}

type scaleMetrics struct {
	PeakHeapAlloc, PeakHeapInuse          uint64
	SampledPeakFDs, SampledPeakGoroutines int
	ParentStatus                          string
}

func sampleScale(stop <-chan struct{}, done chan<- scaleMetrics) {
	var peak scaleMetrics
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	sample := func() {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		peak.PeakHeapAlloc = max(peak.PeakHeapAlloc, m.HeapAlloc)
		peak.PeakHeapInuse = max(peak.PeakHeapInuse, m.HeapInuse)
		fds, _ := os.ReadDir("/proc/self/fd")
		peak.SampledPeakFDs = max(peak.SampledPeakFDs, len(fds))
		peak.SampledPeakGoroutines = max(peak.SampledPeakGoroutines, runtime.NumGoroutine())
	}
	sample()
	for {
		select {
		case <-tick.C:
			sample()
		case <-stop:
			sample()
			data, _ := os.ReadFile("/proc/self/status")
			peak.ParentStatus = string(data)
			done <- peak
			return
		}
	}
}

// Complete raw pages use the existing supervised NATIVE transport, Limit128.
// This is not the N1 accepting walker: no splitting/synthetic intermediate
// cookie, writer submission, unsupported filtering or committed EOF occurs.
func walkScale(ctx context.Context, root string, count int, seed uint64, limit int) (files, pages, depth int, err error) {
	binary, err := os.Executable()
	if err != nil {
		return 0, 0, 0, err
	}
	client := storage.New(binary)
	owner := storage.NewOperationScope()
	defer func() {
		owner.Close()
		if e := owner.Wait(context.Background()); err == nil {
			err = e
		}
	}()
	type frame struct {
		q        storage.NativeInventoryRequest
		p        *storage.NativeInventoryPage
		at       int
		identity string
	}
	stack := []frame{{q: storage.NativeInventoryRequest{Root: []byte(root), RelativePath: []byte("."), Limit: limit}}}
	rootID := ""
	seen := make([]byte, (count+7)/8)
	for len(stack) > 0 {
		if e := ctx.Err(); e != nil {
			return files, pages, depth, e
		}
		depth = max(depth, len(stack))
		f := &stack[len(stack)-1]
		if f.p == nil {
			f.q.RootIdentity = rootID
			p, e := client.NativeInventoryPage(ctx, owner, "ec4-metadata-component", f.q)
			if e != nil {
				return files, pages, depth, e
			}
			pages++
			if rootID == "" {
				rootID = p.RootIdentity
			}
			if p.RootIdentity != rootID || p.Unstable || len(p.Issues) != 0 || len(p.Entries) > limit || p.DirectoryIdentity == "" || p.DirectoryRevision == "" || f.identity != "" && f.identity != p.DirectoryIdentity || f.q.DirectoryRevision != "" && f.q.DirectoryRevision != p.DirectoryRevision {
				return files, pages, depth, fmt.Errorf("changed or incomplete native page")
			}
			if !p.Complete && (p.NextCursor == "" || p.NextCursor == f.q.Cursor) {
				return files, pages, depth, fmt.Errorf("nonprogressing cookie")
			}
			f.identity = p.DirectoryIdentity
			f.p = &p
			f.at = 0
		}
		if f.at == len(f.p.Entries) {
			if f.p.Complete {
				stack = stack[:len(stack)-1]
			} else {
				f.q.Cursor, f.q.DirectoryRevision = f.p.NextCursor, f.p.DirectoryRevision
				f.p = nil
			}
			continue
		}
		entry := f.p.Entries[f.at]
		f.at++
		relative, e := filepath.Rel(root, string(entry.Path))
		if e != nil || !filepath.IsLocal(relative) {
			return files, pages, depth, fmt.Errorf("outside fixture")
		}
		if entry.Directory {
			if len(stack) > 12 {
				return files, pages, depth, fmt.Errorf("unexpected depth")
			}
			stack = append(stack, frame{q: storage.NativeInventoryRequest{Root: []byte(root), RelativePath: []byte(relative), Limit: limit}})
			continue
		}
		marker := strings.LastIndex(string(entry.Name), "[EC4-")
		if marker < 0 || len(entry.Name) < marker+15 {
			return files, pages, depth, fmt.Errorf("unexpected member")
		}
		index, e := strconv.Atoi(string(entry.Name[marker+5 : marker+14]))
		if e != nil || index < 0 || index >= count {
			return files, pages, depth, fmt.Errorf("bad member index")
		}
		expected, size := scaleMember(index, seed)
		if relative != expected || entry.Size != size || seen[index/8]&(1<<uint(index%8)) != 0 {
			return files, pages, depth, fmt.Errorf("missing/duplicate/changed fixture member %d", index)
		}
		seen[index/8] |= 1 << uint(index%8)
		files++
	}
	if files != count {
		return files, pages, depth, fmt.Errorf("observed %d of %d", files, count)
	}
	return files, pages, depth, nil
}

func runScaleComponent(t *testing.T, count int, limit int) {
	t.Helper()
	if runtime.GOOS != "linux" || os.Getenv("PORTICO_LANE_EVIDENCE_DIR") == "" || flag.Lookup("test.run").Value.String() != "^"+t.Name()+"$" {
		t.Skip("explicit exact lane-focused component selection only")
	}
	scratch := t.TempDir()
	stop := make(chan struct{})
	done := make(chan scaleMetrics, 1)
	go sampleScale(stop, done)
	var once sync.Once
	defer once.Do(func() { close(stop); <-done })
	started := time.Now()
	digest, logical, err := generateScale(t.Context(), filepath.Join(scratch, "library"), count, scaleSeed)
	generation := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	started = time.Now()
	files, pages, depth, walkErr := walkScale(t.Context(), filepath.Join(scratch, "library"), count, scaleSeed, limit)
	traversal := time.Since(started)
	close(stop)
	metrics := <-done
	once.Do(func() {})
	evidence := struct {
		Label                             string
		Qualification                     bool
		FixtureSHA256                     string
		Seed                              uint64
		Files, Pages, Depth, RawLimit     int
		LogicalBytes, WrittenPayloadBytes int64
		Sparse                            bool
		GenerationNS, TraversalNS         int64
		Metrics                           scaleMetrics
		TraversalError                    string
		FilenameVisibleNS, BasicNS        *int64
	}{"COMPONENT_ONLY", false, digest, scaleSeed, files, pages, depth, limit, logical, int64(count) * 4096, true, generation.Nanoseconds(), traversal.Nanoseconds(), metrics, "", nil, nil}
	if walkErr != nil {
		evidence.TraversalError = walkErr.Error()
	}
	data, _ := json.MarshalIndent(evidence, "", "  ")
	path := filepath.Join(os.Getenv("PORTICO_LANE_EVIDENCE_DIR"), fmt.Sprintf("ec4-%d-limit%d.json", count, limit))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("component files=%d pages=%d depth=%d generation=%s traversal=%s heap_peak=%d fds_sampled_peak=%d sparse=true qualification=false", files, pages, depth, generation, traversal, metrics.PeakHeapAlloc, metrics.SampledPeakFDs)
	if walkErr != nil {
		t.Fatal(walkErr)
	}
}
func TestEC4Full400kLibrary(t *testing.T) { runScaleComponent(t, 400000, 128) }
func TestEC4N1ScalePilot(t *testing.T)    { runScaleComponent(t, 2048, 1) }

func TestEC4ScaleRecipeDeterministic(t *testing.T) {
	a, bytesA, err := generateScale(t.Context(), filepath.Join(t.TempDir(), "a"), 32, scaleSeed)
	if err != nil {
		t.Fatal(err)
	}
	b, bytesB, err := generateScale(t.Context(), filepath.Join(t.TempDir(), "b"), 32, scaleSeed)
	if err != nil || a != b || bytesA != bytesB {
		t.Fatal("not deterministic", a, b, err)
	}
	pathA, sizeA := scaleMember(1, scaleSeed)
	pathB, sizeB := scaleMember(1, scaleSeed+1)
	if pathA == pathB && sizeA == sizeB {
		t.Fatal("seed ignored")
	}
}
