import React from 'react';
import {Platform} from 'react-native';
import type {BrowseLabels} from '../../kit/browse';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {sameAuthority} from '../../shell/model';
import type {NativeRouteRenderer} from '../../shell/route-registry';
import type {NativeDestinationActivationPort} from '../destinations/focus';
import {nativeDestinationLifetime} from '../destinations/lifetime';
import {LibraryScreen, type NativeLibraryActions, type NativeLibrarySelectionPort} from './LibraryScreen';
import {NativeLibraryDestination} from './model';
import {NativeLibraryDirectory} from './directory';
import {hiddenReadingLibraryOnTV, isReadingLibraryKind} from '../read/platform';
import {libraryTab, libraryTabs, type LibraryTabId} from '../../../../../packages/client-core/src/presentation/library-tabs';
import {NativeReadingLibraryScreen} from '../read/ReadingLibrary';
import {createNativeLibraryPlaySelection} from './play-selection';
import {createNativeTVLibraryNamingStore, nativeTVLibraryActions, nativeTVNamingPanelId, NativeTVLibrarySurface} from './tv-library';
import type {NativeTVLibraryNamingStore} from './tv-library';
import {nativeLibraryOpening, nativeLibraryOpeningRoute} from './opening';
import {takeLibrarySavedView} from './saved-view-intent';

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
  let disposed = false, selected: Readonly<{id: string; kind: string; name: string; tab?: LibraryTabId; opening: object; destination: NativeLibraryDestination; actions: NativeLibraryActions; selection?: NativeLibrarySelectionPort}> | undefined;
  // TV owns exactly one extra surface for a Library route: the F-LIB-52 Save
  // view name keyboard. Other platforms keep their own sheet unchanged.
  let tvNaming: NativeTVLibraryNamingStore | undefined;
  const tvNamingStore = (): NativeTVLibraryNamingStore => tvNaming ??= createNativeTVLibraryNamingStore(context);
  const retire = (value: typeof selected) => {value?.actions.dispose?.(); value?.destination.dispose(); if (selected === value) selected = undefined;};
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
        return <NativeReadingLibraryScreen key={id} context={context} runtime={runtime} libraryId={id} title={library.name} labels={ports.labels}
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
        selected = {id, kind: library.kind, name: library.name, opening: route.destination, ...(routeTab ? {tab: routeTab.id} : {}), destination,
          actions: Platform.isTV ? nativeTVLibraryActions(actions, tvNamingStore(), destination) : actions,
          selection: ports.selection?.(destination)};
      }
      const page = <LibraryScreen key={id} destination={selected.destination} runtime={runtime} directory={directory} libraries={context.libraries()}
        activation={ports.activation(id)} labels={ports.labels} actions={selected.actions} selection={selected.selection} owner={context.session()?.viewer.role === 'owner'}
        mixAnchor={route.destination.anchor === 'mixes'}/>;
      return tvNaming ? <NativeTVLibrarySurface key={id} library={page} naming={tvNaming} panelId={nativeTVNamingPanelId(context.authority(), id)}/> : page;
    },
    dispose() {if (disposed) return; disposed = true; retireSelected(); pivots.clear(); tvNaming?.dispose(); tvNaming = undefined; directory.dispose();},
  };
}

export type {NativeLibraryActions, NativeLibrarySelectionPort} from './LibraryScreen';
export type {NativeLibraryViewDraft} from './model';
export {createNativeLibraryPlaySelection};
