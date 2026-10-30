import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {playbackFence, sameFence} from '../../../packages/player-core/src/identity.ts';
import {sameAuthority} from '../src-next/shell/model.ts';

const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'family', viewerFence: 'viewer'};
const scope = {serverId: 'server', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane', authority: 'local'} as const;
const fence = playbackFence({kind: 'channel', playback: {scope, run: 1}, sessionId: 'session', channelKind: 'live-source', sourceId: 'source', channelId: 'channel-2', channelGeneration: 'generation', generation: 1});
const settle = () => new Promise<void>(done => setImmediate(done));

async function fixture({touch = true, origin = true, routeKind = 'shell', invalid = ''}: {touch?: boolean; origin?: boolean; routeKind?: 'shell' | 'channel'; invalid?: string} = {}) {
  const requests: {path: string; signal?: AbortSignal}[] = [], navigations: {kind: string; route: any}[] = [];
  const playbackListeners = new Set<() => void>(), routeListeners = new Set<() => void>(), authorityListeners = new Set<() => void>();
  let active = true, viewerFence = 'viewer', token = 1, changedNeighbor = false, deferred: {promise: Promise<void>; resolve: () => void} | undefined;
  const rawRows = Array.from({length: 5}, (_, index) => ({id: `channel-${index}`, sourceId: 'source', provenance: 'live-source', generation: 'generation', number: String(200 + index), name: `Channel ${index}`, tuneAvailable: true, hidden: false, programmes: []}));
  const asRow = (raw: any) => ({id: raw.id, sourceId: raw.sourceId, kind: 'live', name: raw.name, number: raw.number, tuneAvailable: raw.tuneAvailable, native: raw});
  const options = {kind: 'live-source', sourceId: 'source', timezone: 'UTC', search: 'saved-filter', sort: 'number'};
  let route: any = {authority, revision: 1, route: routeKind === 'channel' ? {kind: 'channel', sourceId: 'source', channelId: 'channel-2'} : {kind: 'shell', destination: {kind: 'live'}}};
  const channel = {kind: 'live-source', sourceId: 'source', channelId: 'channel-2', generation: 'generation'};
  let source: any = {authority, fence, dock: {local: {expanded: true}}, playback: {session: {id: 'session', generation: 1}, channel, linear: {channel}}, ...(origin ? {channelGuide: {row: asRow(rawRows[2]), options}} : {})};
  const context: any = {current: () => active, authority: () => authority,
    session: () => ({sessionFamilyId: 'family', tokenGeneration: token, authorizationHorizon: 'horizon', deviceId: 'device', installationId: 'installation'}),
    playback: {subscribe: (listener: () => void) => {playbackListeners.add(listener);return () => playbackListeners.delete(listener);}, getSnapshot: () => source},
    navigation: {subscribe: (listener: () => void) => {routeListeners.add(listener);return () => routeListeners.delete(listener);}, getSnapshot: () => route,
      open: (next: any) => {navigations.push({kind: 'open', route: next});route = {...route, revision: route.revision + 1, route: next};for (const listener of routeListeners) listener();return true;},
      replaceChannel: (expected: unknown, next: any) => {assert.equal(expected, route);navigations.push({kind: 'replace', route: next});return true;}},
    subscribeAuthority: (listener: () => void) => {authorityListeners.add(listener);return () => authorityListeners.delete(listener);},
    api: {request: async (path: string, _method: string, _body: unknown, signal?: AbortSignal) => {
      requests.push({path, signal});
      if (path.startsWith('/v1/guide?')) {
        if (deferred) await deferred.promise;
        const raw = {...rawRows[2], ...(invalid === 'identity' ? {id: 'different'} : invalid === 'generation' ? {generation: 'old'} : invalid === 'denied' ? {tuneAvailable: false} : invalid === 'hidden' ? {hidden: true} : {})};
        return {viewerFence: invalid === 'viewer' ? 'other-viewer' : viewerFence, channels: [raw]};
      }
      return {};
    }},
  };
  const h = hooks(), {NativeTVLiveGuide} = await screenModule(new URL('../src-next/screens/watch/live-guide.ts', import.meta.url), {
    react: h.react, '../../../../../packages/player-core/src': {sameFence}, '../../shell/model': {sameAuthority}, '../../../../../packages/contracts/src/routes': {routePath: () => '/v1/guide'},
    '../../../../../packages/client-core/src/channel-guide': {parseChannelGuide: (value: any) => value},
    '../../../../../packages/client-core/src/guide/directory': {fetchGuideSourceSummaries: async (api: any, _server: string, _zone: string, signal: AbortSignal) => {await api.request('/sources', 'GET', undefined, signal);return {viewerFence, sources: [{id: 'source', provenance: 'live-source', generation: 'generation'}]};}},
    '../../../../../packages/client-core/src/guide/legacy-source': {legacyChannel: (row: any) => row.native, legacyChannelRow: asRow, legacyGuideSource: (api: any, _server: string, query: any) => ({
      neighbor: async (row: any, direction: number, signal: AbortSignal) => {
        await api.request(`/neighbor?anchor=${row.id}&direction=${direction}&sort=${query.sort ?? 'server'}`, 'GET', undefined, signal);
        const index = rawRows.findIndex(value => value.id === row.id), raw = rawRows[(index + direction + rawRows.length) % rawRows.length]!;
        return asRow(changedNeighbor ? {...raw, number: 'changed'} : raw);
      },
      programs: async (ids: string[], start: number, end: number, signal: AbortSignal) => {
        assert.equal(ids.length, 1);assert.equal(end - start, 3 * 3600_000);
        await api.request(`/programmes?channel=${ids[0]}`, 'GET', undefined, signal);
        return {[ids[0]!]: [{id: `programme-${ids[0]}`, channelId: ids[0], title: 'On now', start, end}]};
      },
    })},
  });
  const guide = new NativeTVLiveGuide(context, touch);
  return {guide, requests, navigations, rawRows, options, context, source: () => source,
    changeViewer: () => {viewerFence = 'new-viewer';}, changeNeighbor: () => {changedNeighbor = true;},
    retire: (reason: string) => {if (reason === 'authority') {token++;for (const listener of authorityListeners) listener();}
      else if (reason === 'route') {route = {...route, revision: route.revision + 1};for (const listener of routeListeners) listener();}
      else {source = {...source, fence: {...fence, generation: 2}, playback: {...source.playback, session: {id: 'session', generation: 2}}};for (const listener of playbackListeners) listener();}},
    defer: () => {let resolve!: () => void;const promise = new Promise<void>(done => {resolve = done;});deferred = {promise, resolve};return resolve;},
    dispose: () => {active = false;guide.dispose();},
  };
}

test('touch guide inherits the authorized original query after its launch route has returned', async () => {
  const f = await fixture();try {
    await settle();const state = f.guide.getSnapshot();assert.equal(state.phase, 'ready');assert.equal(state.rows.length, 5);
    assert.equal(state.previous.channel.id, 'channel-1');assert.equal(state.next.channel.id, 'channel-3');
    assert.equal(f.requests.filter(row => row.path.startsWith('/neighbor')).length, 4);
    assert.equal(f.requests.some(row => row.path.startsWith('/v1/guide?')), false);
    assert.deepEqual(f.guide.captureSource().origin.options, f.options);
    await f.guide.tune(state.next, state);assert.equal(f.navigations[0]?.kind, 'open');assert.equal(f.navigations[0]?.route.channelId, 'channel-3');assert.equal(f.navigations[0]?.route.tune.fence, fence);
    const before = f.navigations.length;await f.guide.tune(state.next, state);assert.equal(f.navigations.length, before, 'an old button cannot tune a successor route');
  } finally {f.dispose();}
});

test('Home/direct starts read exactly the accepted current channel before bounded neighbors', async () => {
  const f = await fixture({origin: false});try {
    await settle();const state = f.guide.getSnapshot();assert.equal(state.phase, 'ready');assert.equal(state.rows.length, 5);
    const read = f.requests.filter(row => row.path.startsWith('/v1/guide?'));assert.equal(read.length, 1);
    const query = new URL(`http://test${read[0]!.path}`).searchParams;assert.equal(query.get('channels'), 'channel-2');assert.equal(query.get('sourceId'), 'source');assert.equal(query.get('limit'), '1');assert.equal(query.get('cursor'), '');
    assert.equal(f.requests.filter(row => row.path.startsWith('/neighbor')).length, 4);
    assert.equal(f.guide.captureSource().origin.row.native.id, fence.channelId);
    assert.equal(f.guide.open(), true);await settle();
    assert.equal(f.requests.filter(row => row.path.startsWith('/programmes')).length, 5);assert.ok(f.guide.getSnapshot().rows.every((row: any) => row.programmes.length === 1));
  } finally {f.dispose();}
});

for (const invalid of ['identity', 'generation', 'viewer', 'denied', 'hidden']) test(`Home current-channel ${invalid} mismatch never authorizes a lineup`, async () => {
  const f = await fixture({origin: false, invalid});try {
    await settle();assert.equal(f.guide.getSnapshot().phase, 'unavailable');assert.equal(f.guide.getSnapshot().rows.length, 0);assert.ok(f.guide.getSnapshot().error);
    assert.equal(f.requests.some(row => row.path.startsWith('/neighbor')), false);
  } finally {f.dispose();}
});

for (const reason of ['route', 'authority', 'fence']) test(`retained channel and Guide callbacks refuse after ${reason} replacement`, async () => {
  const f = await fixture();try {
    await settle();const state = f.guide.getSnapshot(), open = f.guide.guideAction();f.retire(reason);await settle();
    await f.guide.tune(state.next, state);open();assert.equal(f.navigations.length, 0);
  } finally {f.dispose();}
});

for (const changed of ['neighbor', 'viewer']) test(`channel tune revalidates ${changed} before dispatch`, async () => {
  const f = await fixture();try {
    await settle();const state = f.guide.getSnapshot(), errors: unknown[] = [];
    if (changed === 'neighbor') f.changeNeighbor();else f.changeViewer();
    await f.guide.tune(state.next, state, (error: unknown) => errors.push(error));assert.equal(f.navigations.length, 0);assert.equal(errors.length, 1);assert.ok(f.guide.getSnapshot().error);
  } finally {f.dispose();}
});

test('TV retains its channel-route requirement and exact replace-channel dispatch', async () => {
  const noRoute = await fixture({touch: false});try {await settle();assert.equal(noRoute.guide.getSnapshot().phase, 'unavailable');assert.equal(noRoute.requests.length, 0);} finally {noRoute.dispose();}
  const f = await fixture({touch: false, routeKind: 'channel'});try {await settle();const state = f.guide.getSnapshot();await f.guide.tune(state.next, state);assert.equal(f.navigations[0]?.kind, 'replace');} finally {f.dispose();}
});

test('disposing a pending current-channel read aborts it and prevents late neighbor work', async () => {
  const f = await fixture({origin: false});const resolve = f.defer();await settle();
  const pending = f.requests.find(row => row.path.startsWith('/v1/guide?'));assert.ok(pending);f.dispose();assert.equal(pending.signal?.aborted, true);
  resolve();await settle();assert.equal(f.requests.some(row => row.path.startsWith('/neighbor')), false);
});
