package enrichment

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
)

// datasetError contains only a closed error code, a numeric HTTP status and an
// origin. Provider text, URL paths, userinfo, queries and nested errors never
// reach persisted diagnostics or an error returned by the download boundary.
type datasetError struct {
	code   string
	origin string
	status int
}

func (e *datasetError) Error() string {
	message := "title dataset: " + e.code
	if e.status >= 100 && e.status <= 599 {
		message += " (" + strconv.Itoa(e.status) + ")"
	}
	if e.origin != "" {
		message += " at " + e.origin
	}
	return message
}

func datasetFailure(code, target string) *datasetError {
	origin := ""
	if u, err := url.Parse(target); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && len(u.Host) <= 260 && !strings.ContainsAny(u.Host, "\x00\r\n\\") {
		// Construct a new URL, retaining no credentials, path, query or fragment.
		origin = (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
	}
	return &datasetError{code: code, origin: origin}
}

func datasetDiagnostic(problem error) string {
	if problem == nil {
		return ""
	}
	var failure *datasetError
	if errors.As(problem, &failure) {
		return failure.Error()
	}
	code := "dataset_failed"
	if errors.Is(problem, context.Canceled) {
		code = "canceled"
	} else if errors.Is(problem, context.DeadlineExceeded) {
		code = "deadline_exceeded"
	}
	return datasetFailure(code, "").Error()
}

func datasetURL(target string) (*url.URL, error) {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || len(u.Host) > 260 || strings.ContainsAny(target, "\x00\r\n\\") {
		return nil, datasetFailure("network_policy", target)
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, datasetFailure("network_policy", target)
		}
	}
	return u, nil
}

func datasetOrigin(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "443"
	}
	return net.JoinHostPort(strings.ToLower(strings.TrimSuffix(u.Hostname(), ".")), port)
}

func (s *Service) admitDatasetURL(ctx context.Context, target string) error {
	u, err := datasetURL(target)
	if err != nil {
		return err
	}
	// Only classify the origin. Signed URL paths and queries are opaque to the
	// downloader and do not confer address approval.
	destination, err := (remotemedia.Policy{Resolver: s.resolver}).Classify(ctx, "https://"+u.Host+"/")
	if err != nil || !destination.Public {
		return datasetFailure("network_policy", target)
	}
	return nil
}

func (s *Service) datasetClient(base *http.Client) *http.Client {
	client := &http.Client{Timeout: 10 * time.Minute}
	if base != nil && base.Timeout > 0 {
		client.Timeout = base.Timeout
	}
	// A configured client may choose a timeout, but cannot replace egress policy
	// with a proxy, custom dialer, redirect handler or cookie jar.
	transport := &http.Transport{
		Proxy: nil, DialContext: s.dialDataset, ForceAttemptHTTP2: true,
		MaxConnsPerHost: 2, MaxIdleConns: 2, MaxIdleConnsPerHost: 2,
		IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 30 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second, MaxResponseHeaderBytes: 32 << 10,
	}
	client.Transport = transport
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) == 0 || len(via) >= 5 || datasetOrigin(request.URL) != datasetOrigin(via[0].URL) {
			return datasetFailure("network_policy", request.URL.String())
		}
		if err := s.admitDatasetURL(request.Context(), request.URL.String()); err != nil {
			return err
		}
		// In particular, never copy a signed query into a Referer header.
		for _, name := range []string{"Referer", "Authorization", "Proxy-Authorization", "Cookie"} {
			request.Header.Del(name)
		}
		return nil
	}
	return client
}

func (s *Service) dialDataset(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, remotemedia.ErrPolicy
	}
	origin := "https://" + net.JoinHostPort(host, port) + "/"
	destination, err := (remotemedia.Policy{Resolver: s.resolver}).Classify(ctx, origin)
	if err != nil || !destination.Public || len(destination.Addresses) == 0 {
		return nil, remotemedia.ErrPolicy
	}
	// Validate every DNS answer before trying any, then dial numeric addresses.
	// The HTTP transport retains the original hostname for TLS verification/SNI.
	for _, address := range destination.Addresses {
		connection, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(address, port))
		if err == nil {
			return connection, nil
		}
	}
	return nil, remotemedia.ErrUnavailable
}

func (s *Service) openDatasetURL(ctx context.Context, target string) (*http.Response, error) {
	if err := s.admitDatasetURL(ctx, target); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, datasetFailure("network_policy", target)
	}
	response, err := s.cfg.Client.Do(request)
	if err != nil {
		var failure *datasetError
		if errors.As(err, &failure) {
			return nil, failure
		}
		return nil, datasetFailure("request_failed", target)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		failure := datasetFailure("response_status", target)
		failure.status = response.StatusCode
		return nil, failure
	}
	return response, nil
}

func (s *Service) get(ctx context.Context, target string, limit int64) ([]byte, error) {
	response, err := s.openDatasetURL(ctx, target)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, datasetFailure("response_read", target)
	}
	if int64(len(raw)) > limit {
		return nil, datasetFailure("response_too_large", target)
	}
	return raw, nil
}
