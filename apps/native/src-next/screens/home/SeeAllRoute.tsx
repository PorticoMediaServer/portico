import {useNativeStackHosted, useNativeStackPlacement} from '../../kit/navigation/stack-slot';
import React, {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {Platform, View, useWindowDimensions} from 'react-native';
import {useFocusRegistry, type FocusSnapshot} from '../../kit/focus';
import {isRecommendationRow, notInterestedTarget, setNotInterested} from '../../../../../packages/client-core/src/recommendation-feedback';
import {ToastHost} from '../../kit/overlays';
import {useDockClearance} from '../../kit/dock/DockClearance';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {HomeSeeAll} from './HomeSeeAll';
import {HomeRowFilters} from './HomeRowFilters';
import {NativeHomeRowController} from './row-controller';
import {nativeRouteCrypto} from '../../platform/server-connections';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRuntime} from '../../shell/runtime';
import {nativeEntryRoute, nativeEntryRouteSupported, type NativeShellRoute} from '../../shell/routes';
import {nativeShellRoutes, type NativeRouteRenderer} from '../../shell/route-registry';
import {shellLayout} from '../../shell/model';
import {useTheme} from '../../kit/theme';
import {hiddenReadingLibraryOnTV} from '../read/platform';
import {openNativeReadingBookEntry} from '../read/reading-books-open';
import {nativeDispatchedPlayLifetime, nativeHomeEntryLifetime, reportDispatchedPlayFailure} from './entry-lifetime';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import {createNativeEntryActions, type NativeEntryActions} from '../destinations/entry-actions';

type RowRoute = Extract<NativeShellRoute, {kind: 'home-see-all'}>;
type RowLifetime = Readonly<{route: RowRoute; controller: NativeHomeRowController; actions: NativeEntryActions; abort: AbortController; key: number; labels: ReadonlyMap<string, string>}>;

export function createNativeHomeSeeAllRenderer(context: NativeShellBootstrapContext): NativeRouteRenderer & {renderRetained: (runtime: NativeShellRuntime) => React.ReactNode} {
  let disposed = false, runtime: NativeShellRuntime | undefined;
  const rows = createNativeHomeRows(context, error => {if (!disposed && context.current()) {if (!runtime) throw error; runtime.reportError(error);}});
  const unsubscribe = context.navigation.subscribe(rows.reconcile);
  rows.reconcile();
  return {
    render: (route, actual) => {
      // Only real row destinations are this renderer's page. A collection banner
      // opens the registered collection page itself; anything else here is not a
      // row and simply keeps no retained page.
      if (route.kind !== 'home-see-all' || route.destination.type !== 'row') return null;
      runtime = actual; rows.reconcile(); return null;
    },
    renderRetained: actual => {runtime = actual; return <HomeRowsCanvas>{rows.render(actual)}</HomeRowsCanvas>;},
    dispose: () => {disposed = true; unsubscribe(); rows.dispose(); runtime = undefined;},
  };
}

function HomeRowsCanvas({children}: {children: React.ReactNode}) {
  const theme = useTheme(), window = useWindowDimensions();
  const layout = shellLayout(window.width, window.height, theme.isTV);
  return <View pointerEvents="box-none" style={{position: 'absolute', inset: 0}}>
    {theme.isTV ? <View pointerEvents="box-none" style={{position: 'absolute', left: layout.left, top: layout.top, width: layout.width, height: layout.height,
      transformOrigin: 'top left', transform: [{scale: layout.scale}]}}>{children}</View> : children}
  </View>;
}

/** Own only actual row destinations. Retaining a title never replaces the
 * requested row, its sparse collection, sort/filter state or mounted grid. */
export function createNativeHomeRows(context: NativeShellBootstrapContext, reportError: (error: unknown) => void) {
  const rows = new Map<RowRoute, RowLifetime>();
  let alive = true, sequence = 0;
  const retained = () => alive && context.current()
    ? [...context.navigation.retainedRoutes(), context.navigation.getSnapshot().route] : [];
  const current = (source: RowLifetime) => alive && context.current() && rows.get(source.route) === source && context.navigation.getSnapshot().route === source.route;
  const reconcile = () => {
    const keep = new Set(retained());
    for (const [route, source] of rows) if (!keep.has(route)) {source.abort.abort(); source.actions.dispose(); source.controller.dispose(); rows.delete(route);}
    for (const route of keep) {
      if (route.kind !== 'home-see-all' || route.destination.type !== 'row' || rows.has(route)) continue;
      const controller = new NativeHomeRowController({api: context.api, rowId: route.destination.rowId, viewerFence: context.authority().viewerFence,
        current: () => alive && context.current() && rows.has(route),
        kinds: context.libraries().filter(library => !Platform.isTV || !hiddenReadingLibraryOnTV(library.kind)).map(library => ({id: library.id, libraryIds: [library.id]}))});
      const actions = createNativeEntryActions(context, {
        current: () => alive && context.current() && rows.has(route) && context.navigation.getSnapshot().route === route,
        contains: entry => controller.getSnapshot().phase === 'ready' && !controller.collection.getSnapshot().refreshing
          && controller.collection.residentItems().some(item => item.entry === entry),
        subscribe: listener => {const off = [controller.subscribe(listener), controller.collection.subscribe(listener)]; return () => off.forEach(stop => stop());},
      });
      rows.set(route, {route, controller, actions, abort: new AbortController(), key: ++sequence, labels: new Map(context.libraries().map(library => [library.id, library.name]))});
    }
  };
  const play = (source: RowLifetime, shuffle: boolean) => {
    if (!current(source)) return;
    const query = source.controller.wholeGroup();
    if (!query) return;
    const pending = new AbortController(), abort = () => pending.abort();
    source.abort.signal.addEventListener('abort', abort, {once: true});
    const off = context.navigation.subscribe(() => {if (!current(source)) pending.abort();});
    void context.playback.runtime.startGroup({query, shuffle, signal: pending.signal, isCurrent: () => current(source)}).catch(error => {
      if (!pending.signal.aborted && current(source)) reportError(error);
    }).finally(() => {
      source.abort.signal.removeEventListener('abort', abort); off();
    });
  };
  /** A recommendation's own Not interested mark, and the same field set back to false
   * with a fresh operation id for Undo. The row is re-read from the server afterwards,
   * so a title leaves or returns only when the server says so. */
  const markNotInterested = async (source: RowLifetime, entry: ContentEntry, value: boolean): Promise<boolean> => {
    if (!current(source)) return false;
    const original = context.navigation.getSnapshot(), pending = new AbortController();
    const live = () => !pending.signal.aborted && current(source) && context.navigation.getSnapshot() === original;
    const abort = () => pending.abort();
    source.abort.signal.addEventListener('abort', abort, {once: true});
    const off = context.navigation.subscribe(() => {if (!live()) abort();});
    try {
      const bytes = nativeRouteCrypto.random(16);
      const operationId = Array.from(bytes).map(byte => byte.toString(16).padStart(2, '0')).join('');
      await setNotInterested({request: (path, method, body, signal) => live()
        ? context.api.request(path, method, body, signal) : Promise.reject(Object.assign(new Error('cancelled'), {name: 'AbortError'}))},
        {itemId: notInterestedTarget(entry), notInterested: value, serverId: context.authority().serverId, operationId, signal: pending.signal});
      if (!live()) return false;
      void source.controller.load().catch(reportError);
      return true;
    } catch (error) {if (live()) reportError(error); return false;}
    finally {off(); source.abort.signal.removeEventListener('abort', abort);}
  };
  return {
    reconcile,
    owns: (route: NativeShellRoute) => rows.has(route as RowRoute),
    render: (runtime: NativeShellRuntime) => [...rows.values()].map(source => <RetainedHomeRow key={source.key} source={source} runtime={runtime} active={current(source)}
      onBack={() => {if (current(source)) context.navigation.back(context.authority());}}
      onPlay={shuffle => play(source, shuffle)}
      onOpen={(entry, entryCurrent) => {
        if (!current(source) || !entryCurrent()) return;
        const snapshot = context.navigation.getSnapshot();
        const operation = nativeHomeEntryLifetime(() => current(source) && context.navigation.getSnapshot() === snapshot && entryCurrent(),
          [context.navigation.subscribe, context.subscribeAuthority, source.controller.subscribe, source.controller.collection.subscribe], source.abort.signal);
        const fail = () => {if (operation.lifetime.isCurrent()) reportError(Object.assign(new Error('unavailable'), {code: 'unavailable'}));};
        if (entry.kind === 'song' || entry.kind === 'audiobook_file') {
          if (!entry.playback) {fail(); operation.dispose(); return;}
          operation.dispose();
          // E-tv-36 rule: checked current just above; a dispatched Play ends only with the authority.
          const dispatched = nativeDispatchedPlayLifetime(() => context.current(), context.subscribeAuthority, new AbortController().signal);
          void context.playback.start({entry, target: entry.playback, lifetime: dispatched.lifetime})
            .catch(error => reportDispatchedPlayFailure(error, () => context.current(), reportError)).finally(dispatched.dispose);
          return;
        }
        if (entry.kind === 'ebook' || entry.kind === 'comic') {
          const libraryId = entry.libraryId, kind = entry.kind === 'ebook' ? 'book' : 'comic';
          const resident = () => operation.lifetime.isCurrent() && context.libraries().some(library => library.id === libraryId && library.kind === kind);
          if (!libraryId || !resident()) {fail(); operation.dispose(); return;}
          void openNativeReadingBookEntry(context, {libraryId, entry, origin: {route: snapshot.route, revision: snapshot.revision}, resident, signal: operation.lifetime.signal})
            .then(opened => {if (!opened) fail();}).catch(error => {if (resident()) reportError(error);}).finally(operation.dispose);
          return;
        }
        if (!nativeEntryRouteSupported(entry)) fail();
        else context.navigation.open(nativeEntryRoute(entry), context.authority());
        operation.dispose();
      }}
      onPlayEntry={(entry, entryCurrent) => {
        if (!current(source) || !entryCurrent() || !entry.playback) return;
        const captured = context.navigation.getSnapshot();
        const operation = nativeHomeEntryLifetime(() => current(source) && context.navigation.getSnapshot() === captured && entryCurrent(),
          [context.navigation.subscribe, source.controller.subscribe, source.controller.collection.subscribe], source.abort.signal);
        void context.playback.start({entry, target: entry.playback, ...(entry.kind === 'chapter' && entry.playback.startSeconds !== undefined ? {startIntent: 'explicit' as const} : {}), lifetime: operation.lifetime})
          .catch(error => {if (operation.lifetime.isCurrent()) reportError(error);}).finally(operation.dispose);
      }}
      onNotInterested={(entry, value) => markNotInterested(source, entry, value)} kindLabel={id => source.labels.get(id)!}/>),
    dispose: () => {alive = false; for (const source of rows.values()) {source.abort.abort(); source.actions.dispose(); source.controller.dispose();} rows.clear();},
  };
}

nativeShellRoutes.registerRoute('home-see-all', createNativeHomeSeeAllRenderer);

function RetainedHomeRow({source, runtime, active, onBack, onPlay, onOpen, onPlayEntry, onNotInterested, kindLabel}: {
  source: RowLifetime; runtime: NativeShellRuntime; active: boolean;
  onBack: () => void; onPlay: (shuffle: boolean) => void;
  onOpen: React.ComponentProps<typeof HomeSeeAll>['onOpen']; onPlayEntry: React.ComponentProps<typeof HomeSeeAll>['onPlayEntry'];
  onNotInterested: (entry: ContentEntry, value: boolean) => Promise<boolean>; kindLabel: (id: string) => string;
}) {
  const registry = useFocusRegistry(), opener = useRef<FocusSnapshot | undefined>(undefined), release = useRef<(() => void) | undefined>(undefined), shown = useRef(active);
  const place = useNativeStackPlacement(source.route), stacked = useNativeStackHosted();
  const row = useSyncExternalStore(source.controller.subscribe, source.controller.getSnapshot).row;
  const recommendation = !!row && isRecommendationRow({id: row.id, kind: row.kind});
  const [undo, setUndo] = useState<Readonly<{entry: ContentEntry; id: string}>>();
  const displayedUndo = useRef(undo); displayedUndo.current = undo;
  const undoSequence = useRef(0), toastOpening = useRef(0), dockTop = useDockClearance();
  useEffect(() => {if (!active) {toastOpening.current++; setUndo(undefined);}}, [active]);
  useEffect(() => () => {toastOpening.current++;}, []);
  const [filters, setFilters] = useState(false);
  useEffect(() => {if (!active) return; const remember = () => {const value = registry.capture(); if (value?.regionId.startsWith('home-see-all:')) opener.current = value;};
    remember(); return registry.subscribe(remember);
  }, [active, registry]);
  useLayoutEffect(() => {
    if (shown.current && !active && opener.current && !release.current) release.current = registry.retain(opener.current);
    const restored = !shown.current && active; shown.current = active;
    if (!restored) return;
    release.current?.(); release.current = undefined;
    const frame = requestAnimationFrame(() => registry.restore(opener.current));
    return () => cancelAnimationFrame(frame);
  }, [active, registry]);
  useEffect(() => () => release.current?.(), []);
  return place(<View pointerEvents={active ? 'auto' : 'none'} accessibilityElementsHidden={!active} importantForAccessibility={active ? 'auto' : 'no-hide-descendants'}
    style={{position: 'absolute', inset: 0, opacity: active || stacked ? 1 : 0}}>
    <HomeSeeAll controller={source.controller} runtime={runtime} active={active && !filters} onBack={onBack} onPlay={onPlay} onOpen={onOpen} onPlayEntry={onPlayEntry}
      onMenu={(entry, anchor) => source.actions.menu(entry, anchor, undefined, recommendation ? [{id: 'not-interested', label: defaultI18n.t('entry.notInterested'), icon: 'i-eyeoff', onSelect: () => {
        const opening = ++toastOpening.current;
        displayedUndo.current = undefined;
        setUndo(undefined);
        void onNotInterested(entry, true).then(ok => {if (ok && shown.current && opening === toastOpening.current && !source.abort.signal.aborted)
          setUndo({entry, id: `home-not-interested:${source.key}:${++undoSequence.current}`});});
      }}] : undefined)}
      onFilters={() => setFilters(true)} kindLabel={kindLabel} onReshuffle={() => {if (active) try {const bytes = nativeRouteCrypto.random(4); const sort = source.controller.getSnapshot().sort;
        if (sort) source.controller.setSort({...sort, seed: new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength).getUint32(0)});} catch (error) {runtime.reportError(error);}}}/>
    {active && filters && <HomeRowFilters controller={source.controller} runtime={runtime} onClose={() => setFilters(false)}/>}
    <ToastHost testID="home-see-all-not-interested" obstructionTop={dockTop} toast={active && undo ? {
      id: undo.id, message: defaultI18n.t('entry.notInterestedDone', {title: undo.entry.displayTitle ?? undo.entry.title}), durationMs: 8000, actionOnTV: true,
      action: {label: defaultI18n.t('action.undo'), onPress: () => {
        if (!shown.current || source.abort.signal.aborted || displayedUndo.current !== undo) return;
        const entry = undo.entry; displayedUndo.current = undefined; toastOpening.current++; setUndo(undefined);
        void onNotInterested(entry, false).then(ok => {if (!ok && shown.current && !source.abort.signal.aborted) runtime.reportError(Object.assign(new Error(defaultI18n.t('entry.notInterestedUndoFailed', {title: entry.displayTitle ?? entry.title})), {code: 'not_interested_undo_failed'}));});
      }},
    } : null} onDismiss={id => {
      if (displayedUndo.current?.id === id) displayedUndo.current = undefined;
      setUndo(value => value?.id === id ? undefined : value);
    }}/>
  </View>);
}
