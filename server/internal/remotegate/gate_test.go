package remotegate

import (
	"context"
	"net/netip"
	"testing"
	"time"
)

var (
	homeNet  = netip.MustParsePrefix("192.168.1.0/24")
	proxyNet = netip.MustParsePrefix("192.168.1.10/32")
	homePeer = netip.MustParseAddr("192.168.1.20")
	proxy    = netip.MustParseAddr("192.168.1.10")
	outside  = netip.MustParseAddr("203.0.113.9")
	tailnetP = netip.MustParseAddr("100.101.102.103")
	ownIP    = netip.MustParseAddr("198.51.100.7")
)

func policy(security, access string) *Policy {
	return NewPolicy(Settings{Security: security, Access: access, Home: []netip.Prefix{homeNet}, Proxies: []netip.Prefix{proxyNet},
		Hosts: []string{"https://media.example.com", "Own.Example.org:443"}, Own: func() []netip.Addr { return []netip.Addr{ownIP} }})
}

func TestSocket(t *testing.T) {
	for _, c := range []struct {
		security, access string
		peer             netip.Addr
		tls              bool
		want             Connection
	}{
		{SecurityEnhanced, AccessOn, homePeer, false, Admit},
		{SecurityEnhanced, AccessOn, ownIP, false, Admit},
		{SecurityEnhanced, AccessOn, proxy, false, Admit},
		{SecurityEnhanced, AccessOn, outside, true, Admit},
		{SecurityEnhanced, AccessOn, outside, false, RefuseAfterHost},
		{SecurityStandard, AccessOn, outside, false, Admit},
		{SecurityStandard, AccessOn, outside, true, Admit},
		{SecurityStandard, AccessPrivate, outside, true, Refuse},
		{SecurityEnhanced, AccessOff, outside, true, Refuse},
		{SecurityEnhanced, AccessOff, tailnetP, true, Refuse},
		{SecurityEnhanced, AccessPrivate, tailnetP, false, Admit},
		{SecurityEnhanced, AccessOff, homePeer, false, Admit},
	} {
		if got := policy(c.security, c.access).Socket(c.peer, c.tls); got != c.want {
			t.Errorf("%s/%s %v tls=%v: got %v, want %v", c.security, c.access, c.peer, c.tls, got, c.want)
		}
	}
}

func TestRequest(t *testing.T) {
	type want struct {
		admit  bool
		reason string
	}
	for _, c := range []struct {
		name             string
		security, access string
		client           netip.Addr
		proxied          bool
		serverName       string
		proto            string
		host             string
		want             want
	}{
		{"home, any name, plain", SecurityEnhanced, AccessOn, homePeer, false, "", "", "anything", want{true, ""}},
		{"own TLS name", SecurityEnhanced, AccessOn, outside, false, "x-1.ns.portico.direct", "", "X-1.ns.portico.direct:32500", want{true, ""}},
		{"Host differs from SNI", SecurityEnhanced, AccessOn, outside, false, "x-1.ns.portico.direct", "", "evil.example", want{false, ReasonName}},
		{"Host a listed name over own TLS", SecurityEnhanced, AccessOn, outside, false, "x-1.ns.portico.direct", "", "media.example.com", want{true, ""}},
		{"no SNI (by IP)", SecurityEnhanced, AccessOn, outside, false, "", "", "203.0.113.1", want{false, ReasonHTTP}},
		{"proxy, https, listed", SecurityEnhanced, AccessOn, outside, true, "", "https", "media.example.com", want{true, ""}},
		{"proxy, no proto (nginx), listed", SecurityEnhanced, AccessOn, outside, true, "", "", "media.example.com", want{true, ""}},
		{"proxy, explicit http, listed", SecurityEnhanced, AccessOn, outside, true, "", "HTTP", "media.example.com", want{false, ReasonHTTP}},
		{"proxy, https, unlisted", SecurityEnhanced, AccessOn, outside, true, "", "https", "other.example.com", want{false, ReasonName}},
		{"proxy, no proto, unlisted", SecurityEnhanced, AccessOn, outside, true, "", "", "other.example.com", want{false, ReasonName}},
		{"plain HTTP outside", SecurityEnhanced, AccessOn, outside, false, "", "", "media.example.com", want{false, ReasonHTTP}},
		{"standard plain any", SecurityStandard, AccessOn, outside, false, "", "", "myserver.mydomain.com", want{true, ""}},
		{"standard, closed", SecurityStandard, AccessOff, outside, false, "", "", "media.example.com", want{false, ""}},
		{"private, tailnet client", SecurityEnhanced, AccessPrivate, tailnetP, false, "", "", "box", want{true, ""}},
	} {
		admit, reason := policy(c.security, c.access).Request(c.client, c.proxied, c.serverName, c.proto, c.host)
		if (want{admit, reason}) != c.want {
			t.Errorf("%s: got admit=%v reason=%q, want %+v", c.name, admit, reason, c.want)
		}
	}
}

func TestDefaultsAreSafe(t *testing.T) {
	// Before the settings are installed, the startup policy refuses every
	// outside connection, TLS or not, and still answers loopback.
	g := New()
	p0 := g.Policy()
	if p0 == nil || p0.Access() != AccessOff {
		t.Fatalf("startup policy: %+v", p0)
	}
	if p0.Socket(outside, true) != Refuse || p0.Socket(outside, false) != Refuse {
		t.Fatal("the startup policy admitted an outside connection")
	}
	if admit, _ := p0.Request(outside, false, "own.example.com", "", "own.example.com"); admit {
		t.Fatal("the startup policy admitted an outside request")
	}
	if p0.Socket(netip.MustParseAddr("127.0.0.1"), false) != Admit || p0.Socket(netip.MustParseAddr("::1"), true) != Admit {
		t.Fatal("the startup policy refused loopback")
	}
	g.Record("media.example.com", ReasonHTTP)
	if len(g.Refused.entries) != 0 {
		t.Fatal("recorded before the settings were installed")
	}
	p := NewPolicy(Settings{})
	if p.Security() != SecurityEnhanced || p.Access() != AccessOn {
		t.Fatalf("default %s/%s", p.Security(), p.Access())
	}
	if got := p.Socket(outside, false); got != RefuseAfterHost {
		t.Fatalf("default plain HTTP from outside: %v", got)
	}
	if p := NewPolicy(Settings{Security: "nonsense", Access: "nonsense"}); p.Security() != SecurityEnhanced || p.Access() != AccessOn {
		t.Fatalf("unknown values: %s/%s", p.Security(), p.Access())
	}
}

func TestHostName(t *testing.T) {
	for raw, want := range map[string]string{"https://Media.Example.com:8443/path": "media.example.com", "media.example.com.": "media.example.com", "[2001:db8::1]:443": "2001:db8::1", "": "", "user@host.example:1": "host.example"} {
		if got := HostName(raw); got != want {
			t.Errorf("%q: %q, want %q", raw, got, want)
		}
	}
}

func TestRefusedNoiseAndBudget(t *testing.T) {
	r := NewRefused()
	clock := time.Unix(1_800_000_000, 0)
	r.now = func() time.Time { return clock }
	lookups := map[string]int{}
	r.resolve = func(_ context.Context, name string) ([]netip.Addr, error) {
		lookups[name]++
		if name == "media.mine.example" {
			return []netip.Addr{ownIP}, nil
		}
		return []netip.Addr{netip.MustParseAddr("192.0.2.200")}, nil
	}
	for _, noise := range []string{"203.0.113.9", "localhost", "box", "", "bad_name.example", "[2001:db8::1]"} {
		r.Record(noise, ReasonHTTP)
	}
	if len(r.entries) != 0 {
		t.Fatalf("noise recorded: %v", r.entries)
	}
	r.Record("media.mine.example", ReasonCertificate)
	r.Record("random-abc.scanner.example", ReasonHTTP)
	r.Record("media.mine.example:443", ReasonCertificate)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		n := len(r.lookups)
		r.mu.Unlock()
		if n == 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if lookups["media.mine.example"] != 1 || lookups["random-abc.scanner.example"] != 1 {
		t.Fatalf("one lookup per name: %v", lookups)
	}
	listed := r.List([]netip.Addr{ownIP}, nil)
	if len(listed) != 1 || listed[0].Name != "media.mine.example" || listed[0].Count != 2 {
		t.Fatalf("listed: %+v", listed)
	}
	// Another refusal the same day doesn't look the name up again.
	r.Record("media.mine.example", ReasonCertificate)
	if len(r.queue) != 0 {
		t.Fatal("re-queued within a day")
	}
	// Allowing a name doesn't fix a missing certificate, so it stays listed,
	// with its reason.
	allowed := func(n string) bool { return n == "media.mine.example" }
	if got := r.List([]netip.Addr{ownIP}, allowed); len(got) != 1 || got[0].Reason != ReasonCertificate {
		t.Fatalf("certificate refusal hidden by allowing: %+v", got)
	}
	// A name refused only for not being listed drops out once allowed.
	r.Record("media.mine.example", ReasonName)
	if got := r.List([]netip.Addr{ownIP}, allowed); len(got) != 0 {
		t.Fatalf("allowed name still listed: %+v", got)
	}
}

func TestRefusedBounded(t *testing.T) {
	r := NewRefused()
	r.resolve = func(context.Context, string) ([]netip.Addr, error) { return nil, nil }
	for i := 0; i < refusedMax*3; i++ {
		r.Record("n"+itoa(i)+".flood.example", ReasonHTTP)
	}
	if len(r.entries) > refusedMax || len(r.queue) > lookupQueue {
		t.Fatalf("unbounded: %d entries, %d queued", len(r.entries), len(r.queue))
	}
}

func TestLookupBudget(t *testing.T) {
	r := NewRefused()
	clock := time.Unix(1_800_000_000, 0)
	r.now = func() time.Time { return clock }
	spent := 0
	for i := 0; i < 50; i++ {
		if r.spend() {
			spent++
		}
	}
	if spent != lookupsPerMinute {
		t.Fatalf("spent %d in one minute", spent)
	}
	clock = clock.Add(time.Minute)
	if !r.spend() {
		t.Fatal("budget not renewed after a minute")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for ; i > 0; i /= 10 {
		s = string(rune('0'+i%10)) + s
	}
	return s
}
