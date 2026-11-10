//go:build !unix

package scan

import "io/fs"

// folderID names the folder itself, however it was reached. Here it cannot be told.
func folderID(fs.FileInfo) string { return "" }
