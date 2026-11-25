import {readPlaybackItem} from '@core/playback-v1/item.ts';
import type {RoomSessionService as GroupSessionService} from '@core/room-session.ts';
import {roomEntry} from '@core/room-protocol.ts';
import type {PlaybackFence} from '../../../../packages/player-core/src/index.ts';
import {bindRuntimeGroup, type RuntimeGroupBinding} from './group-binding.ts';
import type {WebPlaybackRuntimeSnapshot, WebPlaybackScreenLifetime, WebPlaybackSource} from './types.ts';
import {problem, validSeconds, type GroupTransportCommand, type Stack} from './runtime-types.ts';

export type TogetherRuntimeHost = Readonly<{
  stack(): Stack;
  source(): WebPlaybackSource;
  life(): AbortController;
  snapshot(): WebPlaybackRuntimeSnapshot;
  group(): RuntimeGroupBinding | undefined;
  setGroup(binding: RuntimeGroupBinding | undefined): void;
  setDetachGroup(detach: (() => void) | undefined): void;
  groupServices(): WeakMap<RuntimeGroupBinding, GroupSessionService>;
  groupRate(): number;
  setGroupRate(rate: number): void;
  nextOperation(): number;
  operation(): number;
  assertCurrent(): void;
  current(stack?: Stack): boolean;
  matches(fence: PlaybackFence): boolean;
  require(fence: PlaybackFence): Stack;
  retainMediaStart(signal?: AbortSignal, isCurrent?: () => boolean): () => void;
  replace(mode?: Stack['mode']): Stack;
  sync(): void;
}>;

/** Canonical room follower and local corrections share this runtime's one actual engine. */
export function bindRuntimeTogether(host: TogetherRuntimeHost, service: GroupSessionService, isCurrent: () => boolean, reportError: (error: unknown) => void): () => void {
  host.assertCurrent();
  if (host.group()) problem('group_playback_already_bound');
  const currentGroup = () => {const value = service.getSnapshot();return host.current() && isCurrent() && (value.phase === 'live' || value.phase === 'reconnecting') ? value.group : undefined;};
  let activeGroupId = currentGroup()?.id;
  const owner = bindRuntimeGroup({service, isCurrent: () => host.current() && isCurrent(), reportError, changed: host.sync,
    player: {
      state: () => {const value = host.snapshot().playback;return {itemId: value.itemId ?? '', positionSeconds: value.positionSeconds,
        playing: value.phase === 'ready' && value.intent === 'playing' && !value.audioEffects.waiting, ready: value.phase === 'ready',
        buffering: value.phase === 'starting' || !!value.recovering || !!value.audioEffects.waiting, seeking: !!value.pendingSeek,
        ...(value.duration > 0 ? {durationSeconds: value.duration} : {})};},
      play: () => {const live = currentGroup(); if (live && roomEntry(live)?.itemId === host.stack().service.getSnapshot().itemId) host.stack().service.resume();},
      pause: () => {const live = currentGroup(); if (live && roomEntry(live)?.itemId === host.stack().service.getSnapshot().itemId) host.stack().service.pause();},
      seek: seconds => {const live = currentGroup(); if (live && roomEntry(live)?.itemId === host.stack().service.getSnapshot().itemId) host.stack().service.seek(seconds);},
      setRate: rate => {if (host.current() && Number.isFinite(rate) && rate >= 0.25 && rate <= 4) {host.setGroupRate(rate);host.stack().service.setPlaybackRate(rate);host.stack().web.setRate(rate);host.sync();}},
    },
    load: (itemId, seconds, playing, context) => playGroupItem(host, itemId, seconds, playing, context),
  });
  host.setGroup(owner);
  host.groupServices().set(owner, service);
  const off = service.subscribe(() => {
    const nextGroupId = owner.active()?.id;
    // Reset drifted catch-up playbackRate whenever leaving a room or switching rooms.
    if (!nextGroupId || nextGroupId !== activeGroupId) host.setGroupRate(1);
    activeGroupId = nextGroupId;
    host.sync();
  });
  host.sync();
  const detach = () => {
    if (host.group() !== owner) return;
    off();
    host.setGroup(undefined);
    host.setDetachGroup(undefined);
    owner.dispose();
    host.setGroupRate(1);
    if (host.current()) host.sync();
  };
  host.setDetachGroup(detach);
  return detach;
}

async function playGroupItem(host: TogetherRuntimeHost, itemId: string, seconds: number, playing: boolean, context: WebPlaybackScreenLifetime): Promise<void> {
  host.assertCurrent();
  if (!/^[A-Za-z0-9_.-]{1,128}$/.test(itemId) || !validSeconds(seconds) || context.signal.aborted || !context.isCurrent()) problem('stale_selection_context');
  const serial = host.nextOperation(), previous = host.stack();
  const finishRetention = host.retainMediaStart(context.signal, () => serial === host.operation());
  try {
    const item = await readPlaybackItem(host.source().api, itemId, AbortSignal.any([context.signal, previous.life.signal, host.life().signal]));
    if (!host.current(previous) || serial !== host.operation() || context.signal.aborted || !context.isCurrent()) problem('stale_selection_context');
    if (item.id !== itemId || !item.available) problem('invalid_playback_target');
    // Music starts docked once per room; after that each song keeps Now playing as the listener left it.
    const room = host.group()?.active()?.id, carried = room !== undefined && previous.groupRoom === room && previous.placed && !previous.retired ? previous.expanded : undefined;
    const stack = host.replace();stack.itemId = item.id;stack.libraryId = item.libraryId;stack.subject = item.subject;stack.posterUrl = item.posterUrl;stack.backdropUrl = item.backdropUrl;
    if (room !== undefined) stack.groupRoom = room;
    if (carried === undefined) stack.expanded = stack.subject.kind !== 'music';
    else {stack.expanded = carried;stack.placed = true;}
    const signal = AbortSignal.any([context.signal, stack.life.signal, host.life().signal]);
    const current = () => !signal.aborted && host.current(stack) && serial === host.operation() && context.isCurrent();
    const cancel = () => {if (host.current(stack) && serial === host.operation()) stack.service.leave();};
    signal.addEventListener('abort', cancel, {once: true});
    try {
      stack.capacityCurrent = current;
      await stack.queue.playItem(itemId, seconds, undefined, undefined, undefined, undefined, {signal, isCurrent: current, state: playing ? 'playing' : 'paused'});
      if (!current()) problem('stale_selection_context');
      if (stack.queue.currentSession()?.itemId !== itemId || stack.service.getSnapshot().phase === 'error') problem('group_playback_failed');
      host.sync();
    } catch (error) {cancel();throw error;} finally {if (stack.capacityCurrent === current) stack.capacityCurrent = undefined;signal.removeEventListener('abort', cancel);}
  } finally {finishRetention();}
}

export async function groupTransport(host: TogetherRuntimeHost, command: GroupTransportCommand, fence: PlaybackFence, seconds?: number): Promise<boolean> {
  const groupBinding = host.group();
  if (!groupBinding?.matches(fence)) return false;
  host.require(fence);
  const group = groupBinding.active();if (!group?.you.mayControl) problem('group_control_forbidden');
  const service = host.groupServices().get(groupBinding);if (!service) problem('group_playback_unavailable');
  if (command === 'next' || command === 'previous') {
    const index = group.queue.findIndex(entry => entry.entryId === group.clock.entryId);
    const entries = command === 'next' ? group.queue.slice(index + 1) : group.queue.slice(0, index).reverse();
    const next = entries.find(entry => entry.available && entry.itemId);
    if (!next || !await service.transport('watch', {entryId: next.entryId})) problem('group_transport_refused');
  } else if (!await service.transport(command === 'play' && group.clock.waitUntil ? 'goAhead' : command,
    seconds === undefined ? {} : {positionMs: Math.round(seconds * 1000)})) problem('group_transport_refused');
  return true;
}

/** Current room occurrence only; equal title IDs do not prove a room's source was adopted. */
export function groupPlaybackMatches(host: TogetherRuntimeHost, fence: PlaybackFence, groupId: string, entryId: string): boolean {
  const groupBinding = host.group(), active = groupBinding?.active();
  return host.matches(fence) && !!groupBinding?.matches(fence) && active?.id === groupId && active.clock.entryId === entryId;
}

/** Routes unsolicited play/pause from media keys or Picture-in-Picture through the room
 * controller while in Watch Together, preventing local-only drift. */
export function handleExternalTransport(host: TogetherRuntimeHost, stack: Stack, state: 'paused' | 'playing'): void {
  if (!host.current(stack)) return;
  const fence = host.snapshot()?.fence, groupBinding = host.group();
  if (fence && groupBinding?.matches(fence)) {
    const group = groupBinding.active();
    if (!group?.you.mayControl) {
      // Viewer lacks control: immediately re-apply the room's current intent to the media port.
      stack.web.apply(stack.service.getSnapshot());
      return;
    }
    void groupTransport(host, state === 'paused' ? 'pause' : 'play', fence).catch(() => {
      if (host.current(stack)) {
        stack.web.apply(stack.service.getSnapshot());
        host.sync();
      }
    });
    return;
  }
  if (state === 'paused') stack.service.pause();
  else stack.service.resume();
}
