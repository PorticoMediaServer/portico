import {validSelectionBasis} from '@core/selection-basis.ts';
import {parseBrowseNode} from '@core/browse.ts';
import type {QueueBrowseSelector} from '@core/playback-v1/types.ts';

/** Whole server resources only. Visible-page item arrays are deliberately a different action. */
export type WebPlaybackQueueSource = Readonly<
  {query: QueueBrowseSelector} |
  {container: Readonly<{kind: 'playlist' | 'collection' | 'season' | 'artist' | 'album' | 'radio'; id: string}>} |
  {saved: Readonly<{id: string}>}
>;

function invalid(): never {throw new Error('invalid_queue_playback_source');}
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
const id = (value: unknown): value is string => typeof value === 'string' && /^[A-Za-z0-9_.:-]{1,128}$/.test(value);
const word = (value: unknown, max: number): value is string => typeof value === 'string' && value.length > 0 && value.length <= max && !/[\r\n]/.test(value);
function keys(value: Record<string, unknown>, allowed: readonly string[]) {if (Object.keys(value).some(key => !allowed.includes(key))) invalid();}
function freeze<T>(value: T): T {
  if (value && typeof value === 'object') {for (const child of Object.values(value)) freeze(child);Object.freeze(value);}
  return value;
}

/** Detach the exact captured query/sort before the first asynchronous operation. The server
 * alone authorizes and snapshots membership, resource revisions, expansion and queue order. */
export function captureQueueSource(input: WebPlaybackQueueSource): WebPlaybackQueueSource {
  if (!object(input) || Object.keys(input).length !== 1) invalid();
  const serialized = JSON.stringify(input);
  if (new TextEncoder().encode(serialized).length > 64 * 1024) invalid();
  if ('container' in input) {
    const value = input.container;
    if (!object(value) || !['playlist', 'collection', 'season', 'artist', 'album', 'radio'].includes(value.kind as string) || !id(value.id)) invalid();
    keys(value, ['kind', 'id']);
    return freeze({container: {kind: value.kind as 'playlist' | 'collection' | 'season' | 'artist' | 'album' | 'radio', id: value.id}});
  }
  if ('saved' in input) {
    if (!object(input.saved) || !id(input.saved.id)) invalid();keys(input.saved, ['id']);
    return freeze({saved: {id: input.saved.id}});
  }
  if (!('query' in input) || !object(input.query)) invalid();
  const value = input.query;
  keys(value, ['libraryId', 'pivot', 'query', 'sort', 'selectionRevision', 'selectionBasis']);
  if (value.selectionBasis !== undefined && !validSelectionBasis(value.selectionBasis)) invalid();
  if (value.selectionRevision !== undefined && (typeof value.selectionRevision !== 'string' || !/^browse_[a-f0-9]{64}$/.test(value.selectionRevision))) invalid();
  if (!word(value.libraryId, 256) || value.pivot !== undefined && !word(value.pivot, 64)) invalid();
  if (value.sort !== undefined && (!Array.isArray(value.sort) || value.sort.length > 3)) invalid();
  const sort = value.sort?.map(entry => {
    if (!object(entry) || !word(entry.field, 256) || !['asc', 'desc'].includes(entry.direction as string)) invalid();
    keys(entry, ['field', 'direction', 'seed']);
    if (entry.seed !== undefined && (entry.field !== 'random' || !Number.isInteger(entry.seed) || (entry.seed as number) < 0 || (entry.seed as number) > 4294967295)) invalid();
    return {field: entry.field, direction: entry.direction as 'asc' | 'desc', ...(entry.seed === undefined ? {} : {seed: entry.seed as number})};
  });
  return freeze({query: {libraryId: value.libraryId, ...(value.selectionBasis === undefined ? {} : {selectionBasis: value.selectionBasis}), ...(value.selectionRevision === undefined ? {} : {selectionRevision: value.selectionRevision}), ...(value.pivot === undefined ? {} : {pivot: value.pivot}),
    ...(value.query === undefined ? {} : {query: parseBrowseNode(value.query)}), ...(sort === undefined ? {} : {sort})}});
}
