/**
 * Saved on the rebuilt server: My List and Favorites (GET /v1/items?inList / ?favorite), the profile's playlists
 * (GET /v1/lists?kind=playlist), a playlist's own page (GET /v1/items?list={id}, in its own order by default) and its
 * commands. Each reader answers in the shape `saved.ts` checks, so the Saved screens are unchanged.
 *
 * Definition area 6: lists rules 24 to 34; an edit names the list's revision (If-Match), so one made on an out-of-date
 * copy is refused with "This list changed. Reload and try again" (rule 34); removing an entry offers Undo for 30 seconds,
 * which puts it back in its place (rule 32); a list is reordered only in its own order (rule 31).
 */
import type {Item, ItemPage, List, ListAdd, ListAdded, ListCreate, ListCreated, ListPage, ListShares, ListShare} from '../../contracts/src/next/api.ts';
import {resourcePinned, setResourcePin} from './library-pins.ts';
import {callServer, serverPath, type ServerRequester} from './query/server-api.ts';
import {wireEntryFromItem} from './items.ts';
import {CodedError} from './server-messages.ts';
import type {SavedIntent, SavedRoute, SavedScope} from './saved.ts';

type ListRoute = Exclude<SavedRoute, {view: 'playlist'}>;
type PlaylistRoute = Extract<SavedRoute, {view: 'playlist'}>;

/** A transport that can also send per-request headers (`HttpLocalApi.requestRaw`). Every list change needs it:
 * If-Match on an edit, Idempotency-Key on an addition. */
export type SavedNextApi = ServerRequester & Partial<Readonly<{requestRaw(path: string, method: 'POST' | 'PATCH' | 'PUT' | 'DELETE', options?: Readonly<{headers?: Readonly<Record<string, string>>; body?: unknown; signal?: AbortSignal}>): Promise<{status: number; headers: Record<string, string>; body?: unknown}>}>>;

const PAGE = 40;
/** The kinds each Saved shape draws: squares are music, posters everything else. */
const shapeKinds: Readonly<Record<'posters' | 'squares', string>> = {posters: 'movie,show,season,episode,audiobook,book,comic,series,collection', squares: 'artist,album,track'};
/** My List and Favorites filters, as GET /v1/items reads them. */
export const listFilters: Readonly<Record<string, Readonly<Record<string, string | boolean>>>> = {all: {}, unwatched: {watched: false}, inProgress: {inProgress: 'true'}, watched: {watched: true}, favorites: {favorite: true}};
const listSorts: Readonly<Record<string, string>> = {title: 'title', added: 'added', year: 'year', duration: 'duration'};
export const pageSorts: Readonly<Record<string, string>> = {position: 'order', title: 'title', year: 'year', duration: 'duration'};
export const filterLabels: Readonly<Record<string, string>> = {all: 'All', unwatched: 'Unwatched', inProgress: 'In progress', watched: 'Watched', favorites: 'Favorites'};
const navigation = [{id: 'watchlist', labelKey: 'saved.watchlist', view: 'watchlist'}, {id: 'favorites', labelKey: 'saved.favorites', view: 'favorites'}, {id: 'playlists', labelKey: 'saved.playlists', view: 'playlists'}];
const notYet = (): never => {throw new CodedError('not_available', 'This is not available on this server yet.');};
export const revisionOf = (list: Pick<List, 'revision'>) => {const n = Number(list.revision); return Number.isSafeInteger(n) && n >= 0 ? n : 0;};

export async function items(api: SavedNextApi, query: Readonly<Record<string, string | number | boolean | undefined>>, signal?: AbortSignal): Promise<ItemPage> {
  const page = await callServer(api, 'GET /v1/items', {query, signal});
  if (!page || !Array.isArray(page.items)) throw new CodedError('invalid_saved', 'The saved list could not be read.');
  return page;
}
/** How many titles a filtered list holds: one counted read of a single title. */
const total = async (api: SavedNextApi, query: Readonly<Record<string, string | number | boolean | undefined>>, signal?: AbortSignal) =>
  (await items(api, {...query, limit: 1, total: true}, signal)).total ?? 0;

function wireEntries(page: ItemPage): unknown[] {return page.items.map(item => wireEntryFromItem(item as Item)).filter(Boolean) as unknown[];}
function scopeOf(bound: SavedScope, view: SavedRoute['view'], entityId = '') {
  return {serverId: bound.serverId, libraryId: '', libraryKind: 'mixed', view, entityId, viewerFence: 'viewer'};
}

/** My List or Favorites: one page, with the filter counts and the shape counts the page draws. */
async function savedList(api: SavedNextApi, bound: SavedScope, selected: ListRoute, cursor: string | null, signal?: AbortSignal): Promise<unknown> {
  const base = selected.view === 'watchlist' ? {inList: true} : {favorite: true};
  const filter = (selected as {filter?: string}).filter ?? 'all', shape = (selected as {shape?: 'posters' | 'squares'}).shape;
  const filters = selected.view === 'watchlist' ? ['all', 'unwatched', 'inProgress', 'watched', 'favorites'] : ['all', 'unwatched', 'inProgress', 'watched'];
  if (!filters.includes(filter)) notYet();
  const sort = selected.sort ?? 'added', direction = selected.direction ?? (sort === 'title' ? 'asc' : 'desc');
  if (!listSorts[sort]) notYet();
  const kinds = shape ? {kind: shapeKinds[shape]} : {};
  const [page, counts, posters, squares] = await Promise.all([
    items(api, {...base, ...listFilters[filter], ...kinds, sort: listSorts[sort], order: direction, limit: PAGE, total: true, cursor: cursor ?? undefined}, signal),
    Promise.all(filters.map(name => total(api, {...base, ...listFilters[name], ...kinds}, signal))),
    total(api, {...base, kind: shapeKinds.posters}, signal), total(api, {...base, kind: shapeKinds.squares}, signal),
  ]);
  return {scope: scopeOf(bound, selected.view), revision: {catalog: 0, viewer: 0}, heading: {key: 'saved.' + selected.view, fallback: ''},
    query: {sort, direction, category: filter, q: '', limit: PAGE, searchMode: 'none'}, navigation,
    sorts: Object.keys(listSorts).map(id => ({id, labelKey: 'sort.' + id, directions: ['asc', 'desc']})),
    filters: [{id: 'filter', labelKey: 'saved.filter', options: filters.map((name, n) => ({id: name, label: filterLabels[name], count: counts[n]}))}],
    shapeCounts: {posters, squares},
    sections: [{id: 'items', type: 'grid', heading: {key: 'saved.' + selected.view, fallback: ''}, entries: wireEntries(page), totalCount: page.total ?? page.items.length, nextCursor: page.nextCursor ?? '', start: 0}]};
}

/** The profile's playlists: its own and those shared with it, as cards. */
async function playlists(api: SavedNextApi, bound: SavedScope, selected: SavedRoute, cursor: string | null, signal?: AbortSignal): Promise<unknown> {
  const filter = (selected as {filter?: string}).filter ?? 'all';
  if (filter === 'pinned') notYet();
  const page: ListPage = await callServer(api, 'GET /v1/lists', {query: {kind: 'playlist', limit: PAGE, cursor: cursor ?? undefined}, signal});
  if (!page || !Array.isArray(page.items)) throw new CodedError('invalid_saved', 'The playlists could not be read.');
  const shown = page.items.filter(list => list.kind === 'playlist' && (filter === 'all' || (filter === 'mine') === (list.role === 'owner')));
  const cards = shown.map(list => ({id: list.id, kind: 'playlist', title: list.name, ...(list.description ? {subtitle: list.description} : {}), count: list.count,
    ...(list.updatedAt ? {lastChanged: list.updatedAt.replace(/\.\d+Z$/, 'Z')} : {}), artworkPaths: list.covers.map(images => images.poster ?? images.cover ?? images.thumb).filter(Boolean).slice(0, 4),
    navigation: {view: 'playlist', entityId: list.id}}));
  return {scope: scopeOf(bound, 'playlists'), revision: {catalog: 0, viewer: 0}, heading: {key: 'saved.playlists', fallback: ''},
    query: {sort: selected.sort ?? 'updated', direction: selected.direction ?? 'desc', category: '', q: '', limit: PAGE, searchMode: 'none'}, navigation,
    sorts: [], filters: [{id: 'filter', labelKey: 'saved.filter', options: ['all', 'mine', 'shared'].map(id => ({id, label: id, count: 0}))}], actions: ['create_playlist'],
    sections: [{id: 'playlists', type: 'grid', heading: {key: 'saved.playlists', fallback: ''}, entries: cards, totalCount: cards.length, nextCursor: page.nextCursor ?? '', start: 0}]};
}

/** One playlist as `saved.ts` reads it: who made it, this viewer's role, and what that role may do. Anyone who sees a
 * playlist may pin it: a pin is the profile's own (its `pins` setting, area 6 rule 38). When the pins cannot be read,
 * Pin is left out and the playlist still opens. */
async function playlistResource(api: SavedNextApi, bound: SavedScope, list: List, signal?: AbortSignal): Promise<Record<string, unknown>> {
  const owner = list.role === 'owner';
  const [shares, pinned] = await Promise.all([owner ? callServer(api, 'GET /v1/lists/{id}/shares', {id: list.id, signal}) : undefined, resourcePinned(api, 'playlist', list.id, signal)]);
  return {serverId: bound.serverId, viewerFence: 'viewer', id: list.id, name: list.name, summary: list.description ?? '', revision: revisionOf(list), pinned: pinned === true, pinRevision: 0,
    role: list.role === 'owner' || list.role === 'editor' ? list.role : 'viewer', entryCount: list.count,
    actions: [...(owner ? ['rename', 'summary', 'delete', 'share', 'add', 'remove', 'reorder'] : list.role === 'editor' ? ['add', 'remove', 'reorder'] : []), ...(pinned === undefined ? [] : ['pin'])],
    limits: {maxShares: 100, maxReorderEntries: 200},
    ...(shares ? {shares: (shares.shares ?? []).map((share: ListShare) => ({authority: 'local', accountId: share.profileId, profileId: share.profileId, role: share.canEdit ? 'editor' : 'viewer', displayName: share.name ?? share.person ?? ''}))} : {})};
}

/** A playlist's page and the playlist itself, read together so their revisions match. Its own order (`position`) reads
 * the whole list's last entry too, which moving to the end names. */
async function playlistPage(api: SavedNextApi, bound: SavedScope, selected: PlaylistRoute, cursor: string | null, signal?: AbortSignal): Promise<{page: unknown; resource: unknown}> {
  const sort = selected.sort ?? 'position', filter = selected.filter ?? 'all', manual = sort === 'position' && filter === 'all';
  if (!pageSorts[sort] || !listFilters[filter]) notYet();
  const direction = selected.direction ?? 'asc';
  const [list, page, last] = await Promise.all([
    callServer(api, 'GET /v1/lists/{id}', {id: selected.playlistId, signal}),
    items(api, {list: selected.playlistId, ...listFilters[filter], sort: pageSorts[sort], order: sort === 'position' ? undefined : direction, limit: PAGE, total: true, cursor: cursor ?? undefined}, signal),
    manual ? items(api, {list: selected.playlistId, sort: 'order', order: 'desc', limit: 1}, signal) : Promise.resolve(undefined),
  ]);
  const entries = page.items.flatMap(item => {const media = wireEntryFromItem(item as Item); return media && item.entryId ? [{id: item.entryId, kind: 'playlist_entry', hidden: false, media}] : [];});
  const lastEntry = last?.items[0]?.entryId;
  const resource = await playlistResource(api, bound, list, signal);
  return {resource, page: {scope: scopeOf(bound, 'playlist', selected.playlistId), revision: {catalog: 0, viewer: 0}, playlistRevision: revisionOf(list), heading: {key: 'saved.playlist', fallback: list.name},
    query: {sort, direction, category: filter, q: '', limit: PAGE, searchMode: 'none'}, navigation: [],
    sorts: Object.keys(pageSorts).map(id => ({id, labelKey: 'sort.' + id, directions: id === 'position' ? ['asc'] : ['asc', 'desc']})),
    filters: [{id: 'filter', labelKey: 'saved.filter', options: ['all', 'unwatched', 'inProgress', 'watched'].map(id => ({id, label: filterLabels[id], count: 0}))}],
    sections: [{id: 'entries', type: 'list', heading: {key: 'saved.playlist', fallback: list.name}, entries, totalCount: page.total ?? entries.length, nextCursor: page.nextCursor ?? '', start: 0,
      ...(manual && lastEntry ? {order: {last: lastEntry}} : {})}]}};
}

/** One Saved page and, for a playlist, the playlist: the reads `saved.ts` makes, on the rebuilt server. Collections, saved
 * views and History are `personal-saved.ts`'s. */
export async function readSavedPage(api: SavedNextApi, bound: SavedScope, selected: SavedRoute, cursor: string | null, signal?: AbortSignal): Promise<{page: unknown; resource: unknown}> {
  switch (selected.view) {
    case 'watchlist': case 'favorites': return {page: await savedList(api, bound, selected as ListRoute, cursor, signal), resource: null};
    case 'playlists': return {page: await playlists(api, bound, selected, cursor, signal), resource: null};
    case 'playlist': return playlistPage(api, bound, selected as PlaylistRoute, cursor, signal);
    default: return notYet();
  }
}

/** A playlist's entries in its own order, a page at a time (a hundred unless asked otherwise). */
export async function readPlaylistOrder(api: SavedNextApi, playlistId: string, cursor: string | null, signal?: AbortSignal, limit = 100): Promise<{revision: number; entryIds: string[]; nextCursor: string}> {
  const [list, page] = await Promise.all([callServer(api, 'GET /v1/lists/{id}', {id: playlistId, signal}), items(api, {list: playlistId, sort: 'order', limit, cursor: cursor ?? undefined}, signal)]);
  return {revision: revisionOf(list), entryIds: page.items.map(item => item.entryId).filter((id): id is string => !!id), nextCursor: page.nextCursor ?? ''};
}

/** Who a playlist could be shared with: everyone else on the server the viewer can see (area 6 rule 27). */
export async function readShareCandidates(api: SavedNextApi, playlistId: string, cursor: string | null, signal?: AbortSignal): Promise<{revision: number; candidates: unknown[]; nextCursor: string}> {
  const [list, shares] = await Promise.all([callServer(api, 'GET /v1/lists/{id}', {id: playlistId, signal}), callServer(api, 'GET /v1/lists/{id}/shares', {id: playlistId, signal})]);
  const shared = new Set((shares.shares ?? []).map(share => share.profileId)), start = Number(cursor ?? 0) || 0;
  const everyone = (shares.people ?? []).filter(person => !shared.has(person.profileId));
  // Forty at a time, as the share picker pages.
  return {revision: revisionOf(list), nextCursor: start + 40 < everyone.length ? String(start + 40) : '', candidates: everyone.slice(start, start + 40)
    .map(person => ({id: person.profileId, authority: 'local', accountId: person.profileId, profileId: person.profileId, displayName: person.name ?? person.person ?? ''}))};
}

const unsupported = (): never => {throw new CodedError('saved_unsupported', 'This app cannot send list changes safely.');};
/** A change sent with its own headers (If-Match, Idempotency-Key). An edit needs If-Match, so through a transport that
 * cannot send headers it is refused before anything is sent: it must never go without one and overwrite (rule 34). */
export async function sendRaw<T>(api: SavedNextApi, path: string, method: 'POST' | 'PATCH' | 'PUT' | 'DELETE', headers: Readonly<Record<string, string>>, body: unknown, signal?: AbortSignal): Promise<T> {
  if (!api.requestRaw) return unsupported();
  const answer = await api.requestRaw(path, method, {headers, body, signal});
  if (answer.status >= 400) {
    const error = (answer.body as {error?: {code?: string; message?: string; retryable?: boolean}} | undefined)?.error;
    throw Object.assign(new Error(error?.message ?? (answer.status === 412 ? 'This list changed. Reload and try again.' : 'The list could not be changed.')),
      {status: answer.status, code: error?.code ?? (answer.status === 412 ? 'list_changed' : 'request_failed'), retryable: error?.retryable ?? answer.status >= 500});
  }
  return answer.body as T;
}
/** An edit that names the list's revision: refused with 412 `list_changed` when the list moved on (area 6 rule 34).
 * An edit with no revision to name is refused here, as an out-of-date copy would be. */
export async function edit<T>(api: SavedNextApi, path: string, method: 'PATCH' | 'PUT' | 'DELETE', revision: number | undefined, body: unknown, signal?: AbortSignal): Promise<T> {
  if (!api.requestRaw) return unsupported();
  if (revision === undefined) throw Object.assign(new CodedError('list_changed', 'This list changed. Reload and try again.'), {status: 412});
  return sendRaw<T>(api, path, method, {'If-Match': `"${revision}"`}, body, signal);
}

/** An Undo of a removal: the list it was removed from, the title, and the entry it came after (empty at the front). */
/** `before`: for a list's first entry, the entry that came after it, which the title goes back in front of. */
export type UndoToken = Readonly<{listId: string; itemId: string; after: string; before?: string}>;
export const encodeUndo = (token: UndoToken) => 'undo.' + btoa(JSON.stringify(token)).replace(/=+$/, '');
export function decodeUndo(raw: string): UndoToken {
  try {const value = JSON.parse(atob(raw.slice(5))); if (raw.startsWith('undo.') && typeof value?.listId === 'string' && typeof value.itemId === 'string' && typeof value.after === 'string' && (value.before === undefined || typeof value.before === 'string')) return value;} catch {/* refused below */}
  throw new CodedError('undo_expired', 'Too late to undo.');
}

/** Undo of a removal: the title goes back just after the entry that was in front of it (area 6 rule 32). A list's first
 * entry has none in front, and an add with no `after` goes to the end; so its token names the entry that came after it
 * (`before`), the title is added just after that one, and then moved in front of it. The add's body is the same on a
 * repeat, so its key holds; entries are told apart by their ids, so a twin of the title is never moved, and a repeat
 * after the move finds that entry no longer first and moves nothing. If the list changed so that entry is not first,
 * the title stays just after it. */
export async function restoreEntry(api: SavedNextApi, token: UndoToken, add: (body: ListAdd) => Promise<ListAdded>, signal?: AbortSignal): Promise<ListAdded> {
  if (token.after || !token.before) return add({items: [token.itemId], ...(token.after ? {after: token.after} : {})});
  const done = await add({items: [token.itemId], after: token.before});
  const [first, restored] = (await items(api, {list: token.listId, sort: 'order', limit: 2}, signal)).items;
  if (first?.entryId !== token.before || !restored?.entryId || restored.id !== token.itemId) return done;
  const path = serverPath('PATCH /v1/lists/{id}/entries/{entryId}', {id: token.listId, ids: {entryId: restored.entryId}});
  return {...done, list: await edit<List>(api, path, 'PATCH', revisionOf(done.list), {before: first.entryId}, signal)};
}

export type PlaylistCommandContext = Readonly<{
  playlistId?: string; revision?: number;
  /** The playlist's order as shown: each entry's id and title, so a removal can be undone and a move placed. */
  entries?: readonly Readonly<{id: string; itemId: string}>[];
}>;
/** One playlist command on the rebuilt server, answered in the receipt shape `saved.ts` checks. */
export async function sendPlaylistCommand(api: SavedNextApi, bound: SavedScope, intent: SavedIntent, context: PlaylistCommandContext, operationId: string, signal?: AbortSignal): Promise<Record<string, unknown>> {
  const receipt = (playlistId: string, list: Pick<List, 'revision'> | undefined, extra: Record<string, unknown> = {}) =>
    ({serverId: bound.serverId, viewerFence: 'viewer', operationId, playlistId, revision: list ? revisionOf(list) : (context.revision ?? 0) + 1, deleted: false, ...extra});
  // An addition names its operation as its Idempotency-Key, which Retry keeps: a repeat whose first answer was lost
  // adds nothing twice (a playlist would take a title twice, area 6 rule 24). It overwrites nothing, so a transport
  // that cannot send headers (the pickers outside Saved) still sends it, without the key.
  const once = {'Idempotency-Key': operationId};
  const addTo = (id: string, body: ListAdd) => api.requestRaw ? sendRaw<ListAdded>(api, serverPath('POST /v1/lists/{id}/entries', {id}), 'POST', once, body, signal)
    : callServer(api, 'POST /v1/lists/{id}/entries', {id, body, signal});
  if (intent.action === 'create') {
    const body: ListCreate = {kind: 'playlist', name: intent.name.trim(), ...(intent.summary ? {description: intent.summary} : {}), ...(intent.itemIds?.length ? {items: [...intent.itemIds]} : {})};
    const created = api.requestRaw ? await sendRaw<ListCreated>(api, serverPath('POST /v1/lists'), 'POST', once, body, signal) : await callServer(api, 'POST /v1/lists', {body, signal});
    return receipt(created.id, created);
  }
  const id = context.playlistId;
  if (!id) throw new Error('Playlist is not selected.');
  const listPath = serverPath('GET /v1/lists/{id}', {id}), entryPath = (entryId: string) => `${listPath}/entries/${encodeURIComponent(entryId)}`;
  switch (intent.action) {
    case 'rename': return receipt(id, await edit<List>(api, listPath, 'PATCH', context.revision, {...(intent.name === undefined ? {} : {name: intent.name.trim()}), ...(intent.summary === undefined ? {} : {description: intent.summary})}, signal));
    case 'add': return receipt(id, (await addTo(id, {items: [intent.itemId]})).list);
    case 'restore': {
      const token = decodeUndo(intent.undoToken);
      if (token.listId !== id) throw new CodedError('undo_other_list', 'This can only be undone in the list it was removed from.');
      return receipt(id, (await restoreEntry(api, token, body => addTo(id, body), signal)).list);
    }
    case 'remove': {
      const order = context.entries ?? [], at = order.findIndex(entry => entry.id === intent.entryId);
      const list = await edit<List>(api, entryPath(intent.entryId), 'DELETE', context.revision, undefined, signal);
      // Undo puts the title back just after the entry in front of it (area 6 rule 32).
      const undo = at >= 0 ? {undoToken: encodeUndo({listId: id, itemId: order[at]!.itemId, after: at > 0 ? order[at - 1]!.id : '', ...(at === 0 && order[1] ? {before: order[1].id} : {})}), undoUntil: new Date(Date.now() + 30_000).toISOString().replace(/\.\d+Z$/, 'Z')} : {};
      return receipt(id, list, {entryId: intent.entryId, ...undo});
    }
    case 'reorder': {
      // Each entry goes just after the one before it: the first after the anchor ("" the front). Without an anchor the
      // entries keep the place of the first of them, which is how two neighbours swap.
      const order = context.entries ?? [], ids = intent.entryIds;
      let anchor = intent.afterEntryId;
      if (anchor === undefined) {const first = Math.min(...ids.map(entry => order.findIndex(row => row.id === entry)).filter(n => n >= 0)); anchor = Number.isFinite(first) && first > 0 ? order[first - 1]!.id : '';}
      let list: List | undefined, revision = context.revision;
      for (const [n, entryId] of ids.entries()) {
        const after = n === 0 ? anchor : ids[n - 1]!;
        const first = order.find(row => !ids.includes(row.id))?.id;
        const body = after ? {after} : first ? {before: first} : undefined;
        if (!body) continue;
        try {list = await edit<List>(api, entryPath(entryId), 'PATCH', revision, body, signal);}
        // Once a move has landed the list is no longer the one on screen: say so, so it is read again.
        catch (error) {throw list && error && typeof error === 'object' ? Object.assign(error, {partlyMade: true}) : error;}
        revision = revisionOf(list);
      }
      return receipt(id, list);
    }
    case 'delete': await edit<void>(api, listPath, 'DELETE', context.revision, undefined, signal); return receipt(id, undefined, {deleted: true});
    case 'share': case 'unshare': {
      const current = await callServer(api, 'GET /v1/lists/{id}/shares', {id, signal});
      const others = (current.shares ?? []).filter(share => share.profileId !== intent.actor.profileId).map(share => ({profileId: share.profileId, canEdit: share.canEdit}));
      const shares = intent.action === 'share' ? [...others, {profileId: intent.actor.profileId, canEdit: intent.role === 'editor'}] : others;
      return receipt(id, await edit<List>(api, `${listPath}/shares`, 'PUT', context.revision, {shares}, signal));
    }
    case 'pin': await setResourcePin(api, 'playlist', id, intent.pinned, 0, operationId); return receipt(id, undefined, {revision: context.revision ?? 0});
  }
  throw new Error('Invalid playlist command.');
}

/** The Saved landing page's counts, read from the lists themselves (the rebuilt server keeps no summary). */
export async function readSavedSummary(api: SavedNextApi, bound: SavedScope, signal?: AbortSignal): Promise<unknown> {
  const counts = (base: Readonly<Record<string, boolean>>) => Promise.all([total(api, {...base, kind: shapeKinds.posters}, signal), total(api, {...base, kind: shapeKinds.squares}, signal)]);
  const allLists = async () => {
    const lists: List[] = [];
    for (let cursor: string | undefined, pages = 0; pages < 20; pages++) {
      const page: ListPage = await callServer(api, 'GET /v1/lists', {query: {limit: 200, cursor}, signal});
      lists.push(...(page?.items ?? []));
      if (!page?.nextCursor) break;
      cursor = page.nextCursor;
    }
    return lists;
  };
  const [lists, myList, favorites] = await Promise.all([allLists(), counts({inList: true}), counts({favorite: true})]);
  const of = (kind: string) => lists.filter(list => list.kind === kind).length;
  return {serverId: bound.serverId, viewerFence: 'viewer', revision: {catalog: 0, viewer: 0}, myList: {posters: myList[0], squares: myList[1]}, favorites: {posters: favorites[0], squares: favorites[1]},
    playlists: of('playlist'), collections: of('collection'), savedViews: of('saved_view')};
}

/** What `SavedService` asks the server, as one port: the rebuilt server's by default. A test can give its own. */
export type SavedTransport = Readonly<{
  read: typeof readSavedPage; send: typeof sendPlaylistCommand; order: typeof readPlaylistOrder; candidates: typeof readShareCandidates;
}>;
export const savedTransport: SavedTransport = Object.freeze({read: readSavedPage, send: sendPlaylistCommand, order: readPlaylistOrder, candidates: readShareCandidates});
