package workload

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// Fabricated scheduling controls, never native FULL/checkpoint measurements.
func TestEC2SingleWriterAndCapabilityManifest(t *testing.T) {
	s := EC2Schedule{WriterEvery: time.Second, ReaderEvery: time.Second, PassiveEvery: 250 * time.Millisecond, RecoveryEvery: time.Second, WALEvery: time.Second, CheckpointMode: "concurrent",
		WriterPattern: []EC2Operation{EC2Inventory, EC2Effective, EC2ProgressCapture, EC2ProgressDrain, EC2Security}, ReaderPattern: []EC2Operation{EC2Browse, EC2Detail, EC2Playback, EC2Continue}}
	bindings := EC2Bindings{}
	for _, op := range append(append(append([]EC2Operation{}, s.WriterPattern...), s.ReaderPattern...), EC2Passive, EC2Recovery, EC2WAL) {
		bindings[op] = control
	}
	streams, m, err := EC2Streams(s, bindings)
	if err != nil || len(streams) != 5 || !m.CompleteCapabilities || !m.MixedFULLCadence || len(m.Missing) != 0 {
		t.Fatal(streams, m, err)
	}
	if streams[0].Name != "writer" || len(streams[0].Pattern) != 5 || streams[1].Name != "foreground" {
		t.Fatal(streams)
	}
	for i, step := range streams[0].Pattern {
		if step.Name != string(s.WriterPattern[i]) {
			t.Fatal(step)
		}
	}
	// Security and progress must never acquire a second driver writer worker.
	for _, stream := range streams[1:] {
		for _, step := range stream.Pattern {
			if step.Name == string(EC2Security) || step.Name == string(EC2ProgressDrain) || step.Name == string(EC2Inventory) {
				t.Fatal("parallel writer stream", stream)
			}
		}
	}
	original := m.Schedule.WriterPattern[0]
	s.WriterPattern[0] = EC2Security
	if m.Schedule.WriterPattern[0] != original {
		t.Fatal("mutable manifest")
	}
}

func TestEC2MissingBindingsRemainFailedRawOffers(t *testing.T) {
	s := EC2Schedule{WriterEvery: time.Second, WALEvery: time.Second, CheckpointMode: "concurrent", PassiveEvery: time.Second, RecoveryEvery: time.Second, WriterPattern: []EC2Operation{EC2Security, EC2Inventory}}
	streams, m, err := EC2Streams(s, EC2Bindings{EC2Inventory: control, EC2WAL: control})
	if err != nil || m.CompleteCapabilities || !reflect.DeepEqual(m.Missing, []EC2Operation{EC2Security, EC2Passive, EC2Recovery}) {
		t.Fatal(m, err)
	}
	for _, index := range []int{0, 1, 2} {
		step := streams[index].Pattern[0]
		var raw Observation
		err := step.Run(t.Context(), Offer{}, func(o Observation) error { raw = o; return nil })
		if !errors.Is(err, ErrEC2Composition) || !raw.Stub || raw.CallError == "" || raw.CallStarted != nil || raw.CallReturned != nil || raw.HoldNS != nil || raw.CommitNS != nil || raw.Evidence != nil {
			t.Fatal(raw, err)
		}
	}
	cfg := testConfig()
	cfg.Duration = time.Second
	sink := &memorySink{}
	if err := Run(t.Context(), cfg, streams, sink); !errors.Is(err, ErrEC2Composition) {
		t.Fatal(err)
	}
	f := final(t, sink)
	if f.ScheduleComplete || !f.DriversReturned || f.Error == "" {
		t.Fatal(f)
	}
}

func TestEC2ScheduleRejectsRoleAndCadenceDrift(t *testing.T) {
	base := EC2Schedule{WriterEvery: time.Second, WALEvery: time.Second, CheckpointMode: "serialized", WriterPattern: []EC2Operation{EC2Inventory}}
	cases := []EC2Schedule{base, base, base, base, base, base}
	cases[0].WriterPattern = []EC2Operation{EC2Inventory, EC2Browse}
	cases[1].WriterPattern = []EC2Operation{EC2Security}
	cases[2].PassiveEvery = time.Millisecond
	cases[3].RecoveryEvery = time.Second
	cases[4].ReaderPattern = []EC2Operation{EC2Inventory}
	cases[4].ReaderEvery = time.Second
	cases[5].CheckpointMode = "fake_reset"
	for _, s := range cases {
		if _, _, err := EC2Streams(s, nil); err == nil {
			t.Fatal("accepted invalid schedule", s)
		}
	}
}

func TestEC2ActionsKeepOriginalOfferAndError(t *testing.T) {
	offer := Offer{Stream: "writer", Operation: "inventory", Slot: 7, ScheduledAt: time.Now().Add(-time.Second)}
	failure := errors.New("physical unknown, original retained")
	action := func(ctx context.Context, actual Offer, emit func(Observation) error) error {
		if actual != offer {
			t.Fatal("rebased original offer", actual)
		}
		return errors.Join(failure, emit(Observation{Phase: "ACCEPT", CallError: failure.Error()}))
	}
	streams, _, err := EC2Streams(EC2Schedule{WriterEvery: time.Second, WALEvery: time.Second, CheckpointMode: "serialized", WriterPattern: []EC2Operation{EC2Inventory}}, EC2Bindings{EC2Inventory: action, EC2WAL: control})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	err = streams[0].Pattern[0].Run(t.Context(), offer, func(o Observation) error {
		count++
		if o.CallError != failure.Error() || o.Evidence != nil {
			t.Fatal(o)
		}
		return nil
	})
	if !errors.Is(err, failure) || count != 1 {
		t.Fatal(err, count)
	}
}
