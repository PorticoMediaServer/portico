package playback

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// Retained range reads replace only STRM's unconditioned byte transport. Source
// policy and credentials remain in the existing adapter; this response does not
// forward an origin URL or an origin header to the player.
type subtitleSourceBody struct {
	ctx           context.Context
	input         subtitles.RenderInput
	position, end int64
}

func (b *subtitleSourceBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if b.position >= b.end {
		return 0, io.EOF
	}
	n := min(int64(len(p)), b.end-b.position, int64(256<<10))
	raw, e := b.input.ReadExtent(b.ctx, b.position, n)
	if e != nil {
		return 0, e
	}
	b.position += int64(len(raw))
	return copy(p, raw), nil
}
func (b *subtitleSourceBody) Close() error { return nil } // request completion owns the acquisition
func subtitleSourceResponse(ctx context.Context, input subtitles.RenderInput, method, rangeHeader string) (*http.Response, error) {
	if method != http.MethodGet && method != http.MethodHead {
		return nil, subtitles.ErrInput
	}
	size := input.Size()
	start, end := int64(0), size
	status := 200
	if rangeHeader != "" {
		if !strings.HasPrefix(rangeHeader, "bytes=") || strings.Contains(rangeHeader, ",") {
			return nil, subtitles.ErrInput
		}
		a, b, ok := strings.Cut(strings.TrimPrefix(rangeHeader, "bytes="), "-")
		if !ok {
			return nil, subtitles.ErrInput
		}
		var e error
		if a == "" {
			n, e := strconv.ParseInt(b, 10, 64)
			if e != nil || n <= 0 {
				return nil, subtitles.ErrInput
			}
			start = max(int64(0), size-n)
		} else {
			start, e = strconv.ParseInt(a, 10, 64)
			if e != nil {
				return nil, e
			}
			if b != "" {
				last, e := strconv.ParseInt(b, 10, 64)
				if e != nil || last < 0 || last >= size {
					return nil, subtitles.ErrInput
				}
				end = last + 1
			}
		}
		if start < 0 || start >= end || end > size {
			return nil, subtitles.ErrInput
		}
		status = 206
	}
	header := http.Header{"Content-Type": []string{"video/mp4"}, "Accept-Ranges": []string{"bytes"}, "Content-Length": []string{strconv.FormatInt(end-start, 10)}}
	if status == 206 {
		header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end-1, size))
	}
	var body io.ReadCloser = &subtitleSourceBody{ctx, input, start, end}
	if method == http.MethodHead {
		body = io.NopCloser(strings.NewReader(""))
	}
	return &http.Response{StatusCode: status, Header: header, ContentLength: end - start, Body: body}, nil
}
