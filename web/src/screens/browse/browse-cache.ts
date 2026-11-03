import {useCallback, useLayoutEffect, useRef, useSyncExternalStore} from 'react';
import type {QueryKey} from '@tanstack/query-core';
import type {ViewerQueries} from '@core/query/viewer-queries.ts';
import type {LibraryContentApi} from '@core/library-content.ts';
import {useQuery} from '../../platform/freshness';
import {BrowseController, type BrowseSource} from './controller';

/** The grid has something to show for its query: its first page, or why it has none. */
function answered(controller: BrowseController): boolean {
  const state = controller.getSnapshot();
  return state.phase === 'error' || state.phase === 'ready' && (!!state.revision || controller.collection.getSnapshot().error !== undefined);
}
const noSubscribe = () => () => {};

/** The cache's query for one grid: the grid is built, starts its first read and joins the hub at
 *  once, so a screen can draw its loading state; it is let go when it leaves the cache. */
export function browseGridQuery(queries: ViewerQueries, queryKey: QueryKey, source: BrowseSource, raw?: LibraryContentApi['requestRaw']) {
  return {
    queryKey,
    // The key names everything the grid is built from, so the first source for a key is its only one.
    queryFn: async (): Promise<BrowseController> => {
      const controller = new BrowseController({request: queries.api.request, ...(raw ? {requestRaw: raw} : {})}, source, () => !queries.isDisposed());
      void controller.load();
      controller.keepCurrent(queries);
      return controller;
    },
    // The value is the live grid itself: never compared or copied.
    structuralSharing: false,
    meta: {release: (value: unknown) => (value as BrowseController).dispose()},
  } as const;
}

/**
 * A browse grid from the viewer's cache, under its query key (a library tab's or a See all
 * page's address, filters and order included). The grid is a live value, not a document: its
 * windowed pages, its session and its place stay with it, so Back to a grid already seen draws it
 * at once with no request. The hub keeps it current while it is cached (`keepCurrent`), and reads
 * it in place while it is shown. A new key on the same screen (a filter or a sort) keeps the grid
 * shown until the new one has its first page. A grid that leaves the cache is disposed with it, and
 * the viewer's cache ending disposes every one.
 *
 * `raw` is the screen API's raw reader, which the preparation reader uses for its late handles.
 */
export function useBrowseController(queries: ViewerQueries, queryKey: QueryKey | undefined, source: BrowseSource, raw?: LibraryContentApi['requestRaw']) {
  const result = useQuery<BrowseController>({...browseGridQuery(queries, queryKey ?? ['browse', 'none'], source, raw), enabled: queryKey !== undefined});
  const latest = queryKey === undefined || result.data?.isDisposed() ? undefined : result.data;
  const subscribe = useCallback((listener: () => void) => {
    if (!latest) return noSubscribe();
    const grid = latest.subscribe(listener), pages = latest.collection.subscribe(listener);
    return () => {grid(); pages();};
  }, [latest]);
  const ready = useSyncExternalStore(subscribe, () => !!latest && answered(latest), () => false);
  // The grid this screen showed last, kept on screen until its successor has something to show.
  const shown = useRef<BrowseController | undefined>(undefined);
  const previous = shown.current && shown.current !== latest && !shown.current.isDisposed() ? shown.current : undefined;
  const controller = queryKey === undefined ? undefined : latest && (ready || !previous) ? latest : previous;
  useLayoutEffect(() => {shown.current = controller;}, [controller]);
  // On screen: the hub reads it in place when something it shows moves.
  useLayoutEffect(() => controller?.show(), [controller]);
  return {controller, placeholder: !!controller && controller !== latest};
}
