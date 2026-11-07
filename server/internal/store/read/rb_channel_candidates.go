package read

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

const rbChannelRaw = 4

type rbChannelMeta struct {
	ID, Revision, Year, Kind, Show, Season                                int64
	Public, MetadataIncarnation, PlaybackIncarnation                      [16]byte
	PlaybackRevision, SourceRevision                                      int64
	Title, TitleOrder, TitleKeep, Added, Numbering, Release, ReleaseOrder string
	SeasonNumber, EpisodeNumber                                           int64
	HasSeasonNumber, HasEpisodeNumber                                     bool
	TargetTitle                                                           string
	TargetYear, TargetKind                                                int64
	ShowPublic                                                            [16]byte
	Eligible                                                              bool
}

func rbChannelScopeHash(scope *SharedChannelScope) [32]byte {
	h := sha256.New()
	text := func(s string) {
		var n [8]byte
		binary.LittleEndian.PutUint64(n[:], uint64(len(s)))
		_, _ = h.Write(n[:])
		_, _ = h.Write([]byte(s))
	}
	integer := func(n int64) { var b [8]byte; binary.LittleEndian.PutUint64(b[:], uint64(n)); _, _ = h.Write(b[:]) }
	for _, s := range []string{scope.issuer.database, scope.input.Channel, scope.input.Generation, scope.input.Rule, scope.input.Library, scope.library.root} {
		text(s)
	}
	for _, n := range []int64{scope.stamp.revision, scope.stamp.createdMS, scope.library.id, scope.library.data, scope.library.order, scope.library.metadata, scope.library.ageInput, scope.library.agePublished, scope.library.pending, scope.library.playback, scope.library.configuration, scope.library.configurationMetadata} {
		integer(n)
	}
	for _, b := range [][32]byte{scope.stamp.channelRaw, scope.stamp.generationRaw, scope.stamp.canonical, scope.stamp.query} {
		_, _ = h.Write(b[:])
	}
	_, _ = h.Write(scope.library.incarnation[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
func rbChannelFence(scope *SharedChannelScope, kind int64) SharedChannelFence {
	i, l := scope.info, scope.library
	return SharedChannelFence{Database: i.Database, Channel: i.Channel, Generation: i.Generation, Rule: i.Rule, Library: i.Library, ChannelRevision: i.ChannelRevision, CatalogueLibrary: l.id, DataRevision: l.data, OrderRevision: l.order, MetadataRevision: l.metadata, AdmissionRevision: l.agePublished, ConfigHash: i.ConfigHash, Query: i.QueryHash, ScopeHash: rbChannelScopeHash(scope), Kind: kind, source: scope.stamp, library: l}
}
func rbChannelSamePublicFence(a, b SharedChannelFence) bool {
	a.source = rbChannelStamp{}
	a.library = rbChannelLibrary{}
	b.source = rbChannelStamp{}
	b.library = rbChannelLibrary{}
	return a == b
}

// All source work is bounded; callbacks below are fixed reader-owned steps,
// never caller callbacks. Every physical quantum rechecks channel+library.
func (r *SharedChannelReader) channelQuantum(ctx context.Context, request governor.Request, deadline time.Time, out BrowseOutput, scope *SharedChannelScope, result *BrowseReadResult, publication bool, step func(browseQuery, rbChannelQuery) error) error {
	physical, err := r.snapshots.Read(ctx, request, time.Until(deadline), func(s *governor.ReadScope) error {
		q := func(name string, args ...any) ([][]any, error) {
			if publication {
				result.PublicationStatements++
			} else {
				result.LookupStatements++
			}
			if err := r.a.CheckContext(ctx, out.Grant, out.Request); err != nil {
				return nil, err
			}
			return s.Query(readStatementID(name), args...)
		}
		var query rbChannelQuery
		stamp, info, err := r.channelSource(q, scope.input, out.Request.Need[governor.MemoryBytes]/2, &query)
		if err != nil {
			return err
		}
		lib, err := rbReadChannelLibrary(q, scope.input.Library)
		if err != nil {
			return err
		}
		info.CatalogueLibrary = lib.id
		if stamp != scope.stamp || lib != scope.library || info != scope.info {
			return ErrStale
		}
		return errors.Join(step(q, query), ctx.Err())
	})
	if publication {
		result.Publication = physical
		if !physical.Started.IsZero() {
			result.PublicationWindows = append(result.PublicationWindows, physical)
		}
	} else {
		result.Lookup = physical
		if !physical.Started.IsZero() {
			result.LookupWindows = append(result.LookupWindows, physical)
		}
	}
	if physical.Refusal != nil {
		result.Refusal = physical.Refusal
		return governor.ErrBusy
	}
	return err
}

func rbChannelMetaValue(row []any, kind int64) (rbChannelMeta, error) {
	var m rbChannelMeta
	if len(row) != 29 {
		return m, ErrUnqualified
	}
	m.ID, m.Revision, m.Year, m.Kind = row[0].(int64), row[3].(int64), row[5].(int64), kind
	if row[4] != int64(0) {
		return m, nil
	}
	blob := func(v any, d *[16]byte) bool {
		b, ok := v.([]byte)
		if !ok || len(b) != 16 {
			return false
		}
		copy(d[:], b)
		return true
	}
	if !blob(row[1], &m.Public) || !blob(row[19], &m.MetadataIncarnation) {
		return m, ErrUnqualified
	}
	// An unbound origin is consumed negative raw work, not a whole playable
	// item. The complete metadata row is still fenced at final publication,
	// including a nil origin becoming bound during this operation.
	if row[16] == nil || row[17] == nil {
		return m, nil
	}
	if !blob(row[16], &m.PlaybackIncarnation) {
		return m, ErrUnqualified
	}
	text := func(v any) string {
		if v == nil {
			return ""
		}
		return v.(string)
	}
	m.Title, m.TitleOrder, m.TitleKeep, m.Added = text(row[2]), text(row[14]), text(row[15]), text(row[13])
	m.Numbering, m.Release, m.ReleaseOrder = text(row[10]), text(row[11]), text(row[12])
	m.PlaybackRevision, m.SourceRevision = row[17].(int64), row[18].(int64)
	m.TargetTitle, m.TargetYear, m.TargetKind = m.Title, m.Year, kind
	if row[9] != nil {
		m.EpisodeNumber = row[9].(int64)
		m.HasEpisodeNumber = true
	}
	if kind == 4 {
		if row[6] == nil || row[20] == nil || row[22] != int64(0) || row[24] != int64(2) {
			return m, nil
		}
		m.Show = row[6].(int64)
		if !blob(row[20], &m.ShowPublic) {
			return m, ErrUnqualified
		}
		m.TargetTitle, m.TargetYear, m.TargetKind = text(row[21]), row[23].(int64), 2
		// Absolute-numbered anime and unassigned episodes legitimately have no
		// season. When a season IS assigned, retain the same current parent proof.
		if row[7] != nil {
			if row[25] == nil || row[27] != int64(0) || row[28] != row[6] {
				return m, nil
			}
			m.Season = row[7].(int64)
			if row[26] != nil {
				m.SeasonNumber = row[26].(int64)
				m.HasSeasonNumber = true
			}
		}
	}
	for _, s := range []string{m.Title, m.TitleOrder, m.TitleKeep, m.Added, m.Numbering, m.Release, m.ReleaseOrder, m.TargetTitle} {
		if !utf8.ValidString(s) {
			return m, ErrUnqualified
		}
	}
	m.Eligible = true
	return m, nil
}

func rbChannelRuleMatches(query rbChannelQuery, program rbScalarFilter, m rbChannelMeta, library string, createdMS int64) bool {
	leaf, show := entityid.Encode(m.Public[:]), entityid.Encode(m.ShowPublic[:])
	if slices.Contains(query.ExcludeItemIDs, leaf) || m.Show != 0 && slices.Contains(query.ExcludeItemIDs, show) {
		return false
	}
	pinned := slices.Contains(query.IncludeItemIDs, leaf) || m.Show != 0 && slices.Contains(query.IncludeItemIDs, show)
	kind := "movie"
	if m.Kind == 4 {
		kind = "episode"
	}
	base := slices.Contains(query.Kinds, kind)
	criteria := len(query.Filter) > 0 || strings.TrimSpace(query.Text) != "" || len(query.ShowIDs) > 0 || query.RecentDays > 0
	if !criteria && len(query.IncludeItemIDs) > 0 {
		base = false
	}
	if len(query.ShowIDs) > 0 && !slices.Contains(query.ShowIDs, show) {
		base = false
	}
	if !program.matches(rbFilterFacts{title: m.TargetTitle, year: m.TargetYear, kind: m.TargetKind, library: library}) {
		base = false
	}
	if query.RecentDays > 0 {
		added, err := time.Parse(time.RFC3339Nano, m.Added)
		if err != nil || added.Before(time.UnixMilli(createdMS).UTC().AddDate(0, 0, -query.RecentDays)) {
			base = false
		}
	}
	return base || pinned
}

func rbChannelCompileQuery(query rbChannelQuery, kind int64) (rbScalarFilter, error) {
	if kind != 1 && kind != 4 {
		return rbScalarFilter{}, ErrUnqualified
	}
	name := "movie"
	if kind == 4 {
		name = "episode"
	}
	if !slices.Contains(query.Kinds, name) && len(query.IncludeItemIDs) == 0 {
		return rbScalarFilter{}, ErrDenied
	}
	if query.Text != "" || query.Order == "rating" {
		return rbScalarFilter{}, ErrUnqualified
	}
	// Search/membership/rating source reducers remain separate families. Their
	// absence must refuse before raw traversal, including under any NOT node.
	return compileRBScalarFilter(query.Filter)
}

// Actual retained bytes, plus JSON/copy/header overlap, are funded BEFORE
// constructing retained batches or source digests. Native rows have separate
// prospective driver reservations in each physical read window.
func rbChannelRetainedBytes(v any) int64 {
	var walk func(reflect.Value) int64
	walk = func(v reflect.Value) int64 {
		if !v.IsValid() {
			return 16
		}
		if v.Kind() == reflect.Interface {
			if v.IsNil() {
				return 16
			}
			return 16 + walk(v.Elem())
		}
		if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
			n := int64(64)
			for i := 0; i < v.Len(); i++ {
				n += walk(v.Index(i))
			}
			return n
		}
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return 16
			}
			return 16 + walk(v.Elem())
		}
		if v.Kind() == reflect.Struct {
			n := int64(64)
			for i := 0; i < v.NumField(); i++ {
				n += walk(v.Field(i))
			}
			return n
		}
		if v.Kind() == reflect.String {
			if !utf8.ValidString(v.String()) {
				return 1 << 40
			}
			return 32 + 16*int64(v.Len())
		}
		return int64(v.Type().Size()) + 16
	}
	return walk(reflect.ValueOf(v))
}

func rbChannelPlayable(meta rbChannelMeta, proof homeWholePlayableBinding, file []any, scope *SharedChannelScope) (SharedChannelPlayable, error) {
	var out SharedChannelPlayable
	if len(file) != 9 || file[0] != proof.Token || file[3] != proof.AssetSize || file[4] != proof.AssetModifiedNS || file[5] != proof.AssetRevision || file[6] != proof.PreparedSourceRevision || file[7] != int64(1) || file[8] != int64(0) {
		return out, ErrStale
	}
	path, container := file[1].(string), file[2].(string)
	if !utf8.ValidString(path) || !utf8.ValidString(container) {
		return out, ErrUnqualified
	}
	l := scope.library
	if proof.Item != meta.ID || proof.Kind != meta.Kind || proof.ItemPublic != meta.Public || proof.ItemMetadataIncarnation != meta.MetadataIncarnation || proof.ItemRevision != meta.Revision || proof.ItemIncarnation != meta.PlaybackIncarnation || proof.ItemPlaybackRevision != meta.PlaybackRevision || proof.ItemSourceRevision != meta.SourceRevision || proof.Root.LibraryIncarnation != l.incarnation || proof.Root.LibraryOriginRevision != l.playback || proof.Root.DataRevision != l.data || proof.Root.MetadataRevision != l.metadata || proof.Root.AgeRevision != l.agePublished {
		return out, ErrStale
	}
	out = SharedChannelPlayable{Item: BrowseItem{ID: meta.ID, PublicID: meta.Public, Title: meta.Title, Revision: meta.Revision}, Kind: meta.Kind, Library: l.id, ItemIncarnation: proof.ItemIncarnation, ItemSourceRevision: proof.ItemSourceRevision, ItemPlaybackRevision: proof.ItemPlaybackRevision, Asset: proof.Asset, AssetIncarnation: proof.AssetIncarnation, LinkIncarnation: proof.LinkIncarnation, AssetRevision: proof.AssetRevision, LinkRevision: proof.LinkRevision, PreparedSourceRevision: proof.PreparedSourceRevision, Token: proof.Token, Path: path, Container: container, Size: proof.AssetSize, ModifiedNS: proof.AssetModifiedNS, DurationSeconds: proof.DurationSeconds, Show: meta.Show, Season: meta.Season, Numbering: meta.Numbering, AiredAt: meta.Release, ReleaseDate: meta.Release, ReleaseOrderKey: meta.ReleaseOrder, TitleOrder: meta.TitleOrder, TitleOrderKeepArticles: meta.TitleKeep, AddedAt: meta.Added, Year: meta.Year, Object: proof.Object, ObjectRevision: proof.ObjectRevision, Source: proof.Source, SourceIncarnation: proof.SourceIncarnation, SourceGeneration: proof.SourceGeneration, SourceRoot: proof.SourceRoot, SourceConfiguredRoot: proof.SourceConfiguredRoot}
	if meta.HasSeasonNumber {
		value := meta.SeasonNumber
		out.SeasonNumber = &value
	}
	if meta.HasEpisodeNumber {
		value := meta.EpisodeNumber
		out.EpisodeNumber = &value
	}
	// Fixed exported value types only: no user marshalers or runtime extensions.
	encoded, err := json.Marshal(struct {
		Scope [32]byte
		Meta  rbChannelMeta
		Proof homeWholePlayableBinding
		Value SharedChannelPlayable
	}{rbChannelScopeHash(scope), meta, proof, out})
	if err != nil {
		return SharedChannelPlayable{}, ErrUnqualified
	}
	out.BindingHash = sha256.Sum256(encoded)
	return out, nil
}
