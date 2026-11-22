import type {QueuePlaybackView} from '../../../../../packages/client-core/src/queue-controller';

/** Whether a title that has ended shows its own end screen ("Finished", Replay, Close): only when
 * nothing is queued after it, and only while it is still the queue's current item. When Up next
 * hands over to the last episode, the queue already names that episode (so it has nothing after
 * it) while the player still holds the one that ended; that moment is a hand-over, not an end. */
export function showsOwnEnd(phase: string, itemId: string | undefined, view: QueuePlaybackView | null | undefined): boolean {
  if (phase !== 'ended') return false;
  if (view?.next.available === true) return false;
  const currentEntryId = view?.queue.currentEntryId;
  const currentItemId = currentEntryId ? view?.items.find(item => item.entryId === currentEntryId)?.itemId : undefined;
  return !(currentItemId && itemId && currentItemId !== itemId);
}
