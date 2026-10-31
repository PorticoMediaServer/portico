//go:build devtrust && !release

package main

import "testing"

func TestDevelopmentTrustedProxiesAreLoopbackOnly(t *testing.T) {
	for raw, want := range map[string]int{"": 0, "127.0.0.1/32": 1, "127.0.0.0/8": 1, "::1/128": 1, "127.0.0.1/32, ::1/128": 2} {
		got, err := developmentTrustedProxies(raw)
		if err != nil || len(got) != want {
			t.Fatalf("%q: %v, %v", raw, got, err)
		}
	}
	for _, raw := range []string{"10.0.0.0/8", "0.0.0.0/0", "127.0.0.0/7", "::/0", "::1/127", "::ffff:127.0.0.1/128", "192.168.1.2/32", "127.0.0.1", "garbage", "127.0.0.1/32,10.0.0.1/32"} {
		if got, err := developmentTrustedProxies(raw); err == nil {
			t.Fatalf("%q accepted as %v", raw, got)
		}
	}
}
