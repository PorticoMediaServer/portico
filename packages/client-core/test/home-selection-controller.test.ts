import assert from 'node:assert/strict';
import test from 'node:test';
import {HomeSelectionController, homeSelectionContext} from '../src/home-selection/index.ts';
import type {HomeDocument, HomeEntry, HomeRow, HomeActions, HomeChannel} from '../src/home.ts';
import {HomeService} from '../src/home-service.ts';
import type {HomeSelectionOptions, HomeSelectionPlaybackRequest} from '../src/home-selection/index.ts';
import {routePath} from '../../contracts/src/routes.ts';
import {homeEntrySelection} from '../src/home-selection.ts';

const fence = 'a'.repeat(64), revision = 'b'.repeat(64);
let sequence = 0;
const requestId = async () => `00000000-0000-4000-8000-${String(++sequence).padStart(12, '0')}`;
const offers = (patch: Partial<HomeActions> = {}): HomeActions => ({play: true, queue: true, playlist: true, collection: true, myList: true, favorite: true, watched: true, played: false, rating: true, editMetadata: true, refreshMetadata: true, delete: true, download: true, details: true, selection: {play: true, queue: true, playlist: true, collection: true, myList: true, favorite: true, watched: true, download: true, edit: true, refresh: true, delete: true}, state: {watchlisted: false, favorite: false, watched: false, rating: 6, personalRevision: 0, containerRevision: 0}, ...patch});
function entry(id: string, kind: HomeEntry['kind'] = 'movie'): HomeEntry {return {id, libraryId: 'library', kind, title: `Title ${id}`, available: true, playback: {itemId: `play-${id}`}, homeActions: offers()};}
function row(entries: readonly HomeEntry[]): HomeRow {return {id: 'recent', viewerFence: fence, title: 'Recent', kind: 'media', artworkShape: 'poster', endpoint: '', privacySensitivity: '', policyState: '', priority: 0, required: false, hideable: true, reorderable: true, critical: false, cursorCapable: true, entries, total: entries.length, start: 0, limit: entries.length, hasMore: false, nextCursor: '', revision: {catalog: 1, viewer: 1}};}
function harness(entries = [entry('a'), entry('b')], request?: HomeSelectionOptions['api']['request'], home?: HomeService) {
  let document: HomeDocument = {serverId: 'server', viewerFence: fence, rows: [row(entries)], spotlight: [], layout: {revision: 1, rowOrder: ['recent'], hiddenRowIds: []}, revision: {catalog: 1, viewer: 1}, generatedAt: '2026-10-22T00:00:00Z'};
  const currentDocument = () => home?.getSnapshot().document ?? document;
  let authorityCurrent = true;
  const listeners = new Set<() => void>(), played: HomeSelectionPlaybackRequest[] = [], queued: HomeSelectionPlaybackRequest[] = [], errors: unknown[] = [];
  const header = {id: 'queue', revision: '1', total: 2, repeat: 'off' as const, segments: []};
  const options: HomeSelectionOptions = {
    api: {request: request ?? (async () => {throw new Error('Unexpected network request');})},
    home: home ?? {getSnapshot: () => ({phase: 'ready', document, paging: new Set()}), subscribe: (listener: () => void) => {listeners.add(listener); return () => listeners.delete(listener);}} as unknown as HomeService,
    events: {on: () => () => {}}, context: homeSelectionContext({serverId: 'server', authority: 'local', accountId: 'owner', profileId: 'profile', viewId: 'home'}, currentDocument()), environment: {platform: 'desktop-web', isOwner: true, canEditMetadata: true}, isCurrent: context => authorityCurrent && JSON.stringify(context) === JSON.stringify(homeSelectionContext({serverId: 'server', authority: 'local', accountId: 'owner', profileId: 'profile', viewId: 'home'}, currentDocument())), requestId,
    playback: {play: async input => {played.push(input); return {queue: header};}, enqueue: async input => {queued.push(input); return {queue: header};}, playChannel: async () => {throw new Error('Unexpected channel playback');}},
    routes: {openEntry: () => {throw new Error('Unexpected navigation');}, openCollection: () => {throw new Error('Unexpected navigation');}, openDestination: () => {throw new Error('Unexpected navigation');}}, reportError: error => errors.push(error),
  };
  const controller = new HomeSelectionController(options);
  return {controller, options, entries, played, queued, errors, get document() {return currentDocument();}, retire() {authorityCurrent = false;}, replace(next: HomeDocument, beforeNotify?: () => void) {document = next; beforeNotify?.(); for (const listener of listeners) listener();}};
}
function pickAll(h: ReturnType<typeof harness>) {h.controller.registerWindow({id: 'window', row: h.document.rows[0], sectionOrder: 0}); h.controller.selectLoaded();}

test('Home Play follows display order, queue follows pick order, aggregate identity retains actual resume child', async () => {
  const a = entry('show', 'show'), b = entry('movie');
  const h = harness([a, b]), target = entry('episode', 'episode');
  h.replace({...h.document, rows: [{...h.document.rows[0], resumes: [{entryId: a.id, target}]}]});
  h.controller.registerWindow({id: 'window', row: h.document.rows[0], sectionOrder: 0});
  h.controller.toggle('window', h.controller.key({kind: b.kind, id: b.id, libraryId: 'library'}));
  h.controller.toggle('window', h.controller.key({kind: a.kind, id: a.id, libraryId: 'library'}));
  await h.controller.execute('play');
  assert.deepEqual(h.played[0].items.map(item => 'id' in item.identity ? item.identity.id : ''), ['show', 'movie']);
  const first = h.played[0].items[0]; assert.ok('entry' in first); assert.equal(first.entry, a); assert.equal(first.target, target.playback);
  await h.controller.execute('play-later');
  assert.deepEqual(h.queued[0].items.map(item => 'id' in item.identity ? item.identity.id : ''), ['movie', 'show']);
  assert.equal(h.errors.length, 0); h.controller.dispose();
});

test('foreign rows, reordered windows and independent spotlight impostors are refused', () => {
  const h = harness();
  assert.throws(() => h.controller.registerWindow({id: 'fake', row: {...h.document.rows[0]}, sectionOrder: 0}), /unoffered-home-row/);
  assert.throws(() => h.controller.registerWindow({id: 'reordered', row: h.document.rows[0], entries: [...h.entries].reverse(), sectionOrder: 0}), /home-window-order-mismatch/);
  const spotlight = {entry: h.entries[0], reason: {kind: 'featured', text: {code: 'featured', fallback: 'Featured'}}};
  h.replace({...h.document, spotlight: [spotlight]});
  assert.throws(() => h.controller.registerSpotlight({id: 'fake-hero', spotlight: {...spotlight}}), /unoffered-home-spotlight/);
  assert.equal(h.controller.registerSpotlight({id: 'hero', spotlight}).ok, true); h.controller.dispose();
});

test('spotlight My List saves the displayed show flag without a fabricated row or descendant job', async () => {
  const calls: {path: string; body: unknown}[] = [];
  const api: HomeSelectionOptions['api']['request'] = async (path, _method, body) => {
    calls.push({path, body});
    return {revision: 1, watched: false, watermark: '', watchlisted: body !== undefined, favorite: false} as never;
  };
  const show = entry('show', 'show'), h = harness([show], api), spotlight = {entry: show, reason: {kind: 'featured', text: {code: 'featured', fallback: 'Featured'}}};
  h.replace({...h.document, spotlight: [spotlight]}); h.controller.registerSpotlight({id: 'hero', spotlight});
  h.controller.openMenu('hero', h.controller.key({kind: 'show', id: 'show', libraryId: 'library'})); await h.controller.executeMenu('watchlist');
  assert.equal(calls.length, 2); assert.match(calls[0].path, /containers\/show\/show\/personal-state$/);
  assert.deepEqual(calls[1].body, {expectedRevision: 0, watchlisted: true}); assert.equal(h.errors.length, 0); h.controller.dispose();
});

test('authority/revision change clears retained picks and blocks late playback completion', async () => {
  const h = harness(); let release!: (value: {queue: {id: string; revision: string; total: number; repeat: 'off'; segments: []}}) => void;
  h.options.playback.play = async () => new Promise(resolve => {release = resolve;});
  pickAll(h); const running = h.controller.execute('play');
  h.replace({...h.document, viewerFence: 'c'.repeat(64), revision: {catalog: 1, viewer: 2}});
  release({queue: {id: 'late', revision: '1', total: 2, repeat: 'off', segments: []}}); await running;
  assert.equal(h.controller.getSnapshot().selection.summary.count, 0); assert.deepEqual(h.controller.getSnapshot().displayMembers, []);
  assert.notEqual(h.controller.getSnapshot().action.phase, 'complete'); h.controller.dispose();
});

function preview(targets: readonly {kind: string; contentKind: string; id: string; libraryId: string}[]) {
  return {serverId: 'server', viewerFence: fence, contentKind: 'movie', kind: 'item', total: targets.length, schema: [{field: 'year', label: 'Year', group: 'numbers', type: 'integer', min: 0, max: 9999, bulk: true}], singleItemFields: [{field: 'year', label: 'Year', group: 'numbers', type: 'integer', min: 0, max: 9999, bulk: true}, {field: 'title', label: 'Title', group: 'general', type: 'text', bulk: false}], excludedFields: [{field: 'title', label: 'Title', group: 'general', type: 'text', bulk: false}], fields: {year: {state: 'shared', value: '2020', distinctCount: 1, values: [{value: '2020', count: targets.length}], entries: [], locked: 0, truncated: false}}, targets: targets.map(target => ({kind: target.kind, id: target.id, libraryId: target.libraryId, expectedRevision: revision}))};
}
test('metadata explicit retry previews only failed targets and retains every actual receipt', async () => {
  const previews: string[][] = [], edits: {operationId: string; targets: {kind: string; id: string}[]}[] = [];
  const api: HomeSelectionOptions['api']['request'] = async (path, _method, raw) => {
    const body = raw as {targets: {kind: string; contentKind: string; id: string; libraryId: string}[]; operationId: string};
    if (path === routePath('post_metadata_bulk_preview')) {previews.push(body.targets.map(target => target.id)); return preview(body.targets) as never;}
    if (path !== routePath('post_metadata_bulk')) throw new Error(`Unexpected route ${path}`);
    edits.push(body);
    const first = edits.length === 1;
    return {operationId: body.operationId, updated: 1, failed: first ? 1 : 0, results: body.targets.map(target => first && target.id === 'b' ? {kind: 'item', id: 'b', ok: false, code: 'metadata_conflict', message: 'Changed'} : {kind: 'item', id: target.id, ok: true, revision: 'd'.repeat(64)})} as never;
  };
  const h = harness(undefined, api); pickAll(h); await h.controller.openEditor(); h.controller.setEditorDraft({fields: {year: {value: '2025'}}, lockEdited: true}); await h.controller.saveEditor();
  assert.equal(h.controller.getSnapshot().editor?.receipts.length, 1); await h.controller.retryEditor();
  assert.deepEqual(previews, [['a', 'b'], ['b']]); assert.deepEqual(edits.map(edit => edit.targets.map(target => target.id)), [['a', 'b'], ['b']]);
  assert.notEqual(edits[0].operationId, edits[1].operationId); assert.deepEqual(h.controller.getSnapshot().editor?.receipts.map(receipt => receipt.operationId), edits.map(edit => edit.operationId)); h.controller.dispose();
});

test('an ambiguous metadata response retries the original byte-identical operation', async () => {
  const bodies: unknown[] = [];
  const api: HomeSelectionOptions['api']['request'] = async (path, _method, raw) => {
    const body = raw as {targets: {kind: string; contentKind: string; id: string; libraryId: string}[]; operationId: string};
    if (path === routePath('post_metadata_bulk_preview')) return preview(body.targets) as never;
    bodies.push(raw); if (bodies.length === 1) throw new Error('Response lost');
    return {operationId: body.operationId, updated: body.targets.length, failed: 0, results: body.targets.map(target => ({kind: target.kind, id: target.id, ok: true, revision: 'd'.repeat(64)}))} as never;
  };
  const h = harness(undefined, api); pickAll(h); await h.controller.openEditor(); h.controller.setEditorDraft({fields: {year: {value: '2025'}}}); await h.controller.saveEditor();
  assert.equal(h.controller.getSnapshot().editor?.phase, 'uncertain'); await h.controller.saveEditor();
  assert.deepEqual(bodies[1], bodies[0]); assert.equal(h.controller.getSnapshot().editor?.receipts.length, 1); h.controller.dispose();
});

test('picker search requests a new server window rather than filtering the displayed page', async () => {
  const requests: {query: string; cursor?: string}[] = [];
  const api: HomeSelectionOptions['api']['request'] = async (path, _method, raw) => {
    assert.equal(path, routePath('post_home_destination_picker')); const body = raw as {kind: string; query: string; cursor?: string}; requests.push(body);
    return {serverId: 'server', viewerFence: fence, kind: body.kind, query: body.query, revision: {catalog: 1, viewer: 1}, rows: [{id: body.query ? 'outside-first-page' : 'first-page', name: body.query || 'Initial', count: 5, revision: 2, canAdd: true, actions: ['add'], artwork: []}], nextCursor: ''} as never;
  };
  const h = harness(undefined, api); pickAll(h); await h.controller.openPicker('playlist'); await h.controller.pickerSearch('Elsewhere');
  assert.deepEqual(requests.map(request => request.query), ['', 'Elsewhere']); assert.equal(h.controller.getSnapshot().picker?.rows[0].id, 'outside-first-page'); h.controller.dispose();
});

test('delete over 200 opens the real cap explanation without requesting or changing media', async () => {
  const h = harness(Array.from({length: 201}, (_, index) => entry(String(index)))); pickAll(h); await h.controller.openDelete();
  assert.equal(h.controller.getSnapshot().deletion?.phase, 'limit'); assert.equal(h.controller.getSnapshot().deletion?.preview, null); h.controller.dispose();
});

test('per-row editor retry rejects successful or duplicate target IDs before another read', async () => {
  let edits = 0, reads = 0;
  const api: HomeSelectionOptions['api']['request'] = async (path, _method, raw) => {
    const body = raw as {targets: {kind: string; contentKind: string; id: string; libraryId: string}[]; operationId: string};
    if (path === routePath('post_metadata_bulk_preview')) {reads++; return preview(body.targets) as never;}
    edits++; return {operationId: body.operationId, updated: 1, failed: 1, results: [{kind: 'item', id: 'a', ok: true, revision}, {kind: 'item', id: 'b', ok: false, code: 'metadata_conflict', message: 'Changed'}]} as never;
  };
  const h = harness(undefined, api); pickAll(h); await h.controller.openEditor(); h.controller.setEditorDraft({fields: {year: {value: '2025'}}}); await h.controller.saveEditor();
  await assert.rejects(h.controller.retryEditor(h.controller.getSnapshot().editor!.draft, ['a']), /invalid-editor-retry-targets/);
  await assert.rejects(h.controller.retryEditor(h.controller.getSnapshot().editor!.draft, ['b', 'b']), /invalid-editor-retry-targets/);
  assert.equal(reads, 1); assert.equal(edits, 1); h.controller.dispose();
});

test('a failed retry preview preserves receipts, draft and only the same failed targets on retry', async () => {
  const reads: string[][] = [], writes: string[][] = [];
  const api: HomeSelectionOptions['api']['request'] = async (path, _method, raw) => {
    const body = raw as {targets: {kind: string; contentKind: string; id: string; libraryId: string}[]; operationId: string};
    if (path === routePath('post_metadata_bulk_preview')) {
      reads.push(body.targets.map(target => target.id));
      if (reads.length === 2) throw new Error('Preview connection lost');
      return preview(body.targets) as never;
    }
    writes.push(body.targets.map(target => target.id));
    const first = writes.length === 1;
    return {operationId: body.operationId, updated: 1, failed: first ? 1 : 0, results: body.targets.map(target => first && target.id === 'b' ? {kind: 'item', id: 'b', ok: false, code: 'metadata_conflict', message: 'Changed'} : {kind: 'item', id: target.id, ok: true, revision})} as never;
  };
  const h = harness(undefined, api); pickAll(h); await h.controller.openEditor();
  h.controller.setEditorDraft({fields: {year: {value: '2024'}}}); await h.controller.saveEditor();
  const draft = {fields: {year: {value: '2025'}}}; await h.controller.retryEditor(draft, ['b']);
  assert.equal(h.controller.getSnapshot().editor?.phase, 'error'); assert.equal(h.controller.getSnapshot().editor?.receipts.length, 1);
  assert.deepEqual(h.controller.getSnapshot().editor?.draft, draft);
  await h.controller.reloadEditor();
  assert.deepEqual(reads, [['a', 'b'], ['b'], ['b']]); assert.deepEqual(writes, [['a', 'b'], ['b']]);
  assert.equal(h.controller.getSnapshot().editor?.receipts.length, 2); h.controller.dispose();
});

test('toolbar descendant grants never become an unsupported album title My List toggle', async () => {
  const bodies: unknown[] = [];
  const album = {...entry('album', 'album'), homeActions: offers({myList: false})};
  const api: HomeSelectionOptions['api']['request'] = async (path, _method, raw) => {
    assert.equal(path, routePath('create_job')); bodies.push(raw);
    return {jobId: 'real-job', command: 'personal-state', state: 'complete', total: 2, totalKnown: true, done: 2, failed: 0} as never;
  };
  const h = harness([album], api); pickAll(h);
  h.controller.openMenu('window', h.controller.key({kind: 'album', id: 'album', libraryId: 'library'}));
  await assert.rejects(h.controller.executeMenu('watchlist'), /menu-action-not-offered/);
  await h.controller.execute('my-list');
  assert.equal(bodies.length, 1); assert.deepEqual((bodies[0] as {selector: unknown}).selector, {container: {kind: 'album', id: 'album'}});
  assert.deepEqual((bodies[0] as {args: unknown}).args, {watchlist: true}); h.controller.dispose();
});

test('confirmed partial personal retry submits only real failed leaves and retains actual job ids', async () => {
  const bodies: {selector: unknown}[] = [];
  const api: HomeSelectionOptions['api']['request'] = async (path, _method, raw) => {
    if (path.endsWith('/first-job/failures')) return {items: [{itemId: 'b', code: 'personal_needs_resolution'}]} as never;
    assert.equal(path, routePath('create_job')); bodies.push(raw as {selector: unknown});
    const first = bodies.length === 1;
    return {jobId: first ? 'first-job' : 'retry-job', command: 'personal-state', state: first ? 'partial' : 'complete', total: first ? 2 : 1, totalKnown: true, done: 1, failed: first ? 1 : 0} as never;
  };
  const h = harness(undefined, api); pickAll(h); await h.controller.execute('my-list');
  assert.equal(h.controller.getSnapshot().action.phase, 'partial'); await h.controller.retryAction();
  assert.deepEqual(bodies[1].selector, {items: {ids: ['b']}}); assert.equal(h.controller.getSnapshot().action.phase, 'complete');
  assert.deepEqual(h.controller.getSnapshot().action.jobIds, ['first-job', 'retry-job']); h.controller.dispose();
});

function ratingDetail(itemId = 'a', rating: number | null = 8) {
  return {scope: {serverId: 'server', libraryId: 'library', itemId, viewerFence: fence}, revision: {catalog: 1, viewer: 1}, item: {id: itemId, libraryId: 'library', kind: 'movie', title: `Title ${itemId}`, available: true}, personal: {watchlisted: false, favorite: false, watched: false, rating, revision: 1}, actions: [{id: 'rating', labelKey: 'action.rating', enabled: true, min: 0, max: 10, step: 1}]};
}
function openRating(h: ReturnType<typeof harness>, id = 'a', supplied?: number | null) {
  h.controller.openMenu('window', h.controller.key({kind: 'movie', id, libraryId: 'library'}));
  return h.controller.executeMenu('rate', supplied);
}

test('rating read error retries the retained GET and opens only the genuine value without writing', async () => {
  for (const value of [8, null]) {
    const calls: {path: string; method: string | undefined; body: unknown}[] = [];
    const h = harness(undefined, async (path, method, body) => {
      calls.push({path, method, body});
      if (calls.length === 1) throw new TypeError('Connection interrupted');
      return ratingDetail('a', value) as never;
    });
    pickAll(h); await openRating(h);
    assert.equal(h.controller.getSnapshot().rating?.phase, 'error');
    assert.equal(h.controller.getSnapshot().rating?.value, undefined);
    // A different current pick does not redirect recovery of this retained More target.
    h.controller.toggle('window', h.controller.key({kind: 'movie', id: 'a', libraryId: 'library'}));
    await h.controller.retryRating();
    assert.deepEqual(calls, [0, 1].map(() => ({path: routePath('get_items_id_detail', {id: 'a'}) + '?related=all', method: 'GET', body: undefined})));
    assert.deepEqual(h.controller.getSnapshot().rating, {phase: 'ready', currentValue: value, value, error: null});
    h.controller.dispose();
  }
});

test('rating read retry preserves denial, unavailable actions and stale detail scope/revisions', async () => {
  const valid = ratingDetail();
  const denied = Object.assign(new Error('No longer allowed'), {code: 'forbidden', status: 403});
  const replies = [denied, {...valid, actions: []}, {...valid, actions: [{...valid.actions[0], enabled: false}]}, {...valid, scope: {...valid.scope, viewerFence: 'new-fence'}}, {...valid, revision: {catalog: 2, viewer: 1}}, {...valid, revision: {catalog: 1, viewer: 2}}, ratingDetail('b')];
  for (const reply of replies) {
    const methods: (string | undefined)[] = [];
    const h = harness(undefined, async (_path, method) => {
      methods.push(method);
      if (methods.length === 1) throw new TypeError('Read interrupted');
      if (reply instanceof Error) throw reply;
      return reply as never;
    });
    pickAll(h); await openRating(h); await h.controller.retryRating();
    assert.equal(h.controller.getSnapshot().rating?.phase, 'error');
    assert.equal(h.controller.getSnapshot().rating?.currentValue, undefined);
    assert.deepEqual(methods, ['GET', 'GET']); h.controller.dispose();
  }
});

test('rating read retry requires the actual retained Home rate offer even when detail still offers it', async () => {
  let reads = 0;
  const a = {...entry('a')};
  const h = harness([a], async () => {if (++reads === 1) throw new TypeError('Read interrupted'); return ratingDetail() as never;});
  pickAll(h); await openRating(h);
  a.homeActions = offers({rating: false});
  await h.controller.retryRating();
  assert.equal(h.controller.getSnapshot().rating?.phase, 'error');
  assert.equal(h.controller.getSnapshot().rating?.error?.code, 'rating-not-offered'); h.controller.dispose();
});

test('late rating read recovery cannot publish after close, context replacement or a newer rating target', async () => {
  for (const stop of ['close', 'context', 'new-target'] as const) {
    let reads = 0, release!: (value: ReturnType<typeof ratingDetail>) => void, signal: AbortSignal | undefined;
    const late = new Promise<ReturnType<typeof ratingDetail>>(resolve => {release = resolve;});
    const h = harness(undefined, async (_path, _method, _body, abort) => {
      if (++reads === 1) throw new TypeError('Read interrupted');
      if (reads === 2) {signal = abort; return await late as never;}
      return ratingDetail('b', 3) as never;
    });
    pickAll(h); await openRating(h);
    const retry = h.controller.retryRating();
    assert.equal(h.controller.getSnapshot().rating?.phase, 'loading');
    if (stop === 'close') h.controller.closeRating();
    else if (stop === 'context') h.replace({...h.document, viewerFence: 'new-fence'});
    else await openRating(h, 'b');
    const expected = h.controller.getSnapshot().rating;
    assert.equal(signal?.aborted, true);
    release(ratingDetail('a', 9)); await retry;
    assert.deepEqual(h.controller.getSnapshot().rating, expected);
    if (stop === 'new-target') assert.equal(expected?.value, 3);
    else assert.equal(expected, null);
    h.controller.dispose();
  }
});

test('uncertain rating retry keeps the exact original mutation operation and body', async () => {
  const writes: unknown[] = [], methods: (string | undefined)[] = [];
  const h = harness(undefined, async (_path, method, input) => {
    methods.push(method);
    if (method === 'GET') return ratingDetail() as never;
    writes.push(input);
    if (writes.length === 1) throw new TypeError('Response lost');
    const body = input as {operationId: string; rating: number};
    return {operationId: body.operationId, serverId: 'server', libraryId: 'library', itemId: 'a', viewerFence: fence, personal: {...ratingDetail().personal, rating: body.rating, revision: 2}} as never;
  });
  pickAll(h); await openRating(h); h.controller.setRatingValue(4); await h.controller.submitRating();
  assert.equal(h.controller.getSnapshot().rating?.phase, 'uncertain'); await h.controller.retryRating();
  assert.equal(writes.length, 2); assert.deepEqual(writes[1], writes[0]);
  assert.equal(h.controller.getSnapshot().rating?.phase, 'result'); assert.equal(h.controller.getSnapshot().rating?.value, 4);
  assert.equal(methods.filter(method => method === 'PUT').length, 2); h.controller.dispose();
});

test('genuine model clones and reordered context/capability properties retain the same semantic offers', async () => {
  let reads = 0;
  const h = harness(undefined, async () => {reads++; return {serverId: 'server', viewerFence: fence, kind: 'playlist', query: '', revision: {catalog: 1, viewer: 1}, rows: [], nextCursor: ''} as never;});
  pickAll(h); const target = h.controller.getSnapshot().selection.target!;
  assert.equal(target.mode, 'explicit'); if (target.mode !== 'explicit') throw new Error('Explicit target required');
  const reorder = <T extends object>(value: T): T => Object.fromEntries(Object.entries(value).reverse()) as T;
  const items = target.items.map(pick => ({...pick, item: {...reorder(pick.item), capabilities: reorder(pick.item.capabilities)}}));
  await h.controller.openPicker('playlist', {...target, context: {revision: reorder(target.context.revision), scope: reorder(target.context.scope)}, items, displayItems: items});
  assert.equal(reads, 1); assert.equal(h.controller.getSnapshot().picker?.phase, 'ready'); h.controller.dispose();
});

test('changed genuine descendant offers in either direction still deny a mismatched retained target', async () => {
  for (const newlyGranted of [false, true]) {
    const a = {...entry('a')}; a.homeActions = offers({selection: {...offers().selection!, favorite: !newlyGranted}});
    const h = harness([a]); pickAll(h);
    const target = h.controller.getSnapshot().selection.target!;
    if (target.mode !== 'explicit') throw new Error('Explicit target required');
    a.homeActions = offers({selection: {...offers().selection!, favorite: newlyGranted}});
    await assert.rejects(h.controller.openPicker('playlist', target), /home-offers-changed/);
    const item = homeEntrySelection(a, {platform: 'web', playbackAvailable: true, queueAvailable: true})!;
    const items = [{key: h.controller.key(item), item}];
    await assert.rejects(h.controller.openPicker('playlist', {...target, items, displayItems: items}), /home-offers-changed/);
    h.controller.dispose();
  }
});


function menuKey(h: ReturnType<typeof harness>, item = h.entries.at(-1)!) {
  return h.controller.key({kind: item.kind, id: item.id, libraryId: item.libraryId!});
}
function openLast(h: ReturnType<typeof harness>) {
  const registered = h.controller.registerWindow({id: 'window', row: h.document.rows[0], sectionOrder: 0});
  assert.ok(registered.ok); h.controller.openMenu('window', menuKey(h)); return registered;
}
function nextContext(h: ReturnType<typeof harness>) {return homeSelectionContext(h.options.context.scope, h.document);}
function rerank(h: ReturnType<typeof harness>, entries: readonly HomeEntry[]) {
  h.replace({...h.document, revision: {catalog: h.document.revision.catalog + 1, viewer: h.document.revision.viewer + 1}, rows: [{...h.document.rows[0], entries, revision: {catalog: 2, viewer: 2}}]});
}

test('reconcile: no-key rerank retains third-card menu/opener atomically and dispatches latest genuine resume child', async () => {
  const h = harness([entry('a'), entry('b'), {...entry('c', 'show'), progressSeconds: 50}]), old = openLast(h);
  const key = menuKey(h), changes: (string | null)[] = [];
  h.controller.subscribe(() => changes.push(h.controller.getSnapshot().menu?.key ?? null));
  const latest = {...h.entries[2], title: 'Updated C', homeActions: offers({state: {...offers().state, watchlisted: true}})};
  const child = {...entry('new-child', 'episode'), playback: {itemId: 'latest-playable-child', startSeconds: 75}};
  h.replace({...h.document, revision: {catalog: 2, viewer: 2}, rows: [{...h.document.rows[0], entries: [latest, h.entries[1], h.entries[0]], resumes: [{entryId: latest.id, target: child}], revision: {catalog: 2, viewer: 2}}]});
  const menu = h.controller.getSnapshot().menu;
  assert.ok(menu); assert.equal(menu.key, key); assert.equal(menu.windowId, 'window'); assert.equal(menu.member.entry, latest);
  assert.equal(menu.groups.flatMap(group => group.actions).find(action => action.id === 'watchlist')?.selected, true);
  assert.equal(menu.member.playbackTarget, child); assert.deepEqual(h.controller.getSnapshot().context, nextContext(h));
  assert.ok(changes.length > 0); assert.ok(changes.every(value => value === key), 'subscribers never observe a transient closed menu');
  // Existing web/native onDocument setContext follows the controller subscription.
  h.controller.setContext(nextContext(h), h.options.environment); old.unregister();
  assert.equal(h.controller.getSnapshot().menu?.key, key);
  await h.controller.executeMenu('resume');
  assert.equal(h.played.length, 1); assert.equal(h.played[0].context.revision.catalogRevision, '2');
  const item = h.played[0].items[0]; assert.ok('entry' in item); assert.equal(item.entry, latest); assert.equal(item.target, child.playback);
  assert.deepEqual(h.errors, []); h.controller.dispose();
});

test('reconcile: same-document setContext is idempotent for existing callers', () => {
  const h = harness(); openLast(h); const menu = h.controller.getSnapshot().menu;
  h.controller.setContext(nextContext(h)); assert.equal(h.controller.getSnapshot().menu, menu); h.controller.dispose();
});

test('reconcile: removal, moved library, unavailable target and revoked title/selection grants close instead of retargeting', async () => {
  for (const change of ['removed', 'library', 'kind', 'unavailable', 'target', 'play', 'rating', 'selection', 'row', 'ambiguous'] as const) {
    const h = harness([entry('a'), entry('b'), entry('c')]); openLast(h);
    const c = h.entries[2];
    const changed = change === 'library' ? {...c, libraryId: 'other-library'} : change === 'kind' ? {...c, kind: 'episode' as const}
      : change === 'unavailable' ? {...c, available: false} : change === 'target' ? {...c, playback: undefined}
      : change === 'play' ? {...c, homeActions: offers({play: false})} : change === 'rating' ? {...c, homeActions: offers({rating: false})}
      : change === 'selection' ? {...c, homeActions: offers({selection: {...offers().selection, queue: false}})} : c;
    rerank(h, change === 'removed' ? [h.entries[1], h.entries[0]] : change === 'ambiguous' ? [c, {...c}, h.entries[0]] : [changed, h.entries[1], h.entries[0]]);
    if (change === 'row') h.replace({...h.document, rows: [{...h.document.rows[0], id: 'replacement-row'}]});
    assert.equal(h.controller.getSnapshot().menu, null, change);
    await assert.rejects(h.controller.executeMenu('play'), /menu-action-not-offered/); assert.equal(h.played.length, 0); h.controller.dispose();
  }
});

test('reconcile: scope, fence and parent authority changes invalidate', () => {
  for (const property of ['serverId', 'authority', 'accountId', 'profileId', 'viewId', 'privacyFence'] as const) {
    const h = harness(); openLast(h);
    const context = {...nextContext(h), scope: {...nextContext(h).scope, [property]: property === 'authority' ? 'hosted' as const : 'different'}};
    if (['serverId', 'authority', 'accountId', 'profileId'].includes(property)) assert.throws(() => h.controller.setContext(context), /recreate-home-controller-for-source/);
    else h.controller.setContext(context);
    assert.equal(h.controller.getSnapshot().menu, null, property); h.controller.dispose();
  }
  const h = harness(); openLast(h); h.retire(); rerank(h, [...h.entries].reverse());
  assert.equal(h.controller.getSnapshot().menu, null); assert.throws(() => h.controller.openMenu('window', menuKey(h)), /stale-home-context/); h.controller.dispose();
});

test('reconcile: same-revision genuine changes rebind the menu and refuse a revoked action', async () => {
  const h = harness(); openLast(h); const latest = {...h.entries[1], title: 'Latest same-revision title'};
  h.replace({...h.document, rows: [{...h.document.rows[0], entries: [latest, h.entries[0]]}]});
  assert.equal(h.controller.getSnapshot().menu?.member.entry, latest);
  h.replace({...h.document, rows: [{...h.document.rows[0], entries: [{...latest, homeActions: offers({play: false})}, h.entries[0]]}]});
  assert.equal(h.controller.getSnapshot().menu, null); await assert.rejects(h.controller.executeMenu('play'), /menu-action-not-offered/);
  assert.equal(h.played.length, 0); h.controller.dispose();
});

test('reconcile: pending response A cannot update or retarget menu B after a document refresh', async () => {
  let release!: (value: never) => void; let calls = 0;
  const h = harness([entry('a', 'show'), entry('b', 'show')], async () => {calls++; return new Promise(resolve => {release = resolve;});});
  h.controller.registerWindow({id: 'window', row: h.document.rows[0], sectionOrder: 0});
  h.controller.openMenu('window', menuKey(h, h.entries[0])); const pending = h.controller.executeMenu('watchlist');
  h.controller.openMenu('window', menuKey(h)); rerank(h, [...h.entries].reverse());
  assert.equal(h.controller.getSnapshot().menu?.key, menuKey(h));
  release({revision: 1, watched: false, watermark: '', watchlisted: false, favorite: false} as never); await pending;
  assert.equal(h.controller.getSnapshot().menu?.key, menuKey(h)); assert.notEqual(h.controller.getSnapshot().action.phase, 'complete'); assert.equal(calls, 1, 'old read never issues a write'); assert.deepEqual(h.errors, []); h.controller.dispose();
});

test('reconcile: dispatch rejects an unregistered current menu window', async () => {
  const h = harness(), registered = openLast(h); registered.unregister();
  await assert.rejects(h.controller.executeMenu('play'), /unregistered-home-entry|menu-action-not-offered/); assert.equal(h.played.length, 0); h.controller.dispose();
});

function liveChannel(): HomeChannel {
  return {source: {id: 'source', name: 'Source', generation: 'g', publishedAt: '2026-10-22T00:00:00Z', availableStart: '2026-10-22T00:00:00Z', availableEnd: '2026-10-23T00:00:00Z', provenance: 'live-source', refreshState: 'ready'}, channel: {id: 'channel', sourceId: 'source', provenance: 'live-source', name: 'Channel', number: '1', group: '', generation: 'g', programmes: [], tuneAvailable: true, tuneUnavailableReason: '', recordAvailable: false, recordUnavailableReason: '', favorite: false, hidden: false, preferenceRevision: 1, mark: {text: 'C', seed: 'channel'}}, actions: {play: true, favorite: true}};
}
test('reconcile: Live expiry blocks dispatch and registration; a genuine refreshed window admits current channel only', async () => {
  let writes = 0; const h = harness(undefined, async () => {writes++; throw new Error('Expired channel must not write');}), clock = Date.now, channel = liveChannel(); let now = Date.parse('2026-10-22T12:00:00Z'); Date.now = () => now;
  try {
    const live = {revision: 'live-1', observedAt: new Date(now).toISOString(), expiresAt: new Date(now + 1000).toISOString(), sort: 'favorites' as const, group: '', groups: [], channels: [channel]};
    h.replace({...h.document, rows: [{...h.document.rows[0], entries: [], live}]});
    let tuned = 0; h.options.playback.playChannel = async () => {tuned++;};
    h.controller.registerWindow({id: 'window', row: h.document.rows[0], sectionOrder: 0});
    const key = h.controller.key({kind: 'channel', sourceId: 'source', channelId: 'channel'}); h.controller.openMenu('window', key);
    now += 1001; await assert.rejects(h.controller.executeMenu('play'), /stale-home-live-window|home-offers-changed|menu-action-not-offered/);
    assert.equal(h.controller.getSnapshot().menu, null); assert.equal(tuned, 0);
    assert.deepEqual(h.controller.registerWindow({id: 'expired', row: h.document.rows[0], sectionOrder: 0}), {ok: false, reason: 'stale-home-live-window'}, 'an expired Live window is dropped, never thrown');
    const fresh = {...channel, channel: {...channel.channel, generation: 'g2'}};
    h.replace({...h.document, rows: [{...h.document.rows[0], live: {...live, revision: 'live-2', expiresAt: new Date(now + 1000).toISOString(), channels: [fresh]}}]});
    h.controller.setContext(nextContext(h)); h.controller.registerWindow({id: 'window', row: h.document.rows[0], sectionOrder: 0}); h.controller.openMenu('window', key); await h.controller.executeMenu('play');
    assert.equal(tuned, 1);
    h.controller.openMenu('window', key); const pending = h.controller.executeMenu('favorite'); now += 1001; await pending;
    assert.equal(writes, 0, 'expiry during async command preparation cannot dispatch a stale Live action'); assert.equal(h.controller.getSnapshot().action.phase, 'cancelled'); assert.deepEqual(h.errors, []); h.controller.dispose();
  } finally {Date.now = clock; h.controller.dispose();}
});

test('reconcile: spotlight menu follows semantic identity instead of its changing index', async () => {
  const h = harness(); const spotlights = h.entries.map(value => ({entry: value, reason: {kind: 'featured', text: {code: 'featured', fallback: 'Featured'}}}));
  h.replace({...h.document, spotlight: spotlights}); h.controller.registerSpotlight({id: 'hero', spotlight: spotlights[1]}); h.controller.openMenu('hero', menuKey(h));
  const latest = {...spotlights[1], entry: {...spotlights[1].entry, title: 'Latest spotlight'}};
  h.replace({...h.document, revision: {catalog: 2, viewer: 2}, spotlight: [latest, spotlights[0]]});
  assert.equal(h.controller.getSnapshot().menu?.member.entry, latest.entry); await h.controller.executeMenu('play');
  assert.equal(h.played.length, 1); h.controller.dispose();
});


test('reconcile: existing caller setContext before Home subscription keeps the menu bound to the current source', async () => {
  const h = harness(); openLast(h); const latest = {...h.entries[1], title: 'New document'};
  h.replace({...h.document, revision: {catalog: 2, viewer: 1}, rows: [{...h.document.rows[0], entries: [latest, h.entries[0]]}]}, () => h.controller.setContext(nextContext(h), h.options.environment));
  assert.equal(h.controller.getSnapshot().menu?.member.entry, latest); await h.controller.executeMenu('play'); assert.equal(h.played.length, 1); h.controller.dispose();
});

test('reconcile: same-revision late playback receipt is aborted and cannot complete over a newer menu', async () => {
  const h = harness(); let release!: (value: Awaited<ReturnType<HomeSelectionOptions['playback']['play']>>) => void;
  h.options.playback.play = input => {h.played.push(input); return new Promise(resolve => {release = resolve;});};
  openLast(h); const running = h.controller.executeMenu('play');
  h.controller.openMenu('window', menuKey(h, h.entries[0]));
  h.replace({...h.document, rows: [{...h.document.rows[0], entries: [...h.entries].reverse()}]});
  assert.equal(h.played[0].signal.aborted, true); assert.equal(h.controller.getSnapshot().menu?.key, menuKey(h, h.entries[0]));
  release({queue: {id: 'late-old-queue', revision: '1', total: 1, repeat: 'off', segments: []}}); await running;
  assert.equal(h.controller.getSnapshot().menu?.key, menuKey(h, h.entries[0])); assert.notEqual(h.controller.getSnapshot().action.phase, 'complete'); h.controller.dispose();
});


test('reconcile: closing a retained menu releases its one-item registration without removing a newer shell window', () => {
  const h = harness(); openLast(h); rerank(h, [...h.entries].reverse());
  assert.equal(h.controller.getSnapshot().selection.registeredItemCount, 1); h.controller.closeMenu();
  assert.equal(h.controller.getSnapshot().selection.registeredItemCount, 0);
  h.controller.registerWindow({id: 'window', row: h.document.rows[0], sectionOrder: 0}); h.controller.openMenu('window', menuKey(h)); rerank(h, h.entries); h.controller.clear();
  assert.equal(h.controller.getSnapshot().selection.registeredItemCount, 0, 'selection clear also releases the controller-owned menu window');
  h.controller.registerWindow({id: 'window', row: h.document.rows[0], sectionOrder: 0}); h.controller.openMenu('window', menuKey(h)); rerank(h, h.entries);
  h.controller.registerWindow({id: 'window', row: h.document.rows[0], sectionOrder: 0}); h.controller.closeMenu();
  assert.equal(h.controller.getSnapshot().selection.registeredItemCount, 2); h.controller.dispose();
});


test('reconcile: reusing a window id for a different genuine row never moves the menu origin', async () => {
  for (const refresh of [false, true]) {
    const h = harness(); const other = {...h.document.rows[0], id: 'different-row'};
    h.replace({...h.document, rows: [...h.document.rows, other]}); openLast(h);
    h.controller.registerWindow({id: 'window', row: other, sectionOrder: 1});
    if (refresh) h.replace({...h.document, revision: {catalog: 2, viewer: 2}});
    await assert.rejects(h.controller.executeMenu('play'), /unregistered-home-entry|menu-action-not-offered/);
    assert.equal(h.controller.getSnapshot().menu, null); assert.equal(h.played.length, 0); h.controller.dispose();
  }
});


test('reconcile: an explicit new opener releases the previous retained menu window', () => {
  const h = harness(); openLast(h); rerank(h, [...h.entries].reverse());
  h.controller.registerWindow({id: 'other', row: h.document.rows[0], sectionOrder: 0, entries: [h.entries[0]]});
  h.controller.openMenu('other', menuKey(h, h.entries[0]));
  assert.equal(h.controller.getSnapshot().menu?.windowId, 'other'); assert.equal(h.controller.getSnapshot().selection.registeredItemCount, 1); h.controller.dispose();
});


async function serviceHarness() {
  const a = {...row([entry('a0'), entry('a1'), entry('a2')]), start: 10}, b = {...row([entry('b0'), entry('b1')]), id: 'other', total: 3, nextCursor: 'page-b', hasMore: true};
  const document: HomeDocument = {serverId: 'server', viewerFence: fence, rows: [a, b], spotlight: [{entry: a.entries[0], reason: {kind: 'featured', text: {code: 'featured', fallback: 'Featured'}}}], layout: {revision: 1, rowOrder: [a.id, b.id], hiddenRowIds: []}, revision: {catalog: 1, viewer: 1}, generatedAt: '2026-10-22T00:00:00Z'};
  const home = new HomeService({request: async <T>(path: string) => {
    if (path === routePath('get_home')) return document as T;
    assert.match(path, /other.*cursor=page-b/);
    return {...b, entries: [entry('b2')], start: 2, limit: 1, nextCursor: '', hasMore: false} as T;
  }});
  await home.load(); assert.equal(home.getSnapshot().phase, 'ready');
  return {...harness([], undefined, home), home};
}

for (const withMenu of [false, true]) test(`reconcile F1: real HomeService B paging preserves mounted A without reattachment, menu=${withMenu}`, async t => {
  const h = await serviceHarness(); t.after(() => {h.controller.dispose(); h.home.dispose();});
  const before = h.home.getSnapshot().document!, a = before.rows[0], key = menuKey(h, a.entries[1]);
  const registered = h.controller.registerWindow({id: 'mounted-a', row: a, entries: [a.entries[1]], start: 11, sectionOrder: 0}); assert.ok(registered.ok);
  h.controller.toggle('mounted-a', key);
  if (withMenu) h.controller.openMenu('mounted-a', key);
  await h.home.more('other');
  const after = h.home.getSnapshot().document!;
  assert.notEqual(after, before); assert.equal(after.rows[0], a); assert.notEqual(after.rows[1], before.rows[1]); assert.deepEqual(after.revision, before.revision);
  assert.equal(h.controller.getSnapshot().selection.summary.count, 0, 'a new document still clears bulk picks');
  assert.equal(h.controller.getSnapshot().selection.registeredItemCount, 1);
  if (withMenu) assert.equal(h.controller.getSnapshot().menu?.key, key);
  else h.controller.openMenu('mounted-a', key);
  h.controller.closeMenu();
  assert.equal(h.controller.getSnapshot().selection.registeredItemCount, 1, 'closing a menu does not own the mounted A window');
  assert.equal(h.controller.toggle('mounted-a', key).ok, true);
  registered.unregister();
  assert.equal(h.controller.getSnapshot().selection.registeredItemCount, 0, 'the original shell cleanup still unregisters A');
  assert.throws(() => h.controller.openMenu('mounted-a', key), /unregistered-home-entry/);
});

test('reconcile F1: real optimistic B removal preserves mounted spotlight and original cleanup', async t => {
  const h = await serviceHarness(); t.after(() => {h.controller.dispose(); h.home.dispose();});
  const before = h.home.getSnapshot().document!, spotlight = before.spotlight[0], key = menuKey(h, spotlight.entry);
  const registered = h.controller.registerSpotlight({id: 'mounted-hero', spotlight}); assert.ok(registered.ok);
  h.controller.openMenu('mounted-hero', key);
  h.home.withoutEntry('b0');
  const after = h.home.getSnapshot().document!;
  assert.notEqual(after, before); assert.equal(after.spotlight, before.spotlight); assert.equal(after.rows[0], before.rows[0]); assert.deepEqual(after.revision, before.revision);
  assert.equal(h.controller.getSnapshot().menu?.member.entry, spotlight.entry);
  h.controller.closeMenu(); assert.equal(h.controller.toggle('mounted-hero', key).ok, true);
  registered.unregister(); assert.equal(h.controller.getSnapshot().selection.registeredItemCount, 0);
});

test('reconcile F1: unchanged A still cancels old document work when only B changes', async t => {
  const h = await serviceHarness(); t.after(() => {h.controller.dispose(); h.home.dispose();});
  let release!: (value: Awaited<ReturnType<HomeSelectionOptions['playback']['play']>>) => void;
  h.options.playback.play = input => {h.played.push(input); return new Promise(resolve => {release = resolve;});};
  const a = h.home.getSnapshot().document!.rows[0], key = menuKey(h, a.entries[0]);
  h.controller.registerWindow({id: 'mounted-a', row: a, sectionOrder: 0}); h.controller.openMenu('mounted-a', key);
  const pending = h.controller.executeMenu('play');
  await h.home.more('other');
  assert.equal(h.played[0].signal.aborted, true); h.controller.openMenu('mounted-a', key);
  release({queue: {id: 'late', revision: '1', total: 1, repeat: 'off', segments: []}}); await pending;
  assert.equal(h.controller.getSnapshot().menu?.key, key); assert.notEqual(h.controller.getSnapshot().action.phase, 'complete');
});

test('reconcile F1: changed source coordinates or environment cannot preserve a mounted registration', () => {
  for (const change of ['row-order', 'spotlight-order', 'environment'] as const) {
    const h = harness(); const other = {...row([entry('other')]), id: 'other'};
    const spotlight = {entry: h.entries[0], reason: {kind: 'featured', text: {code: 'featured', fallback: 'Featured'}}};
    h.replace({...h.document, rows: [h.document.rows[0], other], spotlight: [spotlight, {...spotlight, entry: h.entries[1]}]});
    if (change === 'spotlight-order') h.controller.registerSpotlight({id: 'mounted', spotlight});
    else h.controller.registerWindow({id: 'mounted', row: h.document.rows[0], sectionOrder: 0});
    if (change === 'environment') h.controller.setContext(nextContext(h), {...h.options.environment, isOwner: false});
    else h.replace({...h.document, ...(change === 'row-order' ? {rows: [...h.document.rows].reverse()} : {spotlight: [...h.document.spotlight].reverse()})});
    assert.equal(h.controller.getSnapshot().selection.registeredItemCount, 0, change); h.controller.dispose();
  }
});


test('reconcile F1: retained controller-owned menus still release their window after an unrelated refresh or ambiguity', () => {
  for (const ambiguous of [false, true]) {
    const h = harness(); const spotlight = {entry: h.entries[1], reason: {kind: 'featured', text: {code: 'featured', fallback: 'Featured'}}};
    h.replace({...h.document, spotlight: [spotlight]}); h.controller.registerSpotlight({id: 'hero', spotlight}); h.controller.openMenu('hero', menuKey(h));
    h.replace({...h.document, revision: {catalog: 2, viewer: 2}});
    assert.equal(h.controller.getSnapshot().selection.registeredItemCount, 1);
    h.replace({...h.document, ...(ambiguous ? {spotlight: [spotlight, {...spotlight}]} : {rows: [{...h.document.rows[0], title: 'Changed B'}]})});
    if (ambiguous) assert.equal(h.controller.getSnapshot().menu, null);
    else {assert.equal(h.controller.getSnapshot().menu?.key, menuKey(h)); h.controller.closeMenu();}
    assert.equal(h.controller.getSnapshot().selection.registeredItemCount, 0); h.controller.dispose();
  }
});

test('reconcile F1: original preserved cleanup cannot unregister a newer shell replacement', () => {
  const h = harness(), a = h.document.rows[0], other = {...row([entry('other')]), id: 'other'};
  h.replace({...h.document, rows: [a, other]});
  const original = h.controller.registerWindow({id: 'mounted-a', row: a, sectionOrder: 0}); assert.ok(original.ok);
  h.replace({...h.document, rows: [a, {...other, title: 'Changed B'}]});
  const replacement = h.controller.registerWindow({id: 'mounted-a', row: a, sectionOrder: 0}); assert.ok(replacement.ok);
  original.unregister(); assert.equal(h.controller.getSnapshot().selection.registeredItemCount, 2);
  assert.equal(h.controller.toggle('mounted-a', menuKey(h)).ok, true);
  replacement.unregister(); assert.equal(h.controller.getSnapshot().selection.registeredItemCount, 0); h.controller.dispose();
});

test('RN-08: Resume retains positions 41 and 72 with resume intent; Start over carries its own intent', async () => {
  for (const position of [41, 72]) {
    const a = {...entry('movie'), progressSeconds: position, playback: {itemId: 'movie', startSeconds: position}};
    const h = harness([a]);
    try {
      h.controller.registerWindow({id: 'window', row: h.document.rows[0], sectionOrder: 0});
      const key = h.controller.key({kind: a.kind, id: a.id, libraryId: a.libraryId});
      h.controller.openMenu('window', key); await h.controller.executeMenu('resume');
      const resumed = h.played[0].items[0]; assert.ok('target' in resumed);
      assert.equal(resumed.startIntent, 'resume'); assert.equal(resumed.target.startSeconds, position);
      h.controller.openMenu('window', key); await h.controller.executeMenu('playFromStart');
      const restarted = h.played[1].items[0]; assert.ok('target' in restarted);
      assert.equal(restarted.startIntent, 'start-over'); assert.equal(restarted.target.startSeconds, 0);
      assert.equal(a.playback.startSeconds, position);
      h.controller.toggle('window', key); await h.controller.execute('play-later');
      const queued = h.queued[0].items[0]; assert.ok('target' in queued);
      assert.equal(queued.startIntent, 'resume'); assert.equal(queued.target.startSeconds, position);
    } finally {h.controller.dispose();}
  }
});

test('RN-08: deliberately selected chapter retains its explicit position', async () => {
  const chapter = {...entry('chapter', 'chapter'), playback: {itemId: 'book-file', startSeconds: 72}};
  const h = harness([chapter]);
  try {
    pickAll(h); await h.controller.execute('play');
    const chosen = h.played[0].items[0]; assert.ok('target' in chosen);
    assert.equal(chosen.startIntent, 'explicit'); assert.equal(chosen.target.startSeconds, 72);
  } finally {h.controller.dispose();}
});

test('removing a Home window never throws: after Live expiry, twice, or after the controller is gone (E-tv-30)', () => {
  const h = harness(), clock = Date.now, channel = liveChannel(); let now = Date.parse('2026-10-22T12:00:00Z'); Date.now = () => now;
  try {
    const live = {revision: 'live-1', observedAt: new Date(now).toISOString(), expiresAt: new Date(now + 1000).toISOString(), sort: 'favorites' as const, group: '', groups: [], channels: [channel]};
    h.replace({...h.document, rows: [{...h.document.rows[0], entries: [], live}]});
    const registered = h.controller.registerWindow({id: 'live', row: h.document.rows[0], sectionOrder: 0});
    assert.equal(registered.ok, true);
    now += 5000;
    assert.deepEqual(h.controller.registerWindow({id: 'later', row: h.document.rows[0], sectionOrder: 0}), {ok: false, reason: 'stale-home-live-window'});
    if (!registered.ok) throw new Error('registered');
    assert.doesNotThrow(() => registered.unregister(), 'deregistering an expired window');
    assert.doesNotThrow(() => registered.unregister(), 'deregistering it again');
    h.controller.dispose();
    assert.doesNotThrow(() => registered.unregister(), 'deregistering after dispose');
  } finally {Date.now = clock;}
});
