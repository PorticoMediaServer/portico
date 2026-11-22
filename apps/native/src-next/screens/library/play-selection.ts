import {BrowseSelectionModel, browseSelectionKey, playCatalogueSelection, readCatalogueSelectionItem, type BrowseSelectionContext, type SelectionItem, type SelectionOperation} from '../../../../../packages/client-core/src/selection-model';
import {playCatalogueQuerySelection} from '../../../../../packages/client-core/src/catalogue-query-selection';
import {browseWholeSelectionQuery} from '../../../../../packages/client-core/src/browse-whole-selection';
import type {BrowseWholeSelection} from '../../../../../packages/client-core/src/browse';
import type {CatalogueSelectionPorts} from '../../../../../packages/client-core/src/selection-model';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {nativeDestinationActivation} from '../../shell/routes';
import {nativeDestinationLifetime} from '../destinations/lifetime';
import {nativeScreenServices} from '../native-screen-services';
import type {NativeLibraryDestination} from './model';
import {viewerQueriesFor} from '../../shell/freshness';

/** The real ports a library title may be offered: leaf Play, Add to queue only with a catalogue player, the personal
 * actions, and Edit for the owner alone. One title's selection read and the whole-view summary use the same ports, so
 * Select all counts each action exactly as picking every title alone would. */
export function nativeLibrarySelectionPorts(context: NativeShellBootstrapContext): CatalogueSelectionPorts {
  return {play: true, queue: !!context.playback.cataloguePlayback?.(), myList: true, favorite: true, watched: true, playlist: true, collection: true,
    edit: context.session()?.viewer.role === 'owner', delete: true};
}

const cancelled = () => Object.assign(new Error('native_library_selection_retired'), {name: 'AbortError', code: 'cancelled'});
const unavailable = () => Object.assign(new Error('native_library_selection_unavailable'), {code: 'unavailable'});

/** One captured applied Browse query, not a Home context. Recreate after a
 * query/token change. Leaves carry the grants the library really executes. The
 * whole view (Select all) is bound only to the server's exact summary, read when
 * someone selects; only Play and Queue have query executors. */
export function createNativeLibraryPlaySelection(context: NativeShellBootstrapContext, source: NativeLibraryDestination) {
  const lifetime = nativeDestinationLifetime(context), opening = source.getSnapshot(), viewer = context.session()!.viewer;
  if (opening.phase !== 'ready' || !opening.revision || !opening.collection || !opening.sort) throw unavailable();
  const authority = viewer.authority;
  if (authority !== 'local' && authority !== 'hosted') throw unavailable();
  const definition = source.captureSavedView({predicates: opening.predicates, sort: opening.sort});
  const captured: BrowseSelectionContext = Object.freeze({
    scope: Object.freeze({serverId: viewer.serverId, authority, accountId: viewer.accountId, profileId: viewer.profileId,
      viewId: JSON.stringify(['native-library', definition.libraryId, definition.pivot, definition.query, definition.sort])}),
    revision: Object.freeze({pageRevision: opening.revision}),
  });
  const model = new BrowseSelectionModel(captured), player = context.playback.runtime.cataloguePlayback();
  const common = nativeScreenServices(context), activation = nativeDestinationActivation(context.navigation, {kind: 'library', libraryId: opening.library.id});
  const collection = opening.collection, cleanups: (() => void)[] = [];
  // The server's exact whole-view summary, asked for once when the first window is read (so only once someone selects,
  // never when the list opens). Every window waits for that one answer and binds to it, so no window registers without
  // the query another one carries. It is released with this producer and never carried to another view.
  let whole: Readonly<{id: string; total: number}> | undefined, releaseWhole: (() => void) | undefined;
  let wholeRequest: Promise<BrowseWholeSelection | undefined> | undefined;
  const bindWhole = (ticket: ReturnType<typeof model.captureContext>, descriptor: BrowseWholeSelection | undefined) => {
    const snapshot = source.getSnapshot();
    if (whole || !descriptor || snapshot.phase !== 'ready' || descriptor.libraryId !== snapshot.library.id) return;
    const query = browseWholeSelectionQuery(descriptor);
    if (!query) return;
    const registration = model.registerQuery(ticket, query);
    if (registration.ok) {releaseWhole = registration.unregister; whole = Object.freeze({id: query.id, total: query.total});}
  };
  const windows = new Map<string, () => void>();
  const operations = new Map<AbortController, SelectionOperation<BrowseSelectionContext>>();
  let disposed = false, reading: AbortController | undefined;
  const current = () => {
    const next = source.getSnapshot();
    return !disposed && lifetime.current() && next.phase === 'ready' && next.collection === collection
      && next.queryRevision === opening.queryRevision && next.revision === opening.revision && next.tab === opening.tab;
  };
  const abortPending = () => {reading?.abort(); reading = undefined; for (const controller of operations.keys()) controller.abort();};
  const dispose = () => {
    if (disposed) return;
    disposed = true; abortPending(); releaseWhole?.(); releaseWhole = undefined; whole = undefined; cleanups.splice(0).forEach(stop => stop());
    for (const stop of [...windows.values()]) stop();
    windows.clear(); model.dispose();
  };
  cleanups.push(source.subscribe(() => {if (!current()) dispose();}), context.subscribeAuthority(() => {if (!current()) dispose();}),
    context.navigation.subscribe(abortPending), model.subscribe(() => {
      for (const [controller, operation] of operations) if (!model.isOperationCurrent(operation)) controller.abort();
    }));

  return Object.freeze({model,
    /** A real contiguous loaded range, at its actual absolute positions.
     * At most four Detail reads and one window preparation are in flight. */
    prepareWindow: async (id: string, start: number, input: readonly ContentEntry[]) => {
      if (!current() || !activation.getSnapshot().visible) throw cancelled();
      if (!Number.isSafeInteger(start) || start < 0 || !input.length || input.length > 60) throw unavailable();
      const entries = Object.freeze([...input]);
      const member = () => entries.every((entry, offset) => collection.itemAt(start + offset) === entry && source.contains(entry));
      if (!member()) throw cancelled();
      windows.get(id)?.();
      if (!windows.has(id) && windows.size >= 32) throw unavailable();
      reading?.abort(); const controller = new AbortController(); reading = controller;
      const ticket = model.captureContext(), revision = context.navigation.getSnapshot().revision;
      const live = () => !controller.signal.aborted && current() && activation.getSnapshot().visible
        && context.navigation.getSnapshot().revision === revision && model.isContextCurrent(ticket) && member();
      const check = () => {if (!live()) controller.abort();};
      const stops = [...new Set(entries.map((_, offset) => collection.pageOf(start + offset)))].map(page => collection.subscribePage(page, check));
      let registered = false, unregister: (() => void) | undefined;
      const cleanup = () => {
        controller.abort(); unregister?.(); stops.splice(0).forEach(stop => stop());
        if (windows.get(id) === cleanup) windows.delete(id);
      };
      try {
        const items: SelectionItem[] = new Array(entries.length); let cursor = 0;
        // The same real ports the selection bar may offer: leaf Play plus the
        // Detail actions an item actually enables. Absent executors (queue when
        // there is no catalogue player) leave that capability false and hidden.
        const ports = nativeLibrarySelectionPorts(context);
        wholeRequest ??= source.requestWholeSelection().catch(() => undefined);
        await Promise.all(Array.from({length: Math.min(4, entries.length)}, async () => {
          while (cursor < entries.length) {
            const index = cursor++;
            items[index] = await readCatalogueSelectionItem({queries: viewerQueriesFor(context.api, lifetime.scope), api: context.api, scope: lifetime.scope, requestId: common.requestId,
              signal: controller.signal, isCurrent: live}, entries[index], ports);
          }
        }));
        if (!live()) throw cancelled();
        // Only the server's exact summary (counted with these same ports) describes the whole view; a client guess refused
        // every window once one title differed (22 Nov). Where there is none (series, books, an empty list, a failed read)
        // titles are picked one by one.
        const descriptor = await wholeRequest;
        if (!live()) throw cancelled();
        bindWhole(ticket, descriptor);
        const result = model.registerWindow(ticket, {id, sectionId: 'library-browse', sectionOrder: 0, start, items, ...(whole ? {queryId: whole.id} : {})});
        // A window the bound summary refuses means a title changed since it was read: the next selection reads fresh counts.
        if (!result.ok) {if (whole) source.forgetWholeSelection(); throw cancelled();}
        unregister = result.unregister; registered = true;
        // Once registered, native page eviction unregisters its window while
        // the shared model preserves explicit picks at this same source token.
        stops.splice(0).forEach(stop => stop());
        stops.push(...[...new Set(entries.map((_, offset) => collection.pageOf(start + offset)))].map(page => collection.subscribePage(page, () => {
          if (!current() || !member()) cleanup();
        })));
        windows.set(id, cleanup);
        const keys = new Map(entries.map((entry, index) => [entry, browseSelectionKey(captured.scope, items[index])]));
        return Object.freeze({
          /** The model key each registered entry was given, so a caller can ask the
           * real model whether that exact entry is selected. */
          keys: keys as ReadonlyMap<ContentEntry, string>,
          toggle: (entry: ContentEntry, range = false) => {
            const key = keys.get(entry);
            if (!current() || !activation.getSnapshot().visible || !member() || !key || windows.get(id) !== cleanup) throw cancelled();
            return model.toggle(id, key, range);
          },
          dispose: cleanup,
        });
      } catch (error) {cleanup(); throw error;}
      finally {if (reading === controller) reading = undefined; if (!registered) cleanup();}
    },
    /** Select all N only from the validated aggregate, never from a loaded window. */
    selectWhole: (id: string): boolean => current() && whole?.id === id ? model.selectWholeQuery(id).ok : false,
    whole: () => whole,
    play: async () => {
      if (!current() || !activation.getSnapshot().visible) throw cancelled();
      const operation = model.captureOperation();
      // Both targets are lawful here: the explicit path plays its own leaves and the
      // query path validates its descriptor through the canonical executor.
      if (!operation) throw unavailable();
      const controller = new AbortController(), revision = context.navigation.getSnapshot().revision;
      const isCurrent = () => !controller.signal.aborted && current() && activation.getSnapshot().visible
        && context.navigation.getSnapshot().revision === revision && model.isOperationCurrent(operation);
      operations.set(controller, operation);
      try {return operation.target.mode === 'query'
        ? await playCatalogueQuerySelection(player, operation.target, {signal: controller.signal, isCurrent})
        : await playCatalogueSelection(player, operation.target.displayItems.map(value => value.item), {signal: controller.signal, isCurrent});}
      finally {operations.delete(controller);}
    },
    dispose,
  });
}
