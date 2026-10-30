package disc

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type remoteDiscFixture struct {
	data       []byte
	calls      int
	short      bool
	afterRead  func()
	lastOffset int64
}

func (f *remoteDiscFixture) Snapshot() storage.Snapshot {
	return storage.Snapshot{Size: int64(len(f.data))}
}
func (f *remoteDiscFixture) Version() mediasource.Version { return mediasource.Version{} }
func (f *remoteDiscFixture) Access() string               { return "finite_random" }
func (f *remoteDiscFixture) Observe(context.Context) (storage.Snapshot, error) {
	return f.Snapshot(), nil
}
func (f *remoteDiscFixture) Close() error { return nil }
func (f *remoteDiscFixture) OpenRange(_ context.Context, offset, length int64) (io.ReadCloser, error) {
	f.calls++
	f.lastOffset = offset
	if f.short {
		length--
	}
	return &remoteDiscFixtureBody{Reader: bytes.NewReader(f.data[offset : offset+length]), afterRead: f.afterRead}, nil
}

type remoteDiscFixtureBody struct {
	*bytes.Reader
	afterRead func()
}

func (b *remoteDiscFixtureBody) Close() error {
	if b.afterRead != nil {
		b.afterRead()
	}
	return nil
}
func remoteDiscRequest(offset uint64, length uint32) []byte {
	raw := make([]byte, 16)
	copy(raw, "PDR1")
	binary.BigEndian.PutUint64(raw[4:], offset)
	binary.BigEndian.PutUint32(raw[12:], length)
	return raw
}

func TestRemoteDiscBrokerPublishesOnlyTheCapturedCompleteRange(t *testing.T) {
	f := &remoteDiscFixture{data: []byte("original-source")}
	var out bytes.Buffer
	serveRemoteDiscRanges(context.Background(), bytes.NewReader(remoteDiscRequest(3, 5)), &out, f, int64(len(f.data)), 1, 5, nil)
	want := append([]byte{'P', 'D', 'A', '1', 0, 0, 0, 5}, []byte("ginal")...)
	if !bytes.Equal(out.Bytes(), want) || f.calls != 1 || f.lastOffset != 3 {
		t.Fatalf("range was substituted or framed incorrectly: calls=%d offset=%d", f.calls, f.lastOffset)
	}
}

func TestRemoteDiscBrokerRefusesInvalidOrOverBudgetRequestsBeforeOpening(t *testing.T) {
	badMagic := remoteDiscRequest(0, 1)
	badMagic[0] = 'X'
	for name, request := range map[string][]byte{
		"magic": badMagic, "zero": remoteDiscRequest(0, 0),
		"offset overflow": remoteDiscRequest(^uint64(0), 1),
		"past extent":     remoteDiscRequest(3, 2),
		"past budget":     remoteDiscRequest(0, 4),
		"truncated":       remoteDiscRequest(0, 1)[:15],
	} {
		t.Run(name, func(t *testing.T) {
			f := &remoteDiscFixture{data: []byte("data")}
			var out bytes.Buffer
			serveRemoteDiscRanges(context.Background(), bytes.NewReader(request), &out, f, 4, 1, 3, nil)
			if f.calls != 0 || out.Len() != 0 {
				t.Fatal("invalid request reached the source or published bytes")
			}
		})
	}
}

func TestRemoteDiscBrokerRetirementOrShortBodyPublishesNoSuccessHeader(t *testing.T) {
	for _, retire := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		f := &remoteDiscFixture{data: []byte("data"), short: !retire}
		if retire {
			f.afterRead = cancel
		}
		var out bytes.Buffer
		serveRemoteDiscRanges(ctx, bytes.NewReader(remoteDiscRequest(0, 4)), &out, f, 4, 1, 4, nil)
		cancel()
		if out.Len() != 0 {
			t.Fatal("incomplete or retired read published a successful range")
		}
	}
}

func TestRemoteDiscBrokerCannotRenewItsRequestOrByteBudget(t *testing.T) {
	for _, limits := range [][2]int64{{1, 4}, {2, 2}} {
		f := &remoteDiscFixture{data: []byte("data")}
		var out bytes.Buffer
		request := append(remoteDiscRequest(0, 2), remoteDiscRequest(2, 2)...)
		serveRemoteDiscRanges(context.Background(), bytes.NewReader(request), &out, f, 4, limits[0], limits[1], nil)
		if f.calls != 1 || out.Len() != 10 {
			t.Fatal("helper renewed an exhausted acquisition budget")
		}
	}
}

func TestRemoteDiscProbeRefusesUnversionedSourceBeforeLaunchingTool(t *testing.T) {
	f := &remoteDiscFixture{data: make([]byte, 2048)}
	_, err := (Tool{Binary: "/must-not-launch"}).ProbeRemoteImage(context.Background(), f)
	if !errors.Is(err, ErrRemoteUnqualified) || f.calls != 0 {
		t.Fatal("size alone admitted an unversioned source")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = (Tool{Binary: "/must-not-launch"}).ProbeRemoteImage(ctx, f)
	if !errors.Is(err, context.Canceled) || f.calls != 0 {
		t.Fatal("retired invocation was not refused before tool acquisition")
	}
}

func TestRemoteDiscBrokerRechecksTheOriginalAdmissionBeforePublishing(t *testing.T) {
	retired := errors.New("original occurrence retired")
	for _, beforeRead := range []bool{true, false} {
		live := !beforeRead
		f := &remoteDiscFixture{data: []byte("data"), afterRead: func() { live = false }}
		guard := func(context.Context) error {
			if !live {
				return retired
			}
			return nil
		}
		var out bytes.Buffer
		serveRemoteDiscRanges(context.Background(), bytes.NewReader(remoteDiscRequest(0, 4)), &out, f, 4, 1, 4, guard)
		if out.Len() != 0 || beforeRead && f.calls != 0 || !beforeRead && f.calls != 1 {
			t.Fatal("a retired original admission read or published source bytes")
		}
	}
}
