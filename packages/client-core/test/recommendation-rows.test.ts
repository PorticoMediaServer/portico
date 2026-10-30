import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../i18n/src/index.ts';
import {homeRowHeading, isPersonalRowId, validateHomeLayoutView, validateHomeRow} from '../src/home.ts';
import {validateContentProjection as parseContentProjection} from '../src/library-content.ts';
import {fetchShowRecommendations, moreLikeThisRow, titleRecommendationRows, validateItemRecommendations} from '../src/recommendations.ts';
import {serverTextLabel} from '../src/presentation/server-text.ts';
import {HomeRowPager} from '../src/home-row-page.ts';

// Recommendations P7: personal rows, their parameterised titles and the See all page.

const entry = (id: string) => ({id, kind: 'movie', title: `Title ${id}`, navigation: {view: 'item', entityId: id}});
function row(changes: Record<string, unknown> = {}): any {
  return {
    id: 'for_you:genre:drama', title: 'Drama for you', titleText: {code: 'home.row.genreForYou', params: {genre: 'Drama'}, fallback: 'Drama for you'}, family: 'for_you',
    kind: 'recommendation', artworkShape: 'poster', endpoint: '/v1/home/rows/for_you:genre:drama', viewerFence: 'fence', privacySensitivity: 'personal', policyState: 'available',
    priority: 61, cacheTtlMs: 300000, required: false, hideable: true, reorderable: true, critical: false, cursorCapable: true,
    entries: [entry('m1'), entry('m2')], total: 5, start: 0, limit: 2, hasMore: true, nextCursor: 'c1', revision: {catalog: 1, viewer: 1}, ...changes,
  };
}

test('a personal row keeps its family and parameterised title', () => {
  const parsed = validateHomeRow(row());
  assert.equal(parsed.family, 'for_you');
  assert.deepEqual(parsed.titleText, {code: 'home.row.genreForYou', params: {genre: 'Drama'}, fallback: 'Drama for you'});
  assert.equal(serverTextLabel(defaultI18n, parsed.titleText), 'Drama for you');
  assert.deepEqual(homeRowHeading(parsed), {key: 'home.row.genreForYou', fallback: 'Drama for you', params: {genre: 'Drama'}});
  assert.ok(isPersonalRowId(parsed.id) && !isPersonalRowId('for_you') && !isPersonalRowId('recommended'));
  // Title text that cannot be read is left out, and the row's plain title names it.
  for (const titleText of [{code: 'home.row.genreForYou'}, {code: 1, fallback: 'x'}]) {
    const loose = validateHomeRow(row({titleText}));
    assert.equal(loose.titleText, undefined);
    assert.equal(loose.title, 'Drama for you');
  }
  // Parameters that cannot be read are left out, so the fallback names the row.
  assert.deepEqual(validateHomeRow(row({titleText: {code: 'home.row.genreForYou', fallback: 'Drama for you', params: {genre: 3}}})).titleText, {code: 'home.row.genreForYou', params: {}, fallback: 'Drama for you'});
  assert.deepEqual(validateHomeRow(row({titleText: {code: 'home.row.genreForYou', fallback: 'Drama for you', params: ['Drama']}})).titleText, {code: 'home.row.genreForYou', fallback: 'Drama for you'});
  assert.equal(serverTextLabel(defaultI18n, validateHomeRow(row({titleText: {code: 'home.row.genreForYou', fallback: 'Drama for you', params: {genre: 3}}})).titleText), 'Drama for you');
});

test('titles come from the catalogue, with the fallback for an unknown code or missing params', () => {
  const t = (code: string, params?: Record<string, string>, fallback = 'Server words') => serverTextLabel(defaultI18n, {code, params, fallback});
  assert.equal(t('home.row.moreWith', {name: 'Ada Lovelace'}), 'More with Ada Lovelace');
  assert.equal(t('home.row.starring', {name: 'Ada'}), 'Starring Ada');
  assert.equal(t('home.row.moreLike', {title: 'Heat'}), 'More like Heat');
  assert.equal(t('home.row.viewersAlsoWatched'), 'Viewers also watched');
  assert.equal(t('home.row.topRated'), 'Top rated you haven’t seen');
  assert.equal(t('home.row.moodForYou', {mood: 'Feel-good'}), 'Feel-good picks for you');
  assert.equal(t('home.row.themeForYou', {theme: 'Time travel'}), 'Time travel for you');
  assert.equal(t('home.row.someFutureRow'), 'Server words');
  assert.equal(t(''), 'Server words');
  assert.equal(t('home.row.moreWith'), 'Server words', 'a message whose params are missing keeps the server’s words');
  // A content heading names itself the same way.
  assert.equal(serverTextLabel(defaultI18n, {key: 'home.row.fromCreator', params: {name: 'Vince Gilligan'}, fallback: 'From Vince Gilligan'}), 'From Vince Gilligan');
});

test('the layout lists the family once, under its own title', () => {
  const view = validateHomeLayoutView({revision: 1, rowOrder: [], hiddenRowIds: ['for_you'], rows: [
    {id: 'recommended', title: 'Recommended for you', kind: 'recommendation', artworkShape: 'poster', required: false, hideable: true, reorderable: true, hidden: false},
    {id: 'for_you', title: 'Picks for you', titleText: {code: 'home.row.picksForYou', fallback: 'Picks for you'}, kind: 'family', artworkShape: 'poster', required: false, hideable: true, reorderable: true, hidden: true},
  ]});
  assert.equal(view.rows[1]!.titleText?.code, 'home.row.picksForYou');
  assert.equal(serverTextLabel(defaultI18n, view.rows[1]!.titleText), 'Picks for you');
});

test('Discover headings carry params', () => {
  const projection = parseContentProjection({
    heading: {key: 'library.discover', fallback: 'Films'},
    scope: {serverId: 's', viewerFence: 'f', libraryId: 'l', libraryKind: 'movie', view: 'discover', entityId: ''},
    navigation: [], query: {sort: 'server', direction: 'asc', category: '', q: '', limit: 8, searchMode: 'none'}, revision: {catalog: 1, viewer: 1}, sorts: [], filters: [],
    sections: [{id: 'for_you:genre:drama', type: 'rail', heading: {key: 'home.row.genreForYou', fallback: 'Drama for you', params: {genre: 'Drama'}}, entries: [entry('m1')], totalCount: 1, nextCursor: '',
      seeAll: {pivot: 'movies', query: {variant:'predicate',field: 'genre', operator: 'contains', value: 'Drama'}, sort: [{field: 'forYou', direction: 'desc'}]}},
      ...['recommended', 'for_you:new', 'for_you:gems', 'for_you:top', 'trending_now', 'recently_added'].map(id => ({id, type: 'rail', heading: {key: 'section.' + id, fallback: id}, entries: [entry(id)], totalCount: 1, nextCursor: ''}))],
  } as never, {libraryId: 'l', view: 'discover'}, {viewerId: 'v', serverId: 's'}, 60);
  assert.deepEqual(projection.sections[0]!.heading, {key: 'home.row.genreForYou', fallback: 'Drama for you', params: {genre: 'Drama'}});
  assert.deepEqual(projection.sections[0]!.seeAll, {pivot: 'movies', query: {variant:'predicate',field: 'genre', operator: 'contains', value: 'Drama'}, sort: [{field: 'forYou', direction: 'desc'}]});
  assert.equal(projection.sections.length, 7, 'Discover carries more than four rows now');
  assert.equal(projection.sections[1]!.seeAll, undefined);
});

const related = (relation: string, extra: Record<string, unknown> = {}) => row({id: `${relation}:local:x`, title: relation, titleText: undefined, family: undefined, kind: 'related', relation, entries: [entry(relation + '1')], total: 1, limit: 1, hasMore: false, nextCursor: '', cursorCapable: false, ...extra});

test('a title page shows every row the endpoint returned, in its order', () => {
  const recs = validateItemRecommendations({itemId: 'm0', revision: {catalog: 1, viewer: 1}, rows: [
    related('more_like', {titleText: {code: 'home.row.moreLike', params: {title: 'Heat'}, fallback: 'More like Heat'}}), related('starring'), related('creator'), related('viewers_also_watched'), related('director'), related('genre'),
  ]}, 'm0');
  assert.deepEqual(titleRecommendationRows(recs).map(r => r.relation), ['more_like', 'starring', 'creator', 'viewers_also_watched', 'director', 'genre']);
  assert.equal(moreLikeThisRow(recs, 'More like this')?.id, 'more_like:local:x');
  const older = validateItemRecommendations({itemId: 'm0', revision: {catalog: 1, viewer: 1}, rows: [related('genre'), related('director')]}, 'm0');
  assert.deepEqual(titleRecommendationRows(older).map(r => r.relation), ['genre', 'director']);
  assert.deepEqual(titleRecommendationRows(null), []);
});

test('a show page reads its own rows', async () => {
  const paths: string[] = [];
  const api = {request: async <T>(path: string) => { paths.push(path); return {showId: 's 1', revision: {catalog: 1, viewer: 1}, rows: [related('more_like')]} as T; }};
  const recs = await fetchShowRecommendations(api, 's 1', 12);
  assert.equal(paths[0], '/v1/shows/s%201/recommendations?limit=12');
  assert.equal(recs.rows[0]!.relation, 'more_like');
  const echo = {request: async <T>() => ({showId: 's1', revision: {catalog: 1, viewer: 1}, rows: [related('more_like', {entries: [entry('s1')]})]} as T)};
  // The show itself is never one of its own recommendations: a row left with nothing is not shown.
  assert.equal((await fetchShowRecommendations(echo, 's1')).rows.length, 0);
  await assert.rejects(fetchShowRecommendations(echo, 'another'));
});

test('See all pages the whole row and starts again on a stale cursor', async () => {
  const calls: string[] = [];
  let stale = true;
  const api = {request: async <T>(path: string) => {
    calls.push(path);
    if (path.includes('cursor=c1')) {
      if (stale) { stale = false; throw Object.assign(new Error('changed'), {code: 'stale_continuation'}); }
      return row({entries: [entry('m2'), entry('m3')], nextCursor: ''}) as T;
    }
    return row() as T;
  }};
  const pager = new HomeRowPager(api, 'for_you:genre:drama');
  await pager.load();
  assert.equal(calls[0], '/v1/home/rows/for_you%3Agenre%3Adrama?limit=60');
  assert.deepEqual(pager.getSnapshot().entries.map(e => e.id), ['m1', 'm2']);
  await pager.more();
  assert.equal(pager.getSnapshot().restarts, 1, 'a stale cursor restarts from the first page');
  assert.deepEqual(pager.getSnapshot().entries.map(e => e.id), ['m1', 'm2']);
  await pager.more();
  assert.deepEqual(pager.getSnapshot().entries.map(e => e.id), ['m1', 'm2', 'm3'], 'a repeated entry is dropped, nothing is capped');
  assert.equal(pager.getSnapshot().nextCursor, '');
  await pager.more();
  assert.equal(calls.length, 4, 'no cursor, no request');
  pager.dispose();
});

// A screen that is open is never blanked: a refused cursor starts the row again
// behind what is shown, and the first page replaces it when it arrives.
test('stale cursor restart keeps the grid on screen until its replacement first page arrives', async () => {
  let resolveRestart!: (value: unknown) => void;
  const restart = new Promise<unknown>(resolve => { resolveRestart = resolve; });
  let first = true;
  const api = {request: async <T,>(path: string) => {
    if (path.includes('cursor=')) throw Object.assign(new Error('changed'), {code: 'stale_continuation'});
    if (first) { first = false; return row() as T; }
    return restart as T;
  }};
  const pager = new HomeRowPager(api, 'for_you:genre:drama');
  await pager.load();
  const loading = pager.more();
  await new Promise(setImmediate);
  const shown = pager.getSnapshot().entries.map(card => card.id);
  assert.equal(pager.getSnapshot().phase, 'ready');
  assert.ok(shown.length > 0 && !shown.includes('fresh'), 'the entries already shown stay');
  assert.notEqual(pager.getSnapshot().row, undefined);
  assert.equal(pager.getSnapshot().nextCursor, '', 'the refused cursor is not offered again');
  assert.equal(pager.getSnapshot().restarts, 1);
  resolveRestart(row({entries: [entry('fresh')], nextCursor: ''}));
  await loading;
  assert.deepEqual(pager.getSnapshot().entries.map(card => card.id), ['fresh']);
  pager.dispose();
});

// Deep-list refresh: a title removed from a page the viewer scrolled into goes on the next quiet refresh.
test('a quiet refresh reads every page scrolled into again, so a title removed deep in the row goes', async () => {
  let removed = false;
  const api = {request: async <T,>(path: string) => {
    if (path.includes('cursor=c1')) return row({entries: removed ? [entry('m3')] : [entry('m3'), entry('m4')], nextCursor: 'c2'}) as T;
    if (path.includes('cursor=c2')) return row({entries: [entry('m5')], nextCursor: ''}) as T;
    return row() as T;
  }};
  const pager = new HomeRowPager(api, 'for_you:genre:drama');
  await pager.load();
  await pager.more();
  assert.deepEqual(pager.getSnapshot().entries.map(e => e.id), ['m1', 'm2', 'm3', 'm4']);
  removed = true;
  await pager.revalidate();
  assert.deepEqual(pager.getSnapshot().entries.map(e => e.id), ['m1', 'm2', 'm3', 'm5'], 'm4 is gone; the next page now begins where the re-read ended');
  assert.equal(pager.getSnapshot().nextCursor, '');
  assert.equal(pager.getSnapshot().phase, 'ready');
  pager.dispose();
});
