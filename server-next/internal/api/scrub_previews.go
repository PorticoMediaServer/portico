package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/artwork"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server-next/internal/segments"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

// ScrubPreviews is one version's navigation pictures. Unavailable is an ordinary scrubber
// with no pictures, never a request to start analysis. Sheets are protected URLs on this route.
type ScrubPreviews struct {
	Version     string   `json:"version"`
	Available   bool     `json:"available"`
	DurationMS  int64    `json:"durationMs,omitempty"`
	IntervalMS  float64  `json:"intervalMs,omitempty"`
	FrameWidth  int      `json:"frameWidth,omitempty"`
	FrameHeight int      `json:"frameHeight,omitempty"`
	Columns     int      `json:"columns,omitempty"`
	Rows        int      `json:"rows,omitempty"`
	FrameCount  int      `json:"frameCount,omitempty"`
	Sheets      []string `json:"sheets,omitempty"`
}

func (s *Server) scrubPreviewJobs() {
	s.jobs.Register(jobs.Kind{Name: "scrub_previews", Access: jobs.Internal, Run: s.makeScrubPreviews})
	s.jobs.Tidy("scrub previews", time.Minute, s.queueScrubPreviews)
	route := &Route{Pattern: "GET /v1/items/{id}/scrub-previews", Access: WithProfile, Scope: ScopeRead, Response: reflect.TypeFor[ScrubPreviews]()}
	s.routes = append(s.routes, route)
	s.mux.HandleFunc(route.Pattern, func(w http.ResponseWriter, r *http.Request) {
		r, err := s.admit(r, route)
		if err == nil {
			err = s.scrubPreviews(w, r)
		}
		if err != nil {
			s.writeError(w, r, err)
		}
	})
}

func (s *Server) scrubPreviews(w http.ResponseWriter, r *http.Request) error {
	p, err := s.db.ScrubPreviewFor(r.Context(), viewer(r), r.PathValue("id"), r.URL.Query().Get("version"))
	if err != nil {
		return refuse(err)
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Query().Has("sheet") {
		n, err := strconv.Atoi(r.URL.Query().Get("sheet"))
		if err != nil || n < 0 || n >= len(p.Sheets) {
			return ErrNotFound()
		}
		data, err := s.artwork.ReadSheet(p.Sheets[n])
		if errors.Is(err, artwork.ErrNotFound) {
			return ErrNotFound()
		}
		if err != nil {
			return err
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, err = w.Write(data)
		return err
	}
	out := ScrubPreviews{Version: p.Version, Available: p.FrameCount > 0 && len(p.Sheets) > 0}
	if out.Available {
		out.DurationMS, out.IntervalMS = p.DurationMS, float64(p.IntervalUS)/1000
		out.FrameWidth, out.FrameHeight, out.Columns, out.Rows, out.FrameCount = p.FrameWidth, p.FrameHeight, p.Columns, p.Rows, p.FrameCount
		for n := range p.Sheets {
			out.Sheets = append(out.Sheets, "/v1/items/"+r.PathValue("id")+"/scrub-previews?version="+url.QueryEscape(p.Version)+"&sheet="+strconv.Itoa(n))
		}
	}
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(out)
}

func (s *Server) queueScrubPreviews(ctx context.Context) error {
	supervisor, ffmpeg, err := s.analysisTools(ctx)
	if err != nil || supervisor == nil || ffmpeg == "" {
		return err
	}
	libraries, err := s.db.AllLibraries(ctx)
	if err != nil {
		return err
	}
	for _, lib := range libraries {
		if !lib.Options.ScrubPreviews || lib.Options.Depth != db.DepthComplete {
			continue
		}
		entries, err := s.db.ScrubPreviewEntries(ctx, lib.PublicID, 0)
		if err != nil {
			return err
		}
		if len(entries) > 0 {
			if _, err = s.jobs.Start(ctx, "scrub_previews", jobs.Request{Target: jobs.Target{Library: lib.PublicID}}); err != nil {
				return err
			}
		}
	}
	return nil
}

// This lane never runs on the scan/request path. The existing confined analysis decoder
// uses low priority and independent background capacity, leaving Play and scan probes room.
func (s *Server) makeScrubPreviews(ctx context.Context, work jobs.Work) error {
	var after, done int64
	for {
		lib, err := s.segmentLibrary(ctx, work.Target.Library)
		if errors.Is(err, db.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if !lib.Options.ScrubPreviews || lib.Options.Depth != db.DepthComplete {
			return nil
		}
		entries, err := s.db.ScrubPreviewEntries(ctx, lib.PublicID, after)
		if err != nil || len(entries) == 0 {
			return err
		}
		for _, e := range entries {
			for {
				if err := s.settings.WaitForBackground(ctx, s.now); err != nil {
					return err
				}
				open, next := settings.General.Get(s.settings).HeavyWork.Open(s.now())
				if open {
					break
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(min(time.Minute, max(time.Second, next.Sub(s.now())))):
				}
			}
			current, err := s.segmentLibrary(ctx, lib.PublicID)
			if err != nil {
				return err
			}
			if !current.Options.ScrubPreviews || current.Options.Depth != db.DepthComplete {
				return nil
			}
			supervisor, ffmpeg, err := s.analysisTools(ctx)
			if err != nil || supervisor == nil || ffmpeg == "" {
				return err
			}
			p := db.ScrubPreview{}
			if e.Parts == 1 && e.StartMS == 0 && e.EndMS == 0 && e.DurationMS > 0 {
				source, err := segments.OpenFile(ctx, s.remote.Open, e)
				if err != nil {
					after = e.MediaID
					continue
				} // offline or changed: a later pass tries again
				p, err = s.makeScrubPreview(ctx, e, mediaanalysis.FileDecode(supervisor, ffmpeg, nil, source, e.Size))
				source.Close()
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if err != nil {
					var disk *os.PathError
					if errors.As(err, &disk) {
						return err
					}
					p = db.ScrubPreview{}
				}
			}
			if err := s.db.SaveScrubPreview(ctx, e, p); err != nil {
				return err
			}
			after, done = e.MediaID, done+1
			work.Progress(done, 0)
		}
	}
}

func (s *Server) makeScrubPreview(ctx context.Context, e db.SegmentEntry, decode mediaanalysis.Decode) (db.ScrubPreview, error) {
	p := db.ScrubPreview{}
	descriptor, err := mediaanalysis.MakeTrickplay(ctx, decode, "scrub:"+e.VersionID, mediaanalysis.TrickplayRequest{
		DurationUS: e.DurationMS * 1000, SourceVideo: decoder.PreviewVideoFacts{DynamicRange: e.HDR}},
		func(kind, contentType string, index int, start, end int64, width, height int, data []byte) error {
			if kind != mediaanalysis.TrickplaySheetKind {
				return nil
			}
			sum := sha256.Sum256(append([]byte("scrub:"), data...))
			hash := hex.EncodeToString(sum[:])
			if err := s.artwork.PutSheet(hash, data); err != nil {
				return err
			}
			p.Sheets = append(p.Sheets, hash)
			return nil
		})
	if err != nil {
		return db.ScrubPreview{}, err
	}
	p.IntervalUS, err = strconv.ParseInt(descriptor.IntervalUS, 10, 64)
	p.FrameWidth, p.FrameHeight, p.Columns, p.Rows, p.FrameCount = descriptor.TileWidth, descriptor.TileHeight, descriptor.Columns, descriptor.Rows, descriptor.FrameCount
	return p, err
}
