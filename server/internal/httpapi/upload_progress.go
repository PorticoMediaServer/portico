package httpapi

import (
	"io"
	"net/http"
	"time"
)

// A large guide may take minutes on a slow uplink. Its body size and two upload
// slots bound resources; a rolling inactivity deadline stops a stalled sender.
// Authorization still precedes reading, in the actual upload handler.
type progressUploadBody struct {
	io.ReadCloser
	controller *http.ResponseController
	idle       time.Duration
}

func (b *progressUploadBody) Read(p []byte) (int, error) {
	_ = b.controller.SetReadDeadline(time.Now().Add(b.idle))
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		_ = b.controller.SetReadDeadline(time.Now().Add(b.idle))
	}
	return n, err
}
func (b *progressUploadBody) clearDeadline() { _ = b.controller.SetReadDeadline(time.Time{}) }
