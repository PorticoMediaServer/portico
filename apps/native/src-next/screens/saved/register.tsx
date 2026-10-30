import {useNativeStackPlacement} from '../../kit/navigation/stack-slot';
import React, {useEffect, useLayoutEffect, useMemo, useSyncExternalStore} from 'react';
import {Platform, View, useWindowDimensions} from 'react-native';
import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src';
import type {Library} from '../../../../../packages/client-core/src/index';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {shellLayout} from '../../shell/model';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeShellRoute} from '../../shell/routes';
import {nativeShellRoutes, type NativeRouteRenderer} from '../../shell/route-registry';
import {useTheme} from '../../kit/theme';
import {nativeDestinationLifetime} from '../destinations/lifetime';
import {SavedScreen} from './SavedScreen';
import {SavedDirectoryScreen, type NativeSavedDirectoryRoute} from './SavedDirectoryScreen';
import {SavedListScreen, type NativeSavedListRoute} from './SavedListScreen';
import {SavedResourceScreen} from './SavedResourceScreen';
import {createNativeSavedState} from './state';
import {nativeSavedRouteTarget, openNativeSavedView, type NativeSavedRouteTarget} from './navigation';
import type {NativeSavedDownloadsCopy} from './SavedDownloadCard';
import {nativeScreenServices} from '../native-screen-services';
import {createNativeSavedSelection, createNativeSavedViewSelection} from './selection';
import {NativeSavedViewDestination} from './view-model';
import {createNativeEntryActions} from '../destinations/entry-actions';
import type {SavedRoute} from '../../../../../packages/client-core/src/saved';
import {createNativeDownloadsView} from '../downloads/view';
import {NativeHomeSavedViewSummaries} from '../home/saved-view-summaries';
import {createNativeSavedDirectoryCommands} from './directory-commands';
import {createNativeSavedViewSummaryOwner} from './view-summary-owner';

const savedDownloadsCopy: NativeSavedDownloadsCopy = Object.freeze({downloading: 'next.saved.phone.downloading'});
const onThisPhone: MessageId = 'next.saved.phone.onThisPhone';

type RouteView = NonNullable<ReturnType<ReturnType<typeof createNativeSavedState>['view']>>;
type Entry = Readonly<{route: NativeSavedRouteTarget; key: number; view: RouteView}>;

/** The one Saved registration. TV's destination chrome is registered by the
 * TV-more owner; the pushed routes are this screen family's and register here
 * for every platform. */
export function createNativeSavedModule(context: NativeShellBootstrapContext): NativeRouteRenderer {
  const lifetime = nativeDestinationLifetime(context), state = createNativeSavedState(context), services = nativeScreenServices(context);
  let downloads: ReturnType<typeof createNativeDownloadsView> | undefined;
  const inlineDownloads = () => downloads ??= createNativeDownloadsView(context);
  const views = new Map<NativeSavedRouteTarget, Entry>();
  let selection: ReturnType<typeof createNativeSavedSelection> | undefined;
  const savedSelection = () => selection ??= createNativeSavedSelection(context, state.source, state.activation);
  let disposed = false, sequence = 0;
  const current = () => !disposed && lifetime.current();
  const retained = () => current() ? [...context.navigation.retainedRoutes(), context.navigation.getSnapshot().route] : [];
  const isSavedTarget = (route: NativeShellRoute): route is NativeSavedRouteTarget => route.kind === 'saved-view' || route.kind === 'saved-destination';
  const reconcile = () => {
    if (!current()) return;
    const keep = new Set(retained().filter(isSavedTarget));
    for (const [route, entry] of views) if (!keep.has(entry.route)) {views.delete(route); entry.view.dispose();}
    for (const route of keep) {
      if (views.has(route)) continue;
      const view = state.view(route);
      if (!view) continue;
      views.set(route, {route, key: ++sequence, view});
      void view.load().catch(error => {if (current()) services.reportError(error);});
    }
  };
  const changed = () => {
    if (!current()) return;
    void state.source.load().catch(error => {if (current()) services.reportError(error);});
    for (const entry of views.values()) void entry.view.source.refresh().catch((error: unknown) => {if (current()) services.reportError(error);});
  };
  const back = () => {if (current()) context.navigation.back(context.authority());};
  const open = (route: Parameters<typeof openNativeSavedView>[1]) => {if (current()) openNativeSavedView(context, route);};
  return {
    render: (route, runtime) => {
      if (!current()) return null;
      if (route.kind !== 'shell' || route.destination.kind !== 'saved') return null;
      if (Platform.isTV) return null;
      return <SavedScreen destination={state.source} runtime={runtime} device={state.offline} actions={state.actions} selection={savedSelection()}
        downloadsCopy={savedDownloadsCopy} onThisPhone={onThisPhone} activation={state.activation}
        downloads={Platform.isTV ? undefined : value => inlineDownloads().render(value, true)}
        tabBody={(tab, value) => <SavedInlineHost key={tab} tab={tab} context={context} runtime={value} activation={state.activation} open={open} changed={changed}/>}/>;
    },
    renderRetained: runtime => {
      if (!current()) return null;
      reconcile();
      const hosts = <>{[...views.values()].map(entry => <SavedRouteHost key={entry.key} entry={entry} runtime={runtime} libraries={context.libraries()}
        open={open} back={back} changed={changed} showOnHome={state.actions.showOnHome} openInLibrary={state.actions.openInLibrary} context={context}/>)}</>;
      return <SavedRoutesCanvas>{hosts}</SavedRoutesCanvas>;
    },
    dispose: () => {
      if (disposed) return;
      disposed = true;
      selection?.dispose(); selection = undefined;
      for (const entry of views.values()) entry.view.dispose();
      views.clear(); state.dispose(); downloads?.dispose();
    },
  };
}

/** Each tab body has a separate canonical Saved service. The shell route remains
 * the actual Saved destination, so its tabs and sidebar stay mounted. Unmounting a
 * tab retires its command/selection openings before a successor body can act. */
function SavedInlineHost({tab, context, runtime, activation, open, changed}: {
  tab: 'favorites' | 'playlists' | 'collections' | 'views' | 'history'; context: NativeShellBootstrapContext;
  runtime: NativeShellRuntime; activation: import('../destinations/focus').NativeDestinationActivationPort;
  open: (route: SavedRoute) => void; changed: () => void;
}) {
  const owner = useMemo(() => {
    const lifetime = nativeDestinationLifetime(context), feedback = nativeScreenServices(context), route = context.navigation.getSnapshot().route;
    let disposed = false;
    const retained = () => !disposed && lifetime.current();
    const current = () => retained() && activation.getSnapshot().visible && context.navigation.getSnapshot().route === route;
    const source = new NativeSavedViewDestination({api: context.api, scope: lifetime.scope, requestId: feedback.requestId, current: retained});
    const actions = createNativeEntryActions(context, {current, contains: source.contains, subscribe: source.subscribe});
    const selection = createNativeSavedViewSelection(context, source, activation);
    const commands = createNativeSavedDirectoryCommands(context, route, activation);
    const summaries = tab === 'views' ? createNativeSavedViewSummaryOwner(context, source, retained) : undefined;
    return {source, actions, selection, commands, summaries, current,
      dispose: () => {disposed = true; selection.dispose(); commands.dispose(); summaries?.dispose(); actions.dispose(); source.dispose();}};
  }, [activation, context, tab]);
  useLayoutEffect(() => () => owner.dispose(), [owner]);
  const route = useMemo(() => ({view: tab}), [tab]);
  const common = {destination: owner.source, activation, runtime, actions: owner.actions, inline: true, back: () => {}};
  return tab === 'favorites' || tab === 'history'
    ? <SavedListScreen {...common} route={route as NativeSavedListRoute} selection={owner.selection}/>
    : <SavedDirectoryScreen {...common} route={route as NativeSavedDirectoryRoute} libraries={context.libraries()} open={open} changed={changed}
        commands={owner.commands} summaries={owner.summaries} routeCurrent={owner.current}/>;
}

function SavedResourceHost({context, entry, route, runtime, libraries, back, changed, showOnHome, openInLibrary}: {
  context: NativeShellBootstrapContext; entry: Entry; route: ReturnType<typeof nativeSavedRouteTarget>; runtime: NativeShellRuntime;
  libraries: readonly Library[]; back: () => void; changed: () => void;
  showOnHome?: (resource: import('../../../../../packages/client-core/src/personal-saved').PersonalResource) => void;
  openInLibrary?: (resource: import('../../../../../packages/client-core/src/personal-saved').PersonalResource) => void;
}) {
  const active = useSyncExternalStore(entry.view.activation.subscribe, entry.view.activation.getSnapshot);
  const source = useSyncExternalStore(entry.view.source.subscribe, entry.view.source.getSnapshot);
  const resource = source.personal?.resource;
  const {revision: activationRevision, intent: activationIntent, visible: activationVisible} = active;
  // The reader belongs to the exact rendered activation. Away-to-Back retires and
  // aborts the old capability read; the fresh activation gets its own reader and read.
  const reader = useMemo(() => new NativeHomeSavedViewSummaries({api: context.api, serverId: context.authority().serverId,
    current: () => {
      if (!context.current() || !activationVisible) return false;
      const now = entry.view.activation.getSnapshot();
      return now.visible && now.revision === activationRevision && now.intent === activationIntent;
    }, libraryIds: () => context.libraries().map(value => value.id)}), [activationIntent, activationRevision, activationVisible, context, entry]);
  useSyncExternalStore(reader.subscribe, reader.getSnapshot);
  useEffect(() => {if (activationVisible && resource) void reader.load([resource]);}, [activationIntent, activationRevision, activationVisible, reader, resource]);
  useEffect(() => () => reader.dispose(), [reader]);
  const labels = {t: runtime.text.t, has: defaultI18n.has, locale: runtime.text.region?.uiLocale ?? defaultI18n.locale};
  const condition = activationVisible && resource ? reader.line(resource, labels) : undefined;
  return <SavedResourceScreen destination={entry.view.source} activation={entry.view.activation} runtime={runtime} actions={entry.view.actions}
    route={route} libraries={libraries} back={back} changed={changed} selection={entry.view.selection} showOnHome={showOnHome} openInLibrary={openInLibrary} condition={condition}/>;
}

function SavedRoutesCanvas({children}: {children: React.ReactNode}) {
  const theme = useTheme(), window = useWindowDimensions(), layout = shellLayout(window.width, window.height, theme.isTV);
  return <View pointerEvents="box-none" style={{position: 'absolute', inset: 0}}>
    {theme.isTV ? <View pointerEvents="box-none" style={{position: 'absolute', left: layout.left, top: layout.top, width: layout.width, height: layout.height,
      transformOrigin: 'top left', transform: [{scale: layout.scale}]}}>{children}</View> : children}
  </View>;
}

function SavedRouteHost({entry, runtime, libraries, open, back, changed, showOnHome, openInLibrary, context}: {
  entry: Entry; runtime: NativeShellRuntime; libraries: readonly Library[];
  open: (route: Parameters<typeof openNativeSavedView>[1]) => void; back: () => void; changed: () => void;
  showOnHome?: (resource: import('../../../../../packages/client-core/src/personal-saved').PersonalResource) => void;
  openInLibrary?: (resource: import('../../../../../packages/client-core/src/personal-saved').PersonalResource) => void;
  context: NativeShellBootstrapContext;
}) {
  const active = useSyncExternalStore(entry.view.activation.subscribe, entry.view.activation.getSnapshot);
  const route = useMemo(() => nativeSavedRouteTarget(entry.route), [entry.route]);
  const place = useNativeStackPlacement(entry.route);
  const commands = useMemo(() => createNativeSavedDirectoryCommands(context, entry.route, entry.view.activation), [context, entry]);
  useLayoutEffect(() => () => commands.dispose(), [commands]);
  const common = {destination: entry.view.source, activation: entry.view.activation, runtime};
  const body = route.view === 'playlists' || route.view === 'collections' || route.view === 'views'
    ? <SavedDirectoryScreen {...common} route={route as NativeSavedDirectoryRoute} actions={entry.view.actions} libraries={libraries} back={back} open={open} changed={changed}
        summaries={'summaries' in entry.view ? entry.view.summaries : undefined} commands={commands}
        routeCurrent={() => context.current() && context.navigation.getSnapshot().route === entry.route}/>
    : route.view === 'watchlist' || route.view === 'favorites' || route.view === 'history'
      ? <SavedListScreen {...common} route={route as NativeSavedListRoute} actions={entry.view.actions} back={back} changed={changed} selection={entry.view.selection}/>
      : <SavedResourceHost context={context} entry={entry} route={route} runtime={runtime} libraries={libraries} back={back} changed={changed} showOnHome={showOnHome} openInLibrary={openInLibrary}/>;
  return place(<View pointerEvents={active.visible ? 'auto' : 'none'} accessibilityElementsHidden={!active.visible} importantForAccessibility={active.visible ? 'auto' : 'no-hide-descendants'}
    style={{position: 'absolute', inset: 0, backgroundColor: 'transparent'}}>{body}</View>);
}

// The phone hub is the only destination this family registers; the TV landing
// belongs to the TV-more owner, so a single platform registration avoids two
// factories for one shell kind.
if (!Platform.isTV) nativeShellRoutes.registerDestination('saved', createNativeSavedModule);
nativeShellRoutes.registerRoute('saved-view', createNativeSavedModule);
nativeShellRoutes.registerRoute('saved-destination', createNativeSavedModule);
