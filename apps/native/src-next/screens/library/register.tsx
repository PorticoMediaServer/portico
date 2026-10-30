import React, {useSyncExternalStore} from 'react';
import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src';
import {browseNameId} from '../../../../../packages/client-core/src/presentation/browse-toolbar';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {nativeShellRoutes, type NativeRouteRenderer} from '../../shell/route-registry';
import {nativeDestinationActivation} from '../../shell/routes';
import {nativeScreenServices} from '../native-screen-services';
import {createNativeEntryActions} from '../destinations/entry-actions';
import {nativeDestinationLifetime} from '../destinations/lifetime';
import {createNativeLibraryRenderer} from './index';
import {createNativeLibrarySelection} from './selection';
import {nativeLibraryCanSave, type NativeLibraryDestination, type NativeLibraryViewDraft} from './model';
import {NativeLibrarySaveView} from './save-view';
import {NativeLibraryMarkAll, type NativeMarkAllRequest} from './mark-all';
import {NativeLibraryMarkAllDialog} from './MarkAllDialog';
import {NativeLibrarySaveViewSheet} from './SaveViewSheet';

const label = (value: {id: string; labelKey?: string}) => {
  const id = browseNameId(value.id, value.labelKey, defaultI18n.has);
  if (!id) throw Object.assign(new Error('native_browse_copy_unavailable'), {code: 'invalid_response'});
  return defaultI18n.t(id as MessageId);
};
type NamingStore = Readonly<{subscribe: (listener: () => void) => () => void; getSnapshot: () => NativeLibrarySaveView | undefined}>;
function NamingOutlet({store}: {store: NamingStore}) {
  const controller = useSyncExternalStore(store.subscribe, store.getSnapshot);
  return controller ? <NativeLibrarySaveViewSheet controller={controller}/> : null;
}
type MarkAllStore = Readonly<{subscribe: (listener: () => void) => () => void; getSnapshot: () => NativeLibraryMarkAll | undefined}>;
function MarkAllOutlet({store}: {store: MarkAllStore}) {
  const controller = useSyncExternalStore(store.subscribe, store.getSnapshot);
  return controller ? <NativeLibraryMarkAllDialog controller={controller}/> : null;
}

/** Handheld Library owns its genuine query, bounded entries and naming sheet.
 * Watch contributes its already-registered menu; the shell keeps the page mounted. */
export function createNativeHandheldLibraryModule(context: NativeShellBootstrapContext): NativeRouteRenderer {
  const lifetime = nativeDestinationLifetime(context), common = nativeScreenServices(context);
  const listeners = new Set<() => void>();
  let disposed = false, naming: NativeLibrarySaveView | undefined, markAll: NativeLibraryMarkAll | undefined;
  const emit = () => {for (const listener of [...listeners]) listener();};
  const closeNaming = () => {naming?.dispose(); naming = undefined; emit();};
  const closeMarkAll = () => {markAll?.dispose(); markAll = undefined; emit();};
  const store: NamingStore = {subscribe: listener => {listeners.add(listener); return () => {listeners.delete(listener);};}, getSnapshot: () => naming};
  const markAllStore: MarkAllStore = {subscribe: listener => {listeners.add(listener); return () => {listeners.delete(listener);};}, getSnapshot: () => markAll};
  const renderer = createNativeLibraryRenderer(context, {
    activation: libraryId => nativeDestinationActivation(context.navigation, {kind: 'library', libraryId}), labels: {field: label, sort: label, quick: label},
    selection: destination => createNativeLibrarySelection(context, destination,
      nativeDestinationActivation(context.navigation, {kind: 'library', libraryId: destination.getSnapshot().library.id})),
    actions: destination => {
      const activation = nativeDestinationActivation(context.navigation, {kind: 'library', libraryId: destination.getSnapshot().library.id});
      const actions = createNativeEntryActions(context, {
        current: () => !disposed && activation.getSnapshot().visible,
        contains: destination.contains,
        subscribe: listener => {
          let collection = destination.getSnapshot().collection, offCollection = collection?.subscribe(listener);
          const off = destination.subscribe(() => {
            const next = destination.getSnapshot().collection;
            if (next !== collection) {offCollection?.(); collection = next; offCollection = next?.subscribe(listener);}
            listener();
          });
          return () => {off(); offCollection?.();};
        },
      });
      return {...actions, markAll: (input: NativeMarkAllRequest) => {
        if (disposed || !lifetime.current() || !activation.getSnapshot().visible) return;
        closeMarkAll();
        markAll = new NativeLibraryMarkAll({...input, api: context.api, requestId: common.requestId,
          current: () => !disposed && lifetime.current() && activation.getSnapshot().visible,
          finished: () => closeMarkAll()});
        emit();
      }, saveView: (actual: NativeLibraryDestination, draft: NativeLibraryViewDraft) => {
        if (disposed || !lifetime.current() || !activation.getSnapshot().visible || actual !== destination || !nativeLibraryCanSave(actual.getSnapshot(), draft)) return;
        const definition = actual.captureSavedView(draft), revision = context.navigation.getSnapshot().revision;
        closeNaming();
        naming = new NativeLibrarySaveView({api: context.api, scope: lifetime.scope, definition, requestId: common.requestId,
          current: () => !disposed && lifetime.current() && activation.getSnapshot().visible && context.navigation.getSnapshot().revision === revision,
          saved: () => closeNaming()});
        emit();
      }};
    },
  });
  const offRoute = context.navigation.subscribe(() => {
    if (naming && !naming.getSnapshot().open) closeNaming();
    if (markAll && !markAll.getSnapshot().open) closeMarkAll();
  });
  return {render: renderer.render, renderOverlays: () => !disposed && lifetime.current() ? <><NamingOutlet store={store}/><MarkAllOutlet store={markAllStore}/></> : null,
    dispose: () => {if (disposed) return; disposed = true; offRoute(); closeNaming(); closeMarkAll(); renderer.dispose(); listeners.clear();}};
}

nativeShellRoutes.registerDestination('library', createNativeHandheldLibraryModule);
