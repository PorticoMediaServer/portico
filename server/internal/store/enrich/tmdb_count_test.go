package enrich

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

func TestTMDBMovieAggregatePreflightBeforeCandidateCopies(t *testing.T) {
	body := tmdbCreditsFixture(101, 51)
	ob := tmdbObservation()
	ob.EvidenceID = strings.Repeat("i", 400)
	ob.Region = "CA" + strings.Repeat("x", 3016)
	// Independent occurrence arithmetic: header3,587 +150*413 =65,537.
	headerBytes := len(ob.EvidenceID) + len(TMDBMovieParserVersion) + len("tmdb") + len("movie") + len(ob.ProviderID) + len(ob.Language) + len(ob.Region) + len(TMDBMovieRequestShape) + 64
	if headerBytes != 3587 || headerBytes+150*(400+4+5+4) != 65537 {
		t.Fatal("review reproduction drift")
	}
	policy := tmdbMoviePolicy(t)
	previousRate := runtime.MemProfileRate
	runtime.MemProfileRate = 1
	defer func() { runtime.MemProfileRate = previousRate }()
	before := tmdbCandidateCopiedObjects(t)
	doc, err := PrepareTMDBMovie(policy, ob, body)
	var refusal *writer.PolicyError
	if !errors.As(err, &refusal) || !errors.Is(err, writer.ErrCap) || refusal.Field != "seal_control_strings" || refusal.Limit != 65536 || doc.Header().ID() != "" || doc.Credits().Count() != 0 {
		t.Fatal("aggregate one-over produced a partial document", err)
	}
	after := tmdbCandidateCopiedObjects(t)
	if after != before {
		t.Fatalf("candidate text was materialized before aggregate preflight: %d allocations", after-before)
	}
}

// Count gross allocations, including objects already freed, on this exact
// regression's candidate decoder stack. Body construction, profile scratch and
// allocations from other tests are excluded. This test does not run in parallel.
func tmdbCandidateCopiedObjects(t *testing.T) int64 {
	t.Helper()
	runtime.GC()
	runtime.GC()
	var records []runtime.MemProfileRecord
	for {
		n, _ := runtime.MemProfile(nil, true)
		records = make([]runtime.MemProfileRecord, n+64)
		if n, ok := runtime.MemProfile(records, true); ok {
			records = records[:n]
			break
		}
	}
	var allocated int64
	for _, record := range records {
		frames := runtime.CallersFrames(record.Stack())
		candidate, producer, test := false, false, false
		for {
			frame, more := frames.Next()
			candidate = candidate || strings.HasSuffix(frame.Function, ".readTMDBCredits")
			producer = producer || strings.HasSuffix(frame.Function, ".PrepareTMDBMovie")
			test = test || strings.HasSuffix(frame.Function, ".TestTMDBMovieAggregatePreflightBeforeCandidateCopies")
			if !more {
				break
			}
		}
		if candidate && producer && test {
			allocated += record.AllocObjects
		}
	}
	return allocated
}

func TestTMDBCreditCountBorrowsValidatedBytes(t *testing.T) {
	for name, tc := range map[string]struct {
		body []byte
		want tmdbMovieCounts
	}{
		"complete_cohort":       {tmdbCreditsFixture(101, 51), tmdbMovieCounts{101, 51}},
		"escaped_keys_and_text": {[]byte(`{"cre\u0064its":{"ca\u0073t":[{"name":"braces } ] \"quoted\" \\"}],"cr\u0065w":[]},"id":42,"title":"x"}`), tmdbMovieCounts{1, 0}},
		"nested_unknown":        {[]byte(`{"shadow":{"credits":{"cast":[{},{}],"crew":[{}]}},"credits":{"crew":[{"x":[{},[],"} ]"]}],"cast":[]}}`), tmdbMovieCounts{0, 1}},
		"empty":                 {[]byte(" \n{\"credits\":{\"crew\":[  ],\"cast\":[]}} \t"), tmdbMovieCounts{}},
	} {
		t.Run(name, func(t *testing.T) {
			if !validTMDBJSON(tc.body) {
				t.Fatal("invalid JSON fixture")
			}
			var got tmdbMovieCounts
			var err error
			allocs := testing.AllocsPerRun(20, func() { got, err = countTMDBMovieCredits(tc.body) })
			if err != nil || got != tc.want || allocs != 0 {
				t.Fatal("count pass copied decoded values or changed counts", got, err, allocs)
			}
		})
	}
}
