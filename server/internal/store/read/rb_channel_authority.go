package read

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// SharedChannelReader is a closed LC authority, not a general service reader.
// It cannot execute caller SQL, accept a caller config, or synthesize a viewer.
type SharedChannelReader struct {
	pool      *governor.Snapshots
	snapshots browseSnapshots
	a         *governor.Authority
	database  string
}
type rbChannelStamp struct {
	revision, createdMS                         int64
	channelRaw, generationRaw, canonical, query [32]byte
}
type rbChannelLibrary struct {
	id, data, order, metadata, ageInput, agePublished, pending, playback int64
	configuration, configurationMetadata                                 int64
	root                                                                 string
	incarnation                                                          [16]byte
}

func NewSharedChannelReader(pool *governor.Snapshots, a *governor.Authority, database string) (*SharedChannelReader, error) {
	if pool == nil || a == nil || !pool.OwnedBy(a) || !rbChannelName(database, 256) {
		return nil, ErrInput
	}
	return &SharedChannelReader{pool: pool, snapshots: pool, a: a, database: strings.Clone(database)}, nil
}
func rbChannelName(s string, max int) bool {
	return s != "" && len(s) <= max && utf8.ValidString(s) && !strings.ContainsAny(s, "\x00\r\n")
}
func (r *SharedChannelReader) custody(ctx context.Context, request governor.Request, out BrowseOutput, budget time.Duration) error {
	if r == nil || ctx == nil || budget <= 0 || request.Writer || request.Need[governor.Reader] != 1 || (request.Class != governor.Foreground && request.Class != governor.Background) || out.Authority != r.a || out.Grant == nil || out.Request.Writer || out.Request.Owner != request.Owner || out.Request.Class != request.Class {
		return ErrInput
	}
	goBytes := request.Need[governor.MemoryBytes] - r.pool.NativeRowReservation()
	if goBytes <= 0 || out.Request.Need[governor.MemoryBytes]/2 < goBytes {
		return governor.ErrSnapshotBudget
	}
	for resource, n := range out.Request.Need {
		if resource != int(governor.MemoryBytes) && n != 0 {
			return governor.ErrSnapshotBudget
		}
	}
	return r.a.CheckContext(ctx, out.Grant, out.Request)
}

// OpenSharedChannelScope proves the actual channel, generation, rule and named
// library in two independent bounded snapshots. There is no account/session
// lookup: all read authority comes from this exact enabled channel's config.
func (r *SharedChannelReader) OpenSharedChannelScope(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, in SharedChannelScopeInput) (*SharedChannelScope, SharedChannelScopeInfo, BrowseReadResult, error) {
	if r == nil {
		return nil, SharedChannelScopeInfo{}, BrowseReadResult{}, ErrInput
	}
	if request.Class != governor.Foreground {
		return r.openSharedChannelScope(ctx, request, budget, out, in)
	}
	type opened struct {
		scope *SharedChannelScope
		info  SharedChannelScopeInfo
	}
	value, result, err := liveRead(ctx, request, budget, out, r.pool, time.Now(), func(ctx context.Context, request governor.Request, budget time.Duration, _ time.Time) (opened, BrowseReadResult, error) {
		scope, info, result, err := r.openSharedChannelScope(ctx, request, budget, out, in)
		return opened{scope, info}, result, err
	})
	return value.scope, value.info, result, err
}
func (r *SharedChannelReader) openSharedChannelScope(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, in SharedChannelScopeInput) (scope *SharedChannelScope, info SharedChannelScopeInfo, result BrowseReadResult, err error) {
	started := time.Now()
	defer func() { result.Elapsed = time.Since(started) }()
	if !rbChannelName(in.Channel, 128) || !rbChannelName(in.Generation, 128) || !rbChannelName(in.Rule, 128) || !rbChannelName(in.Library, 128) || in.ExpectedRevision <= 0 {
		return nil, info, result, ErrInput
	}
	if err = r.custody(ctx, request, out, budget); err != nil {
		return nil, info, result, err
	}
	deadline := earliest(request.Deadline, out.Request.Deadline, time.Now().Add(min(budget, time.Second)))
	operation, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	var stamp rbChannelStamp
	var library rbChannelLibrary
	var firstInfo SharedChannelScopeInfo
	read := func(publication bool) (governor.SnapshotResult, error) {
		return r.snapshots.Read(operation, request, time.Until(deadline), func(s *governor.ReadScope) error {
			q := func(name string, args ...any) ([][]any, error) {
				if publication {
					result.PublicationStatements++
				} else {
					result.LookupStatements++
				}
				if err := r.a.CheckContext(operation, out.Grant, out.Request); err != nil {
					return nil, err
				}
				return s.Query(readStatementID(name), args...)
			}
			current, meta, e := r.channelSource(q, in, out.Request.Need[governor.MemoryBytes]/2, nil)
			if e != nil {
				return e
			}
			lib, e := rbReadChannelLibrary(q, in.Library)
			if e != nil {
				return e
			}
			meta.CatalogueLibrary = lib.id
			if publication {
				if current != stamp || lib != library || meta != firstInfo {
					return ErrStale
				}
			} else {
				stamp, library, firstInfo = current, lib, meta
			}
			return operation.Err()
		})
	}
	result.Lookup, err = read(false)
	result.Attempts = 1
	if !result.Lookup.Started.IsZero() {
		result.LookupWindows = append(result.LookupWindows, result.Lookup)
	}
	if result.Lookup.Refusal != nil {
		result.Refusal = result.Lookup.Refusal
		err = governor.ErrBusy
	}
	if err != nil {
		return nil, info, result, err
	}
	result.Publication, err = read(true)
	if !result.Publication.Started.IsZero() {
		result.PublicationWindows = append(result.PublicationWindows, result.Publication)
	}
	if result.Publication.Refusal != nil {
		result.Refusal = result.Publication.Refusal
		err = governor.ErrBusy
	}
	if err == nil {
		err = errors.Join(operation.Err(), r.a.CheckContext(operation, out.Grant, out.Request))
	}
	if err != nil {
		return nil, info, result, err
	}
	scope = &SharedChannelScope{issuer: r, input: in, stamp: stamp, library: library, info: firstInfo}
	return scope, firstInfo, result, nil
}

// RevalidateSharedChannelScope checks current stored facts again. This proves
// only this read boundary, never permission to publish later from stale facts;
// MG's accepting publication must also retain/recheck the returned source fence.
func (r *SharedChannelReader) RevalidateSharedChannelScope(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, scope *SharedChannelScope) (BrowseReadResult, error) {
	if scope == nil || scope.issuer != r {
		return BrowseReadResult{}, ErrInput
	}
	fresh, _, result, err := r.OpenSharedChannelScope(ctx, request, budget, out, scope.input)
	if err != nil {
		return result, err
	}
	if fresh.stamp != scope.stamp || fresh.library != scope.library || fresh.info != scope.info {
		return result, ErrStale
	}
	return result, nil
}

func (r *SharedChannelReader) channelSource(q browseQuery, in SharedChannelScopeInput, copyBytes int64, queryOut *rbChannelQuery) (rbChannelStamp, SharedChannelScopeInfo, error) {
	var stamp rbChannelStamp
	var info SharedChannelScopeInfo
	lengths, err := q("rb.channel.octets", in.Channel, in.Generation)
	if err != nil {
		return stamp, info, err
	}
	if len(lengths) != 1 {
		return stamp, info, ErrDenied
	}
	n, m := lengths[0][0].(int64), lengths[0][1].(int64)
	// Reserve actual UTF-8 config bytes plus a bounded decode/normalization and
	// fixed1024-library metadata envelope BEFORE reading or parsing either body.
	if n <= 0 || m <= 0 || n > 24<<10 || m > 24<<10 || 16*(n+m)+(256<<10) > copyBytes {
		return stamp, info, governor.ErrSnapshotBudget
	}
	rows, err := q("rb.channel.source", in.Channel, in.Generation)
	if err != nil {
		return stamp, info, err
	}
	if len(rows) != 1 {
		return stamp, info, ErrDenied
	}
	v := rows[0]
	if v[0] != in.ExpectedRevision || v[2] != int64(1) || v[3] != int64(0) || v[4] != in.Channel || v[5] != in.ExpectedRevision || v[7] != "pending" {
		return stamp, info, ErrStale
	}
	channelRaw, generationRaw := v[1].(string), v[6].(string)
	if int64(len(channelRaw)) != n || int64(len(generationRaw)) != m {
		return stamp, info, ErrStale
	}
	canonical, cfg, err := rbChannelConfig(channelRaw, in.Channel)
	if err != nil {
		return stamp, info, err
	}
	generationCanonical, _, err := rbChannelConfig(generationRaw, in.Channel)
	if err != nil || !bytes.Equal(canonical, generationCanonical) {
		return stamp, info, ErrStale
	}
	if !cfg.Enabled || v[8].(int64) <= 0 {
		return stamp, info, ErrDenied
	}
	var rule *rbChannelRule
	var libraries []string
	for i := range cfg.Rules {
		ruleCandidate := &cfg.Rules[i]
		libraries = append(libraries, ruleCandidate.Query.LibraryIDs...)
		if ruleCandidate.ID == in.Rule {
			rule = ruleCandidate
		}
	}
	if rule == nil || !slices.Contains(rule.Query.LibraryIDs, in.Library) {
		return stamp, info, ErrDenied
	}
	slices.Sort(libraries)
	libraries = slices.Compact(libraries)
	if len(libraries) > len(info.RequiredLibraries) {
		return stamp, info, ErrUnqualified
	}
	encoded, err := json.Marshal(rule.Query)
	if err != nil {
		return stamp, info, ErrInput
	}
	stamp = rbChannelStamp{revision: in.ExpectedRevision, createdMS: v[8].(int64), channelRaw: sha256.Sum256([]byte(channelRaw)), generationRaw: sha256.Sum256([]byte(generationRaw)), canonical: sha256.Sum256(canonical), query: sha256.Sum256(encoded)}
	info = SharedChannelScopeInfo{Database: r.database, Channel: strings.Clone(in.Channel), Generation: strings.Clone(in.Generation), Rule: strings.Clone(in.Rule), Library: strings.Clone(in.Library), ChannelRevision: in.ExpectedRevision, CreatedMS: stamp.createdMS, ConfigHash: stamp.canonical, QueryHash: stamp.query, RequiredLibraryCount: len(libraries)}
	for i, s := range libraries {
		info.RequiredLibraries[i] = strings.Clone(s)
	}
	if queryOut != nil {
		*queryOut = rule.Query
	}
	return stamp, info, nil
}
func rbReadChannelLibrary(q browseQuery, name string) (rbChannelLibrary, error) {
	var f rbChannelLibrary
	rows, err := q("rb.channel.library", name)
	if err != nil {
		return f, err
	}
	if len(rows) != 1 {
		return f, ErrDenied
	}
	v := rows[0]
	if v[1] != int64(0) || v[2] != v[3] || len(v[11].([]byte)) != 16 {
		return f, ErrDenied
	}
	f = rbChannelLibrary{id: v[0].(int64), root: v[2].(string), data: v[4].(int64), order: v[5].(int64), metadata: v[6].(int64), ageInput: v[7].(int64), agePublished: v[8].(int64), pending: v[9].(int64), playback: v[10].(int64)}
	copy(f.incarnation[:], v[11].([]byte))
	if v[12] != nil {
		f.configuration = v[12].(int64)
	}
	if v[13] != nil {
		f.configurationMetadata = v[13].(int64)
	}
	if f.pending != 0 || f.ageInput != f.agePublished {
		return f, ErrNotReady
	}
	return f, nil
}
