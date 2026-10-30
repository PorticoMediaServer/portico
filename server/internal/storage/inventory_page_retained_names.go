package storage

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Retain the platform's directory stream, including Go's unread readdir buffer.
// Reopening or seeking a Darwin directory by an entry count is not a continuation.
// This cache lives in the isolated helper (or a deterministic local caller).
var inventoryNameCache = struct {
	sync.Mutex
	entries map[string]*inventoryNameStream
}{entries: map[string]*inventoryNameStream{}}

type inventoryNameStream struct {
	mu          sync.Mutex
	file        *os.File
	changes     *inventoryChanges
	info        os.FileInfo
	revision    string
	input, next string
	names       []string
	done        bool
	timer       *time.Timer
	expires     time.Time
	closed      bool
}

func inventoryToken() (string, error) {
	var b [16]byte
	_, err := rand.Read(b[:])
	return hex.EncodeToString(b[:]), err
}
func retainedInventoryNames(f *os.File, cursor string, limit int) ([]string, string, bool, error) {
	return retainedInventoryNamesAt(f, f.Name(), cursor, limit)
}
func retainedInventoryNamesAt(f *os.File, path, cursor string, limit int) ([]string, string, bool, error) {
	if limit < 1 || limit > 128 {
		return nil, "", false, ErrInventoryChanged
	}
	id, _, hasID := strings.Cut(cursor, ":")
	var s *inventoryNameStream
	inventoryNameCache.Lock()
	if cursor != "" {
		if !hasID {
			inventoryNameCache.Unlock()
			return nil, "", false, ErrInventoryChanged
		}
		s = inventoryNameCache.entries[id]
		if s == nil {
			inventoryNameCache.Unlock()
			return nil, "", false, ErrInventoryChanged
		}
	} else {
		if len(inventoryNameCache.entries) >= 32 {
			for oldID, old := range inventoryNameCache.entries {
				if old.mu.TryLock() {
					if old.done && old.file == nil {
						old.closed = true
						old.changes.Close()
						if old.timer != nil {
							old.timer.Stop()
						}
						delete(inventoryNameCache.entries, oldID)
						old.mu.Unlock()
						break
					}
					old.mu.Unlock()
				}
			}
		}
		if len(inventoryNameCache.entries) >= 32 {
			inventoryNameCache.Unlock()
			return nil, "", false, ErrBusy
		}
		var err error
		id, err = inventoryToken()
		if err != nil {
			inventoryNameCache.Unlock()
			return nil, "", false, err
		}
		s = &inventoryNameStream{}
		inventoryNameCache.entries[id] = s
	}
	// The registry never holds its mutex across filesystem operations.
	if !s.mu.TryLock() {
		inventoryNameCache.Unlock()
		return nil, "", false, ErrBusy
	}
	inventoryNameCache.Unlock()
	defer s.mu.Unlock()
	expire := func() {
		// Closing a stuck OS descriptor retains this one bounded cache slot until
		// close completes. Production helpers are also physically supervised.
		s.mu.Lock()
		if time.Now().Before(s.expires) {
			s.mu.Unlock()
			return
		}
		s.closed = true
		s.changes.Close()
		if s.file != nil {
			s.file.Close()
			s.file = nil
		}
		s.mu.Unlock()
		inventoryNameCache.Lock()
		if inventoryNameCache.entries[id] == s {
			delete(inventoryNameCache.entries, id)
		}
		inventoryNameCache.Unlock()
	}
	if s.timer != nil {
		s.timer.Stop()
	}
	s.expires = time.Now().Add(2 * time.Minute)
	s.timer = time.AfterFunc(2*time.Minute, expire)
	if s.closed {
		return nil, "", false, ErrInventoryChanged
	}
	if s.info == nil && s.changes == nil {
		var err error
		s.changes, err = watchInventoryChanges(f)
		if err != nil {
			return nil, "", false, err
		}
	}
	if s.changes.Changed() {
		return nil, "", false, ErrInventoryChanged
	}
	info, err := f.Stat()
	if err != nil {
		return nil, "", false, err
	}
	revision := InventoryRevision(snapshot(f.Name(), info))
	if s.info == nil {
		// Open once, then compare the physical object to the already anchored handle.
		s.file, err = os.Open(path)
		if err != nil {
			return nil, "", false, err
		}
		s.info, err = s.file.Stat()
		if err != nil || !os.SameFile(info, s.info) {
			return nil, "", false, ErrInventoryChanged
		}
		s.revision = revision
	} else if !os.SameFile(info, s.info) || s.revision != revision {
		return nil, "", false, ErrInventoryChanged
	}
	if s.changes.Changed() {
		return nil, "", false, ErrInventoryChanged
	}
	if s.names != nil && cursor == s.input {
		return append([]string{}, s.names...), s.next, s.done, nil
	}
	if s.done || cursor != s.next {
		return nil, "", false, ErrInventoryChanged
	}
	names, err := s.file.Readdirnames(limit)
	if err != nil && err != io.EOF {
		return nil, "", false, err
	}
	if s.changes.Changed() {
		return nil, "", false, ErrInventoryChanged
	}
	s.input = cursor
	s.names = append([]string{}, names...)
	s.done = err == io.EOF
	if s.done {
		s.file.Close()
		s.file = nil
		s.next = ""
	} else {
		// Only this stream interprets its sequence, never a pathname or OS offset.
		n := int64(0)
		if cursor != "" {
			_, value, _ := strings.Cut(cursor, ":")
			n, err = strconv.ParseInt(value, 10, 64)
			if err != nil {
				return nil, "", false, ErrInventoryChanged
			}
		}
		s.next = id + ":" + strconv.FormatInt(n+1, 10)
	}
	return append([]string{}, s.names...), s.next, s.done, nil
}
