package worker

import "testing"

func TestBroadcastReachesEverySubscriberAndUnsubscribes(t *testing.T) {
	var b Broadcast
	a, remove := b.Subscribe()
	c, closeC := b.Subscribe()
	defer closeC()
	b.Wake()
	b.Wake()
	for _, s := range []*Signal{a, c} {
		select {
		case <-s.channel():
		default:
			t.Fatal("subscriber missed wake")
		}
		select {
		case <-s.channel():
			t.Fatal("wakes did not coalesce")
		default:
		}
	}
	remove()
	b.Wake()
	select {
	case <-a.channel():
		t.Fatal("removed subscriber woken")
	default:
	}
	select {
	case <-c.channel():
	default:
		t.Fatal("remaining subscriber missed wake")
	}
}
