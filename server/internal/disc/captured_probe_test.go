package disc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
)

type capturedReaderFixture struct {
	file          *os.File
	life          context.Context
	id            string
	version       mediasource.Version
	checks        int
	lifetimeReads int
}

func (r *capturedReaderFixture) File() *os.File           { return r.file }
func (r *capturedReaderFixture) Context() context.Context { r.lifetimeReads++; return r.life }
func (r *capturedReaderFixture) CaptureIdentity() (string, mediasource.Version) {
	return r.id, r.version
}
func (r *capturedReaderFixture) Check(context.Context) error            { r.checks++; return nil }
func (r *capturedReaderFixture) CheckTx(context.Context, *sql.Tx) error { r.checks++; return nil }

func capturedObservationFixture(t *testing.T) (*CapturedProbe, *capturedReaderFixture, context.CancelFunc, context.CancelFunc) {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "owned-observation-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	caller, cancelCaller := context.WithCancel(context.Background())
	life, cancelLife := context.WithCancel(context.Background())
	t.Cleanup(cancelCaller)
	t.Cleanup(cancelLife)
	version := capturedFixtureVersion(t, "original-source", "owned bytes")
	source := &capturedReaderFixture{file: file, life: life, id: "original-capture", version: version}
	observation := &CapturedProbe{source: source, file: file, caller: caller, life: life, id: source.id, version: version}
	return observation, source, cancelCaller, cancelLife
}

func TestCapturedProbeCannotRetargetOriginalReader(t *testing.T) {
	for _, changed := range []string{"capture", "version", "descriptor"} {
		t.Run(changed, func(t *testing.T) {
			observation, source, _, _ := capturedObservationFixture(t)
			switch changed {
			case "capture":
				source.id = "replacement-capture"
			case "version":
				source.version = capturedFixtureVersion(t, "other-source", "owned bytes")
			case "descriptor":
				other, err := os.Open(source.file.Name())
				if err != nil {
					t.Fatal(err)
				}
				defer other.Close()
				source.file = other // even another handle of the same file is not this reader
			}
			if err := observation.Check(context.Background()); !errors.Is(err, mediasource.ErrSourceChanged) || source.checks != 0 {
				t.Fatal("old observation consulted a replacement reader's authority")
			}
		})
	}
}

func TestCapturedProbeRetainsOriginalCallerAndReaderLifetime(t *testing.T) {
	for _, retireCaller := range []bool{true, false} {
		observation, source, caller, reader := capturedObservationFixture(t)
		if retireCaller {
			caller()
		} else {
			reader()
		}
		if err := observation.Check(context.Background()); !errors.Is(err, context.Canceled) || source.checks != 0 {
			t.Fatal("a fresh caller context revived the original observation")
		}
	}
}

func TestCapturedProbeStructureCannotBeMutatedByItsConsumer(t *testing.T) {
	probe := capturedProbeFixture(t)
	raw, err := json.Marshal(probe)
	if err != nil {
		t.Fatal(err)
	}
	observation := &CapturedProbe{raw: raw}
	copy, err := observation.Structure()
	if err != nil {
		t.Fatal(err)
	}
	copy.Titles[0].DurationMs++
	again, err := observation.Structure()
	if err != nil || again.Titles[0].DurationMs != probe.Titles[0].DurationMs {
		t.Fatal("a consumer changed the measured structure retained for publication")
	}
}

func TestCapturedProbeRefusesNonRetirableOrRetiredReaderBeforeTool(t *testing.T) {
	_, source, _, _ := capturedObservationFixture(t)
	source.life = context.Background()
	if _, err := (Tool{Binary: "/must-not-launch"}).ProbeCapturedISO(context.Background(), source); !errors.Is(err, ErrProtocol) {
		t.Fatal("a placeholder reader lifetime was accepted")
	}
	_, source, _, reader := capturedObservationFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Tool{Binary: "/must-not-launch"}).ProbeCapturedISO(ctx, source); !errors.Is(err, context.Canceled) {
		t.Fatal("retired caller reached the helper")
	}
	reader()
	if _, err := (Tool{Binary: "/must-not-launch"}).ProbeCapturedISO(context.Background(), source); !errors.Is(err, context.Canceled) {
		t.Fatal("retired reader reached the helper")
	}
}

func TestCapturedProbeRefusesCarriedTransactionBeforeSourceWork(t *testing.T) {
	source := &capturedReaderFixture{life: context.Background()}
	ctx := dbwork.WithWrite(context.Background(), new(sql.Tx), dbwork.ClassBackgroundMedia)
	_, err := (Tool{Binary: "/must-not-launch"}).ProbeCapturedISO(ctx, source)
	if !errors.Is(err, ErrProtocol) || source.lifetimeReads != 0 || source.checks != 0 {
		t.Fatal("carried transaction reached capture/helper admission")
	}
}
