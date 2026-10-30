import {DetailService, type DetailSnapshot} from '@core/detail.ts';
import {sameFence, type PlaybackFence} from '../../../../../packages/player-core/src/index.ts';
import type {WebPlaybackRuntimeSnapshot, WebShellPlaybackRuntime} from '../../../platform/playback';
import type {WatchPlaybackContext} from '../play-on-binding';
import {musicQueueItem} from './queue-facts';

export type MusicFavoriteSource = Readonly<{
  context: WatchPlaybackContext; runtime: WebShellPlaybackRuntime; authority: WebPlaybackRuntimeSnapshot['authority'];
  fence: PlaybackFence; run: number; libraryId: string; itemId: string; isCurrent?: () => boolean;
}>;

/** One expansion's canonical personal-state reader/writer. No optimistic favorite or raw PUT. */
export function createMusicFavorite(source: MusicFavoriteSource) {
  const {context, runtime, authority} = source, life = new AbortController();
  const original = {...context.session()}, pin = context.api.getRouteConnection()?.pin?.publicKey, origin = new URL(context.api.baseUrl).origin;
  let service: DetailService | undefined;
  const dispose = () => {if (!life.signal.aborted) {life.abort();service?.dispose();}};
  const matches = () => {
    const session = context.session(), viewer = session.viewer, snapshot = runtime.getSnapshot(), item = musicQueueItem(snapshot);
    return !life.signal.aborted && context.isCurrent() && (!source.isCurrent || source.isCurrent())
      && context.epoch === authority.epoch && new URL(context.api.baseUrl).origin === origin && context.api.getRouteConnection()?.pin?.publicKey === pin
      && session.sessionFamilyId === original.sessionFamilyId && session.deviceId === original.deviceId
      && viewer.serverId === authority.serverId && viewer.authority === authority.authority && viewer.accountId === authority.accountId && viewer.profileId === authority.profileId
      && !snapshot.disposed && snapshot.authority === authority && sameFence(snapshot.fence, source.fence)
      && snapshot.playback.playbackRun === source.run && snapshot.presentation?.kind === 'music' && snapshot.presentation.expanded
      && sameFence(snapshot.presentation.fence, source.fence) && !snapshot.dock.remote && snapshot.dock.local?.kind === 'music'
      && snapshot.dock.local.expanded && sameFence(snapshot.dock.local.fence, source.fence)
      && !!item && item.libraryId === source.libraryId && item.itemId === source.itemId;
  };
  const current = () => {
    if (life.signal.aborted) return false;
    let accepted = false;
    try {accepted = matches() && matches();} catch { /* A retired authority offers no favorite action. */ }
    if (!accepted || life.signal.aborted) {dispose();return false;}
    return true;
  };
  const assert = () => {if (!current()) throw new DOMException('Music favorite opening retired', 'AbortError');};
  const api = {async acknowledgeUserMutation(intentId: string): Promise<void> {
    assert();await context.api.acknowledgeUserMutation?.(intentId);assert();
  }, async request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> {
    assert();const active = signal ? AbortSignal.any([life.signal, signal]) : life.signal;active.throwIfAborted();
    const result = await context.api.request<T>(path, method, body, active);
    assert();active.throwIfAborted();return result;
  }};
  service = new DetailService({api, scope: {serverId: authority.serverId, viewerId: JSON.stringify([authority.authority, authority.accountId, authority.profileId])},
    requestId: async () => {assert();return crypto.randomUUID();}});
  const owned = service;
  const admit = (shown: DetailSnapshot) => {assert();if (owned.getSnapshot() !== shown) throw new DOMException('Favorite detail changed', 'AbortError');assert();};
  return {source, current, dispose, getSnapshot: owned.getSnapshot, subscribe: owned.subscribe,
    load: async () => {assert();await owned.select({libraryId: source.libraryId, itemId: source.itemId});},
    toggle: async (shown: DetailSnapshot) => {
      admit(shown);
      if (shown.phase !== 'ready' || !shown.data || shown.pending.length || shown.mutationError || !shown.data.actions.some(action => action.id === 'favorite' && action.enabled)) return;
      await owned.mutate({action: 'favorite', value: !shown.data.personal.favorite});
    },
    retry: async (shown: DetailSnapshot) => {admit(shown);if (shown.mutationError) await owned.retryMutation();else await owned.retry();},
  };
}
export type MusicFavoriteBinding = ReturnType<typeof createMusicFavorite>;


/** DetailService presents its own failures; only unexpected opening rejections need a toast. */
export function reportMusicFavoriteLoadFailure(owner: Pick<MusicFavoriteBinding, 'getSnapshot'>, error: unknown, reportError: (error: unknown) => void): void {
  if (error && typeof error === 'object' && 'name' in error && error.name === 'AbortError') return;
  const state = owner.getSnapshot();
  if (state.error && error === state.error || state.mutationError && error === state.mutationError) return;
  reportError(error);
}
