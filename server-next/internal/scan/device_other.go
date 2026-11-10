//go:build !unix

package scan

import "io/fs"

// device is the number of the disk a file or folder is on. Here it cannot be told.
func device(fs.FileInfo) int64 { return 0 }
