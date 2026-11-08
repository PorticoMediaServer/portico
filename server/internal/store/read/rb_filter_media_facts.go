package read

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/readingcap"
)

// Bounded source caps. A larger set is unqualified, never silently truncated.
const (
	rbMediaLinkCap        = 64
	rbMediaStreamCap      = 256
	rbMediaSubtitleCap    = 256
	rbMediaResourceCap    = 64
	rbMediaPublicationCap = 64
	rbMediaSourceCap      = 64

	rbMediaFilterFixedOverhead = 4096
	rbMediaFilterRowOverhead   = 256
	// The kernel never reserves more than this even when a caller offers more.
	// Larger results belong to a preparation recipe, not this point read.
	rbMediaFilterMaxBytes = 1 << 20
)

var errRBMediaUnresolved = errors.New("rb media filter: accepted facts unresolved")
var errRBMediaBudget = errors.New("rb media filter: variable byte budget exhausted")

// A current personal subtitle resource belongs to one viewing principal. This
// kernel binds no principal, so the item's subtitle language cannot be answered
// exactly for every viewer; it is a named pending source, never an exact empty.
var errRBMediaSubtitlePrincipalPending = fmt.Errorf("%w: personal subtitle resource requires the viewing principal", errRBMediaPending)

// rbMediaConverter is private trusted composition, never a request option. The
// phase-4 adapter must derive it from the existing configured converter; a
// filename or a client flag never enables a format.
type rbMediaConverter struct{ MOBI, CB7, CBR bool }

type rbMediaFilterInput struct {
	// Library is the native catalog library id; LibraryKey is the public library
	// id used by library_sources and inventory_objects.
	Library, Item int64
	LibraryKey    string
	// VariableBytes is the caller's remaining same-snapshot text allowance.
	VariableBytes int64
	Converter     rbMediaConverter
}

type rbMediaFilterProgress struct {
	Links, Assets, Streams, Subtitles, Resources, Publications, Sources int
	// HiddenLinks are links whose asset identity is absent; they stay in the
	// version set and count as unavailable instead of disappearing.
	HiddenLinks int
}

type rbMediaFilterResult struct {
	Facts    rbMediaFilterFacts
	Progress rbMediaFilterProgress
}

type rbMediaBytes struct{ used, limit int64 }

func (b *rbMediaBytes) add(n int64) error {
	if n < 0 || n > b.limit-b.used {
		return errRBMediaBudget
	}
	b.used += n
	return nil
}

// Stored text is retained plus folded/decoded once, so reserve twice its bytes.
func (b *rbMediaBytes) text(n int64) error { return b.add(2 * n) }

type rbMediaAssetFacts struct {
	token, path                 string
	available, size, modifiedNS int64
	height                      int64
	duration                    float64
	hasHeight, hasDuration      bool
}

type rbMediaStream struct {
	kind, language string
	dynamicRange   string
}

// readRBMediaFilterFacts reads accepted current asset, stream and Reading
// attribution facts for ONE admitted item and ONE admitted snapshot. It opens no
// source, follows no path and invokes no converter. q must be the closed
// registered query of that snapshot. The caller owns eligibility and freshness
// comparison; this kernel only refuses facts it cannot prove.
func readRBMediaFilterFacts(q browseQuery, in rbMediaFilterInput) (rbMediaFilterResult, error) {
	result, err := rbMediaFilterFactsRead(q, in)
	if err != nil {
		// No bytes and no partial facts survive a refusal.
		return rbMediaFilterResult{}, err
	}
	return result, nil
}

func rbMediaFilterFactsRead(q browseQuery, in rbMediaFilterInput) (rbMediaFilterResult, error) {
	var out rbMediaFilterResult
	if q == nil || in.Library <= 0 || in.Item <= 0 || strings.TrimSpace(in.LibraryKey) == "" || in.VariableBytes <= 0 {
		return out, ErrInput
	}
	budget := rbMediaBytes{limit: min(in.VariableBytes, int64(rbMediaFilterMaxBytes))}
	if err := budget.add(rbMediaFilterFixedOverhead); err != nil {
		return out, err
	}

	// Current entity point: kind, disc number and the accepted Reading
	// attribution tuple. A partial tuple is a refused fact, never a NULL guess.
	n, err := rbMediaPreflight(q, &budget, "rb.filter.media.attribution.bytes", 0, in.Item, in.Library)
	if err != nil {
		return out, err
	}
	rows, err := q("rb.filter.media.attribution", in.Item, in.Library)
	if err != nil {
		return out, err
	}
	if n != 1 || len(rows) != 1 {
		return out, errRBMediaUnresolved
	}
	v := rows[0]
	if len(v) != 10 {
		return out, errRBMediaUnresolved
	}
	kind, ok := v[0].(int64)
	if !ok {
		return out, errRBMediaUnresolved
	}
	disc := int64(1)
	if v[1] != nil {
		if disc, ok = v[1].(int64); !ok {
			return out, errRBMediaUnresolved
		}
	}
	author, err := rbMediaAttribution(v[3], v[4], v[5])
	if err != nil {
		return out, err
	}
	series, err := rbMediaAttribution(v[6], v[7], v[9])
	if err != nil {
		return out, err
	}
	if (v[6] == nil) != (v[8] == nil) {
		return out, errRBMediaUnresolved
	}
	out.Facts.Kind, out.Facts.DiscNumber = kind, disc
	if author != nil {
		out.Facts.Author = &author.value
		if err = rbMediaVerifyAttribution(q, &budget, "rb.filter.media.attribution.author", in, kind, *author); err != nil {
			return out, err
		}
	}
	if series != nil {
		out.Facts.Series = &series.value
		if err = rbMediaVerifyAttribution(q, &budget, "rb.filter.media.attribution.series", in, kind, *series); err != nil {
			return out, err
		}
	}

	// A bounded source set proves root/health fallback only; it is not a viewer
	// scope and the kernel derives no permission from it.
	n, err = rbMediaPreflight(q, &budget, "rb.filter.media.sources.bytes", 0, in.LibraryKey, int64(rbMediaSourceCap+1))
	if err != nil {
		return out, err
	}
	sources, err := q("rb.filter.media.sources", in.LibraryKey, int64(rbMediaSourceCap+1))
	if err != nil {
		return out, err
	}
	if n != len(sources) || len(sources) > rbMediaSourceCap {
		return out, ErrUnqualified
	}
	out.Progress.Sources = len(sources)
	sourceRows := make([]rbMediaSourceFact, 0, len(sources))
	for _, row := range sources {
		s, ok := rbMediaSourceRow(row)
		if !ok {
			return out, errRBMediaUnresolved
		}
		sourceRows = append(sourceRows, s)
	}
	gone, err := q("rb.filter.media.retired", in.Item)
	if err != nil {
		return out, err
	}
	retired := len(gone) > 0

	n, err = rbMediaPreflight(q, &budget, "rb.filter.media.links.bytes", 1, in.Library, in.Item, int64(rbMediaLinkCap+1))
	if err != nil {
		return out, err
	}
	links, err := q("rb.filter.media.links", in.Library, in.Item, int64(rbMediaLinkCap+1))
	if err != nil {
		return out, err
	}
	if n != len(links) || len(links) > rbMediaLinkCap {
		return out, ErrUnqualified
	}
	out.Progress.Links = len(links)

	reading := kind == 20 || kind == 21
	assets := make(map[int64]rbMediaAssetFacts, len(links))
	linkAvailable := make(map[int64]bool, len(links))
	// The published oracle is catalog_item_version_counts, whose triggers count
	// only part_index=0 links. A movie or audiobook split over parts 0,1,2 is
	// one version there; counting every link would invent extra versions. The
	// same trigger rule covers Reading leaves (a publication is a part-0 link).
	versions := int64(0)
	anyAvailable, anyUnavailable := false, false
	resolutions := []string{}
	durations := []float64{}
	dynamicRanges := []string{}
	audioLanguages := []string{}
	subtitleLanguages := []string{}
	for _, row := range links {
		if len(row) != 3 {
			return out, errRBMediaUnresolved
		}
		assetID, ok := row[0].(int64)
		if !ok {
			return out, errRBMediaUnresolved
		}
		partIndex, ok := row[1].(int64)
		if !ok {
			return out, errRBMediaUnresolved
		}
		if partIndex == 0 {
			versions++
		}
		token, hasToken := row[2].(string)
		if !hasToken {
			// The link is real but its asset identity is absent. Keep the version
			// and its unavailability; never hide it from the count or the state.
			out.Progress.HiddenLinks++
			anyUnavailable = true
			linkAvailable[assetID] = false
			continue
		}
		assetRows, err := q("rb.filter.media.asset", assetID)
		if err != nil {
			return out, err
		}
		if len(assetRows) != 1 {
			return out, errRBMediaUnresolved
		}
		asset, pending, ok := rbMediaAssetPoint(assetRows[0], in.Library)
		if !ok || asset.token != token {
			return out, errRBMediaUnresolved
		}
		// Mid-publication technical facts are not current. Refuse rather than
		// publish a stale height, duration or stream language.
		if pending {
			return out, errRBMediaUnresolved
		}
		out.Progress.Assets++
		assets[assetID] = asset
		available, err := rbMediaLinkAvailable(q, asset, in.LibraryKey, sourceRows, retired)
		if err != nil {
			return out, err
		}
		linkAvailable[assetID] = available
		if available {
			anyAvailable = true
		} else {
			anyUnavailable = true
		}
		if reading {
			continue
		}
		if asset.hasHeight {
			resolutions = rbMediaAddUnique(resolutions, rbMediaResolution(asset.height))
		}
		if asset.hasDuration {
			durations = append(durations, asset.duration)
		}
		streams, err := rbMediaReadStreams(q, &budget, asset)
		if err != nil {
			return out, err
		}
		out.Progress.Streams += len(streams)
		for _, s := range streams {
			if s.kind == "video" && s.dynamicRange != "" {
				dynamicRanges = rbMediaAddUnique(dynamicRanges, s.dynamicRange)
			}
			if s.kind == "audio" && s.language != "" {
				audioLanguages = rbMediaAddUnique(audioLanguages, s.language)
			}
		}
		subtitles, err := rbMediaReadSubtitles(q, &budget, asset)
		if err != nil {
			return out, err
		}
		out.Progress.Subtitles += len(subtitles)
		for _, language := range subtitles {
			if language != "" {
				subtitleLanguages = rbMediaAddUnique(subtitleLanguages, language)
			}
		}
	}
	if !reading {
		// Uploaded/shared subtitle files are a separate accepted subtitle source
		// in the legacy predicate. Reading leaves never carry them.
		resources, err := rbMediaReadSubtitleResources(q, &budget, in.Item)
		if err != nil {
			return out, err
		}
		out.Progress.Resources = len(resources)
		for _, language := range resources {
			if language != "" {
				subtitleLanguages = rbMediaAddUnique(subtitleLanguages, language)
			}
		}
	}
	if reading {
		pubs, err := rbMediaReadPublications(q, &budget, in.Item)
		if err != nil {
			return out, err
		}
		out.Progress.Publications = len(pubs)
		anyAvailable, anyUnavailable = false, false
		for _, pub := range pubs {
			available, ok := linkAvailable[pub.asset]
			if !ok {
				return out, errRBMediaUnresolved
			}
			local := false
			if pub.format == "cb7" || pub.format == "cbr" {
				facts, found := assets[pub.asset]
				if !found {
					return out, errRBMediaUnresolved
				}
				local, err = rbMediaAssetLocal(q, facts, in.LibraryKey)
				if err != nil {
					return out, err
				}
			}
			if rbMediaReadingFormat(kind, pub.format, local, in.Converter) && available {
				anyAvailable = true
			} else {
				anyUnavailable = true
			}
		}
	}
	// The published oracle is the part-0 link count for every kind, Reading
	// included: reading_publications is a foreign-key child of
	// catalog_asset_links, so each publication is one part-0 link.
	out.Facts.Versions = versions
	slices.Sort(resolutions)
	slices.Sort(dynamicRanges)
	slices.Sort(audioLanguages)
	slices.Sort(subtitleLanguages)
	out.Facts.Availability = rbMediaAvailabilityState(anyAvailable, anyUnavailable)
	out.Facts.Resolutions = resolutions
	out.Facts.DynamicRanges = dynamicRanges
	out.Facts.AudioLanguages = audioLanguages
	out.Facts.SubtitleLanguages = subtitleLanguages
	out.Facts.Durations = durations
	return out, nil
}

type rbMediaSourceFact struct {
	root, configuredRoot, health string
	enabled                      bool
}

func rbMediaSourceRow(row []any) (rbMediaSourceFact, bool) {
	if len(row) != 6 {
		return rbMediaSourceFact{}, false
	}
	text := func(i int) (string, bool) {
		s, ok := row[i].(string)
		return s, ok
	}
	var out rbMediaSourceFact
	var ok bool
	if _, ok = text(0); !ok {
		return out, false
	}
	if out.root, ok = text(1); !ok {
		return out, false
	}
	if out.configuredRoot, ok = text(2); !ok {
		return out, false
	}
	enabled, ok := row[3].(int64)
	if !ok {
		return out, false
	}
	out.enabled = enabled != 0
	if out.health, ok = text(4); !ok {
		return out, false
	}
	if _, ok = text(5); !ok {
		return out, false
	}
	return out, true
}

// rbMediaAssetPoint validates the technical point and reports a pending
// publication change separately from an invalid row.
func rbMediaAssetPoint(row []any, library int64) (rbMediaAssetFacts, bool, bool) {
	var out rbMediaAssetFacts
	if len(row) != 9 {
		return out, false, false
	}
	token, ok := row[0].(string)
	if !ok {
		return out, false, false
	}
	path, ok := row[1].(string)
	if !ok {
		return out, false, false
	}
	out.token, out.path = token, path
	numbers := make([]int64, 0, 5)
	for _, v := range []any{row[2], row[3], row[4], row[5], row[7], row[8]} {
		n, ok := v.(int64)
		if !ok {
			return out, false, false
		}
		numbers = append(numbers, n)
	}
	if numbers[5] != library {
		return out, false, false
	}
	out.available, out.size, out.modifiedNS, out.height = numbers[0], numbers[1], numbers[2], numbers[3]
	out.hasHeight = out.height > 0
	duration, err := rbMediaFloat(row[6])
	if err != nil {
		return out, false, false
	}
	switch {
	case duration == 0:
		out.hasDuration = false
	case duration < 0 || duration > 7*24*60*60 || math.IsNaN(duration) || math.IsInf(duration, 0):
		return out, false, false
	default:
		out.duration, out.hasDuration = duration, true
	}
	return out, numbers[4] != 0, true
}

func rbMediaFloat(v any) (float64, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case int64:
		return float64(x), nil
	}
	return 0, errRBMediaUnresolved
}

type rbMediaAttributionFact struct{ value, key string }

// rbMediaAttribution accepts the complete accepted tuple or its complete
// absence. It never invents a value from one surviving column.
func rbMediaAttribution(value, key, source any) (*rbMediaAttributionFact, error) {
	if value == nil && key == nil && source == nil {
		return nil, nil
	}
	v, vok := value.(string)
	k, kok := key.(string)
	s, sok := source.(string)
	if !vok || !kok || !sok || v == "" || k == "" {
		return nil, errRBMediaUnresolved
	}
	_ = s
	return &rbMediaAttributionFact{value: v, key: k}, nil
}

// rbMediaVerifyAttribution proves the accepted value named by its own key is
// still a member of the partial facet index for this library and kind. A raw
// tag or an inference from another column cannot satisfy this point.
func rbMediaVerifyAttribution(q browseQuery, b *rbMediaBytes, name string, in rbMediaFilterInput, kind int64, fact rbMediaAttributionFact) error {
	if err := b.text(int64(len(fact.value) + len(fact.key))); err != nil {
		return err
	}
	rows, err := q(name, in.Library, kind, fact.key, fact.value, in.Item)
	if err != nil {
		return err
	}
	if len(rows) != 1 {
		return errRBMediaUnresolved
	}
	return nil
}

func rbMediaPreflight(q browseQuery, b *rbMediaBytes, name string, textFrom int, args ...any) (int, error) {
	rows, err := q(name, args...)
	if err != nil {
		return 0, err
	}
	if err = b.add(int64(len(rows)) * rbMediaFilterRowOverhead); err != nil {
		return 0, err
	}
	for _, row := range rows {
		if textFrom < 0 || textFrom > len(row) {
			return 0, errRBMediaUnresolved
		}
		for _, v := range row[textFrom:] {
			if v == nil {
				continue
			}
			n, ok := v.(int64)
			if !ok || n < 0 {
				return 0, errRBMediaUnresolved
			}
			if err = b.text(n); err != nil {
				return 0, err
			}
		}
	}
	return len(rows), nil
}

func rbMediaAddUnique(values []string, value string) []string {
	if slices.Contains(values, value) {
		return values
	}
	return append(values, value)
}

func rbMediaAvailabilityState(anyAvailable, anyUnavailable bool) string {
	switch {
	case anyAvailable && !anyUnavailable:
		return rbMediaAvailable
	case anyAvailable:
		return rbMediaPartial
	}
	return rbMediaUnavailable
}

// rbMediaResolution mirrors the published class thresholds over the accepted
// asset height.
func rbMediaResolution(height int64) string {
	switch {
	case height >= 2000:
		return "4k"
	case height >= 1000:
		return "1080p"
	case height >= 700:
		return "720p"
	}
	return "sd"
}

// rbMediaLinkAvailable is the accepted inventory_item_availability meaning
// expressed as bounded point checks: a retired item is unavailable; when any
// inventory object exists the ready one decides; otherwise the current asset
// flag and matching source root/health decide. It is a fact, not a permission.
func rbMediaLinkAvailable(q browseQuery, asset rbMediaAssetFacts, libraryKey string, sources []rbMediaSourceFact, retired bool) (bool, error) {
	if retired {
		return false, nil
	}
	exists, err := q("rb.filter.media.inventory.exists", asset.token, libraryKey)
	if err != nil {
		return false, err
	}
	ready, err := q("rb.filter.media.inventory.ready", asset.token, libraryKey)
	if err != nil {
		return false, err
	}
	if len(exists) > 0 {
		return len(ready) > 0, nil
	}
	if asset.available == 0 {
		return false, nil
	}
	matches, goodMatch, anyGood := false, true, false
	for _, s := range sources {
		good := s.enabled && s.health != "root_changed" && s.health != "removing"
		anyGood = anyGood || good
		if rbMediaRootMatch(asset.path, s.root) || rbMediaRootMatch(asset.path, s.configuredRoot) {
			matches = true
			goodMatch = goodMatch && good
		}
	}
	if matches {
		return goodMatch, nil
	}
	return len(sources) == 0 || anyGood, nil
}

func rbMediaRootMatch(path, root string) bool {
	root = strings.TrimRight(root, "/\\")
	return strings.HasPrefix(path, root+"/") || strings.HasPrefix(path, root+"\\")
}

func rbMediaAssetLocal(q browseQuery, asset rbMediaAssetFacts, libraryKey string) (bool, error) {
	rows, err := q("rb.filter.media.asset.local", asset.token, libraryKey, asset.size, asset.modifiedNS)
	if err != nil {
		return false, err
	}
	return len(rows) == 1, nil
}

type rbMediaStreamDetail struct {
	DynamicRange string `json:"dynamicRange"`
}

func rbMediaReadStreams(q browseQuery, b *rbMediaBytes, asset rbMediaAssetFacts) ([]rbMediaStream, error) {
	n, err := rbMediaPreflight(q, b, "rb.filter.media.streams.bytes", 1, asset.token, int64(-1), int64(rbMediaStreamCap+1))
	if err != nil {
		return nil, err
	}
	rows, err := q("rb.filter.media.streams", asset.token, int64(-1), int64(rbMediaStreamCap+1))
	if err != nil {
		return nil, err
	}
	if n != len(rows) || len(rows) > rbMediaStreamCap {
		return nil, ErrUnqualified
	}
	out := make([]rbMediaStream, 0, len(rows))
	for _, row := range rows {
		if len(row) != 7 {
			return nil, errRBMediaUnresolved
		}
		kind, ok := row[1].(string)
		if !ok {
			return nil, errRBMediaUnresolved
		}
		language, ok := row[2].(string)
		if !ok {
			return nil, errRBMediaUnresolved
		}
		detail, ok := row[3].(string)
		if !ok {
			return nil, errRBMediaUnresolved
		}
		if err := rbMediaStreamIdentity(row[4], row[5], row[6], asset); err != nil {
			return nil, err
		}
		facts := rbMediaStream{kind: kind, language: language}
		if kind == "video" && detail != "" {
			var decoded rbMediaStreamDetail
			if json.Unmarshal([]byte(detail), &decoded) != nil {
				return nil, errRBMediaUnresolved
			}
			if decoded.DynamicRange != "" && !slices.Contains([]string{"sdr", "hdr10", "hlg", "dolby_vision"}, decoded.DynamicRange) {
				return nil, errRBMediaUnresolved
			}
			facts.dynamicRange = decoded.DynamicRange
		}
		out = append(out, facts)
	}
	return out, nil
}

func rbMediaStreamIdentity(revision, size, modified any, asset rbMediaAssetFacts) error {
	rev, ok := revision.(int64)
	if !ok || rev <= 0 {
		return errRBMediaUnresolved
	}
	sz, ok := size.(int64)
	if !ok || sz != asset.size {
		return errRBMediaUnresolved
	}
	mtime, ok := modified.(int64)
	if !ok || mtime != asset.modifiedNS {
		return errRBMediaUnresolved
	}
	return nil
}

func rbMediaReadSubtitles(q browseQuery, b *rbMediaBytes, asset rbMediaAssetFacts) ([]string, error) {
	n, err := rbMediaPreflight(q, b, "rb.filter.media.streams.subtitles.bytes", 1, asset.token, "", int64(rbMediaSubtitleCap+1))
	if err != nil {
		return nil, err
	}
	rows, err := q("rb.filter.media.streams.subtitles", asset.token, "", int64(rbMediaSubtitleCap+1))
	if err != nil {
		return nil, err
	}
	if n != len(rows) || len(rows) > rbMediaSubtitleCap {
		return nil, ErrUnqualified
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if len(row) != 6 {
			return nil, errRBMediaUnresolved
		}
		status, ok := row[5].(string)
		if !ok {
			return nil, errRBMediaUnresolved
		}
		// Only a known subtitle inventory is an accepted track list. A pending
		// or partial scan is absence here, exactly as the legacy status='known'
		// join drops it, and its stale identity is not an item-wide failure.
		if status != "known" {
			continue
		}
		language, ok := row[1].(string)
		if !ok {
			return nil, errRBMediaUnresolved
		}
		if err := rbMediaStreamIdentity(row[2], row[3], row[4], asset); err != nil {
			return nil, err
		}
		out = append(out, language)
	}
	return out, nil
}

// rbMediaReadSubtitleResources reads the current viewer-visible subtitle files
// the legacy subtitleLanguage predicate also matches. Shared resources are
// exact; a current personal resource is a named pending because this kernel
// binds no viewing principal.
func rbMediaReadSubtitleResources(q browseQuery, b *rbMediaBytes, item int64) ([]string, error) {
	n, err := rbMediaPreflight(q, b, "rb.filter.media.streams.subtitles.resource.bytes", 0, item, int64(rbMediaResourceCap+1))
	if err != nil {
		return nil, err
	}
	rows, err := q("rb.filter.media.streams.subtitles.resource", item, int64(rbMediaResourceCap+1))
	if err != nil {
		return nil, err
	}
	if n != len(rows) || len(rows) > rbMediaResourceCap {
		return nil, ErrUnqualified
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if len(row) != 2 {
			return nil, errRBMediaUnresolved
		}
		scope, ok := row[0].(string)
		if !ok {
			return nil, errRBMediaUnresolved
		}
		language, ok := row[1].(string)
		if !ok {
			return nil, errRBMediaUnresolved
		}
		switch scope {
		case "shared":
			out = append(out, language)
		case "personal":
			return nil, errRBMediaSubtitlePrincipalPending
		default:
			return nil, errRBMediaUnresolved
		}
	}
	return out, nil
}

type rbMediaPublication struct {
	asset  int64
	format string
}

func rbMediaReadPublications(q browseQuery, b *rbMediaBytes, item int64) ([]rbMediaPublication, error) {
	n, err := rbMediaPreflight(q, b, "rb.filter.media.publication.bytes", 1, item, int64(0), int64(rbMediaPublicationCap+1))
	if err != nil {
		return nil, err
	}
	rows, err := q("rb.filter.media.publication", item, int64(0), int64(rbMediaPublicationCap+1))
	if err != nil {
		return nil, err
	}
	if n != len(rows) || len(rows) > rbMediaPublicationCap {
		return nil, ErrUnqualified
	}
	out := make([]rbMediaPublication, 0, len(rows))
	for _, row := range rows {
		if len(row) != 2 {
			return nil, errRBMediaUnresolved
		}
		asset, ok := row[0].(int64)
		if !ok {
			return nil, errRBMediaUnresolved
		}
		format, ok := row[1].(string)
		if !ok {
			return nil, errRBMediaUnresolved
		}
		out = append(out, rbMediaPublication{asset: asset, format: format})
	}
	return out, nil
}

// rbMediaReadingFormat mirrors rsReadingFormat: native formats never need a
// converter option, and the archive formats require both the compiled
// capability and the private trusted configuration. local is the accepted local
// inventory fact for the same asset.
func rbMediaReadingFormat(kind int64, format string, local bool, converter rbMediaConverter) bool {
	if kind == 20 {
		return format == "epub" || format == "pdf" || converter.MOBI && (format == "mobi" || format == "azw3")
	}
	if kind != 21 {
		return false
	}
	return format == "cbz" || format == "pdf" || local && (format == "cb7" && converter.CB7 && readingcap.CB7() || format == "cbr" && converter.CBR && readingcap.CBR())
}
