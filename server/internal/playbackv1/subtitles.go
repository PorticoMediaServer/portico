package playbackv1

import (
	"context"
	"errors"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// subtitleResult is what a subtitle selection did to a presentation: the
// sidecar files the client loads, the track in force and how it reaches the
// screen, and, when the selection changed the bytes (burn-in on, or off
// again), the presentation's new stream (a new grant and media generation on
// the same playback session).
type subtitleResult struct {
	files    []SubtitleFile
	shown    *PresentationSubtitle
	language string
	replaced *subtitles.Presentation
}

// applySubtitles selects subtitles on one presentation through the subtitle
// service (spec §5.5) at positionMs.
//
//   - nil request: the automatic choice the presentation was created with
//     (profile language, forced tracks) stands.
//   - trackId null: off (a burned presentation goes back to its plain stream).
//   - a text track: a WebVTT sidecar URL under the presentation's grant;
//     delivery "burn" for a text track is refused (only image and styled
//     tracks burn).
//   - an image or styled track: drawn by the device when the engine playing
//     the presentation draws that kind itself (draws, decided from the
//     capability document) or the client insists (delivery embeddedClient):
//     the device is handed the original file beside the stream (or reads the
//     track from the file it is playing). A styled track is otherwise shown as
//     its plain words in a text sidecar when the client asks for delivery
//     "sidecar" or the viewer turned "Styled subtitles" off. Anything else is
//     burned into the video (a conversion, and a new presentation); delivery
//     "sidecar" for an image track is refused. Burn-in needs converting allowed.
func (s *Service) applySubtitles(ctx context.Context, p identity.Principal, item, media string, req *SubtitleRequest, offsetMs, positionMs int64, operation string, draws bool, exactOffset ...string) (subtitleResult, error) {
	out := subtitleResult{files: []SubtitleFile{}}
	if s.Subtitles == nil || media == "" {
		return out, nil
	}
	plan, err := s.Subtitles.Plan(ctx, p, item, media)
	if err != nil {
		return out, nil // A title without a subtitle catalog plays without subtitles.
	}
	if req != nil {
		m := subtitles.SelectRequest{OperationID: operation, Generation: plan.Generation, ExpectedRevision: plan.Revision, Mode: "off", OffsetUS: "0", PositionUS: strconv.FormatInt(max(positionMs, 0)*1000, 10)}
		if req.TrackID != TrackOff && req.TrackID != "" {
			var chosen *subtitles.Resource
			for i := range plan.Resources {
				if plan.Resources[i].ID == string(req.TrackID) {
					chosen = &plan.Resources[i]
				}
			}
			if chosen == nil || !chosen.Enabled {
				return out, &FieldError{Path: "subtitles.trackId"}
			}
			burns := chosen.Renderer == "burn_in"
			out.language = chosen.Language
			switch {
			case req.Delivery == "burn" && !burns, req.Delivery == "sidecar" && burns && subtitleKindOf(chosen.Format) != playback.SubtitleASS:
				return out, &FieldError{Path: "subtitles.delivery"}
			case req.Delivery == "sidecar" && burns:
				// The device shows the styled track's words as plain text.
				m.Rendering = "text"
			case req.Delivery == "embeddedClient":
				// The client draws the track itself: it is handed the original
				// file beside the stream instead of a burned-in picture.
				m.Rendering = subtitles.RenderingFormat(chosen.Format)
			case burns && draws && req.Delivery != "burn":
				// The engine playing this presentation draws this kind itself
				// (the capability document says so). It gets the original file
				// where the server can deliver one; otherwise it reads the track
				// from the file it is playing, and a track that was burned in or
				// drawn as text before is turned off on the server first, so the
				// picture carries no second copy.
				if rendering := subtitles.RenderingFormat(chosen.Format); rendering != "text" {
					m.Rendering = rendering
					break
				}
				if plan.Mode != "off" && plan.Selected != nil {
					if off, offErr := s.Subtitles.Select(ctx, p, item, media, m); offErr == nil {
						out.replaced = off.Presentation
					}
				}
				out.shown = &PresentationSubtitle{TrackID: chosen.ID, Kind: subtitleKindOf(chosen.Format), Delivery: "device"}
				return out, nil
			case styledAsText(chosen, req, s.keepsStyling(ctx, p, chosen)):
				// "Styled subtitles" is off and this engine cannot draw the
				// style: the words, not a burned-in picture.
				m.Rendering = "text"
			}
			m.Mode, m.ResourceID, m.ResourceRevision = "track", chosen.ID, chosen.Revision
			m.OffsetUS = strconv.FormatInt(offsetMs*1000, 10)
			if len(exactOffset) > 0 && exactOffset[0] != "" {
				m.OffsetUS = exactOffset[0]
			}
		}
		priorGeneration := plan.Generation
		if plan, err = s.Subtitles.Select(ctx, p, item, media, m); err != nil {
			if errors.Is(err, subtitles.ErrUnsupported) {
				return out, playback.ErrTranscodingDisabled
			}
			// A re-plan that was refused is not a bad track id.
			if errors.Is(err, playback.ErrQualityUnavailable) {
				return out, err
			}
			return out, &FieldError{Path: "subtitles.trackId"}
		}
		if plan.Generation != priorGeneration {
			out.replaced = plan.Presentation
		}
	}
	return subtitlePlanResult(plan, out), nil
}

// Shared metadata projection only: it performs no source/cache/helper work.
// Private disc starts call it after their exact snapshot activation in the
// actual V1 writer, retaining the original borrow until outer COMMIT.
func subtitlePlanResult(plan subtitles.PlaybackPlan, out subtitleResult) subtitleResult {
	if plan.Selected != nil {
		out.language = plan.Selected.Language
		switch {
		case plan.DocumentURL != "" && plan.Renderer == "external_text":
			out.files = append(out.files, SubtitleFile{TimeDomain: "source-relative", TrackID: plan.Selected.ID, Format: "webvtt", URL: plan.DocumentURL + ".vtt"})
			out.shown = &PresentationSubtitle{TrackID: plan.Selected.ID, Kind: playback.SubtitleText, Delivery: "sidecar"}
		case plan.Renderer == "client":
			// The original file, for a device that draws styled or image subtitles itself.
			for _, d := range plan.Selected.Deliveries {
				if d.Format != "text" {
					offsetUS, _ := strconv.ParseInt(plan.OffsetUS, 10, 64)
					out.files = append(out.files, SubtitleFile{TimeDomain: "source-relative", TrackID: plan.Selected.ID, Format: d.Format, OffsetMs: offsetUS / 1000, URL: d.URL, CompanionURL: d.CompanionURL, FontsURL: d.FontsURL, LinesURL: d.LinesURL})
				}
			}
			out.shown = &PresentationSubtitle{TrackID: plan.Selected.ID, Kind: subtitleKindOf(plan.Selected.Format), Delivery: "device"}
		case plan.Renderer == "burn_in":
			out.shown = &PresentationSubtitle{TrackID: plan.Selected.ID, Kind: subtitleKindOf(plan.Selected.Format), Delivery: "burn"}
		}
	}
	return out
}
