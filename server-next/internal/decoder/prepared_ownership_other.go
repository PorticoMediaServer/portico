//go:build !linux

package decoder

import "os"

func (c *PreparedFileOwnership) waiter() (*os.File, error) { return nil, ErrConfinementUnavailable }
func (c *PreparedFileOwnership) retired(*os.File)          {}
