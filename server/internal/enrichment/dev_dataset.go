//go:build devtrust && !release

package enrichment

import "os"

// A development build can take the title dataset from a stand-in on this
// machine (PORTICO_DEV_DATASET_LOOPBACK=1): http or https on a loopback
// address, everything else unchanged (the signature, key, sequence, hashes,
// sizes and integrity checks all still apply). Release builds never can:
// this file is excluded from them and devLoopback stays false.
func init() {
	devLoopback = os.Getenv("PORTICO_DEV_DATASET_LOOPBACK") == "1"
}
