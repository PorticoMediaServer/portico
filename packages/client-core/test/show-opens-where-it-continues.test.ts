import test from 'node:test';
import assert from 'node:assert/strict';
import {ShowWorkspaceService} from '../src/show-workspace.ts';
import {FreshnessHub} from '../src/freshness-hub.ts';
import {createViewerQueries} from '../src/query/viewer-queries.ts';

// The lead's follow-up to W1 F3 (16 Nov): a show always opened on Season 1, so once it was watched the page had
// no Play. 05-browse rule 23: the main action is the episode in progress, else the next unwatched one.
type Ep = {watched?: boolean; positionMs?: number; playedAt?: number};
function server(seasons: Ep[][]) {
  const item = (id: string, kind: string, extra: object) => ({id, kind, title: id, libraryId: 'L', addedAt: '', images: {}, state: {watched: false, playCount: 0, favorite: false, inList: false, notInterested: false}, ...extra});
  return {request: async <T,>(path: string): Promise<T> => {
    const url = new URL(path, 'http://server.test'), q = url.searchParams;
    if (url.pathname === '/v1/items/show') return item('show', 'show', {}) as T;
    const episode = (season: number, e: Ep, n: number) => item(`s${season}e${n + 1}`, 'episode', {index: n + 1, parentIndex: season, parentId: `s${season}`, rootId: 'show',
      state: {watched: !!e.watched, playCount: e.watched ? 1 : 0, favorite: false, inList: false, notInterested: false, ...(e.positionMs ? {positionMs: e.positionMs} : {}), ...(e.playedAt ? {lastPlayedAt: new Date(e.playedAt * 1000).toISOString()} : {})}});
    if (q.get('sort') === 'lastPlayed') return {items: seasons.flatMap((eps, s) => eps.map((e, n) => ({e, item: episode(s + 1, e, n)}))).filter(x => x.e.playedAt).sort((a, b) => b.e.playedAt! - a.e.playedAt!).map(x => x.item)} as T;
    if (q.get('parent') === 'show') return {items: seasons.map((eps, n) => item(`s${n + 1}`, 'season', {index: n + 1, counts: {children: eps.length, leaves: eps.length, unwatchedLeaves: eps.filter(e => !e.watched).length}}))} as T;
    const season = Number(q.get('parent')!.slice(1)), eps = seasons[season - 1]!.map((e, n) => episode(season, e, n));
    return {items: q.get('order') === 'desc' ? eps.reverse() : eps, total: eps.length} as T;
  }};
}
async function open(seasons: Ep[][], route: object = {}) {
  const scope = {serverId: 'server', viewerId: 'viewer'}, hub = new FreshnessHub({settleMs: 0});
  const queries = createViewerQueries({scope, hub, api: server(seasons) as never, gcTimeMs: Infinity});
  const show = new ShowWorkspaceService({queries, scope: scope as never});
  await show.select({libraryId: 'L', showId: 'show', ...route});
  const data = show.getSnapshot().data!;
  show.dispose(); queries.dispose(); hub.dispose();
  return {season: data.selected.seasonId, next: data.nextUp?.id};
}
const seen = {watched: true}, fresh = {};
test('a show opens on the season where it continues, with that episode as its main action', async () => {
  assert.deepEqual(await open([[fresh, fresh], [fresh, fresh]]), {season: 's1', next: 's1e1'}, 'nothing watched: the first season');
  assert.deepEqual(await open([[seen, seen], [fresh, fresh]]), {season: 's2', next: 's2e1'}, 'Season 1 watched: Season 2');
  assert.deepEqual(await open([[seen, seen], [seen, fresh, fresh], [fresh]]), {season: 's2', next: 's2e2'}, 'part way through Season 2');
  assert.deepEqual(await open([[fresh, fresh], [seen, seen], [fresh]]), {season: 's3', next: 's3e1'}, 'the season after the last one watched');
  assert.deepEqual(await open([[seen], [seen]]), {season: 's1', next: undefined}, 'all watched: the first season');
  assert.deepEqual(await open([[seen, fresh, seen, fresh]]), {season: 's1', next: 's1e4'}, 'the next unwatched after the last one watched');
  assert.deepEqual(await open([[seen, fresh, seen, fresh]], {episodeDirection: 'desc'}), {season: 's1', next: 's1e4'}, 'in the show\'s order whatever order the list is in');
  assert.deepEqual(await open([[seen, {positionMs: 5000}, fresh]]), {season: 's1', next: 's1e2'}, 'the episode in progress first');
  // The Simpsons on the demo: only S7 E25 started, nothing marked watched.
  assert.deepEqual(await open([[fresh, fresh], [fresh, {positionMs: 9000, playedAt: 10}]]), {season: 's2', next: 's2e2'}, 'the season of the episode in progress');
  assert.deepEqual(await open([[seen, seen], [{watched: true, playedAt: 5}, fresh], [{watched: true, playedAt: 3}]]), {season: 's2', next: 's2e2'}, 'from the season last played, not the last with a watched episode');
});
