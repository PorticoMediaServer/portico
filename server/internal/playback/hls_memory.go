package playback

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/diskspace"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"sort"
	"strconv"
	"strings"
	"time"
)

const memoryHeadroom int64 = 16 << 20
const memoryUsageInterval = time.Second

type memoryFacts struct{ available, total, free, filesystemLimit int64 }

// ConfigureMemory is startup-only, before serving or starting maintenance. A
// tmpfs kernel ceiling must fit the effective allowance. Sampling alone cannot
// bound direct converter writes, including while cancellation drains a child.
// Oversized or unverifiable volumes use disk; no privileged mounts are created.
func (h *HLS) ConfigureMemory(root string) error {
	if !filepath.IsAbs(root) || root == filepath.VolumeName(root)+string(os.PathSeparator) {
		return errors.New("invalid memory output directory")
	}
	root = filepath.Clean(root)
	disk, err := filepath.Abs(h.root)
	if err != nil {
		return err
	}
	if root == disk || strings.HasPrefix(root, disk+string(os.PathSeparator)) || strings.HasPrefix(disk, root+string(os.PathSeparator)) {
		return errors.New("memory and disk output must be separate directories")
	}
	pinned, err := prepareMemoryOwnership(root, disk)
	if err != nil {
		return err
	}
	installed := false
	defer func() {
		if !installed {
			pinned.Close()
		}
	}()
	check := h.memoryCheck
	if check == nil {
		check = checkMemoryOutput
	}
	if err = check(root); err != nil {
		return err
	}
	cursor, err := os.Open(root)
	if err != nil {
		return err
	}
	defer cursor.Close()
	adopted := map[string]bool{}
	for {
		entries, readErr := cursor.ReadDir(128)
		for _, entry := range entries {
			if !entry.IsDir() || !generatedDirectoryID.MatchString(entry.Name()) {
				continue
			}
			id := entry.Name()
			_, diskErr := os.Lstat(filepath.Join(h.root, id))
			if diskErr == nil || h.diskPlacementExists(id) || !validOutputGeneration(filepath.Join(root, id)) {
				if diskErr != nil && !os.IsNotExist(diskErr) {
					return diskErr
				}
				if err = h.persistDiskPlacement(id); err != nil {
					return err
				}
				if err = os.RemoveAll(filepath.Join(root, id)); err != nil {
					return err
				}
				continue
			} else if !os.IsNotExist(diskErr) {
				return diskErr
			}
			adopted[id] = true
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	h.memoryRoot, h.memoryOutputRoot = root, pinned
	h.inMemory = adopted
	h.moving = map[string]chan struct{}{}
	installed = true
	return nil
}

func (h *HLS) outputDirLocked(id string) string {
	if h.inMemory[id] {
		return filepath.Join(h.memoryRoot, id)
	}
	return filepath.Join(h.root, id)
}
func (h *HLS) outputDir(id string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.outputDirLocked(id)
}
func (h *HLS) outputRootLocked(id string) *os.Root {
	if h.inMemory[id] {
		return h.memoryOutputRoot
	}
	return h.outputRoot
}
func (h *HLS) hasMemoryOutput() bool { h.mu.Lock(); defer h.mu.Unlock(); return len(h.inMemory) > 0 }

func (h *HLS) readMemory() (memoryFacts, error) {
	if h.memoryFacts != nil {
		return h.memoryFacts(h.memoryRoot)
	}
	return readMemoryFacts(h.memoryRoot)
}
func memoryBudget(setting string, facts memoryFacts, ours int64) (cap, floor int64) {
	floor = max(1<<30, facts.total/10)
	base := facts.available + ours
	switch setting {
	case "half":
		cap = base / 2
	case "2gb":
		cap = 2 << 30
	case "4gb":
		cap = 4 << 30
	case "8gb":
		cap = 8 << 30
	case "16gb":
		cap = 16 << 30
	default:
		cap = base / 4
	}
	cap = min(cap, max(0, ours+facts.available-floor), ours+facts.free)
	return max(0, cap), floor
}
func (h *HLS) memoryBytesLocked(reserve bool) int64 {
	var ours int64
	for id := range h.inMemory {
		if usage := h.usage[id]; usage != nil && !usage.measured.IsZero() {
			ours += usage.bytes
		} else if reserve {
			ours += memoryHeadroom
		}
	}
	return ours
}

// memoryVolumeFits reserves the entire remaining kernel allowance, not a
// producer's expected rate. Even every producer writing at once between samples
// (or ignoring cancellation) cannot grow beyond this volume. Keep the floor
// available after that worst case, including a constrained cgroup's allowance.
func memoryVolumeFits(setting string, facts memoryFacts, ours int64) bool {
	// Compare the ceiling with the configured/host allowance, independently of
	// remaining volume space. Ownership metadata and unmeasured writes consume
	// free blocks without changing the kernel ceiling. Admission below still
	// charges their actual free-space cost.
	allowance := facts
	allowance.free = facts.filesystemLimit
	cap, floor := memoryBudget(setting, allowance, ours)
	return facts.total > 0 && facts.filesystemLimit > 0 &&
		facts.filesystemLimit <= cap && facts.free >= 0 &&
		facts.free <= facts.filesystemLimit && facts.available-facts.free >= floor
}

// MemoryOutputAvailable publishes the same conservative volume admission used
// for placement. It probes facts only; it never creates or mounts a filesystem.
// Charging no existing output also avoids promising capacity on a stale sample.
func MemoryOutputAvailable(root, setting string) bool {
	if root == "" || checkMemoryOutput(root) != nil {
		return false
	}
	facts, err := readMemoryFacts(root)
	return err == nil && facts.free >= memoryHeadroom && memoryVolumeFits(setting, facts, 0)
}

// memoryAdmissionLocked charges unmeasured folders a reservation. Restarting a
// measured folder needs no new-folder headroom, but still needs tmpfs free space.
func (h *HLS) memoryAdmissionLocked(id, setting string, facts memoryFacts) bool {
	ours := h.memoryBytesLocked(true)
	headroom := memoryHeadroom
	if h.inMemory[id] {
		if usage := h.usage[id]; usage != nil && !usage.measured.IsZero() {
			headroom = 0
		} else {
			ours -= memoryHeadroom // This folder already has a reservation.
		}
	}
	cap, _ := memoryBudget(setting, facts, h.memoryBytesLocked(false))
	return memoryVolumeFits(setting, facts, h.memoryBytesLocked(false)) && ours+headroom <= cap && facts.free >= memoryHeadroom
}

// createOutput decides once, under the same lock as producer/cleanup admission.
// Facts and filesystem operations run outside h.mu. A creation lease fences
// cleanup and makes a spill wait until the selected directory is installed.
// Streaming callers classify disc/finite sources before creating their folder.
func (h *HLS) createOutput(id string, streaming bool) (string, error) {
	if !generatedDirectoryID.MatchString(id) {
		return "", os.ErrInvalid
	}
	cfg := h.configuration()
	setting := cfg.MemoryCap
	preferMemory := cfg.TranscodeLocation != "disk"
	var facts memoryFacts
	var factsErr error
	if streaming && preferMemory && h.memoryRoot != "" {
		facts, factsErr = h.readMemory()
	}
	h.mu.Lock()
	if h.moving[id] != nil || h.memoryFull[id] || h.cleaning[id] || h.outputBusy[id] > 0 {
		h.mu.Unlock()
		return "", ErrSegmentPreparing
	}
	if h.outputBusy == nil {
		h.outputBusy = map[string]int{}
	}
	h.outputBusy[id]++
	h.mu.Unlock()
	defer h.releaseOutput(id)

	// Pin the placement through the disk lookup: a spill can begin while we
	// read, but cannot flip it until this lease ends. Recheck its fence below.
	dir := filepath.Join(h.root, id)
	_, diskErr := os.Lstat(dir)
	diskOnly := h.diskPlacementExists(id)
	h.mu.Lock()
	if h.moving[id] != nil || h.memoryFull[id] || h.cleaning[id] {
		h.mu.Unlock()
		return "", ErrSegmentPreparing
	}
	if h.inMemory[id] {
		defer h.mu.Unlock()
		if !streaming {
			return "", errors.New("disk-only output has a memory placement")
		}
		if !preferMemory || factsErr != nil || !h.memoryAdmissionLocked(id, setting, facts) {
			h.memoryRefused.Add(1)
			// A refusal must force relocation even if the sweep's next sample
			// recovers or cannot be read. Otherwise a restart can stay preparing.
			h.markMemoryPressureLocked(id)
			return "", ErrSegmentPreparing
		}
		return h.outputDirLocked(id), nil
	}
	if diskErr == nil || !os.IsNotExist(diskErr) {
		h.mu.Unlock()
		return dir, diskErr
	}
	memory := false
	if streaming && preferMemory && !diskOnly && !h.diskOnly[id] && h.memoryRoot != "" {
		if factsErr == nil && h.memoryAdmissionLocked(id, setting, facts) {
			dir = filepath.Join(h.memoryRoot, id)
			h.inMemory[id] = true
			memory = true
		} else {
			h.memoryRefused.Add(1)
		}
	}
	if !memory {
		h.markDiskPlacementLocked(id)
	}
	h.mu.Unlock()
	var err error
	if !memory {
		err = h.persistDiskPlacement(id)
	}
	if err == nil {
		if memory {
			// The root already exists. Never follow or take over a conflicting
			// session entry, including a directory symlink.
			err = os.Mkdir(dir, 0700)
		} else {
			err = os.MkdirAll(dir, 0700)
		}
	}
	if err != nil && memory {
		// RAM is a preference. A race with ENOSPC or a changed directory must
		// try disk now, under the same lease, and remain there on every retry.
		h.mu.Lock()
		delete(h.inMemory, id)
		delete(h.memoryFull, id)
		h.markDiskPlacementLocked(id)
		h.mu.Unlock()
		dir = filepath.Join(h.root, id)
		err = h.persistDiskPlacement(id)
		if err == nil {
			err = os.MkdirAll(dir, 0700)
		}
	}
	return dir, err
}

// waitOutputMove fences readers before resolving a path. Starts return the
// existing retryable preparing result while moving; no converter can sneak in.
func (h *HLS) waitOutputMove(ctx context.Context, id string) error {
	h.mu.Lock()
	done := h.moving[id]
	h.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// sweepMemory measures idle as well as live folders, then spills at most one.
func (h *HLS) sweepMemory(ctx context.Context) {
	if h.memoryRoot == "" {
		return
	}
	h.memorySweepMu.Lock()
	defer h.memorySweepMu.Unlock()
	h.mu.Lock()
	ids := make([]string, 0, len(h.inMemory))
	for id := range h.inMemory {
		ids = append(ids, id)
	}
	h.mu.Unlock()
	h.enforceSessionCeilings(ids)
	facts, err := h.readMemory()
	cfg := h.configuration()
	setting := cfg.MemoryCap
	// ENOSPC and refused restarts independently require relocation, even if
	// host memory facts became unreadable. Unknown facts alone never spill.
	h.mu.Lock()
	forced := false
	for id := range h.memoryFull {
		if h.inMemory[id] {
			forced = true
			break
		}
	}
	h.mu.Unlock()
	// Unknown capacity cannot authorize continued RAM production. Spill an
	// adopted/current folder as well as refusing new placement.
	type candidate struct {
		id     string
		live   bool
		bytes  int64
		served time.Time
	}
	h.mu.Lock()
	ours := h.memoryBytesLocked(false)
	cap, _ := memoryBudget(setting, facts, ours)
	if cfg.TranscodeLocation != "disk" && !forced && err == nil && ours <= cap && facts.free >= memoryHeadroom && memoryVolumeFits(setting, facts, ours) {
		h.mu.Unlock()
		return
	}
	candidates := []candidate{}
	for id := range h.inMemory {
		if h.moving[id] != nil || h.cleaning[id] || forced && !h.memoryFull[id] {
			continue
		}
		c := candidate{id: id, live: h.sessionActiveLocked(id)}
		if u := h.usage[id]; u != nil {
			c.bytes, c.served = u.bytes, u.served
		}
		candidates = append(candidates, c)
	}
	h.mu.Unlock()
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.live != b.live {
			return !a.live
		}
		if a.live {
			return a.bytes > b.bytes
		}
		return a.served.Before(b.served)
	})
	if len(candidates) > 0 {
		h.spillMemory(ctx, candidates[0].id)
	}
}

func (h *HLS) spillMemory(ctx context.Context, id string) {
	h.mu.Lock()
	if !h.inMemory[id] || h.moving[id] != nil || h.cleaning[id] {
		h.mu.Unlock()
		return
	}
	done := make(chan struct{})
	if h.moving == nil {
		h.moving = map[string]chan struct{}{}
	}
	h.moving[id] = done
	// A timed-out spill must keep starts fenced until the old runs exit and a
	// later sweep completes relocation, even if memory facts recover meanwhile.
	h.markMemoryPressureLocked(id)
	// Mark the exact runs as well as fencing the session. If a stalled producer
	// outlives the move timeout, its eventual failure is still spill cancellation;
	// a replacement run never inherits the marker. All accesses hold h.mu.
	for key, run := range h.activeRuns {
		if producerSession(key) == id {
			run.spillCancelled = true
		}
	}
	source := h.outputDirLocked(id)
	h.mu.Unlock()
	finishMove := func() {
		h.mu.Lock()
		if h.moving[id] == done {
			delete(h.moving, id)
			close(done)
		}
		h.mu.Unlock()
	}
	defer finishMove()
	h.cancelSession(id)
	wait, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		h.mu.Lock()
		active := h.sessionActiveLocked(id) || h.outputBusy[id] > 0
		h.mu.Unlock()
		if !active {
			break
		}
		select {
		case <-wait.Done():
			return
		case <-tick.C:
		}
	}
	if ctx.Err() != nil {
		return
	}
	// Reserve durable placement before copying can consume the disk's last inode.
	h.mu.Lock()
	h.markDiskPlacementLocked(id)
	h.mu.Unlock()
	markerErr := h.persistDiskPlacement(id)
	if markerErr != nil {
		log.Printf("Memory conversion spill %s: disk placement could not be persisted: %v", id, markerErr)
	}
	temp, err := os.MkdirTemp(h.root, ".spill-"+id+"-")
	if err == nil {
		defer os.RemoveAll(temp)
		err = copySpill(ctx, source, temp)
		if err == nil {
			// A producer can fill tmpfs during initial manifest publication,
			// before it installs its generation stamp. Such an incomplete tree
			// cannot resume: FileContext only regenerates wholly absent output.
			if !validOutputGeneration(temp) {
				err = errors.New("incomplete generation in memory output")
			}
		}
	}
	if err != nil && temp != "" {
		_ = os.RemoveAll(temp)
	}
	// The move fence excludes starts, cleanup and new readers while filesystem
	// publication runs. Other sessions never wait for these disk operations.
	if err == nil {
		err = h.renameOutput(temp, filepath.Join(h.root, id))
	}
	// Retire memory while starts and path resolution remain fenced. If copying
	// failed, dropping regenerable output is safer than retaining RAM pressure.
	retired := filepath.Join(h.memoryRoot, fmt.Sprintf(".evicted-%s-%d", id, time.Now().UnixNano()))
	retireErr := h.renameOutput(source, retired)
	if retireErr != nil && !os.IsNotExist(retireErr) {
		log.Printf("Memory conversion spill %s: cannot retire output: %v", id, retireErr)
		return
	}
	h.mu.Lock()
	delete(h.inMemory, id)
	delete(h.memoryFull, id)
	if usage := h.usage[id]; usage != nil {
		usage.bytes, usage.files, usage.measured = 0, 0, time.Time{}
	}
	h.mu.Unlock()
	finishMove()
	if retireErr == nil {
		_ = os.RemoveAll(retired)
	}
	h.memorySpills.Add(1)
	if err == nil {
		log.Printf("Conversion %s spilled from memory to disk", id)
	} else {
		log.Printf("Conversion %s moved to disk after dropping memory output: %v", id, err)
	}
	// The directory column is diagnostic only; serving never follows it.
	if h.db != nil {
		write, cancel := persist(ctx)
		defer cancel()
		_, _ = dbwork.ExecWrite(write, h.db, dbwork.ClassEstablishedPlayback, `UPDATE playback_artifacts SET directory=? WHERE session_id=?`, filepath.Join(h.root, id), id)
	}
}

// copySpill never follows links or copies device/FIFO/socket entries. Traversal
// and copy buffers are bounded, and cancellation is checked between chunks.
func copySpill(ctx context.Context, source, dest string) error {
	root, err := os.OpenRoot(source)
	if err != nil {
		return err
	}
	defer root.Close()
	var bytes int64
	buf := make([]byte, 64<<10)
	return walkOutput(ctx, root, func(rel string, entry os.DirEntry) error {
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("symlink in conversion output")
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(dest, rel), 0700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("nonregular conversion output")
		}
		in, err := root.OpenFile(rel, hlsReadFlags, 0)
		if err != nil {
			return err
		}

		opened, statErr := in.Stat()
		if statErr != nil || !opened.Mode().IsRegular() {
			in.Close()
			return errors.New("nonregular spill source")
		}
		out, err := os.OpenFile(filepath.Join(dest, rel), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			in.Close()
			return err
		}
		for {
			if err = ctx.Err(); err != nil {
				break
			}
			var n int
			n, err = in.Read(buf)
			if n > 0 {
				bytes += int64(n)
				if bytes > hlsGlobalByteBudget {
					err = errors.New("spill copy exceeds byte bound")
					break
				}
				if _, e := out.Write(buf[:n]); e != nil {
					err = e
					break
				}
			}
			if err == io.EOF {
				err = nil
				break
			}
			if err != nil {
				break
			}
		}
		closeErr := out.Close()
		in.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
}

// Disc and finite producers always create disk output, including private discs
// that do not create a chapter sidecar while their source is being opened.
func (h *HLS) createStreamingOutput(ctx context.Context, id string, plan *DeliveryPlan) (string, error) {
	streaming := plan == nil || plan.Profile != finiteDeliveryProfile
	if h.memoryRoot != "" && streaming {
		var disc bool
		err := dbwork.QueryRow(ctx, h.db, `SELECT EXISTS(SELECT 1 FROM playback_disc_pins WHERE session_id=?) OR EXISTS(SELECT 1 FROM playback_disc_snapshots WHERE session_id=?)`, id, id).Scan(&disc)
		streaming = err == nil && !disc
	}
	return h.createOutput(id, streaming)
}

// Retention deletes played output outside h.mu. A move waits for these bounded
// mutations, just as it waits for producers, before copying a stable tree.
func (h *HLS) retainOutput(id string) (string, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.moving[id] != nil || h.cleaning[id] {
		return "", nil
	}
	if h.outputBusy == nil {
		h.outputBusy = map[string]int{}
	}
	h.outputBusy[id]++
	return h.outputDirLocked(id), func() { h.releaseOutput(id) }
}

// The kernel may refuse a tmpfs write before a sweep samples its size. Preserve
// the presentation, fence new producers and let maintenance move it to disk.
func (h *HLS) retryMemoryOutput(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.inMemory[id] {
		return false
	}
	h.markMemoryPressureLocked(id)
	return true
}

// memoryFull also records a refused restart so the next sweep must move it.
func (h *HLS) markMemoryPressureLocked(id string) {
	if h.memoryFull == nil {
		h.memoryFull = map[string]bool{}
	}
	h.memoryFull[id] = true
}

// Early manifest/setup failures do not carry a converter failure code. If the
// memory volume is already full, defer that failure to disk regeneration too.
func (h *HLS) memoryOutputPressure(id string) bool {
	h.mu.Lock()
	memory := h.inMemory[id]
	h.mu.Unlock()
	if !memory {
		return false
	}
	free, err := diskspace.Free(h.memoryRoot)
	if h.memoryFacts != nil {
		facts, e := h.readMemory()
		free, err = facts.free, e
	}
	return err == nil && free < memoryHeadroom && h.retryMemoryOutput(id)
}

const memoryOwnershipMarker = ".portico-hls-memory-v1"
const diskPlacementPrefix = ".disk-"

// A name is never evidence of ownership. Establish this boundary only over an
// empty private child, and bind it to the canonical disk root across restarts.
func prepareMemoryOwnership(directory, disk string) (*os.Root, error) {
	canonicalDisk, err := filepath.EvalSymlinks(disk)
	if err != nil {
		return nil, err
	}
	for path := directory; ; path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return nil, errors.New("memory output requires real directory parents")
		}
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if path == filepath.Dir(path) {
			break
		}
	}
	mounts, err := storagepaths.CurrentMounts()
	if err != nil {
		return nil, err
	}
	for _, mount := range mounts {
		if mount.Point == directory || strings.HasPrefix(mount.Point, directory+string(os.PathSeparator)) {
			return nil, errors.New("memory output must be a private child without mounted descendants")
		}
	}
	if err = os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	expected := fmt.Sprintf("Portico HLS memory v1\n%x\n", sha256.Sum256([]byte(canonicalDisk)))
	verify := func() error {
		info, err := root.Lstat(memoryOwnershipMarker)
		if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(expected)) {
			return errors.New("memory output is not owned by this installation")
		}
		file, err := root.OpenFile(memoryOwnershipMarker, hlsReadFlags, 0)
		if err != nil {
			return err
		}
		defer file.Close()
		opened, err := file.Stat()
		if err != nil || !os.SameFile(info, opened) {
			return errors.New("memory ownership changed")
		}
		raw, err := io.ReadAll(io.LimitReader(file, int64(len(expected)+1)))
		if err != nil || string(raw) != expected {
			return errors.New("memory output belongs to another installation")
		}
		return nil
	}
	if _, err = root.Lstat(memoryOwnershipMarker); os.IsNotExist(err) {
		cursor, e := root.Open(".")
		if e != nil {
			root.Close()
			return nil, e
		}
		entries, readErr := cursor.ReadDir(1)
		cursor.Close()
		if len(entries) != 0 || readErr != io.EOF {
			root.Close()
			return nil, errors.New("refusing to adopt nonempty unowned memory output")
		}
		if e := root.Chmod(".", 0700); e != nil {
			root.Close()
			return nil, e
		}
		marker, e := root.OpenFile(memoryOwnershipMarker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e == nil {
			_, e = io.WriteString(marker, expected)
			if e == nil {
				e = marker.Sync()
			}
			e = errors.Join(e, marker.Close())
		}
		if e != nil {
			root.Close()
			return nil, e
		}
	}
	info, err := root.Stat(".")
	if err != nil || info.Mode().Perm()&0022 != 0 {
		root.Close()
		return nil, errors.New("memory output requires a private directory")
	}
	if err = verify(); err != nil {
		root.Close()
		return nil, err
	}
	return root, nil
}

func validOutputGeneration(directory string) bool {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return false
	}
	defer root.Close()
	info, err := root.Lstat("generation")
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > 32 {
		return false
	}
	file, err := root.OpenFile("generation", hlsReadFlags, 0)
	if err != nil {
		return false
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return false
	}
	raw, err := io.ReadAll(io.LimitReader(file, 33))
	generation, parseErr := strconv.Atoi(string(raw))
	return err == nil && parseErr == nil && generation > 0 && string(raw) == strconv.Itoa(generation)
}

func (h *HLS) markDiskPlacementLocked(id string) {
	if h.diskOnly == nil {
		h.diskOnly = map[string]bool{}
	}
	h.diskOnly[id] = true
}
func (h *HLS) diskPlacementExists(id string) bool {
	info, err := os.Lstat(filepath.Join(h.root, diskPlacementPrefix+id))
	return err == nil && info.Mode().IsRegular() && info.Size() == 0
}
func (h *HLS) persistDiskPlacement(id string) error {
	if h.diskPlacementExists(id) {
		return nil
	}
	marker, err := os.OpenFile(filepath.Join(h.root, diskPlacementPrefix+id), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	return errors.Join(marker.Sync(), marker.Close())
}
func (h *HLS) renameOutput(from, to string) error {
	if h.outputRename != nil {
		return h.outputRename(from, to)
	}
	return os.Rename(from, to)
}

func (h *HLS) releaseOutput(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.outputBusy[id]--
	if h.outputBusy[id] == 0 {
		delete(h.outputBusy, id)
	}
}

// Interrupted initial publication has no usable generation. Only owned trees
// may be retired; an existing valid but different generation is never adopted.
// The cleanup fence excludes a producer publishing its first stamp right now.
func (h *HLS) retireIncompleteOutput(id string) {
	h.mu.Lock()
	if h.sessionActiveLocked(id) || h.moving[id] != nil || h.cleaning[id] || h.outputBusy[id] > 0 {
		h.mu.Unlock()
		return
	}
	if h.cleaning == nil {
		h.cleaning = map[string]bool{}
	}
	h.cleaning[id] = true
	directory := h.outputDirLocked(id)
	memory := h.inMemory[id]
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.cleaning, id); h.mu.Unlock() }()
	if (!memory && !h.diskPlacementExists(id)) || validOutputGeneration(directory) {
		return
	}
	if memory {
		if err := h.persistDiskPlacement(id); err != nil {
			return
		}
	}
	retired := filepath.Join(filepath.Dir(directory), fmt.Sprintf(".evicted-%s-%d", id, time.Now().UnixNano()))
	if err := h.renameOutput(directory, retired); err != nil {
		return
	}
	h.mu.Lock()
	h.markDiskPlacementLocked(id)
	delete(h.inMemory, id)
	delete(h.memoryFull, id)
	if usage := h.usage[id]; usage != nil {
		usage.bytes, usage.files, usage.measured = 0, 0, time.Time{}
	}
	h.mu.Unlock()
	_ = os.RemoveAll(retired)
}

// Known output setup writes may fail before a converter can report ENOSPC.
// Healthy/unreadable capacity facts do not make that RAM write safe to retry.
func (h *HLS) outputSetupFailed(ctx context.Context, id string) {
	if !h.retryMemoryOutput(id) {
		h.failed(ctx, id)
	}
}

func (h *HLS) retryMemoryPathError(id, directory string, cause error) bool {
	paths := []string{}
	var pathError *os.PathError
	if errors.As(cause, &pathError) {
		paths = append(paths, pathError.Path)
	}
	var linkError *os.LinkError
	if errors.As(cause, &linkError) {
		paths = append(paths, linkError.Old, linkError.New)
	}
	for _, path := range paths {
		relative, err := filepath.Rel(directory, path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			return h.retryMemoryOutput(id)
		}
	}
	return false
}

func (h *HLS) outputPublicationFailed(ctx context.Context, id string, plan *DeliveryPlan, position float64, cause error) {
	if h.retryMemoryPathError(id, h.outputDir(id), cause) {
		return
	}
	h.fragmentedFailure(ctx, id, plan, position, cause)
}
