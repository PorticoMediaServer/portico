package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// bindCheckpointKeeper is trusted startup composition only. openScanStore has
// attested these values against the actual opened native file and re-attests
// every replacement. No writable handle leaves W and no worker can bind one.
func (o *scanOpening) bindCheckpointKeeper(k *governor.CheckpointKeeper, g governor.Governor, a *governor.Authority, wal *governor.WALGate) error {
	if o == nil || o.executor == nil || o.binding == nil {
		return ErrInvalid
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	w := o.executor
	if !w.mu.TryLock() {
		return ErrBusy
	}
	defer w.mu.Unlock()
	if o.closed || w.closed || w.keeper.Load() != nil {
		return ErrClosed
	}
	if err := o.binding.check(); err != nil {
		return err
	}
	service, err := k.BindWriter(g, a, wal, o.binding.device, o.binding.inode)
	if err != nil {
		return err
	}
	w.keeper.Store(service)
	return nil
}
