import {validSelectionBasis} from './selection-basis.ts';
import {clonePortableDto} from './clone-portable-dto.ts';
import {parseBrowseNode} from './browse.ts';
import type {CatalogueSelectionLifetime, CatalogueSelectionQueuePort} from './catalogue-selection.ts';
import type {SegmentInput} from './playback-v1/queue.ts';
import type {V1QueuePlayer} from './playback-v1/v1-queue-player.ts';
import {selectionQuerySelector} from './selection-model/targets.ts';
import type {QuerySelectionTarget} from './selection-model/types.ts';

const unavailable = () => new Error('catalogue-query-selection-unavailable');
const leaves = new Set(['movie', 'episode', 'song', 'audiobook_file']);
function freeze<T>(value: T): T {
  if (value && typeof value === 'object') {for (const child of Object.values(value)) freeze(child);Object.freeze(value);}
  return value;
}
/** A held view becomes one server-captured query, never its resident page. The
 * Browse proof remains mandatory through the queue transaction; no fallback. */
export function catalogueQuerySelectionSegments<C>(target: QuerySelectionTarget<C>, capability: 'play' | 'queue'): readonly SegmentInput[] {
  const checked = selectionQuerySelector(target);
  if (!checked.ok || !('query' in checked.selector) || !target.total || !target.summary.every[capability]
    || !target.summary.types.length || target.summary.types.some(type => !leaves.has(type.kind))
    || !/^browse_[a-f0-9]{64}$/.test(checked.expected.catalogRevision)) throw unavailable();
  const query = checked.selector.query;
  if (query.selectionBasis !== undefined && !validSelectionBasis(query.selectionBasis)) throw unavailable();
  if (!query.libraryId || !query.pivot || target.summary.library.kind !== 'single'
    || target.summary.library.libraryId !== query.libraryId) throw unavailable();
  const sort = query.sort?.map(value => {
    if (!value.field || !['asc', 'desc'].includes(value.direction)) throw unavailable();
    return {...value};
  });
  return freeze<readonly SegmentInput[]>([{source: {query: {libraryId: query.libraryId, pivot: query.pivot,
    ...(query.filter === undefined || query.filter === null ? {} : {query: parseBrowseNode(clonePortableDto(query.filter))}),
    ...(sort?.length ? {sort} : {}), ...(query.selectionBasis === undefined ? {} : {selectionBasis: query.selectionBasis}), selectionRevision: checked.expected.catalogRevision}}, order: {mode: 'source'}}]);
}
function current(lifetime: CatalogueSelectionLifetime) {
  if (lifetime.signal.aborted || !lifetime.isCurrent()) throw Object.assign(new Error('Selection retired'), {name: 'AbortError'});
}
export async function playCatalogueQuerySelection<C>(player: Pick<V1QueuePlayer, 'playSegments'>, target: QuerySelectionTarget<C>, lifetime: CatalogueSelectionLifetime) {
  const captured = Object.freeze({...lifetime});current(captured);
  if (target.summary.unavailableActions?.play) throw unavailable();
  const segments = catalogueQuerySelectionSegments(target, 'play');
  const result = await player.playSegments(segments, captured);current(captured);return result;
}
export async function enqueueCatalogueQuerySelection<C>(player: CatalogueSelectionQueuePort, target: QuerySelectionTarget<C>, placement: 'next' | 'end', lifetime: CatalogueSelectionLifetime) {
  const captured = Object.freeze({...lifetime});current(captured);
  if (placement !== 'next' && placement !== 'end') throw unavailable();
  if (target.summary.unavailableActions?.queue) throw unavailable();
  const segments = catalogueQuerySelectionSegments(target, 'queue');
  const result = await player.enqueueSegments(segments, placement, captured);current(captured);return result;
}
