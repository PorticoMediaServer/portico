package streaming

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
