//go:build !windows

package sqlite

import (
	"fmt"
	"unsafe"

	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

// SQLiteMainFileIdentity reads the identity SQLite obtained from its OPEN main
// file, not a new pathname lookup. The bundled Unix VFS retains its fstat-derived
// device/inode in unixInodeInfo for the lifetime of that file. Unknown VFSes,
// memory databases and closed connections cannot supply this attestation.
// No descriptor, native pointer or mutable callback escapes the connection.
func (c *conn) SQLiteMainFileIdentity() (device, inode uint64, err error) {
	c.Lock()
	defer c.Unlock()
	if c.db == 0 {
		return 0, 0, fmt.Errorf("sqlite: file identity on closed connection")
	}
	word := int(unsafe.Sizeof(uintptr(0)))
	arg := c.tls.Alloc(2 * word)
	defer c.tls.Free(2 * word)
	*(*uintptr)(unsafe.Pointer(arg)) = 0
	*(*uintptr)(unsafe.Pointer(arg + uintptr(word))) = 0
	if err := c.fileControl("main", sqlite3.SQLITE_FCNTL_VFS_POINTER, arg); err != nil {
		return 0, 0, err
	}
	vfs := *(*uintptr)(unsafe.Pointer(arg))
	if vfs == 0 {
		return 0, 0, fmt.Errorf("sqlite: main file has no VFS")
	}
	name := libc.GoString((*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(vfs)).FzName)
	if name != "unix" && name != walVFSName {
		return 0, 0, fmt.Errorf("sqlite: unsupported main-file identity VFS %q", name)
	}
	if err := c.fileControl("main", sqlite3.SQLITE_FCNTL_FILE_POINTER, arg+uintptr(word)); err != nil {
		return 0, 0, err
	}
	file := *(*uintptr)(unsafe.Pointer(arg + uintptr(word)))
	if file == 0 {
		return 0, 0, fmt.Errorf("sqlite: missing main file")
	}
	f := (*sqlite3.TunixFile)(unsafe.Pointer(file))
	if f.FpInode == 0 || f.FpVfs == 0 || libc.GoString((*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(f.FpVfs)).FzName) != "unix" {
		return 0, 0, fmt.Errorf("sqlite: main file is not an owned Unix VFS inode")
	}
	id := (*sqlite3.TunixInodeInfo)(unsafe.Pointer(f.FpInode)).FfileId
	return uint64(id.Fdev), uint64(id.Fino), nil
}
