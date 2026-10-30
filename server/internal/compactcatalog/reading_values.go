package compactcatalog

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"math"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Reading values are library-scoped attributions, never people or audio Author
// entities. Source rules consume accepted inventory paths; they do no file I/O
// and cannot authorize a scan, archive read or publication.
type ReadingValueSource string

const (
	ReadingFileName     ReadingValueSource = "file-name"
	ReadingFolder       ReadingValueSource = "folder"
	ReadingOwner        ReadingValueSource = "owner"
	ReadingFileMetadata ReadingValueSource = "file-metadata"
)

var ErrReadingValue = errors.New("invalid reading attribution value")

type ReadingValue struct {
	Value  string
	Source ReadingValueSource
}

type ReadingNumber struct {
	Value  float64
	Source ReadingValueSource
}

type ReadingValues struct {
	Title           string
	Author, Series  *ReadingValue
	IssueDate       *ReadingValue
	PublicationType *ReadingValue
	SeriesNumber    *ReadingNumber
}

type ReadingRuleInput struct {
	Item                     int64
	SourceRoot, RelativePath string // Actual admitted library folder root, not an asset token.
	Kind                     Kind
}

type ReadingOwnerValue struct {
	Value  string
	Locked bool
}

var readingResolutionTag = regexp.MustCompile(`(?i)\b(?:480|576|720|1080|1440|2160|4320)p\b`)
var readingTags = regexp.MustCompile(`\[[^\[\]]*\]|\([^()]*\)`)
var readingMarkedNumber = regexp.MustCompile(`(?i)(?:^|[ _.-])(?:v(?:ol(?:ume)?\.?)?|issue|book|#)\s*([0-9]+(?:\.[0-9]+)?)(?:$|[ _-])`)
var readingDashNumber = regexp.MustCompile(`\s+-\s+([0-9]+(?:\.[0-9]+)?)(?:\s+-\s+|$)`)
var readingStartsNumber = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)(?:$|[ _-])`)
var readingSequence = regexp.MustCompile(`(?i)(?:^|[ _.-])(?:(?:v(?:ol(?:ume)?\.?)?|issue|book|#)\s*)?([0-9]+(?:\.[0-9]+)?)(?:$|[ _-])`)
var readingDate = regexp.MustCompile(`\b(?:19|20)[0-9]{2}(?:-[0-9]{2}(?:-[0-9]{2})?|[0-9]{4})\b`)
var readingOwnerNumber = regexp.MustCompile(`^[+]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

type readingDirectory struct{ root, path string }

var readingGenericFolders = map[string]bool{
	"books": true, "ebooks": true, "e-books": true, "comics": true, "manga": true, "magazines": true, "library": true,
	"calibre library": true, "unsorted": true, "misc": true, "new": true, "downloads": true, "inbox": true, "to read": true,
}

func readingFolders(relative string) []string {
	var out []string
	parts := strings.Split(relative, "/")
	for _, part := range parts[:len(parts)-1] {
		if strings.TrimSpace(part) != "" && !readingGenericFolders[ReadingValueKey(readingCleanTitle(part))] {
			out = append(out, part)
		}
	}
	return out
}

func readingFolderLabel(value string) string {
	if strings.ContainsAny(value, "\r\n") {
		return strings.Join(strings.Fields(value), " ")
	}
	return value
}

func readingFileAuthor(stem string) (string, string) {
	if strings.Count(stem, " - ") != 1 {
		return "", stem
	}
	left, right, _ := strings.Cut(stem, " - ")
	left, right = strings.TrimSpace(left), strings.TrimSpace(right)
	first, _ := utf8.DecodeRuneInString(left)
	if left == "" || right == "" || unicode.IsDigit(first) || readingHasSequence(left) {
		return "", stem
	}
	if strings.ContainsAny(left, "\r\n") {
		left = strings.Join(strings.Fields(left), " ")
	}
	return left, right
}

func readingHasSequence(stem string) bool {
	for _, match := range readingSequence.FindAllStringSubmatchIndex(stem, -1) {
		if !readingNumberOverlapsDate(stem, match[2], match[3]) {
			return true
		}
	}
	return false
}

func readingNumberOverlapsDate(stem string, start, end int) bool {
	for _, date := range readingDate.FindAllStringIndex(stem, -1) {
		if start < date[1] && end > date[0] {
			return true
		}
	}
	return false
}

func readingCleanTitle(s string) string {
	s = readingTags.ReplaceAllString(s, " ")
	s = readingResolutionTag.ReplaceAllString(s, " ")
	s = strings.NewReplacer("_", " ", ".", " ").Replace(s)
	return strings.Trim(strings.Join(strings.Fields(s), " "), " -")
}

// ReadingFilenameTitle is the forgiving ingest fallback, before matching or
// folder context exists. A poor name never prevents creating a readable item.
func ReadingFilenameTitle(filename string) string {
	filename = path.Base(filename)
	stem := strings.TrimSuffix(filename, path.Ext(filename))
	_, title := readingFileAuthor(stem)
	_, title = readingNumber(readingTags.ReplaceAllString(title, " "), "")
	if title = readingCleanTitle(title); title != "" {
		return title
	}
	if title = readingCleanTitle(stem); title != "" {
		return title
	}
	if title = strings.TrimSpace(stem); title != "" {
		return title
	}
	return strings.TrimSpace(filename)
}

func readingStripPrefix(value, prefix string) (string, bool) {
	v, p := []rune(value), []rune(prefix)
	if len(v) < len(p) || !strings.EqualFold(string(v[:len(p)]), prefix) {
		return value, false
	}
	if len(v) > len(p) && !strings.ContainsRune(" _.-#0123456789", v[len(p)]) {
		return value, false
	}
	return strings.TrimLeft(string(v[len(p):]), " _.-"), true
}

func readingNumber(stem, series string) (*ReadingNumber, string) {
	for _, pattern := range []*regexp.Regexp{readingMarkedNumber, readingDashNumber} {
		for _, match := range pattern.FindAllStringSubmatchIndex(stem, -1) {
			overlapsDate := false
			for _, date := range readingDate.FindAllStringIndex(stem, -1) {
				if match[2] < date[1] && match[3] > date[0] {
					overlapsDate = true
				}
			}
			if overlapsDate {
				continue
			}
			n, err := strconv.ParseFloat(stem[match[2]:match[3]], 64)
			if err == nil && !math.IsInf(n, 0) {
				prefix := stem[:match[0]]
				if series != "" && strings.EqualFold(readingCleanTitle(prefix), readingCleanTitle(series)) {
					prefix = ""
				}
				return &ReadingNumber{n, ReadingFileName}, prefix + " " + stem[match[1]:]
			}
		}
	}
	rest := stem
	if stripped, ok := readingStripPrefix(stem, series); ok && series != "" {
		rest = stripped
	}
	if series != "" && !readingDate.MatchString(rest) {
		match := readingStartsNumber.FindStringSubmatch(rest)
		if len(match) > 1 {
			n, err := strconv.ParseFloat(match[1], 64)
			if err == nil && !math.IsInf(n, 0) {
				return &ReadingNumber{n, ReadingFileName}, strings.TrimLeft(rest[len(match[0]):], " -")
			}
		}
	}
	return nil, stem
}

// ReadingPathValues consumes accepted paths relative to the library root.
// Inputs are ordered by the producer's canonical version selection; versions
// of the same item count as one title, using that first version's path. No
// spelling/capitalisation-based person classifier is used. Section 45 permits
// source-labelled folder guesses and excludes fixed generic folder names.
// ReadingPathFolderEvidence identifies the actual innermost non-generic
// directory and whether this title supports its series interpretation. Generic
// descendants are ignored, but distinct physical directories never coalesce.
func ReadingPathFolderEvidence(in ReadingRuleInput) (string, bool, error) {
	if err := readingPathInput(in); err != nil {
		return "", false, err
	}
	key, err := ReadingDirectoryKey(path.Dir(in.RelativePath))
	if err != nil {
		return "", false, err
	}
	if key == "" {
		return "", false, nil
	}
	stem := strings.TrimSuffix(path.Base(in.RelativePath), path.Ext(in.RelativePath))
	_, title := readingFileAuthor(stem)
	return key, strings.HasPrefix(ReadingValueKey(readingCleanTitle(title)), ReadingValueKey(readingCleanTitle(path.Base(key)))) || readingHasSequence(title), nil
}

func ReadingDirectoryKey(directory string) (string, error) {
	if directory == "." {
		return "", nil
	}
	if directory == "" || len(directory) > 4096 || !utf8.ValidString(directory) || path.Clean(directory) != directory || strings.HasPrefix(directory, "/") || strings.HasPrefix(directory, "../") || directory == ".." || strings.ContainsAny(directory, "\\\x00") {
		return "", ErrReadingValue
	}
	parts := strings.Split(directory, "/")
	end := -1
	for i, part := range parts {
		if strings.TrimSpace(part) != "" && !readingGenericFolders[ReadingValueKey(readingCleanTitle(part))] {
			end = i
		}
	}
	if end < 0 {
		return "", nil
	}
	return strings.Join(parts[:end+1], "/"), nil
}

func readingPathInput(in ReadingRuleInput) error {
	if in.Item < 1 || in.SourceRoot == "" || len(in.SourceRoot) > 4096 || !utf8.ValidString(in.SourceRoot) || strings.ContainsRune(in.SourceRoot, 0) || len(in.RelativePath) > 4096 || !utf8.ValidString(in.RelativePath) || path.Clean(in.RelativePath) != in.RelativePath || strings.HasPrefix(in.RelativePath, "/") || strings.ContainsAny(in.RelativePath, "\\\x00") || in.RelativePath == "." || strings.HasPrefix(in.RelativePath, "../") || (in.Kind != Ebook && in.Kind != Comic) {
		return ErrReadingValue
	}
	return nil
}

// ReadingPathValue applies a counted folder context. The ingest producer uses
// this form while streaming bounded batches; it need not retain a whole source.
func ReadingPathValue(in ReadingRuleInput, titles int, everySeries bool) (ReadingValues, error) {
	if err := readingPathInput(in); err != nil || titles < 1 {
		return ReadingValues{}, ErrReadingValue
	}
	parts := readingFolders(in.RelativePath)
	stem := strings.TrimSuffix(path.Base(in.RelativePath), path.Ext(in.RelativePath))
	author, title := readingFileAuthor(stem)
	v := ReadingValues{}
	if date := ReadingIssueDate(in.RelativePath); date != "" {
		v.IssueDate = &ReadingValue{date, ReadingFileName}
	}
	if author != "" {
		v.Author = &ReadingValue{author, ReadingFileName}
	}
	if len(parts) > 0 {
		inner := readingFolderLabel(parts[len(parts)-1])
		parent := ""
		if len(parts) > 1 {
			parent = readingFolderLabel(parts[len(parts)-2])
		}
		switch {
		case titles == 1:
			if ReadingValueKey(readingCleanTitle(inner)) == ReadingValueKey(readingCleanTitle(title)) {
				if parent != "" && v.Author == nil {
					v.Author = &ReadingValue{parent, ReadingFolder}
				}
			} else if v.Author == nil {
				v.Author = &ReadingValue{inner, ReadingFolder}
			}
		case parent != "":
			if v.Author == nil {
				v.Author = &ReadingValue{parent, ReadingFolder}
			}
			v.Series = &ReadingValue{inner, ReadingFolder}
		case everySeries:
			v.Series = &ReadingValue{inner, ReadingFolder}
		default:
			if v.Author == nil {
				v.Author = &ReadingValue{inner, ReadingFolder}
			}
		}
	}
	series := ""
	if v.Series != nil {
		series = v.Series.Value
	}
	v.SeriesNumber, title = readingNumber(readingTags.ReplaceAllString(title, " "), series)
	v.Title = readingCleanTitle(title)
	if series != "" && v.SeriesNumber == nil {
		if rest, ok := readingStripPrefix(v.Title, readingCleanTitle(series)); ok && rest != "" {
			v.Title = rest
		}
	}
	// A name that carries only the matching series and its number, such as
	// "Little Nemo 01.cbz" inside Little Nemo/, leaves no title of its own. The
	// accepted series name is then the volume's title. Never restore the raw
	// stem here: it would put the sequence number back into the title.
	if v.Title == "" && v.Series != nil {
		v.Title = v.Series.Value
	}
	if v.Title == "" {
		v.Title = readingCleanTitle(stem)
	}
	if v.Title == "" {
		v.Title = strings.TrimSpace(stem)
	}
	if v.Title == "" {
		v.Title = path.Base(in.RelativePath)
	}
	return v, nil
}

func ReadingPathValues(items []ReadingRuleInput) (map[int64]ReadingValues, error) {
	type folder struct {
		count     int
		allSeries bool
	}
	folders := map[readingDirectory]*folder{}
	canonical := make([]ReadingRuleInput, 0, len(items))
	seen := map[int64]Kind{}
	for _, in := range items {
		key, evidence, err := ReadingPathFolderEvidence(in)
		if err != nil {
			return nil, err
		}
		if kind, ok := seen[in.Item]; ok {
			if kind != in.Kind {
				return nil, ErrReadingValue
			}
			continue
		}
		seen[in.Item] = in.Kind
		canonical = append(canonical, in)
		k := readingDirectory{in.SourceRoot, key}
		f := folders[k]
		if f == nil {
			f = &folder{allSeries: true}
			folders[k] = f
		}
		f.count++
		f.allSeries = f.allSeries && evidence
	}
	out := make(map[int64]ReadingValues, len(canonical))
	for _, in := range canonical {
		key, _, _ := ReadingPathFolderEvidence(in)
		f := folders[readingDirectory{in.SourceRoot, key}]
		v, err := ReadingPathValue(in, f.count, f.allSeries)
		if err != nil {
			return nil, err
		}
		out[in.Item] = v
	}
	return out, nil
}

// ReadingAutomaticValues combines already accepted facts, without opening a
// publication or accepting raw archive metadata. Later file metadata wins over
// path rules; the owner overlay remains a separate, higher-priority step.
func ReadingAutomaticValues(pathValues, fileMetadata ReadingValues) (ReadingValues, error) {
	out := pathValues
	for _, fields := range []struct {
		input  *ReadingValue
		target **ReadingValue
	}{{fileMetadata.Author, &out.Author}, {fileMetadata.Series, &out.Series}} {
		if fields.input == nil {
			continue
		}
		v := fields.input
		if v.Source != ReadingFileMetadata || strings.TrimSpace(v.Value) == "" || len(v.Value) > 4096 || !utf8.ValidString(v.Value) || strings.ContainsAny(v.Value, "\x00\r\n") {
			return ReadingValues{}, ErrReadingValue
		}
		copy := *v
		*fields.target = &copy
	}
	if v := fileMetadata.PublicationType; v != nil {
		if v.Source != ReadingFileMetadata || !ValidReadingPublicationType(v.Value) {
			return ReadingValues{}, ErrReadingValue
		}
		copy := *v
		out.PublicationType = &copy
	}
	if v := fileMetadata.SeriesNumber; v != nil {
		if v.Source != ReadingFileMetadata || math.IsInf(v.Value, 0) || math.IsNaN(v.Value) || v.Value < 0 {
			return ReadingValues{}, ErrReadingValue
		}
		copy := *v
		out.SeriesNumber = &copy
	}
	return out, nil
}

// ReadingEffectiveValues never mutates the automatic facts or owner rows.
// Empty or unlocked owner fields restore the genuine producer value/absence.
func ReadingEffectiveValues(automatic ReadingValues, owner map[string]ReadingOwnerValue) (ReadingValues, error) {
	out := automatic
	for field, target := range map[string]**ReadingValue{"author": &out.Author, "series": &out.Series, "publicationType": &out.PublicationType} {
		value := owner[field]
		if !value.Locked || strings.TrimSpace(value.Value) == "" {
			continue
		}
		if len(value.Value) > 4096 || !utf8.ValidString(value.Value) || strings.ContainsAny(value.Value, "\x00\r\n") {
			return ReadingValues{}, ErrReadingValue
		}
		if field == "publicationType" && !ValidReadingPublicationType(value.Value) {
			return ReadingValues{}, ErrReadingValue
		}
		*target = &ReadingValue{value.Value, ReadingOwner}
	}
	if value := owner["seriesNumber"]; value.Locked && strings.TrimSpace(value.Value) != "" {
		raw := strings.TrimSpace(value.Value)
		n, err := strconv.ParseFloat(raw, 64)
		if !readingOwnerNumber.MatchString(raw) || err != nil || math.IsInf(n, 0) || math.IsNaN(n) || n < 0 {
			return ReadingValues{}, ErrReadingValue
		}
		out.SeriesNumber = &ReadingNumber{n, ReadingOwner}
	}
	return out, nil
}

func ReadingValueKey(value string) string { return localGenreKey(value) }

func ReadingSeriesID(library, name string) string {
	sum := sha256.Sum256([]byte("portico.reading.series.v1\x00" + library + "\x00" + ReadingValueKey(name)))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

type ReadingFacetItem struct {
	Item     int64
	TitleKey string // The catalogue's actual title sort key.
	Values   ReadingValues
}

type ReadingValueFacet struct {
	Value string
	Count int
}
type ReadingSeriesFacet struct {
	ID, Name string
	Count    int
}
type ReadingValueFacets struct {
	Authors []ReadingValueFacet
	Series  []ReadingSeriesFacet
}

// ReadingFacets consumes only the caller's already-authorized, library-scoped
// accepted rows. The SQL producer must filter visibility before counts/LIMIT.
func ReadingFacets(library string, items []ReadingFacetItem) ReadingValueFacets {
	type group struct {
		count          int
		labels, owners map[string]int
	}
	authors, series := map[string]*group{}, map[string]*group{}
	add := func(groups map[string]*group, value *ReadingValue) {
		if value == nil {
			return
		}
		key := ReadingValueKey(value.Value)
		if key == "" {
			return
		}
		g := groups[key]
		if g == nil {
			g = &group{labels: map[string]int{}, owners: map[string]int{}}
			groups[key] = g
		}
		g.count++
		g.labels[value.Value]++
		if value.Source == ReadingOwner {
			g.owners[value.Value]++
		}
	}
	for _, item := range items {
		add(authors, item.Values.Author)
		add(series, item.Values.Series)
	}
	label := func(g *group) string {
		counts := g.labels
		if len(g.owners) > 0 {
			counts = g.owners
		}
		best, number := "", -1
		for name, count := range counts {
			if count > number || count == number && name < best {
				best, number = name, count
			}
		}
		return best
	}
	out := ReadingValueFacets{}
	for _, g := range authors {
		out.Authors = append(out.Authors, ReadingValueFacet{label(g), g.count})
	}
	for _, g := range series {
		name := label(g)
		out.Series = append(out.Series, ReadingSeriesFacet{ReadingSeriesID(library, name), name, g.count})
	}
	sort.Slice(out.Authors, func(i, j int) bool {
		return ReadingValueKey(out.Authors[i].Value) < ReadingValueKey(out.Authors[j].Value)
	})
	sort.Slice(out.Series, func(i, j int) bool { return ReadingValueKey(out.Series[i].Name) < ReadingValueKey(out.Series[j].Name) })
	return out
}

func ReadingSeriesItems(library, id string, items []ReadingFacetItem) ([]ReadingFacetItem, bool) {
	var out []ReadingFacetItem
	for _, item := range items {
		if item.Values.Series != nil && ReadingSeriesID(library, item.Values.Series.Value) == id {
			out = append(out, item)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Values.SeriesNumber, out[j].Values.SeriesNumber
		if (a == nil) != (b == nil) {
			return a != nil
		}
		if a != nil && a.Value != b.Value {
			return a.Value < b.Value
		}
		if out[i].TitleKey != out[j].TitleKey {
			return out[i].TitleKey < out[j].TitleKey
		}
		return out[i].Item < out[j].Item
	})
	return out, len(out) != 0
}

// A publication type is descriptive metadata, never an ebook/comic or library identity.
func ValidReadingPublicationType(value string) bool {
	return value == "book" || value == "magazine" || value == "newspaper"
}

// Bump when recognized mappings change; older automatic facts become unavailable immediately.
const ReadingPublicationTypeRuleVersion = 1
