//go:build !linux

package storage

import "os"

// Production keeps this stream in one supervised helper across pages. A local
// caller gets the same retained continuation, invalid after process loss/TTL.
func inventoryNames(f *os.File, cursor string, limit int) ([]string, string, bool, error) {
	return retainedInventoryNames(f, cursor, limit)
}
