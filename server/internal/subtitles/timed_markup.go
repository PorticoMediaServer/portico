package subtitles

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var samiSync = regexp.MustCompile(`(?i)<sync\b[^>]{0,1000}>`)
var markupAttribute = regexp.MustCompile(`(?i)([a-z][a-z0-9:_-]*)\s*=\s*(?:"([^"<>]{0,256})"|'([^'<>]{0,256})'|([^\s<>]{1,256}))`)
var markupBreak = regexp.MustCompile(`(?i)<br\s*/?>`)
var ttmlStart = regexp.MustCompile(`(?is)<(?:[a-z0-9_]+:)?p\b([^>]{0,1000})>`)
var ttmlEnd = regexp.MustCompile(`(?is)</(?:[a-z0-9_]+:)?p\s*>`)

func attributes(s string) map[string]string {
	out := map[string]string{}
	for _, m := range markupAttribute.FindAllStringSubmatch(s, -1) {
		v := m[2]
		if v == "" {
			v = m[3]
		}
		if v == "" {
			v = m[4]
		}
		out[strings.ToLower(m[1])] = v
	}
	return out
}
func finishMarkup(cues []StyledCue, durationUS int64) ([]StyledCue, error) {
	if len(cues) > MaxCues {
		return nil, ErrCapacity
	}
	limit := MaxDurationMS
	if durationUS > 0 {
		limit = min(limit, durationUS/1000+2000)
	}
	kept := cues[:0]
	for _, c := range cues {
		if c.StartMS < 0 || c.StartMS >= limit || c.EndMS <= c.StartMS {
			continue
		}
		c.EndMS = min(c.EndMS, limit)
		if c.Text != "" {
			kept = append(kept, c)
		}
	}
	if len(kept) == 0 {
		return nil, ErrText
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].StartMS < kept[j].StartMS })
	return kept, nil
}
func parseSAMI(raw []byte, durationUS int64) ([]StyledCue, error) {
	decoded, e := decodeLoosely(raw)
	if e != nil {
		return nil, e
	}
	s := string(decoded)
	matches := samiSync.FindAllStringIndex(s, -1)
	if len(matches) > MaxCues*2 {
		return nil, ErrCapacity
	}
	times := make([]int64, 0, len(matches))
	for _, m := range matches {
		v, e := strconv.ParseInt(attributes(s[m[0]:m[1]])["start"], 10, 64)
		if e == nil && v >= 0 && v <= MaxDurationMS {
			times = append(times, v)
		}
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	var cues []StyledCue
	for i, m := range matches {
		start, e := strconv.ParseInt(attributes(s[m[0]:m[1]])["start"], 10, 64)
		if e != nil || start < 0 || start > MaxDurationMS {
			continue
		}
		end := MaxDurationMS
		next := len(s)
		if durationUS > 0 {
			end = durationUS / 1000
		}
		nextTime := sort.Search(len(times), func(j int) bool { return times[j] > start })
		if nextTime < len(times) {
			end = times[nextTime]
		}
		if i+1 < len(matches) {
			next = matches[i+1][0]
		}
		body, italic, top := plainCueText(markupBreak.ReplaceAllString(s[m[1]:next], "\n"))
		// SAMI uses nonbreaking space for the clear event. It provides an end for
		// the preceding cue but is not itself dialogue.
		if body != "" {
			cues = append(cues, StyledCue{StartMS: start, EndMS: end, Text: body, Italic: italic, Top: top})
		}
	}
	return finishMarkup(cues, durationUS)
}
func markupTime(s string) (int64, bool) {
	if v, ok := tolerantStampMS(s); ok {
		return v, true
	}
	units := []struct {
		suffix string
		factor float64
	}{{"ms", 1}, {"h", 3600000}, {"m", 60000}, {"s", 1000}}
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			v, e := strconv.ParseFloat(strings.TrimSuffix(s, u.suffix), 64)
			if e == nil && v >= 0 && v*u.factor <= float64(MaxDurationMS) {
				return int64(v * u.factor), true
			}
			return 0, false
		}
	}
	return 0, false
}
func parseTTML(raw []byte, durationUS int64) ([]StyledCue, error) {
	decoded, e := decodeLoosely(raw)
	if e != nil {
		return nil, e
	}

	text := string(decoded)
	matches := ttmlStart.FindAllStringSubmatchIndex(text, MaxCues+1)
	if len(matches) > MaxCues {
		return nil, ErrCapacity
	}
	var cues []StyledCue
	for i, m := range matches {
		a := attributes(text[m[2]:m[3]])
		start, ok := markupTime(a["begin"])
		if !ok {
			continue
		}
		end, valid := markupTime(a["end"])
		if !valid {
			if duration, yes := markupTime(a["dur"]); yes && duration <= MaxDurationMS-start {
				end = start + duration
				valid = true
			}
		}
		if !valid || end <= start {
			continue
		}
		stop := len(text)
		if i+1 < len(matches) {
			stop = matches[i+1][0]
		}
		body := text[m[1]:stop]
		if close := ttmlEnd.FindStringIndex(body); close != nil {
			body = body[:close[0]]
		}
		plain, italic, top := plainCueText(markupBreak.ReplaceAllString(body, "\n"))
		cues = append(cues, StyledCue{StartMS: start, EndMS: end, Text: plain, Italic: italic, Top: top})
	}
	return finishMarkup(cues, durationUS)
}
