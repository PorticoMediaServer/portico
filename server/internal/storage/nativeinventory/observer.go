package nativeinventory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/naming"
	"github.com/PorticoMediaServer/portico/server/internal/storage/sourceops"
)

// Transport is the closed typed bridge to the EXISTING Client/Supervisor.
// It accepts no caller callback, command kind, runner or authority flag.
// Trusted composition supplies the same actual storage instance.
type Transport interface {
	NativeInventoryPreflight(Request) ([]byte, error)
	NativeInventorySettings() Settings
	NativeInventoryExchange(context.Context, *Exchange) error
}

type Settings struct {
	Timeout time.Duration
	Monitor *Monitor
}

// Observer is a bound IO adapter, not a source/run/lease capability or issuer.
// W retains the funded original preparation and its private sourceops.Scope.
type Observer struct{ transport Transport }

func NewObserver(transport Transport) Observer { return Observer{transport: transport} }

func (o Observer) Observe(ctx context.Context, owner *sourceops.Scope, key string, r Request) (Page, error) {
	if runtime.GOOS != "linux" || owner == nil || !validNativeRequest(r) {
		return Page{}, ErrNativeInventory
	}
	return o.observeOwned(ctx, owner, key, r)
}

// Exchange is the opaque original wire/consumer/retirement state. Its fields
// and construction stay here; the parent bridge uses the original spawn site.
// No public callback or alternative execution function is carried by it.
type Exchange struct {
	key        string
	input      []byte
	lease      *sourceops.Lease
	retirement *Retirement
	request    Request
	page       *Page
}

func (e *Exchange) Key() string   { return e.key }
func (e *Exchange) Input() []byte { return e.input }
func (e *Exchange) Retire() {
	e.retirement.mu.Lock()
	if e.retirement.finishLocked() {
		e.lease.Release()
		close(e.retirement.done)
	}
	e.retirement.mu.Unlock()
}
func (e *Exchange) Consume(reader io.Reader) error {
	raw, err := io.ReadAll(io.LimitReader(reader, (2<<20)+1))
	if err != nil {
		return err
	}
	if len(raw) > 2<<20 {
		return ErrNativeInventory
	}
	if err := json.Unmarshal(raw, e.page); err != nil {
		return errors.Join(ErrNativeInventory, err)
	}
	return validateNativePage(*e.page, e.request)
}

func (o Observer) observeOwned(ctx context.Context, owner *sourceops.Scope, key string, r Request) (NativeInventoryPage, error) {
	var empty NativeInventoryPage
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	// A pending-retirement return can outlive the caller's request slices.
	// Keep the exact bounded validation witnesses owned by the physical work.
	r.Root = bytes.Clone(r.Root)
	r.RelativePath = bytes.Clone(r.RelativePath)
	r.RootIdentity = strings.Clone(r.RootIdentity)
	r.DirectoryRevision = strings.Clone(r.DirectoryRevision)
	r.Cursor = strings.Clone(r.Cursor)
	mountedRoot, err := o.transport.NativeInventoryPreflight(r)
	if err != nil {
		return empty, err
	}
	wire := Wire{NativeInventoryRequest: r, MountedRoot: mountedRoot}
	if len(wire.MountedRoot) > naming.MaxPathBytes || bytes.IndexByte(wire.MountedRoot, 0) >= 0 {
		return empty, ErrNativeInventory
	}
	data, err := marshalNativeRequest(&wire)
	if err != nil || len(data) > 64<<10 {
		return empty, ErrNativeInventory
	}
	lease, err := owner.Acquire()
	if err != nil {
		return empty, err
	}
	settings := o.transport.NativeInventorySettings()
	retirement := NewRetirement(settings.Monitor)
	timeout := settings.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	lifetime := sourceops.NewDeadline(ctx, timeout)
	defer lifetime.Cancel()
	ctx = nativeInventoryContext(lifetime.Context())
	lifetime.BindOwner(owner)
	defer lifetime.UnbindOwner()
	if owner.Context().Err() != nil {
		lifetime.Cancel()
	}
	var page NativeInventoryPage
	err = o.transport.NativeInventoryExchange(ctx, &Exchange{
		key: key, page: &page,
		input: data, request: r, lease: lease, retirement: retirement,
	})
	// Per-operation barrier, NOT shared owner shutdown or cancellation as proof.
	err = retirement.Wait(err)
	if err == nil {
		err = CodeError(page.ErrorCode)
	}
	if err != nil {
		return empty, err
	}
	return page, nil
}

func CodeError(code string) error {
	switch code {
	case "":
		return nil
	case "not_found":
		return os.ErrNotExist
	case "changed":
		return ErrInventoryChanged
	case "unreadable":
		return os.ErrPermission
	}
	return ErrInventoryChanged
}
