package disc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
)

// CapturedDiscReader is an original owned-capture reader, not an arbitrary
// file/digest pair. Its adapter resolves the exact committed receipt and keeps
// the artifact and normalized reader pin alive. Check/CheckTx must recheck the
// original source/config/physical asset/read-policy and authority witnesses.
// Context is its actual retirable lifetime; a non-retirable placeholder fails.
type CapturedDiscReader interface {
	File() *os.File
	CaptureIdentity() (string, mediasource.Version)
	Context() context.Context
	Check(context.Context) error
	CheckTx(context.Context, *sql.Tx) error
}

// CapturedProbe is a process-private observation of the captured bytes. A
// publication writer must call CheckTx in its accepting transaction and retain
// the original reader through commit. No fields can be supplied from a request
// or rebound to a later reader/capture. It conveys no playback authority.
type CapturedProbe struct {
	source      CapturedDiscReader
	file        *os.File
	caller      context.Context
	life        context.Context
	id          string
	version     mediasource.Version
	raw         []byte
	fingerprint string
}

// ProbeCapturedISO reads only the verified descriptor of this original capture.
// It never probes/refetches the provider. The caller owns the reader and must
// keep it open until accepting publication is complete. Encrypted metadata may
// return an observation with ErrEncrypted; it is never a playable selection.
func (t Tool) ProbeCapturedISO(ctx context.Context, source CapturedDiscReader) (*CapturedProbe, error) {
	if ctx == nil || source == nil {
		return nil, ErrProtocol
	}
	// Helpers read bytes outside SQL transactions. Only the later accepting
	// writer borrows its original transaction for this observation's CheckTx.
	if dbwork.Snapshot(ctx) != nil {
		return nil, ErrProtocol
	}
	life := source.Context()
	if life == nil || life.Done() == nil {
		return nil, ErrProtocol
	}
	id, version := source.CaptureIdentity()
	file := source.File()
	if file == nil || !validCaptureIdentity(id, version) {
		return nil, ErrProtocol
	}
	out := &CapturedProbe{source: source, file: file, caller: ctx, life: life, id: id, version: version}
	run, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(life, cancel)
	defer func() { stop(); cancel() }()
	if err := out.Check(run); err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != version.Evidence().Size {
		return nil, mediasource.ErrSourceChanged
	}
	probe, probeErr := t.ProbeDescriptor(run, file, false)
	if probeErr != nil && !errors.Is(probeErr, ErrEncrypted) {
		return nil, probeErr
	}
	if err = out.Check(run); err != nil {
		return nil, err
	}
	fingerprint, err := CapturedStructureFingerprint(id, version, probe)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(probe)
	if err != nil {
		return nil, ErrProtocol
	}
	if err = out.Check(run); err != nil {
		return nil, err
	}
	out.raw, out.fingerprint = raw, fingerprint
	return out, probeErr
}

func (p *CapturedProbe) current(ctx context.Context) error {
	if p == nil || p.source == nil || p.file == nil || p.caller == nil || p.life == nil || ctx == nil {
		return ErrProtocol
	}
	for _, life := range []context.Context{ctx, p.caller, p.life} {
		if err := life.Err(); err != nil {
			return err
		}
	}
	id, version := p.source.CaptureIdentity()
	if id != p.id || version.ID() != p.version.ID() || p.source.File() != p.file {
		return mediasource.ErrSourceChanged
	}
	return nil
}

func (p *CapturedProbe) Check(ctx context.Context) error {
	if err := p.current(ctx); err != nil {
		return err
	}
	if err := p.source.Check(ctx); err != nil {
		return err
	}
	return p.current(ctx)
}

func (p *CapturedProbe) CheckTx(ctx context.Context, tx *sql.Tx) error {
	if tx == nil {
		return ErrProtocol
	}
	if err := p.current(ctx); err != nil {
		return err
	}
	if err := p.source.CheckTx(ctx, tx); err != nil {
		return err
	}
	return p.current(ctx)
}

func (p *CapturedProbe) CaptureIdentity() (string, mediasource.Version) { return p.id, p.version }
func (p *CapturedProbe) Fingerprint() string                            { return p.fingerprint }

// Structure decodes a private copy, so callers cannot mutate an observation
// while an accepting writer checks its original provenance.
func (p *CapturedProbe) Structure() (Probe, error) {
	if p == nil || len(p.raw) == 0 {
		return Probe{}, ErrProtocol
	}
	return DecodeProbe(p.raw)
}
