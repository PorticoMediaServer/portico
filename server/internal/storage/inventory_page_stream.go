package storage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
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
	call          sync.Mutex
	id, key       string
	client        *Client
	request       InventoryRequest
	pages         chan InventoryPage
	done, retired chan struct{}
	cancel        context.CancelFunc
	err           error     // published by closing done
	touched       time.Time // registry mutex
	last          *InventoryPage
	input, next   string
	number        int64
	finished      bool // registry mutex
}

func inventoryStreamHelper(r InventoryRequest, out io.Writer) error {
	encoder := json.NewEncoder(out)
	for {
		page, err := LocalInventoryPage(r)
		if err != nil {
			return inventoryFailure(err, out)
		}
		if err = encoder.Encode(page); err != nil {
			return err
		}
		if page.Complete {
			return nil
		}
		r.RootIdentity, r.DirectoryRevision, r.Cursor = page.RootIdentity, page.DirectoryRevision, page.NextCursor
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
	var s *inventoryStream
	if r.Cursor != "" {
		parts := strings.Split(r.Cursor, ":")
		if len(parts) != 3 || parts[0] != "stream1" {
			inventoryStreams.Unlock()
			return empty, ErrInventoryChanged
		}
		s = inventoryStreams.byID[parts[1]]
		if s == nil || s.client != c || s.key != mapKey {
			inventoryStreams.Unlock()
			return empty, ErrInventoryChanged
		}
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
			parent := context.Background()
			if c.SourceOperations != nil {
				parent = c.SourceOperations.Context()
			}
			life, cancel := context.WithTimeout(parent, 30*time.Minute)
			s = &inventoryStream{id: id, key: mapKey, client: c, request: r, pages: make(chan InventoryPage, 1), done: make(chan struct{}), retired: make(chan struct{}), cancel: cancel}
			inventoryStreams.byID[id], inventoryStreams.byKey[mapKey] = s, s
			supervise.Go("storage.inventory-page", func() { s.produce(life) })
		}
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
		return validate(*s.last)
	}
	if r.Cursor != s.next {
		return empty, ErrInventoryChanged
	}
	// A quantum timeout is not a producer timeout. A slow first page keeps its
	// descriptor, buffered entries and physical supervisor permit across retries.
	wait := 500 * time.Millisecond
	if c.Timeout > 0 && c.Timeout < wait {
		wait = c.Timeout
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	var page InventoryPage
	select {
	case page = <-s.pages:
	case <-s.done:
		select {
		case page = <-s.pages:
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
func (s *inventoryStream) produce(ctx context.Context) {
	c := s.client
	r := s.request
	r.Stream = true
	r.Cursor = ""
	req := request{Operation: "inventory-page", Path: filepath.Join(r.Root, r.RelativePath), Inventory: &r}
	if c.MountedRoot != nil {
		req.MountedRoot = c.MountedRoot(req.Path)
	}
	data, _ := json.Marshal(req)
	cmd := exec.Command(c.Binary, "--portico-storage-helper")
	cmd.Stdin = bytes.NewReader(data)
	var final *InventoryPage
	var scopeDone func()
	retired := func() {
		if scopeDone != nil {
			scopeDone()
		}
		<-inventoryStreams.slots
		close(s.retired)
	}
	if c.SourceOperations != nil {
		var err error
		scopeDone, err = c.SourceOperations.Begin()
		if err != nil {
			s.err = err
			retired()
			close(s.done)
			return
		}
	}
	// Keep the global producer slot and owner accounting until physical Wait,
	// even when cancellation returns to this goroutine before a stuck child dies.
	s.err = c.Supervisor.RunOwnedCompletion(ctx, "inventory-stream:"+s.id, cmd, func(reader io.Reader) error {
		scanner := newInventoryPageDecoder(reader)
		for {
			page, err := scanner()
			if err == io.EOF {
				if final == nil {
					return io.ErrUnexpectedEOF
				}
				return nil
			}
			if err != nil {
				return err
			}
			if page.ErrorCode != "" {
				return inventoryCodeError(page.ErrorCode)
			}
			if final != nil || len(page.Entries) > 128 || page.RootIdentity == "" || page.DirectoryIdentity == "" || page.DirectoryRevision == "" {
				return ErrInventoryChanged
			}
			if page.Complete {
				copy := page
				final = &copy
				continue
			}
			select {
			case s.pages <- page:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}, nil, retired)
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
func newInventoryPageDecoder(reader io.Reader) func() (InventoryPage, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	return func() (InventoryPage, error) {
		var p InventoryPage
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
