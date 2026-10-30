import {validSelectionBasis} from '@core/selection-basis.ts';
import {browsePath, parseBrowseNode, parseBrowseResult} from '@core/browse.ts';
import {readCatalogueSelectionItem} from '@core/catalogue-selection.ts';
import {selectionQuerySelector, type BrowseSelectionContext, type QuerySelectionTarget} from '@core/selection-model/index.ts';
import type {EntryActionPorts, EntryActionTarget} from '../../shell/entry-actions';

export function queryDeleteTarget<C>(target: QuerySelectionTarget<C>): boolean {
  const checked = selectionQuerySelector(target);
  return checked.ok && 'query' in checked.selector && (checked.selector.query.selectionBasis === undefined || validSelectionBasis(checked.selector.query.selectionBasis)) && Number.isSafeInteger(target.total) && target.total > 0 && target.total <= 200
    && target.summary.every.delete && target.summary.library.kind === 'single'
    && target.summary.library.libraryId === checked.selector.query.libraryId && /^browse_[a-f0-9]{64}$/.test(checked.expected.catalogRevision)
    && target.summary.types.length > 0 && target.summary.types.every(type => ['movie', 'episode', 'song', 'audiobook_file'].includes(type.kind));
}

/** At most 200 selected leaves become the existing explicit delete preview. The
 * held full witness is checked by the server in the same snapshot as this one exact
 * range under the held revision, never a walk over resident or paged rows. */
export async function resolveQueryDelete(target: QuerySelectionTarget<BrowseSelectionContext>, ports: EntryActionPorts, signal: AbortSignal): Promise<readonly Pick<EntryActionTarget, 'itemId' | 'libraryId' | 'kind'>[]> {
  const check = () => {if (signal.aborted || !ports.isCurrent()) throw new DOMException('Selection retired', 'AbortError');};
  check();
  const checked = selectionQuerySelector(target);
  if (target.summary.unavailableActions?.delete || !queryDeleteTarget(target) || !checked.ok || !('query' in checked.selector)) throw new Error('query-delete-unavailable');
  const query = structuredClone(checked.selector.query), expected = checked.expected.catalogRevision;
  const filter = query.filter === undefined || query.filter === null ? null : parseBrowseNode(query.filter);
  const body = {pivot: query.pivot, ...(query.selectionBasis === undefined ? {} : {selectionBasis: query.selectionBasis}), ...(filter ? {query: filter} : {}), ...(query.sort?.length ? {sort: query.sort} : {}),
    limit: 200, range: {start: 0, revision: expected}};
  const raw = await ports.api.request<unknown>(browsePath(query.libraryId), 'POST', body, signal);check();
  if (!raw || typeof raw !== 'object' || !('pageInfo' in raw) || !raw.pageInfo || typeof raw.pageInfo !== 'object'
    || !('total' in raw.pageInfo) || raw.pageInfo.total !== target.total
    || !('hasMore' in raw.pageInfo) || raw.pageInfo.hasMore !== false
    || 'totalIsLowerBound' in raw.pageInfo && raw.pageInfo.totalIsLowerBound !== false) throw new Error('query-delete-selection-changed');
  const result = parseBrowseResult(raw);
  if (result.pivot !== query.pivot || result.pageInfo.revision !== expected || result.pageInfo.start !== 0
    || result.pageInfo.totalIsLowerBound || result.pageInfo.total !== target.total || result.pageInfo.hasMore || result.pageInfo.nextCursor
    || result.entries.length !== target.total || JSON.stringify(result.applied.query) !== JSON.stringify(filter)
    || JSON.stringify(result.applied.sort) !== JSON.stringify(query.sort ?? [])) throw new Error('query-delete-selection-changed');
  const targets: Pick<EntryActionTarget, 'itemId' | 'libraryId' | 'kind'>[] = [];
  for (const entry of result.entries) {
    check();
    if (entry.libraryId !== query.libraryId || !['movie', 'episode', 'song', 'audiobook_file'].includes(entry.kind)) throw new Error('query-delete-target-mismatch');
    const item = await readCatalogueSelectionItem({api: ports.api, scope: {serverId: ports.serverId, viewerId: ports.viewerId},
      requestId: ports.requestId, signal, isCurrent: ports.isCurrent}, entry, {delete: true});check();
    if (!item.capabilities.delete) throw new Error('query-delete-not-offered');
    targets.push(Object.freeze({itemId: entry.id, libraryId: query.libraryId, kind: entry.kind}));
  }
  return Object.freeze(targets);
}
