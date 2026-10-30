package networking

import (
	"context"
	"errors"
	"testing"
	"time"
)

type reviewGateway struct {
	prepared, mapped int
	reboot           bool
	observed         Mapping
}

func (g *reviewGateway) Prepare(_ context.Context, m Mapping) (Mapping, error) {
	g.prepared++
	if g.reboot {
		m.EpochGeneration++
	}
	return m, nil
}
func (g *reviewGateway) Map(_ context.Context, m Mapping, lifetime uint32) (Mapping, error) {
	g.mapped++
	g.observed = m
	if lifetime != 0 {
		panic("cleanup attempted an add")
	}
	return m, nil
}
func TestW2I02CleanupRequiresAnOwnedEffect(t *testing.T) {
	g := &reviewGateway{}
	manager := &RemoteManager{mapper: g}
	m := reviewMapping()
	m.MutationPending = false
	if e := manager.cleanupMapping(context.Background(), m, Topology{}); e != nil || g.mapped != 0 {
		t.Fatal("prepare failure became delete authority", e)
	}
	m.MutationPending = true
	if e := manager.cleanupMapping(context.Background(), m, Topology{}); e != nil || g.mapped != 1 || g.observed.Nonce != m.Nonce {
		t.Fatal("ambiguous PCP owner was not retained", e)
	}
}
func TestW2I02NATPMPDeletionRejectsAnotherNetworkAndReusedEpoch(t *testing.T) {
	topology := Topology{Gateway: "192.168.1.1", LocalAddress: "192.168.1.20", Bind: "0.0.0.0:32500", LAN: []string{"192.168.1.20"}}
	m := reviewMapping()
	m.Protocol = "natpmp"
	m.Network = mappingNetwork(topology)
	m.PotentialUntil = time.Now().Add(time.Hour)
	g := &reviewGateway{}
	manager := &RemoteManager{mapper: g}
	foreign := topology
	foreign.LAN = []string{"192.168.1.20", "192.168.10.20"}
	if e := manager.cleanupMapping(context.Background(), m, foreign); !errors.Is(e, ErrUnavailable) || g.prepared != 0 || g.mapped != 0 {
		t.Fatal("foreign tuple deletion", e)
	}
	g.reboot = true
	if e := manager.cleanupMapping(context.Background(), m, topology); e != nil || g.mapped != 0 || g.prepared != 1 {
		t.Fatal("reboot allowed nonce-less deletion", e)
	}
	g.reboot = false
	if e := manager.cleanupMapping(context.Background(), m, topology); e != nil || g.mapped != 1 {
		t.Fatal("current owned tuple not cleaned", e)
	}
	m.Network = ""
	if e := manager.cleanupMapping(context.Background(), m, topology); !errors.Is(e, ErrUnavailable) || g.mapped != 1 {
		t.Fatal("legacy unknown network replayed deletion")
	}
}
