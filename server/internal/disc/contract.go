// Package disc contains the bounded portico-disc protocol. A disc title is a
// library-reader selection, never a concatenation of VOB or M2TS files.
package disc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const Schema = 1
const MaxOutputBytes = 1 << 20
const MaxTitles = 4096

var ErrProtocol = errors.New("invalid disc probe response")
var ErrEncrypted = errors.New("encrypted discs are not supported")
var ErrRemoteUnqualified = errors.New("remote disc streaming is not qualified")

const EncryptedMessage = "Not supported. Portico can’t play encrypted discs."
const RemoteMessage = "Not supported yet. Portico can’t play discs from remote folders."

// All time fields are integer milliseconds relative to the selected title.
// Clip input/output points use their playlist's 90 kHz clock, stated in names.
// Playlist is a pointer because playlist zero is a valid Blu-ray selection.
type Probe struct {
	Schema     int     `json:"schema"`
	Kind       string  `json:"kind"`
	Packaging  string  `json:"packaging"`
	Encryption string  `json:"encryption"`
	Titles     []Title `json:"titles"`
}
type Title struct {
	DiscOrder    int       `json:"discOrder"`
	Playlist     *int      `json:"playlist,omitempty"`
	PlaylistFile string    `json:"playlistFile,omitempty"`
	Title        int       `json:"title,omitempty"`
	VTS          int       `json:"vts,omitempty"`
	PGC          int       `json:"pgc,omitempty"`
	DurationMs   int64     `json:"durationMs"`
	AngleCount   int       `json:"angleCount"`
	Clips        []Clip    `json:"clips"`
	Streams      []Stream  `json:"streams"`
	Chapters     []Chapter `json:"chapters"`
}
type Clip struct {
	Sequence   int    `json:"sequence"`
	File       string `json:"file"`
	InTime90k  int64  `json:"inTime90k"`
	OutTime90k int64  `json:"outTime90k"`
}
type Stream struct {
	// ClipSequence identifies each STN PID in each ordered clip. PID repeats
	// between clips are retained, since language or coding may change.
	ClipSequence                int    `json:"clipSequence"`
	PID                         int    `json:"pid"`
	Kind                        string `json:"kind"`
	Coding                      int    `json:"coding"`
	Language                    string `json:"language"`
	DolbyVisionEnhancementLayer bool   `json:"dolbyVisionEnhancementLayer"`
	// A false enhancement flag with Known=false is unobserved, not proof of BL.
	DolbyVisionEnhancementKnown bool `json:"dolbyVisionEnhancementKnown"`
	// Only an observed PMT dependency supplies this PID. STN EL membership
	// alone never invents a relationship to a neighbouring or first video PID.
	DolbyVisionBasePID *int `json:"dolbyVisionBasePid,omitempty"`
	// VideoRole is the observed libbluray STN category, when supplied. It is
	// not inferred from resolution, PID order, RPU presence or file naming.
	VideoRole string `json:"videoRole,omitempty"`
}
type Chapter struct {
	StartMs int64 `json:"startMs"`
}
type Seek struct {
	Schema      int   `json:"schema"`
	Playlist    int   `json:"playlist"`
	RequestedMs int64 `json:"requestedMs"`
	ByteOffset  int64 `json:"byteOffset"`
	TitleTimeMs int64 `json:"titleTimeMs"`
}

var playlistFile = regexp.MustCompile(`^[0-9]{5}\.mpls$`)
var clipFile = regexp.MustCompile(`^[0-9]{5}\.m2ts$`)
var isoLanguage = regexp.MustCompile(`^(?:[a-z]{2,3})?$`)

func decode(raw []byte, out any) error {
	if len(raw) == 0 || len(raw) > MaxOutputBytes {
		return ErrProtocol
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return ErrProtocol
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return ErrProtocol
	}
	return nil
}
func DecodeProbe(raw []byte) (Probe, error) {
	var p Probe
	if err := decode(raw, &p); err != nil {
		return p, err
	}
	if p.Schema != Schema || (p.Kind != "bluray" && p.Kind != "dvd") || (p.Packaging != "iso" && p.Packaging != "folder") || p.Titles == nil || len(p.Titles) > MaxTitles {
		return p, ErrProtocol
	}
	switch p.Encryption {
	case "none", "aacs", "bdplus", "css":
	default:
		return p, ErrProtocol
	}
	seen := map[string]bool{}
	for i, t := range p.Titles {
		if t.DiscOrder != i || t.DurationMs < 60000 || t.DurationMs > 7*24*60*60*1000 || t.AngleCount < 1 || t.AngleCount > 255 || t.Clips == nil || len(t.Clips) > 4096 || t.Streams == nil || len(t.Streams) > 65536 || t.Chapters == nil || len(t.Chapters) > 65535 {
			return p, ErrProtocol
		}
		key := ""
		if p.Kind == "bluray" {
			if t.Playlist == nil || *t.Playlist < 0 || *t.Playlist > 99999 || !playlistFile.MatchString(t.PlaylistFile) || t.PlaylistFile != fmt.Sprintf("%05d.mpls", *t.Playlist) || t.Title != 0 || t.VTS != 0 || t.PGC != 0 || len(t.Clips) == 0 {
				return p, ErrProtocol
			}
			key = t.PlaylistFile
		} else {
			if t.Playlist != nil || t.PlaylistFile != "" || t.Title < 1 || t.Title > 99 || t.VTS < 1 || t.VTS > 99 || t.PGC < 1 || len(t.Clips) > 0 {
				return p, ErrProtocol
			}
			key = fmt.Sprint(t.Title)
		}
		if seen[key] {
			return p, ErrProtocol
		}
		seen[key] = true
		for j, c := range t.Clips {
			if c.Sequence != j || !clipFile.MatchString(c.File) || c.InTime90k < 0 || c.OutTime90k <= c.InTime90k {
				return p, ErrProtocol
			}
		}
		last := int64(-1)
		for _, c := range t.Chapters {
			if c.StartMs < 0 || c.StartMs >= t.DurationMs || c.StartMs <= last {
				return p, ErrProtocol
			}
			last = c.StartMs
		}
		basePIDs := make(map[[2]int]bool)
		for _, stream := range t.Streams {
			if stream.Kind == "video" && stream.Coding == 0x24 && !stream.DolbyVisionEnhancementLayer {
				basePIDs[[2]int{stream.ClipSequence, stream.PID}] = true
			}
		}
		for _, s := range t.Streams {
			if s.ClipSequence < 0 || p.Kind == "bluray" && s.ClipSequence >= len(t.Clips) || s.PID < 0 || s.PID > 8191 || s.Coding < 0 || s.Coding > 255 || !isoLanguage.MatchString(s.Language) || (s.Kind != "video" && s.Kind != "audio" && s.Kind != "subtitle" && s.Kind != "interactive") || s.DolbyVisionEnhancementLayer && !s.DolbyVisionEnhancementKnown {
				return p, ErrProtocol
			}
			if s.VideoRole != "" && (p.Kind != "bluray" || s.Kind != "video" ||
				(s.VideoRole != "primary" && s.VideoRole != "secondary" && s.VideoRole != "enhancement") ||
				(s.VideoRole == "enhancement") != s.DolbyVisionEnhancementLayer) {
				return p, ErrProtocol
			}
			if s.DolbyVisionBasePID != nil && (p.Kind != "bluray" || !s.DolbyVisionEnhancementLayer ||
				!s.DolbyVisionEnhancementKnown || s.Kind != "video" || s.Coding != 0x24 ||
				*s.DolbyVisionBasePID <= 31 || *s.DolbyVisionBasePID >= 8191 || *s.DolbyVisionBasePID == s.PID) {
				return p, ErrProtocol
			}
			if s.DolbyVisionBasePID != nil {
				if !basePIDs[[2]int{s.ClipSequence, *s.DolbyVisionBasePID}] {
					return p, ErrProtocol
				}
			}
		}
	}
	if p.Encryption != "none" {
		if len(p.Titles) != 0 {
			return p, ErrProtocol
		}
		return p, ErrEncrypted
	}
	return p, nil
}
func DecodeSeek(raw []byte, playlist int, requestedMs int64) (Seek, error) {
	var s Seek
	if err := decode(raw, &s); err != nil {
		return s, err
	}
	if s.Schema != Schema || s.Playlist != playlist || s.RequestedMs != requestedMs || requestedMs < 0 || s.ByteOffset < 0 || s.TitleTimeMs < 0 || s.TitleTimeMs > requestedMs {
		return s, ErrProtocol
	}
	return s, nil
}

// OrderedTitles supplies stable Title N display positions. Changing the main
// title is an independent owner choice and never reorders these names.
func (p Probe) OrderedTitles() []Title {
	out := append([]Title(nil), p.Titles...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].DurationMs > out[j].DurationMs })
	return out
}

// MaxConsumedMembers bounds the file manifest one folder analysis may stat.
const MaxConsumedMembers = 256

// DiscMember is one file a published disc structure or a selected title read
// consumes, relative to the admitted disc folder. Required members must exist
// and be regular; optional structure files may be absent.
type DiscMember struct {
	Path      string
	Required  bool
	Directory bool
}

// ConsumedMembers names every file the published title list and its media
// reads depend on. The manifest covers all titles, not only the selected one,
// because every title is published; it refuses a disc that needs more members
// than the bound instead of sampling.
func ConsumedMembers(p Probe) ([]DiscMember, error) {
	if p.Packaging != "folder" {
		return nil, ErrProtocol
	}
	out := []DiscMember{}
	index := map[string]int{}
	overBound := fmt.Errorf("disc needs more than %d consumed members", MaxConsumedMembers)
	// add stops the traversal at the bound instead of naming every member first
	// and refusing afterwards: a probe with a long title list must not make the
	// manifest do unbounded work before it can say no.
	add := func(path string, required, directory bool) error {
		if i, ok := index[path]; ok {
			out[i].Required = out[i].Required || required
			out[i].Directory = out[i].Directory || directory
			return nil
		}
		if len(out) >= MaxConsumedMembers {
			return overBound
		}
		index[path] = len(out)
		out = append(out, DiscMember{Path: path, Required: required, Directory: directory})
		return nil
	}
	switch p.Kind {
	case "bluray":
		// The title set is discovered from this directory membership, so the
		// directories' identity and revision are part of the witness: a
		// playlist added between observations changes the directory even
		// though no named file changed.
		for _, dir := range []string{"BDMV", "BDMV/PLAYLIST", "BDMV/CLIPINF", "BDMV/STREAM"} {
			if err := add(dir, true, true); err != nil {
				return nil, err
			}
		}
		if err := add("BDMV/index.bdmv", true, false); err != nil {
			return nil, err
		}
		if err := add("BDMV/MovieObject.bdmv", false, false); err != nil {
			return nil, err
		}
		for _, t := range p.Titles {
			if err := add("BDMV/PLAYLIST/"+t.PlaylistFile, true, false); err != nil {
				return nil, err
			}
			for _, clip := range t.Clips {
				if err := add("BDMV/STREAM/"+clip.File, true, false); err != nil {
					return nil, err
				}
				if base, ok := strings.CutSuffix(clip.File, ".m2ts"); ok {
					if err := add("BDMV/CLIPINF/"+base+".clpi", true, false); err != nil {
						return nil, err
					}
				}
			}
		}
	case "dvd":
		if err := add("VIDEO_TS", true, true); err != nil {
			return nil, err
		}
		if err := add("VIDEO_TS/VIDEO_TS.IFO", true, false); err != nil {
			return nil, err
		}
		for _, t := range p.Titles {
			if err := add(fmt.Sprintf("VIDEO_TS/VTS_%02d_0.IFO", t.VTS), true, false); err != nil {
				return nil, err
			}
			if err := add(fmt.Sprintf("VIDEO_TS/VTS_%02d_1.VOB", t.VTS), true, false); err != nil {
				return nil, err
			}
			for n := 0; n < 10; n++ {
				if n != 1 {
					if err := add(fmt.Sprintf("VIDEO_TS/VTS_%02d_%d.VOB", t.VTS, n), false, false); err != nil {
						return nil, err
					}
				}
			}
		}
	default:
		return nil, ErrProtocol
	}
	return out, nil
}

// MainTitle is the title a disc plays by default: the longest title of at
// least a minute, ties keeping disc order. Shorter titles stay listed after it.
func (p Probe) MainTitle() (Title, bool) {
	for _, t := range p.OrderedTitles() {
		if t.DurationMs >= 60_000 {
			return t, true
		}
	}
	return Title{}, false
}

func (t Title) Key(kind string) string {
	if kind == "bluray" && t.Playlist != nil {
		return fmt.Sprintf("bd:%05d", *t.Playlist)
	}
	if kind == "dvd" {
		return fmt.Sprintf("dvd:%d", t.Title)
	}
	return ""
}
func cleanLocalPath(path string) bool { return filepath.IsAbs(path) && filepath.Clean(path) == path }
