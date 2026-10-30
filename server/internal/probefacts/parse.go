package probefacts

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var ErrFacts = errors.New("invalid or excessive probe facts")
var namePattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
var decimalPattern = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]{1,9})?$`)
var ratioPattern = regexp.MustCompile(`^[0-9]+/[0-9]+$`)

// Parse accepts a bounded complete FFprobe document. It strips arbitrary tags
// and private filenames from the result, and preserves absent timing as absent.
func Parse(data []byte) (ProbeFacts, error) {
	if len(data) == 0 || len(data) > MaxOutputBytes {
		return ProbeFacts{}, ErrFacts
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	tokens := 0
	if err := uniqueValue(d, 0, &tokens); err != nil {
		return ProbeFacts{}, err
	}
	if _, err := d.Token(); err != io.EOF {
		return ProbeFacts{}, ErrFacts
	}
	var raw struct {
		Format struct {
			Names    string  `json:"format_name"`
			Start    *string `json:"start_time"`
			Duration *string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			Index       *int64  `json:"index"`
			Kind        string  `json:"codec_type"`
			Codec       string  `json:"codec_name"`
			TimeBase    *string `json:"time_base"`
			Start       *int64  `json:"start_pts"`
			Duration    *int64  `json:"duration_ts"`
			Width       *int64  `json:"width"`
			Height      *int64  `json:"height"`
			SampleRate  *string `json:"sample_rate"`
			Channels    *int64  `json:"channels"`
			Disposition struct {
				Default int `json:"default"`
			} `json:"disposition"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return ProbeFacts{}, ErrFacts
	}
	if len(raw.Streams) == 0 || len(raw.Streams) > MaxStreams {
		return ProbeFacts{}, ErrFacts
	}
	result := ProbeFacts{}
	result.Format.Names = strings.Split(raw.Format.Names, ",")
	if len(result.Format.Names) > MaxFormatNames {
		return ProbeFacts{}, ErrFacts
	}
	for _, name := range result.Format.Names {
		if !validName(name) {
			return ProbeFacts{}, ErrFacts
		}
	}
	var err error
	if result.Format.StartSeconds, err = parseSeconds(raw.Format.Start); err != nil {
		return ProbeFacts{}, err
	}
	if result.Format.DurationSeconds, err = parseSeconds(raw.Format.Duration); err != nil {
		return ProbeFacts{}, err
	}
	if result.Format.DurationSeconds != nil && result.Format.DurationSeconds.Numerator < 0 {
		return ProbeFacts{}, ErrFacts
	}
	seen := map[int64]bool{}
	for _, s := range raw.Streams {
		if s.Index == nil || *s.Index < 0 || *s.Index > 1<<20 || seen[*s.Index] || !validName(s.Codec) || (s.Disposition.Default != 0 && s.Disposition.Default != 1) {
			return ProbeFacts{}, ErrFacts
		}
		seen[*s.Index] = true
		switch s.Kind {
		case "video", "audio", "subtitle", "data", "attachment":
		default:
			return ProbeFacts{}, ErrFacts
		}
		fact := StreamFacts{Index: *s.Index, Kind: s.Kind, Codec: s.Codec, StartTimestamp: s.Start, DurationTimestamp: s.Duration, Default: s.Disposition.Default == 1}
		if s.Duration != nil && *s.Duration < 0 {
			return ProbeFacts{}, ErrFacts
		}
		if s.TimeBase != nil && *s.TimeBase != "N/A" {
			if len(*s.TimeBase) > 64 || !ratioPattern.MatchString(*s.TimeBase) {
				return ProbeFacts{}, ErrFacts
			}
			fact.TimeBase, err = parseRational(*s.TimeBase)
			if err != nil || fact.TimeBase.Numerator <= 0 {
				return ProbeFacts{}, ErrFacts
			}
		}
		if s.Kind == "video" && s.Width != nil && s.Height != nil {
			if *s.Width < 1 || *s.Height < 1 || *s.Width > 65536 || *s.Height > 65536 {
				return ProbeFacts{}, ErrFacts
			}
			fact.Video = &VideoFacts{Width: *s.Width, Height: *s.Height}
		}
		if s.Kind == "audio" && s.SampleRate != nil && s.Channels != nil {
			rate, e := strconv.ParseInt(*s.SampleRate, 10, 64)
			if e != nil || rate < 1 || rate > 1<<24 || *s.Channels < 1 || *s.Channels > 1024 {
				return ProbeFacts{}, ErrFacts
			}
			fact.Audio = &AudioFacts{SampleRate: rate, Channels: *s.Channels}
		}
		result.Streams = append(result.Streams, fact)
	}
	return result, nil
}

func validName(s string) bool {
	return len(s) > 0 && len(s) <= MaxNameBytes && namePattern.MatchString(s)
}
func parseSeconds(s *string) (*Rational, error) {
	if s == nil || *s == "N/A" {
		return nil, nil
	}
	if len(*s) > 64 || !decimalPattern.MatchString(*s) {
		return nil, ErrFacts
	}
	return parseRational(*s)
}
func parseRational(s string) (*Rational, error) {
	r, ok := new(big.Rat).SetString(s)
	if !ok || !r.Num().IsInt64() || !r.Denom().IsInt64() {
		return nil, ErrFacts
	}
	return &Rational{r.Num().Int64(), r.Denom().Int64()}, nil
}

// Duplicate fields are rejected even in discarded metadata; a shallow decoder
// otherwise silently chooses the last value. Depth and token limits also bound
// nested source-controlled structures before typed decoding.
func uniqueValue(d *json.Decoder, depth int, tokens *int) error {
	if depth > 16 {
		return ErrFacts
	}
	*tokens++
	if *tokens > 32768 {
		return ErrFacts
	}
	token, err := d.Token()
	if err != nil {
		return ErrFacts
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, e := d.Token()
			if e != nil {
				return ErrFacts
			}
			name, ok := key.(string)
			folded := foldKey(name)
			if !ok || seen[folded] {
				return ErrFacts
			}
			seen[folded] = true
			if err = uniqueValue(d, depth+1, tokens); err != nil {
				return err
			}
		}
		end, e := d.Token()
		if e != nil || end != json.Delim('}') {
			return ErrFacts
		}
	case '[':
		for d.More() {
			if err = uniqueValue(d, depth+1, tokens); err != nil {
				return err
			}
		}
		end, e := d.Token()
		if e != nil || end != json.Delim(']') {
			return ErrFacts
		}
	default:
		return ErrFacts
	}
	return nil
}

// Match encoding/json's Unicode case-fold equivalence when detecting key
// collisions, including aliases in discarded objects.
func foldKey(key string) string {
	return strings.Map(func(r rune) rune {
		smallest := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < smallest {
				smallest = next
			}
		}
		return smallest
	}, key)
}
