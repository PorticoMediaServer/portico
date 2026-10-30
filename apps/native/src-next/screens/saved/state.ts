import type {PersonalResource} from '../../../../../packages/client-core/src/personal-saved';
import type {NativeShellRoute} from '../../shell/routes';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {nativeDestinationLifetime} from '../destinations/lifetime';
import {createNativeEntryActions} from '../destinations/entry-actions';
import {createNativeDownloadsDevice} from '../downloads/device';
import {nativeScreenServices} from '../native-screen-services';
import type {NativeSavedActions} from './SavedScreen';
import {createNativeSavedViewSelection} from './selection';
import {createNativeSavedHubController, createNativeSavedViewController, type NativeSavedRouteTarget} from './navigation';
import {createNativeSavedViewSummaryOwner, type NativeSavedViewSummaryOwner} from './view-summary-owner';
import {createNativeSavedViewRenameController, type NativeSavedViewRenameController} from './rename-view';
import {askHomeCustomizeView, clearHomeCustomizeIntent} from '../home/customize-intent';
import {askLibrarySavedView, clearLibrarySavedViewIntent} from '../library/saved-view-intent';

/** The read ports a retained views-directory route exposes. Disposal stays
 * internal to the parent state, which owns construction and retirement. */
export type NativeSavedViewSummaries = Pick<NativeSavedViewSummaryOwner, 'start' | 'getSnapshot' | 'subscribe' | 'line'>;

/** Real Saved inputs for a generated presenter. This binds existing services,
 * the borrowed Watch module and the same native device lease as Downloads;
 * it creates neither a renderer nor a second offline store/player. */
export function createNativeSavedState(context: NativeShellBootstrapContext) {
  const lifetime = nativeDestinationLifetime(context), common = nativeScreenServices(context);
  const hub = createNativeSavedHubController(context, common.requestId), device = createNativeDownloadsDevice(context);
  let disposed = false;
  const current = () => !disposed && lifetime.current();
  const entries = createNativeEntryActions(context, {current: () => current() && hub.activation.getSnapshot().visible,
    contains: hub.source.contains, subscribe: hub.source.subscribe});
  const intentOwners = new Map<object, () => void>();
  /** One intent's subscriptions live only while its exact target route is current or
   * pending; leaving that route, losing authority, or a successor clears only its own
   * token. */
  const ownIntent = (target: NativeShellRoute, clear: (token?: object) => void) => {
    const token = {};
    let offNav = () => {}, offAuthority = () => {};
    const release = () => {offNav(); offAuthority(); intentOwners.delete(token);};
    const clearThis = () => clear(token);
    offNav = context.navigation.subscribe(() => {if (context.navigation.getSnapshot().route !== target) clearThis();});
    offAuthority = context.subscribeAuthority(clearThis);
    intentOwners.set(token, clearThis);
    return {token, release};
  };
  const actions: NativeSavedActions = Object.freeze({
    menuState: entries.menuState, openView: hub.openView, openEntry: entries.open, menuEntry: entries.menu, isMenuOpen: entries.isMenuOpen,
    openDownloads: hub.openDownloads, openPin: hub.openPin, editPins: hub.editPins,
    selectMyList: hub.source.selectMyList, nextMyList: hub.source.nextMyList,
    showOnHome: resource => {
      if (!current() || resource.kind !== 'view') return;
      const session = context.session(); if (!session) return;
      const route = Object.freeze({kind: 'home-customize' as const}), authority = context.authority();
      const owned = ownIntent(route, clearHomeCustomizeIntent);
      askHomeCustomizeView({token: owned.token, release: owned.release, route, authority, sessionFamilyId: session.sessionFamilyId, serverId: resource.serverId,
        resourceId: resource.id, revision: resource.revision, viewerFence: resource.viewerFence});
      if (!context.navigation.open(route, authority)) clearHomeCustomizeIntent(owned.token);
    },
    openInLibrary: resource => {
      if (!current() || resource.kind !== 'view' || !resource.definition) return;
      const library = context.libraries().find(value => value.id === resource.definition!.libraryId);
      const session = context.session();
      if (!library || !session) return;
      const route = Object.freeze({kind: 'shell' as const, destination: Object.freeze({kind: 'library' as const, libraryId: library.id})}), authority = context.authority();
      const owned = ownIntent(route, clearLibrarySavedViewIntent);
      askLibrarySavedView({token: owned.token, release: owned.release, route, libraryId: library.id, authority, sessionFamilyId: session.sessionFamilyId, definition: resource.definition,
        resourceId: resource.id, revision: resource.revision, viewerFence: resource.viewerFence});
      if (!context.navigation.open(route, authority)) clearLibrarySavedViewIntent(owned.token);
    },
  });
  type View = ReturnType<typeof createView>;
  const views = new Map<NativeSavedRouteTarget, View>(), cleanups: (() => void)[] = [];
  const retained = (target: NativeSavedRouteTarget) => context.navigation.getSnapshot().route === target || context.navigation.retainedRoutes().includes(target);
  function createView(target: NativeSavedRouteTarget) {
    const controller = createNativeSavedViewController(context, target, common.requestId);
    const selection = createNativeSavedViewSelection(context, controller.source, controller.activation);
    const entryActions = createNativeEntryActions(context, {current: () => current() && controller.activation.getSnapshot().visible,
      contains: controller.source.contains, subscribe: controller.source.subscribe});
    let resourcePlay: Readonly<{abort: AbortController; current: () => boolean}> | undefined;
    const checkPlay = () => {if (resourcePlay && !resourcePlay.current()) resourcePlay.abort.abort();};
    const playOff = [controller.source.subscribe(checkPlay), context.navigation.subscribe(checkPlay), context.subscribeAuthority(checkPlay)];
    /** Play the named server resource as one ordered queue. A loaded preview is never its
     * membership: the server snapshots every eligible occurrence, including later pages. */
    const playResource = async (shuffle = false): Promise<void> => {
      if (!current() || !controller.activation.getSnapshot().visible || resourcePlay) return;
      const port = context.playback.cataloguePlayback?.();
      if (!port) throw Object.assign(new Error('native_saved_playback_unavailable'), {code: 'unavailable'});
      const source = controller.source.getSnapshot(), route = source.route;
      const resource = route?.view === 'playlist' ? source.saved?.playlist : route?.view === 'resource' ? source.personal?.resource : undefined;
      if (source.phase !== 'ready' || !resource || 'status' in resource && resource.status !== 'ready') return;
      const revision = context.navigation.getSnapshot().revision, abort = new AbortController();
      const isCurrent = () => {
        if (abort.signal.aborted || !current() || !controller.activation.getSnapshot().visible || context.navigation.getSnapshot().revision !== revision) return false;
        const next = controller.source.getSnapshot();
        const loaded = route?.view === 'playlist' ? next.saved?.playlist : next.personal?.resource;
        return next.phase === 'ready' && !!loaded && loaded.id === resource.id && loaded.revision === resource.revision
          && loaded.serverId === resource.serverId && loaded.viewerFence === resource.viewerFence && (!('status' in loaded) || loaded.status === 'ready');
      };
      const operation = {abort, current: isCurrent}; resourcePlay = operation;
      try {
        await port.playSegments([{
          source: route?.view === 'playlist' ? {container: {kind: 'playlist', id: resource.id}} : {saved: {id: resource.id}},
          ...(shuffle ? {order: {mode: 'shuffle' as const}} : {}),
        }], {signal: abort.signal, isCurrent});
      } finally {if (resourcePlay === operation) resourcePlay = undefined;}
    };
    const pageView = target.kind === 'saved-view' ? target.view : undefined;
    const owner = pageView?.view === 'views'
      ? createNativeSavedViewSummaryOwner(context, controller.source, () => !disposed && lifetime.current() && retained(target))
      : undefined;
    const summaries: NativeSavedViewSummaries | undefined = owner && Object.freeze({start: owner.start, getSnapshot: owner.getSnapshot,
      subscribe: owner.subscribe, line: owner.line});
    let rename: {controller: NativeSavedViewRenameController; resource: PersonalResource} | undefined;
    const openRename = (resource: PersonalResource): NativeSavedViewRenameController | undefined => {
      if (pageView?.view !== 'resource') return;
      const held = rename;
      if (held) {
        const status = held.controller.getSnapshot();
        if (!status.closed && !status.retired) {
          const loaded = controller.source.getSnapshot().personal?.resource;
          return resource === held.resource || resource === loaded ? held.controller : undefined;
        }
        rename = undefined;
      }
      const binding = controller.source.viewRename(resource);
      if (!binding) return;
      const created = createNativeSavedViewRenameController({service: binding.port, resource,
        current: () => !disposed && lifetime.current() && retained(target) && binding.current()});
      const holder: {current?: NativeSavedViewRenameController} = {};
      const exposed = Object.freeze({getSnapshot: created.getSnapshot, subscribe: created.subscribe, setName: created.setName,
        save: created.save, retry: created.retry, cancel: created.cancel,
        dispose: () => {created.dispose(); if (rename?.controller === holder.current) rename = undefined;}}) as NativeSavedViewRenameController;
      holder.current = exposed;
      rename = {controller: exposed, resource};
      return exposed;
    };
    return Object.freeze({...controller, selection, load: () => {
      if (!current()) return Promise.resolve();
      void owner?.start();
      return controller.load();
    }, ...(summaries ? {summaries} : {}), rename: openRename, actions: Object.freeze({...entryActions, playResource}),
    dispose: () => {resourcePlay?.abort.abort(); resourcePlay = undefined; playOff.forEach(off => off()); rename?.controller.dispose(); rename = undefined; owner?.dispose(); selection.dispose(); entryActions.dispose(); controller.dispose();}});
  }
  const dispose = () => {
    if (disposed) return;
    disposed = true; for (const clear of [...intentOwners.values()]) clear(); intentOwners.clear(); cleanups.splice(0).forEach(stop => stop());
    for (const view of views.values()) view.dispose();
    views.clear(); entries.dispose(); hub.dispose(); device.dispose();
  };
  const check = () => {
    if (!current()) {dispose(); return;}
    for (const [target, view] of views) if (!retained(target)) {views.delete(target); view.dispose();}
  };
  cleanups.push(context.subscribeAuthority(check), context.navigation.subscribe(check));
  return Object.freeze({source: hub.source, activation: hub.activation, actions, offline: device.device,
    view: (target: NativeSavedRouteTarget) => {
      check();
      if (!current() || !retained(target)) return;
      let value = views.get(target);
      if (!value) {value = createView(target); views.set(target, value);}
      return value;
    },
    dispose,
  });
}
