import test from 'node:test';
import assert from 'node:assert/strict';
import {appendHomeRowPage, fetchHomeRow, hasHomeRowContent, homeDocument, homeRowPagingState} from '../src/home.ts';
import type {Home, HomeRow} from '../../contracts/src/next/api.ts';

const observedAt = '2026-11-16T12:00:00Z', expiresAt = '2026-11-16T12:01:00Z';
function wire(ids: string[], nextCursor = ''): HomeRow {
  return {id: 'on_now', title: 'On now', shape: 'landscape', items: [], nextCursor, live: {
    revision: observedAt, observedAt, expiresAt, sort: 'favorites', group: '', groups: [], channels: ids.map((id, index) => {
      const library = id === 'library', sourceId = library ? id : 'server', provenance = library ? 'library-channel' : 'live-source';
      return {source: {id: sourceId, name: '', generation: '1', publishedAt: observedAt, availableStart: '', availableEnd: '', provenance, refreshState: 'ready'},
        channel: {id, sourceId, provenance, name: id, number: String(index + 1), group: '', generation: '1', logoPath: '/v1/images/' + 'a'.repeat(64),
          mark: {text: '1', seed: id}, programmes: [{id: 'programme-' + id, channelId: id, title: 'Current show', start: observedAt, end: expiresAt, lineage: 'interval-only', description: ''}],
          tuneAvailable: true, recordAvailable: false, tuneUnavailableReason: '', guide: 'full', favorite: true, hidden: false, preferenceRevision: 0},
        actions: {play: true, favorite: true}};
    })}};
}

test('rebuilt On now reaches the existing live renderer and appends opaque server pages to the true end', async () => {
  const first = homeDocument({rows: [wire(['live', 'library'], 'opaque&cursor=1')]} as Home).rows[0]!;
  assert.equal(hasHomeRowContent(first), true);
  assert.equal(first.entries.length, 0);
  assert.equal(first.live!.channels.length, 2);
  assert.equal(first.live!.channels[0]!.channel.logoPath, '/v1/images/' + 'a'.repeat(64));
  assert.equal(first.live!.channels[0]!.channel.programmes[0]!.title, 'Current show');
  assert.equal(first.live!.channels[1]!.channel.sourceId, 'library');
  assert.equal(homeRowPagingState(first, 0, false).prefetch, true);
  const asked: string[] = [];
  const api = {request: async <T>(path: string) => {asked.push(path); return wire(['last']) as T;}};
  const next = await fetchHomeRow(api, 'on_now', {cursor: first.nextCursor, limit: 50});
  assert.equal(new URL(asked[0]!, 'https://server').searchParams.get('cursor'), 'opaque&cursor=1');
  const all = appendHomeRowPage(first, next);
  assert.deepEqual(all.live!.channels.map(value => value.channel.id), ['live', 'library', 'last']);
  assert.equal(homeRowPagingState(all, 2, false).exhausted, true);
  assert.equal(all.live!.observedAt, observedAt);
  const emptyEnd = await fetchHomeRow({request: async <T>() => wire([]) as T}, 'on_now');
  assert.equal(appendHomeRowPage(first, emptyEnd).live!.channels.length, 2);
  assert.equal(homeDocument({rows: [wire([])]} as Home).rows.length, 0);
});

test('a different live observation cannot be appended to the retained row', () => {
  const first = homeDocument({rows: [wire(['first'])]} as Home).rows[0]!;
  const changed = wire(['next']);
  changed.live!.revision = 'different';
  const next = homeDocument({rows: [changed]} as Home).rows[0]!;
  assert.throws(() => appendHomeRowPage(first, next));
});

test('the rebuilt row adapter also keeps catalogue cards, counts and their continuation', async () => {
  const item = {id: 'film', kind: 'movie', title: 'A film', libraryId: 'films', images: {}, state: {watched: false, positionMs: 0, favorite: false, inList: false, playCount: 0}};
  const raw = {id: 'recently_added', title: 'Recently Added', shape: 'poster', items: [item], nextCursor: 'opaque-next', total: 12};
  const first = homeDocument({rows: [raw]} as Home).rows[0]!;
  const row = await fetchHomeRow({request: async <T>() => raw as T}, raw.id);
  assert.deepEqual(row.entries.map(entry => entry.id), ['film']);
  assert.deepEqual([first.nextCursor, row.nextCursor, row.total], ['opaque-next', 'opaque-next', 12]);
});
