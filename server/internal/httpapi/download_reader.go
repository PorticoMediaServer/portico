package httpapi

import (
	"context"
	"io"
	"sync"
)

// downloadReader preserves ServeContent's seeks while guarding every read,
// including reads made by its multipart range writer. Ownership of source
// passes to this reader; either a failed guard or the handler closes it once.
type downloadReader struct {
	io.ReadSeekCloser
	guard    guardedReadSeeker
	once     sync.Once
	closeErr error
}

func newDownloadReader(ctx context.Context, source io.ReadSeekCloser, full func() error) *downloadReader {
	r := &downloadReader{ReadSeekCloser: source}
	authority := newStreamAuthority(full)
	r.guard = guardedReadSeeker{ReadSeeker: source, check: func() error {
		// Cancellation cannot wait for a cached authority verdict to expire.
		err := ctx.Err()
		if err == nil {
			err = authority.check()
		}
		if err != nil {
			_ = r.Close()
		}
		return err
	}}
	return r
}

func (r *downloadReader) Read(p []byte) (int, error) {
	// Bound the bytes in flight between checks even if a consumer supplies a
	// larger buffer. Revocation during the read discards its returned bytes.
	if len(p) > 32*1024 {
		p = p[:32*1024]
	}
	return r.guard.Read(p)
}

func (r *downloadReader) Close() error {
	r.once.Do(func() { r.closeErr = r.ReadSeekCloser.Close() })
	return r.closeErr
}
