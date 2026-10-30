package servicelog

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PruneFiles scans only the bounded log set, outside the recording lock.
// Publication takes the lock briefly and is abandoned if rotation or append
// changed the source. A busy log is retried on the next maintenance tick.
func (r *Recorder) PruneFiles() {
	if !r.pruneMu.TryLock() {
		return
	}
	defer r.pruneMu.Unlock()
	r.mu.Lock()
	days := make(map[string]int, len(r.retention))
	for category, age := range r.retention {
		days[category] = age
	}
	r.mu.Unlock()
	if len(days) == 0 || r.options.Directory == "" {
		return
	}
	for index := r.options.FileCount; index >= 0; index-- {
		r.pruneFile(index, days)
	}
}

func (r *Recorder) pruneFile(index int, days map[string]int) {
	path := r.path(index)
	input, err := os.Open(path)
	if err != nil {
		return
	}
	before, err := input.Stat()
	if err != nil || before.Size() > r.options.FileBytes+(64<<10) {
		input.Close()
		return
	}
	now := r.options.Now()
	// A file with nothing to drop is the common case: read it once, create no
	// output and leave the inode, bytes and modification time untouched.
	removed, err := retentionWouldRemove(input, before.Size(), days, now)
	if err != nil || !removed {
		input.Close()
		return
	}
	// Only a file that really loses lines is rewritten, from the same snapshot
	// size. The publication check below still abandons it if an append or
	// rotation changed the file while this ran.
	if _, err = input.Seek(0, io.SeekStart); err != nil {
		input.Close()
		return
	}
	output, err := os.CreateTemp(r.options.Directory, ".retention-*")
	if err != nil {
		input.Close()
		return
	}
	defer os.Remove(output.Name())
	scanner := bufio.NewScanner(io.LimitReader(input, before.Size()+1))
	scanner.Buffer(make([]byte, 4096), 64<<10)
	var writeErr error
	for scanner.Scan() {
		line := scanner.Text()
		if retentionDrops(strings.SplitN(line, "\t", 4), days, now) {
			continue
		}
		if _, writeErr = output.WriteString(line + "\n"); writeErr != nil {
			break
		}
	}
	input.Close()
	err = writeErr
	if err == nil {
		err = scanner.Err()
	}
	if err == nil {
		err = output.Sync()
	}
	closeErr := output.Close()
	if err != nil || closeErr != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, err := os.Stat(path)
	if err != nil || !os.SameFile(before, current) || current.Size() != before.Size() || !current.ModTime().Equal(before.ModTime()) {
		return
	}
	if index == 0 && r.file != nil {
		r.file.Close()
		r.file = nil
	}
	// Windows cannot replace an open log. Close the recorder before rename,
	// and retain the original if publication fails.
	if err = os.Rename(output.Name(), path); err != nil {
		if index == 0 {
			r.file, _ = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		}
		return
	}
	if index == 0 {
		r.file, _ = os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if r.file != nil {
			r.written = fileSize(r.file)
		}
	}
}

// retentionWouldRemove reports whether any line would be dropped. It only reads;
// no output file is created when the answer is no.
func retentionWouldRemove(input io.Reader, size int64, days map[string]int, now time.Time) (bool, error) {
	scanner := bufio.NewScanner(io.LimitReader(input, size+1))
	scanner.Buffer(make([]byte, 4096), 64<<10)
	for scanner.Scan() {
		if retentionDrops(strings.SplitN(scanner.Text(), "\t", 4), days, now) {
			return true, nil
		}
	}
	return false, scanner.Err()
}

// retentionDrops is the one retention predicate shared by the read-only pass and
// the rewrite, so the two can never disagree about what a line is.
func retentionDrops(fields []string, days map[string]int, now time.Time) bool {
	if len(fields) != 4 {
		return false
	}
	age := days[fields[2]]
	if age <= 0 {
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, fields[0])
	return err == nil && now.Sub(at) > time.Duration(age)*24*time.Hour
}
