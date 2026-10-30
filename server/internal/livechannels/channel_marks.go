package livechannels

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// ChannelMark is authoritative within a source's complete current lineup.
// Filtering and page boundaries never change the collision domain.
type ChannelMark struct {
	Text string `json:"text"`
	Seed string `json:"seed"`
}
type markName struct{ source, id, name, number string }

func channelMarkBase(name string) (string, string) {
	words := strings.FieldsFunc(name, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	if len(words) == 0 {
		return "?", "?"
	}
	initials := ""
	for _, w := range words {
		r := []rune(w)
		initials += strings.ToUpper(string(r[0]))
	}
	base := initials
	if len([]rune(base)) > 2 {
		base = string([]rune(base)[:2])
	}
	if len(words) == 1 {
		r := []rune(strings.ToUpper(words[0]))
		base = string(r[:min(2, len(r))])
	}
	alternate := initials
	for _, w := range words {
		if unicode.IsNumber([]rune(w)[0]) {
			alternate = string([]rune(initials)[:1]) + w
			break
		}
	}
	return base, alternate
}
func channelMarks(names []markName) map[string]ChannelMark {
	sort.Slice(names, func(i, j int) bool {
		a, b := names[i], names[j]
		if a.source != b.source {
			return a.source < b.source
		}
		if a.name != b.name {
			return a.name < b.name
		}
		return a.id < b.id
	})
	baseCounts := map[string]int{}
	for _, n := range names {
		base, _ := channelMarkBase(n.name)
		baseCounts[n.source+"\x00"+base]++
	}
	proposed := make([]string, len(names))
	counts := map[string]int{}
	for i, n := range names {
		base, alternate := channelMarkBase(n.name)
		if baseCounts[n.source+"\x00"+base] > 1 && alternate != "" {
			base = alternate
		}
		proposed[i] = base
		counts[n.source+"\x00"+base]++
	}
	out := map[string]ChannelMark{}
	used := map[string]bool{}
	for i, n := range names {
		mark := proposed[i]
		if counts[n.source+"\x00"+mark] > 1 {
			suffix := n.number
			if suffix == "" {
				suffix = n.id
			}
			mark += " " + suffix
		}
		key := n.source + "\x00" + mark
		if used[key] {
			mark += " " + n.id
		}
		used[n.source+"\x00"+mark] = true
		out[n.source+"\x00"+n.id] = ChannelMark{Text: mark, Seed: n.source + ":" + n.id}
	}
	return out
}

// The process-wide LRU is shared by owner, guide and recording reads. Database
// identity prevents cross-server reuse. At most four snapshots / 100,000 marks
// are retained; builds run outside the mutex.
const markCacheEntries = 4
const markCacheChannels = 100000

type markCacheKey struct {
	db                 *sql.DB
	source, generation string
	revision           int64
}
type markCacheEntry struct {
	key   markCacheKey
	marks map[string]ChannelMark
}

var sourceMarkCache struct {
	sync.Mutex
	entries []markCacheEntry // oldest first
}

// SourceChannelMarksTx returns an immutable map keyed by source + NUL + channel.
// It includes hidden channels and effective owner numbers in the caller's
// snapshot. Authorization belongs to the caller, never the collision domain.
// Pass the owning database on read-only snapshots to reuse the bounded cache;
// pass nil in write transactions so rolled-back values can never be cached.
func SourceChannelMarksTx(ctx context.Context, tx *sql.Tx, db *sql.DB, source string) (map[string]ChannelMark, error) {
	key := markCacheKey{db: db, source: source}
	err := tx.QueryRowContext(ctx, `SELECT active_generation,COALESCE((SELECT revision FROM admin_documents WHERE scope='live-source:'||live_sources.id),1) FROM live_sources WHERE id=?`, source).Scan(&key.generation, &key.revision)
	if err != nil {
		return nil, err
	}
	if db != nil {
		// The lock covers only the lookup and the insert: a cold build of a large
		// lineup never stalls warm reads of other sources. Two cold readers of
		// one source may both build; the second insert replaces the first.
		sourceMarkCache.Lock()
		for i, entry := range sourceMarkCache.entries {
			if entry.key == key {
				copy(sourceMarkCache.entries[i:], sourceMarkCache.entries[i+1:])
				sourceMarkCache.entries[len(sourceMarkCache.entries)-1] = entry
				sourceMarkCache.Unlock()
				return entry.marks, nil
			}
		}
		sourceMarkCache.Unlock()
	}
	rows, err := tx.QueryContext(ctx, `SELECT v.channel_id,v.name,COALESCE(NULLIF(n.number,''),v.number) FROM live_channel_versions v LEFT JOIN admin_channel_numbers n ON n.source_id=? AND n.channel_id=v.channel_id WHERE v.generation_id=?`, source, key.generation)
	if err != nil {
		return nil, err
	}
	names := []markName{}
	for rows.Next() {
		var n markName
		n.source = source
		if err = rows.Scan(&n.id, &n.name, &n.number); err != nil {
			break
		}
		names = append(names, n)
	}
	if e := rows.Err(); err == nil {
		err = e
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	marks := channelMarks(names)
	if db != nil && len(marks) <= markCacheChannels {
		sourceMarkCache.Lock()
		defer sourceMarkCache.Unlock()
		previous := sourceMarkCache.entries
		kept := previous[:0]
		total := len(marks)
		for _, entry := range sourceMarkCache.entries {
			// A changed generation/revision replaces this source's old snapshot.
			if entry.key.db != db || entry.key.source != source {
				kept = append(kept, entry)
				total += len(entry.marks)
			}
		}
		clear(previous[len(kept):])
		for len(kept) > 0 && (len(kept) >= markCacheEntries || total > markCacheChannels) {
			total -= len(kept[0].marks)
			kept[0] = markCacheEntry{}
			kept = kept[1:]
		}
		sourceMarkCache.entries = append(kept, markCacheEntry{key, marks})
	}
	return marks, nil
}

func (s *Store) directoryMarks(ctx context.Context, tx *sql.Tx, scope DirectoryScope, channels []Channel) error {
	// Library configurations are already bounded to 64 rows. Live source marks
	// use their full generation, independently of this viewer's projection.
	names := make([]markName, 0, len(scope.LibraryRows))
	for _, row := range scope.LibraryRows {
		c := row.Channel
		names = append(names, markName{c.SourceID, c.ID, c.Name, c.Number})
	}
	marks := channelMarks(names)
	sources := map[string]map[string]ChannelMark{}
	for _, c := range channels {
		for _, source := range scope.AllowedLiveSources {
			if c.SourceID == source && sources[source] == nil {
				var err error
				sources[source], err = SourceChannelMarksTx(ctx, tx, s.db, source)
				if err != nil {
					return err
				}
			}
		}
	}
	for i := range channels {
		c := &channels[i]
		domain := marks
		if live := sources[c.SourceID]; live != nil {
			domain = live
		}
		if mark, ok := domain[c.SourceID+"\x00"+c.ID]; ok {
			channels[i].Mark = &mark
		}
	}
	return nil
}
