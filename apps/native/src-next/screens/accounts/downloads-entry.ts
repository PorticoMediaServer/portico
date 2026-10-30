import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {sameAuthority} from '../../shell/model';
import {nativeDestinationLifetime} from '../destinations/lifetime';
import {createNativeDownloadsDevice} from '../downloads/device';

import {noAccountDownloads, type AccountDownloadsEntry, type AccountDownloadsSnapshot} from './downloads-entry-model';
export type {AccountDownloadsEntry} from './downloads-entry-model';
const availableDownloads: AccountDownloadsSnapshot = Object.freeze({available: true});
const cancelled = () => Object.assign(new Error('native_downloads_opening_closed'), {code: 'cancelled'});

/** Borrow the existing admitted viewer's device; never derive a viewer from an
 * account directory, invitation, stored label or raw receipt. Media opens only through the existing route. */
export function createAccountDownloadsEntry(context: NativeShellBootstrapContext, ownerCurrent: () => boolean): AccountDownloadsEntry {
  const lifetime = nativeDestinationLifetime(context), authority = Object.freeze({...context.authority()});
  const {route: originRoute, revision: originRevision} = context.navigation.getSnapshot();
  let disposed = false, state = noAccountDownloads;
  const listeners = new Set<() => void>();
  const current = () => {
    try {if (disposed || !ownerCurrent() || !lifetime.current() || !sameAuthority(authority, context.authority())) return false;
      const nav = context.navigation.getSnapshot(); return nav.route === originRoute && nav.revision === originRevision;
    } catch {return false;}
  };
  if (!current()) throw cancelled();
  const lease = createNativeDownloadsDevice(context);
  if (!current()) {lease.dispose(); throw cancelled();}
  const usable = () => {
    if (!current()) return false;
    try {const value = lease.device.getSnapshot();
      return value.supported && value.ready && typeof lease.device.openOffline === 'function' && value.entries.some(entry => entry.state === 'ready' && lease.device.playabilityReason(entry) === null) && current();
    } catch {return false;}
  };
  const refresh = () => {const next = usable() ? availableDownloads : noAccountDownloads;
    if (state === next) return; state = next; listeners.forEach(listener => listener());};
  let offDevice = () => {}, offAuthority = () => {}, offNavigation = () => {};
  try {offDevice = lease.device.subscribe(refresh); offAuthority = context.subscribeAuthority(refresh); offNavigation = context.navigation.subscribe(refresh); refresh();
    if (!current()) throw cancelled();
    void Promise.resolve().then(() => {if (current()) return lease.device.open();}).then(refresh, refresh);}
  catch (error) {offDevice(); offAuthority(); offNavigation(); lease.dispose(); throw error;}
  return Object.freeze({
    getSnapshot: () => usable() ? availableDownloads : noAccountDownloads,
    subscribe: listener => {if (disposed) return () => {}; listeners.add(listener); return () => {listeners.delete(listener);};},
    current,
    open: () => {if (current() && usable()) context.navigation.open({kind: 'downloads'}, authority);},
    dispose: () => {if (disposed) return; disposed = true; state = noAccountDownloads; listeners.clear();
      offDevice(); offAuthority(); offNavigation(); lease.dispose();},
  });
}
