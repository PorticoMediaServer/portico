import {DetailService, type DetailAction, type DetailScope} from '../../../../packages/client-core/src/detail';
import type {QueuePlaybackView} from '../../../../packages/client-core/src/queue-controller';
import {sameFence, type PlaybackFence, type QueuePlaybackFence} from '../../../../packages/player-core/src';
import type {NativePlaybackReadLifetime} from '../platform/playback/types';
import {nativeScreenServices} from '../screens/native-screen-services';
import type {NativeShellBootstrapContext} from './bootstrap';

/** The current queue song's own detail. The object is an issued offer token: only a read from
 * this port can produce one, and favorite() accepts it back only while everything it names
 * (target, fence, opening lifetime, offer and personal revision) is still the same document. */
export type NativeListeningSong = Readonly<{itemId: string; libraryId: string; favorite: boolean; revision: number; actions: readonly DetailAction[]}>;

type Bound = Readonly<{viewer: string; family: string; authority: string; navigation: number; fence: QueuePlaybackFence;
  lifetime: NativePlaybackReadLifetime; target: Readonly<{itemId: string; libraryId: string}>; scope: DetailScope; live: () => boolean}>;
type Offer = Readonly<{bound: Bound; revision: number; favorite: boolean; viewerFence: string}>;

const cancelled = () => Object.assign(new Error('cancelled'), {name: 'AbortError', code: 'cancelled'});
const conflict = (error: unknown) => (error as {code?: unknown} | null)?.code === 'personal_state_conflict';

/** One bounded DetailService read/mutation for the exact song the queue is playing. Never the
 * album/display entry, never another library, and never a borrowed later offer or lease. */
export function createNativeListeningSongDetail(context: NativeShellBootstrapContext) {
  let disposed = false;
  const issued = new WeakMap<object, Offer>(), active = new Set<{abort: AbortController; service?: DetailService}>();
  const sessionKeys = () => {
    const session = context.session();
    if (!session) return;
    const viewer = session.viewer;
    return {viewer: JSON.stringify([viewer.serverId, viewer.authority, viewer.accountId, viewer.profileId]), family: session.sessionFamilyId};
  };
  const scopeOf = (): DetailScope | undefined => {
    const viewer = context.session()?.viewer;
    return viewer ? {serverId: viewer.serverId, viewerId: JSON.stringify([viewer.authority, viewer.accountId, viewer.profileId])} : undefined;
  };
  const authorityKey = () => {const authority = context.authority(); return JSON.stringify([authority.serverId, authority.accountId, authority.profileId, authority.epoch, authority.privacyFence ?? authority.viewerFence]);};
  // Only the accepted window for exactly this queue and revision may state a row's availability.
  // A mismatch, duplicate row or missing fact stays unknown, so a visible drawn slot is never
  // taken as admission; the entry is never cast or assumed available.
  const loadedAvailability = (view: QueuePlaybackView, entryId: string): boolean | undefined => {
    const window = view.window;
    if (!window || window.queueId !== view.queue.id || window.revision !== view.queue.revision
      || new Set(window.entries.map(entry => entry.entryId)).size !== window.entries.length) return;
    const rows = window.entries.filter(entry => entry.entryId === entryId);
    return rows.length === 1 ? rows[0].available : undefined;
  };
  const liveBound = (bound: Omit<Bound, 'live'>) => {
    if (disposed || !context.current() || !bound.lifetime.isCurrent() || bound.lifetime.signal.aborted) return false;
    const keys = sessionKeys();
    if (!keys || keys.viewer !== bound.viewer || keys.family !== bound.family || authorityKey() !== bound.authority
      || context.navigation.getSnapshot().revision !== bound.navigation) return false;
    const state = context.playback.getSnapshot(), view = state.queue?.view;
    if (state.dock.local?.expanded !== true || !sameFence(state.fence, bound.fence) || !view || view.queue.currentEntryId !== bound.fence.entryId) return false;
    const entry = view.queue.entries.find(value => value.id === bound.fence.entryId), item = view.items.find(value => value.entryId === bound.fence.entryId);
    return !!entry && !!item && entry.id === bound.fence.entryId && loadedAvailability(view, bound.fence.entryId) === true && !entry.hidden && !entry.removed
      && item.entryId === bound.fence.entryId && item.kind === 'song' && item.itemId === bound.target.itemId && item.libraryId === bound.target.libraryId;
  };
  const bind = (fence: PlaybackFence, lifetime: NativePlaybackReadLifetime): Bound | undefined => {
    if (fence.kind !== 'queue') return;
    const queueFence: QueuePlaybackFence = fence, keys = sessionKeys(), scope = scopeOf(), authority = authorityKey();
    const state = context.playback.getSnapshot(), view = state.queue?.view;
    if (!keys || !scope || !authority || disposed || !context.current() || !lifetime.isCurrent() || lifetime.signal.aborted
      || state.dock.local?.expanded !== true || !sameFence(state.fence, queueFence) || !view || view.queue.currentEntryId !== queueFence.entryId) return;
    const entry = view.queue.entries.find(value => value.id === queueFence.entryId), item = view.items.find(value => value.entryId === queueFence.entryId);
    if (!entry || !item || item.kind !== 'song' || entry.hidden || entry.removed || loadedAvailability(view, queueFence.entryId) !== true || !item.itemId || !item.libraryId) return;
    const bound: Omit<Bound, 'live'> = {viewer: keys.viewer, family: keys.family, authority, navigation: context.navigation.getSnapshot().revision,
      fence: queueFence, lifetime, target: {itemId: item.itemId, libraryId: item.libraryId}, scope};
    return Object.freeze({...bound, live: () => liveBound(bound)});
  };
  const open = async (bound: Bound) => {
    let service: DetailService | undefined;
    const operation = {abort: new AbortController()} as {abort: AbortController; service?: DetailService};
    const stop = () => operation.abort.abort(), lifetime = bound.lifetime;
    const retire = () => {if (!bound.live()) stop();};
    const off = [context.navigation.subscribe(retire), context.subscribeAuthority(retire), context.playback.subscribe(retire)];
    lifetime.signal.addEventListener('abort', stop, {once: true});
    const request = <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
      if (!bound.live() || operation.abort.signal.aborted) return Promise.reject(cancelled());
      const linked = new AbortController(), abort = () => linked.abort();
      signal?.addEventListener('abort', abort); operation.abort.signal.addEventListener('abort', abort);
      if (signal?.aborted || operation.abort.signal.aborted) linked.abort();
      return context.api.request<T>(path, method, body, linked.signal).then(value => {
        if (!bound.live() || linked.signal.aborted) throw cancelled();
        return value;
      }, error => {
        // A conflict is the original command's answer; never let DetailService replace its
        // expectedRevision and retry a document the person did not see.
        if (conflict(error)) service?.dispose();
        throw error;
      }).finally(() => {signal?.removeEventListener('abort', abort); operation.abort.signal.removeEventListener('abort', abort);});
    };
    const requestId = async () => {
      if (!bound.live() || operation.abort.signal.aborted) throw cancelled();
      const id = await nativeScreenServices(context).requestId();
      if (!bound.live() || operation.abort.signal.aborted || lifetime.signal.aborted || !lifetime.isCurrent()) throw cancelled();
      return id;
    };
    service = new DetailService({api: {request}, scope: bound.scope, requestId});
    operation.service = service; active.add(operation);
    const close = () => {lifetime.signal.removeEventListener('abort', stop); off.forEach(stop => stop()); active.delete(operation); service?.dispose();};
    try {
      await service.select(bound.target);
      if (!bound.live() || operation.abort.signal.aborted) {close(); return;}
      const data = service.getSnapshot().data;
      // The Detail parser only requires a nonempty kind, and its favorite action is the server's
      // to offer on any title. This port is for the queue's song, so the opened projection must
      // itself say song for the exact library target; a queue display kind is not that proof.
      if (!data || data.item.id !== bound.target.itemId || data.item.kind !== 'song'
        || data.item.libraryId !== bound.target.libraryId || data.scope.libraryId !== bound.target.libraryId) {close(); return;}
      return {service, data, operation, close};
    } catch {close(); return;}
  };
  return Object.freeze({
    canRead: (fence: PlaybackFence, lifetime: NativePlaybackReadLifetime) => !!bind(fence, lifetime),
    read: async (fence: PlaybackFence, lifetime: NativePlaybackReadLifetime): Promise<NativeListeningSong | undefined> => {
      const bound = bind(fence, lifetime);
      if (!bound) return;
      const opened = await open(bound);
      if (!opened) return;
      try {
        if (opened.operation.abort.signal.aborted || !bound.live()) return;
        const data = opened.data;
        const token: NativeListeningSong = Object.freeze({itemId: bound.target.itemId, libraryId: bound.target.libraryId,
          favorite: data.personal.favorite, revision: data.personal.revision, actions: data.actions});
        issued.set(token, Object.freeze({bound, revision: data.personal.revision, favorite: data.personal.favorite, viewerFence: data.scope.viewerFence}));
        return token;
      } finally {opened.close();}
    },
    favorite: async (token: NativeListeningSong, value: boolean, lifetime: NativePlaybackReadLifetime): Promise<boolean> => {
      const offer = issued.get(token);
      // The successor lease B may never revive a token read under opening A.
      if (!offer || lifetime !== offer.bound.lifetime || !offer.bound.live()) return false;
      const bound = offer.bound, opened = await open(bound);
      if (!opened) return false;
      try {
        const data = opened.data, action = data.actions.find(entry => entry.id === 'favorite');
        // The fresh projection must still be the exact document the heart drew.
        if (opened.operation.abort.signal.aborted || !bound.live() || data.scope.viewerFence !== offer.viewerFence
          || data.scope.libraryId !== token.libraryId || data.item.id !== token.itemId || data.personal.revision !== offer.revision
          || data.personal.favorite !== offer.favorite || data.personal.favorite === value || !action?.enabled) return false;
        await opened.service.mutate({action: 'favorite', value});
        const accepted = opened.service.getSnapshot().data;
        if (opened.operation.abort.signal.aborted || !bound.live() || accepted?.personal.favorite !== value) return false;
        issued.delete(token);
        return true;
      } catch {return false;}
      finally {opened.close();}
    },
    dispose: () => {
      if (disposed) return;
      disposed = true;
      for (const operation of active) {operation.abort.abort(); operation.service?.dispose();}
      active.clear();
    },
  });
}

export type NativeListeningSongDetail = ReturnType<typeof createNativeListeningSongDetail>;
