package remotemedia

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func privateSetupServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	if host := os.Getenv("PORTICO_PRIVATE_ORIGIN_TEST_HOST"); host != "" {
		ip, err := netip.ParseAddr(host)
		if err != nil || !ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			t.Fatal("invalid owned private fixture endpoint")
		}
		listener, err := net.Listen("tcp4", net.JoinHostPort(ip.String(), "0"))
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewUnstartedServer(handler)
		server.Listener.Close()
		server.Listener = listener
		server.Start()
		t.Cleanup(server.Close)
		return server
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	var listener net.Listener
	for _, address := range addresses {
		prefix, err := netip.ParsePrefix(address.String())
		if err != nil {
			continue
		}
		ip := prefix.Addr().Unmap()
		if !ip.Is4() || !nativePrivate(ip) || !nativeAddressAllowed(ip) {
			continue
		}
		listener, err = net.Listen("tcp4", net.JoinHostPort(ip.String(), "0"))
		if err == nil {
			break
		}
	}
	if listener == nil {
		t.Fatal("an existing permitted private IPv4 fixture interface is required")
	}
	server := httptest.NewUnstartedServer(handler)
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return server
}

func TestSourceDAVSetupRangeFactsNeverInferSupport(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, mode := range []string{"supported", "ignored", "wrong", "wrong_start", "wrong_end", "bad_header", "short", "long", "timeout", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			budget := NewOperationBudget(context.Background(), 1)
			hold, err := budget.AcquireTest(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer hold.Close()
			calls := 0
			server := privateSetupServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if hold.Operations().Counts().Active != 1 {
					t.Error("diagnostic did not retain one source-budget slot")
				}
				user, password, ok := r.BasicAuth()
				if !ok || user != "fixture" || password != "fixture-only" {
					t.Error("saved credentials missing")
					w.WriteHeader(401)
					return
				}
				if r.Method != "GET" || r.URL.Path != "/one.bin" {
					t.Error("not an already-listed file GET")
					w.WriteHeader(400)
					return
				}
				var start, end int64
				if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil || end < start || end-start+1 > 16 {
					t.Error("unbounded diagnostic range")
					w.WriteHeader(400)
					return
				}
				length := end - start + 1
				switch mode {
				case "timeout":
					<-r.Context().Done()
					return
				case "redirect":
					w.Header().Set("Location", "/unlisted")
					w.WriteHeader(302)
					return
				case "ignored":
					w.Header().Set("Content-Length", "1048576")
					w.WriteHeader(200)
					return
				}
				total := int64(64)
				if mode == "wrong" {
					total++
				}
				rangeStart, rangeEnd := start, end
				if mode == "wrong_start" {
					rangeStart++
				}
				if mode == "wrong_end" {
					rangeEnd--
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", rangeStart, rangeEnd, total))
				if mode == "bad_header" {
					w.Header().Set("Content-Range", "invalid")
				}
				if mode != "long" {
					w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
				}
				w.WriteHeader(206)
				if mode == "long" {
					w.(http.Flusher).Flush()
					length++
				}
				if mode == "short" {
					length--
				}
				_, _ = w.Write([]byte(strings.Repeat("x", int(length))))
			}))
			config, err := ValidateSourceDAVConfig(context.Background(), DAVConfig{Root: server.URL + "/", Username: "fixture", Password: "fixture-only", InsecureLocal: true}, nil)
			if err != nil {
				t.Fatal(err)
			}
			dav, err := NewSourceDAV(config, nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx := hold.Context()
			cancel := func() {}
			if mode == "timeout" {
				ctx, cancel = context.WithTimeout(ctx, 1500*time.Millisecond)
			}
			defer cancel()
			support, err := dav.DiagnoseSetupRanges(ctx, DAVEntry{Relative: "one.bin", Size: 64}, hold, 50, 0, func(ctx context.Context) error { return ctx.Err() })
			switch mode {
			case "supported":
				if err != nil || support != SetupRangeSupported || calls != 3 {
					t.Fatal(support, calls, err)
				}
			case "timeout", "redirect":
				if err == nil || support != SetupRangeUnknown || calls != 1 {
					t.Fatal(support, calls, err)
				}
			default:
				if err != nil || support != SetupRangeUnsupported || calls != 1 {
					t.Fatal(support, calls, err)
				}
			}
			if hold.Operations().Counts().Active != 0 {
				t.Fatal("diagnostic leaked capacity")
			}
		})
	}
}

func TestSourceDAVSetupDiagnosticPacesRequestsAndBytes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	hold, err := NewOperationBudget(context.Background(), 1).AcquireTest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	var mu sync.Mutex
	var calls []time.Time
	server := privateSetupServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, time.Now())
		mu.Unlock()
		var start, end int64
		fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end)
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/64", start, end))
		w.WriteHeader(206)
		_, _ = w.Write([]byte(strings.Repeat("x", int(end-start+1))))
	}))
	config, err := ValidateSourceDAVConfig(context.Background(), DAVConfig{Root: server.URL + "/", InsecureLocal: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dav, err := NewSourceDAV(config, nil)
	if err != nil {
		t.Fatal(err)
	}
	// This resource-only low-BPS fixture makes pacing observable without a large
	// payload. Production receives only the actual normalized saved profile.
	start := time.Now()
	support, err := dav.DiagnoseSetupRanges(hold.Context(), DAVEntry{Relative: "one.bin", Size: 64}, hold, 2, 32, func(ctx context.Context) error { return ctx.Err() })
	if err != nil || support != SetupRangeSupported {
		t.Fatal(support, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 3 || calls[0].Sub(start) < time.Second || calls[1].Sub(calls[0]) < 500*time.Millisecond || calls[2].Sub(calls[1]) < 500*time.Millisecond || time.Since(start) < time.Second+time.Duration(51*int64(time.Second)/32) {
		t.Fatal("saved TPS/BPS not applied", calls, time.Since(start))
	}
}

func TestSourceDAVSetupNeverListAndNoGenericDescriptorGrant(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "169.254.169.254", "169.254.1.2", "100.100.100.200", "192.0.2.1"} {
		if _, err := ValidateSourceDAVConfig(context.Background(), DAVConfig{Root: "http://" + host + "/", InsecureLocal: true}, nil); err == nil {
			t.Fatal("forbidden setup origin admitted", host)
		}
	}
	dav := &DAV{policy: Policy{}}
	if support, err := dav.DiagnoseSetupRanges(context.Background(), DAVEntry{Relative: "one.bin", Size: 64}, nil, 2, 0, func(ctx context.Context) error { return ctx.Err() }); err == nil || support != SetupRangeUnknown {
		t.Fatal("generic descriptor became setup authority", support, err)
	}
}

func TestSourceDAVSetupReresolvesBeforeEveryOriginRequest(t *testing.T) {
	hold, err := NewOperationBudget(context.Background(), 1).AcquireTest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	var resolverMu sync.Mutex
	resolver := setupChangingResolver{mu: &resolverMu}
	calls := 0
	server := privateSetupServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var start, end int64
		fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/64", start, end))
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.WriteHeader(206)
		_, _ = w.Write([]byte(strings.Repeat("x", int(end-start+1))))
		resolverMu.Lock()
		resolver.address = netip.MustParseAddr("169.254.169.254")
		resolverMu.Unlock()
	}))
	host, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	resolver.address = netip.MustParseAddr(host)
	config, err := ValidateSourceDAVConfig(context.Background(), DAVConfig{Root: "http://source.fixture:" + port + "/", InsecureLocal: true}, &resolver)
	if err != nil {
		t.Fatal(err)
	}
	dav, err := NewSourceDAV(config, &resolver)
	if err != nil {
		t.Fatal(err)
	}
	support, err := dav.DiagnoseSetupRanges(hold.Context(), DAVEntry{Relative: "one.bin", Size: 64}, hold, 50, 0, func(ctx context.Context) error { return ctx.Err() })
	if !errors.Is(err, ErrPolicy) || support != SetupRangeUnknown || calls != 1 || hold.Operations().Counts().Active != 0 {
		t.Fatal("rebound origin reused its approval", support, calls, err)
	}
}

type setupChangingResolver struct {
	mu      *sync.Mutex
	address netip.Addr
}

func (r *setupChangingResolver) LookupNetIP(ctx context.Context, _, _ string) ([]netip.Addr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return []netip.Addr{r.address}, ctx.Err()
}

func TestSourceDAVSetupSharesThreeRequestsAcrossLeafDiagnostics(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, firstSize := range []int64{64, 1} {
		t.Run(strconv.FormatInt(firstSize, 10), func(t *testing.T) {
			budget := NewOperationBudget(context.Background(), 1)
			hold, err := budget.AcquireTest(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer hold.Close()
			calls := 0
			server := privateSetupServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var start, end int64
				if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
					t.Error(err)
					return
				}
				total := int64(64)
				if r.URL.Path == "/first.bin" {
					total = firstSize
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, total))
				w.WriteHeader(206)
				_, _ = w.Write([]byte(strings.Repeat("x", int(end-start+1))))
			}))
			config, err := ValidateSourceDAVConfig(context.Background(), DAVConfig{Root: server.URL + "/", InsecureLocal: true}, nil)
			if err != nil {
				t.Fatal(err)
			}
			dav, err := NewSourceDAV(config, nil)
			if err != nil {
				t.Fatal(err)
			}
			check := func(ctx context.Context) error { return ctx.Err() }
			support, err := dav.DiagnoseSetupRanges(hold.Context(), DAVEntry{Relative: "first.bin", Size: firstSize}, hold, 50, 0, check)
			if err != nil || support != SetupRangeSupported {
				t.Fatal(support, err)
			}
			support, err = dav.DiagnoseSetupRanges(hold.Context(), DAVEntry{Relative: "second.bin", Size: 64}, hold, 50, 0, check)
			if err != nil || support != SetupRangeUnknown || calls != 3 {
				t.Fatal("aggregate allowance escaped", support, calls, err)
			}
		})
	}
}
