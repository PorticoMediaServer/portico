//go:build !unix

package api

import "os"

// canWrite reports whether the server may make files in a folder. Here it is found out by
// making one and taking it away again.
func canWrite(folder string) bool {
	f, err := os.CreateTemp(folder, ".portico-write-check-")
	if err != nil {
		return false
	}
	f.Close()
	return os.Remove(f.Name()) == nil
}
