import React from 'react';
import {Platform} from 'react-native';
import type {BrowseLabels} from '../kit/browse';
import type {NativeShellBootstrapContext} from './bootstrap';
import {sameAuthority} from './model';
import type {NativeRouteRenderer} from './route-registry';
import type {NativeDestinationActivationPort} from '../screens/destinations/focus';
import {nativeDestinationLifetime} from '../screens/destinations/lifetime';
import type {NativeSelection} from './native-selection';
import {MusicMixesRow} from '../kit/music/MusicMixesRow';
import {CoveredLayer} from '../kit/focus';
import {homeSeeAllRoute} from './routes';
import {LibraryPage} from '../pages/touch/library/LibraryPage';
import {LibraryPage as TVLibraryPage} from '../pages/tv/library/LibraryPage';
import type {LibraryProps} from '../pages/shared/library/useLibrary';
import type {NativeLibraryActions, NativeLibrarySelectionPort} from '../screens/library/types';
import {NativeLibraryDestination} from '../screens/library/model';
import {NativeLibraryDirectory} from '../screens/library/directory';
import {hiddenReadingLibraryOnTV, isReadingLibraryKind} from '../screens/read/platform';
import {libraryTab, libraryTabs, type LibraryTabId} from '../../../../packages/client-core/src/presentation/library-tabs';
import {BooksPage} from '../pages/touch/reading/BooksPage';
import {createNativeLibraryPlaySelection} from '../screens/library/play-selection';
import {createNativeDiscoverSelection} from '../screens/library/discover-selection';
import {createNativeTVLibraryNamingStore, nativeTVLibraryActions, nativeTVNamingPanelId} from '../screens/library/tv-library';
import {NativeTVLibrarySurface} from '../kit/library/TVSaveViewPanel';
import type {NativeTVLibraryNamingStore} from '../screens/library/tv-library';
import {nativeLibraryOpening, nativeLibraryOpeningRoute} from '../screens/library/opening';
import {takeLibrarySavedView} from '../screens/library/saved-view-intent';
import {observeFirstScan, type NativeLibraryFirstScanPort} from '../screens/library/first-scan';
import {viewerQueriesFor} from './freshness';

export type NativeLibraryRendererPorts = Readonly<{
  activation: (libraryId: string) => NativeDestinationActivationPort;
  labels: BrowseLabels;
  actions: NativeLibraryActions | ((destination: NativeLibraryDestination) => NativeLibraryActions);
  /** Only a genuine nonHome execution port enables selection. */
  selection?: (destination: NativeLibraryDestination) => NativeLibrarySelectionPort;
}>;

/** Accounts composes this descriptor into its ONE shell dispatcher. A removed
 * ID returns no screen here; the shell's authorized overview handles it. */
export function createNativeLibraryRenderer(context: NativeShellBootstrapContext, ports: NativeLibraryRendererPorts): NativeRouteRenderer & {dispose: () => void} {
  const lifetime = nativeDestinationLifetime(context);
  let disposed = false, selected: Readonly<{id: string; kind: string; name: string; tab?: LibraryTabId; opening: object; destination: NativeLibraryDestination; actions: NativeLibraryActions; selection?: NativeLibrarySelectionPort; shelfSelection?: NativeSelection}> | undefined;
  // TV owns exactly one extra surface for a Library route: the F-LIB-52 Save
  // view name keyboard. Other platforms keep their own sheet unchanged.
  let tvNaming: NativeTVLibraryNamingStore | undefined;
  const tvNamingStore = (): NativeTVLibraryNamingStore => tvNaming ??= createNativeTVLibraryNamingStore(context);
  const retire = (value: typeof selected) => {value?.shelfSelection?.dispose(); value?.actions.dispose?.(); value?.destination.dispose(); if (selected === value) selected = undefined;};
  const retireSelected = () => retire(selected);
  // Justin's Libraries rule (21 Oct, frame phone-app4): the Libraries tab returns to the
  // library last opened "on the pivot you left". The shell keeps one library destination
  // alive, so opening another library disposes the first and would lose its pivot. This
  // keeps only the admitted tab id per library, for this renderer's own lifetime: no
  // controller, query, filter, scroll position or preference, and nothing across a sign-in.
  const pivots = new Map<string, Readonly<{kind: string; tab: LibraryTabId}>>();
  /** Leave the current destination, remembering its pivot first when the next library differs.
   * Only a library the current render still authorizes, on a pivot it still offers, is kept. */
  const leaveSelected = (nextId: string, authorized: readonly {id: string; kind: string}[]) => {
    const departing = selected;
    if (departing && departing.id !== nextId && current()) {
      const library = authorized.find(value => value.id === departing.id);
      if (library && library.kind === departing.kind) {
        const snapshot = departing.destination.getSnapshot();
        // Idle means not admitted yet or already disposed; neither attests a pivot.
        if (selected === departing && current() && snapshot.phase !== 'idle'
          && libraryTabs(departing.kind).some(value => value.id === snapshot.tab.id)) {
          pivots.set(departing.id, {kind: departing.kind, tab: snapshot.tab.id});
        }
      }
    }
    retire(departing);
  };
  /** Only a pivot the current, authorized library of that exact kind still offers survives. */
  const prunePivots = (libraries: readonly {id: string; kind: string}[]) => {
    if (!pivots.size) return;
    const kinds = new Map(libraries.map(value => [value.id, value.kind]));
    for (const [libraryId, remembered] of pivots) {
      const kind = kinds.get(libraryId);
      if (!kind || kind !== remembered.kind || !libraryTabs(kind).some(value => value.id === remembered.tab)) pivots.delete(libraryId);
    }
  };
  /** A constructor seed only, for a library being newly opened, never a reconciliation input. */
  const rememberedPivot = (library: {id: string; kind: string}) => {
    const remembered = pivots.get(library.id);
    return remembered && remembered.kind === library.kind ? libraryTabs(library.kind).find(value => value.id === remembered.tab) : undefined;
  };
  const current = () => !disposed && lifetime.current();
  const directory = new NativeLibraryDirectory({api: context.api, current});
  // An owner's empty library reads its own scan history (as web does) and can open server activity:
  // the Dashboard, or Server settings on TV. Members never get the port.
  let firstScan: NativeLibraryFirstScanPort | undefined;
  const ownerFirstScan = (): NativeLibraryFirstScanPort => firstScan ??= {
    observe: (libraryId, onChange) => observeFirstScan({api: context.api, scope: lifetime.scope, queries: viewerQueriesFor(context.api, {serverId: lifetime.scope.serverId})}, libraryId, onChange),
    openActivity: () => {if (current()) context.navigation.open(Platform.isTV ? {kind: 'server-settings'} : {kind: 'server-dashboard'}, context.authority());},
  };
  return {
    render(route, runtime) {
      if (!current() || route.kind !== 'shell' || route.destination.kind !== 'library') return null;
      const libraries = context.libraries(), id = route.destination.libraryId, library = libraries.find(value => value.id === id);
      prunePivots(libraries);
      if (!library || Platform.isTV && hiddenReadingLibraryOnTV(library.kind)) {leaveSelected(id, libraries); return null;}
      // Book and comic libraries have no browse pivot: their authored producer is
      // the bounded Reading list (`GET /v1/reading/items`), not generic content.
      if (isReadingLibraryKind(library.kind)) {
        leaveSelected(id, libraries);
        return <BooksPage key={id} context={context} runtime={runtime} libraryId={id} title={library.name} labels={ports.labels}
          onBack={() => runtime.onDestination({kind: 'libraries'})}/>;
      }
      const savedIntent = takeLibrarySavedView(nativeLibraryOpeningRoute(context, route) ?? route, id, context.authority(), context.session()?.sessionFamilyId);
      // The route's own tab is the only tab metadata that takes part in reconciliation; a
      // remembered pivot seeds a new destination and can never force a rebuild or replace
      // the saved view's own opening.
      const routeTab = route.destination.tab ? libraryTab(library.kind, route.destination.tab) : undefined;
      if (savedIntent || selected?.id !== id || selected.kind !== library.kind || selected.name !== library.name
        || selected.tab !== routeTab?.id || routeTab && selected.opening !== route.destination) {
        leaveSelected(id, libraries);
        const initial = savedIntent ?? (route.destination.definition ? nativeLibraryOpening(context, route, route.destination.definition) : undefined);
        if (route.destination.definition && !initial) return null;
        // Resolved only after the saved definition is admitted: an explicit route tab or a
        // saved opening wins, and an unoffered pivot leaves the destination's own default.
        const seed = routeTab ?? (initial ? undefined : rememberedPivot(library));
        const destination = new NativeLibraryDestination({api: context.api, scope: lifetime.scope, library, ...(seed ? {tab: seed} : {}),
          ...(initial ? {initial} : {}),
          current: () => current() && context.libraries().some(value => value.id === id && value.kind === library.kind)
            && (!initial || sameAuthority(initial.authority, context.authority()) && context.session()?.sessionFamilyId === initial.sessionFamilyId
              && (context.navigation.getSnapshot().route === initial.route || context.navigation.retainedRoutes().includes(initial.route)))});
        const actions = typeof ports.actions === 'function' ? ports.actions(destination) : ports.actions;
        const shelfSelection = createNativeDiscoverSelection(context, {subscribe: destination.subscribe, getSnapshot: destination.getSnapshot},
          () => current() && context.navigation.getSnapshot().route === route);
        selected = {id, kind: library.kind, name: library.name, opening: route.destination, ...(routeTab ? {tab: routeTab.id} : {}), destination,
          actions: Platform.isTV ? nativeTVLibraryActions(actions, tvNamingStore(), destination) : actions,
          selection: ports.selection?.(destination), shelfSelection};
      }
      const page = <LibrarySurface key={id} destination={selected.destination} runtime={runtime} directory={directory} libraries={context.libraries()}
        activation={ports.activation(id)} labels={ports.labels} actions={selected.actions} selection={selected.selection} shelfSelection={selected.shelfSelection}
        owner={context.session()?.viewer.role === 'owner'} firstScan={context.session()?.viewer.role === 'owner' ? ownerFirstScan() : undefined} mixAnchor={route.destination.anchor === 'mixes'}
        onSeeAll={(target, title) => {if (current()) context.navigation.open(homeSeeAllRoute(target, title), context.authority());}}/>;
      return tvNaming ? <NativeTVLibrarySurface key={id} library={page} naming={tvNaming} panelId={nativeTVNamingPanelId(context.authority(), id)}/> : page;
    },
    dispose() {if (disposed) return; disposed = true; retireSelected(); pivots.clear(); tvNaming?.dispose(); tvNaming = undefined; directory.dispose();},
  };
}

export type {NativeLibraryActions, NativeLibrarySelectionPort} from '../screens/library/types';
export type {NativeLibraryViewDraft} from '../screens/library/model';
export {createNativeLibraryPlaySelection};

function LibrarySurface(props: LibraryProps) {
  const active = React.useSyncExternalStore(props.activation.subscribe, props.activation.getSnapshot);
  const state = React.useSyncExternalStore(props.destination.subscribe, props.destination.getSnapshot);
  if (Platform.isTV) return <TVLibraryPage {...props}/>;
  return <CoveredLayer covered={!active.visible} testID={`native-library-${encodeURIComponent(state.library.id)}`} style={{flex: 1}}><LibraryPage {...props} mixes={state.library.kind === 'music' && props.runtime.mixes ? events =>
    <MusicMixesRow {...events} port={props.runtime.mixes!} artwork={props.runtime.artwork} reportError={props.runtime.reportError}
      active={active.visible} focusScope={`library:${state.library.id}.mixes`}/> : undefined}/></CoveredLayer>;
}
