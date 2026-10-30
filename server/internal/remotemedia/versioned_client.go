package remotemedia

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"regexp"
	"strconv"
)

var ErrRepresentationRange = errors.New("remote source does not provide the pinned representation range")

// VersionedClient is the finite random-access adapter. Its immutable version is
// established before any response is handed to a producer/viewer. Policy still
// validates every connection and redirect through the existing Client transport.
// It currently supports strong ETags; provider-native version APIs require a
// separately qualified adapter rather than trusting arbitrary version strings.
type VersionedClient struct {
	base    *Client
	version mediasource.Version
	binding LocatorBinding
}

// LocatorBinding is a trusted provider/source adapter, never a client capability.
// It must resolve identity independently of a response ETag. Generic STRM sources
// use ExactLocatorBinding; signed URL refresh requires provider evidence that the
// new locator names the same scoped object. A shared host/path alone is not proof.
type LocatorBinding interface {
	ResolveLocator(context.Context, string) (scope, object string, err error)
}

type exactLocatorBinding struct{ locator, scope, object string }

// ExactLocatorBinding captures the full selected resource URI, including query.
// It cannot qualify a different URL or redirect target as the same object.
func ExactLocatorBinding(locator, scope, object string) (LocatorBinding, error) {
	u, err := parseURL(locator)
	if err != nil {
		return nil, err
	}
	if scope == "" || object == "" {
		return nil, mediasource.ErrEvidence
	}
	return exactLocatorBinding{u.String(), scope, object}, nil
}
func (b exactLocatorBinding) ResolveLocator(ctx context.Context, locator string) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	u, err := parseURL(locator)
	if err != nil || u.String() != b.locator {
		return "", "", mediasource.ErrIdentityRequired
	}
	return b.scope, b.object, nil
}

func bindLocator(ctx context.Context, locator, scope, object string, binding LocatorBinding) error {
	if binding == nil {
		return mediasource.ErrIdentityRequired
	}
	actualScope, actualObject, err := binding.ResolveLocator(ctx, locator)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || actualScope == "" || actualObject == "" {
		return mediasource.ErrIdentityRequired
	}
	if actualScope != scope || actualObject != object {
		return mediasource.ErrSourceChanged
	}
	return nil
}

func newBoundClient(ctx context.Context, locator string, policy Policy, scope, object string, binding LocatorBinding) (*Client, error) {
	if binding == nil {
		return nil, mediasource.ErrIdentityRequired
	}
	base, err := New(ctx, locator, policy)
	if err != nil {
		return nil, err
	}
	if err := bindLocator(ctx, locator, scope, object, binding); err != nil {
		base.Close()
		return nil, err
	}
	checkPolicy := base.client.CheckRedirect
	base.client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if err := checkPolicy(r, via); err != nil {
			return err
		}
		return bindLocator(r.Context(), r.URL.String(), scope, object, binding)
	}
	return base, nil
}

func NewVersioned(ctx context.Context, locator string, policy Policy, version mediasource.Version, binding LocatorBinding) (*VersionedClient, error) {
	if version.ID() == "" || version.Evidence().Kind != mediasource.StrongETag {
		return nil, mediasource.ErrIdentityRequired
	}
	e := version.Evidence()
	base, err := newBoundClient(ctx, locator, policy, e.Scope, e.Object, binding)
	if err != nil {
		return nil, err
	}
	return &VersionedClient{base: base, version: version, binding: binding}, nil
}

// DiscoverVersion reads one byte under the existing source policy to acquire a
// strong validator and length. Missing/weak validators or ignored ranges require
// preparation/qualified sequential delivery; no guessed immutable version exists.
// Scope and object are trusted adapter identities independent of expiring URLs.
func DiscoverVersion(ctx context.Context, locator string, policy Policy, scope, object string, binding LocatorBinding) (*VersionedClient, error) {
	base, err := newBoundClient(ctx, locator, policy, scope, object, binding)
	if err != nil {
		return nil, err
	}
	return discoverVersion(ctx, base, scope, object, binding)
}
func discoverVersion(ctx context.Context, base *Client, scope, object string, binding LocatorBinding) (*VersionedClient, error) {
	ok := false
	defer func() {
		if !ok {
			base.Close()
		}
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.url, nil)
	if err != nil {
		return nil, ErrPolicy
	}
	if base.authorization != "" {
		req.Header.Set("Authorization", base.authorization)
	}
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", "Portico/1")
	resp, err := base.client.Do(req)
	if err != nil {
		return nil, boundRequestError(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, ErrDenied
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, ErrUnavailable
	}
	if resp.StatusCode != http.StatusPartialContent || resp.Header.Get("Content-Encoding") != "" {
		return nil, mediasource.ErrIdentityRequired
	}
	start, end, total, err := parseRepresentationRange(resp.Header.Get("Content-Range"))
	if err != nil || start != 0 || end != 0 {
		return nil, ErrRepresentationRange
	}
	if resp.ContentLength >= 0 && resp.ContentLength != 1 {
		return nil, ErrRepresentationRange
	}
	version, err := mediasource.NewVersion(mediasource.Evidence{Kind: mediasource.StrongETag, Scope: scope, Object: object, Revision: resp.Header.Get("ETag"), Size: total})
	if err != nil {
		return nil, mediasource.ErrIdentityRequired
	}
	if err := consumeOneByte(resp.Body); err != nil {
		return nil, err
	}
	ok = true
	return &VersionedClient{base: base, version: version, binding: binding}, nil
}

func (c *VersionedClient) Version() mediasource.Version { return c.version }
func (c *VersionedClient) Close()                       { c.base.Close() }

// RenewLocator validates policy and representation before returning a separate
// adapter. Existing callers/reads keep their old adapter until their owner switches
// and closes it. This cannot mutate a version under an in-flight read.
func (c *VersionedClient) RenewLocator(ctx context.Context, locator string, policy Policy) (*VersionedClient, error) {
	next, err := NewVersioned(ctx, locator, policy, c.version, c.binding)
	if err != nil {
		return nil, err
	}
	resp, err := next.Open(ctx, http.MethodGet, "bytes=0-0")
	if err != nil {
		next.Close()
		return nil, err
	}
	defer resp.Body.Close()
	if err := consumeOneByte(resp.Body); err != nil {
		next.Close()
		return nil, err
	}
	return next, nil
}

func consumeOneByte(body io.Reader) error {
	var bytes [2]byte
	n, err := io.ReadFull(body, bytes[:])
	if n != 1 || err != io.ErrUnexpectedEOF {
		return ErrRepresentationRange
	}
	return nil
}

func (c *VersionedClient) Open(ctx context.Context, method, rangeHeader string) (*http.Response, error) {
	if method != http.MethodGet && method != http.MethodHead {
		return nil, ErrPolicy
	}
	if len(rangeHeader) > 128 || rangeHeader != "" && !validRange(rangeHeader) {
		return nil, ErrRepresentationRange
	}
	// Range only applies to GET. A HEAD request describes the whole selected
	// representation even when a downstream caller supplied a Range field.
	if method == http.MethodHead {
		rangeHeader = ""
	}
	r, err := http.NewRequestWithContext(ctx, method, c.base.url, nil)
	if err != nil {
		return nil, ErrPolicy
	}
	r.Header.Set("Accept-Encoding", "identity")
	r.Header.Set("User-Agent", "Portico/1")
	// If-Match fails closed (412); If-Range would permit a 200 whole new object.
	if c.base.authorization != "" {
		r.Header.Set("Authorization", c.base.authorization)
	}
	r.Header.Set("If-Match", c.version.Evidence().Revision)
	if rangeHeader != "" {
		r.Header.Set("Range", rangeHeader)
	}
	resp, err := c.base.client.Do(r)
	if err != nil {
		return nil, boundRequestError(ctx, err)
	}
	accepted := false
	defer func() {
		if !accepted {
			resp.Body.Close()
		}
	}()
	if resp.StatusCode == http.StatusPreconditionFailed {
		return nil, mediasource.ErrSourceChanged
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, ErrDenied
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		return nil, ErrUnavailable
	}
	evidence := c.version.Evidence()
	if resp.Header.Get("ETag") != evidence.Revision {
		return nil, mediasource.ErrSourceChanged
	}
	if resp.Header.Get("Content-Encoding") != "" {
		return nil, ErrRepresentationRange
	}
	length := evidence.Size
	if rangeHeader != "" {
		start, end, satisfiable := requestedRepresentationRange(rangeHeader, evidence.Size)
		if !satisfiable {
			if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable || resp.Header.Get("Content-Range") != "bytes */"+strconv.FormatInt(evidence.Size, 10) {
				return nil, ErrRepresentationRange
			}
			resp.Body.Close()
			resp.Body = http.NoBody
			resp.ContentLength = 0
			resp.Header.Del("Content-Length")
			accepted = true
			return resp, nil
		}
		if resp.StatusCode != http.StatusPartialContent {
			return nil, ErrRepresentationRange
		}
		actualStart, actualEnd, total, err := parseRepresentationRange(resp.Header.Get("Content-Range"))
		if err != nil {
			return nil, ErrRepresentationRange
		}
		if total != evidence.Size {
			return nil, mediasource.ErrSourceChanged
		}
		if actualStart != start || actualEnd != end {
			return nil, ErrRepresentationRange
		}
		length = end - start + 1
	} else if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Range") != "" {
		return nil, ErrRepresentationRange
	}
	if resp.ContentLength >= 0 && resp.ContentLength != length {
		if resp.StatusCode == http.StatusOK {
			return nil, mediasource.ErrSourceChanged
		}
		return nil, ErrRepresentationRange
	}
	if method == http.MethodHead && resp.ContentLength < 0 {
		return nil, ErrRepresentationRange
	}
	if method == http.MethodGet {
		resp.Body = &exactRepresentationBody{ReadCloser: resp.Body, remaining: length}
	}
	accepted = true
	return resp, nil
}

func boundRequestError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var admission *serveAdmissionFailure
	if errors.As(err, &admission) {
		// Preserve the actual captured authority reason through net/http's
		// wrapper. A changed/revoked source is not an offline provider report.
		return admission.cause
	}
	for _, kind := range []error{ErrPolicy, mediasource.ErrSourceChanged, mediasource.ErrIdentityRequired} {
		if errors.Is(err, kind) {
			return kind
		}
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return ErrStalled
	}
	return ErrUnavailable
}

var representationRangePattern = regexp.MustCompile(`^bytes ([0-9]+)-([0-9]+)/([0-9]+)$`)

func parseRepresentationRange(value string) (start, end, total int64, err error) {
	m := representationRangePattern.FindStringSubmatch(value)
	if m == nil {
		return 0, 0, 0, ErrRepresentationRange
	}
	values := []*int64{&start, &end, &total}
	for i, p := range values {
		*p, err = strconv.ParseInt(m[i+1], 10, 64)
		if err != nil {
			return 0, 0, 0, ErrRepresentationRange
		}
	}
	if start < 0 || end < start || total <= end {
		return 0, 0, 0, ErrRepresentationRange
	}
	return start, end, total, nil
}

// rangeHeader was validated before this function; subtraction never wraps at
// MaxInt64 because every extent is clamped to the pinned finite source size.
func requestedRepresentationRange(value string, size int64) (start, end int64, ok bool) {
	if size <= 0 {
		return 0, 0, false
	}
	m := rangePattern.FindStringSubmatch(value)
	if m == nil {
		return 0, 0, false
	}
	end = size - 1
	if m[1] == "" {
		suffix, _ := strconv.ParseInt(m[2], 10, 64)
		return max(int64(0), size-suffix), end, true
	}
	start, _ = strconv.ParseInt(m[1], 10, 64)
	if start >= size {
		return 0, 0, false
	}
	if m[2] != "" {
		requested, _ := strconv.ParseInt(m[2], 10, 64)
		end = min(end, requested)
	}
	return start, end, true
}

type exactRepresentationBody struct {
	io.ReadCloser
	remaining int64
}

func (b *exactRepresentationBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if b.remaining == 0 {
		var extra [1]byte
		n, err := b.ReadCloser.Read(extra[:])
		if n != 0 {
			return 0, ErrRepresentationRange
		}
		return 0, err
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	if err == io.EOF && b.remaining != 0 {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}
