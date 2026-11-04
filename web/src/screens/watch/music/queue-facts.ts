import type {WebPlaybackRuntimeSnapshot} from '../../../platform/playback';

/** The queue view, with the playing occurrence's fence, only while that view's current entry is
 * this occurrence: every queue fact below is read through it. */
function currentOccurrence(snapshot: WebPlaybackRuntimeSnapshot) {
  const view = snapshot.queueView, fence = snapshot.fence;
  if (fence?.kind !== 'queue' || !view || view.queue.currentEntryId !== fence.entryId) return;
  return {view, fence};
}

/** Origin belongs to the current occurrence's issued queue window, never its artist/title. */
export function musicQueueOrigin(snapshot: WebPlaybackRuntimeSnapshot): string | undefined {
  const occurrence = currentOccurrence(snapshot), view = occurrence?.view, window = view?.window, fence = occurrence?.fence;
  if (!view || !window || !fence || window.queueId !== view.queue.id || window.revision !== view.queue.revision) return;
  const entry = window.entries.find(row => row.entryId === fence.entryId);
  if (!entry?.segmentId) return;
  const label = window.segments.find(segment => segment.id === entry.segmentId)?.label;
  return label?.trim() || undefined;
}

/** phone-app 14's "Next  {title}": the entry the queue itself says plays next, named from its own
 * rows, only while that answer belongs to this playing occurrence. */
export function musicQueueNext(snapshot: WebPlaybackRuntimeSnapshot): string | undefined {
  const view = currentOccurrence(snapshot)?.view, next = view?.next;
  if (!view || !next?.available || !next.entryId) return;
  const title = view.items.find(row => row.entryId === next.entryId)?.title;
  return title?.trim() || undefined;
}

/** Detail's favorite grant needs the real library and item of this accepted occurrence. */
export function musicQueueItem(snapshot: WebPlaybackRuntimeSnapshot): Readonly<{libraryId: string; itemId: string}> | undefined {
  const occurrence = currentOccurrence(snapshot);
  if (!occurrence) return;
  const {view, fence} = occurrence, item = view.items.find(row => row.entryId === fence.entryId && row.itemId === fence.itemId);
  if (item?.kind !== 'song' || !item.libraryId) return;
  return {libraryId: item.libraryId, itemId: item.itemId};
}
