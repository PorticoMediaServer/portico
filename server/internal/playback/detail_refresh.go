package playback

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// A library scanned before stream detail existed has codec names and nothing
// else: no bit depth, no profile, no Dolby Vision configuration. Planning against
// that would either refuse direct play it could have allowed or allow direct play
// that then fails on the device. Rather than ask the owner to rescan, the first
// play of such a title refreshes it: one bounded probe, outside any transaction,
// staged as the existing normalized stream families and installed only by the
// accepted source publication. See info_source.go for the exact observation,
// witness and per-quantum fence.

// detailRefreshTimeout bounds the probe. A title that cannot be probed in time
// plays anyway, planned on what is known.
const detailRefreshTimeout = 8 * time.Second

// ConfigureProbe wires the ffprobe used to refresh stream detail at first play.
// Without it, planning simply uses whatever the scanner recorded.
func (s *Service) ConfigureProbe(p assets.Probe) {
	s.probe = &p
	s.probeSlots = make(chan struct{}, 2)
}

// refreshStreamDetail brings the selected source up to the current detail
// version through the bounded source-input pipeline. It returns the producer's
// explicit receipt: accepted-pending while the accepted body waits for the
// source41 owner, applied only when the canonical stream frame really is
// current, or an existing typed defer when the source-input slots or the 32 MiB
// ledger refuse it. A zero receipt with a nil error means no probe was needed,
// no probe slot was free, or no exact descriptor-bound observation was
// available.
//
// Every failure is silent at the call site: this is an improvement to a
// decision, never a precondition for playing.
func (s *Service) refreshStreamDetail(ctx context.Context, selectedAsset string) (compactcatalog.InfoAcceptReceipt, error) {
	if s.probe == nil {
		return compactcatalog.InfoAcceptReceipt{}, nil
	}
	observation, ok, err := s.observeDetailRefresh(ctx, selectedAsset)
	if err != nil || !ok {
		return compactcatalog.InfoAcceptReceipt{}, err
	}
	if s.StorageGuard != nil && s.StorageGuard(observation.Path) != nil {
		return compactcatalog.InfoAcceptReceipt{}, nil
	}
	if err = observation.bindDescriptor(); err != nil {
		// No acquired descriptor means no exact object to witness; the scanner
		// owns whatever the pathname now names.
		return compactcatalog.InfoAcceptReceipt{}, nil
	}
	select {
	case s.probeSlots <- struct{}{}:
		defer func() { <-s.probeSlots }()
	default:
		// Two refreshes are already running. This play proceeds on known facts and
		// a later one refreshes the title.
		return compactcatalog.InfoAcceptReceipt{}, nil
	}
	probeCtx, cancelProbe := context.WithTimeout(ctx, detailRefreshTimeout)
	facts, err := s.probe.InspectScan(probeCtx, observation.Path)
	cancelProbe()
	if err != nil || facts.Streams == nil {
		return compactcatalog.InfoAcceptReceipt{}, nil
	}
	if err = observation.stillAcquired(); err != nil {
		// The pathname no longer names the descriptor the observation bound; the
		// scanner owns a replaced file, never this refresh.
		return compactcatalog.InfoAcceptReceipt{}, nil
	}
	publishCtx, cancelPublish := context.WithTimeout(ctx, detailRefreshPublishTimeout)
	defer cancelPublish()
	return s.publishDetailRefresh(publishCtx, observation, facts)
}
