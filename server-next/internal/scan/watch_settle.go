package scan

import (
	"os"
	"strconv"
	"time"
)

// settling follows folders in which something is still being written. A system that tells of
// a folder's entries changing, and not of a file being closed after writing (macOS), says
// nothing while a film is copied in and nothing when the copy ends. So a folder that changed
// is looked at again every few seconds, and each time it is found different (a file grew) its
// library is reported again; after two looks with no difference it is left alone. The last
// report therefore comes after the last byte.
type settling struct {
	folders map[string]*settlingFolder
}

type settlingFolder struct {
	library string
	looks   string // what the folder looked like at the last look
	quiet   int    // looks in a row with no difference
	next    time.Time
}

const (
	settleEvery = 10 * time.Second
	settleQuiet = 2
)

// touch starts following a folder, or starts its quiet count again.
func (s *settling) touch(folder, library string, now time.Time) {
	if s.folders == nil {
		s.folders = map[string]*settlingFolder{}
	}
	f := s.folders[folder]
	if f == nil {
		f = &settlingFolder{library: library, looks: looksOf(folder)}
		s.folders[folder] = f
	}
	f.quiet, f.next = 0, now.Add(settleEvery)
}

// poll looks again at every folder whose time has come, and reports the library of each one
// that changed since its last look.
func (s *settling) poll(now time.Time, changed func(library string)) {
	for folder, f := range s.folders {
		if now.Before(f.next) {
			continue
		}
		f.next = now.Add(settleEvery)
		if looks := looksOf(folder); looks != f.looks {
			f.looks, f.quiet = looks, 0
			changed(f.library)
		} else if f.quiet++; f.quiet >= settleQuiet {
			delete(s.folders, folder)
		}
	}
}

// looksOf is a folder's entries reduced to a few numbers: how many, their sizes together and
// the latest time one was written. A folder that cannot be listed looks like nothing.
func looksOf(folder string) string {
	entries, err := os.ReadDir(folder)
	if err != nil {
		return ""
	}
	var size, latest int64
	for _, entry := range entries {
		if info, err := entry.Info(); err == nil {
			size += info.Size()
			latest = max(latest, info.ModTime().UnixNano())
		}
	}
	return strconv.Itoa(len(entries)) + ":" + strconv.FormatInt(size, 10) + ":" + strconv.FormatInt(latest, 10)
}
