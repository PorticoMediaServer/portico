package networking

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLiveWANObserver(t *testing.T) {
	if os.Getenv("PORTICO_LIVE_WAN") != "1" {
		t.Skip("set PORTICO_LIVE_WAN=1 to ask real address services")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, server := range defaultSTUNServers {
		ip, e := stunAddress(ctx, server)
		t.Log(server, ip, e)
	}
	for _, service := range defaultAddressServices {
		ip, e := httpAddress(ctx, service)
		t.Log(service, ip, e)
	}
}
