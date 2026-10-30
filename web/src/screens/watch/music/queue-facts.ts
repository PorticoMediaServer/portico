import type {WebPlaybackRuntimeSnapshot} from '../../../platform/playback';

/** Origin belongs to the current occurrence's issued queue window, never its artist/title. */
export function musicQueueOrigin(snapshot: WebPlaybackRuntimeSnapshot): string | undefined {
  const view = snapshot.queueView, window = view?.window, fence = snapshot.fence;
  if (fence?.kind !== 'queue' || !view || !window || view.queue.currentEntryId !== fence.entryId
    || window.queueId !== view.queue.id || window.revision !== view.queue.revision) return;
  const entry = window.entries.find(row => row.entryId === fence.entryId);
  if (!entry?.segmentId) return;
  const label = window.segments.find(segment => segment.id === entry.segmentId)?.label;
  return label?.trim() || undefined;
}

/** Detail's favorite grant needs the real library and item of this accepted occurrence. */
export function musicQueueItem(snapshot: WebPlaybackRuntimeSnapshot): Readonly<{libraryId: string; itemId: string}> | undefined {
  const fence = snapshot.fence, view = snapshot.queueView;
  if (fence?.kind !== 'queue' || !view || view.queue.currentEntryId !== fence.entryId) return;
  const item = view.items.find(row => row.entryId === fence.entryId && row.itemId === fence.itemId);
  if (item?.kind !== 'song' || !item.libraryId) return;
  return {libraryId: item.libraryId, itemId: item.itemId};
}
