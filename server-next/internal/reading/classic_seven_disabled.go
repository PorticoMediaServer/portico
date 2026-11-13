//go:build !portico_cb7

package reading

import "io"

const sevenEnabled = false

func sevenMembers(io.ReaderAt, int64, string, io.Writer) ([]comicMember, error) {
	return nil, ErrFormatLater
}
