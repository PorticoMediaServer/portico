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
const iaEpisodeMaxEvidence = 64

// Prepared observations only. Identity source keys are supplied by the caller's
// naming/scan evidence and are never derived, normalized or allocated here. No
// current authority, asset row, entity ID or search generation is captured.
type iaEpisodeInventoryRequest struct {
	scope     inventoryScope
	source    inventorySource
	run       inventoryRun
	directory inventoryDirectory
	files     []inventoryFile // exactly one original media observation
	naming    naming.Reading
	complete  bool            // complete ordered cohort assertion, not source proof
	showKey   string          // prepared source key of the show subject
	episodes  []iaEpisodeLink // complete ordered link cohort, never a prefix
}

// Independent seasonal/episode/part and optional source range facts. Ranges are
// observations only; they do not assert technical or playback availability.
// seasonKey is empty only when the link has no season container.
type iaEpisodeLink struct {
	season, episode, part int64
	start                 float64
	end                   *float64
	seasonKey, episodeKey string
}
type iaEpisodeInventoryCommand struct {
	identity CommandIdentity
	request  iaEpisodeInventoryRequest
	digest   RequestDigest
	valid    bool
}

func newIAEpisodeInventoryCommand(id CommandIdentity, r iaEpisodeInventoryRequest) (*iaEpisodeInventoryCommand, error) {
	// Counts precede all text traversal, copying and hashing.
	if len(r.files) != 1 || len(r.episodes) == 0 || len(r.episodes) > naming.MaxEpisodeLinks || !iaEpisodeCounts(reflect.ValueOf(r.naming)) {
		return nil, invalid("ia_episode_count")
	}
	for _, e := range r.naming.Episodes {
		if len(e.Value) > naming.MaxEpisodeLinks {
			return nil, invalid("ia_episode_alternative_count")
		}
	}
	size := uint64(len(canonicalRequestCodec) + 4 + 16 + 8 + 16)
	iaEpisodeWireSize(reflect.ValueOf(r), &size)
	if size > maxCanonicalRequestBytes {
		return nil, &PolicyError{Field: "ia_episode_bytes", Limit: maxCanonicalRequestBytes, Cause: ErrCap}
	}
	if !iaEpisodeValidText(reflect.ValueOf(r)) || !utf8.Valid(r.source.resolvedRoot) || bytes.IndexByte(r.source.resolvedRoot, 0) >= 0 {
		return nil, invalid("ia_episode_text")
	}
	if err := validateIAEpisodeOriginal(id, r); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(IAAcceptEpisodeInventory, iaEpisodeRequestVersion, id, r)
	if err != nil {
		return nil, err
	}
	r.files = slices.Clone(r.files)
	r.source.resolvedRoot = bytes.Clone(r.source.resolvedRoot)
	r.naming = iaEpisodeCloneReading(r.naming)
	r.episodes = slices.Clone(r.episodes)
	for i := range r.episodes {
		if r.episodes[i].end != nil {
			v := *r.episodes[i].end
			r.episodes[i].end = &v
		}
	}
	for _, s := range iaEpisodeTexts(&r) {
		*s = strings.Clone(*s)
	}
	return &iaEpisodeInventoryCommand{identity: id, request: r, digest: digest, valid: true}, nil
}

// Every accepting gate is a closed witness comparison. Container keys are the
// caller's prepared identity evidence, so a reused show/season/episode is bound
// by the same exact key rather than by a title or a movie heuristic.
func validateIAEpisodeOriginal(id CommandIdentity, r iaEpisodeInventoryRequest) error {
	s, u, d := r.source, r.run, r.directory
	if id.Sequence <= 0 || id.OperationID == ([16]byte{}) || id.Incarnation == ([16]byte{}) ||
		r.scope.runID == "" || r.scope.libraryKey == "" || r.scope.libraryID <= 0 ||
		s.id == "" || s.incarnation == "" || s.rootIdentity == "" || s.generation <= 0 || len(s.resolvedRoot) == 0 || s.resolvedRoot[0] != '/' ||
		u.revision <= 0 || u.policyRevision <= 0 || u.leaseOwner <= 0 || u.leaseGeneration <= 0 || u.leaseExpiresMS <= 0 || u.lastSequence != id.Sequence-1 || u.acknowledgedSequence != u.lastSequence || u.phase != "inventory" || !inventoryScanMode(u.scanMode) ||
		d.identity == "" || d.revision == "" || d.cursorKind != inventoryLinuxCursor || !inventoryPath(d.path, true) || d.pages < 0 || d.skipped < 0 || d.rawNames != 1 || d.unstable {
		return invalid("ia_episode_original")
	}
	root := string(s.resolvedRoot)
	f := r.files[0]
	if !r.complete || strings.ContainsAny(root, `\`) || path.Clean(root) != root || f.sourceKey == "" || f.objectIdentity == "" || f.revision == "" ||
		f.directory || f.size < 0 || f.part < 0 || f.year < 0 || f.metadataLanguage == "" || !inventoryPath(f.relativePath, false) ||
		path.Dir(f.relativePath) != d.path || strings.ContainsAny(f.assetPath, `\`) || path.Clean(f.assetPath) != f.assetPath || f.assetPath != path.Join(root, f.relativePath) {
		return invalid("ia_episode_file")
	}
	if !iaEpisodeKey(r.showKey) || len(r.naming.Titles) == 0 || strings.TrimSpace(r.naming.Titles[0].Value) == "" {
		return invalid("ia_episode_show")
	}
	seasons := make(map[int64]string, len(r.episodes))
	keys := make(map[string]int64, len(r.episodes))
	episodeKeys := make(map[string]bool, len(r.episodes))
	for i, e := range r.episodes {
		if e.season < 0 || e.episode < 0 || e.part < 0 || math.IsNaN(e.start) || math.IsInf(e.start, 0) || e.start < 0 ||
			e.end != nil && (math.IsNaN(*e.end) || math.IsInf(*e.end, 0) || *e.end <= e.start) || !iaEpisodeKey(e.episodeKey) {
			return invalid("ia_episode_link")
		}
		// A season container is identified by exactly one key and that key
		// identifies exactly one season number; no episode is admitted twice.
		if e.season == 0 {
			if e.seasonKey != "" {
				return invalid("ia_episode_season_key")
			}
		} else {
			if !iaEpisodeKey(e.seasonKey) {
				return invalid("ia_episode_season_key")
			}
			if prior, ok := seasons[e.season]; ok && prior != e.seasonKey {
				return invalid("ia_episode_season_key")
			}
			if prior, ok := keys[e.seasonKey]; ok && prior != e.season {
				return invalid("ia_episode_season_key")
			}
			seasons[e.season], keys[e.seasonKey] = e.seasonKey, e.season
		}
		if episodeKeys[e.episodeKey] {
			return invalid("ia_episode_episode_key")
		}
		episodeKeys[e.episodeKey] = true
		for _, previous := range r.episodes[:i] {
			if previous.season == e.season && previous.episode == e.episode && previous.part == e.part {
				return invalid("ia_episode_duplicate")
			}
		}
	}
	return nil
}

func iaEpisodeKey(s string) bool {
	return len(s) > 0 && len(s) <= naming.MaxPathBytes && utf8.ValidString(s) && strings.IndexByte(s, 0) < 0
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

func iaEpisodeFileTexts(f *inventoryFile) []*string {
	return []*string{&f.sourceKey, &f.relativePath, &f.assetPath, &f.objectIdentity, &f.revision, &f.changeToken, &f.title, &f.sortTitle, &f.sortKey, &f.sortHead, &f.sortKeyKeepArticles, &f.sortHeadKeepArticles, &f.metadataLanguage}
}

func iaEpisodeTexts(r *iaEpisodeInventoryRequest) []*string {
	base := inventoryAcceptRequest{scope: r.scope, source: r.source, run: r.run, directory: r.directory, files: r.files}
	texts := []*string{&r.scope.runID, &r.scope.libraryKey, &r.source.id, &r.source.incarnation, &r.source.rootIdentity, &r.run.phase, &r.run.scanMode, &r.directory.path, &r.directory.identity, &r.directory.revision, &r.directory.expectedCursor, &r.directory.nextCursor, &r.directory.cursorKind}
	texts = append(texts, inventoryRequestTexts(&base)[14:]...)
	texts = append(texts, &r.showKey)
	for i := range r.episodes {
		texts = append(texts, &r.episodes[i].seasonKey, &r.episodes[i].episodeKey)
	}
	return append(texts, iaEpisodeReadingTexts(&r.naming)...)
}

func iaEpisodeCounts(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if !iaEpisodeCounts(v.Field(i)) {
				return false
			}
		}
	case reflect.Slice:
		if v.Len() > iaEpisodeMaxEvidence {
			return false
		}
		for i := 0; i < v.Len(); i++ {
			if !iaEpisodeCounts(v.Index(i)) {
				return false
			}
		}
	}
	return true
}

// Exact size of the shared typed encoding, without reading text contents.
func iaEpisodeTypeSize(t reflect.Type) uint64 {
	n := uint64(1 + 8 + len(t.PkgPath()) + 8 + len(t.Name()))
	if t.Kind() == reflect.Struct && t.Name() == "" {
		n += 8
		for i := 0; i < t.NumField(); i++ {
			n += uint64(8+len(t.Field(i).Name)) + iaEpisodeTypeSize(t.Field(i).Type)
		}
	}
	if t.Kind() == reflect.Array {
		n += 8
	}
	if t.Kind() == reflect.Array || t.Kind() == reflect.Slice || t.Kind() == reflect.Pointer {
		n += iaEpisodeTypeSize(t.Elem())
	}
	return n
}

func iaEpisodeWireSize(v reflect.Value, n *uint64) {
	if *n > maxCanonicalRequestBytes {
		return
	}
	*n += iaEpisodeTypeSize(v.Type())
	switch v.Kind() {
	case reflect.String:
		*n += uint64(8 + v.Len())
	case reflect.Bool:
		*n++
	case reflect.Struct:
		*n += 8
		for i := 0; i < v.NumField(); i++ {
			*n += uint64(8 + len(v.Type().Field(i).Name))
			iaEpisodeWireSize(v.Field(i), n)
		}
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice {
			*n++
			if v.IsNil() {
				return
			}
		}
		*n += 8
		if v.Type().Elem().Kind() == reflect.Uint8 {
			*n += uint64(v.Len())
			return
		}
		for i := 0; i < v.Len(); i++ {
			iaEpisodeWireSize(v.Index(i), n)
		}
	case reflect.Pointer:
		*n++
		if !v.IsNil() {
			iaEpisodeWireSize(v.Elem(), n)
		}
	default:
		*n += 8
	}
}

func iaEpisodeCloneEvidence[T any](values []naming.Evidence[T], clone func(T) T) []naming.Evidence[T] {
	result := slices.Clone(values)
	for i := range result {
		result[i].Source = naming.Source(strings.Clone(string(result[i].Source)))
		result[i].Path = strings.Clone(result[i].Path)
		result[i].Value = clone(result[i].Value)
	}
	return result
}

func iaEpisodeCloneReading(r naming.Reading) naming.Reading {
	identity := func(v int) int { return v }
	r.Titles = iaEpisodeCloneEvidence(r.Titles, strings.Clone)
	r.Years = iaEpisodeCloneEvidence(r.Years, identity)
	r.Seasons = iaEpisodeCloneEvidence(r.Seasons, identity)
	r.Episodes = iaEpisodeCloneEvidence(r.Episodes, slices.Clone[[]int])
	r.AbsoluteNumbers = iaEpisodeCloneEvidence(r.AbsoluteNumbers, identity)
	r.AirDates = iaEpisodeCloneEvidence(r.AirDates, strings.Clone)
	r.Parts = iaEpisodeCloneEvidence(r.Parts, identity)
	r.Editions = iaEpisodeCloneEvidence(r.Editions, strings.Clone)
	r.Versions = iaEpisodeCloneEvidence(r.Versions, strings.Clone)
	r.ProviderIDs = iaEpisodeCloneEvidence(r.ProviderIDs, func(v naming.ProviderID) naming.ProviderID {
		return naming.ProviderID{Provider: strings.Clone(v.Provider), ID: strings.Clone(v.ID)}
	})
	r.Extras = iaEpisodeCloneEvidence(r.Extras, func(v naming.Extra) naming.Extra {
		return naming.Extra{Kind: strings.Clone(v.Kind), ParentPath: strings.Clone(v.ParentPath)}
	})
	r.Discs = iaEpisodeCloneEvidence(r.Discs, func(v naming.Disc) naming.Disc {
		return naming.Disc{Format: strings.Clone(v.Format), ParentPath: strings.Clone(v.ParentPath)}
	})
	r.TrackNumbers = iaEpisodeCloneEvidence(r.TrackNumbers, identity)
	r.DiscNumbers = iaEpisodeCloneEvidence(r.DiscNumbers, identity)
	r.Artists = iaEpisodeCloneEvidence(r.Artists, strings.Clone)
	r.Albums = iaEpisodeCloneEvidence(r.Albums, strings.Clone)
	r.Diagnostics = slices.Clone(r.Diagnostics)
	for i := range r.Diagnostics {
		r.Diagnostics[i] = strings.Clone(r.Diagnostics[i])
	}
	return r
}

func iaEpisodeReadingTexts(r *naming.Reading) []*string {
	var out []*string
	for i := range r.Titles {
		e := &r.Titles[i]
		out = append(out, &e.Path, &e.Value)
	}
	for i := range r.Years {
		out = append(out, &r.Years[i].Path)
	}
	for i := range r.Seasons {
		out = append(out, &r.Seasons[i].Path)
	}
	for i := range r.Episodes {
		out = append(out, &r.Episodes[i].Path)
	}
	for i := range r.AbsoluteNumbers {
		out = append(out, &r.AbsoluteNumbers[i].Path)
	}
	for i := range r.AirDates {
		e := &r.AirDates[i]
		out = append(out, &e.Path, &e.Value)
	}
	for i := range r.Parts {
		out = append(out, &r.Parts[i].Path)
	}
	for i := range r.Editions {
		e := &r.Editions[i]
		out = append(out, &e.Path, &e.Value)
	}
	for i := range r.Versions {
		e := &r.Versions[i]
		out = append(out, &e.Path, &e.Value)
	}
	for i := range r.ProviderIDs {
		e := &r.ProviderIDs[i]
		out = append(out, &e.Path, &e.Value.Provider, &e.Value.ID)
	}
	for i := range r.Extras {
		e := &r.Extras[i]
		out = append(out, &e.Path, &e.Value.Kind, &e.Value.ParentPath)
	}
	for i := range r.Discs {
		e := &r.Discs[i]
		out = append(out, &e.Path, &e.Value.Format, &e.Value.ParentPath)
	}
	for i := range r.TrackNumbers {
		out = append(out, &r.TrackNumbers[i].Path)
	}
	for i := range r.DiscNumbers {
		out = append(out, &r.DiscNumbers[i].Path)
	}
	for i := range r.Artists {
		e := &r.Artists[i]
		out = append(out, &e.Path, &e.Value)
	}
	for i := range r.Albums {
		e := &r.Albums[i]
		out = append(out, &e.Path, &e.Value)
	}
	for i := range r.Diagnostics {
		out = append(out, &r.Diagnostics[i])
	}
	return out
}
