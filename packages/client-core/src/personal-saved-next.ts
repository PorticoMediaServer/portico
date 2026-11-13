/**
 * Saved's History on the rebuilt server: `GET /v1/history?period=` (the profile's finished plays, newest first) and
 * `DELETE /v1/history` (Clear history). Each reader answers in the shape `personal-saved.ts` already checks, so the
 * History screens are unchanged.
 *
 * Definition area 6: History lists what the profile finished playing, newest first, for the last 24 hours, 7, 30 or 90
 * days, or all (rule 18); a title removed from the server stays in History by name and cannot be opened to play, and a
 * title the profile may no longer see is absent, which the server decides (rule 19); Clear history empties History and
 * keeps watched marks, resume points, ratings and lists (rule 21).
 */
import type {HistoryEntry, HistoryPage, Item, List, ListAdd, ListAdded, ListCreate, ListCreated, ListEdit, ListMove, ListPage} from '../../contracts/src/next/api.ts';
import {readProfilePins, setResourcePin} from './library-pins.ts';
import {callServer, serverPath, type ServerRequester} from './query/server-api.ts';
import {wireEntryFromItem} from './items.ts';
import {CodedError} from './server-messages.ts';
import {decodeUndo, edit, encodeUndo, restoreEntry, filterLabels, items, listFilters, pageSorts, revisionOf, sendRaw, type SavedNextApi} from './saved-next.ts';
import {libraryItemQuery, libraryQueryControls} from './library-browse.ts';
import type {HistoryPeriod, ResourceContentQuery, SavedScope} from './saved.ts';
import type {PersonalSavedIntent, SavedDefinition} from './personal-saved.ts';

const PAGE = 40;
/** A removed title has no item: it is named, marked missing, and offers nothing to play. */
function mediaOf(entry: HistoryEntry): Record<string, unknown> {
  const card = entry.item ? wireEntryFromItem(entry.item as Item) : undefined;
  if (card) return card;
  return {id: entry.id, kind: entry.kind, title: entry.title, ...(entry.subtitle ? {subtitle: entry.subtitle} : {}), available: false, missing: true};
}
/** One page of History, in the old answer's shape: finished plays only, so every row is completed. */
export async function readHistoryPage(api: ServerRequester, bound: SavedScope, period: HistoryPeriod | undefined, cursor: string | null, signal?: AbortSignal): Promise<unknown> {
  const page: HistoryPage = await callServer(api, 'GET /v1/history', {query: {period: period ?? 'all', limit: PAGE, cursor: cursor ?? undefined}, signal});
  return {serverId: bound.serverId, viewerFence: 'viewer', revision: 0, nextCursor: page?.nextCursor ?? '',
    entries: (Array.isArray(page?.items) ? page.items : []).map(entry => ({id: entry.id, media: mediaOf(entry), updatedAt: entry.playedAt, positionMs: entry.watchedMs ?? 0, completed: true}))};
}
/** Clear history, answered as the old receipt the service checks. The server answers nothing to keep. */
export async function clearHistory(api: ServerRequester, bound: SavedScope, operationId: string, signal?: AbortSignal, _body?: unknown): Promise<unknown> {
  await callServer(api, 'DELETE /v1/history', {signal});
  return {serverId: bound.serverId, viewerFence: 'viewer', receipt: {operationId, revision: 0}};
}

/*
 * Saved's collections and saved views on the rebuilt server: `GET /v1/lists?kind=collection`, one collection's or saved
 * view's page (`GET /v1/lists/{id}` with `GET /v1/items?list={id}`, or a saved view's own stored query on its library),
 * and their changes, answered in the shapes `personal-saved.ts` already checks.
 *
 * Definition area 6: a collection holds each title once and has an order of its own (rule 24); a name is 1 to 200
 * characters and a description up to 4,000 (rule 25); a collection is "Only you" or "Visible to everyone on this
 * server", and others can open and play a visible one and cannot change it (rule 28), so it is shared with no chosen
 * people; a list is reordered only in its own order (rule 31); removing an entry offers Undo for 30 seconds, which
 * puts it back in its place (rule 32); adding a title it holds says "Already in" and changes nothing (rule 33); an
 * edit made on an out-of-date copy is refused with "This list changed" (rule 34). Anyone who sees a list may pin it:
 * the pin is the profile's own `pins` setting (rule 38), as for playlists. A saved view is a saved sort and filter on one
 * library, always current (rule 24); one whose filter names something that no longer exists opens with the rest of
 * its filter and says what was dropped (rule 36), which the server works out.
 */
const collectionCard = (list: List) => ({artworkPaths: list.covers.map(images => images.poster ?? images.cover ?? images.thumb).filter(Boolean).slice(0, 4), samples: [], shape: 'posters', onHome: false});
/** One collection as `personal-saved.ts` reads it. Only its maker changes it (rule 28). */
/** `pins`: the profile's pins, or undefined when they could not be read (Pin is then not offered). */
function collectionOf(bound: SavedScope, list: List, pins?: readonly string[]): Record<string, unknown> {
  const owner = list.role === 'owner';
  // The server's covers on the page too, as on its card: the page's header art is the collection's own.
  return {...collectionCard(list), serverId: bound.serverId, viewerFence: 'viewer', id: list.id, kind: 'collection', name: list.name, summary: list.description ?? '',
    visibility: list.everyone ? 'server' : 'private', revision: revisionOf(list), role: owner ? 'owner' : 'viewer', entryCount: list.count,
    actions: [...(owner ? ['update', 'delete', 'entries', 'reorder'] : []), ...(pins ? ['pin'] : [])], status: 'ready', invalidComponents: [],
    pinned: !!pins?.includes('collection:' + list.id), pinRevision: 0, shares: []};
}
/** The profile's collections (its own and those made visible to everyone) or its saved views, as cards. */
export async function readDirectory(api: SavedNextApi, bound: SavedScope, kind: 'collection' | 'view', cursor: string | null, signal?: AbortSignal): Promise<unknown> {
  const wire = kind === 'view' ? 'saved_view' : 'collection';
  const [page, pins] = await Promise.all([callServer(api, 'GET /v1/lists', {query: {kind: wire, limit: PAGE, cursor: cursor ?? undefined}, signal}) as Promise<ListPage>,
    readProfilePins(api, signal)]);
  if (!page || !Array.isArray(page.items)) throw new CodedError('invalid_saved', 'The saved lists could not be read.');
  return {serverId: bound.serverId, viewerFence: 'viewer', revision: 0, nextCursor: page.nextCursor ?? '',
    resources: page.items.filter(list => list.kind === wire).map(list => kind === 'view' ? viewCard(bound, list, pins) : collectionOf(bound, list, pins))};
}
/** A saved view's card: music views draw square art. */
function viewCard(bound: SavedScope, list: List, pins?: readonly string[]): Record<string, unknown> {
  const definition = definitionOf(list), squares = ['artists', 'albums', 'songs'].includes(definition?.pivot ?? '');
  return {...collectionCard(list), shape: squares ? 'squares' : 'posters', ...viewOf(bound, list, definition, pins)};
}
/** One collection or saved view by its id, as `readPersonalResource` checks it (Customize Home names a view by id). */
export async function readResourceRow(api: SavedNextApi, bound: SavedScope, resourceId: string, signal?: AbortSignal): Promise<unknown> {
  const list = await callServer(api, 'GET /v1/lists/{id}', {id: resourceId, signal});
  if (list.kind === 'saved_view') return viewOf(bound, list, definitionOf(list));
  if (list.kind === 'collection') return collectionOf(bound, list);
  throw new CodedError('not_found', 'This list is not a collection or a saved view.');
}

/** Where a later page of a collection starts: its place in the whole collection, the entry just in front of it and
 * the collection's first entry, which a move to the front names. The server's own cursor rides inside. */
type PageMark = Readonly<{start: number; previous?: string; first?: string; cursor: string}>;
const markOf = (cursor: string | null): PageMark | undefined => {
  if (!cursor?.startsWith('page.')) return undefined;
  try {const value = JSON.parse(atob(cursor.slice(5))); if (Number.isSafeInteger(value?.start) && typeof value.cursor === 'string') return value;} catch {/* refused below */}
  throw new CodedError('invalid_cursor', 'This page can no longer be read.');
};
const markCursor = (mark: PageMark) => 'page.' + btoa(JSON.stringify(mark)).replace(/=+$/, '');

/** A saved view's sort and filter as the browse controls draw them, from the library and query the server keeps.
 * `undefined` for one the controls cannot draw: it is shown for review, never run as something else. */
function definitionOf(list: List): SavedDefinition | undefined {
  const controls = list.library && list.query !== undefined ? libraryQueryControls(list.query) : undefined;
  if (!controls || !list.library) return undefined;
  const {nodes} = controls;
  return {libraryId: list.library, pivot: controls.pivot, query: nodes.length === 0 ? null : nodes.length === 1 ? nodes[0]! : {variant: 'group', all: [...nodes]},
    sort: controls.sorts.length ? controls.sorts : [{field: 'title', direction: 'asc'}], presentation: 'grid'};
}
/** One saved view as `personal-saved.ts` reads it: only its maker changes it, and what the server dropped from its
 * filter is listed (rule 36). */
function viewOf(bound: SavedScope, list: List, definition: SavedDefinition | undefined, pins?: readonly string[]): Record<string, unknown> {
  const owner = list.role === 'owner';
  return {serverId: bound.serverId, viewerFence: 'viewer', id: list.id, kind: 'view', name: list.name, summary: list.description ?? '', visibility: 'private', revision: revisionOf(list),
    role: owner ? 'owner' : 'viewer', entryCount: list.count, actions: [...(owner ? ['update', 'delete'] : []), ...(pins ? ['pin'] : [])], ...(definition ? {definition} : {}), status: definition ? 'ready' : 'needs-review',
    invalidComponents: [...(list.dropped ?? [])], pinned: !!pins?.includes('view:' + list.id), pinRevision: 0, shares: []};
}
/** A saved view's page: its titles, always current, read with its own query on its library, as the browse page the
 * service checks. */
async function viewPage(api: SavedNextApi, bound: SavedScope, list: List, cursor: string | null, signal?: AbortSignal, pins?: readonly string[]): Promise<unknown> {
  const definition = definitionOf(list), resource = viewOf(bound, list, definition, pins);
  if (!definition) return {nextCursor: '', resource, entries: []};
  const mark = markOf(cursor), start = mark?.start ?? 0;
  const page = await items(api, {...Object.fromEntries(new URLSearchParams(list.query ?? '')), library: list.library, limit: PAGE, total: true, cursor: mark?.cursor}, signal);
  const entries = page.items.flatMap(item => {const media = wireEntryFromItem(item as Item, list.library); return media ? [media] : [];});
  const nextCursor = page.nextCursor ? markCursor({start: start + entries.length, cursor: page.nextCursor}) : '', counted = page.total !== undefined;
  return {nextCursor, resource, browse: {pivot: definition.pivot, applied: {query: definition.query, sort: definition.sort, seek: null}, entries, positionIndex: [],
    pageInfo: {start, total: counted ? page.total : start + entries.length, totalIsLowerBound: !counted, revision: 'list-' + list.revision, hasMore: !!nextCursor, nextCursor}}};
}

/** A collection's or saved view's page, in the shape `personal-saved.ts` checks. A collection's comes with its entries
 * and, in its own order over every entry, the entry in front of the page and the collection's last, which moves name.
 * The list and a collection's titles are read together; only a saved view waits for the list to know its query. */
export async function readResource(api: SavedNextApi, bound: SavedScope, resourceId: string, asked: ResourceContentQuery, cursor: string | null, signal?: AbortSignal): Promise<unknown> {
  const sort = asked.sort ?? 'position', filter = asked.filter ?? 'all', direction = asked.direction ?? 'asc', manual = sort === 'position' && filter === 'all';
  const mark = markOf(cursor), start = mark?.start ?? 0;
  const settled = <T,>(read: Promise<T>) => read.then(value => ({value}), (error: unknown) => ({error}));
  const value = <T,>(read: {value: T} | {error: unknown}): T => {if ('error' in read) throw read.error; return read.value;};
  const listRead = callServer(api, 'GET /v1/lists/{id}', {id: resourceId, signal}), pinsRead = settled(readProfilePins(api, signal));
  const pageRead = settled(items(api, {list: resourceId, ...listFilters[filter], sort: pageSorts[sort], order: sort === 'position' ? undefined : direction, limit: PAGE, total: true, cursor: mark?.cursor}, signal));
  const lastRead = manual ? settled(items(api, {list: resourceId, sort: 'order', order: 'desc', limit: 1}, signal)) : Promise.resolve({value: undefined});
  const list = await listRead, pins = value(await pinsRead);
  if (list.kind === 'saved_view') return viewPage(api, bound, list, cursor, signal, pins);
  if (list.kind !== 'collection') throw new CodedError('not_available', 'This is not available on this server yet.');
  const page = value(await pageRead), last = value(await lastRead);
  const entries = page.items.flatMap(item => {const media = wireEntryFromItem(item as Item); return media && item.entryId ? [{id: item.entryId, hidden: false, media}] : [];});
  const ids = entries.map(entry => entry.id), lastEntry = last?.items[0]?.entryId;
  const first = mark ? mark.first : ids[0];
  const nextCursor = page.nextCursor ? markCursor({start: start + entries.length, ...(ids.length ? {previous: ids.at(-1)} : {}), ...(first ? {first} : {}), cursor: page.nextCursor}) : '';
  return {nextCursor, resource: collectionOf(bound, list, pins), entries, query: {sort, category: filter, direction}, totalCount: page.total ?? entries.length, start,
    sorts: Object.keys(pageSorts).map(id => ({id, labelKey: 'sort.' + id, directions: id === 'position' ? ['asc'] : ['asc', 'desc']})),
    filters: [{id: 'filter', labelKey: 'saved.filter', options: ['all', 'unwatched', 'inProgress', 'watched'].map(id => ({id, label: filterLabels[id], count: 0}))}],
    ...(manual && lastEntry ? {order: {...(mark?.previous ? {previous: mark.previous} : {}), last: lastEntry}} : {})};
}

export type ResourceCommandContext = Readonly<{
  resourceId?: string; revision?: number;
  /** What the open resource is: a saved view is renamed and deleted, nothing more. */
  kind?: 'collection' | 'view';
  /** The cursor of the page on screen, which names the entry in front of it and the collection's first. */
  cursor: string | null;
  /** The page's entries in order: each entry's id and title, so a removal can be undone. */
  entries: readonly Readonly<{id: string; itemId?: string}>[];
}>;
/** One collection or saved view change on the rebuilt server, answered as the receipt `personal-saved.ts` checks. The
 * service also hands over the old route's request, which only a test's old-server transport reads. */
export async function sendResourceCommand(api: SavedNextApi, bound: SavedScope, intent: PersonalSavedIntent, context: ResourceCommandContext, operationId: string, signal?: AbortSignal, _old?: Readonly<{path: string; method: string; body: unknown}>): Promise<unknown> {
  const answer = (resourceId: string, list: Pick<List, 'revision'> | undefined, extra: Record<string, unknown> = {}) =>
    ({serverId: bound.serverId, viewerFence: 'viewer', receipt: {operationId, resourceId, revision: list ? revisionOf(list) : (context.revision ?? 0) + 1, deleted: false, ...extra}});
  // An addition names its operation as its Idempotency-Key, which Retry keeps, so a repeat adds nothing twice. It
  // overwrites nothing, so a transport that cannot send headers still sends it, without the key, as a playlist's does.
  const add = (id: string, body: ListAdd, key: string) => api.requestRaw ? sendRaw<ListAdded>(api, serverPath('POST /v1/lists/{id}/entries', {id}), 'POST', {'Idempotency-Key': key}, body, signal)
    : callServer(api, 'POST /v1/lists/{id}/entries', {id, body, signal});
  if (intent.action === 'create' && intent.kind === 'view') {
    // The view keeps its library and the item list's query its sort and filter make (rule 24); a filter the server
    // cannot answer is refused here, in the browse page's own words, before anything is sent.
    const d = intent.definition;
    if (!d) throw new CodedError('invalid_saved', 'A saved view needs its sort and filter.');
    const {library: _, ...list} = libraryItemQuery({id: d.libraryId}, d.pivot, d.query ? ('all' in d.query ? d.query.all : [d.query]) : [], d.sort) as Record<string, unknown>;
    const query = new URLSearchParams(Object.entries(list).filter(([, value]) => value !== undefined).map(([key, value]) => [key, String(value)])).toString();
    const body: ListCreate = {kind: 'saved_view', name: intent.name.trim(), ...(intent.summary ? {description: intent.summary} : {}), library: d.libraryId, query};
    const created = api.requestRaw ? await sendRaw<ListCreated>(api, serverPath('POST /v1/lists'), 'POST', {'Idempotency-Key': operationId}, body, signal) : await callServer(api, 'POST /v1/lists', {body, signal});
    return answer(created.id, created);
  }
  if (intent.action === 'create') {
    if (intent.kind !== 'collection') throw new CodedError('not_available', 'This is not available on this server yet.');
    const body: ListCreate = {kind: 'collection', name: intent.name.trim(), ...(intent.summary ? {description: intent.summary} : {}), everyone: intent.visibility === 'server'};
    const created = api.requestRaw ? await sendRaw<ListCreated>(api, serverPath('POST /v1/lists'), 'POST', {'Idempotency-Key': operationId}, body, signal) : await callServer(api, 'POST /v1/lists', {body, signal});
    return answer(created.id, created);
  }
  const id = context.resourceId;
  if (!id) throw new Error('Collection is not selected.');
  const listPath = serverPath('GET /v1/lists/{id}', {id}), entryPath = (entryId: string) => `${listPath}/entries/${encodeURIComponent(entryId)}`;
  // A saved view is renamed, described or deleted here; its sort and filter are changed where it is saved.
  if (context.kind === 'view' && !(intent.action === 'delete' || intent.action === 'pin' || intent.action === 'update' && !intent.definition && intent.visibility === undefined))
    throw new CodedError('not_available', 'This is not available on this server yet.');
  switch (intent.action) {
    case 'update': {
      const body: ListEdit = {...(intent.name === undefined ? {} : {name: intent.name.trim()}), ...(intent.summary === undefined ? {} : {description: intent.summary}), ...(intent.visibility === undefined ? {} : {everyone: intent.visibility === 'server'})};
      return answer(id, await edit<List>(api, listPath, 'PATCH', context.revision, body, signal));
    }
    case 'delete': await edit<void>(api, listPath, 'DELETE', context.revision, undefined, signal); return answer(id, undefined, {deleted: true});
    // A pin is the profile's own setting (rule 38), not the list's: the list and its revision are unchanged.
    case 'pin': await setResourcePin(api, context.kind === 'view' ? 'view' : 'collection', id, intent.pinned, 0, operationId); return answer(id, undefined, {revision: context.revision ?? 0});
    case 'entries': {
      if (intent.undoToken !== undefined) {
        const token = decodeUndo(intent.undoToken);
        if (token.listId !== id) throw new CodedError('undo_other_list', 'This can only be undone in the list it was removed from.');
        const done = await restoreEntry(api, token, body => add(id, body, operationId), signal);
        return answer(id, done.list, {entries: {added: done.added ? [token.itemId] : [], removed: [], unchanged: done.added ? [] : [token.itemId], failed: []}});
      }
      const added: string[] = [], unchanged: string[] = [], removed: string[] = [], failed: {itemId: string; code: string}[] = [];
      let list: List | undefined, revision = context.revision;
      // One title at a time, so each says whether it was added or already in (rule 33); Retry repeats each key.
      for (const [n, itemId] of (intent.addItemIds ?? []).entries()) {
        try {const done = await add(id, {items: [itemId]}, `${operationId}.${n}`); list = done.list; (done.added ? added : unchanged).push(itemId);}
        catch (error) {failed.push({itemId, code: (error as {code?: string}).code ?? 'request_failed'});}
      }
      if (list) revision = revisionOf(list);
      const order = context.entries, removing = intent.removeEntryIds ?? [];
      for (const entryId of removing) {list = await edit<List>(api, entryPath(entryId), 'DELETE', revision, undefined, signal); revision = revisionOf(list); removed.push(entryId);}
      // Undo of a single removal puts the title back just after the entry in front of it (rule 32).
      const at = removing.length === 1 && !added.length && !unchanged.length ? order.findIndex(entry => entry.id === removing[0]) : -1;
      const before = at > 0 ? order[at - 1]!.id : at === 0 ? markOf(context.cursor)?.previous ?? '' : '';
      const next = at === 0 && !before ? order[1]?.id : undefined;
      const undo = at >= 0 && order[at]!.itemId ? {undoToken: encodeUndo({listId: id, itemId: order[at]!.itemId!, after: before, ...(next ? {before: next} : {})}), undoUntil: new Date(Date.now() + 30_000).toISOString().replace(/\.\d+Z$/, 'Z')} : {};
      return answer(id, list, {entries: {added, removed, unchanged, failed}, ...undo});
    }
    case 'reorder': {
      // Without an anchor the entries keep the place of the first of them: the first goes in front of the one that
      // held that place, and each of the rest just after the one before it. With one, the first goes after it, or to
      // the front of the whole collection ("").
      const mark = markOf(context.cursor), order = [...(mark?.previous ? [mark.previous] : []), ...context.entries.map(entry => entry.id)], ids = intent.entryIds;
      const held = order.find(entry => ids.includes(entry)), front = mark ? mark.first : order[0];
      const lead: ListMove | undefined = intent.afterEntryId ? {after: intent.afterEntryId} : intent.afterEntryId === '' ? (front ? {before: front} : undefined) : held && held !== ids[0] ? {before: held} : undefined;
      let list: List | undefined, revision = context.revision;
      for (const [n, entryId] of ids.entries()) {
        const body = n === 0 ? lead : {after: ids[n - 1]!};
        if (!body) continue;
        try {list = await edit<List>(api, entryPath(entryId), 'PATCH', revision, body, signal);}
        // Once a move has landed the collection is no longer the one on screen: say so, so it is read again.
        catch (error) {throw list && error && typeof error === 'object' ? Object.assign(error, {partlyMade: true}) : error;}
        revision = revisionOf(list);
      }
      return answer(id, list);
    }
  }
  throw new CodedError('not_available', 'This is not available on this server yet.');
}
