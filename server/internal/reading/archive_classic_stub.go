//go:build !portico_rar7z

package reading

import (
	"context"
	"io"
)

// openClassicArchive is absent until Release pins and links the RAR/7z readers
// at a checkpoint; the format is refused rather than recognised.
func openClassicArchive(ctx context.Context, format string, ra io.ReaderAt, size int64) (memberArchive, error) {
	return nil, archiveFormatError(format)
}
