//go:build !unix

package mounts

import "os"

// openConfigFile refuses on platforms without a non-blocking open: a named pipe
// or device on the fixed path could block before any descriptor check. The
// account-configuration read is therefore unavailable there rather than
// pretending to be bounded.
func openConfigFile(string) (*os.File, error) {
	return nil, ErrConfigInvalid
}
