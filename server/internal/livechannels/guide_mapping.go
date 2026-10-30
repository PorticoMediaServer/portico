package livechannels

import (
	"sort"
	"strings"
	"unicode"
)

type guideChannelIndex struct{ ids, names map[string][]int }

// SimpleFold's cycle gives EqualFold-equivalent keys, including final sigma
// and Kelvin sign, which ordinary lowercasing does not normalize together.
func guideName(v string) string {
	return strings.Map(func(r rune) rune {
		key := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < key {
				key = next
			}
		}
		return key
	}, strings.Join(strings.Fields(v), " "))
}

// One selected channel may have provider, number and multilingual aliases.
// Repeated names must not produce a document-sized cross product of mappings.
const maxGuideAliases = 16

func indexGuideChannels(channels []parsedChannel, explicit map[string]bool) guideChannelIndex {
	index := guideChannelIndex{ids: map[string][]int{}, names: map[string][]int{}}
	for i, ch := range channels {
		if explicit[ch.key] {
			continue
		}
		for _, key := range []string{ch.key, ch.guideKey, ch.number} {
			if key != "" {
				index.ids[key] = append(index.ids[key], i)
			}
		}
		key := guideName(ch.name)
		index.names[key] = append(index.names[key], i)
	}
	return index
}
func (index guideChannelIndex) match(id string, names []string) []int {
	candidates := append([]int(nil), index.ids[id]...)
	for _, name := range names {
		candidates = append(candidates, index.names[guideName(name)]...)
	}
	sort.Ints(candidates)
	unique := candidates[:0]
	for _, i := range candidates {
		if len(unique) == 0 || unique[len(unique)-1] != i {
			unique = append(unique, i)
		}
	}
	return unique
}
