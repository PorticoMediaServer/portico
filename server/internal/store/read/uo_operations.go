package read

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// UOOperationsFamily selects one closed operational view. Personal families read
// only the current viewing token's own profile; administrator families require
// a current owner/admin principal at both publication fences.
type UOOperationsFamily uint8

const (
	UOOperationsConflicts UOOperationsFamily = iota + 1
	UOOperationsDevices
	UOOperationsJobs
	UOOperationsJobDue
	UOOperationsSettings
	UOOperationsDeliveryLog
)

func (f UOOperationsFamily) valid() bool {
	return f >= UOOperationsConflicts && f <= UOOperationsDeliveryLog
}
func (f UOOperationsFamily) admin() bool {
	return f == UOOperationsSettings || f == UOOperationsDeliveryLog
}
func (f UOOperationsFamily) name() string {
	switch f {
	case UOOperationsConflicts:
		return "conflicts"
	case UOOperationsDevices:
		return "devices"
	case UOOperationsJobs:
		return "jobs"
	case UOOperationsJobDue:
		return "jobs-due"
	case UOOperationsSettings:
		return "settings"
	case UOOperationsDeliveryLog:
		return "delivery-log"
	}
	return ""
}

// Private compiled principal recipes. They grant no writer, no mutation ID and
// no runtime route; they prove the current principal obligation in both the
// lookup and the fresh publication snapshot.
const (
	uoPersonalOperationsPolicy readPolicyID = 32700
	uoAdminOperationsPolicy    readPolicyID = 32701
)

type uoOperationsPurpose struct{ marker uint8 }

func (*uoOperationsPurpose) readPolicyState() {}

var (
	uoPersonalOperationsPurpose = &uoOperationsPurpose{marker: 1}
	uoAdminOperationsPurpose    = &uoOperationsPurpose{marker: 2}
)

func init() {
	kinds := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	registerReadPolicy(readPolicyRecipe{id: uoPersonalOperationsPolicy, domain: "uo-operations-personal", kinds: kinds, stateful: true, scope: readPrincipalScope, validate: validateUOPersonalOperations})
	registerReadPolicy(readPolicyRecipe{id: uoAdminOperationsPolicy, domain: "uo-operations-admin", kinds: kinds, stateful: true, scope: readPrincipalScope, validate: validateUOAdminOperations})
}

func validateUOPersonalOperations(_ browseQuery, in BrowseInput, f browseFence) (string, error) {
	if in.Library != 0 || in.policy.subject != 0 || f.library != "" || f.libraryID != 0 || f.profile == "" || f.lifetime == "" {
		return "", ErrInput
	}
	return fmt.Sprintf("uo-operations-personal-v1:profile=%s:lifetime=%s:authority=%d", f.profile, f.lifetime, f.authority), nil
}

func validateUOAdminOperations(_ browseQuery, in BrowseInput, f browseFence) (string, error) {
	if in.Library != 0 || in.policy.subject != 0 || f.library != "" || f.libraryID != 0 || f.profile == "" || f.lifetime == "" {
		return "", ErrInput
	}
	if f.role != "owner" && f.role != "admin" {
		return "", ErrDenied
	}
	return fmt.Sprintf("uo-operations-admin-v1:role=%s:profile=%s:lifetime=%s:authority=%d", f.role, f.profile, f.lifetime, f.authority), nil
}

// UOOperationsInput is one bounded operational read. JobID selects the jobs PK
// point; Webhook selects one delivery log. IncludeOutcomes asks for UO-08's
// per-item outcome rows and is refused by name until that writer lands.
type UOOperationsInput struct {
	View            BrowseInput
	Family          UOOperationsFamily
	After           *UOOperationsCursor
	JobID           string
	Webhook         string
	IncludeOutcomes bool
}

// UOOperationsCursor is a values-only keyset position. Its private fence and
// family stop another viewer or family continuing this stream.
type UOOperationsCursor struct {
	fence     browseFence
	family    UOOperationsFamily
	key       string
	createdMS int64
	id        string
}

type UOConflict struct {
	ID           string
	Item         int64
	Field        string
	BaseRevision int64
	BaseValue    string
	Resolved     bool
}

type UODevice struct {
	ID       string
	Sequence int64
}

// UOJob contains fixed scalar status only. Selector, arguments, request hash and
// destination payloads are never selected by this administrative view.
type UOJob struct {
	ID, State, ErrorCode                                        string
	Total, Done, Failed, Cursor, Revision, CreatedMS, UpdatedMS int64
	DueMS, Owner, Generation, ExpiresMS, Attempts, NextMS       int64
	Due                                                         bool
	Runnable                                                    bool
}

type UOSettingPending struct {
	Field    string
	Revision int64
}

// UOSettings is the current ordinary console singleton plus bounded pending
// field keys. Security settings and secrets are not selected.
type UOSettings struct {
	Revision, UpdatedMS, PendingRevision int64
	Pending                              []UOSettingPending
	Exhausted                            bool
}

// UODelivery is the bounded delivery log row. URL, secret, subject and event
// data are deliberately absent.
type UODelivery struct {
	ID, Type, State, Error                                      string
	Attempts, CreatedMS, FinishedMS, NextMS, Status, DurationMS int64
	Revision                                                    int64
}

type UOOperationsPage struct {
	Conflicts  []UOConflict
	Devices    []UODevice
	Jobs       []UOJob
	Settings   *UOSettings
	Deliveries []UODelivery
	// More reports another due candidate; it never means the caller may pick one.
	More bool
	// WebhookRevision/WebhookAuthority are the current gate facts for a log page.
	WebhookRevision, WebhookAuthority int64
	Next                              *UOOperationsCursor
	Exhausted                         bool
	RawRows, RawPositions             int
	Statements                        int
}

// UOOperationsWindow is harness-only and read-only. It performs no INSERT,
// UPDATE, DELETE, job claim or webhook delivery.
func (r *ForegroundReader) UOOperationsWindow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in UOOperationsInput) (UOOperationsPage, BrowseReadResult, error) {
	if r == nil {
		return UOOperationsPage{}, BrowseReadResult{}, ErrInput
	}
	if in.IncludeOutcomes {
		return UOOperationsPage{}, BrowseReadResult{}, &UOProducerPending{Task: "UO-08", Source: "personal job outcomes"}
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (UOOperationsPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.operationsWindow(ctx, request, budget, output, in)
	})
}

func (r *ForegroundReader) operationsWindow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in UOOperationsInput) (page UOOperationsPage, result BrowseReadResult, err error) {
	if r == nil || !in.Family.valid() || in.View.After != nil || in.View.Descending || in.View.KeepArticles ||
		in.View.Library != 0 || in.View.Limit < 1 || in.View.Limit > MaxPage {
		return UOOperationsPage{}, BrowseReadResult{}, ErrInput
	}
	if in.IncludeOutcomes {
		return UOOperationsPage{}, BrowseReadResult{}, &UOProducerPending{Task: "UO-08", Source: "personal job outcomes"}
	}
	switch in.Family {
	case UOOperationsConflicts, UOOperationsDevices, UOOperationsSettings, UOOperationsDeliveryLog:
		if request.Budget.RawRows < MaxRaw {
			return UOOperationsPage{}, BrowseReadResult{}, ErrInput
		}
	case UOOperationsJobs:
		if !uoOperationsKey(in.JobID) || request.Budget.RawRows < 1 {
			return UOOperationsPage{}, BrowseReadResult{}, ErrInput
		}
	case UOOperationsJobDue:
		if request.Budget.RawRows < 1 {
			return UOOperationsPage{}, BrowseReadResult{}, ErrInput
		}
	}
	if in.Family == UOOperationsDeliveryLog && !uoOperationsKey(in.Webhook) {
		return UOOperationsPage{}, BrowseReadResult{}, ErrInput
	}
	view := in.View
	view.Library, view.Kind = 0, 1
	if in.Family.admin() {
		view.policy = readPolicyBinding{recipe: uoAdminOperationsPolicy, state: uoAdminOperationsPurpose}
	} else {
		view.policy = readPolicyBinding{recipe: uoPersonalOperationsPolicy, state: uoPersonalOperationsPurpose}
	}
	steps, root := uoOperationsRead(in, output)
	// Device/conflict keysets carry no publication root: their exact octet
	// preflight and payload share one snapshot behind the shared fence, whose
	// current principal is re-read and compared at publication.
	if root == nil {
		page, result, err = publishRead(ctx, r.snapshots, request, budget, output, view, steps)
	} else {
		page, result, err = publishRead(ctx, r.snapshots, request, budget, output, view, steps, root)
	}
	if err == nil {
		page.Statements = result.LookupStatements + result.PublicationStatements
	}
	return page, result, err
}

func uoOperationsKey(v string) bool {
	return v != "" && len(v) <= 128 && utf8.ValidString(v)
}

func uoOperationsReserve(output BrowseOutput, sizes [][]any) error {
	var total int64
	for _, row := range sizes {
		for _, v := range row {
			if n, ok := v.(int64); ok {
				if n < 0 {
					return ErrUnqualified
				}
				total += n
			}
		}
	}
	limit := output.Request.Need[governor.MemoryBytes] / 2
	if limit <= 0 || 8192+int64(len(sizes))*1024+2*total > limit {
		return governor.ErrSnapshotBudget
	}
	return nil
}

func uoOperationsNext(in UOOperationsInput, f browseFence, key string) *UOOperationsCursor {
	if key == "" {
		return nil
	}
	return &UOOperationsCursor{fence: f, family: in.Family, key: key}
}

// uoOperationsRead compiles the closed root and step for one family. The root is
// re-read at publication, so any generation, gate or exact-row drift refuses the
// earlier draft instead of releasing it.
func uoOperationsRead(in UOOperationsInput, output BrowseOutput) ([]readStep[UOOperationsPage], rootRead) {
	base := "read.uo.operations." + in.Family.name()
	after := ""
	if in.After != nil {
		after = in.After.key
	}
	switch in.Family {
	case UOOperationsConflicts, UOOperationsDevices:
		var rows, sizes [][]any
		step := func(ctx context.Context, q browseQuery, f browseFence, draft *UOOperationsPage) error {
			if in.After != nil && (in.After.fence != f || in.After.family != in.Family) {
				return ErrStale
			}
			rows, sizes = nil, nil
			// The shared fence resolved the viewing token's own profile in this
			// same snapshot; the forced PK device keyset binds that exact profile
			// and never a caller-supplied id. Conflicts stay bound by the token.
			viewer := in.View.TokenHash
			if in.Family == UOOperationsDevices {
				viewer = f.profile
			}
			var e error
			sizes, e = q(base+".sizes", viewer, after, int64(MaxRaw+1))
			if e != nil {
				return e
			}
			if e := uoOperationsReserve(output, sizes); e != nil {
				return e
			}
			rows, e = q(base, viewer, after, int64(len(sizes)))
			if e != nil {
				return e
			}
			if len(rows) != len(sizes) {
				return ErrStale
			}
			draft.RawRows = len(rows)
			for i, row := range rows {
				if e := ctx.Err(); e != nil {
					return e
				}
				if e := uoOperationsCheckSizes(in.Family, row, sizes[i]); e != nil {
					return e
				}
				draft.RawPositions++
				switch in.Family {
				case UOOperationsConflicts:
					value, e := uoConflictValue(row)
					if e != nil {
						return e
					}
					draft.Next = uoOperationsNext(in, f, value.ID)
					draft.Conflicts = append(draft.Conflicts, value)
					if len(draft.Conflicts) == in.View.Limit {
						return uoOperationsFinish(draft, rows)
					}
				case UOOperationsDevices:
					value, e := uoDeviceValue(row)
					if e != nil {
						return e
					}
					draft.Next = uoOperationsNext(in, f, value.ID)
					draft.Devices = append(draft.Devices, value)
					if len(draft.Devices) == in.View.Limit {
						return uoOperationsFinish(draft, rows)
					}
				}
			}
			return uoOperationsFinish(draft, rows)
		}
		return []readStep[UOOperationsPage]{step}, nil
	case UOOperationsSettings:
		var rows, sizes [][]any
		var head [][]any
		root := func(q browseQuery) ([]any, error) {
			head = nil
			var e error
			head, e = q(base+".head", in.View.TokenHash)
			if e != nil {
				return nil, e
			}
			if len(head) != 1 || len(head[0]) != 3 {
				return nil, ErrDenied
			}
			return append([]any(nil), head[0]...), nil
		}
		step := func(ctx context.Context, q browseQuery, f browseFence, draft *UOOperationsPage) error {
			if in.After != nil && (in.After.fence != f || in.After.family != in.Family) {
				return ErrStale
			}
			rows, sizes = nil, nil
			var e error
			sizes, e = q(base+".pending.sizes", after, int64(MaxRaw+1))
			if e != nil {
				return e
			}
			if e := uoOperationsReserve(output, sizes); e != nil {
				return e
			}
			rows, e = q(base+".pending", after, int64(len(sizes)))
			if e != nil {
				return e
			}
			if len(rows) != len(sizes) {
				return ErrStale
			}
			settings, e := uoSettingsValue(head[0])
			if e != nil {
				return e
			}
			draft.RawRows = len(rows)
			for i, row := range rows {
				if e := ctx.Err(); e != nil {
					return e
				}
				if e := uoOperationsCheckSizes(in.Family, row, sizes[i]); e != nil {
					return e
				}
				draft.RawPositions++
				value, e := uoSettingPendingValue(row)
				if e != nil {
					return e
				}
				draft.Next = uoOperationsNext(in, f, value.Field)
				settings.Pending = append(settings.Pending, value)
				if len(settings.Pending) == in.View.Limit {
					break
				}
			}
			settings.Exhausted = draft.RawPositions == len(rows) && len(rows) < MaxRaw+1
			if settings.Exhausted {
				draft.Next = nil
			}
			draft.Settings = &settings
			return nil
		}
		return []readStep[UOOperationsPage]{step}, root
	case UOOperationsJobs:
		var rows, sizes [][]any
		root := func(q browseQuery) ([]any, error) {
			rows, sizes = nil, nil
			var e error
			sizes, e = q(base+".sizes", in.JobID, in.View.TokenHash)
			if e != nil {
				return nil, e
			}
			if e := uoOperationsReserve(output, sizes); e != nil {
				return nil, e
			}
			rows, e = q(base, in.JobID, in.View.TokenHash)
			if e != nil {
				return nil, e
			}
			if len(rows) != len(sizes) {
				return nil, ErrStale
			}
			var source []any
			for i, row := range rows {
				if e := uoOperationsCheckSizes(in.Family, row, sizes[i]); e != nil {
					return nil, e
				}
				source = append(source, row...)
			}
			return source, nil
		}
		step := func(ctx context.Context, _ browseQuery, _ browseFence, draft *UOOperationsPage) error {
			if e := ctx.Err(); e != nil {
				return e
			}
			draft.RawRows = len(rows)
			if len(rows) != 1 {
				return ErrDenied
			}
			value, e := uoJobValue(rows[0], in.View.Now.UnixMilli())
			if e != nil {
				return e
			}
			draft.RawPositions = 1
			draft.Exhausted = true
			draft.Jobs = append(draft.Jobs, value)
			return nil
		}
		return []readStep[UOOperationsPage]{step}, root
	case UOOperationsJobDue:
		var rows, sizes [][]any
		nowMS := in.View.Now.UnixMilli()
		root := func(q browseQuery) ([]any, error) {
			rows, sizes = nil, nil
			if nowMS <= 0 {
				return nil, ErrInput
			}
			var e error
			sizes, e = q(base+".sizes", in.View.TokenHash, nowMS, int64(2))
			if e != nil {
				return nil, e
			}
			if e := uoOperationsReserve(output, sizes); e != nil {
				return nil, e
			}
			rows, e = q(base, in.View.TokenHash, nowMS, int64(len(sizes)))
			if e != nil {
				return nil, e
			}
			if len(rows) != len(sizes) {
				return nil, ErrStale
			}
			var source []any
			for i, row := range rows {
				if e := uoOperationsCheckSizes(in.Family, row, sizes[i]); e != nil {
					return nil, e
				}
				source = append(source, row...)
			}
			return source, nil
		}
		step := func(ctx context.Context, _ browseQuery, _ browseFence, draft *UOOperationsPage) error {
			if e := ctx.Err(); e != nil {
				return e
			}
			draft.RawRows = len(rows)
			if len(rows) == 0 {
				draft.Exhausted = true
				return nil
			}
			draft.More = len(rows) > 1
			draft.RawPositions = 1
			value, e := uoJobValue(rows[0], nowMS)
			if e != nil {
				return e
			}
			draft.Jobs = append(draft.Jobs, value)
			return nil
		}
		return []readStep[UOOperationsPage]{step}, root
	case UOOperationsDeliveryLog:
		var rows, sizes [][]any
		var gate [][]any
		root := func(q browseQuery) ([]any, error) {
			rows, sizes, gate = nil, nil, nil
			var e error
			gate, e = q(base+".gate", in.Webhook, in.View.TokenHash)
			if e != nil {
				return nil, e
			}
			if len(gate) != 1 || len(gate[0]) != 2 {
				return nil, ErrDenied
			}
			var sameSizes, sameRows, olderRows [][]any
			if in.After == nil {
				sizes, e = q(base+".first.sizes", in.Webhook, in.View.TokenHash, int64(MaxRaw+1))
				if e != nil {
					return nil, e
				}
			} else {
				sizes, e = q(base+".same.sizes", in.Webhook, in.View.TokenHash, in.After.createdMS, in.After.id, int64(MaxRaw+1))
				if e != nil {
					return nil, e
				}
				if len(sizes) < MaxRaw+1 {
					sameSizes = sizes
					sizes = nil
					sizes, e = q(base+".older.sizes", in.Webhook, in.View.TokenHash, in.After.createdMS, int64(MaxRaw+1-len(sameSizes)))
					if e != nil {
						return nil, e
					}
					sizes = append(append([][]any(nil), sameSizes...), sizes...)
				}
			}
			if e := uoOperationsReserve(output, sizes); e != nil {
				return nil, e
			}
			if in.After == nil {
				rows, e = q(base+".first", in.Webhook, in.View.TokenHash, int64(len(sizes)))
			} else {
				n := len(sizes)
				if len(sameSizes) > 0 {
					n = len(sameSizes)
				}
				sameRows, e = q(base+".same", in.Webhook, in.View.TokenHash, in.After.createdMS, in.After.id, int64(n))
				if e == nil && len(sizes) > len(sameRows) {
					olderRows, e = q(base+".older", in.Webhook, in.View.TokenHash, in.After.createdMS, int64(len(sizes)-len(sameRows)))
					if e == nil {
						sameRows = append(sameRows, olderRows...)
					}
				}
				rows = sameRows
			}
			if e != nil {
				return nil, e
			}
			if len(rows) != len(sizes) {
				return nil, ErrStale
			}
			source := append([]any(nil), gate[0]...)
			for i, row := range rows {
				if e := uoOperationsCheckSizes(in.Family, row, sizes[i]); e != nil {
					return nil, e
				}
				source = append(source, row...)
			}
			return source, nil
		}
		step := func(ctx context.Context, _ browseQuery, f browseFence, draft *UOOperationsPage) error {
			if in.After != nil && (in.After.fence != f || in.After.family != in.Family) {
				return ErrStale
			}
			revision, ok := gate[0][0].(int64)
			if !ok || revision <= 0 {
				return ErrUnqualified
			}
			authority, ok := gate[0][1].(int64)
			if !ok || authority <= 0 {
				return ErrUnqualified
			}
			draft.WebhookRevision, draft.WebhookAuthority = revision, authority
			draft.RawRows = len(rows)
			for _, row := range rows {
				if e := ctx.Err(); e != nil {
					return e
				}
				value, e := uoDeliveryValue(row)
				if e != nil {
					return e
				}
				if in.After != nil && (value.CreatedMS > in.After.createdMS || value.CreatedMS == in.After.createdMS && value.ID >= in.After.id) {
					return ErrStale
				}
				draft.RawPositions++
				draft.Next = &UOOperationsCursor{fence: f, family: in.Family, createdMS: value.CreatedMS, id: value.ID}
				draft.Deliveries = append(draft.Deliveries, value)
				if len(draft.Deliveries) == in.View.Limit {
					break
				}
			}
			draft.Exhausted = draft.RawPositions == len(rows) && len(rows) < MaxRaw+1
			if draft.Exhausted {
				draft.Next = nil
			}
			return nil
		}
		return []readStep[UOOperationsPage]{step}, root
	}
	return nil, nil
}

func uoOperationsFinish(draft *UOOperationsPage, rows [][]any) error {
	draft.Exhausted = draft.RawPositions == len(rows) && len(rows) < MaxRaw+1
	if draft.Exhausted {
		draft.Next = nil
	}
	return nil
}

func uoOperationsCheckSizes(family UOOperationsFamily, row, size []any) error {
	if len(row) != len(size) {
		return ErrUnqualified
	}
	switch family {
	case UOOperationsConflicts:
		if len(size) != 6 {
			return ErrUnqualified
		}
		id, ok := row[0].(string)
		if !ok || int64(len(id)) != size[0].(int64) || len(id) == 0 || len(id) > 128 {
			return ErrStale
		}
		field, ok := row[1].(string)
		if !ok || int64(len(field)) != size[1].(int64) || len(field) == 0 || len(field) > 128 {
			return ErrStale
		}
		base, ok := row[2].(string)
		if !ok || int64(len(base)) != size[2].(int64) || len(base) > 4096 {
			return ErrUnqualified
		}
		for i := 3; i < 6; i++ {
			if row[i] != size[i] {
				return ErrStale
			}
		}
	case UOOperationsDevices:
		if len(size) != 2 {
			return ErrUnqualified
		}
		id, ok := row[0].(string)
		if !ok || int64(len(id)) != size[0].(int64) || len(id) == 0 || len(id) > 128 {
			return ErrStale
		}
		if row[1] != size[1] {
			return ErrStale
		}
	case UOOperationsSettings:
		if len(size) != 2 {
			return ErrUnqualified
		}
		field, ok := row[0].(string)
		if !ok || int64(len(field)) != size[0].(int64) || len(field) == 0 || len(field) > 128 {
			return ErrStale
		}
		if row[1] != size[1] {
			return ErrStale
		}
	case UOOperationsJobs, UOOperationsJobDue:
		if len(size) != 16 {
			return ErrUnqualified
		}
		id, ok := row[0].(string)
		if !ok || int64(len(id)) != size[0].(int64) || len(id) == 0 || len(id) > 128 {
			return ErrStale
		}
		state, ok := row[1].(string)
		if !ok || int64(len(state)) != size[1].(int64) || len(state) == 0 || len(state) > 32 {
			return ErrStale
		}
		code, ok := row[2].(string)
		if !ok || int64(len(code)) != size[2].(int64) || len(code) > 128 {
			return ErrUnqualified
		}
		for i := 3; i < 16; i++ {
			if row[i] != size[i] && (row[i] != nil || size[i] != nil) {
				return ErrStale
			}
		}
	case UOOperationsDeliveryLog:
		if len(size) != 11 {
			return ErrUnqualified
		}
		id, ok := row[0].(string)
		if !ok || int64(len(id)) != size[0].(int64) || len(id) == 0 || len(id) > 128 {
			return ErrStale
		}
		kind, ok := row[1].(string)
		if !ok || int64(len(kind)) != size[1].(int64) || len(kind) == 0 || len(kind) > 128 {
			return ErrStale
		}
		state, ok := row[2].(string)
		if !ok || int64(len(state)) != size[2].(int64) || len(state) == 0 || len(state) > 16 {
			return ErrStale
		}
		text, ok := row[3].(string)
		if !ok || int64(len(text)) != size[3].(int64) || len(text) > 512 {
			return ErrUnqualified
		}
		for i := 4; i < 11; i++ {
			if row[i] != size[i] {
				return ErrStale
			}
		}
	default:
		return ErrUnqualified
	}
	return nil
}

func uoConflictValue(row []any) (UOConflict, error) {
	if len(row) != 6 {
		return UOConflict{}, ErrUnqualified
	}
	id, ok := row[0].(string)
	if !ok || len(id) == 0 || len(id) > 128 || !utf8.ValidString(id) {
		return UOConflict{}, ErrUnqualified
	}
	field, ok := row[1].(string)
	if !ok || len(field) == 0 || len(field) > 128 || !utf8.ValidString(field) {
		return UOConflict{}, ErrUnqualified
	}
	base, ok := row[2].(string)
	if !ok || len(base) > 4096 || !utf8.ValidString(base) {
		return UOConflict{}, ErrUnqualified
	}
	item, ok := row[3].(int64)
	if !ok || item <= 0 {
		return UOConflict{}, ErrUnqualified
	}
	revision, ok := row[4].(int64)
	if !ok || revision < 0 {
		return UOConflict{}, ErrUnqualified
	}
	resolved, ok := row[5].(int64)
	if !ok || resolved != 0 && resolved != 1 {
		return UOConflict{}, ErrUnqualified
	}
	return UOConflict{ID: id, Item: item, Field: field, BaseRevision: revision, BaseValue: base, Resolved: resolved == 1}, nil
}

func uoDeviceValue(row []any) (UODevice, error) {
	if len(row) != 2 {
		return UODevice{}, ErrUnqualified
	}
	id, ok := row[0].(string)
	if !ok || len(id) == 0 || len(id) > 128 || !utf8.ValidString(id) {
		return UODevice{}, ErrUnqualified
	}
	sequence, ok := row[1].(int64)
	if !ok || sequence < 0 {
		return UODevice{}, ErrUnqualified
	}
	return UODevice{ID: id, Sequence: sequence}, nil
}

func uoJobValue(row []any, nowMS int64) (UOJob, error) {
	if len(row) != 16 {
		return UOJob{}, ErrUnqualified
	}
	id, ok := row[0].(string)
	if !ok || len(id) == 0 || len(id) > 128 || !utf8.ValidString(id) {
		return UOJob{}, ErrUnqualified
	}
	state, ok := row[1].(string)
	if !ok || !uoJobState(state) {
		return UOJob{}, ErrUnqualified
	}
	code, ok := row[2].(string)
	if !ok || len(code) > 128 || !utf8.ValidString(code) {
		return UOJob{}, ErrUnqualified
	}
	out := UOJob{ID: id, State: state, ErrorCode: code}
	for i, dst := range []*int64{&out.Total, &out.Done, &out.Failed, &out.Cursor, &out.Revision, &out.CreatedMS, &out.UpdatedMS} {
		n, ok := row[3+i].(int64)
		if !ok || n < 0 {
			return UOJob{}, ErrUnqualified
		}
		*dst = n
	}
	if out.Revision <= 0 || out.Done > out.Total || out.Failed > out.Total {
		return UOJob{}, ErrUnqualified
	}
	for i, dst := range []*int64{&out.Owner, &out.Generation, &out.ExpiresMS, &out.Attempts} {
		n, ok := row[11+i].(int64)
		if !ok || n < 0 {
			return UOJob{}, ErrUnqualified
		}
		*dst = n
	}
	if out.Attempts > 4294967295 {
		return UOJob{}, ErrUnqualified
	}
	if row[10] != nil {
		due, ok := row[10].(int64)
		if !ok || due < 0 {
			return UOJob{}, ErrUnqualified
		}
		out.Due, out.DueMS = true, due
	}
	nextDue := true
	if row[15] != nil {
		next, ok := row[15].(int64)
		if !ok || next < 0 {
			return UOJob{}, ErrUnqualified
		}
		out.NextMS = next
		nextDue = next <= nowMS
	}
	out.Runnable = out.Due && out.DueMS <= nowMS && (out.Owner == 0 || out.ExpiresMS <= nowMS) && nextDue
	return out, nil
}

func uoJobState(state string) bool {
	switch state {
	case "building", "queued", "running", "complete", "partial", "failed":
		return true
	}
	return false
}

func uoSettingsValue(row []any) (UOSettings, error) {
	if len(row) != 3 {
		return UOSettings{}, ErrUnqualified
	}
	var out UOSettings
	for i, dst := range []*int64{&out.Revision, &out.UpdatedMS, &out.PendingRevision} {
		n, ok := row[i].(int64)
		if !ok || n < 0 {
			return UOSettings{}, ErrUnqualified
		}
		*dst = n
	}
	return out, nil
}

func uoSettingPendingValue(row []any) (UOSettingPending, error) {
	if len(row) != 2 {
		return UOSettingPending{}, ErrUnqualified
	}
	field, ok := row[0].(string)
	if !ok || len(field) == 0 || len(field) > 128 || !utf8.ValidString(field) {
		return UOSettingPending{}, ErrUnqualified
	}
	revision, ok := row[1].(int64)
	if !ok || revision <= 0 {
		return UOSettingPending{}, ErrUnqualified
	}
	return UOSettingPending{Field: field, Revision: revision}, nil
}

func uoDeliveryValue(row []any) (UODelivery, error) {
	if len(row) != 11 {
		return UODelivery{}, ErrUnqualified
	}
	var out UODelivery
	for i, dst := range []*string{&out.ID, &out.Type, &out.State, &out.Error} {
		value, ok := row[i].(string)
		if !ok || !utf8.ValidString(value) {
			return UODelivery{}, ErrUnqualified
		}
		*dst = value
	}
	if out.ID == "" || len(out.ID) > 128 || out.Type == "" || len(out.Type) > 128 || len(out.State) > 16 || len(out.Error) > 512 {
		return UODelivery{}, ErrUnqualified
	}
	if out.State != "pending" && out.State != "delivered" && out.State != "failed" {
		return UODelivery{}, ErrUnqualified
	}
	for i, dst := range []*int64{&out.Attempts, &out.CreatedMS, &out.FinishedMS, &out.NextMS, &out.Status, &out.DurationMS, &out.Revision} {
		n, ok := row[4+i].(int64)
		if !ok || n < 0 {
			return UODelivery{}, ErrUnqualified
		}
		*dst = n
	}
	if out.Attempts > 4294967295 || out.Status > 599 || out.Revision <= 0 {
		return UODelivery{}, ErrUnqualified
	}
	return out, nil
}
