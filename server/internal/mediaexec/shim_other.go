//go:build !linux && !darwin

package mediaexec

// Windows (and any other platform) runs media tools directly: there is no exec
// to replace a process with, and on Windows the server's job object ends every
// child with the server, so there is nothing for a ledger to reap.
func execShim(shimSpec, []string) error { return ErrUnsupported }
func execInPlace([]string) error        { return ErrUnsupported }
func reapLedger(string, string, bool) (int, error) {
	return 0, nil
}
