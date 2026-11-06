package writer

import (
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

const n1ProfileMethod = "n1-profile-observations-v1" // never pool with Wave1/2 methods

type n1MetricMax struct {
	Value       *int64 `json:"value"`
	ObservedMax *int64 `json:"observed_subset_max"`
	KnownUses   int    `json:"known_uses"`
	MissingUses int    `json:"missing_uses"`
	Reason      string `json:"unavailable_reason,omitempty"`
}

type n1IDFacts struct {
	ID           string                 `json:"id"`
	Registered   bool                   `json:"registered_statement"`
	UseCount     int                    `json:"actual_request_use_count"`
	ReturnedUses int                    `json:"returned_uses"`
	ErrorUses    int                    `json:"error_uses"`
	Metrics      map[string]n1MetricMax `json:"maxima"`
}

type n1Interval struct {
	StartedNS  *int64 `json:"started_ns"`
	ReturnedNS *int64 `json:"returned_ns"`
	ElapsedNS  *int64 `json:"elapsed_ns"`
	Scope      string `json:"scope"`
	Reason     string `json:"unavailable_reason,omitempty"`
}

func n1IntervalBetween(start, end *int64, scope string) n1Interval {
	out := n1Interval{Scope: scope}
	if start == nil || end == nil || *end < *start {
		out.Reason = "unobserved or inconsistent boundary; no timing reconstruction"
		return out
	}
	a, b := *start, *end
	n := b - a
	out.StartedNS, out.ReturnedNS, out.ElapsedNS = &a, &b, &n
	return out
}
func n1MissingInterval(reason string) n1Interval { return n1Interval{Reason: reason} }

type n1ProfileInput struct {
	DeclaredCase         n1Case         `json:"declared_case"`
	TextBytes            map[string]int `json:"actual_text_bytes"`
	TextRunes            map[string]int `json:"actual_text_codepoints"`
	Escaping             map[string]int `json:"actual_escape_counts"`
	RootBytes            int            `json:"actual_root_bytes"`
	FileCount            int            `json:"actual_file_count"`
	CanonicalLimitBytes  int            `json:"canonical_request_limit_bytes"`
	TitleLimitCodepoints int            `json:"title_limit_codepoints"`
	LegalMIN             *bool          `json:"complete_legal_min"`
	LegalMAX             *bool          `json:"complete_legal_max"`
	MissingReason        string         `json:"unavailable_reason"`
}

func n1InputProfile(command *inventoryAcceptCommand, cell n1Case) *n1ProfileInput {
	if command == nil {
		return nil
	}
	out := &n1ProfileInput{DeclaredCase: cell, TextBytes: map[string]int{}, TextRunes: map[string]int{}, Escaping: map[string]int{}, RootBytes: len(command.request.source.resolvedRoot), FileCount: len(command.request.files), CanonicalLimitBytes: maxCanonicalRequestBytes, TitleLimitCodepoints: MaxTitleCharacters, MissingReason: "owner confirms no complete legal-MIN/MAX fixture; declared fixture/escaped input is a partial matrix, not a MAX proof"}
	for i, text := range inventoryRequestTexts(&command.request) {
		key := stringIndex(i)
		out.TextBytes[key], out.TextRunes[key] = len(*text), utf8.RuneCountInString(*text)
		out.Escaping[key] = 0
		for _, r := range *text {
			if r == '"' || r == '\\' || r < ' ' || r == '<' || r == '>' || r == '&' || r == '\u2028' || r == '\u2029' {
				out.Escaping[key]++
			}
		}
	}
	return out
}
func stringIndex(i int) string {
	const digits = "0123456789"
	return "text_" + string([]byte{digits[i/10], digits[i%10]})
}

type n1ProfileFacts struct {
	ActualLibraryVector    *[6]int64         `json:"native_library_data_order_metadata_ageinput_agepublished_agepending"`
	ActualAgeCurrent       *bool             `json:"actual_age_current"`
	AgeBucketRows          *int64            `json:"actual_age_bucket_point_rows"`
	BranchReadbackReason   string            `json:"branch_readback_scope"`
	Reference              *ReceiptReference `json:"original_reference"`
	RegistrationReason     string            `json:"registry_scope"`
	FullPrefixReason       string            `json:"full_prefix_unavailable_reason"`
	Method                 string            `json:"method"`
	OriginalID             string            `json:"original_id"`
	TurnID                 string            `json:"physical_call_id"`
	Cohort                 string            `json:"cohort"`
	ConnectionID           *uint64           `json:"physical_connection_id"`
	SaltBefore, SaltAfter  *[8]byte
	SameObservedGeneration *bool                 `json:"same_observed_wal_salt"`
	GenerationReason       string                `json:"generation_scope"`
	PerID                  []n1IDFacts           `json:"per_id"`
	NativeOperations       []n1IDFacts           `json:"native_operations"`
	Boundaries             map[string]n1Interval `json:"owner_boundaries"`
	GVIOMapping            []governor.DeviceIO   `json:"actual_gv_request_device_io"`
	IOMappingReason        string                `json:"io_mapping_scope"`
	ActualIOBytes          n1Counter             `json:"actual_all_io_bytes"`
	LivePeaks              map[string]n1Counter  `json:"incremental_live_peaks_bytes"`
	ForecastBytes          *uint64               `json:"allocation_free_forecast_bytes"`
	FullPrefix             *bool                 `json:"complete_full_prefix"`
	Input                  *n1ProfileInput       `json:"input_facts"`
	ProfileReady           bool                  `json:"qualified_profile_ready"`
	Missing                []string              `json:"profile_gate_missing"`
}

func n1Max(units []*n1Unit, get func(*n1Unit) (*int64, string)) n1MetricMax {
	out := n1MetricMax{}
	for _, u := range units {
		value, reason := get(u)
		if value == nil {
			if reason == "" {
				reason = "request/observer did not return this metric"
			}
			out.MissingUses++
			if out.Reason == "" {
				out.Reason = reason
			}
			continue
		}
		out.KnownUses++
		if out.ObservedMax == nil || *value > *out.ObservedMax {
			n := *value
			out.ObservedMax = &n
		}
	}
	if len(units) == 0 {
		out.Reason = "registered but not used in this physical call; no cost maximum observed"
	} else if out.MissingUses == 0 {
		out.Value = out.ObservedMax
		out.Reason = ""
	}
	return out
}

func n1WALDelta(u *n1Unit, field string) (*int64, string) {
	if !u.exclusive || u.Before == nil || u.After == nil || u.Before.ConnectionID != u.After.ConnectionID || u.Before.WALIO == nil || u.After.WALIO == nil {
		return nil, "overlap or missing same-connection native WAL counter interval"
	}
	a, b := u.Before.WALIO, u.After.WALIO
	if !a.Qualified || !b.Qualified {
		return nil, "native WAL hook reports unqualified; raw counters remain in unit before/after"
	}
	var before, after uint64
	switch field {
	case "wal_attempted_bytes":
		before, after = a.AttemptedBytes, b.AttemptedBytes
	case "wal_written_bytes":
		before, after = a.WrittenBytes, b.WrittenBytes
	case "wal_header_writes":
		before, after = a.HeaderWrites, b.HeaderWrites
	case "wal_errors":
		before, after = a.Errors, b.Errors
	}
	if after < before || after-before > math.MaxInt64 {
		return nil, "native counter regressed or exceeded scalar representation"
	}
	n := int64(after - before)
	return &n, ""
}

func n1IDProfile(id string, registered bool, units []*n1Unit) n1IDFacts {
	out := n1IDFacts{ID: id, Registered: registered, UseCount: len(units), Metrics: map[string]n1MetricMax{}}
	for _, u := range units {
		if u.ReturnedNS != nil {
			out.ReturnedUses++
		}
		if u.Error != "" {
			out.ErrorUses++
		}
	}
	getters := map[string]func(*n1Unit) (*int64, string){
		"elapsed_ns": func(u *n1Unit) (*int64, string) { return u.ElapsedNS, "request did not return" },
		"native_request_span_ns": func(u *n1Unit) (*int64, string) {
			return u.NativeElapsedNS, "native request/row lifetime did not return"
		},
		"rows_returned": func(u *n1Unit) (*int64, string) {
			return u.RowsReturned, "this native call exposes no returned-row count"
		},
		"rows_changed": func(u *n1Unit) (*int64, string) {
			return u.RowsChanged, "RETURNING/read rows do not expose a separate changed-row count"
		},
		"rows_examined": func(u *n1Unit) (*int64, string) {
			return nil, "no examined/rejected-row native hook; returned/changed/fullscan-step are not substitutes"
		},
		"vm_operations": func(u *n1Unit) (*int64, string) { return u.VMOps.Value, u.VMOps.Reason },
		"native_pages": func(u *n1Unit) (*int64, string) {
			return nil, "native page visits/writes unavailable; cache events and WAL frames are not substitutes"
		},
		"all_io_bytes": func(u *n1Unit) (*int64, string) {
			return nil, "only WAL write counters available, not complete native/database/device IO"
		},
		"wal_append_bytes": func(u *n1Unit) (*int64, string) {
			return nil, "native WrittenBytes includes rewrites; no exact append-only byte hook"
		},
		"wal_header_bytes": func(u *n1Unit) (*int64, string) {
			return nil, "HeaderWrites is a count, not actual header bytes; no 32-byte multiplication/subtraction"
		},
	}
	for name, get := range getters {
		out.Metrics[name] = n1Max(units, get)
	}
	for _, name := range []string{"wal_attempted_bytes", "wal_written_bytes", "wal_header_writes", "wal_errors"} {
		out.Metrics[name] = n1Max(units, func(u *n1Unit) (*int64, string) { return n1WALDelta(u, name) })
	}
	return out
}

func n1BuildProfile(record *n1Attempt, trace *n1Trace) *n1ProfileFacts {
	out := &n1ProfileFacts{Reference: record.Reference, FullPrefixReason: "synthetic fixture/warmup has no owner-approved complete full-prefix selection; actual WAL frames/bytes remain raw, never assumed full", Method: n1ProfileMethod, OriginalID: record.OriginalID, TurnID: record.ID, Cohort: record.Cohort, ConnectionID: record.Settings.ConnectionID, GenerationReason: "before/after salts bracket this call only; equality is not atomicity/reset proof or a per-statement generation hook", Input: record.InputProfile, ForecastBytes: record.RequiredBytes, Boundaries: map[string]n1Interval{}, LivePeaks: map[string]n1Counter{}, Missing: []string{"complete legal-MIN/MAX envelope and full branch matrix", "complete full-prefix fixture", "named complete owner Setup/Terminal/header-sync hooks", "examined rows/native pages/complete IO", "incremental LIVE peak and final-release ownership"}}
	if record.WALBefore != nil && record.WALBefore.State != nil {
		salt := record.WALBefore.State.Salt
		out.SaltBefore = &salt
	}
	if record.WALAfter != nil && record.WALAfter.State != nil {
		salt := record.WALAfter.State.Salt
		out.SaltAfter = &salt
	}
	if out.SaltBefore != nil && out.SaltAfter != nil {
		same := *out.SaltBefore == *out.SaltAfter
		out.SameObservedGeneration = &same
	}
	if trace.libraryObserved {
		vector := trace.libraryVector
		out.ActualLibraryVector = &vector
		current := vector[3] == vector[4] && vector[5] == 0
		out.ActualAgeCurrent = &current
	}
	out.BranchReadbackReason = "library vector is the actual registered native read before N1 writes, when observed; declared case/setup snapshots remain separate"
	byID := map[string][]*n1Unit{}
	native := map[string][]*n1Unit{}
	var firstDomain, lastDomain *n1Unit
	var assertStart, assertEnd *int64
	for i := range record.Units {
		u := &record.Units[i]
		if u.ID == scanFreshAge && u.RowsClosed && u.Error == "" && u.RowsReturned != nil {
			n := *u.RowsReturned
			out.AgeBucketRows = &n
		}
		if u.ID != "" {
			byID[string(u.ID)] = append(byID[string(u.ID)], u)
			if firstDomain == nil {
				firstDomain = u
			}
			lastDomain = u
		} else {
			native[u.UnitID] = append(native[u.UnitID], u)
			if u.UnitID == "native.SQLiteDQS" {
				assertStart = u.NativeStartedNS
			}
			if u.UnitID == "PRAGMA temp_store" {
				assertEnd = u.NativeReturnedNS
			}
			if u.UnitID == "BEGIN IMMEDIATE" || u.UnitID == "COMMIT" || u.UnitID == "ROLLBACK" || strings.HasPrefix(u.UnitID, "PRAGMA synchronous=") {
				out.Boundaries[u.UnitID] = n1IntervalBetween(u.NativeStartedNS, u.NativeReturnedNS, "actual native request/row-close bracket; observer overhead separately in units")
			}
		}
	}
	ids := make([]string, 0, len(trace.uses))
	for id := range trace.uses {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	if record.RegistrySHA256 == "" {
		out.RegistrationReason = "actual captured registry not returned; no registered-ID census fabricated"
	} else {
		out.RegistrationReason = "actual captured registry for this physical call"
		for _, id := range ids {
			out.PerID = append(out.PerID, n1IDProfile(id, true, byID[id]))
		}
	}
	ids = ids[:0]
	for id := range native {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		out.NativeOperations = append(out.NativeOperations, n1IDProfile(id, false, native[id]))
	}
	for _, name := range []string{"BEGIN IMMEDIATE", "COMMIT", "ROLLBACK", "PRAGMA synchronous=NORMAL", "PRAGMA synchronous=FULL"} {
		if _, ok := out.Boundaries[name]; !ok {
			out.Boundaries[name] = n1MissingInterval("native phase not observed in this call; owner commit_not_attempted remains independent")
		}
	}
	out.Boundaries["Setup"] = n1MissingInterval("W confirms no complete named owner Setup hook; observer bracket is separate")
	out.Boundaries["Terminal"] = n1MissingInterval("W confirms no complete named owner Terminal hook; observer post-domain bracket is separate")
	out.Boundaries["acquired"] = n1MissingInterval("private sql.Conn checkout boundaries are not exposed by native Connect/policy calls")
	out.Boundaries["assert"] = n1MissingInterval("full Assert entry/return not exposed; native DQS-through-last-policy-read bracket is separate")
	out.Boundaries["assert_native_bracket"] = n1IntervalBetween(assertStart, assertEnd, "actual DQS native call through last assertion query; excludes final Go comparisons, not complete Assert")
	if firstDomain != nil {
		out.Boundaries["setup_observed_bracket"] = n1IntervalBetween(record.Clock.HoldStartedNS, firstDomain.StartedNS, "owner HOLD start to first registered request; not a named complete Setup proof")
	}
	if lastDomain != nil {
		out.Boundaries["post_domain_return_bracket"] = n1IntervalBetween(lastDomain.ReturnedNS, record.Clock.PhysicalReturnedNS, "last registered request return through actual physical/ledger retirement; not isolated Terminal")
	}
	if record.Outcome != "committed" {
		out.Boundaries["error_through_physical_return"] = n1IntervalBetween(record.Clock.HoldStartedNS, record.Clock.PhysicalReturnedNS, "actual failed owner HOLD; no missing owner view is reconstructed")
	}
	out.Boundaries["COMMIT_owner"] = n1IntervalBetween(record.Clock.CommitAttemptedNS, record.Clock.CommitReturnedNS, "actual frame COMMIT interval")
	out.Boundaries["physical_return_and_retirement"] = n1IntervalBetween(record.Clock.HoldStartedNS, record.Clock.PhysicalReturnedNS, "actual complete owner HOLD through physical return/retirement")
	for _, name := range []string{"grant_retirement", "discard", "header_sync", "xsync", "wal_reset"} {
		out.Boundaries[name] = n1MissingInterval("no isolated owner/native interval hook; close, header count, salt change or allocation is not this proof")
	}
	if record.BoundRequest != nil && len(record.BoundRequest.IO) > 0 {
		out.GVIOMapping = append([]governor.DeviceIO(nil), record.BoundRequest.IO...)
		out.IOMappingReason = "actual verified request reservation mapping; bytes are not measured service"
	} else {
		out.IOMappingReason = "synthetic owner fixture request carries no device ID/class/outstanding mapping; OS device is not GV identity"
	}
	out.ActualIOBytes.Reason = "complete per-command native/device IO counter unavailable"
	for _, category := range []string{"go", "native", "bind", "row", "temp", "result", "preparation", "consumer_retained", "through_final_release"} {
		out.LivePeaks[category] = n1Counter{Reason: "complete incremental live ownership/peak hook unavailable; heap allocation totals, forecast and DBStatus raw high are not LIVE peaks"}
	}
	return out
}
