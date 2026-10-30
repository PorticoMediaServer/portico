import {useCallback, useLayoutEffect, useState, useSyncExternalStore} from 'react';
import type {DashboardBindings} from './bindings';
import {readDashboardAccounts} from './pages';
export type ReadState<T> = Readonly<{value?: T; loading: boolean; failed: boolean}>;
const defaultChanges = ['admin.sessions', 'session.updated', 'playback.history', 'library.scan', 'job.updated', 'stream.resync'];
const accountChanges = ['playback.history', 'stream.resync'];
/** One read in flight, cancellation and identity fence on every completion. Events coalesce.
 * Layout cleanup withdraws the original loader before a new range/owner commits;
 * a held old response cannot publish or report through the passive-cleanup gap. */
export function useDashboardRead<T>(bindings: DashboardBindings, load: (signal: AbortSignal) => Promise<T>, enabled: boolean, everyMs = 0, changes: readonly string[] = defaultChanges) {
  const [version, setVersion] = useState(0), [state, setState] = useState<ReadState<T> & {loader?: typeof load}>({loading: true, failed: false});
  const refresh = useCallback(() => setVersion(v => v + 1), []);
  useLayoutEffect(() => {
    if (!enabled || !bindings.isCurrent()) return;
    const controller = new AbortController(); let flight: Promise<void> | undefined, queued = false, lastError: unknown;
    setState(previous => previous.loader === load ? {...previous, loading: true, failed: false} : {loader: load, loading: true, failed: false});
    const read = (): Promise<void> => {
      if (flight) {queued = true; return flight;}
      if (controller.signal.aborted || !bindings.isCurrent() || document.hidden) return Promise.resolve();
      flight = Promise.resolve().then(async () => {
        do {
          queued = false; lastError = undefined;
          if (controller.signal.aborted || !bindings.isCurrent() || document.hidden) return;
          try {const value = await load(controller.signal); if (!controller.signal.aborted && bindings.isCurrent()) setState({loader: load, value, loading: false, failed: false});}
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
    const timer = everyMs ? setInterval(update, everyMs) : undefined; update();
    return () => {controller.abort(); if (timer) clearInterval(timer); offs.forEach(off => off?.()); document.removeEventListener('visibilitychange', visibility);};
  }, [bindings, load, enabled, everyMs, version, changes]);
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
