package writer

import (
	"bytes"
	"math"
	"path"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/naming"
)

const iaMusicRequestVersion uint16 = 1
const iaMusicMaxSongs = 16
const iaMusicMaxArtists = 64

// IA-12 narrows the audio draft to the music variant. The request carries only
// prepared observations: identity source keys are caller naming/scan evidence,
// never derived, normalized or allocated here. No current authority, asset row,
// entity ID or search generation is captured. Provider IDs are deliberately
// absent: filenames and tags remain provisional until Basic accepts technical
// media.
type iaMusicInventoryRequest struct {
	scope     inventoryScope
	source    inventorySource
	run       inventoryRun
	directory inventoryDirectory
	files     []inventoryFile // exactly one original media observation
	complete  bool            // complete ordered cohort assertion, not source proof

	albumKey, albumTitle, albumArtistKey string
	artists                              []iaMusicArtist // complete ordered identity cohort
	songArtistKeys                       []string        // complete ordered song->artist cohort
	songs                                []iaMusicSong   // complete ordered song cohort
	cue                                  *iaMusicCue     // only for a cue-sheet cohort
}

// One artist identity plus its accepted display title. The album-artist key must
// name one member of artists; a song artist that differs is an independent edge.
type iaMusicArtist struct {
	key, title string
}

// One song identity and its exact asset range. cueOrdinal is zero for a
// whole-file song and 1..n for a complete cue cohort; part is the asset link
// part index and is zero only for the whole-file variant.
type iaMusicSong struct {
	key, title       string
	part, cueOrdinal int64
	disc, track      int64
	start            float64
	end              *float64
}

// The cue sheet is an independent sidecar observation. It is a witness only and
// is never linked as media; every song in the cohort carries its range.
type iaMusicCue struct {
	complete bool
	file     inventoryFile
}

type iaMusicInventoryCommand struct {
	identity CommandIdentity
	request  iaMusicInventoryRequest
	digest   RequestDigest
	valid    bool
}

func newIAMusicInventoryCommand(id CommandIdentity, r iaMusicInventoryRequest) (*iaMusicInventoryCommand, error) {
	// Counts precede all text traversal, copying and hashing.
	if len(r.files) != 1 || len(r.artists) == 0 || len(r.artists) > iaMusicMaxArtists ||
		len(r.songArtistKeys) == 0 || len(r.songArtistKeys) > iaMusicMaxArtists ||
		len(r.songs) == 0 || len(r.songs) > iaMusicMaxSongs {
		return nil, invalid("ia_music_count")
	}
	size := uint64(len(canonicalRequestCodec) + 4 + 16 + 8 + 16)
	iaEpisodeWireSize(reflect.ValueOf(r), &size)
	if size > maxCanonicalRequestBytes {
		return nil, &PolicyError{Field: "ia_music_bytes", Limit: maxCanonicalRequestBytes, Cause: ErrCap}
	}
	if !iaEpisodeValidText(reflect.ValueOf(r)) || !utf8.Valid(r.source.resolvedRoot) || bytes.IndexByte(r.source.resolvedRoot, 0) >= 0 {
		return nil, invalid("ia_music_text")
	}
	// The shared walker does not descend pointers; the cue sidecar is text too.
	if r.cue != nil && !iaEpisodeValidText(reflect.ValueOf(*r.cue)) {
		return nil, invalid("ia_music_text")
	}
	if err := validateIAMusicOriginal(id, r); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(IAAcceptAudioInventory, iaMusicRequestVersion, id, r)
	if err != nil {
		return nil, err
	}
	r.files = slices.Clone(r.files)
	r.source.resolvedRoot = bytes.Clone(r.source.resolvedRoot)
	r.artists = slices.Clone(r.artists)
	r.songArtistKeys = slices.Clone(r.songArtistKeys)
	r.songs = slices.Clone(r.songs)
	for i := range r.songs {
		if r.songs[i].end != nil {
			v := *r.songs[i].end
			r.songs[i].end = &v
		}
	}
	if r.cue != nil {
		cue := *r.cue
		r.cue = &cue
	}
	for _, s := range iaMusicTexts(&r) {
		*s = strings.Clone(*s)
	}
	return &iaMusicInventoryCommand{identity: id, request: r, digest: digest, valid: true}, nil
}

// Every accepting gate is a closed witness comparison. Identity keys are the
// caller's prepared evidence, so a reused artist/album/song is bound by the same
// exact key rather than by a title or a movie heuristic. A CUE cohort must be
// complete and strictly ordered; no prefix or overlapping range is accepted.
func validateIAMusicOriginal(id CommandIdentity, r iaMusicInventoryRequest) error {
	s, u, d := r.source, r.run, r.directory
	if id.Sequence <= 0 || id.OperationID == ([16]byte{}) || id.Incarnation == ([16]byte{}) ||
		r.scope.runID == "" || r.scope.libraryKey == "" || r.scope.libraryID <= 0 ||
		s.id == "" || s.incarnation == "" || s.rootIdentity == "" || s.generation <= 0 || len(s.resolvedRoot) == 0 || s.resolvedRoot[0] != '/' ||
		u.revision <= 0 || u.policyRevision <= 0 || u.leaseOwner <= 0 || u.leaseGeneration <= 0 || u.leaseExpiresMS <= 0 || u.lastSequence != id.Sequence-1 || u.acknowledgedSequence != u.lastSequence || u.phase != "inventory" || !inventoryScanMode(u.scanMode) ||
		d.identity == "" || d.revision == "" || d.cursorKind != inventoryLinuxCursor || !inventoryPath(d.path, true) || d.pages < 0 || d.skipped < 0 || d.rawNames != 1 || d.unstable {
		return invalid("ia_music_original")
	}
	root := string(s.resolvedRoot)
	f := r.files[0]
	if !r.complete || strings.ContainsAny(root, `\`) || path.Clean(root) != root || f.sourceKey == "" || f.objectIdentity == "" || f.revision == "" ||
		f.directory || f.size < 0 || f.part < 0 || f.year < 0 || f.metadataLanguage == "" || !inventoryPath(f.relativePath, false) ||
		path.Dir(f.relativePath) != d.path || strings.ContainsAny(f.assetPath, `\`) || path.Clean(f.assetPath) != f.assetPath || f.assetPath != path.Join(root, f.relativePath) {
		return invalid("ia_music_file")
	}
	if !iaMusicKey(r.albumKey) || strings.TrimSpace(r.albumTitle) == "" || !iaMusicKey(r.albumArtistKey) {
		return invalid("ia_music_album")
	}
	artistKeys := make(map[string]bool, len(r.artists))
	for _, a := range r.artists {
		if !iaMusicKey(a.key) || strings.TrimSpace(a.title) == "" || artistKeys[a.key] {
			return invalid("ia_music_artist")
		}
		artistKeys[a.key] = true
	}
	if !artistKeys[r.albumArtistKey] {
		return invalid("ia_music_album_artist")
	}
	songArtists := make(map[string]bool, len(r.songArtistKeys))
	for _, key := range r.songArtistKeys {
		if !artistKeys[key] || songArtists[key] {
			return invalid("ia_music_song_artist")
		}
		songArtists[key] = true
	}
	songKeys := make(map[string]bool, len(r.songs))
	for i, song := range r.songs {
		if !iaMusicKey(song.key) || strings.TrimSpace(song.title) == "" || songKeys[song.key] || song.part < 0 ||
			song.cueOrdinal < 0 || song.disc < 0 || song.track < 0 || math.IsNaN(song.start) || math.IsInf(song.start, 0) || song.start < 0 ||
			song.end != nil && (math.IsNaN(*song.end) || math.IsInf(*song.end, 0) || *song.end <= song.start) {
			return invalid("ia_music_song")
		}
		songKeys[song.key] = true
		if r.cue == nil {
			if len(r.songs) != 1 || i != 0 || song.cueOrdinal != 0 || song.part != 0 || song.start != 0 || song.end != nil {
				return invalid("ia_music_whole_file")
			}
			continue
		}
		if !r.cue.complete || song.cueOrdinal != int64(i+1) || song.part != int64(i) {
			return invalid("ia_music_cue_order")
		}
		if i > 0 {
			previous := r.songs[i-1]
			if song.start <= previous.start || previous.end == nil || *previous.end > song.start {
				return invalid("ia_music_cue_order")
			}
		}
	}
	if r.cue != nil {
		c := r.cue.file
		if c.objectIdentity == "" || c.objectIdentity == f.objectIdentity || c.relativePath == "" || c.relativePath == f.relativePath ||
			!inventoryPath(c.relativePath, false) || path.Dir(c.relativePath) != d.path || c.assetPath != path.Join(root, c.relativePath) {
			return invalid("ia_music_cue_file")
		}
	}
	return nil
}

func iaMusicKey(s string) bool {
	return len(s) > 0 && len(s) <= naming.MaxPathBytes && utf8.ValidString(s) && strings.IndexByte(s, 0) < 0
}

func (c *iaMusicInventoryCommand) checkOriginal() error {
	if c == nil || !c.valid {
		return invalid("ia_music_command")
	}
	checked, err := newIAMusicInventoryCommand(c.identity, c.request)
	if err != nil || checked.digest != c.digest {
		return ErrConflict
	}
	return nil
}

func iaMusicTexts(r *iaMusicInventoryRequest) []*string {
	base := inventoryAcceptRequest{scope: r.scope, source: r.source, run: r.run, directory: r.directory, files: r.files}
	texts := []*string{&r.scope.runID, &r.scope.libraryKey, &r.source.id, &r.source.incarnation, &r.source.rootIdentity, &r.run.phase, &r.run.scanMode, &r.directory.path, &r.directory.identity, &r.directory.revision, &r.directory.expectedCursor, &r.directory.nextCursor, &r.directory.cursorKind}
	texts = append(texts, inventoryRequestTexts(&base)[14:]...)
	texts = append(texts, &r.albumKey, &r.albumTitle, &r.albumArtistKey)
	for i := range r.artists {
		texts = append(texts, &r.artists[i].key, &r.artists[i].title)
	}
	for i := range r.songArtistKeys {
		texts = append(texts, &r.songArtistKeys[i])
	}
	for i := range r.songs {
		texts = append(texts, &r.songs[i].key, &r.songs[i].title)
	}
	if r.cue != nil {
		texts = append(texts, iaEpisodeFileTexts(&r.cue.file)...)
	}
	return texts
}
