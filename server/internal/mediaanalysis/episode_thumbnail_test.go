package mediaanalysis

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"reflect"
	"strconv"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
)

type thumbnailRangeInput struct {
	reads, validated, closed int
	changed                  bool
}

func (i *thumbnailRangeInput) Evidence() string { return "test-evidence" }
func (i *thumbnailRangeInput) Size() int64      { return 512 << 30 }
func (i *thumbnailRangeInput) ReadExtent(_ context.Context, off, n int64) ([]byte, error) {
	if n <= 0 || n > 1024 || off < 0 || off > i.Size()-n {
		return nil, ErrBudget
	}
	i.reads++
	return make([]byte, n), nil
}
func (i *thumbnailRangeInput) Validate(context.Context) error {
	i.validated++
	if i.changed {
		return ErrConflict
	}
	return nil
}
func (i *thumbnailRangeInput) Close() error { i.closed++; return nil }
func thumbnailJPEG(w, h int) []byte {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h)), nil); err != nil {
		panic(err)
	}
	return b.Bytes()
}
func thumbnailAuthorization(context.Context, string, string, bool) error { return nil }

func TestEpisodeThumbnailPolicyProviderAndRangedGeneration(t *testing.T) {
	input := &thumbnailRangeInput{}
	allowed, opened, decoded := 0, 0, 0
	expectedManual := false
	video := decoder.PreviewVideoFacts{DynamicRange: "hdr10"}
	request := EpisodeThumbnailRequest{
		Mode: EpisodeThumbnailDefaultMode, DurationUS: requestDurationUS, EndUS: requestDurationUS, SourceVideo: video,
		Open: func(context.Context) (Input, error) { opened++; return input, nil },
		Authorize: func(_ context.Context, operation, cost string, manual bool) error {
			if operation != "episode_thumbnail" || cost != "decode" || manual != expectedManual {
				t.Fatalf("authorization classified as %q/%q", operation, cost)
			}
			allowed++
			return nil
		},
		Decoder: func(in Input) Decode {
			if allowed != opened*3-1 || in != input {
				t.Fatalf("decoder acquired without current authority: auth=%d opens=%d", allowed, opened)
			}
			return func(ctx context.Context, key string, spec decoder.AnalysisSpec, consume func(io.Reader) error) error {
				decoded++
				if key != catalog.EpisodeThumbnailOperation || spec.Kind != "image" || spec.StartUS != 6000000 || spec.MaxDurationUS != requestDurationUS || spec.SourceVideo != video {
					t.Fatalf("recipe %+v %s", spec, key)
				}
				// Seek far into a large input, requesting only one small extent.
				if _, err := in.ReadExtent(ctx, 1<<30, 128); err != nil {
					return err
				}
				return consume(bytes.NewReader(thumbnailJPEG(320, 180)))
			}
		},
	}
	result, err := MakeEpisodeThumbnail(context.Background(), request)
	if err != nil || result.Origin != "generated" || len(result.JPEG) == 0 || result.PositionUS != 6000000 || decoded != 1 || allowed != 3 || opened != 1 || input.reads != 1 || input.validated != 2 || input.closed != 1 {
		t.Fatalf("result %+v err=%v input=%+v auth=%d opens=%d decodes=%d", result, err, input, allowed, opened, decoded)
	}
	request.ProviderStill = true
	result, err = MakeEpisodeThumbnail(context.Background(), request)
	if err != nil || result.Origin != "provider" || decoded != 1 || opened != 1 || allowed != 3 {
		t.Fatalf("provider path acquired decode work: %+v %v", result, err)
	}
	request.Manual = true
	expectedManual = true
	result, err = MakeEpisodeThumbnail(context.Background(), request)
	if err != nil || result.Origin != "generated" || decoded != 2 || opened != 2 || input.closed != 2 || allowed != 6 {
		t.Fatalf("manual path %+v %v", result, err)
	}
}

const requestDurationUS = 60000000

func TestEpisodeThumbnailAutomaticModesNeverAcquireForbiddenMedia(t *testing.T) {
	for _, mode := range []string{EpisodeThumbnailOffMode, EpisodeThumbnailProviderMode, EpisodeThumbnailDefaultMode} {
		for _, provider := range []bool{false, true} {
			if mode == EpisodeThumbnailDefaultMode && !provider {
				continue // Authorized automatic generation is covered separately.
			}
			r := EpisodeThumbnailRequest{Mode: mode, ProviderStill: provider,
				Authorize: func(context.Context, string, string, bool) error { t.Fatal("decode authority requested"); return nil },
				Open:      func(context.Context) (Input, error) { t.Fatal("media acquired"); return nil, nil },
				Decoder:   func(Input) Decode { t.Fatal("decoder acquired"); return nil },
			}
			result, err := MakeEpisodeThumbnail(context.Background(), r)
			want := "fallback"
			if provider && mode != EpisodeThumbnailOffMode {
				want = "provider"
			}
			if err != nil || result.Origin != want || len(result.JPEG) != 0 {
				t.Fatalf("mode=%s provider=%v: %+v %v", mode, provider, result, err)
			}
		}
	}
	for _, mode := range []string{"", "provider_then_generated", "provider_only", "Provider", " provider", "made"} {
		for _, manual := range []bool{false, true} {
			if _, err := MakeEpisodeThumbnail(context.Background(), EpisodeThumbnailRequest{Mode: mode, Manual: manual}); !errors.Is(err, ErrInput) {
				t.Fatalf("invalid mode %q manual=%v accepted: %v", mode, manual, err)
			}
		}
	}
}

func TestEpisodeThumbnailExplicitCommandGeneratesUnderEveryAutomaticMode(t *testing.T) {
	for _, mode := range []string{EpisodeThumbnailOffMode, EpisodeThumbnailProviderMode, EpisodeThumbnailDefaultMode} {
		for _, provider := range []bool{false, true} {
			t.Run(mode+"/provider="+strconv.FormatBool(provider), func(t *testing.T) {
				input := &thumbnailRangeInput{}
				authorized, opened, decoded := 0, 0, 0
				r := EpisodeThumbnailRequest{Mode: mode, Manual: true, ProviderStill: provider, DurationUS: requestDurationUS, EndUS: requestDurationUS,
					Authorize: func(_ context.Context, operation, cost string, manual bool) error {
						if operation != catalog.EpisodeThumbnailOperation || cost != EpisodeThumbnailCostClass || !manual {
							t.Fatal("explicit invocation used different authority")
						}
						authorized++
						return nil
					},
					Open: func(context.Context) (Input, error) { opened++; return input, nil },
					Decoder: func(in Input) Decode {
						return func(ctx context.Context, key string, spec decoder.AnalysisSpec, consume func(io.Reader) error) error {
							decoded++
							if key != catalog.EpisodeThumbnailOperation || spec.Kind != "image" || spec.StartUS != requestDurationUS/10 {
								t.Fatal("not one bounded image recipe")
							}
							if _, err := in.ReadExtent(ctx, 1<<30, 128); err != nil {
								return err
							}
							return consume(bytes.NewReader(thumbnailJPEG(320, 180)))
						}
					},
				}
				result, err := MakeEpisodeThumbnail(context.Background(), r)
				if err != nil || result.Origin != "generated" || len(result.JPEG) == 0 || opened != 1 || decoded != 1 || authorized != 3 || input.reads != 1 || input.closed != 1 || r.Mode != mode {
					t.Fatalf("mode=%s provider=%v result=%+v err=%v opens=%d decodes=%d auth=%d input=%+v", mode, provider, result, err, opened, decoded, authorized, input)
				}
			})
		}
	}
}

func TestEpisodeThumbnailDefaultModes(t *testing.T) {
	if EpisodeThumbnailDefaultMode != "provider_then_made" || EpisodeThumbnailProviderMode != "provider" || EpisodeThumbnailOffMode != "off" {
		t.Fatal("stored mode vocabulary changed")
	}
	for _, kind := range []string{"tv", "anime"} {
		if catalog.DefaultEpisodeThumbnailMode(kind) != "provider_then_made" {
			t.Fatalf("wrong episode default for %s", kind)
		}
	}
	for _, kind := range []string{"movie", "music", "audiobook", "book", "comic", "", "unknown"} {
		if catalog.DefaultEpisodeThumbnailMode(kind) != "off" {
			t.Fatalf("wrong non-episode default for %s", kind)
		}
	}
}

func TestEpisodeThumbnailRejectsPermissionRevocationAtEveryBoundary(t *testing.T) {
	denied := errors.New("source profile revoked decode")
	for _, mode := range []string{EpisodeThumbnailOffMode, EpisodeThumbnailProviderMode, EpisodeThumbnailDefaultMode} {
		for _, manual := range []bool{false, true} {
			if !manual && mode != EpisodeThumbnailDefaultMode {
				continue
			}
			for _, denyAt := range []int{1, 2, 3} {
				t.Run(mode+"/manual="+strconv.FormatBool(manual)+"/deny="+strconv.Itoa(denyAt), func(t *testing.T) {
					input := &thumbnailRangeInput{}
					authorized, opened, decoded := 0, 0, 0
					r := EpisodeThumbnailRequest{Mode: mode, Manual: manual, DurationUS: 10, EndUS: 10,
						Open: func(context.Context) (Input, error) { opened++; return input, nil },
						Authorize: func(_ context.Context, operation, cost string, explicit bool) error {
							if operation != catalog.EpisodeThumbnailOperation || cost != EpisodeThumbnailCostClass || explicit != manual {
								t.Fatal("wrong invocation/operation authorization scope")
							}
							authorized++
							if authorized == denyAt {
								return denied
							}
							return nil
						},
						Decoder: func(Input) Decode {
							decoded++
							return func(_ context.Context, _ string, _ decoder.AnalysisSpec, consume func(io.Reader) error) error {
								return consume(bytes.NewReader(thumbnailJPEG(320, 180)))
							}
						},
					}
					result, err := MakeEpisodeThumbnail(context.Background(), r)
					wantOpen, wantDecode := 0, 0
					if denyAt > 1 {
						wantOpen = 1
					}
					if denyAt > 2 {
						wantDecode = 1
					}
					if !errors.Is(err, denied) || len(result.JPEG) != 0 || opened != wantOpen || input.closed != wantOpen || decoded != wantDecode {
						t.Fatalf("deny=%d result=%+v err=%v opens=%d closed=%d decodes=%d", denyAt, result, err, opened, input.closed, decoded)
					}
				})
			}
		}
	}
}

func TestEpisodeThumbnailRejectsChangedOrInvalidOutput(t *testing.T) {
	valid := thumbnailJPEG(320, 180)
	for _, data := range [][]byte{[]byte("bad image"), thumbnailJPEG(1, 1), valid[:len(valid)/2], make([]byte, (1<<20)+1)} {
		input := &thumbnailRangeInput{}
		r := EpisodeThumbnailRequest{Mode: EpisodeThumbnailDefaultMode, DurationUS: 10, EndUS: 10,
			Open: func(context.Context) (Input, error) { return input, nil }, Authorize: thumbnailAuthorization,
			Decoder: func(Input) Decode {
				return func(_ context.Context, _ string, _ decoder.AnalysisSpec, consume func(io.Reader) error) error {
					return consume(bytes.NewReader(data))
				}
			},
		}
		if result, err := MakeEpisodeThumbnail(context.Background(), r); err == nil || len(result.JPEG) != 0 || input.closed != 1 {
			t.Fatalf("invalid image accepted or input leaked: %+v %v closed=%d", result, err, input.closed)
		}
	}
	for _, changeDuringDecode := range []bool{false, true} {
		input := &thumbnailRangeInput{changed: !changeDuringDecode}
		r := EpisodeThumbnailRequest{Mode: EpisodeThumbnailDefaultMode, DurationUS: 10, EndUS: 10,
			Open: func(context.Context) (Input, error) { return input, nil }, Authorize: thumbnailAuthorization,
			Decoder: func(Input) Decode {
				if !changeDuringDecode {
					t.Fatal("decoder acquired for changed source")
				}
				return func(_ context.Context, _ string, _ decoder.AnalysisSpec, consume func(io.Reader) error) error {
					input.changed = true
					return consume(bytes.NewReader(valid))
				}
			},
		}
		if result, err := MakeEpisodeThumbnail(context.Background(), r); !errors.Is(err, ErrConflict) || len(result.JPEG) != 0 || input.closed != 1 {
			t.Fatalf("changed source accepted or leaked: %+v %v", result, err)
		}
	}
}

type thumbnailFailureInput struct {
	thumbnailRangeInput
	events *[]string
	after  error
}

func (i *thumbnailFailureInput) Validate(ctx context.Context) error {
	*i.events = append(*i.events, "validate")
	i.validated++
	if i.closed != 0 {
		return errors.New("validation after close")
	}
	if i.validated > 1 {
		return errors.Join(i.after, ctx.Err())
	}
	return ctx.Err()
}

func (i *thumbnailFailureInput) Close() error {
	*i.events = append(*i.events, "close")
	return i.thumbnailRangeInput.Close()
}

func TestEpisodeThumbnailDecodeFailureRechecksThenTerminates(t *testing.T) {
	decodeErr := errors.New("decoder failed after writing a frame")
	metadataErr := errors.New("remote identity changed")
	authorityErr := errors.New("source authority revoked")
	for _, tc := range []struct {
		name                     string
		changed, revoked, cancel bool
	}{
		{name: "decode_error"},
		{name: "identity_changed", changed: true},
		{name: "authority_revoked", revoked: true},
		{name: "both_checks_fail", changed: true, revoked: true},
		{name: "cancelled", cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var events []string
			input := &thumbnailFailureInput{events: &events}
			if tc.changed {
				input.after = metadataErr
			}
			authorized, opened, decoded := 0, 0, 0
			r := EpisodeThumbnailRequest{Mode: EpisodeThumbnailOffMode, Manual: true, DurationUS: 10, EndUS: 10,
				Authorize: func(checkCtx context.Context, operation, cost string, manual bool) error {
					events = append(events, "authorize")
					authorized++
					if checkCtx != ctx || operation != catalog.EpisodeThumbnailOperation || cost != EpisodeThumbnailCostClass || !manual || input.closed != 0 {
						t.Fatal("authority context, classification or retained lifetime changed")
					}
					if authorized == 3 && tc.revoked {
						return authorityErr
					}
					return checkCtx.Err()
				},
				Open: func(context.Context) (Input, error) {
					events = append(events, "open")
					opened++
					return input, nil
				},
				Decoder: func(Input) Decode {
					return func(_ context.Context, _ string, _ decoder.AnalysisSpec, consume func(io.Reader) error) error {
						events = append(events, "decode")
						decoded++
						// Even a complete JPEG must never escape a failed decode.
						if err := consume(bytes.NewReader(thumbnailJPEG(320, 180))); err != nil {
							return err
						}
						if tc.cancel {
							cancel()
						}
						return decodeErr
					}
				},
			}
			result, err := MakeEpisodeThumbnail(ctx, r)
			if !errors.Is(err, decodeErr) || errors.Is(err, metadataErr) != tc.changed || errors.Is(err, authorityErr) != tc.revoked || errors.Is(err, context.Canceled) != tc.cancel {
				t.Fatalf("lost original or fresh check failure: %v", err)
			}
			if result.Origin != "fallback" || len(result.JPEG) != 0 || result.Width != 0 || result.Height != 0 || result.PositionUS != 0 || opened != 1 || decoded != 1 || authorized != 3 || input.validated != 2 || input.closed != 1 {
				t.Fatalf("failed decode published, retried or leaked: %+v input=%+v opens=%d decodes=%d auth=%d", result, input, opened, decoded, authorized)
			}
			want := []string{"authorize", "open", "validate", "authorize", "decode", "validate", "authorize", "close"}
			if !reflect.DeepEqual(events, want) {
				t.Fatalf("check/close order %v, want %v", events, want)
			}
		})
	}
}

func TestEpisodeThumbnailRejectsMissingAuthorityAndCancelledAcquisition(t *testing.T) {
	for _, missing := range []string{"authority", "open", "decoder", "duration", "excessive_duration"} {
		r := EpisodeThumbnailRequest{Mode: EpisodeThumbnailDefaultMode, DurationUS: 10, EndUS: 10,
			Authorize: thumbnailAuthorization,
			Open:      func(context.Context) (Input, error) { t.Fatal("invalid request opened media"); return nil, nil },
			Decoder:   func(Input) Decode { t.Fatal("invalid request acquired decoder"); return nil },
		}
		switch missing {
		case "authority":
			r.Authorize = nil
		case "open":
			r.Open = nil
		case "decoder":
			r.Decoder = nil
		case "duration":
			r.DurationUS = 0
		case "excessive_duration":
			r.DurationUS = 1 << 53
		}
		if _, err := MakeEpisodeThumbnail(context.Background(), r); !errors.Is(err, ErrInput) {
			t.Fatalf("missing %s: %v", missing, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	input := &thumbnailRangeInput{}
	r := EpisodeThumbnailRequest{Mode: EpisodeThumbnailDefaultMode, DurationUS: 10, EndUS: 10, Authorize: thumbnailAuthorization,
		Open:    func(context.Context) (Input, error) { cancel(); return input, nil },
		Decoder: func(Input) Decode { t.Fatal("decoder acquired after cancellation"); return nil },
	}
	if result, err := MakeEpisodeThumbnail(ctx, r); !errors.Is(err, context.Canceled) || len(result.JPEG) != 0 || input.closed != 1 {
		t.Fatalf("cancelled acquisition accepted or leaked: %+v %v closed=%d", result, err, input.closed)
	}
}

func TestEpisodeThumbnailKeepsSharedFileEpisodeClock(t *testing.T) {
	input := &thumbnailRangeInput{}
	r := EpisodeThumbnailRequest{Mode: EpisodeThumbnailDefaultMode, Manual: true, DurationUS: 120000000, StartUS: 60000000, EndUS: 90000000,
		Authorize: thumbnailAuthorization, Open: func(context.Context) (Input, error) { return input, nil },
		Decoder: func(Input) Decode {
			return func(_ context.Context, _ string, spec decoder.AnalysisSpec, consume func(io.Reader) error) error {
				if spec.StartUS != 63000000 || spec.MaxDurationUS != 120000000 {
					t.Fatalf("wrong logical episode clock: %+v", spec)
				}
				return consume(bytes.NewReader(thumbnailJPEG(320, 180)))
			}
		},
	}
	out, err := MakeEpisodeThumbnail(context.Background(), r)
	if err != nil || out.PositionUS != 63000000 {
		t.Fatalf("%+v %v", out, err)
	}
	for _, bounds := range [][2]int64{{-1, 90000000}, {90000000, 90000000}, {60000000, 120000001}, {60000000, 0}} {
		r.StartUS, r.EndUS = bounds[0], bounds[1]
		r.Open = func(context.Context) (Input, error) { t.Fatal("invalid clock opened input"); return nil, nil }
		if _, err = MakeEpisodeThumbnail(context.Background(), r); !errors.Is(err, ErrInput) {
			t.Fatalf("invalid bounds %v: %v", bounds, err)
		}
	}
}
