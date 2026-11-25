import type {HttpLocalApi, LocalSession, PlaybackService} from '../../../../../packages/client-core/src/index';
import {SessionsClient, type Timers} from '../../../../../packages/client-core/src/playback-v1/sessions';
import type {Session} from '../../../../../packages/client-core/src/playback-v1/types';
import type {V1QueuePlayer} from '../../../../../packages/client-core/src/playback-v1/v1-queue-player';
import {sameFence, type PlaybackFence} from '../../../../../packages/player-core/src';
import {createNativeListeningReadPort, createNativeOfflineListeningReadPort, nativeOfflineListeningData} from './listening-controls';
import type {NativeOfflineSource} from './offline-playback';
import {sameBindingFact} from './runtime-external';
import type {PlaybackScopeHttp} from './scope-http';
import type {NativeListeningReadPort, NativePlaybackReadLifetime, NativePlaybackSettings, PlaybackAuthority} from './types';

const authorityKey = (value: PlaybackAuthority) => JSON.stringify([value.serverId, value.accountId, value.profileId, value.epoch, value.privacyFence ?? value.viewerFence]);

export type NativeListeningReadHost = Readonly<{
  api: HttpLocalApi;
  http: PlaybackScopeHttp;
  timers?: Timers;
  bound: PlaybackAuthority;
  playback: PlaybackService;
  queue: V1QueuePlayer;
  authority: () => PlaybackAuthority;
  session: () => LocalSession | undefined;
  settings: () => NativePlaybackSettings;
  subscribe: (listener: () => void) => () => void;
  subscribeAuthority: (listener: () => void) => () => void;
  subscribeSettings: (listener: () => void) => () => void;
  currentAuthority: () => boolean;
  startEpoch: () => number;
  matches: (fence: PlaybackFence) => boolean;
  fence: () => PlaybackFence | undefined;
  currentSession: () => Session | undefined;
  offlineSource: () => NativeOfflineSource | undefined;
  chosenRate: () => number;
  supportedRates: () => readonly number[];
  selectionScope: (signal: AbortSignal, current: () => boolean) => Readonly<{signal: AbortSignal; close: () => void}>;
  request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => Promise<T>;
}>;

/** Info reads borrow the actual queue occurrence. Unlike a replan, a read must
 * remain on the captured generation/revision and must never adopt its reply. */
export async function readPlaybackSession(host: NativeListeningReadHost, fence: PlaybackFence, lifetime: NativePlaybackReadLifetime): Promise<Session | undefined> {
  if (fence.kind !== 'queue' || !host.matches(fence) || lifetime.signal.aborted || !lifetime.isCurrent()) return;
  const original = host.currentSession(), queue = host.queue.getSnapshot().view?.queue, start = host.startEpoch(), route = host.api.getRouteSnapshot();
  if (!original || original.kind !== 'vod' || original.role !== 'local' || original.state === 'ended' || original.end || !queue || original.queue?.queueId !== queue.id
    || original.queue.entryId !== fence.entryId || original.versionId !== fence.sourceId || queue.currentEntryId !== fence.entryId
    || queue.currentPlaybackId !== original.id || route && route.phase !== 'ready') return;
  const current = () => {
    const session = host.currentSession(), live = host.fence(), heldQueue = host.queue.getSnapshot().view?.queue, liveRoute = host.api.getRouteSnapshot();
    return host.currentAuthority() && host.startEpoch() === start && !lifetime.signal.aborted && lifetime.isCurrent()
      && !!live && sameFence(live, fence) && session?.id === original.id && session.presentation.generation === original.presentation.generation
      && heldQueue?.id === queue.id && heldQueue.revision === queue.revision && heldQueue.currentEntryId === fence.entryId
      && heldQueue.currentPlaybackId === original.id && (route ? liveRoute?.phase === 'ready' && liveRoute.revision === route.revision && liveRoute.origin === route.origin : liveRoute === null);
  };
  const scoped = host.selectionScope(lifetime.signal, current);
  const unroute = host.api.subscribeRoute(() => {if (!current()) scoped.close();});
  try {
    if (!current()) return;
    const fresh = await new SessionsClient(host.http, host.timers).get(original.id, scoped.signal);
    if (!current() || fresh.id !== original.id || fresh.kind !== 'vod'
      || fresh.role !== original.role || fresh.state === 'ended' || fresh.end
      || fresh.itemId !== original.itemId || fresh.versionId !== original.versionId
      || fresh.presentation.generation !== fence.generation || fresh.queue?.queueId !== queue.id
      || fresh.queue.entryId !== fence.entryId) return;
    return fresh;
  } catch (error) {
    if (scoped.signal.aborted || !current()) return;
    throw error;
  } finally {unroute(); scoped.close();}
}

/** Read-only audiobook facts. Mutation signatures remain with Media's accepting owner API. */
export function createRuntimeListeningRead(host: NativeListeningReadHost, fence: PlaybackFence, lifetime: NativePlaybackReadLifetime): NativeListeningReadPort | undefined {
  if (fence.kind === 'local') {
    const source = host.offlineSource(), libraryId = source?.metadata.libraryId;
    if (!source || source.metadata.kind !== 'audiobook_file' || !libraryId || !host.matches(fence) || !source.current()
      || lifetime.signal.aborted || !lifetime.isCurrent()) return;
    const current = () => host.currentAuthority() && host.offlineSource() === source && source.current() && host.matches(fence);
    return createNativeOfflineListeningReadPort({
      lifetime, current,
      data: nativeOfflineListeningData(fence, libraryId, source.media.durationSeconds, source.media.extras?.bundle?.chapters ?? []),
      facts: () => ({rate: {selected: host.chosenRate(), supported: host.supportedRates()},
        preferences: {phase: 'ready', values: null, audiobook: host.settings().audiobook}}),
      subscribe: [host.subscribe, host.subscribeAuthority, host.subscribeSettings],
    });
  }
  if (fence.kind !== 'queue' || !host.matches(fence) || lifetime.signal.aborted || !lifetime.isCurrent()) return;
  const original = host.currentSession(), principal = host.session(), context = host.queue.listening.getSnapshot().context, occurrence = original?.queue;
  const native = host.playback.getSnapshot().nativeListening, connection = host.api.getRouteConnection(), route = host.api.getRouteSnapshot();
  if (!original || original.kind !== 'audio' || original.role !== 'local' || original.state === 'ended' || original.end
    || original.id !== fence.sessionId || original.itemId !== fence.itemId || original.versionId !== fence.sourceId
    || original.presentation.generation !== fence.generation || !occurrence || occurrence.entryId !== fence.entryId
    || !principal?.sessionFamilyId || !context || context.kind !== 'audiobook_file' || context.itemId !== fence.itemId
    || !native?.binding || native.terminal || native.binding.sessionId !== fence.sessionId || native.binding.generation !== fence.generation || native.binding.itemId !== fence.itemId
    || !connection || route?.phase !== 'ready') return;
  const captured = Object.freeze({...fence, playback: Object.freeze({...fence.playback, scope: Object.freeze({...fence.playback.scope})})});
  const binding = Object.freeze({...native.binding}), owner = native.owner, family = principal.sessionFamilyId;
  const viewerKey = (value: LocalSession) => JSON.stringify([value.viewer.authority, value.viewer.serverId, value.viewer.accountId, value.viewer.profileId]);
  const originalViewer = viewerKey(principal);
  const authority = authorityKey(host.bound), start = host.startEpoch(), libraryId = context.libraryId, queueId = occurrence.queueId;
  const current = () => {
    const live = host.fence(), session = host.currentSession(), viewer = host.session(), details = host.queue.listening.getSnapshot();
    const heard = host.playback.getSnapshot().nativeListening, liveRoute = host.api.getRouteSnapshot();
    return host.currentAuthority() && authorityKey(host.authority()) === authority && host.startEpoch() === start
      && !lifetime.signal.aborted && lifetime.isCurrent() && !!viewer && viewer.sessionFamilyId === family && viewerKey(viewer) === originalViewer
      && !!live && sameFence(live, captured) && !!session && session.id === original.id && session.kind === 'audio' && session.role === original.role
      && session.state !== 'ended' && !session.end && session.itemId === captured.itemId && session.versionId === captured.sourceId
      && session.presentation.generation === captured.generation && session.queue?.queueId === queueId && session.queue.entryId === captured.entryId
      && details.phase !== 'disposed' && details.context?.kind === 'audiobook_file' && details.context.itemId === captured.itemId && details.context.libraryId === libraryId
      && !!heard && heard.owner === owner && !heard.terminal && sameBindingFact(heard.binding, binding)
      && host.api.getRouteConnection() === connection && liveRoute?.phase === 'ready' && liveRoute.revision === route.revision && liveRoute.origin === route.origin;
  };
  const port = createNativeListeningReadPort({
    target: {libraryId, itemId: captured.itemId, sessionId: captured.sessionId, sessionGeneration: captured.generation, sourceId: captured.sourceId},
    scope: {serverId: host.bound.serverId, viewerId: JSON.stringify([principal.viewer.authority, principal.viewer.accountId, principal.viewer.profileId, family, host.bound.epoch])},
    lifetime, current, request: host.request,
    facts: () => {
      if (!current()) throw new Error('native_listening_read_retired');
      const heard = host.playback.getSnapshot().nativeListening!, details = host.queue.listening.getSnapshot(), audiobook = host.settings().audiobook;
      return {
        rate: {selected: heard.rate, ...(heard.effectiveRate === undefined ? {} : {effective: heard.effectiveRate}), ...(heard.supportedRates === undefined ? {} : {supported: heard.supportedRates})},
        preferences: {phase: details.phase, values: details.preferences, ...(audiobook ? {audiobook} : {})},
      };
    },
    subscribe: [host.subscribe, listener => host.playback.subscribe(listener), listener => host.queue.listening.subscribe(listener),
      listener => host.api.subscribeRoute(listener), host.subscribeAuthority, host.subscribeSettings],
  });
  if (!port.current()) {port.dispose(); return;}
  return port;
}
