// Package segments runs intro and credits detection and preserves its evidence.
// Background work uses the existing job runner, decoder supervisor and owner hours.
package segments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
	"github.com/PorticoMediaServer/portico/server-next/internal/libraryfile"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server-next/internal/processpool"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
	"github.com/PorticoMediaServer/portico/server-next/internal/segmentdetect"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
	"io"
	"path"
	"time"
)

const JobKind = "segment_detection"

type Service struct {
	DB           *db.DB
	Settings     *settings.Settings
	Tools        func(context.Context) (*processpool.Supervisor, string, error)
	Remote       func(context.Context, int64) (remote.Storage, time.Duration, error)
	Now          func() time.Time
	readEvidence func(context.Context, db.SegmentEntry, string) (segmentdetect.Evidence, []segmentdetect.Detection, error)
}
type measurement struct {
	Evidence    segmentdetect.Evidence
	Suggestions []segmentdetect.Detection
}

func (s *Service) Mode(lib db.Library) string {
	mode := lib.Options.SegmentDetection
	if mode == "" || mode == "server" {
		mode = settings.Segments.Get(s.Settings).Mode
	}
	return mode
}

// MeasurementMode changes only when the work itself changes, not when the
// owner changes a viewer confidence floor. Existing measurements then queue
// again through the same source/mode comparison, without another revision table.
func (s *Service) MeasurementMode(lib db.Library) string {
	mode := s.Mode(lib)
	c := settings.Segments.Get(s.Settings)
	if mode == "off" || c.IntroWindow == "usual" && c.CreditsWindow == "usual" && c.FilmCreditsWindow == "usual" &&
		c.ShortestEpisodeMinutes == 2 && c.ShortestFilmMinutes == 10 && c.UseChapterNames {
		return mode
	}
	return fmt.Sprintf("%s:%s:%s:%s:%d:%d:%t", mode, c.IntroWindow, c.CreditsWindow, c.FilmCreditsWindow, c.ShortestEpisodeMinutes, c.ShortestFilmMinutes, c.UseChapterNames)
}
func (s *Service) Kind() jobs.Kind {
	return jobs.Kind{Name: JobKind, Access: jobs.Internal, Lane: "heavy-segments", Run: s.run}
}
func (s *Service) library(ctx context.Context, id string) (db.Library, error) {
	libs, err := s.DB.AllLibraries(ctx)
	for _, lib := range libs {
		if lib.PublicID == id {
			return lib, nil
		}
	}
	if err != nil {
		return db.Library{}, err
	}
	return db.Library{}, db.ErrNotFound
}
func (s *Service) wait(ctx context.Context) error {
	for {
		if err := s.Settings.WaitForBackground(ctx, s.Now); err != nil {
			return err
		}
		open, next := settings.General.Get(s.Settings).HeavyWork.Open(s.Now())
		if open {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(min(time.Minute, max(time.Second, next.Sub(s.Now())))):
		}
	}
}
func (s *Service) run(ctx context.Context, work jobs.Work) error {
	lib, err := s.library(ctx, work.Target.Library)
	if err != nil {
		return err
	}
	mode := s.Mode(lib)
	measuredMode := s.MeasurementMode(lib)
	if mode == "off" {
		return nil
	}
	read := s.readEvidence
	if read == nil {
		read = s.read
	}
	changed := map[string]bool{}
	var after, done int64
	counts, _, err := s.DB.SegmentCounts(ctx, lib.PublicID, "", measuredMode)
	if err != nil {
		return err
	}
	for {
		entries, err := s.DB.SegmentPendingEntries(ctx, lib.PublicID, measuredMode, after)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			break
		}
		for _, e := range entries {
			if err := s.wait(ctx); err != nil {
				return err
			}
			current, err := s.library(ctx, lib.PublicID)
			if err != nil {
				return err
			}
			if s.MeasurementMode(current) != measuredMode {
				return nil
			}
			state, problem := "complete", ""
			var evidence segmentdetect.Evidence
			var found []segmentdetect.Detection
			if e.Parts != 1 || e.StartMS != 0 || e.EndMS != 0 || e.DurationMS <= 0 {
				state, problem = "unsupported", "Intros and credits cannot be measured for this file layout."
			} else {
				evidence, found, err = read(ctx, e, mode)
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if err != nil {
					state, problem = "failed", "This file's intro and credits could not be read."
				}
			}
			if errors.Is(err, errNoDecoder) {
				return jobs.DoneWith{Note: "Intro and credits detection needs a working FFmpeg."}
			}
			raw, _ := json.Marshal(measurement{evidence, found})
			if err = s.DB.SaveSegmentAnalysis(ctx, e, measuredMode, state, problem, raw, markers(found)); err != nil {
				return err
			}
			changed[e.TopID] = true
			after = e.MediaID
			done++
			work.Progress(done, int64(counts.Pending))
		}
	}
	// New evidence can confirm older neighbours too, so revisit each changed show.
	for title := range changed {
		after = 0
		for {
			entries, err := s.DB.SegmentEntries(ctx, lib.PublicID, title, after, 25)
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				break
			}
			for _, e := range entries {
				after = e.MediaID
				if e.Kind != "episode" || e.State != "complete" || e.Mode != measuredMode || e.SourceKey != e.Key() {
					continue
				}
				if err := s.wait(ctx); err != nil {
					return err
				}
				current, err := s.library(ctx, lib.PublicID)
				if err != nil {
					return err
				}
				if s.MeasurementMode(current) != measuredMode {
					return nil
				}
				var self measurement
				if json.Unmarshal(e.Evidence, &self) != nil {
					continue
				}
				neighbours, err := s.DB.SegmentNeighbours(ctx, e, measuredMode)
				if err != nil {
					return err
				}
				var evidence []segmentdetect.Evidence
				for _, n := range neighbours {
					if n.Season != e.Season && len(evidence) >= 4 {
						continue
					}
					var measured measurement
					if n.SourceKey == n.Key() && json.Unmarshal(n.Evidence, &measured) == nil {
						evidence = append(evidence, measured.Evidence)
					}
				}
				found := append([]segmentdetect.Detection{}, self.Suggestions...)
				found = append(found, mediaanalysis.CompareSegments(self.Evidence, evidence)...)
				if err = s.DB.SaveSegmentComparison(ctx, e, measuredMode, e.Evidence, markers(found)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

var errNoDecoder = errors.New("no decoder")

func (s *Service) read(ctx context.Context, e db.SegmentEntry, mode string) (segmentdetect.Evidence, []segmentdetect.Detection, error) {
	empty := segmentdetect.Evidence{}
	supervisor, ffmpeg, err := s.Tools(ctx)
	if err != nil {
		return empty, nil, err
	}
	if supervisor == nil || ffmpeg == "" {
		return empty, nil, errNoDecoder
	}
	var file interface {
		io.ReaderAt
		io.Closer
	}
	if e.RemoteID != 0 && s.Remote != nil {
		storage, _, openErr := s.Remote(ctx, e.RemoteID)
		if openErr != nil {
			return empty, nil, openErr
		}
		remoteFile, openErr := storage.Open(ctx, path.Join(e.Root, e.Relative))
		if openErr != nil {
			return empty, nil, openErr
		}
		if remoteFile.Size() != e.Size {
			remoteFile.Close()
			return empty, nil, db.ErrConflict
		}
		file = remoteFile
	} else {
		local, openErr := libraryfile.Open(e.Path(), []string{e.Root})
		if openErr != nil {
			return empty, nil, openErr
		}
		info, openErr := local.Stat()
		if openErr != nil || info.Size() != e.Size || info.ModTime().UnixNano() != e.ModifiedNS {
			local.Close()
			return empty, nil, db.ErrConflict
		}
		file = local
	}
	defer file.Close()
	chapters, err := s.DB.SegmentChapters(ctx, e.MediaID)
	if err != nil {
		return empty, nil, err
	}
	var names []mediaanalysis.SegmentChapter
	c := settings.Segments.Get(s.Settings)
	for _, c := range chapters {
		names = append(names, mediaanalysis.SegmentChapter{Title: c.Title, StartUS: c.StartMS * 1000, EndUS: c.EndMS * 1000})
	}
	if !c.UseChapterNames {
		names = nil
	}
	decode := mediaanalysis.FileDecode(supervisor, ffmpeg, nil, file, e.Size)
	picture := decoder.PreviewVideoFacts{DynamicRange: e.HDR}
	policy := mediaanalysis.SegmentPlanningOptions{EpisodeMinutes: int64(c.ShortestEpisodeMinutes), FilmMinutes: int64(c.ShortestFilmMinutes),
		IntroWindow: c.IntroWindow, CreditsWindow: c.CreditsWindow, FilmCreditsWindow: c.FilmCreditsWindow}
	return mediaanalysis.ReadSegments(ctx, decode, fmt.Sprint("segments:", e.MediaID), e.Kind, mode, e.DurationMS*1000, e.Audio, e.Video, picture, names, policy)
}
func markers(found []segmentdetect.Detection) []db.SegmentMarker {
	out := make([]db.SegmentMarker, 0, len(found))
	byKind := map[db.MarkerKind]int{}
	for _, d := range found {
		var kind db.MarkerKind
		switch d.Kind {
		case "intro":
			kind = db.MarkerIntro
		case "recap":
			kind = db.MarkerRecap
		case "credits":
			kind = db.MarkerCredits
		case "outro":
			kind = db.MarkerOutro
		default:
			continue
		}
		source := db.MarkerFromAnalysis
		if d.Provenance == segmentdetect.IntroProvenance || d.Provenance == segmentdetect.CreditsProvenance {
			source = db.MarkerConfirmed
		}
		marker := db.SegmentMarker{Kind: kind, Source: source, StartMS: d.StartUS / 1000, EndMS: (d.EndUS + 999) / 1000, Confidence: d.Confidence, Provenance: d.Provenance}
		if index, ok := byKind[kind]; ok {
			if out[index].Source != db.MarkerConfirmed && (source == db.MarkerConfirmed || marker.Confidence > out[index].Confidence) {
				out[index] = marker
			}
		} else {
			byKind[kind] = len(out)
			out = append(out, marker)
		}
	}
	return out
}
