package mediaanalysis

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
)

type terminalThumbnailInput struct {
	thumbnailRangeInput
	recheck func(context.Context) error
}

func (i *terminalThumbnailInput) StopAndRecheck(ctx context.Context) error { return i.recheck(ctx) }

func TestEpisodeThumbnailRetainedFailureStopsAndFreshlyRechecksSameReader(t *testing.T) {
	decodeErr, changedErr := errors.New("decoder failed"), errors.New("retained source changed")
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "changed source", true: "cancelled invocation"}[cancelled], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.WithValue(context.Background(), struct{}{}, "same invocation"))
			defer cancel()
			input := &terminalThumbnailInput{}
			decodes, rechecks, authorizations := 0, 0, 0
			input.recheck = func(check context.Context) error {
				rechecks++
				if check != ctx || decodes != 1 || input.validated != 1 || input.closed != 0 {
					t.Fatal("fresh observation lost the invocation or reader lifetime", check, input)
				}
				return errors.Join(changedErr, check.Err())
			}
			request := EpisodeThumbnailRequest{Mode: EpisodeThumbnailDefaultMode, DurationUS: requestDurationUS, EndUS: requestDurationUS,
				Open: func(context.Context) (Input, error) { return input, nil },
				Authorize: func(check context.Context, _ string, _ string, _ bool) error {
					authorizations++
					if check != ctx {
						t.Fatal("authority detached from the failed invocation")
					}
					return check.Err()
				},
				Decoder: func(Input) Decode {
					return func(_ context.Context, _ string, _ decoder.AnalysisSpec, consume func(io.Reader) error) error {
						decodes++
						if err := consume(bytes.NewReader(thumbnailJPEG(320, 180))); err != nil {
							t.Fatal(err)
						}
						if cancelled {
							cancel()
						}
						return decodeErr
					}
				},
			}
			out, err := MakeEpisodeThumbnail(ctx, request)
			if !errors.Is(err, decodeErr) || !errors.Is(err, changedErr) || errors.Is(err, context.Canceled) != cancelled || len(out.JPEG) != 0 || out.Origin != "fallback" || rechecks != 1 || decodes != 1 || authorizations != 3 || input.closed != 1 || input.validated != 1 {
				t.Fatalf("failed retained reader escaped, retried or lost errors: %+v err=%v input=%+v decode=%d recheck=%d auth=%d", out, err, input, decodes, rechecks, authorizations)
			}
		})
	}
}

func TestEpisodeThumbnailSuccessfulDecodeKeepsRetainedReaderForPublication(t *testing.T) {
	input := &terminalThumbnailInput{recheck: func(context.Context) error { t.Fatal("successful decode stopped the retained source"); return nil }}
	request := EpisodeThumbnailRequest{Mode: EpisodeThumbnailDefaultMode, DurationUS: requestDurationUS, EndUS: requestDurationUS,
		Open: func(context.Context) (Input, error) { return input, nil }, Authorize: thumbnailAuthorization,
		Decoder: func(Input) Decode {
			return func(_ context.Context, _ string, _ decoder.AnalysisSpec, consume func(io.Reader) error) error {
				return consume(bytes.NewReader(thumbnailJPEG(320, 180)))
			}
		},
	}
	if out, err := MakeEpisodeThumbnail(context.Background(), request); err != nil || out.Origin != "generated" || input.validated != 2 || input.closed != 1 {
		t.Fatal(out, err, input)
	}
}
