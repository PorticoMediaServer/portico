package governor

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRestartRiskIsConservativeFreshAndExactAtAdmission(t *testing.T) {
	g, a, clock, wal := checkpointSetup(t)
	for _, sample := range []WALState{walSample(4, 4), walSample(4, 3), {PageSize: 4096}} {
		publish(t, wal, sample, clock.now())
		risk, err := wal.RestartRisk(clock.now())
		if err != nil || risk.Possible != (sample.Frames == sample.Backfill) || (risk.Possible && risk.HeaderBytes != 32) || (!risk.Possible && risk.HeaderBytes != 0) {
			t.Fatal("conservative risk", risk, err)
		}
		r := checkpointWriter(clock, Foreground)
		r.RestartRisk = risk
		p := grant(t, g, r)
		if err := a.Check(p, r); err != nil {
			t.Fatal(err)
		}
		changed := r
		changed.RestartRisk.HeaderBytes++
		if a.Check(p, changed) != ErrGrant {
			t.Fatal("narrower/different restart metadata accepted")
		}
		if _, err := wal.RestartRisk(clock.now()); err == nil {
			t.Fatal("active writer returned warm hint")
		}
		retire(t, a, p)
		refused(t, g, r, FailSafe) // same sample is stale after physical return
		publish(t, wal, sample, clock.now())
		refused(t, g, r, FailSafe) // even identical bytes cannot revive the old generation
		if a.Usage() != [4]Amounts{} {
			t.Fatal("stale risk acquired custody")
		}
	}
	risk, _ := wal.RestartRisk(clock.now())
	r := checkpointWriter(clock, Foreground)
	r.RestartRisk = risk
	cp := checkpointGrant(t, a, checkpointRequest(clock))
	retire(t, a, cp)
	publish(t, wal, walSample(4, 4), clock.now())
	refused(t, g, r, FailSafe) // CP invalidation requires reforecast too
	clock.ms.Add(wal.policy.MaxSampleAge.Milliseconds() + 1)
	if _, err := wal.RestartRisk(clock.now()); err == nil {
		t.Fatal("stale sample silently returned Possible=false")
	}
	if _, err := gate(t).RestartRisk(clock.now()); err == nil {
		t.Fatal("missing sample silently returned Possible=false")
	}
	wal.mu.Lock()
	expiry := make(chan time.Time, 1)
	expiry <- clock.now()
	wal.mu.timer = func(time.Duration) (<-chan time.Time, func()) { return expiry, func() {} }
	_, err := wal.RestartRiskContext(context.Background(), clock.now(), clock.now().Add(time.Second))
	wal.mu.Unlock()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("risk getter must use bounded deadline", err)
	}
}

func TestRestartPlacementAlreadyEligibleOnlyAndImmediateFallback(t *testing.T) {
	risk := WALRestartRisk{ObservedAt: time.Unix(1, 0), Possible: true, HeaderBytes: 32}
	cases := []struct {
		ready []ReadyWriter
		want  int
	}{
		{[]ReadyWriter{{Recovery, true}, {Background, true}}, 1},
		{[]ReadyWriter{{Foreground, true}, {ScanMinimum, true}}, 1},
		{[]ReadyWriter{{Foreground, true}, {Background, false}}, 0},
		{[]ReadyWriter{{Recovery, true}, {ScanMinimum, false}}, 0},
		{[]ReadyWriter{{Foreground, false}, {Recovery, true}}, 1},
		{[]ReadyWriter{{Background, false}}, -1},
		{nil, -1},
	}
	for _, test := range cases {
		got, err := ChooseRestartWriter(risk, test.ready)
		if err != nil || got != test.want {
			t.Fatal("placement/waited-for-unready", test.ready, got, test.want, err)
		}
	}
	risk.Possible, risk.HeaderBytes = false, 0
	if got, err := ChooseRestartWriter(risk, cases[0].ready); err != nil || got != 0 {
		t.Fatal("warm ordering changed", got, err)
	}
	for _, invalid := range []WALRestartRisk{{}, {ObservedAt: risk.ObservedAt, Possible: true}, {ObservedAt: risk.ObservedAt, HeaderBytes: 32}} {
		if _, err := ChooseRestartWriter(invalid, cases[0].ready); err == nil {
			t.Fatal("unavailable/invalid hint accepted")
		}
	}
	if _, err := ChooseRestartWriter(risk, make([]ReadyWriter, 33)); err == nil {
		t.Fatal("unbounded selection accepted")
	}
}

func TestRestartClassAttributionRequiresPhysicalWriterAndSampleLink(t *testing.T) {
	for _, class := range []Class{Recovery, Foreground, ScanMinimum, Background} {
		g, a, clock, wal := checkpointSetup(t)
		risk, err := wal.RestartRisk(clock.now())
		if err != nil {
			t.Fatal(err)
		}
		r := checkpointWriter(clock, class)
		r.RestartRisk = risk
		r.Need[IOBytes] = 16
		p := grant(t, g, r)
		g.Cancel(p)
		next := walSample(1, 0)
		next.Salt[0]++
		if err := wal.Publish(wal.Observation(), next, clock.now()); err == nil {
			t.Fatal("cancel pretended to be physical return")
		}
		retire(t, a, p)
		publish(t, wal, next, clock.now())
		receipt := wal.LastRestartObservation()
		if !receipt.WriterKnown || receipt.Class != class || receipt.InitialHeader || receipt.Risk != risk || receipt.ForecastHold != r.MaxHold || receipt.ForecastIOBytes != r.Need[IOBytes] || receipt.ForecastWALBytes != r.Need[WALBytes] || wal.ResetCount() != 1 {
			t.Fatal("actual absorbing class/forecast missing", receipt)
		}
		cp := checkpointGrant(t, a, checkpointRequest(clock))
		retire(t, a, cp)
		next.Salt[0]++ // no writer return can attest this external transition
		publish(t, wal, next, clock.now())
		if wal.LastRestartObservation().WriterKnown {
			t.Fatal("CP sample invented writer attribution")
		}
	}
}
