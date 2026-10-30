package remotemedia

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

const serveListingProperties = `<d:propfind xmlns:d="DAV:"><d:prop><d:resourcetype/><d:getetag/><d:getcontentlength/><d:getlastmodified/></d:prop></d:propfind>`

// ListSnapshot reads one complete Depth1 response, capped at 4 MiB by the DAV
// parser. It neither requests allprop nor pretends rclone supports sync tokens.
// Nothing is returned after a truncated, over-bound or invalid response. The
// source owner stages this result privately before keyset pages become visible.
func (s *ServeEndpoint) ListSnapshot(ctx context.Context, relative string, reconnect func(context.Context) error) (DAVPage, error) {
	return s.requestSnapshot(ctx, relative, "1", reconnect)
}

// Stat reads only the captured member through rclone's admitted endpoint. Its
// ETag is served metadata; it must never be stored as a provider ETag/hash or
// described as a guaranteed fresh atomic origin revision.
func (s *ServeEndpoint) Stat(ctx context.Context, relative string, reconnect func(context.Context) error) (DAVEntry, error) {
	page, err := s.requestSnapshot(ctx, relative, "0", reconnect)
	if err != nil {
		return DAVEntry{}, err
	}
	if len(page.Entries) != 1 || page.Entries[0].Relative != relative {
		return DAVEntry{}, ErrDAVResponse
	}
	return page.Entries[0], nil
}

func (s *ServeEndpoint) requestSnapshot(ctx context.Context, relative, depth string, reconnect func(context.Context) error) (DAVPage, error) {
	if reconnect == nil {
		return DAVPage{}, ErrPolicy
	}
	ctx, release, err := s.budget.enter(ctx)
	if err != nil {
		return DAVPage{}, err
	}
	defer release()
	if err := reconnect(ctx); err != nil {
		return DAVPage{}, err
	}
	requestContext := ctx
	ctx = context.WithValue(ctx, serveReconnectKey{}, func() error { return reconnect(requestContext) })
	if !cleanDAVRelative(relative) || strings.Contains(relative, ":") {
		return DAVPage{}, ErrPolicy
	}
	target := s.root
	target.Path += relative
	if depth == "1" {
		target.Path = strings.TrimSuffix(target.Path, "/") + "/"
	}
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", target.String(), strings.NewReader(serveListingProperties))
	if err != nil {
		return DAVPage{}, ErrPolicy
	}
	req.Header.Set("Authorization", s.authorization)
	req.Header.Set("Depth", depth)
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := s.client.Do(req)
	if err != nil {
		if errors.Is(err, ErrObservationChanged) {
			return DAVPage{}, ErrObservationChanged
		}
		return DAVPage{}, boundRequestError(ctx, err)
	}
	defer resp.Body.Close()
	if depth == "0" && resp.StatusCode == http.StatusNotFound {
		return DAVPage{}, ErrObservationChanged
	}
	if resp.StatusCode != http.StatusMultiStatus {
		return DAVPage{}, serveResponseError(resp)
	}
	if resp.Header.Get("Content-Encoding") != "" {
		return DAVPage{}, ErrDAVResponse
	}
	d := DAV{config: DAVConfig{Root: s.root.String()}}
	page, err := d.parseResponse(resp.Body, relative, false, depth == "0")
	if err != nil {
		return DAVPage{}, err
	}
	if err = reconnect(ctx); err != nil {
		return DAVPage{}, err
	}
	return page, nil
}

// ServedFileObservation constructs exactly the evidence returned by Stat. A
// missing/invalid modified time stays unknown. No provider validator or object
// ID is copied from old scan evidence to make the served result look stronger.
func ServedFileObservation(source, incarnation, root, object string, entry DAVEntry) (FileObservation, error) {
	expected := object
	if root != "" {
		expected = root + "/" + object
	}
	if entry.Relative != expected || entry.Directory || entry.Deleted {
		return FileObservation{}, ErrObservation
	}
	o := FileObservation{SourceID: source, Incarnation: incarnation, Root: root, Object: object, Size: entry.Size, ServeETag: entry.ETag}
	if modified, err := http.ParseTime(entry.Modified); err == nil && time.Unix(0, modified.UnixNano()).Equal(modified) {
		o.ModifiedKnown = true
		o.ModifiedNS = modified.UnixNano()
	}
	return o.Canonical()
}
