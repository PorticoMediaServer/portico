package telemetry

import (
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// writable answers whether this server can create a file in path, creating the
// directory if it is missing. A permissions check on the directory entry would
// not catch a read-only mount or a full volume, which are the failures owners
// actually hit.
func writable(path string) bool {
	if path == "" {
		return false
	}
	if e := os.MkdirAll(path, 0o700); e != nil {
		return false
	}
	probe := filepath.Join(path, ".portico-write-probe-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	f, e := os.OpenFile(probe, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if e != nil {
		return false
	}
	f.Close()
	os.Remove(probe)
	return true
}
