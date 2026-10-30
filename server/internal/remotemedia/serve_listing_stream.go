package remotemedia

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// VisitSnapshot stages one complete Depth1 response without retaining its
// children in memory. visit MUST write only private, duplicate-rejecting staging
// data: a callback is not publication. Only successful EOF and the final fence
// permit the owner to publish readiness. Errors leave all staged data private.
// The owner retains the existing 512MiB snapshot budget and process/job fences.
func (s *ServeEndpoint) VisitSnapshot(ctx context.Context, relative string, reconnect func(context.Context) error, visit func(DAVEntry) error) error {
	if reconnect == nil || visit == nil || !cleanDAVRelative(relative) || strings.Contains(relative, ":") {
		return ErrPolicy
	}
	ctx, release, err := s.budget.enter(ctx)
	if err != nil {
		return err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	if err := reconnect(ctx); err != nil {
		return err
	}
	requestContext := ctx
	ctx = context.WithValue(ctx, serveReconnectKey{}, func() error { return reconnect(requestContext) })
	ctx = context.WithValue(ctx, serveListingReadKey{}, true)
	client := s.client
	if transport, ok := s.client.Transport.(*http.Transport); ok {
		// Enumeration can legitimately outlive a worker quantum before headers
		// arrive. Isolate that retained listing from metadata/range deadlines.
		owned := transport.Clone()
		owned.ResponseHeaderTimeout = 0 // The bounded listing context owns this deadline.
		defer owned.CloseIdleConnections()
		copy := *s.client
		copy.Transport = owned
		client = &copy
	}
	target := s.root
	target.Path = strings.TrimSuffix(target.Path+relative, "/") + "/"
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", target.String(), strings.NewReader(serveListingProperties))
	if err != nil {
		return ErrPolicy
	}
	req.Header.Set("Authorization", s.authorization)
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Accept-Encoding", "identity")
	response, err := client.Do(req)
	if err != nil {
		if errors.Is(err, ErrObservationChanged) {
			return ErrObservationChanged
		}
		return boundRequestError(ctx, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusMultiStatus {
		return serveResponseError(response)
	}
	if response.Header.Get("Content-Encoding") != "" {
		return ErrDAVResponse
	}
	d := DAV{config: DAVConfig{Root: s.root.String()}}
	if err = d.visitServedSnapshot(ctx, response.Body, relative, visit); err != nil {
		return err
	}
	return reconnect(ctx)
}

// XML's decoder can otherwise allocate an unbounded text token before a caller
// checks its length. Reset this read budget at each bounded token/response; a
// decoder error is terminal, so exhaustion never resumes a partial document.
type listingStepReader struct {
	reader io.Reader
	left   int
}

func (r *listingStepReader) Read(p []byte) (int, error) {
	if r.left == 0 {
		return 0, ErrDAVResponse
	}
	if len(p) > r.left {
		p = p[:r.left]
	}
	n, err := r.reader.Read(p)
	r.left -= n
	return n, err
}

func (d *DAV) visitServedSnapshot(ctx context.Context, input io.Reader, directory string, visit func(DAVEntry) error) error {
	bounded := &listingStepReader{reader: input}
	tokens := &boundedDAVTokens{decoder: xml.NewDecoder(bounded)}
	dec := xml.NewTokenDecoder(tokens)
	root, closed, self := false, false, false
	children := 0
	var envelopeBytes int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		bounded.left, tokens.tokens = 64<<10, 0
		before := tokens.decoder.InputOffset()
		token, err := dec.Token()
		if err == io.EOF {
			break
		}
		start, response := token.(xml.StartElement)
		if !response || start.Name.Space != "DAV:" || start.Name.Local != "response" {
			envelopeBytes += tokens.decoder.InputOffset() - before
		}
		if err != nil || envelopeBytes > 1<<20+int64(children)*256 {
			return ErrDAVResponse
		}
		switch t := token.(type) {
		case xml.StartElement:
			if !root {
				if t.Name.Space != "DAV:" || t.Name.Local != "multistatus" || tokens.depth != 1 {
					return ErrDAVResponse
				}
				root = true
				continue
			}
			if closed || t.Name.Space != "DAV:" || t.Name.Local != "response" || tokens.depth != 2 {
				return ErrDAVResponse
			}
			bounded.left = 64 << 10
			var wire davResponse
			if dec.DecodeElement(&wire, &t) != nil || tokens.decoder.InputOffset()-before > 64<<10 {
				return ErrDAVResponse
			}
			entry, err := d.servedListingEntry(wire, directory)
			if err != nil {
				return err
			}
			if entry.Relative == directory {
				if self || !entry.Directory {
					return ErrDAVResponse
				}
				self = true
				continue
			}
			children++
			if children > 2_000_000 {
				return ErrDAVResponse
			}
			if err = visit(entry); err != nil {
				return err
			}
		case xml.CharData:
			if strings.TrimSpace(string(t)) != "" {
				return ErrDAVResponse
			}
		case xml.EndElement:
			if t.Name.Space != "DAV:" || t.Name.Local != "multistatus" || !root || closed {
				return ErrDAVResponse
			}
			closed = true
		}
	}
	if !root || !closed || !self {
		return ErrDAVResponse
	}
	return ctx.Err()
}

func (d *DAV) servedListingEntry(wire davResponse, directory string) (DAVEntry, error) {
	relative, err := d.href(wire.Href, directory, false)
	if err != nil {
		return DAVEntry{}, ErrDAVResponse
	}
	if status := davStatus(wire.Status); status != 0 && status != http.StatusOK {
		return DAVEntry{}, ErrUnavailable
	}
	entry := DAVEntry{Relative: relative}
	found, sizeKnown := false, false
	for _, properties := range wire.Propstats {
		status := davStatus(properties.Status)
		if status == http.StatusNotFound {
			continue
		}
		if status != http.StatusOK {
			return DAVEntry{}, ErrUnavailable
		}
		found = true
		p := properties.Prop
		if p.Type.Collection != nil {
			entry.Directory = true
		}
		if p.Size != "" {
			entry.Size, err = strconv.ParseInt(strings.TrimSpace(p.Size), 10, 64)
			if err != nil || entry.Size < 0 {
				return DAVEntry{}, ErrDAVResponse
			}
			sizeKnown = true
		}
		if p.ETag != "" {
			entry.ETag = p.ETag
		}
		if p.Modified != "" {
			entry.Modified = p.Modified
		}
		if p.ContentType != "" {
			entry.ContentType = p.ContentType
		}
	}
	if !found || !entry.Directory && !sizeKnown || len(entry.ETag) > 1024 || len(entry.Modified) > 128 || len(entry.ContentType) > 512 {
		return DAVEntry{}, ErrDAVResponse
	}
	return entry, nil
}
