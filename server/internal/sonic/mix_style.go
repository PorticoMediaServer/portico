package sonic

import "math"

// These are indices in the original MTT_LABELS from the pinned ISC musicnn
// configuration.py (SHA256 00fd595eb16bad3a605e10bb6eaf0152cb8125bed6e2c884a636f679ae100419).
// Deliberately exclude instruments, voices, gender, tempo and loudness. In
// particular "classic" is not renamed to the MSD head's "classic rock".
var mixStyles = []struct {
	index int
	tag   string
}{
	{1, "classical"}, {3, "techno"}, {6, "electronic"}, {7, "rock"},
	{10, "ambient"}, {17, "opera"}, {29, "pop"}, {30, "soft"},
	{37, "new age"}, {38, "dance"}, {46, "country"}, {47, "metal"},
}

const MixStyleMinimumScore = .2

// MixStyleTags is the current exact head's schema/presentation allow-list.
// Return a copy so no projection can mutate the model's naming policy.
func MixStyleTags() []string {
	out := make([]string, len(mixStyles))
	for index, style := range mixStyles {
		out[index] = style.tag
	}
	return out
}

// MixStyle returns a model fact, never presentation text. Callers translate the
// allowed tag and the sentence pattern separately. A duplicate strongest tag
// falls back to the artist rather than selecting a weaker, misleading style.
func MixStyle(model Identity, scores []float64, used map[string]bool) (string, bool) {
	if model.ID != ModelID || model.Version != ModelVersion || model.SHA256 != ModelSHA256 || len(scores) != TagCount {
		return "", false
	}
	for _, score := range scores {
		if math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1 {
			return "", false
		}
	}
	best, score := "", -1.0
	for _, style := range mixStyles {
		if scores[style.index] > score {
			best, score = style.tag, scores[style.index]
		}
	}
	if score < MixStyleMinimumScore || used[best] {
		return "", false
	}
	return best, true
}
