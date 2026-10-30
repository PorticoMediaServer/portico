package mediaanalysis

import (
	"bytes"
	"context"
	"errors"
	"image/jpeg"
	"io"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
)

const EpisodeThumbnailCostClass = "decode"
const EpisodeThumbnailDefaultMode = "provider_then_made"
const EpisodeThumbnailProviderMode = "provider"
const EpisodeThumbnailOffMode = "off"

// EpisodeThumbnailRequest is the seam consumed by the effective library/source
// policy owner. Authorize must recheck the effective library/source intersection
// for the supplied operation, cost class and automatic/explicit invocation.
// Open acquires a retained range reader only after permission is granted.
// Automatic provider/off requests never acquire media. An explicit owner request
// can generate under every stored mode, without changing that mode or scan depth.
// The publication transaction must independently recheck the same authority.
type EpisodeThumbnailRequest struct {
	Mode          string
	Manual        bool
	ProviderStill bool
	DurationUS    int64
	StartUS       int64
	EndUS         int64
	Open          func(context.Context) (Input, error)
	Decoder       func(Input) Decode
	Authorize     func(ctx context.Context, operation, costClass string, manual bool) error
	SourceVideo   decoder.PreviewVideoFacts
}
type EpisodeThumbnailResult struct {
	Origin        string
	JPEG          []byte
	Width, Height int
	PositionUS    int64
}

// MakeEpisodeThumbnail produces exactly one bounded picture, or chooses an
// already available provider still for automatic work. Manual generation ignores
// the automatic mode restriction and provider presence, but still requires fresh
// explicit-owner source authority. ProviderStill describes an already permitted
// provider picture; this function never performs a lookup.
func MakeEpisodeThumbnail(ctx context.Context, r EpisodeThumbnailRequest) (EpisodeThumbnailResult, error) {
	out := EpisodeThumbnailResult{Origin: "fallback"}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	switch r.Mode {
	case EpisodeThumbnailOffMode, EpisodeThumbnailProviderMode, EpisodeThumbnailDefaultMode:
	default:
		return out, ErrInput
	}
	if !r.Manual {
		if r.Mode == EpisodeThumbnailOffMode {
			return out, nil
		}
		if r.ProviderStill {
			out.Origin = "provider"
			return out, nil
		}
		if r.Mode == EpisodeThumbnailProviderMode {
			return out, nil
		}
	}
	if r.Open == nil || r.Decoder == nil || r.Authorize == nil || r.DurationUS <= 0 || r.DurationUS > (1<<53)-1 || r.StartUS < 0 || r.EndUS <= r.StartUS || r.EndUS > r.DurationUS {
		return out, ErrInput
	}
	authorize := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return r.Authorize(ctx, catalog.EpisodeThumbnailOperation, EpisodeThumbnailCostClass, r.Manual)
	}
	if err := authorize(); err != nil {
		return out, err
	}
	input, err := r.Open(ctx)
	if err != nil {
		return out, err
	}
	if input == nil {
		return out, ErrInput
	}
	defer input.Close()
	if err := input.Validate(ctx); err != nil {
		return out, err
	}
	if err := authorize(); err != nil {
		return out, err
	}
	// Ten percent avoids most opening cards; a short episode still has a valid
	// seek strictly inside its duration. Decode uses the existing ranged bridge.
	position := min(r.EndUS-1, r.StartUS+(r.EndUS-r.StartUS)/10)
	spec := decoder.AnalysisSpec{Kind: "image", StartUS: position, MaxDurationUS: r.DurationUS, SourceVideo: r.SourceVideo}
	var data []byte
	decode := r.Decoder(input)
	if decode == nil {
		return out, ErrUnsupported
	}
	err = decode(ctx, catalog.EpisodeThumbnailOperation, spec, func(reader io.Reader) error {
		var err error
		data, err = io.ReadAll(io.LimitReader(reader, (1<<20)+1))
		if len(data) > 1<<20 {
			return ErrBudget
		}
		return err
	})
	// A failed decode can be the first sign of a replaced source. Check both
	// retained metadata and authority before closing, even when decoding failed.
	// Keep the original invocation context: cancellation cannot grant more work.
	var validationErr error
	if retained, ok := input.(interface{ StopAndRecheck(context.Context) error }); err != nil && ok {
		// Retained remote readers must stop decoder children before observing
		// fresh metadata on the same captured process. This is terminal for
		// this reader; it cannot acquire replacement media or retry decoding.
		validationErr = retained.StopAndRecheck(ctx)
	} else {
		validationErr = input.Validate(ctx)
	}
	authorizationErr := r.Authorize(ctx, catalog.EpisodeThumbnailOperation, EpisodeThumbnailCostClass, r.Manual)
	if err = errors.Join(err, validationErr, authorizationErr, ctx.Err()); err != nil {
		return out, err
	}
	shape, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || shape.Width != 320 || shape.Height != 180 {
		return out, ErrUnsupported
	}
	if _, err = jpeg.Decode(bytes.NewReader(data)); err != nil {
		return out, ErrUnsupported
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	out = EpisodeThumbnailResult{Origin: "generated", JPEG: data, Width: 320, Height: 180, PositionUS: position}
	return out, nil
}
