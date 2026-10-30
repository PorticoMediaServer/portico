package sqlite

import (
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

// WALIOStats counts native WAL I/O, including uncommitted spills and failed
// writes. Written bytes are not retained allocation: rewriting a frame counts
// again. WrittenEnd is a high-water write offset, not an allocation estimate.
type WALIOStats struct {
	Qualified                              bool
	AttemptedBytes, WrittenBytes           uint64
	WrittenEnd, CurrentEnd                 int64
	Opens, HeaderWrites, Truncates, Errors uint64
}

type walIOState struct {
	path                              string
	refs                              int // guarded by walNative.mu
	unqualified                       atomic.Bool
	attempted, written                atomic.Uint64
	writtenEnd, currentEnd            atomic.Int64
	opens, headers, truncates, errors atomic.Uint64
}

type walNativeFile struct {
	state    *walIOState
	original sqlite3.Tsqlite3_io_methods
	methods  uintptr
}

var walNative = struct {
	once      sync.Once
	base, vfs uintptr // one process-lifetime shim of the default unix VFS
	mu        sync.RWMutex
	states    map[string]*walIOState
	files     map[uintptr]*walNativeFile
}{states: make(map[string]*walIOState), files: make(map[uintptr]*walNativeFile)}

const walVFSName = "portico-wal-io"

// Accounting is qualified only for the source-reviewed default Linux unix
// VFS. Other platforms/custom VFSes preserve their native path and explicitly
// report unqualified. The shim keeps all native locking/sync/read/SHM methods;
// only WAL xWrite/xTruncate/xClose are observed. No extra file descriptor opens.
func accountingVFS(c *conn, requested string) (string, bool) {
	if requested != "" || runtime.GOOS != "linux" {
		return requested, false
	}
	walNative.once.Do(func() {
		base := sqlite3.Xsqlite3_vfs_find(c.tls, 0)
		if base == 0 {
			return
		}
		original := (*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(base))
		if libc.GoString(original.FzName) != "unix" || original.FiVersion < 1 {
			return
		}
		p := sqlite3.Xsqlite3_malloc64(c.tls, uint64(unsafe.Sizeof(*original)))
		if p == 0 {
			return
		}
		name, err := libc.CString(walVFSName)
		if err != nil {
			sqlite3.Xsqlite3_free(c.tls, p)
			return
		}
		shim := (*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(p))
		*shim = *original
		shim.FpNext = 0
		shim.FzName = name
		shim.FxOpen = cFuncPointer(walOpen)
		walNative.base = base
		if sqlite3.Xsqlite3_vfs_register(c.tls, p, 0) != sqlite3.SQLITE_OK {
			walNative.base = 0
			sqlite3.Xsqlite3_free(c.tls, p)
			libc.Xfree(c.tls, name)
			return
		}
		walNative.vfs = p
	})
	if walNative.vfs == 0 {
		return requested, false
	}
	return walVFSName, true
}

func retainWALState(path string) *walIOState {
	walNative.mu.Lock()
	defer walNative.mu.Unlock()
	s := walNative.states[path]
	if s == nil {
		s = &walIOState{path: path}
		walNative.states[path] = s
	}
	s.refs++
	return s
}

func releaseWALState(s *walIOState) {
	if s == nil {
		return
	}
	walNative.mu.Lock()
	defer walNative.mu.Unlock()
	s.refs--
	if s.refs == 0 {
		delete(walNative.states, s.path)
	}
}

func walOpen(tls *libc.TLS, _ uintptr, name, file uintptr, flags int32, outFlags uintptr) int32 {
	base := (*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(walNative.base))
	fn := base.FxOpen
	rc := (*(*func(*libc.TLS, uintptr, uintptr, uintptr, int32, uintptr) int32)(unsafe.Pointer(&fn)))(tls, walNative.base, name, file, flags, outFlags)
	if rc != sqlite3.SQLITE_OK || flags&sqlite3.SQLITE_OPEN_WAL == 0 || name == 0 {
		return rc
	}
	path := libc.GoString(name)
	if !strings.HasSuffix(path, "-wal") {
		return rc
	}
	s := retainWALState(strings.TrimSuffix(path, "-wal"))
	s.opens.Add(1)
	f := (*sqlite3.Tsqlite3_file)(unsafe.Pointer(file))
	if f.FpMethods == 0 {
		s.unqualified.Store(true)
		releaseWALState(s)
		return rc
	}
	original := *(*sqlite3.Tsqlite3_io_methods)(unsafe.Pointer(f.FpMethods))
	p := sqlite3.Xsqlite3_malloc64(tls, uint64(unsafe.Sizeof(original)))
	if p == 0 {
		s.unqualified.Store(true)
		releaseWALState(s)
		return rc
	}
	methods := (*sqlite3.Tsqlite3_io_methods)(unsafe.Pointer(p))
	*methods = original
	methods.FxWrite = cFuncPointer(walWrite)
	methods.FxTruncate = cFuncPointer(walTruncate)
	methods.FxClose = cFuncPointer(walClose)
	walNative.mu.Lock()
	walNative.files[file] = &walNativeFile{state: s, original: original, methods: p}
	walNative.mu.Unlock()
	f.FpMethods = p
	return rc
}

func walFile(file uintptr) *walNativeFile {
	walNative.mu.RLock()
	f := walNative.files[file]
	walNative.mu.RUnlock()
	return f
}

func raiseWALMax(value *atomic.Int64, end int64) {
	for old := value.Load(); end > old; old = value.Load() {
		if value.CompareAndSwap(old, end) {
			return
		}
	}
}

func walWrite(tls *libc.TLS, file, data uintptr, n int32, offset int64) int32 {
	f := walFile(file)
	if n > 0 {
		f.state.attempted.Add(uint64(n))
	}
	fn := f.original.FxWrite
	rc := (*(*func(*libc.TLS, uintptr, uintptr, int32, int64) int32)(unsafe.Pointer(&fn)))(tls, file, data, n, offset)
	if rc == sqlite3.SQLITE_OK {
		if n > 0 {
			f.state.written.Add(uint64(n))
		}
		if offset >= 0 && n >= 0 && offset <= int64(^uint64(0)>>1)-int64(n) {
			end := offset + int64(n)
			raiseWALMax(&f.state.writtenEnd, end)
			raiseWALMax(&f.state.currentEnd, end)
		}
		if offset == 0 {
			f.state.headers.Add(1)
		}
	} else {
		f.state.errors.Add(1)
	}
	return rc
}

func walTruncate(tls *libc.TLS, file uintptr, size int64) int32 {
	f := walFile(file)
	f.state.truncates.Add(1)
	fn := f.original.FxTruncate
	rc := (*(*func(*libc.TLS, uintptr, int64) int32)(unsafe.Pointer(&fn)))(tls, file, size)
	if rc == sqlite3.SQLITE_OK {
		f.state.currentEnd.Store(size)
	} else {
		f.state.errors.Add(1)
	}
	return rc
}

func walClose(tls *libc.TLS, file uintptr) int32 {
	f := walFile(file)
	fn := f.original.FxClose
	rc := (*(*func(*libc.TLS, uintptr) int32)(unsafe.Pointer(&fn)))(tls, file)
	walNative.mu.Lock()
	delete(walNative.files, file)
	walNative.mu.Unlock()
	sqlite3.Xsqlite3_free(tls, f.methods)
	releaseWALState(f.state)
	return rc
}

// WALIO is cumulative for the native filename while its connections/WAL files
// remain alive. It includes every connection's writes to that one WAL; dbwork
// attributes intervals only under its sole writer gate. The OS/native callbacks
// allocate only when a file opens, never per write/frame.
func (c *conn) WALIO() WALIOStats {
	s := c.walIO
	if s == nil {
		return WALIOStats{}
	}
	return WALIOStats{Qualified: !s.unqualified.Load(), AttemptedBytes: s.attempted.Load(), WrittenBytes: s.written.Load(), WrittenEnd: s.writtenEnd.Load(), CurrentEnd: s.currentEnd.Load(), Opens: s.opens.Load(), HeaderWrites: s.headers.Load(), Truncates: s.truncates.Load(), Errors: s.errors.Load()}
}

func (c *conn) WALWriteBytes() (uint64, bool) { s := c.WALIO(); return s.AttemptedBytes, s.Qualified }
