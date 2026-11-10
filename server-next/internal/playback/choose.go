package playback

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Settings a device or a profile can change that decide what a start plays. Values are JSON.
const (
	// The saved quality for each kind of connection, per device: "original", "automatic", or
	// the id of a menu entry. Unset is the original at home and away, and on mobile data
	// 720p for video and 320 kbps for music.
	PrefQualityHome     = "quality.home"
	PrefQualityRemote   = "quality.remote"
	PrefQualityCellular = "quality.cellular"
	PrefMusicHome       = "music.quality.home"
	PrefMusicRemote     = "music.quality.remote"
	PrefMusicCellular   = "music.quality.cellular"
	// PrefResumeRewind is "Rewind when resuming", in seconds: none unless set.
	PrefResumeRewind = "playback.resumeRewindSeconds"
)

// Invalid is a request this package refuses as malformed. Field names the part at fault.
type Invalid struct{ Field, Message string }

func (e *Invalid) Error() string { return e.Field + ": " + e.Message }

// Unplayable is a refusal to play one version, with the versions of the title that would
// play on this device instead.
type Unplayable struct {
	// Err is ErrUnsupported, ErrConversionOff, ErrTooSlow, ErrUpdateRequired or ErrSourceUnavailable.
	Err          error
	Alternatives []Alternative
}

func (e *Unplayable) Error() string { return e.Err.Error() }
func (e *Unplayable) Unwrap() error { return e.Err }

// Alternative is another version of the title.
type Alternative struct {
	VersionID string `json:"versionId"`
	Label     string `json:"label"`
}

// TrackID is the id a track has on the wire. Apps never parse it.
func TrackID(t db.Track) string {
	if t.Sidecar {
		return "f" + strconv.FormatInt(t.FileID, 10) + "-" + strconv.Itoa(t.Index)
	}
	return strconv.Itoa(t.Index)
}

func findTrack(v db.PlaybackVersion, kind db.StreamKind, id string) (db.Track, bool) {
	for _, t := range v.Tracks {
		if t.Kind == kind && TrackID(t) == id {
			return t, true
		}
	}
	return db.Track{}, false
}

// Commentary reports whether an audio track is a commentary, as its title says.
func Commentary(t db.Track) bool { return strings.Contains(strings.ToLower(t.Title), "commentary") }

// pickOf is how a chosen track is remembered: its language and what sets it apart from the
// other tracks in that language. See db.RememberTracks.
func pickOf(t db.Track) string {
	pick := t.Language
	if pick == "" {
		pick = "und"
	}
	switch {
	case t.Kind == db.StreamAudio && Commentary(t):
		pick += " commentary"
	case t.Kind == db.StreamSubtitle && t.Forced:
		pick += " forced"
	case t.Kind == db.StreamSubtitle && t.HearingImpaired:
		pick += " sdh"
	}
	return pick
}

// trackForPick finds the track of a version that a remembered pick means: the same language,
// and the closest in kind. A version with nothing in that language has none.
func trackForPick(v db.PlaybackVersion, kind db.StreamKind, pick string) (db.Track, bool) {
	language, _, _ := strings.Cut(pick, " ")
	var best db.Track
	bestScore := -1
	for _, t := range v.Tracks {
		lang := t.Language
		if lang == "" {
			lang = "und"
		}
		if t.Kind != kind || lang != language {
			continue
		}
		score := 0
		if pickOf(t) == pick {
			score += 4
		}
		if t.Default {
			score++
		}
		if score > bestScore {
			best, bestScore = t, score
		}
	}
	return best, bestScore >= 0
}

// Ask is what a request says about the choices. Nil leaves a choice to what is remembered
// and then to the viewer's settings.
type Ask struct {
	VersionID string
	Audio     *string
	// Subtitle is a track id, or "" for off.
	Subtitle         *string
	SubtitleDelivery string
	SubtitleOffsetMs *int64
	Quality          *Quality
	Network          string
	ThroughputKbps   int
}

// Who is asking, and from where.
type Caller struct {
	Viewer   db.Viewer
	SignInID int64
	// AtHome says the request came from the home network.
	AtHome bool
}

// network decides which kind of connection a request is on. The server tells home from away;
// only the app knows it is on mobile data, and an app away from home cannot claim to be home.
func network(asked string, atHome bool) string {
	switch {
	case asked == NetworkCellular:
		return NetworkCellular
	case atHome:
		return NetworkLocal
	}
	return NetworkRemote
}

func audioKind(kind db.ItemKind) bool { return kind == db.KindTrack || kind == db.KindAudiobook }

func playable(kind db.ItemKind) bool {
	return kind == db.KindMovie || kind == db.KindEpisode || audioKind(kind)
}

func prefText(prefs map[string]string, key string) string { return strings.Trim(prefs[key], `"`) }

// savedQuality is the quality a start gets when it names none.
func savedQuality(prefs map[string]string, kind db.ItemKind, net string, menu []QualityOption) Quality {
	keys := map[string][2]string{
		NetworkLocal: {PrefQualityHome, PrefMusicHome}, NetworkRemote: {PrefQualityRemote, PrefMusicRemote},
		NetworkCellular: {PrefQualityCellular, PrefMusicCellular},
	}[net]
	music := 0
	if audioKind(kind) {
		music = 1
	}
	switch saved := prefText(prefs, keys[music]); saved {
	case QualityAutomatic, QualityCD:
		return Quality{Mode: saved}
	case QualityOriginal:
		return Quality{Mode: QualityOriginal}
	case "":
		if net != NetworkCellular {
			return Quality{Mode: QualityOriginal}
		}
		// The widest entry within the mobile-data default.
		for _, option := range menu {
			if music == 0 && option.MaxHeight > 0 && option.MaxHeight <= 720 ||
				music == 1 && option.MaxAudioBitrateKbps > 0 && option.MaxAudioBitrateKbps <= 320 {
				return optionQuality(option)
			}
		}
	default:
		for _, option := range menu {
			if option.ID == saved {
				return optionQuality(option)
			}
		}
	}
	return Quality{Mode: QualityOriginal}
}

func optionQuality(o QualityOption) Quality {
	return Quality{Mode: o.Mode, ID: o.ID, MaxVideoBitrateKbps: o.MaxVideoBitrateKbps, MaxHeight: o.MaxHeight,
		MaxAudioBitrateKbps: o.MaxAudioBitrateKbps}
}

// askedQuality checks a quality a request names against the menu and fills in what the named
// entry allows. current is the quality in force, for a step.
func askedQuality(q Quality, current Quality, menu []QualityOption) (Quality, error) {
	bad := &Invalid{Field: "quality", Message: "This is not a quality this title offers."}
	entry := func(id string) (Quality, bool) {
		for _, option := range menu {
			if option.ID == id && id != "" {
				return optionQuality(option), true
			}
		}
		return Quality{}, false
	}
	switch q.Mode {
	case QualityOriginal, QualityCD:
		return Quality{Mode: q.Mode}, nil
	case QualityLimit:
		if q.MaxVideoBitrateKbps < 0 || q.MaxHeight < 0 || q.MaxAudioBitrateKbps < 0 {
			return Quality{}, bad
		}
		return Quality{Mode: QualityLimit, MaxVideoBitrateKbps: q.MaxVideoBitrateKbps, MaxHeight: q.MaxHeight,
			MaxAudioBitrateKbps: q.MaxAudioBitrateKbps}, nil
	case QualityFixed:
		out, ok := entry(q.ID)
		if !ok {
			return Quality{}, bad
		}
		out.Mode = QualityFixed
		return out, nil
	case QualityAutomatic:
		id := q.ID
		if q.Step != "" {
			// One step from where the session is. Above the widest entry is the original.
			var steps []string
			for _, option := range menu {
				if option.Mode == QualityFixed && !option.SameAsOriginal {
					steps = append(steps, option.ID)
				}
			}
			at := -1
			for n, step := range steps {
				if current.ID == step {
					at = n
				}
			}
			switch q.Step {
			case "down":
				at = min(at+1, len(steps)-1)
			case "up":
				at--
			default:
				return Quality{}, bad
			}
			id = ""
			if at >= 0 {
				id = steps[at]
			}
		}
		if id == "" || id == QualityOriginal {
			return Quality{Mode: QualityAutomatic}, nil
		}
		out, ok := entry(id)
		if !ok {
			return Quality{}, bad
		}
		out.Mode = QualityAutomatic
		return out, nil
	}
	return Quality{}, bad
}

// resolved is one title planned for one device.
type resolved struct {
	item    db.PlaybackItem
	version db.PlaybackVersion
	choice  Choice
	plan    Plan
	network string
	menu    []QualityOption
	device  db.SessionDevice
	prefs   map[string]string
	// others are the title's other versions that would play on this device.
	others []Alternative
	// audioPicked and subtitlePicked say the request's own track differs from what the server
	// would have chosen: a pick to remember.
	audioPicked, subtitlePicked bool
}

func versionLabel(v db.PlaybackVersion) string {
	switch {
	case v.Name != "":
		return v.Name
	case v.Edition != "":
		return v.Edition
	case v.Height > 0:
		return strconv.Itoa(v.Height) + "p"
	}
	return strings.ToUpper(v.Container)
}

// better orders versions: the taller picture, then the higher bitrate.
func better(a, b db.PlaybackVersion) bool {
	if a.Height != b.Height {
		return a.Height > b.Height
	}
	return a.Bitrate > b.Bitrate
}

// resolve reads a title for a viewer and plans it for the device asking. With no version
// named it picks the best version the device plays as it is, otherwise the best one.
// current is the quality in force on the session being changed, if there is one.
func (s *Service) resolve(ctx context.Context, c Caller, device db.SessionDevice, itemID string, ask Ask, current *Choice) (resolved, error) {
	var out resolved
	item, err := s.db.PlaybackItem(ctx, c.Viewer, itemID)
	if err != nil {
		return out, err
	}
	if !playable(item.Kind) || len(item.Versions) == 0 {
		return out, &Unplayable{Err: ErrUnsupported}
	}
	var capabilities []byte
	if doc, _, err := s.db.DeviceCapabilities(ctx, c.Viewer.UserID, device.PublicID); err == nil {
		capabilities = []byte(doc)
	} else if !errors.Is(err, db.ErrNotFound) {
		return out, err
	}
	prefs, err := s.db.Preferences(ctx, c.Viewer.ProfileID, device.ID)
	if err != nil {
		return out, err
	}
	out.item, out.device, out.network, out.prefs = item, device, network(ask.Network, c.AtHome), prefs

	type attempt struct {
		version db.PlaybackVersion
		choice  Choice
		plan    Plan
		menu    []QualityOption
		err     error
		// What planning chose with the request's own tracks left out.
		autoAudio, autoSubtitle string
	}
	plan := func(version db.PlaybackVersion) attempt {
		a := attempt{version: version, menu: s.planner.Qualities(version, item.Kind, out.network)}
		if current != nil {
			// A session keeps its choices; a track the new version does not have is left open.
			a.choice = *current
			if _, ok := findTrack(version, db.StreamAudio, a.choice.AudioTrackID); !ok {
				a.choice.AudioTrackID = ""
			}
			if _, ok := findTrack(version, db.StreamSubtitle, a.choice.SubtitleTrackID); !ok {
				a.choice.SubtitleTrackID = ""
			}
		}
		// What is remembered for the title, unless this session already has its own choice.
		if current == nil {
			if t, ok := trackForPick(version, db.StreamAudio, item.AudioPick); ok && item.AudioPick != "" {
				a.choice.AudioTrackID = TrackID(t)
			}
			if item.SubtitlePick == db.SubtitlesOff {
				a.choice.SubtitlesOff = true
			} else if t, ok := trackForPick(version, db.StreamSubtitle, item.SubtitlePick); ok && item.SubtitlePick != "" {
				a.choice.SubtitleTrackID = TrackID(t)
			}
			a.choice.Quality = savedQuality(prefs, item.Kind, out.network, a.menu)
		}
		remembered := a.choice
		if ask.Audio != nil {
			if _, ok := findTrack(version, db.StreamAudio, *ask.Audio); !ok {
				a.err = &Invalid{Field: "audio.trackId", Message: "This version has no such audio track."}
				return a
			}
			a.choice.AudioTrackID = *ask.Audio
		}
		if ask.Subtitle != nil {
			a.choice.SubtitleTrackID, a.choice.SubtitlesOff = *ask.Subtitle, *ask.Subtitle == ""
			if _, ok := findTrack(version, db.StreamSubtitle, *ask.Subtitle); !ok && *ask.Subtitle != "" {
				a.err = &Invalid{Field: "subtitles.trackId", Message: "This version has no such subtitle track."}
				return a
			}
		}
		if ask.SubtitleDelivery != "" {
			a.choice.SubtitleDelivery = ask.SubtitleDelivery
		}
		if ask.SubtitleOffsetMs != nil {
			a.choice.SubtitleOffsetMs = *ask.SubtitleOffsetMs
		}
		if ask.Quality != nil {
			if a.choice.Quality, a.err = askedQuality(*ask.Quality, a.choice.Quality, a.menu); a.err != nil {
				return a
			}
		}
		failures, err := s.db.RouteFailures(ctx, device.ID, version.FileID, s.now())
		if err != nil {
			a.err = err
			return a
		}
		request := PlanRequest{Item: item, Version: version, Capabilities: capabilities, Choice: a.choice, Network: out.network,
			ThroughputKbps: ask.ThroughputKbps, Failures: failures, Preferences: prefs}
		a.plan, a.err = s.planner.Plan(request)
		if a.err == nil && current == nil && (ask.Audio != nil || ask.Subtitle != nil) {
			// Was the request's track a pick, or just what the server would have played anyway?
			request.Choice = remembered
			request.Choice.Quality = a.choice.Quality
			if auto, err := s.planner.Plan(request); err == nil {
				a.autoAudio = auto.AudioTrackID
				if auto.Subtitle != nil {
					a.autoSubtitle = auto.Subtitle.TrackID
				}
			}
		}
		return a
	}

	var chosen *attempt
	var attempts []attempt
	for _, version := range item.Versions {
		if ask.VersionID != "" && version.PublicID != ask.VersionID {
			continue
		}
		attempts = append(attempts, plan(version))
	}
	if len(attempts) == 0 {
		return out, &Invalid{Field: "versionId", Message: "This title has no such version."}
	}
	for n := range attempts {
		a := &attempts[n]
		switch {
		case a.err != nil:
		case chosen == nil:
			chosen = a
		case (a.plan.Route == RouteOriginal) != (chosen.plan.Route == RouteOriginal):
			if a.plan.Route == RouteOriginal {
				chosen = a
			}
		case better(a.version, chosen.version):
			chosen = a
		}
	}
	if chosen == nil {
		err := attempts[0].err
		for _, sentinel := range []error{ErrUnsupported, ErrConversionOff, ErrTooSlow, ErrUpdateRequired, ErrSourceUnavailable} {
			if !errors.Is(err, sentinel) {
				continue
			}
			refusal := &Unplayable{Err: sentinel}
			// Where another version would play here, it is offered.
			if ask.VersionID != "" {
				for _, version := range item.Versions {
					if version.PublicID == ask.VersionID {
						continue
					}
					if a := plan(version); a.err == nil {
						refusal.Alternatives = append(refusal.Alternatives, Alternative{version.PublicID, versionLabel(version)})
					}
				}
			}
			return out, refusal
		}
		return out, err
	}
	out.version, out.choice, out.plan, out.menu = chosen.version, chosen.choice, chosen.plan, chosen.menu
	out.audioPicked = ask.Audio != nil && current == nil && *ask.Audio != chosen.autoAudio
	out.subtitlePicked = ask.Subtitle != nil && current == nil && *ask.Subtitle != chosen.autoSubtitle
	for n := range attempts {
		if a := &attempts[n]; a != chosen && a.err == nil {
			out.others = append(out.others, Alternative{a.version.PublicID, versionLabel(a.version)})
		}
	}
	out.item.Versions = item.Versions
	return out, nil
}

// remember keeps the tracks a person picked, for the title and, for an episode, its show.
func (s *Service) remember(ctx context.Context, v db.Viewer, r resolved, audio, subtitle bool) {
	var audioPick, subtitlePick *string
	if audio {
		if t, ok := findTrack(r.version, db.StreamAudio, r.choice.AudioTrackID); ok {
			pick := pickOf(t)
			audioPick = &pick
		}
	}
	if subtitle {
		pick := db.SubtitlesOff
		if t, ok := findTrack(r.version, db.StreamSubtitle, r.choice.SubtitleTrackID); ok && !r.choice.SubtitlesOff {
			pick = pickOf(t)
		}
		subtitlePick = &pick
	}
	if err := s.db.RememberTracks(ctx, v, r.item.ID, audioPick, subtitlePick, s.now()); err != nil {
		s.log.Error("remembering a track choice failed", "error", err)
	}
}

// Options is what a title can be played as on the device asking, and what a start that
// names nothing would play.
type Options struct {
	Item      db.PlaybackItem
	Version   db.PlaybackVersion
	Choice    Choice
	Plan      Plan
	Qualities []QualityOption
	Others    []Alternative
	// ResumeMs is where a start would resume, after "Rewind when resuming".
	ResumeMs int64
}

// Options previews a start: nothing is created.
func (s *Service) Options(ctx context.Context, c Caller, itemID string, ask Ask) (Options, error) {
	device, err := s.db.SessionDevice(ctx, c.SignInID)
	if err != nil {
		return Options{}, err
	}
	r, err := s.resolve(ctx, c, device, itemID, ask, nil)
	if err != nil {
		return Options{}, err
	}
	return Options{Item: r.item, Version: r.version, Choice: r.choice, Plan: r.plan, Qualities: r.menu, Others: r.others,
		ResumeMs: resumeAt(r.item, r.prefs)}, nil
}

// resumeAt is where a title resumes: where the viewer stopped, less "Rewind when resuming".
func resumeAt(item db.PlaybackItem, prefs map[string]string) int64 {
	rewind, _ := strconv.ParseInt(prefText(prefs, PrefResumeRewind), 10, 64)
	if rewind < 0 || rewind > 600 {
		rewind = 0
	}
	return max(item.PositionMS-rewind*1000, 0)
}
