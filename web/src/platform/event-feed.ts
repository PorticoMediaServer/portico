import {routePath} from '../../../packages/contracts/src/routes.ts';
import {eventStreamOpener, sharedStreamOpener, type EventFeedEnvironment, type SharedStreamEnvironment} from '@core/event-feed.ts';
import type {StreamOpener} from '@core/playback-v1/events.ts';
import type {HttpLocalApi} from '@core/index.ts';
import {browserLeaseEnvironment, leaseLock} from './tab-lease';

/**
 * The browser's side of the device's one event feed.
 *
 * The stream is a `fetch` that is read as it arrives (the browser's own EventSource cannot send
 * the sign-in). Every tab of this browser signed in to the same server as the same viewer shares
 * one stream: the server keeps one per installation, so tabs that each opened their own would
 * keep taking it from each other. A named lock decides which tab holds it; a broadcast channel
 * carries each event to the rest.
 *
 * A secure page uses Web Locks. Browsers give a plain-HTTP page (the server's own address on the
 * home network) no Web Locks, so there the lock is a lease in local storage (`tab-lease.ts`): the
 * tabs still share one stream rather than each holding a long poll, which on HTTP/1.1 would use
 * up the browser's few connections to the server. Only where neither works (storage refused, no
 * broadcast channel) does each tab fall back to its own long poll.
 */
const channel: SharedStreamEnvironment['channel'] = name => {
  const broadcast = new BroadcastChannel(name);
  return {
    post: message => { try { broadcast.postMessage(message); } catch { /* closed while leaving the page */ } },
    listen: listener => {
      const heard = (event: MessageEvent) => listener(event.data);
      broadcast.addEventListener('message', heard);
      return () => broadcast.removeEventListener('message', heard);
    },
    close: () => broadcast.close(),
  };
};

/** How this browser can share the stream between tabs, or undefined where it cannot. */
function sharing(): SharedStreamEnvironment | undefined {
  if (typeof window === 'undefined' || typeof BroadcastChannel !== 'function') return undefined;
  // The polyfill gives an insecure page a `navigator.locks` that only orders work within the tab.
  if (window.isSecureContext && typeof navigator !== 'undefined' && typeof navigator.locks?.request === 'function') {
    return {lock: (name, signal, work) => navigator.locks.request(name, {signal}, () => work()) as Promise<void>, channel};
  }
  const leases = browserLeaseEnvironment();
  return leases ? {lock: leaseLock(leases), channel} : undefined;
}

/** The stream opener for this tab, or undefined where only the long poll can be used. `viewer` names who is signed in, never the token. */
export function browserEventStream(api: HttpLocalApi, viewer: string, token: () => string): StreamOpener | undefined {
  const shared = sharing();
  if (!shared) return undefined;
  const open = eventStreamOpener((headers, signal) => api.routeFetch(api.baseUrl + routePath('get_events'), {signal, cache: 'no-store', headers: {...headers, Authorization: 'Bearer ' + token()}}));
  return sharedStreamOpener('portico.events.' + viewer, open, shared);
}

/** The feed follows foreground, playing intent and optional real media custody. */
export function browserFeedEnvironment(busy: () => boolean, onBusy: (listener: () => void) => () => void, retain?: () => boolean, onRetain?: (listener: () => void) => () => void): EventFeedEnvironment {
  return {
    visible: () => document.visibilityState !== 'hidden',
    onVisibility: listener => {
      document.addEventListener('visibilitychange', listener);
      return () => document.removeEventListener('visibilitychange', listener);
    },
    busy, onBusy,
    ...(retain ? {retain} : {}), ...(onRetain ? {onRetain} : {}),
    graceMs: 30_000,
  };
}
