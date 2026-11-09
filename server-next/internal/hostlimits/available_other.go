//go:build !linux

package hostlimits

import "errors"

func AvailableMemory() (available, total int64, err error) {
	return 0, 0, errors.New("memory output is supported only on Linux")
}
