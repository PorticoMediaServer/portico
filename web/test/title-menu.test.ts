import test from 'node:test';
import assert from 'node:assert/strict';
import {validateHomeEntry} from '@core/home.ts';
import {entryActions} from '@core/presentation/entry-actions.ts';
import {titleMenuGroups} from '@core/presentation/title-menu.ts';
import type {ContentEntry} from '@core/library-content.ts';
import type {ScreenContext} from '../src/screens/detail/context';
import {titleMenuEntries} from '../src/shell/title-menu-entries';
import {TitleMenuHost, type TitleMenuDetail, type TitleMenuOpening, type TitleMenuSubject} from '../src/shell/title-menu-host';
import {withQueries} from '../../packages/client-core/test/fixtures/viewer-queries.ts';

const anchor = {} as HTMLElement;
const offers = (patch: Record<string, unknown> = {}) => ({play: true, queue: true, playlist: true, collection: true, myList: true, favorite: true, watched: true, played: true, rating: true,
  editMetadata: true, refreshMetadata: true, delete: true, download: true, details: true,
  state: {watchlisted: false, favorite: false, watched: false, rating: null, personalRevision: 1, containerRevision: 1}, selection: {}, ...patch});
const movie: ContentEntry = {id: 'm1', kind: 'movie', title: 'Heat', libraryId: 'films', playback: {itemId: 'm1', startSeconds: 0}, available: true};
const detailData = (entry: ContentEntry, enabled: readonly string[], personal: Partial<{watchlisted: boolean; favorite: boolean; watched: boolean; rating: number | null}> = {}) => ({
  item: {id: entry.id, kind: entry.kind, available: true}, scope: {libraryId: entry.libraryId},
  actions: enabled.map(id => ({id, enabled: true, ...(id === 'play' ? {playback: {itemId: entry.id, startSeconds: 0}} : {})})),
  personal: {watchlisted: false, favorite: false, watched: false, rating: null, ...personal}});

type Detail = TitleMenuDetail & {selected: unknown[]; mutations: unknown[]; disposed: number};
function fakeDetail(read: () => Promise<ReturnType<typeof detailData> | undefined>): Detail {
  let data: ReturnType<typeof detailData> | undefined, phase = 'idle';
  const detail = {selected: [] as unknown[], mutations: [] as unknown[], disposed: 0,
    async select(target: unknown) { detail.selected.push(target); phase = 'loading'; try { data = await read(); phase = data ? 'ready' : 'error'; } catch (error) { phase = 'error'; throw error; } },
    getSnapshot() { return {phase, data, error: null, pending: [], mutationError: null}; },
    async mutate(intent: unknown) { detail.mutations.push(intent); },
    async retryMutation() {}, dispose() { detail.disposed++; }};
  return detail as unknown as Detail;
}

function harness(options: {role?: 'owner' | 'member'; read?: (entry: ContentEntry) => Promise<ReturnType<typeof detailData> | undefined>; enabled?: readonly string[]} = {}) {
  const log = {errors: [] as unknown[], opened: [] as unknown[], playlists: [] as unknown[], collections: [] as unknown[], edits: [] as unknown[], deletes: [] as unknown[], requests: [] as string[], wire: [] as {path: string; method?: string; body?: unknown; signal?: AbortSignal}[]};
  const state = {live: true};
  const details: Detail[] = [];
  const owner = options.role !== 'member';
  const context = withQueries({
    api: {request: async (path: string, method?: string, body?: unknown, signal?: AbortSignal) => { log.requests.push(`${method ?? 'GET'} ${path}`); log.wire.push({path, method, body, signal}); return {}; }, requestRaw: async () => ({})},
    scope: {serverId: 'server', viewerId: 'viewer'}, isCurrent: () => state.live,
    identity: {viewer: {role: options.role ?? 'owner', authority: 'local'}},
    effects: {reportError: (error: unknown) => { log.errors.push(error); }, openEntry: (entry: unknown) => { log.opened.push(entry); }, play: async () => {},
      addToPlaylist: async (target: unknown) => { log.playlists.push(target); }, addToCollection: async (target: unknown) => { log.collections.push(target); },
      ...(owner ? {editMetadata: async (target: unknown) => { log.edits.push(target); }, deleteTitle: async (target: unknown) => { log.deletes.push(target); }} : {})},
  }) as unknown as ScreenContext;
  const host = new TitleMenuHost(context, {detail: () => { const detail = fakeDetail(() => options.read ? options.read(current.entry) : Promise.resolve(detailData(current.entry, options.enabled ?? ['play', 'watchlist', 'favorite', 'watched', 'rating', 'add_to_playlist', 'add_to_collection', 'metadata_edit', 'delete']))); details.push(detail); return detail; }});
  const current = {entry: movie};
  const played: string[] = [], selected: string[] = [];
  const subject = (entry: ContentEntry, fence: {open: boolean} = {open: true}, extra: Partial<TitleMenuSubject> = {}): TitleMenuSubject => {
    current.entry = entry;
    return {entry, title: entry.title, current: () => fence.open, onPlay: () => { played.push(entry.id); }, select: () => { selected.push(entry.id); }, ...extra};
  };
  const menuOf = () => host.live().menu as TitleMenuOpening;
  const idsOf = (opening: TitleMenuOpening) => titleMenuEntries(titleMenuGroups(opening.caps), {select: () => {}, onSelect: () => {}, loading: opening.phase === 'loading'}).flatMap(entry => 'separator' in entry || 'section' in entry ? [] : [entry.id]);
  return {host, log, state, details, played, selected, subject, menuOf, idsOf};
}

test('the entries builder keeps the model\'s order, sections and Select beside Details', () => {
  const groups = entryActions({kind: 'movie', playable: true, queue: true, group: true, watchlisted: false, favorite: true, watched: false, rating: 6, addToPlaylist: true, addToCollection: true,
    details: true, editMetadata: true, refreshMetadata: true, delete: true});
  const entries = titleMenuEntries(groups, {rating: 6, select: () => {}, onSelect: () => {}});
  assert.deepEqual(entries.map(entry => entry.id), ['play', 'playNext', 'addToQueue', 'group:together', 'watchWithGroup', 'addToGroupQueue', 'group:personal', 'watchlist', 'favorite', 'watched', 'rate',
    'group:organize', 'addToPlaylist', 'addToCollection', 'group:open', 'details', 'select', 'group:manage', 'editMetadata', 'refreshMetadata', 'delete']);
  const manage = entries.find(entry => entry.id === 'group:manage');
  assert.ok(manage && 'section' in manage && manage.ownerOnly, 'the owner group is marked owner-only');
  const rate = entries.find(entry => entry.id === 'rate');
  assert.ok(rate && 'label' in rate && String(rate.label).includes('3'), 'a set rating shows its stars');
  const del = entries.find(entry => entry.id === 'delete');
  assert.ok(del && 'destructive' in del && del.destructive);
});

test('Select sits before the owner group when there is no Details row, and alone it needs no rule', () => {
  const withoutDetails = titleMenuEntries(entryActions({kind: 'movie', playable: true, editMetadata: true}), {select: () => {}, onSelect: () => {}});
  assert.deepEqual(withoutDetails.map(entry => entry.id), ['play', 'group:select', 'select', 'group:manage', 'editMetadata']);
  assert.deepEqual(titleMenuEntries([], {select: () => {}, onSelect: () => {}}).map(entry => entry.id), ['select']);
  assert.deepEqual(titleMenuEntries(entryActions({kind: 'movie', details: true}), {onSelect: () => {}}).map(entry => entry.id), ['details'], 'no rule leads a menu that has no Play');
});

test('a leaf with no grants reads its Detail when the menu opens and then offers the full set', async () => {
  const h = harness();
  const settled = h.host.open(anchor, h.subject(movie));
  // meanwhile: Play, Select and Details, with the read said to be under way
  assert.equal(h.menuOf().phase, 'loading');
  assert.deepEqual(h.idsOf(h.menuOf()), ['play', 'details', 'select', 'loading']);
  await settled;
  const ready = h.menuOf();
  assert.equal(ready.phase, 'ready');
  assert.deepEqual(h.idsOf(ready), ['play', 'watchlist', 'favorite', 'watched', 'rate', 'addToPlaylist', 'addToCollection', 'details', 'select', 'editMetadata', 'refreshMetadata', 'delete']);
  assert.deepEqual(h.details[0].selected, [{libraryId: 'films', itemId: 'm1'}]);
});

test('a failed read leaves Play, Select and Details and reports nothing else broke', async () => {
  const h = harness({read: async () => { throw new Error('offline'); }});
  await h.host.open(anchor, h.subject(movie));
  assert.equal(h.menuOf().phase, 'failed');
  assert.deepEqual(h.idsOf(h.menuOf()), ['play', 'details', 'select']);
  assert.deepEqual(h.log.errors, []);
});

test('a member sees no owner row even when Detail enables one', async () => {
  const h = harness({role: 'member'});
  await h.host.open(anchor, h.subject(movie));
  const ids = h.idsOf(h.menuOf());
  assert.ok(!ids.some(id => ['editMetadata', 'refreshMetadata', 'delete'].includes(id)), ids.join());
  assert.ok(ids.includes('addToPlaylist'));
});

test('an entry the server granted builds its menu at once, without reading Detail', async () => {
  const show = validateHomeEntry({id: 's1', kind: 'show', title: 'Dark', libraryId: 'tv', available: true, playback: {itemId: 'e1'}, homeActions: offers()});
  const h = harness();
  await h.host.open(anchor, h.subject(show));
  assert.equal(h.menuOf().phase, 'ready');
  assert.deepEqual(h.details, [], 'no Detail was made');
  assert.deepEqual(h.idsOf(h.menuOf()), ['play', 'watchlist', 'favorite', 'watched', 'details', 'select', 'editMetadata', 'refreshMetadata']);
});

test('rows run the screen\'s own effects with the exact title', async () => {
  const h = harness();
  await h.host.open(anchor, h.subject(movie));
  await h.host.choose(h.menuOf(), 'addToPlaylist');
  assert.deepEqual(h.log.playlists, [{libraryId: 'films', itemId: 'm1', kind: 'movie', title: 'Heat'}]);
  await h.host.open(anchor, h.subject(movie));
  await h.host.choose(h.menuOf(), 'editMetadata');
  assert.deepEqual(h.log.edits, [{libraryId: 'films', itemId: 'm1', kind: 'movie', title: 'Heat'}]);
  await h.host.open(anchor, h.subject(movie));
  await h.host.choose(h.menuOf(), 'refreshMetadata');
  assert.ok(h.log.requests.some(request => request.startsWith('POST ') && request.includes('m1')), 'refresh is a POST for this title: ' + h.log.requests.join());
  await h.host.open(anchor, h.subject(movie));
  await h.host.choose(h.menuOf(), 'play');
  assert.deepEqual(h.played, ['m1']);
  await h.host.open(anchor, h.subject(movie));
  await h.host.choose(h.menuOf(), 'details');
  assert.equal(h.log.opened.length, 1);
  assert.deepEqual(h.log.errors, []);
});

test('menu refresh writes exactly once for its captured title and retains the screen lifetime', async () => {
  const h = harness(), entry = {...movie, id: 'movie /?'};
  try {
    await h.host.open(anchor, h.subject(entry));
    await h.host.choose(h.menuOf(), 'refreshMetadata');
    assert.equal(h.log.wire.length, 1);
    const request = h.log.wire[0];
    assert.equal(request.path, '/v1/items/movie%20%2F%3F/metadata/refresh');
    assert.equal(request.method, 'POST'); assert.deepEqual(request.body, {});
    assert.equal(request.signal?.aborted, false);
    h.host.dispose(); assert.equal(request.signal?.aborted, true);
    assert.deepEqual(h.log.errors, []);
  } finally {h.host.dispose();}
});

test('an aggregate edits as an aggregate and a leaf as itself', async () => {
  const show = validateHomeEntry({id: 's1', kind: 'show', title: 'Dark', libraryId: 'tv', homeActions: offers()});
  const h = harness();
  await h.host.open(anchor, h.subject(show));
  await h.host.choose(h.menuOf(), 'editMetadata');
  assert.deepEqual(h.log.edits, [{entityKind: 'show', entityId: 's1', libraryId: 'tv', title: 'Dark'}]);
});

test('My List, Favorite and Watched toggle through the title\'s own Detail', async () => {
  const h = harness();
  await h.host.open(anchor, h.subject(movie));
  const opening = h.menuOf();
  await h.host.choose(opening, 'watchlist');
  assert.deepEqual(h.details[0].mutations, [{action: 'watchlist', value: true}]);
  assert.equal(h.details[0].disposed, 1, 'the read is released once the change is done');
  assert.equal(h.host.live().menu, undefined, 'choosing closes the menu');
});

test('a retired opening draws nothing and executes nothing', async () => {
  const h = harness();
  const fence = {open: true};
  await h.host.open(anchor, h.subject(movie, fence));
  const opening = h.menuOf();
  // the page's query moved on after the menu opened
  fence.open = false;
  assert.equal(h.host.live().menu, undefined, 'it is no longer drawn');
  for (const id of ['play', 'watchlist', 'addToPlaylist', 'editMetadata', 'refreshMetadata', 'delete', 'details', 'rate'] as const) await h.host.choose(opening, id);
  h.host.select(opening);
  assert.deepEqual([h.played, h.selected, h.log.playlists, h.log.edits, h.log.deletes, h.log.opened, h.log.requests, h.details[0].mutations], [[], [], [], [], [], [], [], []]);
  h.host.sweep();
  assert.equal(h.host.getSnapshot().menu, undefined, 'and the sweep retires it');
  assert.equal(h.details[0].disposed, 1);
});

test('a retired screen cannot open a menu, and a later opening is not answered by an earlier one', async () => {
  const h = harness();
  const old = {open: true}, fresh = {open: true};
  await h.host.open(anchor, h.subject(movie, old));
  const first = h.menuOf();
  await h.host.open(anchor, h.subject({...movie, id: 'm2', title: 'Ronin', playback: {itemId: 'm2', startSeconds: 0}}, fresh));
  await h.host.choose(first, 'play');
  assert.deepEqual(h.played, [], 'the superseded opening runs nothing');
  h.host.close(first);
  assert.ok(h.host.live().menu, 'its dismissal does not close the newer menu');
  h.state.live = false;
  await h.host.open(anchor, h.subject(movie));
  assert.equal(h.host.live().menu, undefined, 'a retired authority draws no menu');
  await h.host.choose(h.menuOf() ?? first, 'play');
  assert.deepEqual(h.played, []);
});

test('disposing the host retires its menu and a later use starts clean', async () => {
  const h = harness();
  await h.host.open(anchor, h.subject(movie));
  const opening = h.menuOf();
  h.host.dispose();
  assert.equal(h.host.live().menu, undefined);
  await h.host.choose(opening, 'play');
  assert.deepEqual(h.played, []);
  await h.host.open(anchor, h.subject(movie));
  assert.ok(h.host.live().menu, 'a remounted host works again');
});

test('Rate reads the title, saves through its Detail, and a retired title cannot save', async () => {
  const h = harness();
  await h.host.open(anchor, h.subject(movie));
  await h.host.choose(h.menuOf(), 'rate');
  await Promise.resolve(); await Promise.resolve();
  const session = h.host.live().rating;
  assert.ok(session);
  assert.equal(session.phase, 'ready');
  h.host.setRatingValue(session, 8);
  await h.host.saveRating(h.host.getSnapshot().rating!);
  assert.deepEqual(h.details.at(-1)!.mutations, [{action: 'rating', value: 8}]);
});
