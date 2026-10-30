package httpapi

import (
	"fmt"
	"testing"
	"time"
)

func TestPersonalRateIsProfileScopedAndBounded(t *testing.T) {
	l := &personalLimiter{}
	now := time.Now()
	for n := 0; n < 120; n++ {
		if !l.allowAt("profile", now) {
			t.Fatal("early rate rejection", n)
		}
	}
	if l.allowAt("profile", now) {
		t.Fatal("rate limit missing")
	}
	if !l.allowAt("other-profile", now) {
		t.Fatal("profile limit leaked")
	}
	if !l.allowAt("profile", now.Add(time.Minute)) {
		t.Fatal("window failed to renew")
	}
	bounded := &personalLimiter{}
	for n := 0; n < 1024; n++ {
		if !bounded.allowAt(fmt.Sprint(n), now) {
			t.Fatal(n)
		}
	}
	if bounded.allowAt("overflow", now) {
		t.Fatal("unbounded limiter memory")
	}
	if !bounded.allowAt("fresh", now.Add(time.Minute)) {
		t.Fatal("expired limiter entries not reclaimed")
	}
}
