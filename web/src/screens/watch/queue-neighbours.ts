import type {WebPlaybackRuntimeSnapshot} from '../../platform/playback';

/** The queued titles either side of the playing entry (desktop-player f1 and its strip).
 * Only the real queue window names them: an entry the window does not carry, or a title it
 * has none for, stays unnamed rather than borrowing the playing title or an episode label. */
export function watchQueueNeighbours(snapshot: WebPlaybackRuntimeSnapshot): Readonly<{previous?: string; next?: string}> {
  const view = snapshot.queueView, fence = snapshot.fence;
  if (!view || fence?.kind !== 'queue') return Object.freeze({});
  const at = view.items.findIndex(item => item.entryId === fence.entryId);
  if (at < 0) return Object.freeze({});
  const named = (value: string | undefined) => { const title = value?.trim(); return title ? title : undefined; };
  const previous = at > 0 ? named(view.items[at - 1]?.title) : undefined;
  const next = at + 1 < view.items.length ? named(view.items[at + 1]?.title) : undefined;
  return Object.freeze({...(previous ? {previous} : {}), ...(next ? {next} : {})});
}
