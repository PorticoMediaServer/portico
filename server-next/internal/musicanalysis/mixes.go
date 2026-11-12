package musicanalysis

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/sonic"
)

// Mix is one of a profile's Mixes for you (area 7 rule 58; the music-plus notes): songs the
// profile has played that sound alike, closest to the mix's middle first. It plays like a
// playlist and can be saved as one.
type Mix struct {
	ID string
	// Style is the mix's strongest style, one of sonic.MixStyleTags ("soft", "dance"), for the
	// app to name in its own words; "" names it after its first artist.
	Style       string
	Artists     []MixArtist // at most two, the most played first
	MoreArtists bool
	Covers      []MixCover // at most three, one per album
	Songs       []string   // public ids
}

// MixArtist is an artist a mix is drawn with.
type MixArtist struct{ ID, Name string }

// MixCover is an album whose cover a mix is drawn with; Hash is "" for an album without one.
type MixCover struct{ AlbumID, Hash string }

// The old product's bounds, kept: mixes come from the last plays, there are at most
// sonic.MaxClusters of them (one per six songs played), and they appear only once enough has
// been played and enough of the music analysed.
const (
	mixPlays       = sonic.MaxClusterPoints // the last plays mixes are made from
	mixSongsPlayed = 6                      // analysed songs played, at least
	mixAllPlays    = 12                     // plays across the mixes, at least
	mixSongs       = 3                      // songs in one mix, at least
	mixMaxSongs    = 500                    // songs in one mix, at most: what a queue takes by name
	coverageKept   = 10 * time.Minute       // how long a profile's coverage count is kept
)

// model is the model the stored analyses must come from.
var model = sonic.Identity{ID: sonic.ModelID, Version: sonic.ModelVersion, SHA256: sonic.ModelSHA256}

// Mixer makes Mixes for you from what sound analysis stored. It needs no model at work: only
// the analyses already kept.
type Mixer struct {
	DB  *db.DB
	Now func() time.Time

	mu sync.Mutex
	// covered is each profile's coverage, kept for a while: it reads every song the profile
	// may see, and Home asks for mixes often.
	covered map[int64]coverage
}

// coverage is a profile's count of songs and analysed songs.
type coverage struct {
	at              time.Time
	songs, analysed int
}

func (s *Mixer) coverage(ctx context.Context, v db.Viewer) (songs, analysed int, err error) {
	now := s.Now()
	s.mu.Lock()
	c, ok := s.covered[v.ProfileID]
	s.mu.Unlock()
	if ok && now.Sub(c.at) < coverageKept {
		return c.songs, c.analysed, nil
	}
	if songs, analysed, err = s.DB.MusicCoverage(ctx, v, model.Version); err != nil {
		return 0, 0, err
	}
	s.mu.Lock()
	if s.covered == nil {
		s.covered = map[int64]coverage{}
	}
	s.covered[v.ProfileID] = coverage{now, songs, analysed}
	s.mu.Unlock()
	return songs, analysed, nil
}

// Mixes makes the profile's mixes from its recent listening, only of songs it may see now.
// None until it has played at least six analysed songs, twelve times in all, and the model
// has read four in five of the music it may see.
func (s *Mixer) Mixes(ctx context.Context, v db.Viewer) ([]Mix, error) {
	played, err := s.DB.RecentPlayedSongs(ctx, v, model.Version, mixPlays)
	if err != nil {
		return nil, err
	}
	songs := map[int64]db.PlayedSong{}
	vectors := map[int64]sonic.Embedding{}
	var points []sonic.ClusterPoint
	for _, p := range played {
		vector, err := sonic.FromBytes(p.Embedding)
		if err != nil {
			continue
		}
		songs[p.ItemID], vectors[p.ItemID] = p, vector
		points = append(points, sonic.ClusterPoint{ID: p.ItemID, Vector: vector, Weight: float64(p.Plays)})
	}
	if len(points) < mixSongsPlayed {
		return nil, nil
	}
	all, analysed, err := s.coverage(ctx, v)
	if err != nil || all == 0 || analysed*5 < all*4 {
		return nil, err
	}
	clusters, err := sonic.ClusterListening(points, min(sonic.MaxClusters, max(1, len(points)/6)))
	if err != nil {
		return nil, err
	}
	total := 0.0
	for _, c := range clusters {
		total += c.Weight
	}
	if total < mixAllPlays {
		return nil, nil
	}
	used := map[string]bool{}
	var out []Mix
	for _, c := range clusters {
		if m, ok := makeMix(v, c, songs, vectors, used); ok {
			out = append(out, m)
		}
	}
	return out, nil
}

func makeMix(v db.Viewer, c sonic.Cluster, songs map[int64]db.PlayedSong, vectors map[int64]sonic.Embedding, used map[string]bool) (Mix, bool) {
	members := append([]int64(nil), c.Members...)
	closeness := map[int64]float64{}
	for _, id := range members {
		closeness[id], _ = sonic.Cosine(vectors[id], c.Center)
	}
	sort.Slice(members, func(i, j int) bool {
		if closeness[members[i]] != closeness[members[j]] {
			return closeness[members[i]] > closeness[members[j]]
		}
		return members[i] < members[j]
	})
	members = members[:min(len(members), mixMaxSongs)]
	m := Mix{}
	var weight float64
	var tags [sonic.TagCount]float64
	tagged := 0
	artists := map[string]float64{}
	names := map[string]string{}
	albums := map[string]bool{}
	for _, id := range members {
		p := songs[id]
		w := float64(p.Plays)
		weight += w
		m.Songs = append(m.Songs, p.PublicID)
		if len(p.Tags) == sonic.TagCount*4 {
			tagged++
			for i := range tags {
				tags[i] += float64(math.Float32frombits(binary.LittleEndian.Uint32(p.Tags[i*4:]))) * w
			}
		}
		if p.ArtistID != "" {
			artists[p.ArtistID] += w
			names[p.ArtistID] = p.Artist
		}
		if p.AlbumID != "" && !albums[p.AlbumID] && len(m.Covers) < 3 {
			albums[p.AlbumID] = true
			m.Covers = append(m.Covers, MixCover{AlbumID: p.AlbumID, Hash: p.AlbumHash})
		}
	}
	ranked := make([]string, 0, len(artists))
	for id := range artists {
		ranked = append(ranked, id)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if artists[ranked[i]] != artists[ranked[j]] {
			return artists[ranked[i]] > artists[ranked[j]]
		}
		return ranked[i] < ranked[j]
	})
	for _, id := range ranked {
		if len(m.Artists) == 2 {
			m.MoreArtists = true
			break
		}
		m.Artists = append(m.Artists, MixArtist{ID: id, Name: names[id]})
	}
	if len(m.Songs) < mixSongs || len(m.Artists) == 0 || len(m.Covers) == 0 {
		return Mix{}, false
	}
	// A style only from songs that all have their scores, and never one another mix has.
	if tagged == len(members) && weight > 0 {
		scores := make([]float64, sonic.TagCount)
		for i, sum := range tags {
			scores[i] = sum / weight
		}
		if style, ok := sonic.MixStyle(model, scores, used); ok {
			m.Style, used[style] = style, true
		}
	}
	digest := sha256.Sum256([]byte(strconv.FormatInt(v.ProfileID, 10) + "|" + model.Version + "|" + strings.Join(m.Songs, ",")))
	m.ID = "mix_" + hex.EncodeToString(digest[:12])
	return m, true
}

// MixSongs gives the songs of the profile's mix with that id as it is now; a mix it does not
// have now (its listening has moved on, or it may no longer see the songs) is not found.
func (s *Mixer) MixSongs(ctx context.Context, v db.Viewer, id string) ([]string, error) {
	mixes, err := s.Mixes(ctx, v)
	if err != nil {
		return nil, err
	}
	for _, m := range mixes {
		if m.ID == id {
			return m.Songs, nil
		}
	}
	return nil, db.ErrNotFound
}
