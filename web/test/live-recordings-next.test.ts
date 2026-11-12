import test from 'node:test';
import assert from 'node:assert/strict';
import {createRecordingsController} from '../src/screens/live/recordings-controller.ts';
import type {ChannelApi} from '@core/channel-guide.ts';

// The recordings screens against the new server's routes: GET /v1/dvr/recordings (Upcoming, and History with
// state=history), GET /v1/dvr/rules, GET /v1/dvr for the counts and the space.
type Call = Readonly<{path: string; method: string}>;
const booking = (id: string, patch: Record<string, unknown> = {}) => ({id, channel: 'ch_1', title: 'Airing', startsAt: '2026-11-12T20:00:00Z',
  endsAt: '2026-11-12T21:00:00Z', padBeforeMs: 0, padAfterMs: 0, priority: 0, state: 'scheduled', inGuide: true, keep: false, fromRule: false, ...patch});
const dvr = (patch: Record<string, unknown> = {}) => ({upcoming: 3, recorded: 12, unwatched: 4, conflicts: 1, freeBytes: 300e9, hoursLeft: 40,
  keepRule: 'until-space-needed', folderOk: true, canRecord: true, ...patch});
const tick = async () => { for (let n = 0; n < 6; n++) await new Promise<void>(done => setTimeout(done, 0)); };

function server(answer: (call: Call) => unknown) {
  const calls: Call[] = [];
  const api: ChannelApi = {async request<T>(path: string, method = 'GET'): Promise<T> {
    const call = {path, method};
    calls.push(call);
    const value = await answer(call);
    if (value instanceof Error) throw value;
    return value as T;
  }};
  return {api, calls};
}
const controller = (api: ChannelApi, isCurrent = () => true) => createRecordingsController({api, serverId: 'server',
  signal: new AbortController().signal, isCurrent});
const route = (call: Call) => new URL(call.path, 'http://x');

test('each tab reads its own route: Upcoming, History, Storage and the Recorded titles', async () => {
  const {api, calls} = server(call => {
    const {pathname, searchParams} = route(call);
    if (pathname === '/v1/dvr') return dvr();
    if (pathname === '/v1/dvr/rules') return {items: []};
    if (searchParams.get('state') === 'history') return {items: [booking('h1', {state: 'recorded', title: 'News', itemId: 'itm_1'}), booking('h2', {state: 'failed', title: 'News'}),
      booking('h3', {state: 'partial', title: 'Film'})]};
    return {items: [booking('u1')]};
  });
  const c = controller(api);
  await c.load({view: 'upcoming'}); await tick();
  assert.equal(c.getSnapshot().page?.recordings[0]?.id, 'u1');
  await c.load({view: 'history'}); await tick();
  assert.deepEqual(c.getSnapshot().page?.recordings.map(one => one.state), ['completed', 'failed', 'incomplete-playable']);
  await c.load({view: 'storage'}); await tick();
  assert.deepEqual(c.getSnapshot().storage, {freeBytes: 300e9, hoursLeft: 40, keepRule: 'until-space-needed', writeHealthy: true});
  // Recorded: the recordings that were made, by title, each naming its title in the Recorded TV library.
  const groups = await c.readGroups('title');
  assert.deepEqual(groups.groups.map(group => [group.title, group.recordings, group.exceptions]), [['Film', 1, 1], ['News', 1, 0]]);
  assert.equal(groups.groups[1]!.recording.itemId, 'itm_1');
  assert.ok(calls.every(call => route(call).pathname.startsWith('/v1/dvr')));
});

test('an Upcoming page\'s cursor is the server\'s, carried forward exactly', async () => {
  const {api, calls} = server(call => route(call).searchParams.get('cursor') === 'opaque/2' ? {items: [booking('u2')]} : {items: [booking('u1')], nextCursor: 'opaque/2'});
  const c = controller(api);
  await c.load({view: 'upcoming'}); await tick();
  assert.equal(c.getSnapshot().page?.nextCursor, 'opaque/2');
  await c.more(); await tick();
  assert.equal(route(calls.at(-1)!).searchParams.get('cursor'), 'opaque/2');
  assert.equal(c.getSnapshot().page?.recordings[0]?.id, 'u2');
});

test('the counts are the server\'s, never the loaded page, and the newest answer wins', async () => {
  let held: ((value: unknown) => void) | undefined, n = 0;
  const {api} = server(call => {
    const {pathname} = route(call);
    if (pathname === '/v1/dvr/rules') return {items: [{id: 'rule', title: 'News', source: 'lsr', byTitle: true, newOnly: true, priority: 0, paused: false}]};
    if (pathname === '/v1/dvr/recordings') return {items: []};
    // The first summary's read of the counts is held back and settles last.
    if (pathname === '/v1/dvr' && n++ === 0) return new Promise(resolve => { held = resolve; });
    return dvr({upcoming: 7});
  });
  const c = controller(api);
  const first = c.readSummary(), second = await c.readSummary();
  assert.deepEqual([second.upcoming, second.rules, second.conflicts], [7, 1, 1]);
  held?.(dvr({upcoming: 99}));
  await first.catch(() => undefined); await tick();
  assert.equal(c.getSnapshot().summary?.upcoming, 7, 'an older answer that settles last does not replace the newer one');
});

test('a refused read (403) clears what the tab showed and says access was refused', async () => {
  let refuse = false;
  const {api} = server(() => refuse ? Object.assign(new Error('forbidden'), {status: 403}) : {items: [booking('u1')]});
  const c = controller(api);
  await c.load({view: 'upcoming'}); await tick();
  assert.equal(c.getSnapshot().page?.recordings.length, 1);
  refuse = true;
  await c.load({view: 'upcoming'}); await tick();
  assert.equal(c.getSnapshot().page, null);
  assert.equal(c.getSnapshot().accessDenied, true);
});

test('once the screen is no longer current, nothing more is read', async () => {
  let current = true;
  const {api, calls} = server(() => ({items: []}));
  const c = controller(api, () => current);
  await c.load({view: 'upcoming'}); await tick();
  const before = calls.length;
  current = false;
  // A retired screen's load is dropped without settling; what matters is that it sends nothing.
  c.load({view: 'history'}).catch(() => {}); await tick();
  await assert.rejects(c.readSummary());
  assert.equal(calls.length, before);
});

test('cancelling a booking, stopping one that is recording, keeping and deleting each send one request to its route', async () => {
  const {api, calls} = server(call => {
    if (call.method !== 'GET') return call.method === 'PATCH' ? booking('h1', {state: 'recorded', keep: true}) : undefined;
    return route(call).searchParams.get('state') === 'history' ? {items: [booking('h1', {state: 'recorded'})]} : {items: [booking('u1'), booking('u2', {state: 'recording'})]};
  });
  const c = controller(api);
  await c.load({view: 'upcoming'}); await tick();
  const [scheduled, running] = c.getSnapshot().page!.recordings;
  await c.cancel(scheduled!); await tick();
  await c.cancel(c.getSnapshot().page!.recordings.find(one => one.id === running!.id) ?? running!); await tick();
  await c.load({view: 'history'}); await tick();
  const made = c.getSnapshot().page!.recordings[0]!;
  await c.keep(made); await tick();
  const preview = await c.deletePreview(c.getSnapshot().page!.recordings[0]!);
  assert.ok(preview, 'delete is offered for a recording that was made');
  await c.confirmDelete(c.getSnapshot().page!.recordings[0]!); await tick();
  assert.deepEqual(calls.filter(call => call.method !== 'GET').map(call => [call.method, call.path]), [
    ['DELETE', '/v1/dvr/recordings/u1'], ['DELETE', '/v1/dvr/recordings/u2?action=stop'], ['PATCH', '/v1/dvr/recordings/h1'], ['DELETE', '/v1/dvr/recordings/h1']]);
});

test('a second command while one is in flight is busy and sends nothing; a refused command keeps what is shown', async () => {
  let release: (() => void) | undefined, refuse = false;
  const {api, calls} = server(call => {
    if (call.method === 'GET') return {items: [booking('u1'), booking('u2')]};
    if (refuse) return Object.assign(new Error('Every tuner is busy.'), {status: 409});
    return new Promise<void>(resolve => { release = resolve; });
  });
  const c = controller(api);
  await c.load({view: 'upcoming'}); await tick();
  const [first, second] = c.getSnapshot().page!.recordings;
  const running = c.cancel(first!); await tick();
  assert.equal((await c.cancel(second!)).busy, true);
  assert.equal(calls.filter(call => call.method === 'DELETE').length, 1);
  release?.(); await running; await tick();
  refuse = true;
  const refused = await c.cancel(c.getSnapshot().page!.recordings[0]!).catch(() => ({committed: false}));
  assert.equal(refused.committed, false);
  assert.equal(c.getSnapshot().page?.recordings.length, 2, 'what is shown stays');
});
