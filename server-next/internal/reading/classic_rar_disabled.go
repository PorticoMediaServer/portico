//go:build !portico_rar7z

package reading

import "io"

const rarEnabled = false

func rarMembers(io.ReaderAt, int64, string, io.Writer) ([]comicMember, error) {
	return nil, ErrFormatLater
}
