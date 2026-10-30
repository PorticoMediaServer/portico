package httpapi

import (
	"compress/gzip"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// A home document is seventeen rows of up to sixty entries, each carrying an
// overview truncated to a thousand runes — three hundred kilobytes of JSON that
// gzips to about forty. At two hundred viewers that difference is socket time
// and buffer memory on a machine that has neither to spare, and on the bad
// networks this server is expected to serve over it is the difference between a
// page arriving and a page timing out.
//
// Three kinds of response are deliberately left alone:
//
//   - Media bodies and bulk transfers. They are already compressed bytes, so
//     gzip spends CPU to make them very slightly larger, and they are served
//     with byte ranges that a compressed stream cannot express.
//   - Server-sent events. Compressing a stream means buffering it, and an event
//     stream that is buffered is no longer an event stream.
//   - Anything already carrying a Content-Encoding or Cache-Control: no-transform.
//
// The middleware also refuses to compress a response it cannot see the type of
// yet, and it never compresses a 304 — which has no body by definition and whose
// headers must not describe one.

// compressibleLanes are the lanes whose responses are JSON documents. Deciding
// by lane rather than by path keeps this in step with the admission table: a
// route that is classified as a media body is one, whatever it is called.
var uncompressedLanes = map[string]bool{
	laneMediaBody:    true,
	laneBulkTransfer: true,
	laneRealtime:     true,
}

// gzipMinimum is the size below which compression costs more than it saves. A
// few hundred bytes of JSON is one packet either way.
const gzipMinimum = 1024

var gzipWriters = sync.Pool{New: func() any { return gzip.NewWriter(io.Discard) }}

type compressingWriter struct {
	http.ResponseWriter
	gzip    *gzip.Writer
	buffer  []byte
	status  int
	decided bool
	failed  bool
}

func (c *compressingWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

func (c *compressingWriter) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
	// The decision is deferred until the first write, because until then the
	// handler may not have set a Content-Type and the body length is unknown.
	if status == http.StatusNotModified || status == http.StatusNoContent {
		c.decided, c.failed = true, true
		if status == http.StatusNotModified && !responseNoTransform(c.Header()) {
			weakenETag(c.Header())
		}
		c.ResponseWriter.WriteHeader(status)
		return
	}
}

func (c *compressingWriter) Write(p []byte) (int, error) {
	if c.failed {
		return c.ResponseWriter.Write(p)
	}
	if !c.decided {
		c.buffer = append(c.buffer, p...)
		if len(c.buffer) < gzipMinimum {
			return len(p), nil
		}
		c.decide()
		if c.failed {
			pending := c.buffer
			c.buffer = nil
			if _, err := c.ResponseWriter.Write(pending); err != nil {
				return 0, err
			}
			return len(p), nil
		}
		pending := c.buffer
		c.buffer = nil
		if _, err := c.gzip.Write(pending); err != nil {
			return 0, err
		}
		return len(p), nil
	}
	n, err := c.gzip.Write(p)
	if err != nil {
		return n, err
	}
	return len(p), nil
}

// decide commits to compressing or not, and writes the header line.
func (c *compressingWriter) decide() {
	c.decided = true
	header := c.ResponseWriter.Header()
	if responseNoTransform(header) || header.Get("Content-Encoding") != "" || !compressibleType(header.Get("Content-Type")) {
		c.failed = true
		c.flushHeader()
		return
	}
	weakenETag(header)
	header.Set("Content-Encoding", "gzip")
	header.Add("Vary", "Accept-Encoding")
	// The length of the uncompressed body is not the length of what is sent.
	header.Del("Content-Length")
	c.flushHeader()
	writer := gzipWriters.Get().(*gzip.Writer)
	writer.Reset(c.ResponseWriter)
	c.gzip = writer
}

func (c *compressingWriter) flushHeader() {
	status := c.status
	if status == 0 {
		status = http.StatusOK
	}
	c.ResponseWriter.WriteHeader(status)
}

// close finishes the response: a body that never reached the threshold is
// written uncompressed, and a compressed one is flushed and its writer returned
// to the pool.
func (c *compressingWriter) close() {
	if !c.decided {
		c.decided, c.failed = true, true
		c.flushHeader()
		if len(c.buffer) > 0 {
			_, _ = c.ResponseWriter.Write(c.buffer)
			c.buffer = nil
		}
		return
	}
	if c.gzip != nil {
		_ = c.gzip.Close()
		c.gzip.Reset(io.Discard)
		gzipWriters.Put(c.gzip)
		c.gzip = nil
	}
}

// Flush commits the decision before flushing. A handler that writes a short
// chunk and flushes — a progress line, a stream preamble — must reach the socket
// then, not when a later write happens to pass the size threshold.
func (c *compressingWriter) Flush() {
	flusher, ok := c.ResponseWriter.(http.Flusher)
	if !ok {
		return
	}
	if !c.decided {
		c.decide()
		pending := c.buffer
		c.buffer = nil
		if len(pending) > 0 {
			if c.failed {
				_, _ = c.ResponseWriter.Write(pending)
			} else {
				_, _ = c.gzip.Write(pending)
			}
		}
	}
	if c.gzip != nil {
		_ = c.gzip.Flush()
	}
	flusher.Flush()
}

func compressibleType(value string) bool {
	value = strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
	switch {
	case value == "":
		return false
	case value == "text/event-stream":
		// An event stream is the one text type that must never be compressed.
		// Compressing a stream means buffering it, and a buffered event stream is
		// not an event stream — the events arrive when the compressor's window
		// fills rather than when they happen. Lane classification catches the
		// notification stream; this catches the ones on other lanes, and it is the
		// check that will still be right when a new stream is added.
		return false
	case strings.HasPrefix(value, "text/"):
		return true
	case value == "application/json", value == "application/x-mpegurl", value == "application/xml",
		value == "application/javascript", value == "image/svg+xml", value == "application/manifest+json":
		return true
	}
	return false
}

// responseNoTransform recognizes a directive, never a fragment of a quoted
// extension value. Reading uses an exact strong source validator across control
// and byte responses, so transforming either representation would retire it.
func responseNoTransform(header http.Header) bool {
	for _, value := range header.Values("Cache-Control") {
		start, quoted, escaped := 0, false, false
		for i := 0; i <= len(value); i++ {
			if i == len(value) || (value[i] == ',' && !quoted) {
				if strings.EqualFold(strings.TrimSpace(value[start:i]), "no-transform") {
					return true
				}
				start = i + 1
				continue
			}
			if escaped {
				escaped = false
			} else if quoted && value[i] == '\\' {
				escaped = true
			} else if value[i] == '"' {
				quoted = !quoted
			}
		}
	}
	return false
}

func weakenETag(header http.Header) {
	tag := header.Get("ETag")
	if tag != "" && !strings.HasPrefix(tag, "W/") {
		header.Set("ETag", "W/"+tag)
	}
}
func acceptsGzip(r *http.Request) bool {
	explicit, wildcard := false, false
	accepted := false
	for _, value := range strings.Split(strings.Join(r.Header.Values("Accept-Encoding"), ","), ",") {
		parts := strings.Split(value, ";")
		name := strings.ToLower(strings.TrimSpace(parts[0]))
		if name != "gzip" && name != "*" {
			continue
		}
		quality := 1.0
		seenQ := false
		for _, param := range parts[1:] {
			key, v, ok := strings.Cut(strings.TrimSpace(param), "=")
			if !ok || !strings.EqualFold(key, "q") || seenQ {
				quality = 0
				break
			}
			seenQ = true
			q, err := strconv.ParseFloat(v, 64)
			if err != nil || math.IsNaN(q) || q < 0 || q > 1 {
				quality = 0
				break
			}
			quality = q
		}
		if name == "gzip" {
			explicit = true
			accepted = quality > 0
		} else {
			wildcard = quality > 0
		}
	}
	if explicit {
		return accepted
	}
	return wildcard
}
