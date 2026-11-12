package api

import (
	"context"
	"net/http"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/musicanalysis"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

// SoundAnalysisStatus is a music library's Sonic analysis line in its Scanning tab.
type SoundAnalysisStatus struct {
	// Available says the server has the sound model: without it the switch is not offered
	// and State is cant_run.
	Available bool `json:"available"`
	// On is the library's switch (its soundAnalysis option).
	On bool `json:"on"`
	// State is analyzing, paused (outside the hours for heavy work, until PausedUntil), done,
	// off, or cant_run.
	State string `json:"state"`
	// Songs are the library's songs on this server's own folders, and Analyzed how many of
	// them the model has read.
	Songs    int `json:"songs"`
	Analyzed int `json:"analyzed"`
	// PausedUntil is when the hours for heavy work begin again, while paused.
	PausedUntil *time.Time `json:"pausedUntil,omitempty"`
	// SecondsLeft is about how long the rest will take at the current hours, from how fast
	// this server has been reading songs. Estimating says it is still working that out (the
	// first songs since the server started), and then there is no SecondsLeft.
	SecondsLeft *int64 `json:"secondsLeft,omitempty"`
	Estimating  bool   `json:"estimating,omitempty"`
}

// The values of SoundAnalysisStatus.State.
const (
	soundAnalyzing = "analyzing"
	soundPaused    = "paused"
	soundDone      = "done"
	soundOff       = "off"
	soundCantRun   = "cant_run"
)

func (s *Server) soundAnalysisStatus(r *http.Request, music *musicanalysis.Service) (SoundAnalysisStatus, error) {
	ctx := r.Context()
	lib, err := musicLibrary(ctx, s.db, r.PathValue("id"))
	if err != nil {
		return SoundAnalysisStatus{}, err
	}
	out := SoundAnalysisStatus{On: !lib.Options.NoSoundAnalysis, State: soundCantRun}
	runtime, _, _ := music.Runtime(ctx)
	if runtime == nil {
		return out, nil
	}
	out.Available = true
	if out.Songs, out.Analyzed, err = s.db.SoundAnalysisCounts(ctx, lib.ID, runtime.Identity().Version); err != nil {
		return SoundAnalysisStatus{}, err
	}
	perSong, known := music.PerSong()
	return describeSoundAnalysis(out, settings.General.Get(s.settings).HeavyWork, s.now(), perSong, known), nil
}

// describeSoundAnalysis fills in the state and the estimate from the counts, the hours for
// heavy work and how long a song takes here (known false while that is still being worked out).
func describeSoundAnalysis(out SoundAnalysisStatus, hours settings.HeavyWorkSettings, now time.Time, perSong time.Duration, known bool) SoundAnalysisStatus {
	switch {
	case !out.On:
		out.State = soundOff
		return out
	case out.Analyzed >= out.Songs:
		out.State = soundDone
		return out
	}
	out.State = soundAnalyzing
	if until, paused := musicanalysis.PausedUntil(hours, now); paused {
		out.State = soundPaused
		u := until.UTC()
		out.PausedUntil = &u
	}
	if !known {
		out.Estimating = true
		return out
	}
	left := int64(float64(time.Duration(out.Songs-out.Analyzed)*perSong) / hours.OpenShare() / float64(time.Second))
	out.SecondsLeft = &left
	return out
}

// musicLibrary finds a music library by its public id; any other id is not found.
func musicLibrary(ctx context.Context, d *db.DB, publicID string) (db.Library, error) {
	libraries, err := d.MusicLibraries(ctx)
	if err != nil {
		return db.Library{}, err
	}
	for _, l := range libraries {
		if l.PublicID == publicID {
			return l, nil
		}
	}
	return db.Library{}, ErrNotFound()
}
