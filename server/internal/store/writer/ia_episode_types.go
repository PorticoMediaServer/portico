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

const iaEpisodeRequestVersion uint16 = 1

// Prepared observations, never allocated identities or current authority. The
// complete assertion covers the entire ordered link cohort, not a prefix.
type iaEpisodeInventoryRequest struct {
	scope     inventoryScope
	source    inventorySource
	run       inventoryRun
	directory inventoryDirectory
	files     []inventoryFile
	naming    naming.Reading
	complete  bool
	episodes  []iaEpisodeLink
}

// Independent seasonal/episode/part and optional source range facts. Ranges
// are observations only; they do not assert technical or playback availability.
type iaEpisodeLink struct {
	season, episode, part int64
	start                 float64
	end                   *float64
}
type iaEpisodeInventoryCommand struct {
	identity CommandIdentity
	request  iaEpisodeInventoryRequest
	digest   RequestDigest
	valid    bool
}

func newIAEpisodeInventoryCommand(id CommandIdentity, r iaEpisodeInventoryRequest) (*iaEpisodeInventoryCommand, error) {
	// Counts precede all text traversal, copying and hashing.
	if len(r.files) != 1 || len(r.episodes) == 0 || len(r.episodes) > naming.MaxEpisodeLinks || !iaAudioCounts(reflect.ValueOf(r.naming)) {
		return nil, invalid("ia_episode_count")
	}
	for _, e := range r.naming.Episodes {
		if len(e.Value) > naming.MaxEpisodeLinks {
			return nil, invalid("ia_episode_alternative_count")
		}
	}
	size := uint64(len(canonicalRequestCodec) + 4 + 16 + 8 + 16)
	iaAudioWireSize(reflect.ValueOf(r), &size)
	if size > maxCanonicalRequestBytes {
		return nil, &PolicyError{Field: "ia_episode_bytes", Limit: maxCanonicalRequestBytes, Cause: ErrCap}
	}
	if !utf8.Valid(r.source.resolvedRoot) || bytes.IndexByte(r.source.resolvedRoot, 0) >= 0 {
		return nil, invalid("ia_episode_root")
	}
	// Include evidence Source strings as well as values and paths.
	if !iaEpisodeValidText(reflect.ValueOf(r)) {
		return nil, invalid("ia_episode_text")
	}
	base := iaAudioInventoryRequest{kind: "music", scope: r.scope, source: r.source, run: r.run, directory: r.directory, files: r.files}
	if err := validateIAAudioOriginal(id, base); err != nil {
		return nil, err
	}
	root := string(r.source.resolvedRoot)
	f := r.files[0]
	if !r.complete || strings.ContainsAny(root, `\`) || path.Clean(root) != root || strings.ContainsAny(f.assetPath, `\`) || path.Clean(f.assetPath) != f.assetPath || f.assetPath != path.Join(root, f.relativePath) {
		return nil, invalid("ia_episode_original")
	}
	for i, e := range r.episodes {
		if e.season < 0 || e.episode < 0 || e.part < 0 || math.IsNaN(e.start) || math.IsInf(e.start, 0) || e.start < 0 || e.end != nil && (math.IsNaN(*e.end) || math.IsInf(*e.end, 0) || *e.end <= e.start) {
			return nil, invalid("ia_episode_link")
		}
		for _, previous := range r.episodes[:i] {
			if previous.season == e.season && previous.episode == e.episode && previous.part == e.part {
				return nil, invalid("ia_episode_duplicate")
			}
		}
	}
	digest, err := canonicalRequestDigest(IAAcceptEpisodeInventory, iaEpisodeRequestVersion, id, r)
	if err != nil {
		return nil, err
	}
	r.files = slices.Clone(r.files)
	r.source.resolvedRoot = bytes.Clone(r.source.resolvedRoot)
	r.naming = iaAudioCloneReading(r.naming)
	r.episodes = slices.Clone(r.episodes)
	for i := range r.episodes {
		if r.episodes[i].end != nil {
			v := *r.episodes[i].end
			r.episodes[i].end = &v
		}
	}
	// Reuse the scalar/file freezer without projecting movie or audio metadata.
	base = iaAudioInventoryRequest{scope: r.scope, source: r.source, run: r.run, directory: r.directory, files: r.files}
	for _, s := range iaAudioTexts(&base) {
		*s = strings.Clone(*s)
	}
	r.scope, r.source, r.run, r.directory = base.scope, base.source, base.run, base.directory
	return &iaEpisodeInventoryCommand{identity: id, request: r, digest: digest, valid: true}, nil
}

func iaEpisodeValidText(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		s := v.String()
		return utf8.ValidString(s) && !strings.ContainsRune(s, 0)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if !iaEpisodeValidText(v.Field(i)) {
				return false
			}
		}
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return true
		}
		for i := 0; i < v.Len(); i++ {
			if !iaEpisodeValidText(v.Index(i)) {
				return false
			}
		}
	}
	return true
}
func (c *iaEpisodeInventoryCommand) checkOriginal() error {
	if c == nil || !c.valid {
		return invalid("ia_episode_command")
	}
	checked, err := newIAEpisodeInventoryCommand(c.identity, c.request)
	if err != nil || checked.digest != c.digest {
		return ErrConflict
	}
	return nil
}
