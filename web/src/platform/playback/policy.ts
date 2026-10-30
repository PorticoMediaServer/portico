import type {QueuePlaybackView} from '@core/queue-controller.ts';
import {sameFence, sameScope, type QueuePlaybackFence} from '../../../../packages/player-core/src/index.ts';
import type {WebPlaybackRuntimeSnapshot} from './types.ts';

/** The rendered marker, offer or next occurrence is part of the command, so an old button
 * cannot operate a different prompt that appeared later on the same media generation. */
export type WebPlaybackPolicyAction = Readonly<
  {kind: 'skip' | 'dismiss' | 'undo'; markerId: string} |
  {kind: 'watch-credits'; offerId: string} |
  {kind: 'play-now' | 'continue'; nextEntryId: string} |
  {kind: 'cancel-autoplay'}
>;
export type WebPlaybackPolicyNext = QueuePlaybackView['items'][number] & Readonly<{
  queueId: string;
  queueRevision: string;
}>;

/** The actual queue must still describe this local video's full session and occurrence. */
export function currentPolicyQueue(snapshot: WebPlaybackRuntimeSnapshot, fence: QueuePlaybackFence): QueuePlaybackView | null {
  const view = snapshot.queueView, presentation = snapshot.presentation;
  if (snapshot.disposed || !sameFence(snapshot.fence, fence) || !sameFence(presentation?.fence, fence)
    || !sameFence(snapshot.dock.local?.fence, fence) || presentation?.subject.kind !== 'video'
    || !view || !sameScope(fence.playback.scope, view.queue.scope)
    || view.queue.currentEntryId !== fence.entryId || view.queue.currentPlaybackId !== fence.sessionId
    || view.current?.id !== fence.sessionId || view.current.generation !== fence.generation) return null;
  return view;
}

/** Only a real, available next occurrence with a visible queue-window title may draw a card.
 * Artwork fields remain genuine protected-resource inputs; this does not load an image. */
export function playbackPolicyNext(snapshot: WebPlaybackRuntimeSnapshot, fence: QueuePlaybackFence): WebPlaybackPolicyNext | null {
  const view = currentPolicyQueue(snapshot, fence), next = view?.next, post = view?.postPlay;
  if (!view || !next?.available || !next.entryId || !post?.available || post.nextEntryId !== next.entryId) return null;
  const entry = view.queue.entries.find(value => value.id === next.entryId);
  const item = view.items.find(value => value.entryId === next.entryId);
  if (!entry || entry.hidden || entry.removed || !item?.title.trim() || item.itemId !== entry.itemId) return null;
  return Object.freeze({...item, queueId: view.queue.id, queueRevision: view.queue.revision});
}
