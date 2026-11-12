package db

import (
	"context"
	"database/sql"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/searchtext"
)

// LiveChannelMatch is a channel whose name matches a search, and how well: 0 the whole name,
// 1 the name begins with what was typed, 2 every typed word begins a word of the name.
type LiveChannelMatch struct {
	LiveChannel
	Tier int
}

// SearchLiveChannels reads the channels in use (as LiveChannels would list them) whose names
// hold every typed word at the start of one of their words, matched as search matches titles
// (case, accents and punctuation folded away). Candidates are found by the longest typed word
// in the folded sort name, at most most+1 of them in channel-number order; more says the read
// stopped there.
func (d *DB) SearchLiveChannels(ctx context.Context, v Viewer, q LiveChannelQuery, text string, most int) (matches []LiveChannelMatch, more bool, err error) {
	terms, err := parseSearch(text)
	if err != nil {
		return nil, false, err
	}
	longest := ""
	for _, w := range terms.split {
		if len(w) > len(longest) {
			longest = w
		}
	}
	q.Limit = most + 1
	from, where := q.clauses()
	rows, err := d.read.QueryContext(ctx, `SELECT `+liveChannelColumns+` FROM `+from+liveChannelJoins+`
		WHERE `+where+` AND instr(c.sort_name, @word) > 0 ORDER BY c.number, c.id LIMIT @limit`,
		append(q.args(v), sql.Named("word", longest))...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	read := 0
	for rows.Next() {
		c, err := scanLiveChannel(rows)
		if err != nil {
			return nil, false, err
		}
		if read++; read > most {
			more = true
			break
		}
		if tier, ok := channelNameMatch(terms, c.Name); ok {
			matches = append(matches, LiveChannelMatch{LiveChannel: c, Tier: tier})
		}
	}
	return matches, more, rows.Err()
}

// channelNameMatch says whether a name matches the typed words, and how well (see
// LiveChannelMatch).
func channelNameMatch(terms searchTerms, name string) (int, bool) {
	split, joined := searchtext.Words(name)
	if joined == nil {
		joined = split
	}
	best, found := 3, false
	for _, typed := range [][]string{terms.split, terms.joined} {
		if typed == nil {
			continue
		}
		for _, words := range [][]string{split, joined} {
			if tier, ok := wordsMatch(typed, words); ok && tier < best {
				best, found = tier, true
			}
		}
	}
	return best, found
}

func wordsMatch(typed, words []string) (int, bool) {
	for _, t := range typed {
		ok := false
		for _, w := range words {
			if strings.HasPrefix(w, t) {
				ok = true
				break
			}
		}
		if !ok {
			return 0, false
		}
	}
	switch {
	case strings.Join(typed, " ") == strings.Join(words, " "):
		return 0, true
	case len(typed) <= len(words) && strings.HasPrefix(strings.Join(words, " "), strings.Join(typed, " ")):
		return 1, true
	}
	return 2, true
}
