package musicanalysis

import (
	"context"
	"math"
	"math/rand/v2"
	"slices"
	"sort"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/sonic"
)

// The bounds of radio by sound: how many of the library's analysed songs one batch is chosen
// from (all of them in a library this size or smaller; otherwise that many in a row from a
// random place, a different stretch each batch), and how many of the profile's last plays it
// leaves out.
const (
	radioPool   = 4000
	radioRecent = 100
)

// Radio picks the next songs of a radio by sound (area 7 rule 58): the songs of the seed's
// library that sound closest to it (a song, or an album's or an artist's songs together), one
// per artist where it can, leaving out what was played recently and what the radio already
// picked. Nil when the model has read none of the seed's songs: the radio goes by genre.
func (m *Mixer) Radio(ctx context.Context, v db.Viewer, seed string, picked []int64, limit int) ([]int64, error) {
	library, seeds, err := m.DB.RadioSeedSounds(ctx, v, seed, model.Version)
	if err != nil || len(seeds) == 0 {
		return nil, err
	}
	center, ok := centre(seeds)
	if !ok {
		return nil, nil
	}
	pool, err := m.pool(ctx, v, library)
	if err != nil {
		return nil, err
	}
	recent, err := m.DB.RecentlyPlayedSongs(ctx, v.ProfileID, radioRecent)
	if err != nil {
		return nil, err
	}
	leave := map[int64]bool{}
	for _, ids := range [][]int64{picked, recent} {
		for _, id := range ids {
			leave[id] = true
		}
	}
	type scored struct {
		db.SongSound
		score float64
	}
	var ranked []scored
	for _, song := range pool {
		if leave[song.ItemID] {
			continue
		}
		vector, err := sonic.FromBytes(song.Embedding)
		if err != nil {
			continue
		}
		score, _ := sonic.Cosine(center, vector)
		ranked = append(ranked, scored{song, score})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].ItemID < ranked[j].ItemID
	})
	// One song per artist in a batch, then the closest of the rest if that is too few.
	var out []int64
	artists := map[int64]bool{}
	for _, s := range ranked {
		if len(out) == limit {
			break
		}
		if s.Artist == 0 || !artists[s.Artist] {
			artists[s.Artist] = true
			out = append(out, s.ItemID)
		}
	}
	for _, s := range ranked {
		if len(out) == limit {
			break
		}
		if !slices.Contains(out, s.ItemID) {
			out = append(out, s.ItemID)
		}
	}
	return out, nil
}

// centre is the seed songs' mean place, made unit length again.
func centre(songs []db.SongSound) (sonic.Embedding, bool) {
	var sum [sonic.Dimensions]float64
	n := 0
	for _, s := range songs {
		vector, err := sonic.FromBytes(s.Embedding)
		if err != nil {
			continue
		}
		for i, x := range vector {
			sum[i] += float64(x)
		}
		n++
	}
	var norm float64
	for _, x := range sum {
		norm += x * x
	}
	if n == 0 || norm == 0 {
		return sonic.Embedding{}, false
	}
	var out sonic.Embedding
	for i, x := range sum {
		out[i] = float32(x / math.Sqrt(norm))
	}
	return out, true
}

// pool reads the songs a batch is chosen from: radioPool of the library's analysed songs in a
// row, from a random place, going round to the start when the end comes first.
func (m *Mixer) pool(ctx context.Context, v db.Viewer, library int64) ([]db.SongSound, error) {
	last, err := m.DB.LastAnalysedSong(ctx)
	if err != nil {
		return nil, err
	}
	from := int64(0)
	if last > 0 {
		from = rand.Int64N(last)
	}
	songs, err := m.DB.RadioCandidates(ctx, v, library, model.Version, from, radioPool)
	if err != nil || len(songs) == radioPool || from == 0 {
		return songs, err
	}
	more, err := m.DB.RadioCandidates(ctx, v, library, model.Version, 0, radioPool-len(songs))
	for _, s := range more {
		if s.ItemID > from {
			break
		}
		songs = append(songs, s)
	}
	return songs, err
}
