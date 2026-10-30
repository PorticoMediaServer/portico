//go:build linux

package storage

import (
	"encoding/binary"
	"os"
	"strconv"
	"syscall"
)

// Linux directory cookies are captured from each getdents64 record, not from
// File.Seek after ReadDir (which would skip unread entries in Go's buffer).
func inventoryNames(f *os.File, cursor string, limit int) ([]string, string, bool, error) {
	offset, err := parseInventoryOffset(cursor)
	if err != nil {
		return nil, "", false, err
	}
	if _, err = syscall.Seek(int(f.Fd()), offset, 0); err != nil {
		return nil, "", false, err
	}
	names := make([]string, 0, limit)
	buf := make([]byte, 32<<10)
	for {
		n, err := syscall.ReadDirent(int(f.Fd()), buf)
		if err != nil {
			return nil, "", false, err
		}
		if n == 0 {
			return names, "", true, nil
		}
		for pos := 0; pos < n; {
			if n-pos < 19 {
				return nil, "", false, ErrInventoryChanged
			}
			b := buf[pos:n]
			length := int(binary.NativeEndian.Uint16(b[16:18]))
			if length < 20 || length > len(b) {
				return nil, "", false, ErrInventoryChanged
			}
			cookie := int64(binary.NativeEndian.Uint64(b[8:16]))
			if cookie < 0 {
				return nil, "", false, ErrInventoryChanged
			}
			end := 19
			for end < length && b[end] != 0 {
				end++
			}
			name := string(b[19:end])
			pos += length
			if binary.NativeEndian.Uint64(b[:8]) == 0 || name == "." || name == ".." {
				continue
			}
			names = append(names, name)
			offset = cookie
			if len(names) == limit {
				return names, strconv.FormatInt(offset, 10), false, nil
			}
		}
	}
}
