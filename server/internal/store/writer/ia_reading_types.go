package writer

import (
	"bytes"
	"math"
	"path"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
	"github.com/PorticoMediaServer/portico/server/internal/readingcap"
)

const iaReadingRequestVersion uint16 = 1

// One original observation, with library kind, observed format, descriptive
// publication type and path attribution kept independent. Magazine/newspaper
// are descriptive types, not library identities. No decoded text/cover promise,
// accepted revision, allocated identity or current source authority is carried.
type iaReadingInventoryRequest struct {
	kind, format   string
	scope          inventoryScope
	source         inventorySource
	run            inventoryRun
	directory      inventoryDirectory
	files          []inventoryFile
	naming         naming.Reading
	values         compactcatalog.ReadingValues
	classification *iaReadingTypeEvidence
}

// Original explicit classification evidence, matching reading.Metadata's
// source field/raw/complete facts without its decoded text or cover fields.
// Nil means no classification observation, not an implicit Book decision.
type iaReadingTypeEvidence struct {
	sourceFormat, sourceField string
	raw                       []string
	complete                  bool
}
type iaReadingInventoryCommand struct {
	identity CommandIdentity
	request  iaReadingInventoryRequest
	digest   RequestDigest
	valid    bool
}

func newIAReadingInventoryCommand(id CommandIdentity, r iaReadingInventoryRequest) (*iaReadingInventoryCommand, error) {
	if r.classification != nil && (len(r.classification.raw) > 64 || !r.classification.complete) {
		return nil, invalid("ia_reading_classification_count")
	}
	if len(r.files) != 1 || !iaAudioCounts(reflect.ValueOf(r.naming)) {
		return nil, invalid("ia_reading_count")
	}
	for _, e := range r.naming.Episodes {
		if len(e.Value) > naming.MaxEpisodeLinks {
			return nil, invalid("ia_reading_alternative_count")
		}
	}
	size := uint64(len(canonicalRequestCodec) + 4 + 16 + 8 + 16)
	iaAudioWireSize(reflect.ValueOf(r), &size)
	if size > maxCanonicalRequestBytes {
		return nil, &PolicyError{Field: "ia_reading_bytes", Limit: maxCanonicalRequestBytes, Cause: ErrCap}
	}
	if !utf8.Valid(r.source.resolvedRoot) || bytes.IndexByte(r.source.resolvedRoot, 0) >= 0 || !iaReadingText(reflect.ValueOf(r)) {
		return nil, invalid("ia_reading_text")
	}
	base := iaAudioInventoryRequest{kind: "music", scope: r.scope, source: r.source, run: r.run, directory: r.directory, files: r.files}
	if err := validateIAAudioOriginal(id, base); err != nil {
		return nil, err
	}
	root, f := string(r.source.resolvedRoot), r.files[0]
	if strings.Contains(root, `\`) || path.Clean(root) != root || strings.Contains(f.assetPath, `\`) || path.Clean(f.assetPath) != f.assetPath || f.assetPath != path.Join(root, f.relativePath) {
		return nil, invalid("ia_reading_path")
	}
	// catalog.readingFormat's closed extension/build-capability admission rules.
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(f.relativePath)), ".")
	admitted := r.kind == "book" && slices.Contains([]string{"epub", "pdf", "mobi", "azw3"}, ext) || r.kind == "comic" && slices.Contains(readingcap.ComicFormats(), ext)
	if !admitted || r.format != ext {
		return nil, invalid("ia_reading_format")
	}
	if r.values.PublicationType != nil && !compactcatalog.ValidReadingPublicationType(r.values.PublicationType.Value) {
		return nil, invalid("ia_reading_classification")
	}
	if e := r.classification; e != nil {
		if e.sourceFormat != r.format || (r.format != "epub" && r.format != "cbz") || e.sourceField == "" {
			return nil, invalid("ia_reading_classification_source")
		}
		for _, raw := range e.raw {
			if len(raw) > 4096 {
				return nil, invalid("ia_reading_classification_scalar")
			}
		}
	}
	// Preserve only existing closed attribution provenance; owner decisions are
	// not inventory evidence. Absence remains distinct from a present value.
	for _, v := range []*compactcatalog.ReadingValue{r.values.Author, r.values.Series, r.values.IssueDate, r.values.PublicationType} {
		if v != nil && v.Source != compactcatalog.ReadingFileName && v.Source != compactcatalog.ReadingFolder && v.Source != compactcatalog.ReadingFileMetadata {
			return nil, invalid("ia_reading_provenance")
		}
	}
	if v := r.values.SeriesNumber; v != nil && (math.IsNaN(v.Value) || math.IsInf(v.Value, 0) || v.Source != compactcatalog.ReadingFileName && v.Source != compactcatalog.ReadingFolder && v.Source != compactcatalog.ReadingFileMetadata) {
		return nil, invalid("ia_reading_number")
	}
	digest, err := canonicalRequestDigest(IAAcceptReadingInventory, iaReadingRequestVersion, id, r)
	if err != nil {
		return nil, err
	}
	r.files = slices.Clone(r.files)
	r.source.resolvedRoot = bytes.Clone(r.source.resolvedRoot)
	r.naming = iaAudioCloneReading(r.naming)
	r.values = iaReadingCloneValues(r.values)
	if r.classification != nil {
		e := *r.classification
		e.sourceFormat = strings.Clone(e.sourceFormat)
		e.sourceField = strings.Clone(e.sourceField)
		e.raw = slices.Clone(e.raw)
		for i := range e.raw {
			e.raw[i] = strings.Clone(e.raw[i])
		}
		r.classification = &e
	}
	base = iaAudioInventoryRequest{scope: r.scope, source: r.source, run: r.run, directory: r.directory, files: r.files}
	for _, s := range iaAudioTexts(&base) {
		*s = strings.Clone(*s)
	}
	r.scope, r.source, r.run, r.directory = base.scope, base.source, base.run, base.directory
	r.kind, r.format = strings.Clone(r.kind), strings.Clone(r.format)
	return &iaReadingInventoryCommand{identity: id, request: r, digest: digest, valid: true}, nil
}
func iaReadingCloneValues(v compactcatalog.ReadingValues) compactcatalog.ReadingValues {
	v.Title = strings.Clone(v.Title)
	for _, p := range []**compactcatalog.ReadingValue{&v.Author, &v.Series, &v.IssueDate, &v.PublicationType} {
		if *p != nil {
			c := **p
			c.Value = strings.Clone(c.Value)
			c.Source = compactcatalog.ReadingValueSource(strings.Clone(string(c.Source)))
			*p = &c
		}
	}
	if v.SeriesNumber != nil {
		c := *v.SeriesNumber
		c.Source = compactcatalog.ReadingValueSource(strings.Clone(string(c.Source)))
		v.SeriesNumber = &c
	}
	return v
}
func iaReadingText(v reflect.Value) bool {
	if v.Kind() == reflect.Pointer {
		return v.IsNil() || iaReadingText(v.Elem())
	}
	if v.Kind() == reflect.Struct {
		for i := 0; i < v.NumField(); i++ {
			if !iaReadingText(v.Field(i)) {
				return false
			}
		}
		return true
	}
	if v.Kind() == reflect.Slice && v.Type().Elem().Kind() != reflect.Uint8 {
		for i := 0; i < v.Len(); i++ {
			if !iaReadingText(v.Index(i)) {
				return false
			}
		}
		return true
	}
	return iaEpisodeValidText(v)
}
func (c *iaReadingInventoryCommand) checkOriginal() error {
	if c == nil || !c.valid {
		return invalid("ia_reading_command")
	}
	checked, err := newIAReadingInventoryCommand(c.identity, c.request)
	if err != nil || checked.digest != c.digest {
		return ErrConflict
	}
	return nil
}
