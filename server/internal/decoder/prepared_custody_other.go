//go:build !linux

package decoder

import "os"

func (c *PreparedFileCustody) waiter() (*os.File, error) { return nil, ErrConfinementUnavailable }
func (c *PreparedFileCustody) retired(*os.File)          {}
