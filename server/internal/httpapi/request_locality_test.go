package httpapi

import (
	"context"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"time"

	"net/netip"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"testing"
)

func TestRemoteClientLocality(t *testing.T) {
	settings := operations.DefaultSettings()
	settings.LANNetworks = []string{"127.0.0.0/8", "192.168.1.0/24"}
	proxy := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("10.0.0.2/32")}
	guestLAN := localityPolicy{lan: []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}}
	public := []netip.Addr{netip.MustParseAddr("203.0.113.9")}
	cases := []struct {
		name      string
		peer      string
		forwarded []string
		proxies   []netip.Prefix
		policy    localityPolicy
		public    []netip.Addr
		remote    bool
	}{
		{"configured private peer is LAN", "192.168.1.20:5000", nil, nil, newLocalityPolicy(settings), nil, false},
		{"loopback peer is LAN by default", "127.0.0.1:5000", nil, nil, newLocalityPolicy(settings), nil, false},
		{"overlay remains remote", "100.64.1.20:5000", nil, nil, newLocalityPolicy(settings), nil, true},
		{"explicit owner overlay is local", "100.64.1.20:5000", nil, nil, localityPolicy{lan: []netip.Prefix{netip.MustParsePrefix("100.64.0.0/10")}}, nil, false},
		{"public peer is remote", "198.51.100.7:5000", nil, nil, newLocalityPolicy(settings), nil, true},
		{"unparsable peer is remote", "garbage", nil, nil, newLocalityPolicy(settings), nil, true},
		{"untrusted peer's forwarded header is ignored", "198.51.100.7:5000", []string{"192.168.1.20"}, nil, newLocalityPolicy(settings), nil, true},
		{"local proxy forwarding an internet client is remote", "127.0.0.1:5000", []string{"198.51.100.7"}, proxy, newLocalityPolicy(settings), nil, true},
		{"local proxy forwarding a LAN client is LAN", "127.0.0.1:5000", []string{"192.168.1.20"}, proxy, newLocalityPolicy(settings), nil, false},
		{"trusted proxy without a header is remote", "127.0.0.1:5000", nil, proxy, newLocalityPolicy(settings), nil, true},
		{"trusted proxy with a malformed header is remote", "127.0.0.1:5000", []string{"not-an-ip"}, proxy, newLocalityPolicy(settings), nil, true},
		{"spoofed left-most hop is ignored", "127.0.0.1:5000", []string{"192.168.1.20, 198.51.100.7"}, proxy, newLocalityPolicy(settings), nil, true},
		{"chained trusted proxies are skipped", "127.0.0.1:5000", []string{"198.51.100.7, 10.0.0.2"}, proxy, newLocalityPolicy(settings), nil, true},
		{"chained trusted proxies reach a LAN client", "127.0.0.1:5000", []string{"192.168.1.20", "10.0.0.2"}, proxy, newLocalityPolicy(settings), nil, false},
		{"configured LAN is authoritative for other private ranges", "192.168.50.4:5000", nil, nil, guestLAN, nil, true},
		{"configured LAN is authoritative for loopback", "127.0.0.1:5000", nil, nil, guestLAN, nil, true},
		{"configured LAN includes its members", "192.168.1.20:5000", nil, nil, guestLAN, nil, false},
		{"own public address is LAN when treated so", "203.0.113.9:5000", nil, nil, localityPolicy{lan: []netip.Prefix{netip.MustParsePrefix("203.0.113.9/32")}}, public, false},
		{"own public address is remote when not treated so", "203.0.113.9:5000", nil, nil, newLocalityPolicy(settings), public, true},
		{"own public address beats a configured LAN", "203.0.113.9:5000", nil, nil, guestLAN, public, true},
		{"IPv4-mapped peer is unmapped", "[::ffff:192.168.1.20]:5000", nil, nil, newLocalityPolicy(settings), nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/v1/me", nil)
			r.RemoteAddr = c.peer
			for _, v := range c.forwarded {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := remoteClient(r, c.proxies, c.policy); got != c.remote {
				t.Fatalf("remote = %v, want %v", got, c.remote)
			}
		})
	}
}

func TestUploadCeiling(t *testing.T) {
	cases := []struct {
		capacity  int
		used      int64
		ceiling   int
		exhausted bool
	}{
		{20000, 0, (16000 - 192) * 1000, false},
		{20000, 8000, (8000 - 192) * 1000, false},
		{20000, 15000, uploadFloorKbps * 1000, true},
		{5000, 0, (4000 - 192) * 1000, false},
		{1000, 0, uploadFloorKbps * 1000, true},
	}
	for _, c := range cases {
		got, full, err := uploadCeiling(c.capacity, c.used)
		if err != nil || got != c.ceiling || full != c.exhausted {
			t.Fatalf("uploadCeiling(%d, %d) = %d, %v, %v; want %d, %v", c.capacity, c.used, got, full, err, c.ceiling, c.exhausted)
		}
	}
	if narrowBitrate(0, 5) != 5 || narrowBitrate(3, 5) != 3 || narrowBitrate(7, 5) != 5 || narrowBitrate(7, 0) != 7 {
		t.Fatal("narrowBitrate")
	}
}

func TestUploadBudgetCountsActiveRemoteSessions(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "budget.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.UnixMilli(1_000_000_000)
	insert := func(id, location string, bitrate int, lease, ended int64) {
		presentation := fmt.Sprintf(`{"generation":1,"mode":"transcode","bitrateKbps":%d}`, bitrate)
		if _, err := db.Exec(`INSERT INTO playback_v1_sessions(id,device_id,account_id,profile_id,authority,start_key,start_digest,kind,role,state,request,revision,generation,presentation,location,lease_expires_ms,created_ms,updated_ms,ended_ms)
			VALUES(?,?,'a','p','local',?,'d','movie','viewer','playing','{}',1,1,?,?,?,1,1,?)`, id, "dev-"+id, "key-"+id, presentation, location, lease, ended); err != nil {
			t.Fatal(err)
		}
	}
	live := now.UnixMilli() + 60_000
	insert("remote-a", "remote", 4000, live, 0)
	insert("cellular-b", "cellular", 2000, live, 0)
	insert("local-c", "local", 30000, live, 0)
	insert("ended-d", "remote", 9000, live, now.UnixMilli())
	insert("lapsed-e", "remote", 9000, now.UnixMilli()-1, 0)
	insert("replanned-f", "remote", 3000, live, 0)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// 20000 * 80% = 16000; used 4000 + 2000 (f is the stream being re-planned).
	ceiling, full, err := uploadBudgetCeiling(context.Background(), tx, 20000, "replanned-f", now)
	if err != nil || full || ceiling != (16000-6000-uploadAudioAllowanceKbps)*1000 {
		t.Fatalf("ceiling = %d, %v, %v", ceiling, full, err)
	}
	ceiling, full, err = uploadBudgetCeiling(context.Background(), tx, 10000, "", now)
	if err != nil || !full || ceiling != uploadFloorKbps*1000 {
		t.Fatalf("exhausted ceiling = %d, %v, %v", ceiling, full, err)
	}
}
