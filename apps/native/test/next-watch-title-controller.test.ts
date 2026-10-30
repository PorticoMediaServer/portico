import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '../../../packages/client-core/src/index.ts';
import {NativeTitleController} from '../src-next/screens/watch/title-controller.ts';

const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
function fixture() {
  let current = authority, held: Promise<void> | undefined, host: {revision: number} = {revision: 1};
  const hostListeners = new Set<() => void>();
  const listeners = new Set<() => void>(), paths: URL[] = [], signals: (AbortSignal | null | undefined)[] = [], starts: any[] = [];
  const episode = (id: string) => ({id, kind: 'episode', title: id, libraryId: 'library', available: true, navigation: {view: 'item', entityId: id}, playback: {itemId: id, startMs: 17000}});
  const bodies: unknown[] = [];
  const api = new HttpLocalApi('https://portico.test', '', async (input, init) => {
    const url = new URL(String(input)); paths.push(url); signals.push(init?.signal);
    if (init?.method === 'PUT') {const body = JSON.parse(String(init.body)); bodies.push(body); await held; return new Response(JSON.stringify({revision: 2, ...body}), {status: 200});}
    await held;
    const selectedSeason = url.searchParams.get('selectedSeasonId') ?? 's1';
    const next = url.searchParams.get('cursor') === 'next';
    const entries = next ? [episode('e3')] : url.searchParams.get('playState') === 'unplayed' ? [episode('e2')] : [episode('e1'), episode('e2')];
    const revision = {catalog: 1, viewer: 1};
    return new Response(JSON.stringify({scope: {serverId: 'server', libraryId: 'library', viewerFence: 'viewer'}, revision,
      show: {id: 'show', libraryId: 'library', title: 'Actual show', libraryKind: 'tv', providerMatchStatus: 'matched'}, selected: {seasonId: selectedSeason},
      seasons: ['s1', 's2'].map((id, number) => ({id, showId: 'show', title: id, number: number + 1})), seasonTotalCount: 2, nextSeasonCursor: '',
      nextUp: episode('e2'), episodes: {scope: {serverId: 'server', libraryId: 'library', libraryKind: 'tv', viewerFence: 'viewer', view: 'season', entityId: selectedSeason}, revision,
        query: {sort: 'episode', direction: 'asc', category: '', q: '', limit: 40, searchMode: 'none'}, sections: [{id: 'episodes', type: 'list', entries, totalCount: 3, nextCursor: next ? '' : 'next'}]},
    }), {status: 200});
  });
  const controller = new NativeTitleController({api, scope: {serverId: 'server', viewerId: 'viewer'}, entry: {id: 'show', libraryId: 'library', kind: 'show', title: 'Actual show'},
    authority, current: () => current, subscribeCurrent(listener) {listeners.add(listener); return () => {listeners.delete(listener);};}, requestId: async () => 'request',
    host: () => host, subscribeHost(listener) {hostListeners.add(listener); return () => {hostListeners.delete(listener);};}, playback: {start: async input => {starts.push(input);}}});
  return {controller, paths, signals, starts, bodies, hold: (value: Promise<void>) => {held = value;},
    leave: () => {host = {revision: host.revision + 1}; for (const listener of hostListeners) listener();},
    retire: () => {current = {...current, epoch: 'new'}; for (const listener of listeners) listener();}};
}

test('title reads canonical show pages, sends Hide watched and retains it through a season change', async () => {
  const d = fixture();
  try {
    await d.controller.open(); assert.equal(d.controller.getSnapshot().workspace?.phase, 'ready');
    await d.controller.moreEpisodes(); assert.deepEqual(d.controller.getSnapshot().episodes.map(entry => entry.id), ['e1', 'e2', 'e3']);
    d.controller.remember({itemId: 'e3', offset: 22});
    await d.controller.hideWatched(true);
    assert.equal(d.paths.at(-1)?.searchParams.get('playState'), 'unplayed');
    assert.equal(d.controller.getSnapshot().place.hideWatched, true);
    assert.deepEqual(d.controller.getSnapshot().episodes.map(entry => entry.id), ['e2']);
    await d.controller.season('s2');
    assert.equal(d.paths.at(-1)?.searchParams.get('selectedSeasonId'), 's2');
    assert.equal(d.paths.at(-1)?.searchParams.get('playState'), 'unplayed');
    assert.equal(d.controller.getSnapshot().place.seasonId, 's2');
  } finally {d.controller.dispose();}
});

test('show Play names the genuine continuation and asks for authoritative Resume', async () => {
  const d = fixture();
  try {
    await d.controller.open(); await d.controller.play();
    assert.equal(d.starts.length, 1); assert.deepEqual(d.starts[0].target, {itemId: 'e2', startSeconds: 17});
    assert.equal(d.starts[0].startIntent, 'resume', 'the retained position is not an explicit seek');
    await d.controller.play(d.controller.entry, true); assert.equal(d.starts.length, 1);
  } finally {d.controller.dispose();}
});

test('an authority replacement aborts a pending title request and cannot apply its filter state', async () => {
  const d = fixture();
  try {
    await d.controller.open(); let release!: () => void;
    d.hold(new Promise<void>(resolve => {release = resolve;}));
    const pending = d.controller.hideWatched(true);
    await new Promise(resolve => setImmediate(resolve)); d.retire();
    assert.equal(d.signals.at(-1)?.aborted, true);
    release(); await pending;
    assert.equal(d.controller.getSnapshot().retired, true); assert.equal(d.controller.getSnapshot().place.hideWatched, false);
  } finally {d.controller.dispose();}
});

const settings = (revision: number) => ({hideSeasons: revision > 1, absoluteNumbering: false, ranges: false, newestFirst: false, revision});

test('owner show settings save the page and host the sheet opened on, and stop when either leaves', async () => {
  const d = fixture();
  try {
    await d.controller.open(); const before = d.paths.length;
    const lease = d.controller.pageLease()!;
    assert.equal(await d.controller.saveSettings(lease, settings(1)), true);
    assert.deepEqual(d.bodies[0], settings(1));
    assert.equal(d.paths.at(-1)?.pathname.endsWith('/show-workspace'), true);
    assert.ok(d.paths.length > before, 'the page is read again after the server applied the settings');
    // A page replaced since the sheet opened is never written.
    await d.controller.season('s2');
    assert.equal(await d.controller.saveSettings(lease, settings(2)), false);
    assert.equal(d.bodies.length, 1, 'only the page the sheet opened on is saved');
    // A route that leaves mid-write aborts the transport and never reports success.
    const moving = d.controller.pageLease()!;
    let release!: () => void, held!: Promise<void>;
    held = new Promise<void>(resolve => {release = resolve;});
    d.hold(held);
    const pending = d.controller.saveSettings(moving, settings(3));
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(d.bodies.length, 2, 'the write started');
    d.leave();
    assert.equal(d.signals.at(-1)?.aborted, true, 'the write is aborted on the wire when its host leaves');
    release();
    assert.equal(await pending, false);
    // Retirement writes nothing.
    d.retire();
    assert.equal(await d.controller.saveSettings(moving, settings(4)), false);
    assert.equal(d.bodies.length, 2);
  } finally {d.controller.dispose();}
});
