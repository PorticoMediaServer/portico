package naming

import "testing"

func TestSampleExclusionRequiresObservedSizeBelow300MB(t *testing.T) {
	for _, name := range []string{"sample.mkv", "Movie-sample.mkv", "Movie/Samples/Movie.mkv", "Movie/Sample/child/Movie.mkv"} {
		for _, size := range []int64{-1, 0, SampleLimitBytes - 1, SampleLimitBytes, SampleLimitBytes + 1} {
			want := size >= 0 && size < SampleLimitBytes
			if got := IgnorePathWithSize(name, false, size); got != want {
				t.Errorf("%s size=%d ignored=%v want %v", name, size, got, want)
			}
		}
	}
	for _, name := range []string{"Sample", "Samples", "Movie/Samples", "Sample (2020)/Sample (2020).mkv", "A Sample Movie (2020).mkv"} {
		directory := name == "Sample" || name == "Samples" || name == "Movie/Samples"
		if IgnorePathWithSize(name, directory, 0) {
			t.Errorf("excluded directory or complete title %q", name)
		}
	}
	for _, name := range []string{"sample.mkv", "Movie-sample.mkv", "Samples"} {
		if IgnoreNameBeforeStat(name) {
			t.Errorf("sample rejected before stat: %s", name)
		}
	}
	for _, name := range []string{".hidden/film.mkv", "@eaDir/film.mkv", "Film/film.mkv.grab"} {
		if !IgnorePathWithSize(name, false, SampleLimitBytes+1) {
			t.Errorf("size bypassed system rule: %s", name)
		}
	}
}
