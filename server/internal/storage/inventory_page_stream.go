package storage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A portable listing is one retained readdir stream in a killable child, not
// one full directory enumeration per request. Backpressure keeps only a page
// ahead of the consumer. The last delivered page is retained for commit retries.
// A process restart/expired cursor fails closed: a new inventory must start;
// an incomplete prior run can never authorize missing-item reconciliation.
const inventoryStreamIdle = 2 * time.Minute

var inventoryStreams = struct {
	sync.Mutex
	once  sync.Once
	byID  map[string]*inventoryStream
	byKey map[string]*inventoryStream
	slots chan struct{}
}{byID: map[string]*inventoryStream{}, byKey: map[string]*inventoryStream{}, slots: make(chan struct{}, 2)}

type inventoryStream struct {
	call                 sync.Mutex
	id, key              string
	ownerKey             string
	operations           *OperationScope
	scopeDone, ownerDone func()
	client               *Client
	request              InventoryRequest
	pages                chan inventoryStreamPage
	demands              chan int
	done, retired        chan struct{}
	cancel               context.CancelFunc
	err                  error     // published by closing done
	touched              time.Time // registry mutex
	last                 *InventoryPage
	lastRaw              int
	pending              int // call mutex; one admitted page survives quantum cancellation
	input, next          string
	number               int64
	finished             bool // registry mutex
}

type inventoryStreamPage struct {
	InventoryPage
	RawUsed int `json:"rawUsed"`
}

func inventoryStreamHelper(r InventoryRequest, in io.Reader, out io.Writer) error {
	commands := bufio.NewReader(in)
	encoder := json.NewEncoder(out)
	for {
		line, err := commands.ReadSlice('\n')
		if err != nil || len(line) > 64 {
			return ErrInventoryChanged
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue // the initial JSON decoder may retain its trailing newline
		}
		var demand struct{ Limit int }
		if json.Unmarshal(line, &demand) != nil || demand.Limit < 1 || demand.Limit > 128 {
			return ErrInventoryChanged
		}
		r.Limit = demand.Limit
		var frame inventoryStreamPage
		issues := make(map[InventoryIssue]struct{})
		for frame.RawUsed < demand.Limit {
			r.Limit = demand.Limit - frame.RawUsed
			page, raw, err := localInventoryPage(r)
			if err != nil {
				return inventoryFailure(err, out)
			}
			if raw == 0 && !page.Complete {
				return ErrInventoryChanged
			}
			if frame.RootIdentity == "" {
				frame.InventoryPage = page
				frame.Entries = nil
				frame.Issues = nil
			}
			frame.Entries = append(frame.Entries, page.Entries...)
			for _, issue := range page.Issues {
				if _, seen := issues[issue]; !seen {
					frame.Issues = append(frame.Issues, issue)
					issues[issue] = struct{}{}
				}
			}
			frame.RawUsed += raw
			frame.Complete, frame.Unstable, frame.NextCursor = page.Complete, page.Unstable, page.NextCursor
			r.RootIdentity, r.DirectoryRevision, r.Cursor = page.RootIdentity, page.DirectoryRevision, page.NextCursor
			if page.Complete {
				break
			}
		}
		if err = encoder.Encode(frame); err != nil {
			return err
		}
		if frame.Complete {
			return nil
		}
	}
}
func inventoryStreamKey(c *Client, key string, r InventoryRequest) string {
	raw, _ := json.Marshal([]any{key, r.Root, r.RelativePath, r.FollowSymlinks, r.Limit})
	// Clients are checked by pointer separately; no raw path is an authority token.
	return fmt.Sprintf("%p:%s", c, raw)
}
func expireInventoryStreams() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for now := range ticker.C {
		inventoryStreams.Lock()
		for id, s := range inventoryStreams.byID {
			if now.Sub(s.touched) < inventoryStreamIdle {
				continue
			}
			s.cancel()
			select {
			case <-s.retired:
				delete(inventoryStreams.byID, id)
				if inventoryStreams.byKey[s.key] == s {
					delete(inventoryStreams.byKey, s.key)
				}
			default:
			}
		}
		inventoryStreams.Unlock()
	}
}
func (c *Client) retainedInventoryPage(ctx context.Context, key string, r InventoryRequest) (InventoryPage, error) {
	var empty InventoryPage
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if r.Limit < 1 || r.Limit > 128 || len(r.Cursor) > 4096 || !filepath.IsAbs(r.Root) || r.RelativePath != "." && !filepath.IsLocal(r.RelativePath) {
		return empty, ErrInventoryChanged
	}
	if c.Guard != nil {
		if err := c.Guard(filepath.Join(r.Root, r.RelativePath)); err != nil {
			return empty, err
		}
	}
	mapKey := inventoryStreamKey(c, key, r)
	inventoryStreams.once.Do(func() { supervise.Go("storage.inventory-expiry", expireInventoryStreams) })
	inventoryStreams.Lock()
	// Job cancellation closes admission before StopInventory takes this lock.
	// A request that passed Guard earlier must not publish a late new producer.
	if err := ctx.Err(); err != nil {
		inventoryStreams.Unlock()
		return empty, err
	}
	var s *inventoryStream
	if r.Cursor != "" {
		parts := strings.Split(r.Cursor, ":")
		if len(parts) != 3 || parts[0] != "stream1" {
			inventoryStreams.Unlock()
			return empty, ErrInventoryChanged
		}
		s = inventoryStreams.byID[parts[1]]
		// The physical stream retains its original full binding, including its
		// initial Limit. A continuation separately admits the next page budget.
		binding := r
		if s != nil {
			binding.Limit = s.request.Limit
		}
		owner, _ := ctx.Value(inventoryOperationsKey{}).(*OperationScope)
		if s == nil || s.client != c || s.operations != owner || owner != nil && owner.Context().Err() != nil || s.key != inventoryStreamKey(c, key, binding) {
			inventoryStreams.Unlock()
			return empty, ErrInventoryChanged
		}
		mapKey = s.key
	} else {
		s = inventoryStreams.byKey[mapKey]
		if s != nil && s.client != c {
			inventoryStreams.Unlock()
			return empty, ErrBusy
		}
		if s == nil {
			if len(inventoryStreams.byID) >= 32 {
				// Evict only completed, physically retired replay caches. A
				// busy directory never blocks the registry mutex or loses debt.
				var oldest *inventoryStream
				for _, candidate := range inventoryStreams.byID {
					if candidate.finished && (oldest == nil || candidate.touched.Before(oldest.touched)) {
						oldest = candidate
					}
				}
				if oldest != nil {
					delete(inventoryStreams.byID, oldest.id)
				}
			}
			if len(inventoryStreams.byID) >= 32 {
				inventoryStreams.Unlock()
				return empty, ErrBusy
			}
			select {
			case inventoryStreams.slots <- struct{}{}:
			default:
				inventoryStreams.Unlock()
				return empty, ErrBusy
			}
			id, err := inventoryToken()
			if err != nil {
				<-inventoryStreams.slots
				inventoryStreams.Unlock()
				return empty, err
			}
			operations, _ := ctx.Value(inventoryOperationsKey{}).(*OperationScope)
			var ownerDone, scopeDone func()
			if operations != nil {
				ownerDone, err = operations.Begin()
			}
			if err == nil && c.SourceOperations != nil {
				scopeDone, err = c.SourceOperations.Begin()
			}
			if err != nil {
				<-inventoryStreams.slots
				if ownerDone != nil {
					ownerDone()
				}
				inventoryStreams.Unlock()
				return empty, err
			}
			parent := context.Background()
			if c.SourceOperations != nil {
				parent = c.SourceOperations.Context()
			}
			life, cancel := context.WithTimeout(parent, 30*time.Minute)
			if operations != nil {
				stop := context.AfterFunc(operations.Context(), cancel)
				originalCancel := cancel
				cancel = func() { stop(); originalCancel() }
				if operations.Context().Err() != nil {
					cancel()
				}
			}
			s = &inventoryStream{id: id, key: mapKey, ownerKey: key, operations: operations, scopeDone: scopeDone, ownerDone: ownerDone, client: c, request: r, pages: make(chan inventoryStreamPage, 1), demands: make(chan int, 1), done: make(chan struct{}), retired: make(chan struct{}), cancel: cancel}
			inventoryStreams.byID[id], inventoryStreams.byKey[mapKey] = s, s
			supervise.Go("storage.inventory-page", func() { s.produce(life) })
		}
	}
	owner, _ := ctx.Value(inventoryOperationsKey{}).(*OperationScope)
	if s.operations != owner || owner != nil && owner.Context().Err() != nil {
		inventoryStreams.Unlock()
		return empty, ErrInventoryChanged
	}
	s.touched = time.Now()
	inventoryStreams.Unlock()
	if !s.call.TryLock() {
		return empty, ErrBusy
	}
	defer s.call.Unlock()
	validate := func(p InventoryPage) (InventoryPage, error) {
		if r.RootIdentity != "" && !SameRoot(r.RootIdentity, p.RootIdentity) || r.DirectoryRevision != "" && p.DirectoryRevision != r.DirectoryRevision {
			s.cancel()
			return empty, ErrInventoryChanged
		}
		return p, nil
	}
	if s.last != nil && r.Cursor == s.input {
		if s.lastRaw > r.Limit {
			return empty, ErrBusy // an immutable replay cannot exceed this request
		}
		return validate(*s.last)
	}
	if r.Cursor != s.next {
		return empty, ErrInventoryChanged
	}
	if s.pending == 0 {
		s.pending = r.Limit
		s.demands <- r.Limit
	} else if s.pending != r.Limit {
		// A timed-out turn retries its already-admitted page; it cannot replace
		// that credit while the helper is still reading it.
		return empty, ErrBusy
	}
	// A quantum timeout is not a producer timeout. A slow first page keeps its
	// descriptor, buffered entries and physical supervisor permit across retries.
	wait := 500 * time.Millisecond
	if c.Timeout > 0 && c.Timeout < wait {
		wait = c.Timeout
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	var frame inventoryStreamPage
	select {
	case frame = <-s.pages:
	case <-s.done:
		select {
		case frame = <-s.pages:
		default:
			if s.err != nil {
				inventoryStreams.Lock()
				if inventoryStreams.byKey[mapKey] == s {
					delete(inventoryStreams.byKey, mapKey)
				}
				inventoryStreams.Unlock()
				return empty, s.err
			}
			return empty, ErrInventoryChanged
		}
	case <-ctx.Done():
		return empty, ctx.Err()
	case <-timer.C:
		return empty, ErrBusy
	}
	page := frame.InventoryPage
	s.pending = 0
	s.lastRaw = frame.RawUsed
	s.number++
	page.NextCursor = ""
	if !page.Complete {
		page.NextCursor = "stream1:" + s.id + ":" + strconv.FormatInt(s.number, 10)
	}
	s.input, s.next, s.last = r.Cursor, page.NextCursor, &page
	// Once a continuation is consumed, the empty-cursor entry is no longer a
	// retry of this stream. A newly queued scan may start afresh while the old
	// cursor remains explicitly bound by ID (and bounded by idle retirement).
	if r.Cursor != "" {
		inventoryStreams.Lock()
		if inventoryStreams.byKey[mapKey] == s {
			delete(inventoryStreams.byKey, mapKey)
		}
		inventoryStreams.Unlock()
	}
	if page.Complete {
		inventoryStreams.Lock()
		s.finished = true
		if r.Cursor == "" {
			delete(inventoryStreams.byID, s.id)
		}
		if inventoryStreams.byKey[mapKey] == s {
			delete(inventoryStreams.byKey, mapKey)
		}
		inventoryStreams.Unlock()
	}
	return validate(page)
}

// StopInventory retires only this client's exact scan key. Capacity remains
// charged until the original process physically exits and Wait returns.
func (c *Client) StopInventory(key string) {
	inventoryStreams.Lock()
	defer inventoryStreams.Unlock()
	for _, stream := range inventoryStreams.byID {
		if stream.client == c && stream.ownerKey == key {
			stream.cancel()
		}
	}
}

func (s *inventoryStream) produce(ctx context.Context) {
	defer s.cancel()
	c := s.client
	r := s.request
	r.Stream = true
	r.Cursor = ""
	req := request{Operation: "inventory-page", Path: filepath.Join(r.Root, r.RelativePath), Inventory: &r}
	if c.MountedRoot != nil {
		req.MountedRoot = c.MountedRoot(req.Path)
	}
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	cmd := exec.Command(c.Binary, "--portico-storage-helper")
	cmd.Stdin = input
	var final *inventoryStreamPage
	retired := func() {
		<-inventoryStreams.slots
		if s.scopeDone != nil {
			s.scopeDone()
		}
		if s.ownerDone != nil {
			s.ownerDone()
		}
		if ctx.Err() != nil {
			inventoryStreams.Lock()
			delete(inventoryStreams.byID, s.id)
			if inventoryStreams.byKey[s.key] == s {
				delete(inventoryStreams.byKey, s.key)
			}
			inventoryStreams.Unlock()
		}
		close(s.retired)
	}
	// Keep the global producer slot and owner accounting until physical Wait,
	// even when cancellation returns to this goroutine before a stuck child dies.
	s.err = c.Supervisor.RunOwnedCompletion(ctx, "inventory-stream:"+s.id, cmd, func(reader io.Reader) error {
		defer writer.Close() // unblock exec's stdin copier before physical Wait
		encoder := json.NewEncoder(writer)
		if err := encoder.Encode(req); err != nil {
			return err
		}
		scanner := newInventoryPageDecoder(reader)
		for {
			var limit int
			select {
			case limit = <-s.demands:
			case <-ctx.Done():
				return ctx.Err()
			}
			if err := encoder.Encode(struct{ Limit int }{limit}); err != nil {
				return err
			}
			page, err := scanner()
			if err != nil {
				return err
			}
			if page.ErrorCode != "" {
				return inventoryCodeError(page.ErrorCode)
			}
			if page.RawUsed < len(page.Entries) || page.RawUsed > limit || page.RawUsed < 0 || !page.Complete && page.RawUsed == 0 || page.RootIdentity == "" || page.DirectoryIdentity == "" || page.DirectoryRevision == "" {
				return ErrInventoryChanged
			}
			if page.Complete {
				copy := page
				final = &copy
				if _, err := scanner(); err != io.EOF {
					return ErrInventoryChanged
				}
				return nil
			}
			select {
			case s.pages <- page:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}, nil, retired)
	if s.err != nil {
		s.cancel()
		select {
		case <-s.retired:
			inventoryStreams.Lock()
			delete(inventoryStreams.byID, s.id)
			if inventoryStreams.byKey[s.key] == s {
				delete(inventoryStreams.byKey, s.key)
			}
			inventoryStreams.Unlock()
		default:
		}
	}
	if s.err == nil && final != nil {
		select {
		case s.pages <- *final:
		case <-ctx.Done():
			s.err = ctx.Err()
		}
	}
	close(s.done)
}

// Bound each wire page independently; do not cap the entire directory stream.
func newInventoryPageDecoder(reader io.Reader) func() (inventoryStreamPage, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	return func() (inventoryStreamPage, error) {
		var p inventoryStreamPage
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return p, err
			}
			return p, io.EOF
		}
		if err := json.Unmarshal(scanner.Bytes(), &p); err != nil {
			return p, err
		}
		return p, nil
	}
}
