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

const iaAudioRequestVersion uint16 = 1
const iaAudioMaxCueChildren = 16
const iaAudioMaxEvidence = 64

// Original observations only: no allocated IDs, tags, technical facts, admission,
// current source authority or playback availability. Empty folder fields do not
// clear metadata. Naming alternatives retain their independent source facts.
type iaAudioInventoryRequest struct {
	kind      string // closed: music or audiobook
	scope     inventoryScope
	source    inventorySource
	run       inventoryRun
	directory inventoryDirectory
	files     []inventoryFile // exactly one original media observation
	naming    naming.Reading
	book      naming.AudiobookFolder
	cue       *iaAudioCue
}

type iaAudioCue struct {
	complete bool              // captured whole-cohort assertion, not current source proof
	file     inventoryFile     // original cue-sheet observation, independent of media
	children []iaAudioCueChild // complete ordered cohort, never a prefix
}
type iaAudioCueChild struct {
	ordinal, part int64
	title         string
	start         float64
	end           *float64
}
type iaAudioInventoryCommand struct {
	identity CommandIdentity
	request  iaAudioInventoryRequest
	digest   RequestDigest
	valid    bool
}

func newIAAudioInventoryCommand(id CommandIdentity, r iaAudioInventoryRequest) (*iaAudioInventoryCommand, error) {
	if len(r.files) != 1 {
		return nil, invalid("ia_audio_files")
	}
	if r.cue != nil && (len(r.cue.children) == 0 || len(r.cue.children) > iaAudioMaxCueChildren) {
		return nil, invalid("ia_audio_cue_count")
	}
	// Count every naming collection (including nested alternatives) before any
	// text scan/copy/hash; no parser or first-member projection runs here.
	if !iaAudioCounts(reflect.ValueOf(r.naming)) || len(r.book.Narrators) > iaAudioMaxEvidence {
		return nil, invalid("ia_audio_evidence_count")
	}
	// Complete canonical framing plus payload, measured without scanning UTF-8.
	// This is independent of collection ceilings and precedes the digest encoder.
	size := uint64(len(canonicalRequestCodec) + 4 + 16 + 8 + 16)
	iaAudioWireSize(reflect.ValueOf(r), &size)
	if size > maxCanonicalRequestBytes {
		return nil, &PolicyError{Field: "ia_audio_bytes", Limit: maxCanonicalRequestBytes, Cause: ErrCap}
	}
	for _, s := range iaAudioTexts(&r) {
		if !utf8.ValidString(*s) || strings.IndexByte(*s, 0) >= 0 {
			return nil, invalid("ia_audio_text")
		}
	}
	if err := validateIAAudioOriginal(id, r); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(IAAcceptAudioInventory, iaAudioRequestVersion, id, r)
	if err != nil {
		return nil, err
	}
	r.files = slices.Clone(r.files)
	r.source.resolvedRoot = bytes.Clone(r.source.resolvedRoot)
	r.naming = iaAudioCloneReading(r.naming)
	r.book.Narrators = slices.Clone(r.book.Narrators)
	if r.cue != nil {
		cue := *r.cue
		r.cue = &cue
		cue.children = slices.Clone(cue.children)
		r.cue.children = cue.children
		for i := range r.cue.children {
			if p := r.cue.children[i].end; p != nil {
				v := *p
				r.cue.children[i].end = &v
			}
		}
	}
	for _, s := range iaAudioTexts(&r) {
		*s = strings.Clone(*s)
	}
	return &iaAudioInventoryCommand{identity: id, request: r, digest: digest, valid: true}, nil
}

func validateIAAudioOriginal(id CommandIdentity, r iaAudioInventoryRequest) error {
	s, u, d := r.source, r.run, r.directory
	if id.Sequence <= 0 || id.OperationID == ([16]byte{}) || id.Incarnation == ([16]byte{}) ||
		(r.kind != "music" && r.kind != "audiobook") || r.scope.runID == "" || r.scope.libraryKey == "" || r.scope.libraryID <= 0 ||
		s.id == "" || s.incarnation == "" || s.rootIdentity == "" || s.generation <= 0 || len(s.resolvedRoot) == 0 || s.resolvedRoot[0] != '/' || bytes.IndexByte(s.resolvedRoot, 0) >= 0 ||
		u.revision <= 0 || u.policyRevision <= 0 || u.leaseOwner <= 0 || u.leaseGeneration <= 0 || u.leaseExpiresMS <= 0 || u.lastSequence != id.Sequence-1 || u.acknowledgedSequence != u.lastSequence || u.phase != "inventory" || !inventoryScanMode(u.scanMode) ||
		d.identity == "" || d.revision == "" || d.cursorKind != inventoryLinuxCursor || !inventoryPath(d.path, true) || d.pages < 0 || d.skipped < 0 || d.rawNames != 1 || d.unstable {
		return invalid("ia_audio_original")
	}
	if r.kind == "music" && (r.book.Title != "" || r.book.Author != "" || r.book.Series != "" || r.book.Sequence != "" || r.book.Year != "" || r.book.ASIN != "" || len(r.book.Narrators) != 0) {
		return invalid("ia_audio_variant")
	}
	f := r.files[0]
	if !iaAudioFile(f) || path.Dir(f.relativePath) != d.path {
		return invalid("ia_audio_file")
	}
	if r.cue != nil {
		if !r.cue.complete || r.cue.file.objectIdentity == f.objectIdentity || !iaAudioFile(r.cue.file) || path.Dir(r.cue.file.relativePath) != d.path || r.cue.file.relativePath == f.relativePath {
			return invalid("ia_audio_cue_file")
		}
		for i, c := range r.cue.children {
			if c.ordinal != int64(i+1) || c.part < 0 || math.IsNaN(c.start) || math.IsInf(c.start, 0) || c.start < 0 || c.end != nil && (math.IsNaN(*c.end) || math.IsInf(*c.end, 0) || *c.end <= c.start) {
				return invalid("ia_audio_cue_range")
			}
			if i > 0 {
				p := r.cue.children[i-1]
				if c.start <= p.start || p.end == nil || *p.end > c.start {
					return invalid("ia_audio_cue_order")
				}
			}
		}
	}
	return nil
}
func iaAudioFile(f inventoryFile) bool {
	return f.sourceKey != "" && inventoryPath(f.relativePath, false) && f.assetPath != "" && f.assetPath[0] == '/' && f.objectIdentity != "" && f.revision != "" && !f.directory && f.size >= 0 && f.part >= 0 && f.year >= 0
}
func (c *iaAudioInventoryCommand) checkOriginal() error {
	if c == nil || !c.valid {
		return invalid("ia_audio_command")
	}
	checked, err := newIAAudioInventoryCommand(c.identity, c.request)
	if err != nil || checked.digest != c.digest {
		return ErrConflict
	}
	return nil
}

func iaAudioTexts(r *iaAudioInventoryRequest) []*string {
	base := inventoryAcceptRequest{scope: r.scope, source: r.source, run: r.run, directory: r.directory, files: r.files}
	// Pointers to scalar witnesses must address the request itself for freezing.
	texts := []*string{&r.kind, &r.scope.runID, &r.scope.libraryKey, &r.source.id, &r.source.incarnation, &r.source.rootIdentity, &r.run.phase, &r.run.scanMode, &r.directory.path, &r.directory.identity, &r.directory.revision, &r.directory.expectedCursor, &r.directory.nextCursor, &r.directory.cursorKind}
	texts = append(texts, inventoryRequestTexts(&base)[14:]...)
	b := &r.book
	texts = append(texts, &b.Title, &b.Author, &b.Series, &b.Sequence, &b.Year, &b.ASIN)
	for i := range b.Narrators {
		texts = append(texts, &b.Narrators[i])
	}
	if r.cue != nil {
		texts = append(texts, iaAudioFileTexts(&r.cue.file)...)
		for i := range r.cue.children {
			texts = append(texts, &r.cue.children[i].title)
		}
	}
	texts = append(texts, iaAudioReadingTexts(&r.naming)...)
	return texts
}
func iaAudioFileTexts(f *inventoryFile) []*string {
	return []*string{&f.sourceKey, &f.relativePath, &f.assetPath, &f.objectIdentity, &f.revision, &f.changeToken, &f.title, &f.sortTitle, &f.sortKey, &f.sortHead, &f.sortKeyKeepArticles, &f.sortHeadKeepArticles, &f.metadataLanguage}
}

func iaAudioCounts(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if !iaAudioCounts(v.Field(i)) {
				return false
			}
		}
	case reflect.Slice:
		if v.Len() > iaAudioMaxEvidence {
			return false
		}
		for i := 0; i < v.Len(); i++ {
			if !iaAudioCounts(v.Index(i)) {
				return false
			}
		}
	}
	return true
}

// Exact size of the shared typed encoding, without reading text contents.
func iaAudioTypeSize(t reflect.Type) uint64 {
	n := uint64(1 + 8 + len(t.PkgPath()) + 8 + len(t.Name()))
	if t.Kind() == reflect.Struct && t.Name() == "" {
		n += 8
		for i := 0; i < t.NumField(); i++ {
			n += uint64(8+len(t.Field(i).Name)) + iaAudioTypeSize(t.Field(i).Type)
		}
	}
	if t.Kind() == reflect.Array {
		n += 8
	}
	if t.Kind() == reflect.Array || t.Kind() == reflect.Slice || t.Kind() == reflect.Pointer {
		n += iaAudioTypeSize(t.Elem())
	}
	return n
}
func iaAudioWireSize(v reflect.Value, n *uint64) {
	if *n > maxCanonicalRequestBytes {
		return
	}
	*n += iaAudioTypeSize(v.Type())
	switch v.Kind() {
	case reflect.String:
		*n += uint64(8 + v.Len())
	case reflect.Bool:
		*n++
	case reflect.Struct:
		*n += 8
		for i := 0; i < v.NumField(); i++ {
			*n += uint64(8 + len(v.Type().Field(i).Name))
			iaAudioWireSize(v.Field(i), n)
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
			iaAudioWireSize(v.Index(i), n)
		}
	case reflect.Pointer:
		*n++
		if !v.IsNil() {
			iaAudioWireSize(v.Elem(), n)
		}
	default:
		*n += 8
	}
}

func iaAudioCloneEvidence[T any](values []naming.Evidence[T], clone func(T) T) []naming.Evidence[T] {
	result := slices.Clone(values)
	for i := range result {
		result[i].Source = naming.Source(strings.Clone(string(result[i].Source)))
		result[i].Path = strings.Clone(result[i].Path)
		result[i].Value = clone(result[i].Value)
	}
	return result
}

func iaAudioCloneReading(r naming.Reading) naming.Reading {
	identity := func(v int) int { return v }
	r.Titles = iaAudioCloneEvidence(r.Titles, strings.Clone)
	r.Years = iaAudioCloneEvidence(r.Years, identity)
	r.Seasons = iaAudioCloneEvidence(r.Seasons, identity)
	r.Episodes = iaAudioCloneEvidence(r.Episodes, slices.Clone[[]int])
	r.AbsoluteNumbers = iaAudioCloneEvidence(r.AbsoluteNumbers, identity)
	r.AirDates = iaAudioCloneEvidence(r.AirDates, strings.Clone)
	r.Parts = iaAudioCloneEvidence(r.Parts, identity)
	r.Editions = iaAudioCloneEvidence(r.Editions, strings.Clone)
	r.Versions = iaAudioCloneEvidence(r.Versions, strings.Clone)
	r.ProviderIDs = iaAudioCloneEvidence(r.ProviderIDs, func(v naming.ProviderID) naming.ProviderID {
		return naming.ProviderID{Provider: strings.Clone(v.Provider), ID: strings.Clone(v.ID)}
	})
	r.Extras = iaAudioCloneEvidence(r.Extras, func(v naming.Extra) naming.Extra {
		return naming.Extra{Kind: strings.Clone(v.Kind), ParentPath: strings.Clone(v.ParentPath)}
	})
	r.Discs = iaAudioCloneEvidence(r.Discs, func(v naming.Disc) naming.Disc {
		return naming.Disc{Format: strings.Clone(v.Format), ParentPath: strings.Clone(v.ParentPath)}
	})
	r.TrackNumbers = iaAudioCloneEvidence(r.TrackNumbers, identity)
	r.DiscNumbers = iaAudioCloneEvidence(r.DiscNumbers, identity)
	r.Artists = iaAudioCloneEvidence(r.Artists, strings.Clone)
	r.Albums = iaAudioCloneEvidence(r.Albums, strings.Clone)
	r.Diagnostics = slices.Clone(r.Diagnostics)
	for i := range r.Diagnostics {
		r.Diagnostics[i] = strings.Clone(r.Diagnostics[i])
	}
	return r
}

func iaAudioReadingTexts(r *naming.Reading) []*string {
	var out []*string
	for i := range r.Titles {
		e := &r.Titles[i]
		out = append(out, &e.Path)
		out = append(out, &e.Value)
	}
	for i := range r.Years {
		e := &r.Years[i]
		out = append(out, &e.Path)
	}
	for i := range r.Seasons {
		e := &r.Seasons[i]
		out = append(out, &e.Path)
	}
	for i := range r.Episodes {
		e := &r.Episodes[i]
		out = append(out, &e.Path)
	}
	for i := range r.AbsoluteNumbers {
		e := &r.AbsoluteNumbers[i]
		out = append(out, &e.Path)
	}
	for i := range r.AirDates {
		e := &r.AirDates[i]
		out = append(out, &e.Path)
		out = append(out, &e.Value)
	}
	for i := range r.Parts {
		e := &r.Parts[i]
		out = append(out, &e.Path)
	}
	for i := range r.Editions {
		e := &r.Editions[i]
		out = append(out, &e.Path)
		out = append(out, &e.Value)
	}
	for i := range r.Versions {
		e := &r.Versions[i]
		out = append(out, &e.Path)
		out = append(out, &e.Value)
	}
	for i := range r.ProviderIDs {
		e := &r.ProviderIDs[i]
		out = append(out, &e.Path)
		out = append(out, &e.Value.Provider, &e.Value.ID)
	}
	for i := range r.Extras {
		e := &r.Extras[i]
		out = append(out, &e.Path)
		out = append(out, &e.Value.Kind, &e.Value.ParentPath)
	}
	for i := range r.Discs {
		e := &r.Discs[i]
		out = append(out, &e.Path)
		out = append(out, &e.Value.Format, &e.Value.ParentPath)
	}
	for i := range r.TrackNumbers {
		e := &r.TrackNumbers[i]
		out = append(out, &e.Path)
	}
	for i := range r.DiscNumbers {
		e := &r.DiscNumbers[i]
		out = append(out, &e.Path)
	}
	for i := range r.Artists {
		e := &r.Artists[i]
		out = append(out, &e.Path)
		out = append(out, &e.Value)
	}
	for i := range r.Albums {
		e := &r.Albums[i]
		out = append(out, &e.Path)
		out = append(out, &e.Value)
	}
	for i := range r.Diagnostics {
		out = append(out, &r.Diagnostics[i])
	}
	return out
}
