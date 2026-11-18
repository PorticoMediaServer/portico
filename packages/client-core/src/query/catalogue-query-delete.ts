import {throwIfAborted} from '../abort-lifetime.ts';
import {acrossLibraries, parseBrowseNode} from '../browse.ts';
import {entryFromItem, readItems} from '../items.ts';
import {libraryItemQuery} from '../library-browse.ts';
import type {ContentEntry} from '../library-content.ts';
import {selectionQuerySelector, type QuerySelectionTarget} from '../selection-model/index.ts';
import type {ServerRequester} from './server-api.ts';

export const queryDeleteLimit = 200;
const deletableKinds = new Set(['movie', 'episode', 'song', 'audiobook_file']);

/** A whole view can be resolved only as one library's list of deletable leaves. */
export function queryDeleteSelector<C>(target: QuerySelectionTarget<C>) {
  const checked = selectionQuerySelector(target), {library, types} = target.summary;
  if (!checked.ok || !('query' in checked.selector) || library.kind !== 'single'
    || library.libraryId !== checked.selector.query.libraryId || library.libraryId === acrossLibraries
    || !Number.isSafeInteger(target.total) || target.total < 1
    || !types.length || !types.every(type => deletableKinds.has(type.kind))) return undefined;
  return checked.selector.query;
}

/** Read the same filtered list as Browse, then freeze its membership for the explicit preview. */
export async function resolveQueryDelete<C>(api: ServerRequester, target: QuerySelectionTarget<C>, signal: AbortSignal): Promise<readonly ContentEntry[]> {
  const selector = queryDeleteSelector(target);
  if (!selector || target.total > queryDeleteLimit || target.summary.unavailableActions?.delete) throw new Error('query-delete-unavailable');
  const {libraryId, pivot, filter, sort} = selector;
  const query = libraryItemQuery({id: libraryId}, pivot, filter == null ? [] : [parseBrowseNode(filter)], sort ?? []);
  const entries = new Map<string, ContentEntry>();
  const kinds = new Set(target.summary.types.map(type => type.kind));
  const changed = () => new Error('query-delete-selection-changed');
  let cursor: string | undefined;
  do {
    throwIfAborted(signal);
    const page = await readItems(api, query, {cursor, limit: target.total - entries.size}, signal);
    throwIfAborted(signal);
    if (!page.items.length || page.items.length + entries.size > target.total) throw changed();
    for (const item of page.items) {
      const entry = entryFromItem(item, libraryId);
      if (!entry || entry.libraryId !== libraryId || !kinds.has(entry.kind) || entries.has(entry.id)) throw changed();
      entries.set(entry.id, entry);
    }
    cursor = page.nextCursor;
    if (cursor && entries.size === target.total) throw changed();
  } while (cursor);
  if (entries.size !== target.total) throw changed();
  return [...entries.values()];
}
