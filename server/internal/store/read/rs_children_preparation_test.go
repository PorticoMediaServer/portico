package read

import (
	"context"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"reflect"
	"testing"
	"time"
)

func TestRSChildrenPreparationCustodyAndPending(t *testing.T) {
	f, r := rsChildrenFixture(t)
	in := rsChildrenInput(f, RSAlbumTracks)
	base := f.authority.Usage()[governor.Foreground][governor.MemoryBytes]
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	w, refusal, err := BeginRSListeningWork(ctx, f.authority, 1, governor.JobKey{Domain: 79, ID: "custody"}, time.Now().Add(time.Hour), in)
	if err != nil {
		t.Fatal(refusal, err)
	}
	defer w.Close()
	in.Work = w
	deadline := w.lease.Deadline()
	p, cost, err := r.window(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || !p.Preparing || !p.Pending || p.Next != nil || len(p.Items) != 0 || p.PreparedRows != 90 || w.index != rsListeningKeysPerPoll || len(cost.LookupWindows) != 6 {
		t.Fatal("complete-set preparation prefix escaped", p, w.index, err)
	}
	if !deadline.Equal(w.lease.Deadline()) || w.bytes != 90*128+2*rsListeningKeysPerPoll || w.lease.PayloadBytes() != rsListeningPreparationBytes {
		t.Fatal("prospective bytes/deadline", w.bytes)
	}
	charged := base + rsListeningPreparationBytes + governor.PreparationMetadataBytes
	if got := f.authority.Usage()[governor.Foreground][governor.MemoryBytes]; got != charged {
		t.Fatal("work not charged", got, charged)
	}
	wrong := in
	wrong.Family = RSAudiobookFiles
	wrong.Parent = 2002
	wrong.View.Kind = 8
	p, _, err = r.window(t.Context(), foregroundRequest(), time.Second, f.output, wrong)
	if !errors.Is(err, ErrInput) || !reflect.DeepEqual(p, RSChildrenPage{}) {
		t.Fatal("foreign query reused work", p, err)
	}
	cancel()
	p, _, err = r.window(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(p, RSChildrenPage{}) {
		t.Fatal("canceled work escaped", p, err)
	}
	if got := f.authority.Usage()[governor.Foreground][governor.MemoryBytes]; got != charged {
		t.Fatal("logical cancel refunded retained memory", got)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if got := f.authority.Usage()[governor.Foreground][governor.MemoryBytes]; got != base {
		t.Fatal("physical exit failed to retire work", got)
	}
}
func TestRSChildrenPreparationReadyHandoff(t *testing.T) {
	f, r := rsChildrenFixture(t)
	f.exec(t, `DELETE FROM catalog_entities WHERE id>4004 AND id<5000`)
	in := rsChildrenInput(f, RSAlbumTracks)
	in.View.Limit = 1
	base := f.authority.Usage()[governor.Foreground][governor.MemoryBytes]
	w, refusal, err := BeginRSListeningWork(t.Context(), f.authority, 1, governor.JobKey{Domain: 79, ID: "handoff"}, time.Now().Add(12*time.Second), in)
	if err != nil {
		t.Fatal(refusal, err)
	}
	defer w.Close()
	in.Work = w
	p, _, err := r.window(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || p.Preparing || len(p.Items) != 1 || p.Next == nil || !w.handed || f.authority.CheckPreparation(w.lease) == nil {
		t.Fatal("ready handoff", p, err)
	}
	if got := f.authority.Usage()[governor.Foreground][governor.MemoryBytes]; got != base+rsListeningPreparationBytes+governor.PreparationMetadataBytes {
		t.Fatal("handoff refunded still-retained memory", got)
	}
	again, _, err := r.window(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrInput) || !reflect.DeepEqual(again, RSChildrenPage{}) {
		t.Fatal("replayed handed work", again, err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if got := f.authority.Usage()[governor.Foreground][governor.MemoryBytes]; got != base {
		t.Fatal("ready consumer exit charge", got)
	}
}
