import {defaultI18n} from '../../../../../packages/i18n/src';
import {RoomFollower, type RoomPlayer} from '../../../../../packages/client-core/src/room-session';
import {roomEntry, roomPositionMs} from '../../../../../packages/client-core/src/room-protocol';
import {contentEntryKind, type ContentEntry} from '../../../../../packages/client-core/src/library-content';
import {parseMediaItem} from '../../../../../packages/client-core/src/media-item';
import {readTogetherPlaybackItem} from '../../../../../packages/client-core/src/query/receiver';
import {playbackUserMessage} from '../../../../../packages/client-core/src/playback-messages';
import {sameFence, sameScope, type PlaybackFence, type QueueScope} from '../../../../../packages/player-core/src';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {sameAuthority} from '../../shell/model';
import {nativeTogetherSession} from './together-session';
import {publishGroupPort} from './group-port';

export type NativeTogetherPlaybackContext = Pick<NativeShellBootstrapContext, 'api' | 'authority' | 'current' | 'session' | 'subscribeAuthority' | 'events'> & Readonly<{
  playback: Pick<NativeShellBootstrapContext['playback'], 'getSnapshot' | 'start' | 'toggle' | 'seek' | 'skip' | 'next' | 'previous' | 'replay' | 'queuePlay' | 'setRate' | 'open'>;
  navigation: Pick<NativeShellBootstrapContext['navigation'], 'getSnapshot' | 'subscribe'>;
}>;
const owners = new WeakMap<NativeTogetherPlaybackContext, () => void>();

/** One authority-owned follower on the existing native player. The direct ports
 * below bypass the viewer-command policy so clock corrections never echo to the room. */
export function bindNativeTogether(context: NativeTogetherPlaybackContext, television: boolean): () => void {
  const existing = owners.get(context);
  if (existing) return existing;
  const service = nativeTogetherSession(context), playback = context.playback;
  const direct = {toggle: playback.toggle, seek: playback.seek, skip: playback.skip, next: playback.next, previous: playback.previous, replay: playback.replay, queuePlay: playback.queuePlay, setRate: playback.setRate, open: playback.open};
  const presentationListeners = new Set<() => void>();
  let playerOpen = false;
  const emitPresentation = () => {for (const listener of [...presentationListeners]) listener();};
  const playerVisible = () => !television || context.navigation.getSnapshot().route.kind !== 'together' || playerOpen;
  const closePlayer = () => {if (!television || !context.current() || !playerOpen || context.navigation.getSnapshot().route.kind !== 'together') return false;playerOpen = false;emitPresentation();return true;};
  let pendingCurrent: (() => boolean) | undefined;
  let disposed = false, follower: RoomFollower | undefined, pending: AbortController | undefined;
  let local: {groupId: string; viewer: string; entryId: string; itemId: string; run: number; queueId: string; localEntry: string; sessionId: string; scope: QueueScope} | undefined;
  const room = () => {const state = service.getSnapshot();return context.current() && (state.phase === 'live' || state.phase === 'reconnecting') ? state.group : undefined;};
  // The fence names a session/entry, not a queue. Borrow the queue identity only
  // when this runtime's session, current queue and visible occurrence agree.
  const boundOccurrence = () => {
    if (!context.current()) return;
    const actual = playback.getSnapshot(), fence = actual.fence, session = actual.session, queue = actual.queue.view?.queue;
    if (actual.retired || !sameAuthority(actual.authority, context.authority()) || fence?.kind !== 'queue' || !session || !queue
      || !sameScope(queue.scope, fence.playback.scope) || fence.playback.run !== actual.playback.playbackRun
      || session.role !== 'local' || session.state === 'ended' || session.end
      || session.id !== fence.sessionId || session.presentation.generation !== fence.generation || session.versionId !== fence.sourceId
      || actual.playback.session?.id !== session.id || actual.playback.session.generation !== fence.generation
      || session.itemId !== fence.itemId || actual.playback.itemId !== fence.itemId
      || session.queue?.queueId !== queue.id || session.queue.entryId !== fence.entryId
      || queue.currentEntryId !== fence.entryId || queue.currentPlaybackId !== session.id) return;
    const entry = queue.entries.find(value => value.id === fence.entryId);
    if (!entry || entry.hidden || entry.removed || entry.itemId !== fence.itemId) return;
    return {actual, fence, queue};
  };
  const ownsLocal = (bound: ReturnType<typeof boundOccurrence>) => !!local && !!bound
    && bound.actual.playback.playbackRun === local.run && bound.fence.itemId === local.itemId
    && bound.queue.id === local.queueId && bound.fence.entryId === local.localEntry && bound.fence.sessionId === local.sessionId
    && sameScope(bound.fence.playback.scope, local.scope);
  const matches = (fence?: PlaybackFence) => {
    const group = room(), bound = boundOccurrence();
    return !!local && !!group && group.id === local.groupId && group.you.memberId === local.viewer && group.clock.entryId === local.entryId
      && roomEntry(group)?.itemId === local.itemId && !!bound && ownsLocal(bound) && (!fence || sameFence(fence, bound.fence));
  };
  const command = async (fence: PlaybackFence, work: () => Promise<unknown>, fallback: () => Promise<void>) => {
    if (!matches(fence)) {await fallback();return;}
    const group = room();if (!group) return;
    if (!group.you.mayControl) {service.notice(defaultI18n.t('together.hostOnly.playPause'));return;}
    await work();
  };
  playback.open = fence => {if (television && matches(fence)) {playerOpen = true;emitPresentation();}direct.open(fence);};
  playback.toggle = fence => command(fence, () => service.transport(room()?.clock.waitUntil ? 'goAhead' : playback.getSnapshot().playback.intent === 'playing' ? 'pause' : 'play'), () => direct.toggle(fence));
  playback.seek = (fence, seconds) => command(fence, () => service.transport('seek', {positionMs: Math.round(Math.max(0, seconds) * 1000)}), () => direct.seek(fence, seconds));
  playback.skip = (fence, seconds) => command(fence, () => service.transport('seek', {positionMs: Math.round(Math.max(0, playback.getSnapshot().playback.positionSeconds + seconds) * 1000)}), () => direct.skip(fence, seconds));
  const step = async (direction: 1 | -1) => {
    const group = room();if (!group) return;
    const index = group.queue.findIndex(entry => entry.entryId === group.clock.entryId);
    const entries = direction === 1 ? group.queue.slice(index + 1) : group.queue.slice(0, Math.max(0, index)).reverse();
    const entry = entries.find(value => value.available && value.itemId);
    if (entry) await service.transport('watch', {entryId: entry.entryId});
  };
  playback.next = fence => command(fence, () => step(1), () => direct.next(fence));
  playback.previous = fence => command(fence, () => step(-1), () => direct.previous(fence));
  playback.replay = fence => command(fence, () => service.transport('seek', {positionMs: 0}), () => direct.replay(fence));
  if (direct.queuePlay) playback.queuePlay = (queueId, entryId, revision) => {
    const value = playback.getSnapshot();
    // A device queue occurrence is not a group queue entry. Never translate its
    // position or ID into another room's queue command.
    if (value.fence && matches(value.fence)) return Promise.reject(new Error('together_queue_requires_room_entry'));
    return direct.queuePlay!(queueId, entryId, revision);
  };
  playback.setRate = (fence, rate) => {if (matches(fence)) service.notice(defaultI18n.t('together.hostOnly.move'));else direct.setRate(fence, rate);};
  const player: RoomPlayer = {
    occurrence: () => matches() && local ? {groupId: local.groupId, entryId: local.entryId} : null,
    state: () => {const value = playback.getSnapshot().playback;if (!matches()) return {itemId: '', positionSeconds: 0, playing: false, buffering: true, ready: false};return {itemId: matches() ? value.itemId ?? '' : '', positionSeconds: value.positionSeconds,
      playing: value.phase === 'ready' && value.intent === 'playing', buffering: value.phase === 'starting' || !!value.recovering,
      ready: matches() && value.phase === 'ready', seeking: !!value.pendingSeek, durationSeconds: value.duration > 0 ? value.duration : undefined};},
    load(itemId) {
      const group = room();if (!group || !group.clock.entryId || roomEntry(group)?.itemId !== itemId || disposed) return;
      pending?.abort();const abort = new AbortController();pending = abort;
      const original = {groupId: group.id, viewer: group.you.memberId, entryId: group.clock.entryId, itemId};
      const current = () => {const live = room();return !disposed && !abort.signal.aborted && pending === abort && live?.id === original.groupId
        && live.you.memberId === original.viewer && live.clock.entryId === original.entryId && roomEntry(live)?.itemId === itemId;};
      pendingCurrent = current;
      void (async () => {
        const item = parseMediaItem(await readTogetherPlaybackItem(context.api, itemId, abort.signal));
        if (!current()) return;
        if (item.id !== itemId || !item.available || !contentEntryKind(item.kind)) throw new Error('together_media_unavailable');
        const entry: ContentEntry = {...item, kind: item.kind, playback: {itemId}};
        const latest = room()!;
        await playback.start({entry, target: {itemId, startSeconds: roomPositionMs(latest, service.serverNow()) / 1000}, startIntent: 'explicit',
          state: latest.clock.playing && !latest.clock.waitUntil ? 'playing' : 'paused', lifetime: {signal: abort.signal, isCurrent: current}});
        if (!current()) return;
        const bound = boundOccurrence();
        if (!bound || bound.fence.itemId !== itemId) return;
        const {actual: value, fence, queue} = bound;
        local = {...original, run: value.playback.playbackRun, queueId: queue.id, localEntry: fence.entryId, sessionId: fence.sessionId, scope: fence.playback.scope};
        direct.setRate(fence, 1);
        // A pause or seek may have arrived while the engine opened the source.
        const latestRoom = room()!;
        await direct.seek(fence, roomPositionMs(latestRoom, service.serverNow()) / 1000);
        if (!current() || !matches(fence)) return;
        const live = room()!, shouldPlay = live.clock.playing && !live.clock.waitUntil;
        if ((playback.getSnapshot().playback.intent === 'playing') !== shouldPlay) await direct.toggle(fence);
      })().catch(error => {if (current() && !(error instanceof Error && error.name === 'AbortError')) service.notice(playbackUserMessage(error));});
    },
    play() {const value = playback.getSnapshot();if (matches() && value.fence && value.playback.intent !== 'playing') void direct.toggle(value.fence).catch(() => {});},
    pause() {const value = playback.getSnapshot();if (matches() && value.fence && value.playback.intent === 'playing') void direct.toggle(value.fence).catch(() => {});},
    seek(seconds) {const value = playback.getSnapshot();if (matches() && value.fence) void direct.seek(value.fence, seconds).catch(() => {});},
    // This is the existing per-playback choice, not a saved preference. The
    // native runtime accepts only rates the actual bound engine supports.
    setRate(rate) {const value = playback.getSnapshot();if (value.fence && (matches() || rate === 1 && ownsLocal(boundOccurrence()))) direct.setRate(value.fence, rate);},
  };
  const update = () => {
    if (disposed) return;
    const group = room();
    if (pending && pendingCurrent?.() === false) pending.abort();
    if (local && (!group || group.id !== local.groupId || group.you.memberId !== local.viewer || group.clock.entryId !== local.entryId || roomEntry(group)?.itemId !== local.itemId)) {
      const value = playback.getSnapshot();
      if (ownsLocal(boundOccurrence()) && value.fence) {
        direct.setRate(value.fence, 1);
        // A private/unavailable next entry cannot leave the old group title playing.
        // Ending/leaving, however, carries on outside the group as drawn.
        if (group?.id === local.groupId && group.you.memberId === local.viewer && value.playback.intent === 'playing') void direct.toggle(value.fence).catch(() => {});
      }
      local = undefined;pending?.abort();
    }
    const entry = group && roomEntry(group), canFollow = !!entry?.available && !!entry.itemId;
    if (canFollow && !follower) {follower = new RoomFollower(service, player);follower.start();}
    else if (!canFollow && follower) {follower.stop();follower = undefined;pending?.abort();local = undefined;}
  };
  const off = service.subscribe(update), offAuthority = context.subscribeAuthority(update);
  const offNavigation = context.navigation.subscribe(() => {playerOpen = false;emitPresentation();});
  const wrapped = {toggle: playback.toggle, seek: playback.seek, skip: playback.skip, next: playback.next, previous: playback.previous, replay: playback.replay, queuePlay: playback.queuePlay, setRate: playback.setRate, open: playback.open};
  let retirePort = () => {};
  const dispose = () => {
    if (disposed) return;
    disposed = true;
    if (owners.get(context) === dispose) owners.delete(context);
    pending?.abort();follower?.stop();off();offAuthority();offNavigation();retirePort();presentationListeners.clear();
    // Retire only our wrappers. A successor owner may already have replaced one.
    for (const key of Object.keys(direct) as (keyof typeof direct)[]) {
      if (playback[key] === wrapped[key]) Object.assign(playback, {[key]: direct[key]});
    }
  };
  owners.set(context, dispose);
  const registration = publishGroupPort({getSnapshot: service.getSnapshot, subscribe: listener => {const off = service.subscribe(listener);presentationListeners.add(listener);return () => {off();presentationListeners.delete(listener);};}, current: () => !disposed && context.current(), matches, playerVisible, closePlayer,
    makeHost: memberId => {const group = room();const member = group?.members.find(value => value.id === memberId);
      if (!group || !member || !(group.you.host && !member.host && member.status === 'in_step' || memberId === group.you.memberId && group.you.mayBecomeHost)) return Promise.resolve(false);
      return service.makeHost(memberId);}});
  if (disposed) registration();
  else {retirePort = registration;update();}
  return dispose;
}
