//go:build !linux

package decoder

import "os"

func confinedPreparedFileCommand(sandbox, executable string, input, custody *os.File, args []string, libraries ...string) (*preparedFileCommand, error) {
	return nil, ErrConfinementUnavailable
}
func PreparedToolsDigest(string, ...string) (string, error) { return "", ErrConfinementUnavailable }
