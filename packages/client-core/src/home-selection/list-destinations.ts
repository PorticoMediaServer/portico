/**
 * Adding titles to a playlist or a collection on the rebuilt server: the destinations are the profile's lists
 * (`GET /v1/lists?kind=`), an addition is `POST /v1/lists/{id}/entries`, and making one with the titles in it is
 * `POST /v1/lists`. There is no fence: a list carries its own revision.
 *
 * Definition area 6: any profile may add to its own lists and to those shared with it to edit (rules 25, 27); a library's
 * collection is changed by the owner and administrators (rule 29), which the list's role already says; a playlist takes
 * a show, season or album as its playable titles and may hold a title twice (rule 24); a collection holds each title once
 * and says "Already in" for the rest (rule 33).
 */
import type {List, ListAdd, ListAdded, ListCreate, ListCreated, ListPage} from '../../../contracts/src/next/api.ts';
import {callServer, serverPath, type ServerRequester} from '../query/server-api.ts';
import {entryFromItem, readAllItems} from '../items.ts';
import {libraryItemQuery} from '../library-browse.ts';
import type {ContentEntry} from '../library-content.ts';
import type {BrowseNode} from '../browse.ts';
import {selectionQuerySelector, type SelectionTarget} from '../selection-model/index.ts';
import {selectionExecutionError} from './execution.ts';
import type {HomeSelectionPickerRow} from './types.ts';

export type ListKind = 'playlist' | 'collection';
/** The most titles one addition takes; the server refuses more. A larger selection goes in several. */
export const LIST_ADD_AT_ONCE = 500;
/** A transport that may also send headers (`HttpLocalApi.requestRaw`): an addition then names its operation as its
 * Idempotency-Key, so a repeat after a lost answer adds nothing twice. Without one it is sent as before. */
export type ListDestinationApi = ServerRequester & Partial<Readonly<{requestRaw(path: string, method: 'POST', options?: Readonly<{headers?: Readonly<Record<string, string>>; body?: unknown; signal?: AbortSignal}>): Promise<{status: number; headers: Record<string, string>; body?: unknown}>}>>;

const revisionOf = (list: Pick<List, 'revision'>) => {const n = Number(list.revision); return Number.isSafeInteger(n) && n >= 1 ? n : 1;};
/** A list's covers as the artwork its row draws (only drawn, never opened). */
const coversOf = (list: List): ContentEntry[] => (list.covers ?? []).slice(0, 4).flatMap((images, index) => {
  const entry = entryFromItem({id: `${list.id}:cover:${index}`, kind: 'movie', title: list.name, libraryId: 'lists', images} as never);
  return entry ? [entry] : [];
});
/** A list as a destination row. Whether this profile may add to it is the list's own role: it made it, or may edit it. */
export function destinationRow(list: List): HomeSelectionPickerRow {
  return Object.freeze({id: list.id, name: list.name, count: list.count, revision: revisionOf(list), canAdd: list.role === 'owner' || list.role === 'editor', artwork: Object.freeze(coversOf(list))});
}
/** Every list of the kind this profile sees: its own, those shared with it, and the library's collections. */
async function allLists(api: ServerRequester, kind: ListKind, signal?: AbortSignal): Promise<List[]> {
  const lists: List[] = [];
  for (let cursor: string | undefined, pages = 0; pages < 20; pages++) {
    const page: ListPage = await callServer(api, 'GET /v1/lists', {query: {kind, limit: 200, cursor}, signal});
    lists.push(...(page?.items ?? []).filter(list => list?.kind === kind));
    if (!page?.nextCursor) break;
    cursor = page.nextCursor;
  }
  return lists;
}
export type DestinationPage = Readonly<{rows: readonly HomeSelectionPickerRow[]; nextCursor: string; revision: Readonly<{catalog: number; viewer: number}>}>;
/** One page of destination rows whose names hold `query`. The cursor is a position in the profile's lists. */
export async function readDestinationPage(api: ServerRequester, request: Readonly<{kind: ListKind; query: string; cursor?: string; limit: number}>, signal?: AbortSignal): Promise<DestinationPage> {
  if (!Number.isSafeInteger(request.limit) || request.limit < 1 || request.limit > 40 || [...request.query].length > 100 || /[\x00-\x1f\x7f-\x9f]/.test(request.query)) throw selectionExecutionError('invalid-destination-query');
  const words = request.query.trim().toLocaleLowerCase(), start = Number(request.cursor ?? 0);
  if (!Number.isSafeInteger(start) || start < 0) throw selectionExecutionError('invalid-destination-query');
  const matched = (await allLists(api, request.kind, signal)).filter(list => !words || list.name.toLocaleLowerCase().includes(words));
  const rows = matched.slice(start, start + request.limit).map(destinationRow);
  return Object.freeze({rows: Object.freeze(rows), nextCursor: start + request.limit < matched.length ? String(start + request.limit) : '', revision: Object.freeze({catalog: 0, viewer: 0})});
}
/** One destination read again, as its row. A list of the other kind is refused. */
export async function readDestination(api: ServerRequester, kind: ListKind, id: string, signal?: AbortSignal): Promise<HomeSelectionPickerRow> {
  const list = await callServer(api, 'GET /v1/lists/{id}', {id, signal});
  if (list?.kind !== kind || list.id !== id) throw selectionExecutionError('destination-not-permitted');
  return destinationRow(list);
}

/**
 * The titles a selection adds. Picked titles are sent as they are (a playlist takes a show, season or album as its
 * playable titles on the server). A whole view is read here, up to {@link LIST_ADD_AT_ONCE} titles, and a larger one is
 * refused saying how many can be added at once. This is the one place a server-side "add this whole view" replaces.
 */
export async function selectionItemIds(api: ServerRequester, target: SelectionTarget, signal?: AbortSignal): Promise<readonly string[]> {
  if (target.mode !== 'query') return Object.freeze(target.items.map(pick => pick.item).filter(item => item.kind !== 'channel' && item.kind !== 'person').map(item => item.id));
  const checked = selectionQuerySelector(target);
  if (!checked.ok || !('query' in checked.selector)) throw selectionExecutionError(checked.ok ? 'query-destination-unavailable' : checked.reason);
  if (target.total > LIST_ADD_AT_ONCE) throw Object.assign(selectionExecutionError('too-many-to-add'), {limit: LIST_ADD_AT_ONCE, total: target.total});
  const {libraryId, pivot, filter, sort} = checked.selector.query;
  const items = await readAllItems(api, libraryItemQuery({id: libraryId}, pivot, filter ? [filter as BrowseNode] : [], sort ?? []), signal, LIST_ADD_AT_ONCE + 1);
  if (items.length > LIST_ADD_AT_ONCE) throw Object.assign(selectionExecutionError('too-many-to-add'), {limit: LIST_ADD_AT_ONCE, total: items.length});
  return Object.freeze(items.map(item => item.id));
}

async function send<T>(api: ListDestinationApi, route: 'POST /v1/lists' | 'POST /v1/lists/{id}/entries', id: string | undefined, body: ListAdd | ListCreate, key: string, signal?: AbortSignal): Promise<T> {
  if (!api.requestRaw) return (route === 'POST /v1/lists' ? callServer(api, route, {body: body as ListCreate, signal}) : callServer(api, route, {id, body: body as ListAdd, signal})) as Promise<T>;
  const answer = await api.requestRaw(serverPath(route, id === undefined ? {} : {id}), 'POST', {headers: {'Idempotency-Key': key}, body, signal});
  if (answer.status >= 400) {
    const error = (answer.body as {error?: {code?: string; message?: string; retryable?: boolean}} | undefined)?.error;
    throw Object.assign(new Error(error?.message ?? 'The list could not be changed.'), {status: answer.status, code: error?.code ?? 'request_failed', retryable: error?.retryable ?? answer.status >= 500});
  }
  return answer.body as T;
}
export type DestinationAdded = Readonly<{added: number; already: number; revision: number; count: number}>;
/**
 * Adds the titles to the list, {@link LIST_ADD_AT_ONCE} at a time, in their picked order. Each part names its own key
 * (`key` and its part number), the same on a retry, so a part that went in is not added again. `done` says how many
 * titles have gone in so far.
 */
export async function addToDestination(api: ListDestinationApi, id: string, ids: readonly string[], key: string, signal?: AbortSignal, done?: (sent: number) => void): Promise<DestinationAdded> {
  if (!ids.length) throw selectionExecutionError('destination-required');
  let added = 0, already = 0, list: List | undefined;
  for (let part = 0; part * LIST_ADD_AT_ONCE < ids.length; part++) {
    const items = ids.slice(part * LIST_ADD_AT_ONCE, (part + 1) * LIST_ADD_AT_ONCE);
    const answer = await send<ListAdded>(api, 'POST /v1/lists/{id}/entries', id, {items: [...items]}, `${key}.${part}`, signal);
    added += answer?.added ?? 0; already += answer?.already ?? 0; list = answer?.list ?? list;
    done?.(Math.min(ids.length, (part + 1) * LIST_ADD_AT_ONCE));
  }
  return Object.freeze({added, already, revision: list ? revisionOf(list) : 1, count: list?.count ?? added});
}
/** Makes a list of the kind, named `name`, holding the titles: the first part goes in with it, the rest are added after. */
export async function createDestination(api: ListDestinationApi, kind: ListKind, name: string, ids: readonly string[], key: string, signal?: AbortSignal): Promise<Readonly<{row: HomeSelectionPickerRow; added: number}>> {
  const trimmed = name.trim();
  if (!trimmed) throw selectionExecutionError('destination-name-required');
  const created = await send<ListCreated>(api, 'POST /v1/lists', undefined, {kind, name: trimmed, ...(ids.length ? {items: ids.slice(0, LIST_ADD_AT_ONCE)} : {})}, key, signal);
  if (!created?.id) throw selectionExecutionError('destination-unconfirmed', true);
  const rest = ids.slice(LIST_ADD_AT_ONCE);
  const more = rest.length ? await addToDestination(api, created.id, rest, `${key}.rest`, signal) : undefined;
  return Object.freeze({row: destinationRow(more ? {...created, revision: String(more.revision), count: more.count} : created), added: Math.min(ids.length, LIST_ADD_AT_ONCE) + (more?.added ?? 0)});
}
