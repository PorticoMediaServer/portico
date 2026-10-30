package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// The two properties that matter are opposites, so they are asserted together:
// a lone client must never be held back, and a greedy one must never be able to
// take a lane away from anybody else.

func TestALoneClientMayFillAWholeLaneHoweverBusyItMakesIt(t *testing.T) {
	l := newLane("test", laneSpec{capacity: 8})
	l.enter("one")
	defer l.leave("one")
	for i := 0; i < 8; i++ {
		admitted, overShare := l.tryAcquire("one")
		if !admitted {
			t.Fatalf("the only client in the lane was refused slot %d (over share: %v)", i+1, overShare)
		}
	}
	// The ninth is refused because the lane is full, which is capacity working,
	// not fairness: the refusal must not be attributed to a share.
	admitted, overShare := l.tryAcquire("one")
	if admitted || overShare {
		t.Fatalf("a full lane admitted %v or blamed the share %v", admitted, overShare)
	}
}

func TestOneClientCannotTakeMoreThanItsShareOfAContendedLane(t *testing.T) {
	l := newLane("test", laneSpec{capacity: 8})
	l.enter("greedy")
	defer l.leave("greedy")
	// Six of eight is the contention threshold. Up to there the greedy client is
	// alone in the lane and unrestricted.
	for i := 0; i < 6; i++ {
		if admitted, _ := l.tryAcquire("greedy"); !admitted {
			t.Fatalf("slot %d was refused before anyone else wanted the lane", i+1)
		}
	}
	// A second client arrives. Now the share applies, and the greedy one is held
	// at it while the newcomer is admitted straight away.
	l.enter("viewer")
	defer l.leave("viewer")
	if admitted, overShare := l.tryAcquire("greedy"); admitted || !overShare {
		t.Fatalf("the greedy client took a seventh slot with another client waiting (admitted %v, over share %v)", admitted, overShare)
	}
	if admitted, _ := l.tryAcquire("viewer"); !admitted {
		t.Fatal("a client with no slots at all was refused while another held six")
	}
}

func TestAThrottledClientIsAdmittedAsSoonAsItsShareComesFree(t *testing.T) {
	l := newLane("test", laneSpec{capacity: 8})
	l.enter("a")
	defer l.leave("a")
	for i := 0; i < 8; i++ {
		if admitted, _ := l.tryAcquire("a"); !admitted {
			t.Fatalf("slot %d refused", i+1)
		}
	}
	l.enter("b")
	defer l.leave("b")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	waiting := make(chan bool, 1)
	go func() { waiting <- l.acquire(ctx, "b") }()
	// Nothing is free yet, so the waiter must still be waiting.
	select {
	case admitted := <-waiting:
		t.Fatalf("a waiter was answered %v before any slot was released", admitted)
	case <-time.After(50 * time.Millisecond):
	}
	l.release("a")
	select {
	case admitted := <-waiting:
		if !admitted {
			t.Fatal("the released slot did not reach the waiting client")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a released slot never reached the client waiting for it")
	}
}

// The release/acquire pairing is an invariant, not a hope: an imbalance shrinks
// the lane permanently, so it is counted rather than dropped.
func TestBalancedUseNeverRecordsAnImbalance(t *testing.T) {
	l := newLane("test", laneSpec{capacity: 4})
	var wg sync.WaitGroup
	for client := 0; client < 6; client++ {
		wg.Add(1)
		go func(client int) {
			defer wg.Done()
			key := string(rune('a' + client))
			for i := 0; i < 200; i++ {
				l.enter(key)
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				if l.acquire(ctx, key) {
					l.release(key)
				}
				cancel()
				l.leave(key)
			}
		}(client)
	}
	wg.Wait()
	if n := l.imbalance.Load(); n != 0 {
		t.Fatalf("%d releases found the lane's token channel full", n)
	}
	if held := len(l.held); held != 0 {
		t.Fatalf("%d clients still hold slots after every one of them finished", held)
	}
	if interest := len(l.interest); interest != 0 {
		t.Fatalf("%d clients are still recorded as interested in the lane", interest)
	}
	if active := l.active.Load(); active != 0 {
		t.Fatalf("the lane still reports %d active requests", active)
	}
}

// A share is per device, not per account: a household where one television is
// misbehaving must not slow down the phone signed in to the same account.
func TestTheShareKeyIsTheVerifiedCredential(t *testing.T) {
	a := newAdmission()
	for _, secret := range []string{"device-one", "device-two", "grant-abc", "grant-xyz"} {
		a.rememberCredential(secret, identity.Principal{})
	}
	phone := httptest.NewRequest("GET", "/v1/home", nil)
	phone.Header.Set("Authorization", "Bearer device-one")
	television := httptest.NewRequest("GET", "/v1/home", nil)
	television.Header.Set("Authorization", "Bearer device-two")
	if a.clientKey(phone) == a.clientKey(television) {
		t.Fatal("two devices on one account share a fairness key")
	}
	again := httptest.NewRequest("GET", "/v1/items", nil)
	again.Header.Set("Authorization", "Bearer device-one")
	if a.clientKey(phone) != a.clientKey(again) {
		t.Fatal("one device got two fairness keys for two routes")
	}
	// The key must not be the credential itself; diagnostics show lane state and
	// a bearer token is not something to put in a diagnostic document.
	if key := a.clientKey(phone); strings.Contains(key, "device-one") {
		t.Fatalf("the fairness key carries the credential: %q", key)
	}
	// A media body carries no header, so the grant in the path identifies it.
	stream := httptest.NewRequest("GET", "/v1/media/grant-abc/segment-3.ts", nil)
	other := httptest.NewRequest("GET", "/v1/media/grant-xyz/segment-3.ts", nil)
	if a.clientKey(stream) == a.clientKey(other) {
		t.Fatal("two streams share a fairness key")
	}
	// With nothing at all, the client's own address — never its network, which
	// would put a whole household in one bucket.
	bare := httptest.NewRequest("GET", "/v1/readiness", nil)
	bare.RemoteAddr = "192.168.1.55:4000"
	neighbour := httptest.NewRequest("GET", "/v1/readiness", nil)
	neighbour.RemoteAddr = "192.168.1.56:4000"
	if fairnessKey(bare, nil) == fairnessKey(neighbour, nil) {
		t.Fatal("two devices on one home network share a fairness key")
	}
}

// End to end: a flood from one client must not change what another client sees.
// Fairness cannot take back a slot already being served — nothing can, short of
// killing a request in flight — so what it guarantees is not that a flood's
// share drops instantly, but that every slot which comes free is shared out.
// This drives a real flood through the middleware and asserts what a person
// would notice: with one client using four times the lane, the other client is
// answered, every time, and the flood is being held to its share while it
// happens.
func TestAFloodFromOneClientDoesNotRefuseAnother(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	gate := newAdmission()
	gate.rememberCredential("flood", identity.Principal{})
	gate.rememberCredential("viewer", identity.Principal{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/home", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Millisecond)
		w.WriteHeader(200)
	})
	handler := gate.wrap(mux, mux)
	lane := gate.lanes[laneBrowsing]

	stop := make(chan struct{})
	var flood sync.WaitGroup
	for i := 0; i < lane.spec.capacity*4; i++ {
		flood.Add(1)
		go func() {
			defer flood.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				r := httptest.NewRequest("GET", "/v1/home", nil)
				r.Header.Set("Authorization", "Bearer flood")
				handler.ServeHTTP(httptest.NewRecorder(), r)
			}
		}()
	}
	refused, served, slowest := 0, 0, time.Duration(0)
	for i := 0; i < 50; i++ {
		r := httptest.NewRequest("GET", "/v1/home", nil)
		r.Header.Set("Authorization", "Bearer viewer")
		w := httptest.NewRecorder()
		started := time.Now()
		handler.ServeHTTP(w, r)
		if elapsed := time.Since(started); elapsed > slowest {
			slowest = elapsed
		}
		if w.Code == 200 {
			served++
		} else {
			refused++
		}
	}
	close(stop)
	flood.Wait()
	if refused > 0 {
		t.Fatalf("%d of 50 honest requests were refused while one client flooded the lane (slowest %s)", refused, slowest.Round(time.Millisecond))
	}
	if served != 50 {
		t.Fatalf("only %d of 50 honest requests were answered", served)
	}
	if lane.throttled.Load() == 0 {
		t.Fatal("the lane never held anyone to a share, so this measured nothing")
	}
	t.Logf("honest client: %d served, %d refused, slowest %s, flood held to its share %d times",
		served, refused, slowest.Round(time.Millisecond), lane.throttled.Load())
}
