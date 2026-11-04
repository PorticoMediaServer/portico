import {useNativeStackHosted, useNativeStackPlacement} from '../../kit/navigation/stack-slot';
import React, {useEffect, useState, useSyncExternalStore} from 'react';
import {Platform, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {GridSkeleton} from '../../kit/states';
import {ScreenErrorPage} from '../native-screen-services';
import {Focusable} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {useTheme} from '../../kit/theme';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeShellRoute} from '../../shell/routes';
import {nativeShellRoutes, type NativeRouteRenderer} from '../../shell/route-registry';
import {createNativeSelection, type NativeSelection} from '../selection/native-selection';
import {createNativeEntryActions} from '../destinations/entry-actions';
import {createNativePersonController, type NativePersonController, type NativePersonSnapshot} from './controller';
import {createNativePersonCreditWindow, type NativePersonCreditWindow} from './credit-window';
import {PersonScreen} from './PersonScreen';
import {authorityKey} from '../../shell/model';
import {StandardBackdrop} from '../../kit/artwork';
import {usePageWidth} from '../../kit/layout/page-inset';

type PersonRoute = Extract<NativeShellRoute, {kind: 'person'}>;
type Entry = {
  route: PersonRoute; focusScope: string; source: NativePersonController; key: number; stop: () => void;
  snapshot?: NativePersonSnapshot; selection?: NativeSelection; window?: NativePersonCreditWindow;
  /** TV: one window per department of the all-credits page, drawn as a row each (tv-detail frame 10). */
  groupWindows?: readonly Readonly<{id: string; department: string; total: number; window: NativePersonCreditWindow}>[]; actions?: ReturnType<typeof createNativeEntryActions>;
};

/** This handheld module owns one source/window for each actual retained Person
 * history object. It never creates a player or captures a Home/library scope. */
export function createNativePersonRenderer(context: NativeShellBootstrapContext): NativeRouteRenderer {
  const sources = new Map<PersonRoute, Entry>();
  let disposed = false, sequence = 0;
  // A new source snapshot releases the window with its selection and entry actions: after a re-read (access decided
  // again) a multi-select in progress or an open credit ⋯ is dropped with the page it was made on, by design.
  const releaseWindow = (entry: Entry) => {entry.selection?.dispose(); entry.selection = undefined; entry.actions?.dispose(); entry.actions = undefined; entry.window?.dispose(); entry.window = undefined;
    entry.groupWindows?.forEach(group => group.window.dispose()); entry.groupWindows = undefined;};
  const release = (entry: Entry) => {entry.stop(); releaseWindow(entry); entry.source.dispose();};
  const reconcile = () => {
    const routes = !disposed && context.current() ? [...context.navigation.retainedRoutes(), context.navigation.getSnapshot().route] : [];
    const keep = new Set(routes);
    for (const [route, entry] of sources) if (!keep.has(route)) {sources.delete(route); release(entry);}
    for (const route of routes) {
      if (route.kind !== 'person' || sources.has(route)) continue;
      const source = createNativePersonController(context, route);
      const entry: Entry = {route, focusScope: `person:${authorityKey(context.authority())}:${JSON.stringify(route)}:${sequence + 1}`, source, key: ++sequence, stop: () => {}};
      sources.set(route, entry); entry.stop = source.subscribe(reconcile);
    }
    for (const entry of sources.values()) {
      const snapshot = entry.source.getSnapshot();
      if (entry.snapshot !== snapshot) {releaseWindow(entry); entry.snapshot = snapshot;}
      if (snapshot.phase !== 'ready' || snapshot.pageOrigin !== 'initial' || entry.window || context.navigation.getSnapshot().route !== entry.route) continue;
      const window = createNativePersonCreditWindow(context, entry.route, entry.source); entry.window = window;
      const departments = Platform.isTV && 'role' in snapshot.group && snapshot.group.role === 'all' ? (snapshot.page?.groups ?? []).flatMap(group => group.department && group.total > 0 ? [{id: group.id, department: group.department, total: group.total}] : []) : [];
      entry.groupWindows = departments.map(group => ({...group, window: createNativePersonCreditWindow(context, entry.route, entry.source, group)}));
      entry.actions = createNativeEntryActions(context, {
        current: () => !disposed && entry.source.activation.getSnapshot().visible,
        contains: item => entry.source.contains(item) || window.contains(item) || !!entry.groupWindows?.some(group => group.window.contains(item)),
        subscribe: listener => {
          const stops = [entry.source.subscribe(listener), window.collection.subscribe(listener), ...(entry.groupWindows ?? []).map(group => group.window.collection.subscribe(listener))];
          return () => stops.forEach(stop => stop());
        },
      });
      entry.selection = createNativeSelection(context, {
        current: () => !disposed && entry.source.activation.getSnapshot().visible,
        subscribe: listener => {const stops = [entry.source.subscribe(listener), window.collection.subscribe(listener), ...(entry.groupWindows ?? []).map(group => group.window.collection.subscribe(listener))]; return () => stops.forEach(stop => stop());},
        getSnapshot: () => {
          const page = entry.source.getSnapshot().page, viewer = context.session()?.viewer;
          // Department rows replace the flat row: the resident credits are the rows' own, ordered row by row.
          const rows = entry.groupWindows?.length ? entry.groupWindows : undefined;
          const credits = rows ? rows.flatMap((group, row) => group.window.collection.residentItems().map(credit => ({credit, position: row * 1_000_000 + group.window.positionOf(credit)!})))
            : window.collection.residentItems().map(credit => ({credit, position: window.positionOf(credit)!}));
          return {context: page && viewer && (viewer.authority === 'local' || viewer.authority === 'hosted') ? {
            scope: {serverId: viewer.serverId, authority: viewer.authority, accountId: viewer.accountId, profileId: viewer.profileId,
              viewId: JSON.stringify(['native-person', entry.focusScope, entry.source.getSnapshot().group, page.person.revision]), privacyFence: page.viewerFence},
            revision: {catalogRevision: String(page.revision.catalog), viewerRevision: String(page.revision.viewer)}} : undefined,
            groups: [{id: 'known', status: 'success' as const, items: page?.person.knownFor ?? []},
              {id: 'credits', status: 'success' as const, positions: credits.map(value => value.position), items: credits.map(value => value.credit.media)}]};
        },
      }, {area: 'search', aggregates: true, preserveEvicted: true});
    }
  };
  const stops = [context.navigation.subscribe(reconcile), context.subscribeAuthority(reconcile)];
  return {
    render: (route, _runtime) => {if (route.kind === 'person') reconcile(); return null;},
    renderRetained: runtime => {
      reconcile();
      return [...sources.values()].map(entry => <PersonHost key={entry.key} entry={entry} runtime={runtime}
        back={() => {if (context.current() && context.navigation.getSnapshot().route === entry.route) context.navigation.back(context.authority());}}/>);
    },
    dispose: () => {if (disposed) return; disposed = true; stops.forEach(stop => stop()); for (const entry of sources.values()) release(entry); sources.clear();},
  };
}

function PersonHost({entry, runtime, back}: {entry: Entry; runtime: NativeShellRuntime; back: () => void}) {
  const theme = useTheme(), dimensions = useWindowDimensions(), insets = useSafeAreaInsets(), pageWidth = usePageWidth();
  const place = useNativeStackPlacement(entry.route), stacked = useNativeStackHosted();
  const state = useSyncExternalStore(entry.source.subscribe, entry.source.getSnapshot);
  const active = useSyncExternalStore(entry.source.activation.subscribe, entry.source.activation.getSnapshot);
  const [elapsed, setElapsed] = useState(0);
  useEffect(() => {if (active.visible && state.phase === 'idle') void entry.source.load();}, [active.visible, entry.source, state.phase]);
  useEffect(() => {
    if (state.phase !== 'loading') {setElapsed(0); return;}
    const start = Date.now(), timer = setTimeout(() => setElapsed(Math.max(300, Date.now() - start)), 300);
    return () => clearTimeout(timer);
  }, [state.phase]);
  return place(<View pointerEvents={active.visible ? 'auto' : 'none'} accessibilityElementsHidden={!active.visible} importantForAccessibility={active.visible ? 'auto' : 'no-hide-descendants'}
    style={{position: 'absolute', inset: 0, display: active.visible || stacked ? 'flex' : 'none'}}><StandardBackdrop/>
    {state.phase === 'ready' && state.page && entry.window && entry.actions && 'role' in state.group ? <PersonScreen focusScope={entry.focusScope} page={state.page} role={state.group.role} collection={entry.window.collection}
      runtime={runtime} actions={entry.actions} selection={entry.selection} groups={entry.groupWindows?.map(group => ({id: group.id, department: group.department, total: group.total, collection: group.window.collection}))} onRole={role => {void entry.source.select({role});}} onBack={back}/>
      // W-2 sweep: the full-page state, below Back and the status bar, as a title that couldn't load.
      : state.phase === 'error' ? <ScreenErrorPage {...runtime.error(state.error, () => {void entry.source.load();})}/>
      : <GridSkeleton elapsedMs={elapsed} scrollOffset={0} viewportHeight={dimensions.height} width={pageWidth} totalCount={9} columns={3}/>}
    {state.phase !== 'ready' && <Focusable testID="person-pending-back" accessibilityLabel={defaultI18n.t('action.back')} onPress={back} style={{position: 'absolute', top: insets.top + 8, left: 16, width: 40, height: 40, justifyContent: 'center', alignItems: 'center'}}><Icon name="i-left" size={22} color={theme.colors.text}/></Focusable>}
  </View>);
}

nativeShellRoutes.registerRoute('person', createNativePersonRenderer);
