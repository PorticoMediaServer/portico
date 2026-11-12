import {useCallback, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import type {DashboardBindings} from './bindings';
import {readDashboardAccounts} from './pages';
export type ReadState<T> = Readonly<{value?: T; loading: boolean; failed: boolean}>;
const defaultChanges = ['admin.sessions', 'session.updated', 'playback.history', 'library.scan', 'job.updated', 'stream.resync'];
const accountChanges = ['playback.history', 'stream.resync'];
/** One read in flight, cancellation and identity fence on every completion. Events coalesce.
 * Layout cleanup withdraws the original loader before a new range/owner commits;
 * a held old response cannot publish or report through the passive-cleanup gap. */
/** `everyMs` reads again on a timer; with `fallback`, only while the change feed is not connected (a section the server's
 * changes keep current, such as Now playing and the status on `admin.sessions`). */
export function useDashboardRead<T>(bindings: DashboardBindings, load: (signal: AbortSignal, fresh: boolean) => Promise<T>, enabled: boolean, everyMs = 0, changes: readonly string[] = defaultChanges, fallback = false) {
  const [version, setVersion] = useState(0), [state, setState] = useState<ReadState<T> & {loader?: typeof load}>({loading: true, failed: false});
  // A refresh follows an action (Fix permissions, Retry): its read asks the server, never the held copy from before the action.
  const forced = useRef(false);
  const refresh = useCallback(() => {forced.current = true; setVersion(v => v + 1);}, []);
  useLayoutEffect(() => {
    if (!enabled || !bindings.isCurrent()) return;
    const controller = new AbortController(); let flight: Promise<void> | undefined, queued = false, lastError: unknown, first = !forced.current;
    forced.current = false;
    setState(previous => previous.loader === load ? {...previous, loading: true, failed: false} : {loader: load, loading: true, failed: false});
    const read = (): Promise<void> => {
      if (flight) {queued = true; return flight;}
      if (controller.signal.aborted || !bindings.isCurrent() || document.hidden) return Promise.resolve();
      flight = Promise.resolve().then(async () => {
        do {
          queued = false; lastError = undefined;
          if (controller.signal.aborted || !bindings.isCurrent() || document.hidden) return;
          // The first read of a mount may be answered by the viewer's held copy; every later one (an event, a poll, a retry) asks the server.
          const fresh = !first; first = false;
          try {const value = await load(controller.signal, fresh); if (!controller.signal.aborted && bindings.isCurrent()) setState({loader: load, value, loading: false, failed: false});}
          catch (error) {lastError = error; if (!controller.signal.aborted && bindings.isCurrent()) {setState({loader: load, loading: false, failed: true}); bindings.effects.reportError(error);}}
        } while (queued);
      }).finally(() => {flight = undefined;});
      return flight;
    };
    const update = () => {void read();};
    const offs = changes.map(type => bindings.events?.on(type, update));
    const recovery = bindings.events?.onResyncRead?.(async () => {await read(); if (lastError) throw lastError;}, {open: !document.hidden, admission: 'external'});
    offs.push(recovery ? () => recovery.dispose() : bindings.events?.onResync?.(update));
    const visibility = () => {recovery?.setOpen(!document.hidden); update();};
    document.addEventListener('visibilitychange', visibility);
    const timer = everyMs ? setInterval(() => {if (!fallback || bindings.events?.getStatus?.().connected !== true) update();}, everyMs) : undefined; update();
    return () => {controller.abort(); if (timer) clearInterval(timer); offs.forEach(off => off?.()); document.removeEventListener('visibilitychange', visibility);};
  }, [bindings, load, enabled, everyMs, version, changes, fallback]);
  return {...(state.loader === load ? state : {loading: true, failed: false}), refresh};
}

/** The filter enumerates actual account IDs, including servers with more than one hundred people. */
export function useDashboardAccounts(bindings: DashboardBindings, enabled = true) {
  const load = useCallback((signal: AbortSignal) => readDashboardAccounts(bindings, signal), [bindings]);
  const state = useDashboardRead(bindings, load, enabled, 0, accountChanges);
  return {...state, items: state.value ?? []};
}
export type DashboardAccounts = ReturnType<typeof useDashboardAccounts>;

const noAuthoritySubscription = () => () => {};
export function useDashboardAuthority(bindings: DashboardBindings): boolean {
  return useSyncExternalStore(bindings.subscribeAuthority ?? noAuthoritySubscription, bindings.isCurrent, () => false);
}
