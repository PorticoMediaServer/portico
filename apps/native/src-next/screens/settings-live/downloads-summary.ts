import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {createNativeDownloadsDevice} from '../downloads/device';
import {offlineDownloads} from '../../platform/offline-downloads';
import type {NativeSettingsDownloads, NativeSettingsDownloadsSnapshot} from './ports';

/**
 * Read-only Settings view of the one viewer-scoped downloads device. It borrows
 * the same per-authority lease Saved and Downloads use, so it never adopts a
 * second store or starts a second engine. `readOnly` keeps a Settings visit from
 * starting the online service; an ordinary Downloads borrower upgrades the same
 * lease before its own open. A retired lease yields no snapshot rather than a
 * guessed size.
 */
export function createNativeSettingsDownloads(context: NativeShellBootstrapContext): NativeSettingsDownloads {
  const lease = createNativeDownloadsDevice(context, offlineDownloads, {readOnly: true});
  try {void lease.device.open().catch(() => {});} catch { /* The panel stays absent until a real read succeeds. */ }
  let projected: NativeSettingsDownloadsSnapshot | undefined;
  return Object.freeze({
    getSnapshot: (): NativeSettingsDownloadsSnapshot | undefined => {
      try {
        const snapshot = lease.device.getSnapshot();
        if (!projected || projected.supported !== snapshot.supported || projected.ready !== snapshot.ready
          || projected.usedBytes !== snapshot.usedBytes || projected.freeBytes !== snapshot.freeBytes)
          projected = Object.freeze({supported: snapshot.supported, ready: snapshot.ready, usedBytes: snapshot.usedBytes, freeBytes: snapshot.freeBytes});
        return projected;
      } catch {projected = undefined; return undefined;}
    },
    subscribe: (listener: () => void) => {
      try {return lease.device.subscribe(listener);} catch {return () => {};}
    },
    dispose: lease.dispose,
  });
}
