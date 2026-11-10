package naming

// A packed music track number carries the disc and the track in one file-name
// number: Plex reads "302 - Track" as disc 3, track 2. Portico accepts that
// reading only for a whole album cohort that clearly follows the convention.
// An embedded disc or track tag, a disc folder, or an owner edit is stronger
// evidence and always wins.
//
// The helpers in this file are pure: they read no files, metadata, catalogue
// or provider, keep no state, and never change the numbers they are given.

// MaxPackedTrackCohort bounds the numbers one album may offer for inference.
const MaxPackedTrackCohort = 1024

// PackedTrackNumber is one file-name number split into its disc and track.
type PackedTrackNumber struct {
	Disc  int
	Track int
}

// SplitPackedTrackNumber splits one packed number. A packed number is three
// digits, 101-999: disc 1-9 and track 1-99. A zero track (100, 200, ...) is
// not a packed number.
func SplitPackedTrackNumber(n int) (PackedTrackNumber, bool) {
	if n < 101 || n > 999 {
		return PackedTrackNumber{}, false
	}
	disc, track := n/100, n%100
	if disc < 1 || track < 1 {
		return PackedTrackNumber{}, false
	}
	return PackedTrackNumber{Disc: disc, Track: track}, true
}

// PackedTrackCohort decides whether an album's file-name numbers are packed
// disc+track numbers. It infers only when the whole cohort is unambiguous:
//
//   - discEvidence is false: any disc tag or disc folder already answers, so
//     the caller must not ask;
//   - every number is packed and appears once;
//   - discs 1..N all appear, each holding tracks 1..M with no gap;
//   - at least two discs and at least one disc with two or more tracks
//     establish a run rather than isolated large track numbers.
//
// Sparse or mixed cohorts, including one disc alone or a missing track,
// refuse inference. The helper is pure and bounded by MaxPackedTrackCohort.
// Embedded tags and owner edits stay above this evidence: pass only numbers
// that would otherwise be the track number.
func PackedTrackCohort(numbers []int, discEvidence bool) (map[int]PackedTrackNumber, bool) {
	if discEvidence || len(numbers) == 0 || len(numbers) > MaxPackedTrackCohort {
		return nil, false
	}
	seen := make(map[int]bool, len(numbers))
	highest := map[int]int{}
	count := map[int]int{}
	for _, n := range numbers {
		if seen[n] {
			return nil, false
		}
		seen[n] = true
		split, ok := SplitPackedTrackNumber(n)
		if !ok {
			return nil, false
		}
		count[split.Disc]++
		if split.Track > highest[split.Disc] {
			highest[split.Disc] = split.Track
		}
	}
	if len(highest) < 2 {
		return nil, false
	}
	run := false
	for disc := 1; disc <= len(highest); disc++ {
		top, ok := highest[disc]
		if !ok || count[disc] != top {
			return nil, false
		}
		if top >= 2 {
			run = true
		}
	}
	if !run {
		return nil, false
	}
	out := make(map[int]PackedTrackNumber, len(numbers))
	for _, n := range numbers {
		split, _ := SplitPackedTrackNumber(n)
		out[n] = split
	}
	return out, true
}
