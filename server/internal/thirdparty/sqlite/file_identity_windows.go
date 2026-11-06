package sqlite

import "fmt"

// No Unix inode layout is inferred for the Windows VFS. The new scan opening
// refuses until its exact native handle/file-key mapping is implemented.
func (c *conn) SQLiteMainFileIdentity() (device, inode uint64, err error) {
	return 0, 0, fmt.Errorf("sqlite: native file identity is unavailable for this VFS")
}
