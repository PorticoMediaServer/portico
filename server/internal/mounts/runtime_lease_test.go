package mounts

import (
	"context"
	"testing"
)

func TestMountRuntimeLeaseDeepestAndInvalidation(t *testing.T) {
	outer, inner := newRuntimeChild(1), newRuntimeChild(1)
	outer.mountIdentity, inner.mountIdentity = "outer-filesystem", "inner-filesystem"
	defer outer.invalidateRuntime()
	defer inner.invalidateRuntime()
	s := &Service{paths: map[string]string{"outer": "/owned", "inner": "/owned/nested"}, available: map[string]bool{"outer": true, "inner": true}, active: map[string]*child{"outer": outer, "inner": inner}}
	lease, e := s.LeaseFor("/owned/nested/file")
	if e != nil || lease.ID != inner.runtimeID {
		t.Fatal("wrong physical owner", lease, e)
	}
	inner.invalidateRuntime()
	if lease.Lifetime.Err() != context.Canceled {
		t.Fatal("existing lease not fenced")
	}
	if _, e = s.LeaseFor("/owned/nested/file"); e == nil {
		t.Fatal("fell back to ancestor")
	}
	if s.active["inner"] != inner {
		t.Fatal("logical invalidation released physical slot")
	}
	replacement := newRuntimeChild(1)
	replacement.mountIdentity = "replacement-filesystem"
	defer replacement.invalidateRuntime()
	s.active["inner"] = replacement
	next, e := s.LeaseFor("/owned/nested/file")
	if e != nil || next.ID == lease.ID {
		t.Fatal("runtime reused database generation", e)
	}
	if outside, e := s.LeaseFor("/owned-other/file"); e != nil || outside != nil {
		t.Fatal("prefix admitted managed owner")
	}
}

func TestMountRuntimeLeasePhysicalReplacement(t *testing.T) {
	c := newRuntimeChild(1)
	defer c.invalidateRuntime()
	if !c.bindMountIdentity("filesystem-one") || !c.bindMountIdentity("filesystem-one") {
		t.Fatal("stable ready mount denied")
	}
	s := &Service{paths: map[string]string{"mount": "/owned"}, available: map[string]bool{"mount": true}, active: map[string]*child{"mount": c}}
	lease, e := s.LeaseFor("/owned/file")
	if e != nil || lease.Identity != "filesystem-one" {
		t.Fatal("lease omitted readiness identity", e)
	}
	if c.bindMountIdentity("filesystem-two") {
		t.Fatal("live guardian adopted replaced mount")
	}
	if lease.Lifetime.Err() == nil {
		t.Fatal("physical replacement did not revoke prior lease")
	}
	if _, e = s.LeaseFor("/owned/file"); e == nil {
		t.Fatal("replacement admitted while still logically ready")
	}
}
