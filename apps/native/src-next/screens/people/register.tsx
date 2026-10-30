import {useNativeStackHosted, useNativeStackPlacement} from '../../kit/navigation/stack-slot';
import React, {useEffect, useState, useSyncExternalStore} from 'react';
import {View, useWindowDimensions} from 'react-native';
import {GridSkeleton, StateView} from '../../kit/states';
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

type PersonRoute = Extract<NativeShellRoute, {kind: 'person'}>;
type Entry = {
  route: PersonRoute; focusScope: string; source: NativePersonController; key: number; stop: () => void;
  snapshot?: NativePersonSnapshot; selection?: NativeSelection; window?: NativePersonCreditWindow; actions?: ReturnType<typeof createNativeEntryActions>;
};

/** This handheld module owns one source/window for each actual retained Person
 * history object. It never creates a player or captures a Home/library scope. */
export function createNativePersonRenderer(context: NativeShellBootstrapContext): NativeRouteRenderer {
  const sources = new Map<PersonRoute, Entry>();
  let disposed = false, sequence = 0;
  const releaseWindow = (entry: Entry) => {entry.selection?.dispose(); entry.selection = undefined; entry.actions?.dispose(); entry.actions = undefined; entry.window?.dispose(); entry.window = undefined;};
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
      entry.actions = createNativeEntryActions(context, {
        current: () => !disposed && entry.source.activation.getSnapshot().visible,
        contains: item => entry.source.contains(item) || window.contains(item),
        subscribe: listener => {
          const stops = [entry.source.subscribe(listener), window.collection.subscribe(listener)];
          return () => stops.forEach(stop => stop());
        },
      });
      entry.selection = createNativeSelection(context, {
        current: () => !disposed && entry.source.activation.getSnapshot().visible,
        subscribe: listener => {const stops = [entry.source.subscribe(listener), window.collection.subscribe(listener)]; return () => stops.forEach(stop => stop());},
        getSnapshot: () => {
          const page = entry.source.getSnapshot().page, viewer = context.session()?.viewer;
          const credits = window.collection.residentItems();
          return {context: page && viewer && (viewer.authority === 'local' || viewer.authority === 'hosted') ? {
            scope: {serverId: viewer.serverId, authority: viewer.authority, accountId: viewer.accountId, profileId: viewer.profileId,
              viewId: JSON.stringify(['native-person', entry.focusScope, entry.source.getSnapshot().group, page.person.revision]), privacyFence: page.viewerFence},
            revision: {catalogRevision: String(page.revision.catalog), viewerRevision: String(page.revision.viewer)}} : undefined,
            groups: [{id: 'known', status: 'success' as const, items: page?.person.knownFor ?? []},
              {id: 'credits', status: 'success' as const, positions: credits.map(credit => window.positionOf(credit)!), items: credits.map(credit => credit.media)}]};
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
  const theme = useTheme(), dimensions = useWindowDimensions();
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
      runtime={runtime} actions={entry.actions} selection={entry.selection} onRole={role => {void entry.source.select({role});}} onBack={back}/>
      : state.phase === 'error' ? <StateView variant="centered" {...runtime.error(state.error, () => {void entry.source.load();})}/>
      : <GridSkeleton elapsedMs={elapsed} scrollOffset={0} viewportHeight={dimensions.height} width={dimensions.width} totalCount={9} columns={3}/>}
    {state.phase !== 'ready' && <Focusable testID="person-pending-back" accessibilityLabel={defaultI18n.t('action.back')} onPress={back} style={{position: 'absolute', top: 20, left: 16, width: 40, height: 40, justifyContent: 'center', alignItems: 'center'}}><Icon name="i-left" size={22} color={theme.colors.text}/></Focusable>}
  </View>);
}

nativeShellRoutes.registerRoute('person', createNativePersonRenderer);
