/**
 * Home selection → queue segments, planned once for every client.
 *
 * The selection is display order for Play and pick order for Enqueue. An aggregate is sent as the
 * server's existing selector: its bounded preview membership is never expanded and its selected
 * resume child never replaces its identity. Consecutive playable leaves share one items segment of
 * at most 500 ids, in order.
 *
 * `replace` is one queue-create request that replaces playback: the published 16-segment ceiling
 * belongs to that single request and is checked here, before the caller stops, cancels or retires
 * anything. `insert` is the queue-edit path, where one segment is one insert request; it has no
 * invented batch limit of its own.
 */
import type {HomeSelectionPlaybackItem} from './home-selection/types.ts';
import type {SegmentInput} from './playback-v1/queue.ts';
import {id} from './playback-v1/types.ts';

/** The server's ceiling for one queue-create segment list (spec §8). */
export const SELECTION_SEGMENTS_MAX = 16;
/** Ids carried by one `items` segment; a longer run starts another segment. */
const SEGMENT_IDS_MAX = 500;
const containers = new Set(['show', 'season', 'album', 'artist', 'book', 'collection', 'disc']);
const leaves = new Set(['movie', 'episode', 'song', 'audiobook_file', 'extra']);
function invalid(): never { throw new Error('invalid_selection_playback_target'); }

export type SelectionPlaybackOperation = 'replace' | 'insert';
export type SelectionPlaybackPlan = Readonly<{segments: readonly SegmentInput[]; startSeconds: number | undefined}>;

/** The first item's own start position; `startIntent` is already carried by its target. */
export function selectionStartSeconds(items: readonly HomeSelectionPlaybackItem[]): number | undefined {
  const first = items[0];
  return first && 'target' in first ? first.target.startSeconds : undefined;
}

export function planSelectionPlayback(items: readonly HomeSelectionPlaybackItem[], operation: SelectionPlaybackOperation): SelectionPlaybackPlan {
  if (!items.length) invalid();
  const playing = operation === 'replace';
  const segments: SegmentInput[] = [];
  for (const [index, item] of items.entries()) {
    const identity = item.identity;
    if (identity.kind === 'channel' || identity.kind === 'person') invalid();
    id(identity.id, 'selection id'); id(identity.libraryId, 'selection library');
    if ('collection' in item) {
      const collection = item.collection.collection;
      if (identity.kind !== 'collection' || identity.id !== collection.id || identity.libraryId !== collection.libraryId) invalid();
      segments.push({source: {container: {kind: 'collection', id: collection.id}}});
      continue;
    }
    const {entry, target} = item;
    if (identity.id !== entry.id || identity.kind !== entry.kind || identity.libraryId !== entry.libraryId || entry.available === false || entry.missing) invalid();
    id(target.itemId, 'playback target');
    if (target.startSeconds !== undefined && (!Number.isFinite(target.startSeconds) || target.startSeconds < 0 || target.startSeconds > 1e9)) invalid();
    if (containers.has(identity.kind)) {
      segments.push({source: {container: {kind: identity.kind, id: identity.id}}, ...(playing && index === 0 ? {anchor: {itemId: target.itemId}} : {})});
    } else {
      if (!leaves.has(identity.kind) || target.itemId !== identity.id) invalid();
      const last = segments.at(-1);
      if (last && 'items' in last.source && last.source.items.ids.length < SEGMENT_IDS_MAX) segments[segments.length - 1] = {...last, source: {items: {ids: [...last.source.items.ids, identity.id]}}};
      else segments.push({source: {items: {ids: [identity.id]}}});
    }
  }
  if (playing && segments.length > SELECTION_SEGMENTS_MAX) throw new Error('queue_too_large');
  return Object.freeze({segments: Object.freeze(segments), startSeconds: selectionStartSeconds(items)});
}
