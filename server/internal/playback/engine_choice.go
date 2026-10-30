package playback

// planDevice plans one source for a whole capability document: it decides which
// of the device's player engines plays the title, and returns that engine's
// plan.
//
// A device always has its platform's own player ("native"); some also carry
// mpv. The viewer's setting on the device is native, mpv or automatic. With
// automatic the native player is used whenever it can play the file as it is
// or with a plain repackaging. mpv is used only when the alternative on the
// native player is a converted picture, a lossy audio conversion or subtitles
// burned into the picture, and mpv can keep the streams as they are, directly
// or with a plain repackaging. In every other case the title is converted for the native player.
//
// A pinned engine (the re-plan of a presentation that is already playing) is
// never changed here. An engine the viewer chose is used even when the other
// would do better; only when it cannot play the title at all is the other one
// tried, and the trace says so.
//
// The plan returned never has the chosen subtitle burned in: the subtitle
// selection that follows a start does that, for the engine chosen here.
// Burn-in only weighs in the choice.
func planDevice(in DeliveryInput) (DeliveryPlan, error) {
	doc := *in.Device
	in.Device = nil
	run := func(engine, note string) (DeliveryPlan, error) {
		one := in
		one.Client = doc.Profile(engine)
		one.Excluded = in.ExcludedByEngine[one.Client.Engine]
		p, err := planDelivery(one)
		if err == nil && note != "" && p.Trace != nil {
			p.Trace.Notes = append(p.Trace.Notes, note)
		}
		return p, err
	}
	hasMPV := doc.HasEngine(EngineMPV)
	audioOnly := in.Source != nil && in.Source.Video == nil || in.Source == nil && in.VideoCodec == ""
	other := func(engine string) string {
		if engine == EngineMPV {
			return EngineNative
		}
		return EngineMPV
	}
	if in.Engine != "" {
		return run(doc.Profile(in.Engine).Engine, "")
	}
	engine := ""
	switch {
	case !hasMPV || audioOnly:
		// Music is rendered by the client's own audio engine. A STRM is served
		// by this server like any other file, so either engine can take it.
		engine = EngineNative
	case doc.EngineSetting == EngineSettingNative:
		engine = EngineNative
	case doc.EngineSetting == EngineSettingMPV:
		engine = EngineMPV
	}
	if engine != "" {
		p, err := run(engine, "")
		if err != nil && hasMPV && !audioOnly {
			if fallback, fallbackErr := run(other(engine), NoteEngineFallback); fallbackErr == nil {
				return fallback, nil
			}
		}
		return p, err
	}
	native, nativeErr := run(EngineNative, NoteEngineAutomatic)
	if nativeErr == nil && !engineCompromises(native) && !SubtitleNeedsBurnIn(doc.Profile(EngineNative), in.SubtitleKind, native.Mode, in.SubtitleBesideStream) {
		return native, nil
	}
	mpv, mpvErr := run(EngineMPV, NoteEngineAutomatic)
	if mpvErr == nil && (mpv.Strategy == DeliveryOriginal || mpv.Strategy == DeliveryCopyRemux) && !engineCompromises(mpv) && !SubtitleNeedsBurnIn(doc.Profile(EngineMPV), in.SubtitleKind, mpv.Mode, in.SubtitleBesideStream) {
		return mpv, nil
	}
	if nativeErr != nil {
		// The native player cannot be given this title at all; mpv may still
		// take a repackaged or converted stream.
		if mpvErr == nil {
			return mpv, nil
		}
		return native, nativeErr
	}
	return native, nil
}

// engineCompromises reports whether a plan gives something up that another
// engine might keep: a converted picture, audio converted to a lossy codec, or
// an original that can only be played with its first audio track.
func engineCompromises(p DeliveryPlan) bool {
	if p.VideoAction == "convert" || p.AudioAction == "convert" && !p.AudioLossless {
		return true
	}
	if p.Trace != nil && containsString(p.Trace.Notes, NoteFirstAudioTrackPlayed) {
		return true
	}
	return false
}

// SubtitleBurnAlways is the subtitle kind of a track the viewer asked to have
// burned into the picture whatever the engine could draw.
const SubtitleBurnAlways = "burn"

// SubtitleNeedsBurnIn reports whether a styled or image subtitle of this kind
// has to be burned into the picture for one engine and one delivery mode. It
// does unless the engine draws the kind itself and the track can reach it:
// inside the original file the engine is playing (mode "direct"), or beside a
// stream when the server can hand the track over as it is (besideStream).
// An empty kind is no subtitle, or text delivered beside the stream.
func SubtitleNeedsBurnIn(engine ClientProfile, kind, mode string, besideStream bool) bool {
	if kind == "" {
		return false
	}
	if kind == SubtitleBurnAlways || !engine.RendersSubtitle(kind) {
		return true
	}
	return mode != "direct" && mode != "remote" && !besideStream
}
