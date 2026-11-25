import type {PlaybackService} from '../../../../../packages/client-core/src/index';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {PlaybackOptions, Session} from '../../../../../packages/client-core/src/playback-v1/types';
import {queueVideoKind, type V1QueuedEntrySelection, type V1QueueEditIntent, type V1QueuePlayer, type V1QueuePolicyIntent} from '../../../../../packages/client-core/src/playback-v1/v1-queue-player';
import {sameFence, type PlaybackDock, type PlaybackFence, type PlayerSessionChoices, type QueuePlaybackFence} from '../../../../../packages/player-core/src';
import type {PolicyCommand} from '../../../../../packages/player-core/src/playback-policy';
import {nativeVideoPolicyMarkers, nativeVideoPolicyPostPlay, type NativeVideoPolicyFrame} from './video-policy';
import type {NativePlaybackCapabilities, NativePlaybackReadLifetime, NativePlaybackSettings} from './types';

export type NativeQueueActionsHost = Readonly<{
  playback: PlaybackService;
  queue: V1QueuePlayer;
  dock: PlaybackDock;
  choices: PlayerSessionChoices;
  retired: () => boolean;
  preparing: () => boolean;
  transitionEpoch: () => number | undefined;
  expanded: () => boolean;
  effectiveRate: () => number;
  entry: () => ContentEntry | undefined;
  options: () => PlaybackOptions | undefined;
  settings: () => NativePlaybackSettings;
  capabilities: () => NativePlaybackCapabilities;
  currentAuthority: () => boolean;
  startEpoch: () => number;
  startOperationCurrent: (epoch: number) => boolean;
  beginOperation: () => number;
  matches: (fence: PlaybackFence) => boolean;
  fence: () => PlaybackFence | undefined;
  currentSession: () => Session | undefined;
  selectionScope: (signal: AbortSignal, current: () => boolean) => Readonly<{signal: AbortSignal; close: () => void}>;
  setExpanded: (expanded: boolean) => void;
  setPreparing: (preparing: boolean) => void;
  applyOptions: () => void;
  publish: () => void;
  changed: () => void;
}>;

export function buildVideoPolicyFrame(host: NativeQueueActionsHost): NativeVideoPolicyFrame | undefined {
  const state = host.playback.getSnapshot(), fence = host.fence(), queue = host.queue.getSnapshot(), view = queue.view;
  const session = host.currentSession();
  if (host.retired() || host.preparing() || host.transitionEpoch() !== undefined || !fence || fence.kind !== 'queue'
    || !session || session.kind !== 'vod' || session.role !== 'local' || !view || view.queue.currentEntryId !== fence.entryId
    || session.queue?.queueId !== view.queue.id || session.queue.entryId !== fence.entryId) return;
  const duration = Number.isFinite(state.duration) ? Math.max(0, state.duration) : 0;
  const position = Number.isFinite(state.positionSeconds) ? Math.max(0, state.positionSeconds) : 0;
  if (duration > 1e9 || position > 1e9) return;
  const options = host.options();
  const markers = nativeVideoPolicyMarkers(options?.itemId === fence.itemId ? options.markers : [], duration);
  const item = view.items.find(entry => entry.entryId === fence.entryId);
  const rate = host.effectiveRate() > 0 ? host.effectiveRate() : host.choices.get('speed', fence.playback, host.settings().videoRate);
  return {
    markers,
    frame: {
      fence, nowMs: 0, positionSeconds: duration > 0 ? Math.min(position, duration) : position,
      durationSeconds: duration, rate: Math.max(.25, Math.min(4, rate)), kind: item?.kind ?? host.entry()?.kind ?? 'movie',
      playing: state.phase === 'ready' && state.intent === 'playing' && !state.recovering && !state.pendingSeek && !state.failedSeek,
      expanded: host.expanded(), ended: state.phase === 'ended' && queue.completion?.sessionId === fence.sessionId,
      modes: host.settings().skips, postPlay: nativeVideoPolicyPostPlay(view.postPlay),
      ...(state.phase === 'error' ? {attention: 'playback-error' as const} : {}),
    },
  };
}

export async function executeVideoPolicyCommand(
  host: NativeQueueActionsHost,
  command: PolicyCommand,
  videoPolicyCurrent: (fence: QueuePlaybackFence) => boolean,
): Promise<void> {
  const fence = command.fence;
  if (!videoPolicyCurrent(fence)) return;
  if (command.kind === 'expand') {
    // changed() already reveals each new failed source once. A later Back or
    // Minimize must not be undone by another policy tick for that same error.
    if (command.reason === 'playback-error') return;
    host.setExpanded(true); host.dock.expand(fence); host.applyOptions(); host.publish(); return;
  }
  if (command.kind === 'seek') {
    const state = host.playback.getSnapshot();
    if (state.phase !== 'ready' || state.recovering || state.pendingSeek || state.failedSeek) return;
    if (command.reason !== 'undo') host.playback.markerSkipped(command.markerId, command.reason === 'automatic-skip' ? 'automatic' : 'manual', state.positionSeconds);
    host.playback.seek(command.positionSeconds); return;
  }
  const queue = host.queue.getSnapshot(), view = queue.view;
  if (!view || view.queue.currentEntryId !== fence.entryId || view.next.entryId !== command.nextEntryId || !view.next.available) return;
  if (queue.completion?.sessionId === fence.sessionId) await host.queue.continueCompletion(command.mode);
  else if (command.mode === 'automatic') await host.queue.advanceFromCredits(view.queue.id, view.queue.revision);
  else await host.queue.next(view.queue.id, view.queue.revision);
}

export async function selectQueuedEntryAction(
  host: NativeQueueActionsHost,
  fence: PlaybackFence,
  request: V1QueuedEntrySelection,
  lifetime: NativePlaybackReadLifetime,
): Promise<void> {
  if (!host.matches(fence) || fence.kind !== 'queue' || !host.capabilities().queueSelection
    || lifetime.signal.aborted || !lifetime.isCurrent()) return;
  const session = host.currentSession(), view = host.queue.getSnapshot().view, offered = view?.window;
  const entry = offered?.entries.find(row => row.entryId === request.entryId);
  if (!offered || offered.queueId !== request.queueId || offered.revision !== request.expectedRevision
    || entry?.available !== true || entry.position === undefined || !session?.queue || session.queue.queueId !== request.queueId || session.queue.entryId !== fence.entryId
    || view?.queue.id !== request.queueId || view.queue.revision !== request.expectedRevision || request.entryId === fence.entryId
    || !queueVideoKind(view.items.find(item => item.entryId === request.entryId)?.kind ?? '')) return;
  const epoch = host.beginOperation();
  // The route/authority survive this intentional source change; core retains the old output binding.
  const current = () => host.currentAuthority() && host.startOperationCurrent(epoch) && !lifetime.signal.aborted && lifetime.isCurrent();
  const scoped = host.selectionScope(lifetime.signal, current);
  host.setPreparing(true); host.publish();
  try {
    await host.queue.selectQueuedEntry({sessionId: fence.sessionId, generation: fence.generation, playbackRun: fence.playback.run,
      itemId: fence.itemId, versionId: fence.sourceId, queueId: request.queueId, entryId: fence.entryId}, request,
      {signal: scoped.signal, isCurrent: current});
  } finally {
    scoped.close();
    if (epoch === host.startEpoch()) {host.setPreparing(false); host.changed();}
  }
}

export async function editQueueAction(
  host: NativeQueueActionsHost,
  fence: PlaybackFence,
  request: Readonly<{queueId: string; expectedRevision: string; intent: V1QueueEditIntent}>,
  lifetime: NativePlaybackReadLifetime,
): Promise<void> {
  if (!host.matches(fence) || fence.kind !== 'queue' || !host.capabilities().queueEdit
    || lifetime.signal.aborted || !lifetime.isCurrent()) return;
  const session = host.currentSession(), view = host.queue.getSnapshot().view, offered = view?.window;
  if (!offered || offered.queueId !== request.queueId || offered.revision !== request.expectedRevision || !session?.queue || session.queue.queueId !== request.queueId || session.queue.entryId !== fence.entryId
    || view?.queue.id !== request.queueId || view.queue.revision !== request.expectedRevision) return;
  const epoch = host.startEpoch();
  const current = () => epoch === host.startEpoch() && host.matches(fence) && !lifetime.signal.aborted && lifetime.isCurrent();
  const scoped = host.selectionScope(lifetime.signal, current);
  try {
    await host.queue.editQueue({sessionId: fence.sessionId, generation: fence.generation, playbackRun: fence.playback.run,
      itemId: fence.itemId, versionId: fence.sourceId, queueId: request.queueId, entryId: fence.entryId}, request.expectedRevision, request.intent,
      {signal: scoped.signal, isCurrent: current});
  } finally {scoped.close();}
}

export async function setQueuePolicyAction(
  host: NativeQueueActionsHost,
  fence: PlaybackFence,
  request: Readonly<{queueId: string; expectedRevision: string; intent: V1QueuePolicyIntent}>,
  lifetime: NativePlaybackReadLifetime,
): Promise<void> {
  if (!host.matches(fence) || fence.kind !== 'queue' || !host.capabilities().queuePolicy
    || lifetime.signal.aborted || !lifetime.isCurrent()) return;
  const session = host.currentSession(), view = host.queue.getSnapshot().view;
  if (!session?.queue || session.queue.queueId !== request.queueId || session.queue.entryId !== fence.entryId
    || view?.queue.id !== request.queueId || view.queue.revision !== request.expectedRevision) return;
  const epoch = host.startEpoch();
  const current = () => epoch === host.startEpoch() && host.matches(fence) && !lifetime.signal.aborted && lifetime.isCurrent();
  const scoped = host.selectionScope(lifetime.signal, current);
  try {
    await host.queue.setQueuePolicy({sessionId: fence.sessionId, generation: fence.generation, playbackRun: fence.playback.run,
      itemId: fence.itemId, versionId: fence.sourceId, queueId: request.queueId, entryId: fence.entryId}, request.expectedRevision, request.intent,
      {signal: scoped.signal, isCurrent: current});
  } finally {scoped.close();}
}

export async function retryFailedQueueAction(
  host: NativeQueueActionsHost,
  fence: PlaybackFence,
  lifetime: NativePlaybackReadLifetime,
): Promise<void> {
  if (!host.matches(fence) || fence.kind !== 'queue' || host.preparing() || lifetime.signal.aborted || !lifetime.isCurrent()) return;
  const session = host.currentSession(), state = host.playback.getSnapshot();
  if (state.phase !== 'error' || session?.kind !== 'vod' || session.queue?.entryId !== fence.entryId) return;
  const wantedIntent = host.playback.recoveryIntent();
  const epoch = host.beginOperation();
  // The core owns the intentional output replacement; the original screen and authority own this operation.
  const current = () => host.currentAuthority() && epoch === host.startEpoch() && !lifetime.signal.aborted && lifetime.isCurrent();
  const scoped = host.selectionScope(lifetime.signal, () => {
    const live = host.playback.getSnapshot();
    return current() && live.intent === wantedIntent && live.playbackRun === fence.playback.run && live.session?.id === fence.sessionId
      && live.session.generation === fence.generation;
  });
  host.setPreparing(true); host.publish();
  try {
    await host.queue.retryFailed({sessionId: fence.sessionId, generation: fence.generation, playbackRun: fence.playback.run,
      itemId: fence.itemId, versionId: fence.sourceId, queueId: session.queue.queueId, entryId: fence.entryId},
      {signal: scoped.signal, isCurrent: current});
  } finally {
    scoped.close();
    if (epoch === host.startEpoch()) {host.setPreparing(false); host.changed();}
  }
}

export function videoPolicyCurrentFor(host: Pick<NativeQueueActionsHost, 'currentAuthority' | 'fence'>, fence: QueuePlaybackFence): boolean {
  const live = host.fence();
  return host.currentAuthority() && !!live && sameFence(live, fence);
}
