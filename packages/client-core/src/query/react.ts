/**
 * `useQuery` for web and native from one implementation. client-core does not import React, so
 * each app passes its own hooks once (`createQueryHooks(React)`).
 *
 * A screen gets the document under its key: shown at once when the viewer's cache already has it,
 * read once however many screens ask, and kept current by the hub. `data` stays on screen while it
 * is read again; `isPending` is only "nothing to show yet", never a refresh.
 */
import {QueryObserver, notifyManager, type QueryKey, type QueryObserverOptions, type QueryObserverResult} from '@tanstack/query-core';
import type {ViewerQueries} from './viewer-queries.ts';

export type ReactHooks = Readonly<{
  useMemo<T>(factory: () => T, deps: readonly unknown[]): T;
  useEffect(effect: () => void | (() => void), deps?: readonly unknown[]): void;
  useCallback<T extends (...args: never[]) => unknown>(callback: T, deps: readonly unknown[]): T;
  useSyncExternalStore<T>(subscribe: (listener: () => void) => () => void, getSnapshot: () => T, getServerSnapshot?: () => T): T;
}>;

export type ViewerQueryOptions<TData, TKey extends QueryKey = QueryKey> = QueryObserverOptions<TData, Error, TData, TData, TKey>;

export function createQueryHooks(react: ReactHooks) {
  /** The document under `options.queryKey` in `queries`, this viewer's cache. */
  function useViewerQuery<TData, TKey extends QueryKey = QueryKey>(queries: ViewerQueries, options: ViewerQueryOptions<TData, TKey>): QueryObserverResult<TData, Error> {
    const {client} = queries;
    const defaulted = client.defaultQueryOptions(options as never) as unknown as ViewerQueryOptions<TData, TKey> & {_optimisticResults?: string};
    defaulted._optimisticResults = 'optimistic';
    // A different viewer is a different cache: never an observer of the previous one.
    const observer = react.useMemo(() => new QueryObserver<TData, Error, TData, TData, TKey>(client, defaulted), [client]);
    const subscribe = react.useCallback((listener: () => void) => observer.subscribe(notifyManager.batchCalls(listener)), [observer]);
    const current = react.useCallback(() => observer.getCurrentResult(), [observer]);
    react.useSyncExternalStore(subscribe, current, current);
    react.useEffect(() => { observer.setOptions(defaulted); }, [observer, defaulted]);
    // The result for this render's key, even before the effect above runs (a key change shows the new document's state at once).
    return observer.getOptimisticResult(defaulted as never) as QueryObserverResult<TData, Error>;
  }
  return {useViewerQuery};
}
