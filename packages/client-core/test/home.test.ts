import test from 'node:test';
import assert from 'node:assert/strict';
import {fetchHome, fetchHomeRow, resetHomeLayout, saveHomeLayout, validateHomeDocument, validateHomeLayout, validateHomeRow, type HomeApi} from '../src/home.ts';
import {fetchItemRecommendations, fetchSuggestions, moreLikeThisRow, validateItemRecommendations, validateSuggestions} from '../src/recommendations.ts';
import {resetSkippedParts, skippedParts} from '../src/tolerant.ts';

function entry(id = 'm1', kind = 'movie') {
  return {id, kind, title: `Title ${id}`, libraryId: 'movies', available: true, navigation: {view: 'item', entityId: id}, playback: {itemId: id}};
}
function row(changes: Record<string, unknown> = {}): any {
  return {
    id: 'recent_movies', title: 'Recently Added in Movies', kind: 'recent', artworkShape: 'poster', endpoint: '/v1/home/rows/recent_movies',
    libraryId: 'movies', viewerFence: 'fence', privacySensitivity: 'catalog', policyState: 'available', priority: 100, cacheTtlMs: 120000,
    required: false, hideable: true, reorderable: true, critical: false, cursorCapable: true,
    entries: [entry('m1'), entry('m2')], total: 5, start: 0, limit: 2, hasMore: true, nextCursor: 'opaque.signature',
    revision: {catalog: 3, viewer: 1}, ...changes,
  };
}
function document(changes: Record<string, unknown> = {}): any {
  return {
    serverId: 'server', viewerFence: 'fence', rows: [row()], spotlight: [], layout: {revision: 2, rowOrder: ['recent_movies'], hiddenRowIds: []},
    revision: {catalog: 3, viewer: 1}, generatedAt: '2026-09-16T12:00:00.000Z', ...changes,
  };
}
function api(handler: (path: string, method?: string, body?: unknown) => unknown): HomeApi {
  return {request: async <T>(path: string, method?: string, body?: unknown) => handler(path, method, body) as T};
}

test('a home document keeps the rows, policy and paging shape the server published', () => {
  const parsed = validateHomeDocument(document());
  assert.equal(parsed.rows.length, 1);
  assert.equal(parsed.rows[0].artworkShape, 'poster');
  assert.equal(parsed.rows[0].entries[1].id, 'm2');
  assert.equal(parsed.rows[0].cursorCapable, true);
  assert.equal(parsed.layout.revision, 2);
  assert.throws(() => (parsed.rows as unknown as unknown[]).push(row()));
});

test('an unknown card kind drops only that Home entry and retains the published row total', () => {
  const parsed = validateHomeDocument(document({rows: [row({entries: [entry('m1'), entry('future', 'interactive'), entry('m2')]})]}));
  assert.deepEqual(parsed.rows[0].entries.map(card => card.id), ['m1', 'm2']);
  assert.equal(parsed.rows[0].total, 5);
  assert.equal(parsed.rows[0].nextCursor, 'opaque.signature');
});

test('the identity, revisions and bounds of Home still refuse the whole document', () => {
  const cases: unknown[] = [
    'nope',
    document({serverId: ''}),
    document({viewerFence: 7}),
    document({rows: 'nope'}),
    document({rows: Array.from({length: 65}, (_, i) => row({id: `r${i}`}))}),
    document({revision: {catalog: -1, viewer: 0}}),
    document({layout: {revision: -1, rowOrder: [], hiddenRowIds: []}}),
    document({layout: {revision: 2, rowOrder: Array.from({length: 65}, (_, i) => `r${i}`), hiddenRowIds: []}}),
    document({generatedAt: 'x'.repeat(129)}),
  ];
  for (const payload of cases) assert.throws(() => validateHomeDocument(payload), {code:'invalid_home'});
  // One row read on its own is the whole document: its id, cursor, revision and entry bound stay exact.
  for (const broken of [row({id: ''}), row({nextCursor: 'bad\ncursor'}), row({revision: {catalog: -1, viewer: 0}}), row({entries: Array.from({length: 201}, (_, i) => entry(`m${i}`))}), row({entries: 'nope'}), row({title: 'x'.repeat(1025)})]) {
    assert.throws(() => validateHomeRow(broken), {code:'invalid_home'});
  }
});

test('one Home row that cannot be read is left out and the others are kept', () => {
  const good = row({id: 'favorites'});
  resetSkippedParts();
  for (const bad of ['nope', row({id: ''}), row({revision: {catalog: -1, viewer: 0}}), row({nextCursor: 7}), row({entries: Array.from({length: 201}, (_, i) => entry(`m${i}`))})]) {
    assert.deepEqual(validateHomeDocument(document({rows: [bad, good]})).rows.map(r => r.id), ['favorites']);
  }
  assert.deepEqual(skippedParts(), [{document: 'home', part: 'rows', count: 5}]);
  // A Home none of whose rows can be read is refused rather than shown as empty; an empty Home is not.
  assert.throws(() => validateHomeDocument(document({rows: ['nope', row({id: ''})]})), {code: 'invalid_home'});
  assert.equal(validateHomeDocument(document({rows: []})).rows.length, 0);
  // A repeated row keeps its first appearance.
  assert.deepEqual(validateHomeDocument(document({rows: [row({title: 'First'}), row({title: 'Second'})]})).rows.map(r => r.title), ['First']);
  // A row the viewer hid, or one the server sent empty that is not critical, is not shown.
  assert.deepEqual(validateHomeDocument(document({rows: [row(), good], layout: {revision: 2, rowOrder: [], hiddenRowIds: ['recent_movies']}})).rows.map(r => r.id), ['favorites']);
  assert.deepEqual(validateHomeDocument(document({rows: [row({entries: [], total: 0, critical: false, hasMore: false}), good]})).rows.map(r => r.id), ['favorites']);
  assert.deepEqual(validateHomeDocument(document({rows: [row({entries: [], total: 0, critical: true, hasMore: false, id: 'continue', required: true, hideable: false})]})).rows.map(r => r.id), ['continue']);
  // The layout keeps the ids it can read, each once.
  assert.deepEqual([...validateHomeLayout({revision: 2, rowOrder: ['a', 'a', 7, '', 'b'], hiddenRowIds: ['c', 'c']}).rowOrder], ['a', 'b']);
  // A hero that cannot be read is absent; Home is still shown.
  assert.equal(validateHomeDocument(document({hero: {rowId: 7}})).hero, undefined);
  assert.equal(validateHomeDocument(document({hero: {rowId: 'recent_movies', entryId: 'm2'}})).hero, undefined);
});

test('a Home row reads neutrally where it is only drawn, and ignores fields this app does not know', () => {
  const parsed = validateHomeRow(row({
    artworkShape: 'circle', required: true, hideable: true, critical: 'yes', priority: 'high', cacheTtlMs: undefined, limit: -1,
    entries: [entry('m1'), entry('m1'), {id: 'broken'}, entry('m2')], total: 1, titleText: {code: 1}, relation: 7, futureField: {anything: true},
  }));
  assert.equal(parsed.artworkShape, 'poster');
  assert.equal(parsed.required, true);
  assert.equal(parsed.hideable, false, 'a required row is never hideable');
  assert.equal(parsed.critical, false);
  assert.equal(parsed.priority, 0);
  assert.equal(parsed.limit, 0);
  assert.equal(parsed.cacheTtlMs, undefined);
  assert.deepEqual(parsed.entries.map(e => e.id), ['m1', 'm2']);
  assert.equal(parsed.total, 2, 'the total is at least the entries kept');
  assert.equal(parsed.titleText, undefined);
  assert.equal(parsed.relation, undefined);
  assert.equal('futureField' in parsed, false);
  assert.equal(validateHomeRow(row()).cacheTtlMs, 120000);
  // A server that stops sending the presentation fields still has its row shown.
  const bare = validateHomeRow({id: 'bare', viewerFence: 'fence', entries: [entry('m1')], revision: {catalog: 1, viewer: 1}});
  assert.deepEqual([bare.title, bare.kind, bare.artworkShape, bare.nextCursor, bare.total, bare.hasMore], ['', '', 'poster', '', 1, false]);
});

test('a Home row may name where its See all leads; a destination that cannot be read is left out alone', () => {
  const seeAll = {type: 'library', libraryId: 'movies', pivot: 'movies', query: {variant:'predicate',field: 'genre', operator: 'equals', value: 'Drama'}, sort: [{field: 'added', direction: 'desc'}]};
  assert.deepEqual(validateHomeRow(row({seeAll})).seeAll, seeAll);
  assert.deepEqual(validateHomeRow(row({seeAll: {type: 'library', libraryId: 'movies', pivot: 'movies', sort: [{field: 'title', direction: 'asc'}]}})).seeAll, {type: 'library', libraryId: 'movies', pivot: 'movies', sort: [{field: 'title', direction: 'asc'}]});
  assert.equal(validateHomeRow(row()).seeAll, undefined);
  for (const broken of [{pivot: '', sort: seeAll.sort}, {pivot: 'movies', sort: []}, {pivot: 'movies', sort: [{field: 'title', direction: 'sideways'}]}, {...seeAll, query: {variant:'predicate',field: 'genre', operator: 'resembles', value: 'Drama'}}, 'movies']) {
    const parsed = validateHomeRow(row({seeAll: broken}));
    assert.equal(parsed.seeAll, undefined);
    assert.equal(parsed.entries.length, 2);
  }
});

test('row paging asks for a cursor or an anchored range, never both', async () => {
  const requested: string[] = [];
  const client = api(path => {
    requested.push(path);
    return row({start: 2, entries: [entry('m3')], limit: 1});
  });
  await fetchHomeRow(client, 'recent_movies', {limit: 1, cursor: 'opaque.signature'});
  await fetchHomeRow(client, 'recent_movies', {limit: 1, start: 2, revision: '3:1', anchorId: 'm3'});
  assert.equal(requested[0], '/v1/home/rows/recent_movies?limit=1&cursor=opaque.signature');
  assert.equal(requested[1], '/v1/home/rows/recent_movies?limit=1&start=2&revision=3%3A1&anchorId=m3');
  await assert.rejects(() => fetchHomeRow(client, 'recent_movies', {cursor: 'c', start: 1}), /cursor or by anchored range/);
  await assert.rejects(() => fetchHomeRow(api(() => row({id: 'other'})), 'recent_movies'), {code:'invalid_home'});
});

test('home and layout mutations use the published endpoints', async () => {
  const seen: {path: string; method?: string; body?: unknown}[] = [];
  const client = api((path, method, body) => {
    seen.push({path, method, body});
    return path.startsWith('/v1/home?') ? document() : {revision: 3, rowOrder: ['favorites'], hiddenRowIds: ['trending']};
  });
  await fetchHome(client, 6);
  const layout = await saveHomeLayout(client, {expectedRevision: 2, rowOrder: ['favorites'], hiddenRowIds: ['trending'], idempotencyKey: 'k'});
  assert.equal(layout.revision, 3);
  await resetHomeLayout(client, 'k2');
  assert.deepEqual(seen.map(call => `${call.method} ${call.path}`), ['GET /v1/home?limit=6', 'PUT /v1/home/layout', 'POST /v1/home/layout/reset']);
  assert.deepEqual(seen[1].body, {expectedRevision: 2, idempotencyKey: 'k', rowOrder: ['favorites'], hiddenRowIds: ['trending']});
});

test('suggestions carry a reason, a source and a bounded score', () => {
  const payload = {items: [{entry: entry('m4'), reason: 'Because you watched Movie m1', source: 'because_you_watched', score: 0.75}], total: 1, revision: {catalog: 3, viewer: 1}, generatedAt: '2026-09-16T12:00:00.000Z'};
  const parsed = validateSuggestions(payload);
  assert.equal(parsed.items[0].source, 'because_you_watched');
  // What a suggestion is only drawn with reads neutrally; one that cannot be read is left out alone.
  const loose = validateSuggestions({...payload, total: 0, generatedAt: undefined, items: [{entry: entry('m4'), score: 2}, {entry: entry('m4'), reason: 'Again', source: 's', score: 0.5}, {entry: {id: 'broken'}, reason: 'r', source: 's', score: 0.5}, {entry: entry('m5'), reason: 'r', source: 's', score: 0.5, future: 1}]});
  assert.deepEqual(loose.items.map(item => [item.entry.id, item.reason, item.source, item.score]), [['m4', '', '', 0], ['m5', 'r', 's', 0.5]]);
  assert.equal(loose.total, 2);
  assert.equal(loose.generatedAt, '');
  // The bound and the revision stay exact.
  for (const broken of [{...payload, items: Array.from({length: 101}, (_, i) => ({...payload.items[0], entry: entry(`m${i}`)}))}, {...payload, items: 'nope'}, {...payload, revision: {catalog: -1, viewer: 1}}]) {
    assert.throws(() => validateSuggestions(broken), {code:'invalid_recommendations'});
  }
});

test('per-item recommendation rows must be explained and must exclude their own item', async () => {
  const related = row({id: 'genre:tmdb:16', title: 'More Animation', kind: 'related', relation: 'genre', provider: 'tmdb', evidenceId: '16', entries: [entry('m2')], total: 1, limit: 1, hasMore: false, nextCursor: '', cursorCapable: false, reorderable: false, priority: 0, cacheTtlMs: 300000, endpoint: '/v1/items/m1/recommendations'});
  const parsed = validateItemRecommendations({itemId: 'm1', rows: [related], revision: {catalog: 3, viewer: 1}}, 'm1');
  assert.equal(parsed.rows[0].relation, 'genre');
  // The title itself is never one of its own recommendations: that entry is left out, and a row with nothing left is not shown.
  const others = validateItemRecommendations({itemId: 'm1', rows: [{...related, id: 'only-self', entries: [entry('m1')], total: 1}, {...related, id: 'mixed', entries: [entry('m1'), entry('m3')]}, {...related, id: ''}, related, related], revision: {catalog: 3, viewer: 1}}, 'm1');
  assert.deepEqual(others.rows.map(r => [r.id, r.entries.map(e => e.id)]), [['mixed', ['m3']], ['genre:tmdb:16', ['m2']]]);
  // A row without a relation is still shown under its own title.
  assert.equal(validateItemRecommendations({itemId: 'm1', rows: [{...related, relation: undefined}], revision: {catalog: 3, viewer: 1}}, 'm1').rows.length, 1);
  // The title asked for, the revision and the bound of 12 rows stay exact.
  assert.throws(() => validateItemRecommendations({itemId: 'm1', rows: [related], revision: {catalog: 3, viewer: 1}}, 'other'), {code:'invalid_recommendations'});
  assert.throws(() => validateItemRecommendations({itemId: 'm1', rows: [related], revision: {catalog: 3}}, 'm1'), {code:'invalid_recommendations'});
  assert.throws(() => validateItemRecommendations({itemId: 'm1', rows: Array.from({length: 13}, (_, i) => ({...related, id: `r${i}`})), revision: {catalog: 3, viewer: 1}}, 'm1'), {code:'invalid_recommendations'});
  const paths: string[] = [];
  const client = api(path => {
    paths.push(path);
    return path.startsWith('/v1/suggestions')
      ? {items: [], total: 0, revision: {catalog: 3, viewer: 1}, generatedAt: '2026-09-16T12:00:00.000Z'}
      : {itemId: 'm1', rows: [related], revision: {catalog: 3, viewer: 1}};
  });
  await fetchSuggestions(client, 4);
  await fetchItemRecommendations(client, 'm1', 6);
  assert.deepEqual(paths, ['/v1/suggestions?limit=4', '/v1/items/m1/recommendations?limit=6']);
});

test('a row descriptor without entries still validates its own shape', () => {
  const empty = validateHomeRow(row({id: 'continue', required: true, hideable: false, critical: true, entries: [], total: 0, hasMore: false, nextCursor: ''}));
  assert.equal(empty.entries.length, 0);
  assert.equal(empty.required, true);
});

test('GP-RH-1: rows preserve every loaded card and stop only at the server true end', async () => {
  const {appendHomeRowPage, homeRowPagingState} = await import('../src/home.ts');
  const entry = (i: number) => ({id: `e${i}`, kind: 'movie', title: `Title ${i}`, navigation: {view: 'item', entityId: `e${i}`}});
  const page = (from: number, n: number, cursor: string) => validateHomeRow(row({entries: Array.from({length: n}, (_, k) => entry(from + k)), total: 1203, hasMore: !!cursor, nextCursor: cursor}));
  let current = page(0, 37, 'c1');
  assert.deepEqual(homeRowPagingState(current, 25, false), {prefetch:false,skeletons:0,exhausted:false});
  assert.equal(homeRowPagingState(current, 26, false).prefetch, true);
  assert.equal(homeRowPagingState(current, 36, true).skeletons, 1, 'one non-entity tail while the next server page is pending');
  for (let from = 37; from < 1203; from += 37) {
    const prefix = current.entries.map(e => e.id);
    current = appendHomeRowPage(current, page(from, Math.min(37, 1203-from), from+37 < 1203 ? `c${from}` : ''));
    assert.deepEqual(current.entries.slice(0,prefix.length).map(e => e.id),prefix);
  }
  assert.equal(current.entries.length, 1203);
  assert.equal(new Set(current.entries.map(e => e.id)).size, 1203);
  assert.equal(current.nextCursor, '');
  assert.equal(homeRowPagingState(current,1202,false).exhausted,true);
});

test('CD-31/CD-32: Home stays bounded at 64 rows and 64 layout IDs including DVR rows', () => {
  const makeRows = (n: number) => Array.from({length: n}, (_, i) => row({id: `recent_lib${i}`}));
  // 64 populated rows validate; a 65th refuses the whole document rather than half-rendering.
  assert.doesNotThrow(() => validateHomeDocument(document({rows: makeRows(64), layout: {revision: 2, rowOrder: makeRows(64).map(r => r.id), hiddenRowIds: []}})));
  assert.throws(() => validateHomeDocument(document({rows: makeRows(65)})), {code: 'invalid_home'});
  // Saving 41–64 row layouts succeeds, including a DVR row (recent_<64hex>).
  const dvrRow = 'recent_' + 'a'.repeat(64);
  const order41 = [...Array.from({length: 40}, (_, i) => `recent_lib${i}`), dvrRow];
  assert.equal(order41.length, 41);
  assert.doesNotThrow(() => validateHomeLayout({revision: 3, rowOrder: order41, hiddenRowIds: []}));
  const order64 = [...Array.from({length: 63}, (_, i) => `recent_lib${i}`), dvrRow];
  assert.equal(order64.length, 64);
  assert.doesNotThrow(() => validateHomeLayout({revision: 3, rowOrder: order64, hiddenRowIds: []}));
  assert.throws(() => validateHomeLayout({revision: 3, rowOrder: [...order64, 'extra'], hiddenRowIds: []}), {code: 'invalid_home'});
  // Required rows stay protected: a required row is never hideable.
  assert.equal(validateHomeDocument(document({rows: [row({id: 'continue', required: true, hideable: true})]})).rows[0].hideable, false);
});

test('a movie\'s More like this row is the similar-titles row under the catalogue heading, else the first row', () => {
  const base = {kind: 'related', provider: 'local', total: 1, limit: 12, hasMore: false, nextCursor: '', cursorCapable: false, reorderable: false, priority: 0, cacheTtlMs: 300000, endpoint: '/v1/items/m1/recommendations'};
  const genre = row({...base, id: 'genre:tmdb:16', title: 'More Animation', relation: 'genre', evidenceId: '16', entries: [entry('m2')]});
  const similar = row({...base, id: 'because_you_watched:local:m1', title: 'Because you watched Title m1', relation: 'because_you_watched', evidenceId: 'm1', entries: [entry('m3')]});
  const parsed = (rows: any[]) => validateItemRecommendations({itemId: 'm1', rows, revision: {catalog: 3, viewer: 1}}, 'm1');
  assert.deepEqual(moreLikeThisRow(parsed([genre, similar]), 'More like this'), {id: 'because_you_watched:local:m1', title: 'More like this', entries: parsed([similar]).rows[0].entries});
  assert.equal(moreLikeThisRow(parsed([genre]), 'More like this')?.title, 'More Animation');
  assert.equal(moreLikeThisRow(parsed([]), 'More like this'), undefined);
  assert.equal(moreLikeThisRow(null, 'More like this'), undefined);
});

test('Home-origin membership keeps exact delivery targets across page append',async()=>{
 const {appendHomeRowPage}=await import('../src/home.ts');
 const first=validateHomeRow(row({id:'new_on_server',entries:[entry('season','season')],displayTargets:[{membershipId:'show',entryId:'season'}],why:{code:'reason',fallback:'Actual reason'},whyTitles:[{id:'seed',name:'Actual seed'}],nextCursor:'second'}));
 const second=validateHomeRow(row({id:'new_on_server',entries:[entry('episode','episode')],displayTargets:[{membershipId:'other-show',entryId:'episode'}],nextCursor:''}));
 const merged=appendHomeRowPage(first,second);
 assert.deepEqual(merged.displayTargets,[{membershipId:'show',entryId:'season'},{membershipId:'other-show',entryId:'episode'}]);
 assert.deepEqual(merged.whyTitles,[{id:'seed',name:'Actual seed'}]);
 assert.throws(()=>validateHomeRow(row({displayTargets:[{membershipId:'show',entryId:'absent'}]})),{code:'invalid_home'});
 assert.throws(()=>appendHomeRowPage(first,validateHomeRow(row({id:'new_on_server',entries:[entry('new-season','season')],displayTargets:[{membershipId:'show',entryId:'new-season'}]}))),{code:'stale_continuation'});
});
