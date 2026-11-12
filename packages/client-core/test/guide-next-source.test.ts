import test from 'node:test';
import assert from 'node:assert/strict';
import {nextGuideSource} from '../src/guide/next-source.ts';

const channel = (n: number) => ({id: `ch_${n}`, name: `Channel ${n}`, number: String(n), kind: 'live', favorite: false, hidden: false});
const programme = (n: number) => ({id: `pg_${n}`, startsAt: '2026-11-12T18:00:00Z', endsAt: '2026-11-12T19:00:00Z', title: `Show ${n}`, season: 1, episode: n});
function server() {
  const paths: string[] = [];
  const api = {request: async <T>(path: string): Promise<T> => {
    paths.push(path);
    const page = Number(new URL('http://x' + path).searchParams.get('cursor') ?? 0), rows = [0, 1].map(i => page * 2 + i + 1);
    const nextCursor = page < 2 ? String(page + 1) : undefined; // three pages of two channels
    if (path.startsWith('/v1/live/channels')) return {items: rows.map(channel), nextCursor} as T;
    return {items: rows.map(n => ({channel: channel(n), programmes: [programme(n)]})), nextCursor, outOfDate: false} as T;
  }};
  return {paths, api};
}
const signal = new AbortController().signal;

test('the guide screens page channels by place while the server pages by cursor, and scrolling back reads nothing again', async () => {
  const {paths, api} = server();
  const source = nextGuideSource(api);
  const first = await source.channels(0, 3, signal);
  assert.deepEqual(first.items.map(row => row.id), ['ch_1', 'ch_2', 'ch_3']);
  assert.equal(first.total, 5, 'more to scroll to, until the last page is read');
  const last = await source.channels(3, 10, signal);
  assert.deepEqual(last.items.map(row => row.id), ['ch_4', 'ch_5', 'ch_6']);
  assert.equal(last.total, 6);
  const reads = paths.length;
  await source.channels(0, 3, signal);
  assert.equal(paths.length, reads);
});

test('a block of the guide is answered by channel from the server\'s pages for that stretch of time, read once', async () => {
  const {paths, api} = server();
  const source = nextGuideSource(api);
  const start = Date.parse('2026-11-12T18:00:00Z'), end = Date.parse('2026-11-12T21:00:00Z');
  const block = await source.programs(['ch_1', 'ch_3'], start, end, signal);
  assert.deepEqual(block.ch_3!.map(p => [p.title, p.episode?.number, p.start]), [['Show 3', 3, start]]);
  assert.equal(paths.filter(path => path.startsWith('/v1/live/guide')).length, 2);
  await source.programs(['ch_2'], start, end, signal);
  assert.equal(paths.filter(path => path.startsWith('/v1/live/guide')).length, 2);
});

test('On now lists the server\'s order with what is on and next, and a channel named by a link is found by paging when the server has no around', async () => {
  const {nextOnNowSource, findChannel} = await import('../src/guide/next-source.ts');
  const now = {request: async <T>(path: string): Promise<T> => ({items: [{channel: channel(9), now: programme(9)}, {channel: channel(2)}], nextCursor: undefined}) as T};
  const source = nextOnNowSource(now);
  const page = await source.channels(0, 10, signal);
  assert.deepEqual(page.items.map(row => row.id), ['ch_9', 'ch_2']);
  const block = await source.programs(['ch_9', 'ch_2'], Date.parse('2026-11-12T18:30:00Z'), Date.parse('2026-11-12T19:30:00Z'), signal);
  assert.deepEqual([block.ch_9!.length, block.ch_2!.length], [1, 0]);
  const {api} = server();
  const found = await findChannel(api, 'ch_5', Date.parse('2026-11-12T18:30:00Z'));
  assert.deepEqual([found?.row.name, found?.now?.title], ['Channel 5', 'Show 5']);
  assert.equal(await findChannel(api, 'ch_99'), undefined);
});

test('a linked channel the profile may not see is simply not found', async () => {
  const {findChannel} = await import('../src/guide/next-source.ts');
  const api = {request: async <T>(): Promise<T> => { throw Object.assign(new Error('Not found.'), {status: 404, code: 'not_found'}); }};
  assert.equal(await findChannel(api, 'ch_hidden'), undefined);
});
