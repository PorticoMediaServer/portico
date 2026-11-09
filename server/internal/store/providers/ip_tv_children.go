package providers

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sort"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/enrich"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// ErrTVChildren is the closed normalization refusal. A partial declaration,
// duplicate episode, mixed numbering namespace, foreign coordinate or unbounded
// observation is refused rather than repaired.
var ErrTVChildren = errors.New("invalid TV child observation")

// TVChildNumbering is the closed ordering namespace of a TV publication set.
// Aired and absolute coordinates are separate provider namespaces; alternative
// numbering never reinterprets either.
type TVChildNumbering string

const (
	TVAired    TVChildNumbering = "aired"
	TVAbsolute TVChildNumbering = "absolute"
)

const (
	maxTVChildSeasons      = 200
	maxTVEpisodesPerSeason = 2000
	maxTVChildMembers      = 20000
	maxTVSeasonNumber      = 100000
	maxTVEpisodeNumber     = 100000
	maxTVAbsoluteNumber    = 1000000
	maxTVChildText         = 128
)

// TVChildEpisode is one sealed episode evidence coordinate exactly as the
// provider reported it. The present flags keep a real zero coordinate distinct
// from an absent one. Order names the coordinate namespace the row belongs to.
type TVChildEpisode struct {
	EvidenceID                                     string
	Order                                          TVChildNumbering
	SeasonNumber, EpisodeNumber, AbsoluteNumber    int64
	SeasonPresent, EpisodePresent, AbsolutePresent bool
}

// TVChildSeason declares the provider's complete episode count for one season.
// A declaration that does not match the supplied episodes is partial and refused.
type TVChildSeason struct {
	SeasonNumber     int64
	DeclaredEpisodes int64
	Episodes         []TVChildEpisode
}

// TVChildObservation is the complete declared series/season child hierarchy. A
// scalar header cannot construct it: there is no conversion from TVHeader, and
// an empty declaration is refused.
type TVChildObservation struct {
	Kind            enrich.SubjectKind
	Numbering       TVChildNumbering
	DeclaredSeasons int64
	Seasons         []TVChildSeason
}

// TVSetMember is one normalized member in the chosen namespace order. The
// alternative coordinate is retained as data and never used for ordering or
// identity.
type TVSetMember struct {
	EvidenceID                                     string
	SeasonNumber, EpisodeNumber, AbsoluteNumber    int64
	SeasonPresent, EpisodePresent, AbsolutePresent bool
	Ordinal                                        int64
}

// TVChildSet is a complete normalized provider set. It is preparation data
// only: it neither authorizes publication nor writes a catalogue identity.
type TVChildSet struct {
	Kind         enrich.SubjectKind
	Numbering    TVChildNumbering
	Members      []TVSetMember
	memberDigest [32]byte
	childDigest  [32]byte
}

// MemberDigest is the complete ordered member identity the IP set begin declares
// and the writer seal recomputes from persisted rows.
func (s TVChildSet) MemberDigest() [32]byte { return s.memberDigest }

// ChildDigest is the complete coordinate/declaration identity of this provider
// observation. It changes with a partial count, duplicate, coordinate or
// numbering namespace change.
func (s TVChildSet) ChildDigest() [32]byte { return s.childDigest }

// Complete reports that normalization only ever returns a complete declared set.
func (s TVChildSet) Complete() bool { return len(s.Members) > 0 }

// EvidenceIDs returns the ordered member evidence IDs as a copy.
func (s TVChildSet) EvidenceIDs() []string {
	out := make([]string, len(s.Members))
	for i, member := range s.Members {
		out[i] = member.EvidenceID
	}
	return out
}

func tvChildKind(kind enrich.SubjectKind) bool {
	return kind == enrich.Series || kind == enrich.Season
}

func tvChildNumbering(value TVChildNumbering) bool {
	return value == TVAired || value == TVAbsolute
}

func tvChildText(value string) bool {
	return len(value) > 0 && len(value) <= maxTVChildText && utf8.ValidString(value) &&
		indexByte(value, 0) < 0
}

func indexByte(value string, needle byte) int {
	for i := 0; i < len(value); i++ {
		if value[i] == needle {
			return i
		}
	}
	return -1
}

// NormalizeTVChildren turns one complete declared provider hierarchy into the
// bounded ordered member list of a publication set. It never fills in a missing
// season/episode, never mixes numbering namespaces and never reinterprets a
// provider coordinate as a catalogue identity.
func NormalizeTVChildren(o TVChildObservation) (TVChildSet, error) {
	var zero TVChildSet
	if !tvChildKind(o.Kind) || !tvChildNumbering(o.Numbering) ||
		o.DeclaredSeasons < 1 || o.DeclaredSeasons > maxTVChildSeasons ||
		int64(len(o.Seasons)) != o.DeclaredSeasons ||
		(o.Kind == enrich.Season && len(o.Seasons) != 1) {
		return zero, ErrTVChildren
	}
	seenEvidence := make(map[string]bool)
	seenSeason := make(map[int64]bool)
	members := make([]TVSetMember, 0)
	for _, season := range o.Seasons {
		if season.SeasonNumber < 0 || season.SeasonNumber > maxTVSeasonNumber ||
			seenSeason[season.SeasonNumber] {
			return zero, ErrTVChildren
		}
		seenSeason[season.SeasonNumber] = true
		if season.DeclaredEpisodes < 0 || season.DeclaredEpisodes > maxTVEpisodesPerSeason ||
			int64(len(season.Episodes)) != season.DeclaredEpisodes {
			return zero, ErrTVChildren
		}
		for _, episode := range season.Episodes {
			if !tvChildText(episode.EvidenceID) || seenEvidence[episode.EvidenceID] ||
				episode.Order != o.Numbering {
				return zero, ErrTVChildren
			}
			seenEvidence[episode.EvidenceID] = true
			switch o.Numbering {
			case TVAired:
				if !episode.SeasonPresent || !episode.EpisodePresent ||
					episode.SeasonNumber != season.SeasonNumber ||
					episode.EpisodeNumber < 0 || episode.EpisodeNumber > maxTVEpisodeNumber {
					return zero, ErrTVChildren
				}
			case TVAbsolute:
				if !episode.AbsolutePresent || episode.AbsoluteNumber < 1 ||
					episode.AbsoluteNumber > maxTVAbsoluteNumber ||
					(episode.SeasonPresent && episode.SeasonNumber != season.SeasonNumber) ||
					(episode.EpisodePresent && (episode.EpisodeNumber < 0 ||
						episode.EpisodeNumber > maxTVEpisodeNumber)) {
					return zero, ErrTVChildren
				}
			}
			members = append(members, TVSetMember{EvidenceID: episode.EvidenceID,
				SeasonNumber: episode.SeasonNumber, EpisodeNumber: episode.EpisodeNumber,
				AbsoluteNumber: episode.AbsoluteNumber, SeasonPresent: episode.SeasonPresent,
				EpisodePresent: episode.EpisodePresent, AbsolutePresent: episode.AbsolutePresent})
		}
	}
	if len(members) == 0 || len(members) > maxTVChildMembers {
		return zero, ErrTVChildren
	}
	sort.SliceStable(members, func(i, j int) bool {
		if o.Numbering == TVAired {
			if members[i].SeasonNumber != members[j].SeasonNumber {
				return members[i].SeasonNumber < members[j].SeasonNumber
			}
			return members[i].EpisodeNumber < members[j].EpisodeNumber
		}
		return members[i].AbsoluteNumber < members[j].AbsoluteNumber
	})
	seenCoordinate := make(map[[2]int64]bool, len(members))
	for i, member := range members {
		key := [2]int64{member.SeasonNumber, member.EpisodeNumber}
		if o.Numbering == TVAbsolute {
			key = [2]int64{0, member.AbsoluteNumber}
		}
		if seenCoordinate[key] {
			return zero, ErrTVChildren
		}
		seenCoordinate[key] = true
		members[i].Ordinal = int64(i)
	}
	ids := make([]string, len(members))
	for i, member := range members {
		ids[i] = member.EvidenceID
	}
	memberDigest, err := writer.TVSetMemberDigest(ids)
	if err != nil {
		return zero, ErrTVChildren
	}
	return TVChildSet{Kind: o.Kind, Numbering: o.Numbering, Members: members,
		memberDigest: memberDigest, childDigest: tvChildDigest(o, members)}, nil
}

// tvChildDigest binds the complete declaration and the normalized order. It is
// inert provider data, not a writer request or result digest.
func tvChildDigest(o TVChildObservation, members []TVSetMember) [32]byte {
	h := sha256.New()
	text := func(value string) {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(value))
	}
	number := func(value int64) {
		var raw [8]byte
		binary.BigEndian.PutUint64(raw[:], uint64(value))
		_, _ = h.Write(raw[:])
	}
	flag := func(value bool) {
		if value {
			_, _ = h.Write([]byte{1})
		} else {
			_, _ = h.Write([]byte{0})
		}
	}
	text("portico.tv_children.v1")
	text(string(o.Kind))
	text(string(o.Numbering))
	number(o.DeclaredSeasons)
	number(int64(len(o.Seasons)))
	for _, season := range o.Seasons {
		number(season.SeasonNumber)
		number(season.DeclaredEpisodes)
		number(int64(len(season.Episodes)))
		for _, episode := range season.Episodes {
			text(episode.EvidenceID)
			text(string(episode.Order))
			number(episode.SeasonNumber)
			flag(episode.SeasonPresent)
			number(episode.EpisodeNumber)
			flag(episode.EpisodePresent)
			number(episode.AbsoluteNumber)
			flag(episode.AbsolutePresent)
		}
	}
	number(int64(len(members)))
	for _, member := range members {
		text(member.EvidenceID)
		number(member.Ordinal)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
