package remotemedia

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"time"
)

// NewAuthenticated is restricted to one configured DAV origin/root. Secrets are
// headers only and are stripped/reapplied only after a same-origin/root redirect
// has passed the existing DNS/IP policy. No caller can supply an arbitrary proxy.
func NewAuthenticated(ctx context.Context, locator string, policy Policy, username, password string) (*Client, error) {
	c, err := New(ctx, locator, policy)
	if err != nil {
		return nil, err
	}
	root, err := url.Parse(locator)
	if err != nil {
		c.Close()
		return nil, ErrPolicy
	}
	if len(username) > 1024 || len(password) > 8192 {
		c.Close()
		return nil, ErrPolicy
	}
	auth := ""
	if username != "" || password != "" {
		auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
	}
	c.authorization = auth
	check := c.client.CheckRedirect
	c.client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if origin(r.URL) != origin(root) {
			return ErrPolicy
		}
		if err := check(r, via); err != nil {
			return err
		}
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		return nil
	}
	return c, nil
}

// Request allows only read-only DAV/media operations. It returns no URL-bearing
// transport errors. Retry/wait decisions belong to the source owner; repeating
// here could reconnect behind its observation fence or ignore a provider wait.
func (c *Client) Request(ctx context.Context, method string, body []byte, headers http.Header) (*http.Response, error) {
	return c.request(ctx, method, body, headers, false)
}

func (c *Client) request(ctx context.Context, method string, body []byte, headers http.Header, setup bool) (*http.Response, error) {
	switch method {
	case "GET", "HEAD", "PROPFIND", "REPORT", "OPTIONS":
	default:
		return nil, ErrPolicy
	}
	if len(body) > 16<<10 {
		return nil, ErrPolicy
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, ErrPolicy
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", c.userAgent)
	if c.authorization != "" {
		req.Header.Set("Authorization", c.authorization)
	}
	for key, values := range headers {
		switch http.CanonicalHeaderKey(key) {
		case "Depth", "Content-Type", "Range", "If-Match", "If-None-Match":
		default:
			// The public Request contract remains closed. Additional saved leaf
			// headers are used only by the bounded private setup diagnostic.
			if !setup || method != "GET" || !setupHeaderName(http.CanonicalHeaderKey(key)) {
				return nil, ErrPolicy
			}
		}
		if len(values) != 1 || len(values[0]) > 8192 {
			return nil, ErrPolicy
		}
		req.Header.Set(key, values[0])
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, boundRequestError(ctx, err)
	}
	return resp, nil
}

// Credentialed version discovery shares all range/ETag checks with STRM. The
// binding is exact-root/object authority, not equivalence guessed from an ETag.
func DiscoverAuthenticatedVersion(ctx context.Context, locator string, policy Policy, username, password, scope, object string, binding LocatorBinding) (*VersionedClient, error) {
	base, err := NewAuthenticated(ctx, locator, policy, username, password)
	if err != nil {
		return nil, err
	}
	if err = bindLocator(ctx, locator, scope, object, binding); err != nil {
		base.Close()
		return nil, err
	}
	check := base.client.CheckRedirect
	base.client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if err := check(r, via); err != nil {
			return err
		}
		return bindLocator(r.Context(), r.URL.String(), scope, object, binding)
	}
	return discoverVersion(ctx, base, scope, object, binding)
}

func safeResponseError(resp *http.Response) error {
	return responseFailure(resp, time.Now())
}
