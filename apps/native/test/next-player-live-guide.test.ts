import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {playbackFence, sameFence} from '../../../packages/player-core/src/identity.ts';
import * as playerGuide from '../../../packages/client-core/src/guide/player-guide.ts';
import * as legacyGuide from '../../../packages/client-core/src/guide/legacy-source.ts';
import {liveGuideChannel, nextChannelRow} from '../../../packages/client-core/src/guide/next-source.ts';
import {sameAuthority} from '../src-next/shell/model.ts';

const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'family', viewerFence: 'viewer'};
const scope = {serverId: 'server', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane', authority: 'local'} as const;
const fence = playbackFence({kind: 'channel', playback: {scope, run: 1}, sessionId: 'session', channelKind: 'live-source', sourceId: 'server', channelId: 'channel-2', channelGeneration: '1', generation: 1});
const settle = () => new Promise<void>(done => setImmediate(done));

async function fixture({touch = true, origin = true, routeKind = 'shell', invalid = '', pending = false}: {touch?: boolean; origin?: boolean; routeKind?: 'shell' | 'channel'; invalid?: string; pending?: boolean} = {}) {
  const requests: {path: string; signal?: AbortSignal}[] = [], navigations: {kind: string; route: any}[] = [];
  const playbackListeners = new Set<() => void>(), routeListeners = new Set<() => void>(), authorityListeners = new Set<() => void>();
  let active = true, token = 1, changedNeighbor = false, deferred: {promise: Promise<void>; resolve: () => void} | undefined;
  if (pending) {let resolve!: () => void;const promise = new Promise<void>(done => {resolve = done;});deferred = {promise, resolve};}
  const rawRows = Array.from({length: 5}, (_, index) => ({id: `channel-${index}`, kind: 'source', number: String(200 + index), name: `Channel ${index}`, favorite: false, hidden: false}));
  const asRow = (raw: any) => legacyGuide.legacyChannelRow(liveGuideChannel(nextChannelRow(raw))!);
  const options = {kind: 'live-source', sourceId: 'server', timezone: 'UTC', group: 'News', favorites: true, sort: 'name'};
  let route: any = {authority, revision: 1, route: routeKind === 'channel' ? {kind: 'channel', sourceId: 'server', channelId: 'channel-2'} : {kind: 'shell', destination: {kind: 'live'}}};
  const channel = {kind: 'live-source', sourceId: 'server', channelId: 'channel-2', generation: '1'};
  let source: any = {authority, fence, dock: {local: {expanded: true}}, playback: {session: {id: 'session', generation: 1}, channel, linear: {channel}}, ...(origin ? {channelGuide: {row: invalid === 'generation' ? legacyGuide.legacyChannelRow({...liveGuideChannel(nextChannelRow(rawRows[2] as never))!, generation: 'old'}) : asRow(rawRows[2]), options}} : {})};
  const context: any = {current: () => active, authority: () => authority,
    session: () => ({sessionFamilyId: 'family', tokenGeneration: token, authorizationHorizon: 'horizon', deviceId: 'device', installationId: 'installation'}),
    playback: {subscribe: (listener: () => void) => {playbackListeners.add(listener);return () => playbackListeners.delete(listener);}, getSnapshot: () => source},
    navigation: {subscribe: (listener: () => void) => {routeListeners.add(listener);return () => routeListeners.delete(listener);}, getSnapshot: () => route,
      open: (next: any) => {navigations.push({kind: 'open', route: next});route = {...route, revision: route.revision + 1, route: next};for (const listener of routeListeners) listener();return true;},
      replaceChannel: (expected: unknown, next: any) => {assert.equal(expected, route);navigations.push({kind: 'replace', route: next});return true;}},
    subscribeAuthority: (listener: () => void) => {authorityListeners.add(listener);return () => authorityListeners.delete(listener);},
    api: {request: async (path: string, _method: string, _body: unknown, signal?: AbortSignal) => {
      requests.push({path, signal});
      if (!path.startsWith('/v1/live/channels?') && !path.startsWith('/v1/live/guide?')) throw new Error('Unexpected read '+path);
      if (deferred) await deferred.promise;
      if (invalid === 'denied') throw Object.assign(new Error('not permitted'), {status: 403});
      const rows = rawRows.map((row, index) => ({...row, ...(changedNeighbor && index === 3 ? {number: 'changed'} : {}),
        ...(index === 2 && invalid === 'identity' ? {id: 'different'} : index === 2 && invalid === 'kind' ? {kind: 'library'} : index === 2 && invalid === 'hidden' ? {hidden: true} : {})}));
      if (path.startsWith('/v1/live/channels?')) return {items: rows};
      const q = new URL(path, 'https://test').searchParams;
      assert.equal(Date.parse(q.get('to')!) - Date.parse(q.get('from')!), 3 * 3600_000);
      return {items: rows.map(channel => ({channel, programmes: [{id: 'programme-'+channel.id, title: 'On now', startsAt: q.get('from'), endsAt: q.get('to')}]}))};
    }},
  };
  const h = hooks(), {NativeTVLiveGuide} = await screenModule(new URL('../src-next/screens/watch/live-guide.ts', import.meta.url), {
    react: h.react, '../../../../../packages/player-core/src': {sameFence}, '../../shell/model': {sameAuthority},
    '../../../../../packages/client-core/src/guide/player-guide': playerGuide,
    '../../../../../packages/client-core/src/guide/legacy-source': legacyGuide,
  });
  const guide = new NativeTVLiveGuide(context, touch);
  return {guide, requests, navigations, rawRows, options, context, source: () => source,
    changeNeighbor: () => {changedNeighbor = true;},
    retire: (reason: string) => {if (reason === 'authority') {token++;for (const listener of authorityListeners) listener();}
      else if (reason === 'route') {route = {...route, revision: route.revision + 1};for (const listener of routeListeners) listener();}
      else {source = {...source, fence: {...fence, generation: 2}, playback: {...source.playback, session: {id: 'session', generation: 2}}};for (const listener of playbackListeners) listener();}},
    release: () => deferred?.resolve(),
    dispose: () => {active = false;guide.dispose();},
  };
}

test('touch guide inherits the authorized original query after its launch route has returned', async () => {
  const f = await fixture();try {
    await settle();const state = f.guide.getSnapshot();assert.equal(state.phase, 'ready');assert.equal(state.rows.length, 5);
    assert.equal(state.previous.channel.id, 'channel-1');assert.equal(state.next.channel.id, 'channel-3');
    assert.equal(f.requests.length, 1);assert.equal(f.requests[0].path.startsWith('/v1/live/channels?'), true);
    const query = new URL(f.requests[0].path, 'https://test').searchParams;
    assert.equal(query.get('around'), 'channel-2');assert.equal(query.get('group'), 'News');assert.equal(query.get('favorites'), 'true');assert.equal(query.get('sort'), 'name');
    assert.deepEqual(f.guide.captureSource().origin.options, f.options);
    await f.guide.tune(state.next, state);assert.equal(f.navigations[0]?.kind, 'open');assert.equal(f.navigations[0]?.route.channelId, 'channel-3');assert.equal(f.navigations[0]?.route.tune.fence, fence);
    const before = f.navigations.length;await f.guide.tune(state.next, state);assert.equal(f.navigations.length, before, 'an old button cannot tune a successor route');
  } finally {f.dispose();}
});

test('Home/direct starts read bounded channels and programmes on the rebuilt routes', async () => {
  const f = await fixture({origin: false});try {
    await settle();const state = f.guide.getSnapshot();assert.equal(state.phase, 'ready');assert.equal(state.rows.length, 5);
    assert.equal(f.requests.length, 1);
    const query = new URL(f.requests[0]!.path, 'https://test').searchParams;assert.equal(query.get('around'), 'channel-2');
    assert.equal(f.guide.captureSource().origin.row.native.id, fence.channelId);
    assert.equal(f.guide.open(), true);await settle();
    assert.equal(f.requests.filter(row => row.path.startsWith('/v1/live/guide?')).length, 1);assert.ok(f.guide.getSnapshot().rows.every((row: any) => row.programmes.length === 1));
  } finally {f.dispose();}
});

for (const invalid of ['identity', 'kind', 'denied', 'hidden']) test(`Home current-channel ${invalid} mismatch never authorizes a lineup`, async () => {
  const f = await fixture({origin: false, invalid});try {
    await settle();assert.equal(f.guide.getSnapshot().phase, 'unavailable');assert.equal(f.guide.getSnapshot().rows.length, 0);assert.ok(f.guide.getSnapshot().error);
    assert.equal(f.requests.length, 1);
  } finally {f.dispose();}
});

for (const reason of ['route', 'authority', 'fence']) test(`retained channel and Guide callbacks refuse after ${reason} replacement`, async () => {
  const f = await fixture();try {
    await settle();const state = f.guide.getSnapshot(), open = f.guide.guideAction();f.retire(reason);await settle();
    await f.guide.tune(state.next, state);open();assert.equal(f.navigations.length, 0);
  } finally {f.dispose();}
});

test('channel tune revalidates the neighbor before dispatch', async () => {
  const f = await fixture();try {
    await settle();const state = f.guide.getSnapshot(), errors: unknown[] = [];
    f.changeNeighbor();
    await f.guide.tune(state.next, state, (error: unknown) => errors.push(error));assert.equal(f.navigations.length, 0);assert.equal(errors.length, 1);assert.ok(f.guide.getSnapshot().error);
  } finally {f.dispose();}
});

// Step 7: the tune's revalidation is a read, so a moved viewer fence on it is a newer answer and the tune goes on.
test('channel tune revalidates on the viewer read without requiring legacy document fences', async () => {
  const f = await fixture();try {
    await settle();const state = f.guide.getSnapshot(), errors: unknown[] = [];
    await f.guide.tune(state.next, state, (error: unknown) => errors.push(error));assert.equal(f.navigations.length, 1);assert.equal(errors.length, 0);
  } finally {f.dispose();}
});

test('TV retains its channel-route requirement and exact replace-channel dispatch', async () => {
  const noRoute = await fixture({touch: false});try {await settle();assert.equal(noRoute.guide.getSnapshot().phase, 'unavailable');assert.equal(noRoute.requests.length, 0);} finally {noRoute.dispose();}
  const f = await fixture({touch: false, routeKind: 'channel'});try {await settle();const state = f.guide.getSnapshot();await f.guide.tune(state.next, state);assert.equal(f.navigations[0]?.kind, 'replace');} finally {f.dispose();}
});

test('disposing a pending current-channel read aborts it and prevents late neighbor work', async () => {
  const f = await fixture({origin: false, pending: true});await settle();
  const pending = f.requests.find(row => row.path.startsWith('/v1/live/channels?'));assert.ok(pending);f.dispose();assert.equal(pending.signal?.aborted, true);
  f.release();await settle();assert.equal(f.requests.length, 1);assert.equal(f.guide.getSnapshot().phase, 'loading');
});


test('a retained origin from an old channel generation cannot open the new player guide', async () => {
 const f=await fixture({invalid:'generation'});try{await settle();assert.equal(f.guide.getSnapshot().phase,'unavailable');assert.equal(f.requests.length,0);}finally{f.dispose();}
});
