import type {CardSelection} from '../../kit/cards';
import {useEffect, useSyncExternalStore, type ReactNode} from 'react';
import type {ContentEntry} from '@core/library-content.ts';
import {router} from '../../router';

import type {BrowseSelectionScope} from '@core/selection-model/index.ts';
import {randomId} from '@core/random-id.ts';
import type {WebShellPlaybackRuntime} from '../../platform/playback/runtime';
import type {ScreenContext} from '../detail/context';
import {screenLifetime} from '../detail/context';
import {createLibraryPlaySelection, type ExplicitCatalogueSelectionSource} from './play-selection';
import {createLibrarySelection, type LibrarySelection} from './selection';

export type CatalogueDocumentSource = Readonly<{
  getSnapshot(): Readonly<{key: string; revision: string; entries: readonly ContentEntry[]}> | null;
  subscribe(listener: () => void): () => void;
}>;
/** Explicit document selection. Each source owns its genuine continuity token;
 * no document becomes a Browse whole-query descriptor. */
export function createCatalogueDocumentSelection(context: ScreenContext, sourceDocument: CatalogueDocumentSource,
  principal: Omit<BrowseSelectionScope, 'viewId'>, metadataFence: () => string | undefined, runtime?: WebShellPlaybackRuntime) {
  const life = screenLifetime(context), listeners = new Set<() => void>();
  const mounted = new Map<ContentEntry, {order: number; count: number}>();
  let admitted = new WeakSet<ContentEntry>();
  let version = 0, disposed = false, started = false, pending = false, serial = 0;
  let active: {key: string; play: ReturnType<typeof createLibraryPlaySelection>; selection: LibrarySelection; off(): void} | undefined;
  const emit = () => {version++; for (const listener of listeners) listener();};
  const sourceListeners = new Set<() => void>();
  const notifySource = () => {for (const listener of sourceListeners) listener();};
  const sourceKey = () => disposed || !life.current() ? undefined : sourceDocument.getSnapshot()?.key;
  const rows = () => [...mounted].sort((a, b) => a[1].order - b[1].order).map(([entry]) => entry).slice(0, 60);
  const register = () => {
    if (pending || disposed) return;
    pending = true;
    queueMicrotask(() => {
      pending = false; const owned = active;
      if (!owned || sourceKey() !== owned.key) return;
      notifySource();
      const count = rows().length;
      if (count) void owned.play.registerWindow(0, count - 1).catch(error => {if (active === owned && life.current() && sourceKey() === owned.key) context.effects.reportError(error);});
      emit();
    });
  };
  const retire = () => {const old = active; active = undefined; old?.off(); old?.play.dispose(); mounted.clear(); admitted = new WeakSet(); notifySource();};
  const sync = () => {
    const key = sourceKey();
    if (active?.key === key) {for (const entry of sourceDocument.getSnapshot()?.entries ?? []) admitted.add(entry); return;}
    retire();
    if (!key) {emit(); return;}
    const document = sourceDocument.getSnapshot()!;
    for (const entry of document.entries) admitted.add(entry);
    const revision = document.revision, queryRevision = ++serial, viewId = `catalogue-selection:${randomId()}`;
    const current = () => !disposed && life.current() && sourceKey() === key;
    const subscribe = (listener: () => void) => {sourceListeners.add(listener); return () => {sourceListeners.delete(listener);};};
    const source: ExplicitCatalogueSelectionSource = {
      viewIdentity: () => viewId,
      getSnapshot: () => ({phase: current() ? 'ready' : 'retired', revision, queryRevision}),
      subscribe, contains: entry => current() && admitted.has(entry) && mounted.has(entry),
      collection: {subscribe, itemAt: index => {const entry = rows()[index]; return entry ? {entry} : undefined;}},
    };
    const owner = context.identity?.viewer.role === 'owner' && context.identity.viewer.authority === 'local';
    const play = createLibraryPlaySelection({source, api: life.api, scope: context.scope, principal, requestId: async () => randomId(),
      signal: life.signal(), isCurrent: current, editor: owner, deletion: owner, destinations: true, personal: {myList: true, watched: true, favorite: true},
      playback: runtime?.cataloguePlayback(), queue: runtime?.catalogueQueue()});
    const selection = createLibrarySelection({playSelection: play, owner,
      ...(runtime ? {enqueue: play.enqueue} : {}),
      pickers: {api: life.api, serverId: context.scope.serverId, viewerId: context.scope.viewerId, requestId: async () => randomId(), signal: life.signal(), isCurrent: current,
        canCreateCollection: context.identity?.viewer.role === 'owner', pickerIdentity: {...principal, events: context.events}, artwork: context.artwork,
        onView: (destination, id) => {if (!current()) return;
          void router.navigate({to: '/saved', search: destination === 'playlist' ? {view: 'playlist', playlistId: id} : {view: 'resource', resourceId: id}}).catch(context.effects.reportError);}},
      ...(owner ? {editor: {api: life.api, events: context.events, requestId: async () => randomId(), serverId: context.scope.serverId, viewerId: context.scope.viewerId,
        artwork: context.artwork, viewerFence: metadataFence, signal: life.signal(), isCurrent: current},
        deletion: {api: life.api, serverId: context.scope.serverId, viewerId: context.scope.viewerId, requestId: async () => randomId(), signal: life.signal(), isCurrent: current}} : {}),
      actions: {api: life.api, events: context.events, requestId: async () => randomId(), signal: life.signal(), isCurrent: current, reportError: context.effects.reportError},
      reportError: context.effects.reportError,
    });
    active = {key, play, selection, off: play.subscribe(emit)};
    play.start(); emit();
  };
  let off = () => {};
  const start = () => {if (started || disposed) return; started = true; off = sourceDocument.subscribe(sync); sync();};
  const subscribe = (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};};
  const getSnapshot = () => version;
  function Toolbar() {useSyncExternalStore(subscribe, getSnapshot, getSnapshot); return active?.selection.toolbar ?? null;}
  return {
    start, subscribe, getSnapshot, epoch: () => serial, isActive: () => active?.play.getSnapshot().selection?.active === true, toolbar: <Toolbar/>,
    canSelectEntries(entries: readonly ContentEntry[]) {
      const owned = active;
      if (!owned || sourceKey() !== owned.key || !entries.length || entries.length > 60 || new Set(entries).size !== entries.length) return false;
      if (entries.some(entry => entry.kind === 'radio')) return false;
      const registered = owned.play.getSnapshot().rows;
      return entries.every(entry => admitted.has(entry) && mounted.has(entry) && registered.some(row => row.entry === entry));
    },
    selectEntries(entries: readonly ContentEntry[]) {
      const owned = active;
      if (!owned || sourceKey() !== owned.key || !entries.length || entries.length > 60 || new Set(entries).size !== entries.length) throw new DOMException('Selection changed', 'AbortError');
      if (entries.some(entry => entry.kind === 'radio')) throw new DOMException('Selection changed', 'AbortError');
      const registered = owned.play.getSnapshot().rows;
      const selected = entries.map(entry => {
        const row = registered.find(row => row.entry === entry);
        if (!row || !admitted.has(entry) || !mounted.has(entry)) throw new DOMException('Selection changed', 'AbortError');
        return row;
      });
      for (const row of selected) {
        if (active !== owned || sourceKey() !== owned.key || !admitted.has(row.entry) || !mounted.has(row.entry)) throw new DOMException('Selection changed', 'AbortError');
        if (!owned.play.selected(row) && !owned.play.toggle(row).ok) throw new DOMException('Selection changed', 'AbortError');
      }
    },
    register(entry: ContentEntry, order: number) {
      const owned = active;
      if (!owned || sourceKey() !== owned.key || !admitted.has(entry)) return () => {};
      const previous = mounted.get(entry); mounted.set(entry, {order, count: (previous?.count ?? 0) + 1}); register();
      return () => {if (active !== owned) return; const value = mounted.get(entry); if (value && value.count > 1) mounted.set(entry, {...value, count: value.count - 1}); else mounted.delete(entry); register();};
    },
    card(entry: ContentEntry) {return entry.kind === 'radio' ? undefined : active?.selection.entry({entry}, rows().indexOf(entry));},
    dispose() {if (disposed) return; disposed = true; off(); retire(); life.dispose(); emit(); listeners.clear(); sourceListeners.clear();},
  };
}
export type CatalogueDocumentSelection = ReturnType<typeof createCatalogueDocumentSelection>;

const emptySubscribe = () => () => {};
const emptySnapshot = () => 0;
export function useCatalogueSelectionActive(selection?: CatalogueDocumentSelection) {
  useSyncExternalStore(selection?.subscribe ?? emptySubscribe, selection?.getSnapshot ?? emptySnapshot, selection?.getSnapshot ?? emptySnapshot);
  return selection?.isActive() === true;
}
export function CatalogueSelectionEntry({selection, entry, position, children}: {selection?: CatalogueDocumentSelection; entry: ContentEntry; position: number; children: (selection: CardSelection | undefined) => ReactNode}) {
  useCatalogueSelectionActive(selection);
  const epoch = selection?.epoch();
  useEffect(() => selection?.register(entry, position), [selection, entry, position, epoch]);
  return children(selection?.card(entry));
}
