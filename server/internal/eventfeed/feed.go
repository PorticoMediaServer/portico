package eventfeed

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
)

// PageLimit bounds the raw rows one read may examine. A reader further
// behind than this gets one stream.resync instead of a replay.
const PageLimit = 100

// lagWake is how many rows may pass before every reader is woken to read past
// them, comfortably under PageLimit so a connected reader never resyncs for
// lack of a turn.
const lagWake = PageLimit / 2

// PageBytes bounds one whole page as it goes on the wire, as SSE frames or as
// the long-poll body (the larger of the two), every opaque id included.
// pageReserve is held back for the page's own framing and control frames.
const (
	PageBytes       = 64 << 10
	pageReserve     = 1024
	EventDataBytes  = 4096
	eventTypeBytes  = 64
	eventFieldBytes = 128
)

// FrameBytes is what one event costs on the larger of its two encodings: an
// SSE frame ("id: …\nevent: …\ndata: …\n\n") carries the id and type twice.
func FrameBytes(id, kind string, encoded int) int {
	return len("id: \nevent: \ndata: \n\n") + len(id) + len(kind) + encoded
}

// The indexed minimum detects retention gaps. The durable sequence keeps the
// resume position when scheduled pruning empties the ring.
const eventBoundsSQL = `SELECT COALESCE((SELECT min(id) FROM api_events),0),COALESCE((SELECT seq FROM sqlite_sequence WHERE name='api_events'),0)`

type Resource struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}
type Event struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	At       string          `json:"at"`
	Resource Resource        `json:"resource"`
	Revision string          `json:"revision"`
	Data     json.RawMessage `json:"data,omitempty"`
}
type Page struct {
	Events    []Event `json:"events"`
	NextAfter string  `json:"nextAfter"`
	More      bool    `json:"more"`
}

type Hub struct {
	DB      *sql.DB
	mu      sync.Mutex
	readers map[chan struct{}]map[string]bool
	streams map[string]stream
	polls   map[string]int
	next    uint64
	keys    cursorKeys
	// LibraryVisible gates library: audiences. Nil delivers none.
	LibraryVisible func(tx *sql.Tx, p identity.Principal, library string) bool
	// ContentVisible gates retained profile hints against current catalogue
	// authority in the page's snapshot. Nil reveals no title identifiers.
	ContentVisible func(context.Context, *sql.Tx, identity.Principal, string) bool
}
type stream struct {
	id     uint64
	cancel context.CancelFunc
}

func New(db *sql.DB) *Hub {
	return &Hub{DB: db, readers: map[chan struct{}]map[string]bool{}, streams: map[string]stream{}, polls: map[string]int{}}
}

// InstallationKey uses the live token-to-device binding, never a client-sent
// installation header. Two accounts on one installation have separate streams.
func (h *Hub) InstallationKey(ctx context.Context, p identity.Principal) (string, error) {
	var installation string
	err := h.DB.QueryRowContext(ctx, `SELECT d.installation_id FROM authorization_family_tokens t
 JOIN identity_device_families b ON b.family_id=t.family_id
 JOIN identity_devices d ON d.id=b.device_id WHERE t.token_hash=?`, p.Hash).Scan(&installation)
	if err != nil {
		return "", err
	}
	return operations.AccountKey(p) + ":" + installation, nil
}

// Run wakes subscribers from committed changes to the source tables. The
// outbox trigger is in that same commit; no idle client needs a polling loop.
func (h *Hub) Run(ctx context.Context) {
	signal := worker.NewSignal()
	unregister := dbwork.WakeOnTables(signal, "api_events", "notification_inbox", "console_operations", "api_change_pending")
	defer unregister()
	unregisterDirect := dbwork.WakeOnDirectWrites(signal, "api_events", "notification_inbox", "console_operations", "api_change_pending")
	defer unregisterDirect()
	// Security changes must re-authorize existing streams without idle polling.
	security := worker.NewSignal()
	stop := dbwork.WakeOnTableWrites(security, "authorization_session_families", "authorization_family_tokens", "accounts", "direct_memberships", "direct_profiles", "identity_devices", "libraries", "library_sources", "direct_profile_trust", "restrictions", "profile_restrictions", "restriction_outbox", "policy", "library_network_policy", "api_credentials", "hosted_profile_revisions")
	defer stop()
	supervise.Go("api.events.authority", func() {
		for security.Wait(ctx) {
			h.Wake()
		}
	})
	var after, everyone int64
	started := false
	runChanges(ctx, signal, func(ctx context.Context) time.Duration {
		before := dbwork.ChangingCommits()
		count, err := flushPendingHints(ctx, h.DB)
		if err != nil {
			return 2 * time.Second
		}
		rows, err := h.DB.QueryContext(ctx, `SELECT id,audience FROM api_events WHERE id>? ORDER BY id LIMIT 1000`, after)
		if err != nil {
			return 2 * time.Second
		}
		audiences := map[string]bool{}
		read := 0
		for rows.Next() {
			var id int64
			var audience string
			if err = rows.Scan(&id, &audience); err != nil {
				break
			}
			after = id
			audiences[audience] = true
			read++
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return 2 * time.Second
		}
		// Readers are woken only for their own audiences, so one that hears
		// nothing would fall more than a page behind on a busy server and be
		// told to resync for others' activity. Once enough rows have gone by,
		// every reader reads one bounded page and moves past what is not its.
		if !started || after-everyone >= lagWake {
			started, everyone = true, after
			h.Wake()
		} else {
			h.wakeAudiences(audiences)
		}
		if count == 64 || read == 1000 {
			return 2 * time.Second
		}
		retireOwnFlushWake(signal, before, count > 0)
		return 0
	})

}

// flushPendingHints publishes one bounded batch of coalesced hints. It probes
// the outbox on a read handle first, so a pass with nothing pending takes no
// writer gate at all: the gate is the one resource every scan write shares.
//
// The probe is a hint, never the authority. Flush re-reads and re-limits the
// batch inside the transaction, so a hint that lands after an empty probe is
// published by the pass its own commit wakes, and a second drainer that wins
// the race leaves an empty transaction rather than a lost or duplicated hint.
func flushPendingHints(ctx context.Context, db *sql.DB) (int, error) {
	var pending bool
	if err := dbwork.QueryRow(ctx, db, `SELECT EXISTS(SELECT 1 FROM api_change_pending)`).Scan(&pending); err != nil {
		return 0, err
	}
	if !pending {
		return 0, nil
	}
	count := 0
	err := dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		var flushErr error
		count, flushErr = apievents.Flush(ctx, tx, 64)
		return flushErr
	})
	return count, err
}

// A nonempty flush changes exactly one gated transaction; an empty flush
// changes none. Only changing commits can wake subscribers. Counting every
// successful transaction would mistake an unrelated no-change commit for a
// peer write and retain the flush's own already-covered wake. A real peer,
// including an unknown write scope, still retains its pass. A commit after
// this check wakes the signal normally.
func retireOwnFlushWake(signal *worker.Signal, before uint64, ownChange bool) {
	if !signal.Take() {
		return
	}
	covered := before
	if ownChange {
		covered++
	}
	if dbwork.ChangingCommits() != covered {
		signal.Wake()
	}
}

func (h *Hub) Wake() {
	h.mu.Lock()
	for ch := range h.readers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	h.mu.Unlock()
}
func (h *Hub) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	h.readers[ch] = nil
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.readers, ch)
		h.mu.Unlock()
	}
}

// AdmitStream permits one live stream per installation. A newcomer takes the
// place of the stream that was there: the old one is retired and the new one is
// admitted. A device that changed network reconnects before the server has
// noticed its old connection is dead, and refusing it then would turn every
// such reconnect into a failure; there is still only ever one subscription,
// and the newcomer resumes from the cursor it sends.
func (h *Hub) AdmitStream(key string, cancel context.CancelFunc) (func(), bool) {
	if key == "" || cancel == nil {
		return func() {}, false
	}
	h.mu.Lock()
	prior, replaced := h.streams[key]
	h.next++
	id := h.next
	h.streams[key] = stream{id: id, cancel: cancel}
	h.mu.Unlock()
	if replaced {
		prior.cancel()
	}
	return func() {
		h.mu.Lock()
		if current, exists := h.streams[key]; exists && current.id == id {
			delete(h.streams, key)
		}
		h.mu.Unlock()
	}, true
}

// ParkedPolls is how many long polls (waitMs>0) one account on one
// installation may hold open at once (SRV017). A further one is refused at
// once with a paced retry; the client then reads without parking.
const ParkedPolls = 4

// AdmitPoll reserves one parked long-poll permit for key. The release is
// idempotent and frees exactly this reservation. Only keys with a permit held
// stay in memory.
func (h *Hub) AdmitPoll(key string) (func(), bool) {
	if key == "" {
		return func() {}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.polls[key] >= ParkedPolls {
		return func() {}, false
	}
	h.polls[key]++
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			if h.polls[key]--; h.polls[key] <= 0 {
				delete(h.polls, key)
			}
			h.mu.Unlock()
		})
	}, true
}

// administrative is a verified owner or administrator: both hear the admin
// audience's list hints; only the owner's account inbox is the owner's.
//
// One read is one bounded page (SRV018). In one snapshot it captures the
// newest position H and probes the raw rows after the cursor. More than
// PageLimit raw rows, a cursor that does not open for this reader, or a gap
// in the ring all get the same single stream.resync at H: the reader refetches
// current state instead of replaying history. Otherwise it applies every
// audience gate to at most PageLimit rows and keeps whole frames only while
// the page fits PageBytes. Hidden rows are consumed; a visible frame that does
// not fit is left for the next read (More). An empty cursor starts at H.
func (h *Hub) Read(ctx context.Context, p identity.Principal, administrative bool, cursor string) (Page, error) {
	out := Page{Events: []Event{}}
	gate, err := dbwork.BeginSnapshot(ctx, h.DB)
	if err != nil {
		return out, err
	}
	defer gate.Rollback()
	tx := gate.Tx()
	capacityAudience := ""
	if check := capacityRecipient(ctx); check != nil {
		if err = check(ctx, tx, p); err != nil {
			return out, err
		}
		capacityAudience = apievents.CapacityAudience(p.Authority, p.AccountID)
	}
	var deviceID string
	if err = tx.QueryRowContext(ctx, `SELECT device_id FROM identity_device_families b JOIN authorization_family_tokens t ON t.family_id=b.family_id WHERE t.token_hash=?`, p.Hash).Scan(&deviceID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	bound := cursorBinding(p, deviceID, administrative)
	var minimum, maximum int64
	if err = tx.QueryRowContext(ctx, eventBoundsSQL).Scan(&minimum, &maximum); err != nil {
		return out, err
	}
	out.NextAfter = h.keys.seal(maximum, bound)
	if cursor == "" {
		return out, gate.Commit()
	}
	resync := func() (Page, error) {
		out.Events = []Event{{ID: out.NextAfter, Type: "stream.resync", At: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), Resource: Resource{Kind: "stream", ID: "all"}}}
		out.More = false
		return out, gate.Commit()
	}
	after, opened := h.keys.open(cursor, bound)
	if !opened || after > maximum || minimum > 0 && after < minimum-1 || minimum == 0 && after < maximum {
		return resync()
	}
	var raw int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT 1 FROM api_events WHERE id>? AND id<=? ORDER BY id LIMIT ?)`, after, maximum, PageLimit+1).Scan(&raw); err != nil {
		return out, err
	}
	if raw > PageLimit {
		return resync()
	}
	adminInbox := administrative && p.Role == "owner" && p.Authority == "local"
	audience := apievents.ProfileAudience(p.Authority, p.AccountID, p.ProfileID)
	deviceAudience := ""
	if deviceID != "" {
		deviceAudience = apievents.DeviceAudience(deviceID)
	}
	adminAudience := ""
	operationAudience := ""
	if adminInbox {
		adminAudience = apievents.AccountAudience(p.Authority, p.AccountID)
	}
	if administrative {
		operationAudience = apievents.AdminAudience
	}
	addressed := func(row string) bool {
		if row == "server" || strings.HasPrefix(row, "library:") {
			return true
		}
		return row != "" && (row == audience || row == deviceAudience || row == adminAudience || row == operationAudience || row == capacityAudience)
	}
	// The envelope bound is checked in SQL so an oversized payload is never
	// copied out of the database to be measured.
	rows, err := tx.QueryContext(ctx, `SELECT id,audience,type,resource_kind,resource_id,revision,CASE WHEN length(CAST(data AS BLOB))<=?1 THEN data ELSE '' END,at_ms,
 length(CAST(type AS BLOB))>?2 OR length(CAST(resource_kind AS BLOB))>?3 OR length(CAST(resource_id AS BLOB))>?3 OR length(CAST(revision AS BLOB))>?3 OR length(CAST(data AS BLOB))>?1
 FROM api_events WHERE id>?4 AND id<=?5 ORDER BY id`, EventDataBytes, eventTypeBytes, eventFieldBytes, after, maximum)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	// All events and access checks use this one snapshot: each library's
	// verdict is stable for the page, including denied libraries.
	visibility := map[string]bool{}
	contentVisibility := map[string]bool{}
	visibleContent := func(id string) bool {
		if visible, checked := contentVisibility[id]; checked {
			return visible
		}
		visible := h.ContentVisible != nil && h.ContentVisible(ctx, tx, p, id)
		contentVisibility[id] = visible
		return visible
	}
	remaining := PageBytes - pageReserve
	consumed, lastDelivered := after, int64(-1)
	for rows.Next() {
		var id, at int64
		var oversize bool
		var rowAudience, kind, resourceKind, resource, revision, data string
		if err = rows.Scan(&id, &rowAudience, &kind, &resourceKind, &resource, &revision, &data, &at, &oversize); err != nil {
			return Page{}, err
		}
		if !addressed(rowAudience) {
			consumed = id
			continue
		}
		// A capacity hint cannot arrive through server/admin/profile audiences,
		// or carry even a private producer's accidental metadata fields.
		if kind == "capacity.changed" {
			if capacityAudience == "" || rowAudience != capacityAudience || resourceKind != "" || resource != "" || revision != "" || data != "" {
				consumed = id
				continue
			}
		} else if capacityAudience != "" && rowAudience == capacityAudience {
			consumed = id
			continue
		}
		if strings.HasPrefix(rowAudience, "library:") || rowAudience == audience && resourceKind == "library" {
			library := resource
			if strings.HasPrefix(rowAudience, "library:") {
				library = strings.TrimPrefix(rowAudience, "library:")
			}
			visible, checked := visibility[library]
			if !checked {
				visible = h.LibraryVisible != nil && h.LibraryVisible(tx, p, library)
				visibility[library] = visible
			}
			if !visible {
				consumed = id
				continue
			}
		}
		if rowAudience == audience {
			if contentResource(resourceKind) && !visibleContent(resource) {
				consumed = id
				continue
			}
			data = visiblePersonalTitles(data, visibleContent)
		}
		// A deliverable row outside the producer envelope cannot be shortened
		// into a smaller lie; the reader refetches instead.
		if oversize {
			return resync()
		}
		event := Event{ID: h.keys.seal(id, bound), Type: kind, At: time.UnixMilli(at).UTC().Format("2006-01-02T15:04:05.000Z"), Resource: Resource{Kind: resourceKind, ID: resource}, Revision: revision}
		if json.Valid([]byte(data)) {
			event.Data = json.RawMessage(data)
		}
		encoded, err := json.Marshal(event)
		if err != nil {
			return Page{}, err
		}
		cost := FrameBytes(event.ID, event.Type, len(encoded))
		if cost > PageBytes-pageReserve {
			return resync()
		}
		if cost > remaining {
			out.More = true
			break
		}
		remaining -= cost
		out.Events = append(out.Events, event)
		consumed, lastDelivered = id, id
	}
	if err = rows.Err(); err != nil {
		return Page{}, err
	}
	if out.More {
		out.NextAfter = h.keys.seal(consumed, bound)
	} else if len(out.Events) > 0 && lastDelivered == maximum {
		// Same position, same token: a stream need not send a separate
		// position frame after its last event.
		out.NextAfter = out.Events[len(out.Events)-1].ID
	}
	return out, gate.Commit()
}

// SubscribeFor filters private profile/device changes in memory. Library hints
// still pass the snapshot authorization check in Read before delivery.
func (h *Hub) SubscribeFor(p identity.Principal, administrative bool) (<-chan struct{}, func()) {
	return h.subscribeFor(p, administrative, false)
}
func (h *Hub) subscribeFor(p identity.Principal, administrative, capacity bool) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	audiences := map[string]bool{apievents.ProfileAudience(p.Authority, p.AccountID, p.ProfileID): true, "server": true}
	if p.DeviceID != "" {
		audiences[apievents.DeviceAudience(p.DeviceID)] = true
	}
	if capacity {
		audiences[apievents.CapacityAudience(p.Authority, p.AccountID)] = true
	}
	if administrative {
		audiences[apievents.AdminAudience] = true
		if p.Role == "owner" {
			audiences[apievents.AccountAudience(p.Authority, p.AccountID)] = true
		}
	}
	h.mu.Lock()
	h.readers[ch] = audiences
	h.mu.Unlock()
	return ch, func() { h.mu.Lock(); delete(h.readers, ch); h.mu.Unlock() }
}
func (h *Hub) wakeAudiences(changed map[string]bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch, scope := range h.readers {
		relevant := false
		for audience := range changed {
			if scope == nil || scope[audience] || strings.HasPrefix(audience, "library:") {
				relevant = true
				break
			}
		}
		if relevant {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
}

func (h *Hub) TokenDeadline(ctx context.Context, p identity.Principal) (time.Time, error) {
	var expiry string
	err := h.DB.QueryRowContext(ctx, `SELECT min(t.expires_at,f.authorization_horizon) FROM authorization_family_tokens t JOIN authorization_session_families f ON f.id=t.family_id WHERE t.token_hash=?`, p.Hash).Scan(&expiry)
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339Nano, expiry)
}

// runChanges sleeps indefinitely when no committed changes need delivery.
func runChanges(ctx context.Context, signal *worker.Signal, step func(context.Context) time.Duration) {
	var last time.Time
	for ctx.Err() == nil {
		if !last.IsZero() {
			remaining := 2*time.Second - time.Since(last)
			if remaining > 0 {
				timer := time.NewTimer(remaining)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}
		delay := step(ctx)
		last = time.Now()
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		} else if !signal.Wait(ctx) {
			return
		}
	}
}
