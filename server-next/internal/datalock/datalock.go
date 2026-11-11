// Package datalock keeps two Portico servers from running on one data folder.
//
// A server holds the operating system's own lock on a file in its data folder for as long
// as it runs. The system lets go of it when the process ends, however it ends, so a crash
// never leaves a lock for anyone to clear by hand. Two servers on one folder would each run
// the scans, the search index, the backups and any upgrade as if alone.
package datalock

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// FileName is the lock file's name in the data folder. The file is empty.
const FileName = "portico.lock"

// tryLock takes the system's lock on the open file without waiting. It reports whether
// another process holds it; any other failure means the lock could not be asked for at all.
var tryLock = systemLock

// Hold takes the data folder for this process. release lets it go, as does the end of the
// process.
//
// When another server has the folder, err says so and nothing in the folder was touched.
// When the folder's file system cannot lock at all, as some network shares cannot, the
// server is not kept from starting: the log is told that nothing stops a second one there.
func Hold(folder string, log *slog.Logger) (release func(), err error) {
	// Go opens files so that programs this one starts (a media tool, rclone) do not inherit
	// them, so none of those can keep the lock after the server has gone.
	file, err := os.OpenFile(filepath.Join(folder, FileName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	taken, err := tryLock(file)
	if taken {
		file.Close()
		return nil, fmt.Errorf("another Portico server has the data folder %s open. Two servers cannot share a data folder: "+
			"stop the other one, or give this one a folder of its own. Nothing in the folder was changed", folder)
	}
	if err != nil {
		log.Warn("The data folder is on a file system that cannot lock files, so nothing stops a second Portico server from starting on it. "+
			"Run only one, and keep the data folder on a local disk if you can", "data", folder, "error", err)
	}
	// Closing the file lets the lock go.
	return func() { file.Close() }, nil
}
