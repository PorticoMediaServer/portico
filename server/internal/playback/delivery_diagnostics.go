package playback

import (
	"context"
	"errors"
	"time"
)

var ErrDeliveryDiagnosticsBusy = errors.New("playback diagnostics temporarily unavailable")

// DeliveryDiagnostics reports this service's startup configuration and current
// conversion ownership. Configured paths are not source compatibility promises.
// Conversion sessions include waiting actors and outstanding cleanup, not only
// running encoder subprocesses. No paths, credentials or source IDs are exposed.
type DeliveryDiagnostics struct {
	DirectConfigured         bool   `json:"directConfigured"`
	HLSConfigured            bool   `json:"hlsConfigured"`
	FiniteHLSConfigured      bool   `json:"finiteHlsConfigured"`
	Lifecycle                string `json:"lifecycle"`
	ActiveConversionSessions *int   `json:"activeConversionSessions"`
	ConversionSessionLimit   *int   `json:"conversionSessionLimit"`
	ConversionLimitSource    string `json:"conversionLimitSource"`
	OutputPolicy             string `json:"outputPolicy"`
	// HostConversionCapacity is the host-derived ceiling on simultaneous
	// converters, and ConversionsRefused how many starts it has turned away.
	HostConversionCapacity  int    `json:"hostConversionCapacity"`
	MemorySpills            uint64 `json:"memorySpills"`
	MemoryPlacementsRefused uint64 `json:"memoryPlacementsRefused"`
	ConversionsRefused      uint64 `json:"conversionsRefused"`
	// HardwareFallbacks counts conversions whose hardware encoder failed on a
	// real source and were run again in software.
	HardwareFallbacks uint64 `json:"hardwareFallbacks"`
	// FragmentedOutput says whether this server produces fragmented MP4 HLS, the
	// container HEVC and AV1 are copied into.
	FragmentedOutput bool `json:"fragmentedOutput"`
	// RecentFailures are the latest conversion failures and recovered hardware
	// failures, with the converter's own diagnostics. Paths are removed before
	// they are stored; this list is for the owner only.
	RecentFailures []ConversionFailureRecord `json:"recentFailures"`
}

// ConversionFailureRecord is one failed or degraded conversion, for the owner.
type ConversionFailureRecord struct {
	SessionID  string `json:"sessionId"`
	Code       string `json:"code"`
	Diagnostic string `json:"diagnostic"`
	Strategy   string `json:"strategy,omitempty"`
	Backend    string `json:"hardwareBackend,omitempty"`
	Video      string `json:"sourceVideoCodec,omitempty"`
	Audio      string `json:"sourceAudioCodec,omitempty"`
}

// DeliveryDiagnostics reads immutable startup configuration and takes the HLS
// lock only if immediately available. HLS admission may hold that lock during a
// database query, so this owner diagnostic must never wait behind it. ConfigureHLS
// and EnableFinite are startup-only and must complete before HTTP is served.
func (s *Service) DeliveryDiagnostics(ctx context.Context) (DeliveryDiagnostics, error) {
	out := DeliveryDiagnostics{RecentFailures: []ConversionFailureRecord{}, Lifecycle: "unavailable", ConversionLimitSource: "unavailable", OutputPolicy: "unavailable"}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if s == nil || s.db == nil {
		return out, nil
	}
	out.DirectConfigured = true
	out.OutputPolicy = "per_session_plan"
	h := s.hls
	if h == nil {
		return out, nil
	}
	if !h.mu.TryLock() {
		return DeliveryDiagnostics{}, ErrDeliveryDiagnosticsBusy
	}
	if err := ctx.Err(); err != nil {
		h.mu.Unlock()
		return DeliveryDiagnostics{}, err
	}
	count := 0
	for id, window := range h.windows {
		if _, running := h.active[id]; running && window.videoConversion {
			count++
		}
	}
	out.HLSConfigured = true
	out.FiniteHLSConfigured = h.finite != nil && h.finite.encoder != nil
	out.ActiveConversionSessions = &count
	out.ConversionSessionLimit = nil
	out.ConversionLimitSource = "owner_policy"
	out.Lifecycle = "running"
	if h.ctx == nil || h.ctx.Err() != nil {
		out.Lifecycle = "stopping"
	}
	h.mu.Unlock()
	out.HostConversionCapacity = h.configuration().MaxConversions
	if out.HostConversionCapacity > 0 {
		out.ConversionSessionLimit = &out.HostConversionCapacity
	}
	out.ConversionsRefused = h.conversionsRefused.Load()
	out.MemorySpills = h.memorySpills.Load()
	out.MemoryPlacementsRefused = h.memoryRefused.Load()
	out.HardwareFallbacks = h.hardwareFailures.Load()
	out.FragmentedOutput = h.fragmentedOutput()
	out.RecentFailures = []ConversionFailureRecord{}
	// Bounded history survives output reclamation and session retirement.
	rows, err := s.db.QueryContext(ctx, `SELECT session_id,failure_code,diagnostic,strategy,backend,video,audio FROM playback_conversion_failures WHERE recorded_ms>? ORDER BY sequence DESC LIMIT 20`, time.Now().Add(-30*24*time.Hour).UnixMilli())
	if err != nil {
		return out, nil
	}
	defer rows.Close()
	for rows.Next() {
		var r ConversionFailureRecord
		if rows.Scan(&r.SessionID, &r.Code, &r.Diagnostic, &r.Strategy, &r.Backend, &r.Video, &r.Audio) != nil {
			break
		}
		out.RecentFailures = append(out.RecentFailures, r)
	}
	return out, nil
}
