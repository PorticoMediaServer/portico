//go:build !release

package networking

import "net/http"

// UseRoundTripper replaces the transport under the Hosted client, so tests in
// other packages can answer Hosted calls without a network. Like the legacy
// policy fixture (A59), it cannot live in a _test file because other packages'
// tests use it; !release keeps it out of every shipped binary.
func (t *HTTPTransport) UseRoundTripper(rt http.RoundTripper) { t.client.Transport = rt }
