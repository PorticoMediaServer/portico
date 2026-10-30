package telemetry

import (
	"testing"
	"time"
)

func TestTrafficUsesOnlyObservedBytesAndExpiresSessions(t *testing.T) {
	var traffic Traffic
	now := time.Unix(1000, 0)
	traffic.Add("home", "192.0.2.1", false, 250000, now)
	traffic.Add("away", "198.51.100.4", true, 500000, now)
	home, away := traffic.Sample(now)
	if home.Value != 1 || away.Value != 2 {
		t.Fatal(home, away)
	}
	rate, address := traffic.Rate("away", now)
	if rate != 800 || address != "198.51.100.4" {
		t.Fatal(rate, address)
	}
	home, away = traffic.Sample(now.Add(3 * time.Second))
	if home.Value != 0 || away.Value != 0 {
		t.Fatal("old bytes became current")
	}
	traffic.Sample(now.Add(61 * time.Second))
	if len(traffic.streams) != 0 {
		t.Fatal("ended stream observations leaked")
	}
}
