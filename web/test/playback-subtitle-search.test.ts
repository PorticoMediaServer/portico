import test from 'node:test';
import assert from 'node:assert/strict';
import {playbackFence, queueScope} from '../../packages/player-core/src/index.ts';
import {WebShellPlaybackRuntime} from '../src/platform/playback/runtime.ts';

const scope = queueScope({serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile',
  controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'});
const fence = playbackFence({kind: 'queue', playback: {scope, run: 1}, itemId: 'film', sourceId: 'version',
  entryId: 'entry', sessionId: 'session', generation: 1});
const results = (ref = 'offered') => ({items: [{ref, language: 'en', release: ref}], credit: 'OpenSubtitles.com'});
function deferred() {
  let resolve!: (value: unknown) => void;
  const promise = new Promise<unknown>(done => {resolve = done;});
  return {promise, resolve};
}
function fixture(request: (path: string, method?: string, body?: unknown) => Promise<unknown>) {
  // Exercise the runtime commands with their existing transport/offer/selection boundaries only.
  const runtime = Object.create(WebShellPlaybackRuntime.prototype) as any;
  const events: unknown[][] = [];
  let active = true, moved = false;
  const stack = {life: new AbortController(), offers: {refresh: async () => {events.push(['refresh']);}}};
  Object.assign(runtime, {stack, source: {api: {request}}, snapshot: {offers: {subtitlePlan: {}}},
    current: () => active, matches: () => active && !moved, require: () => {assert.ok(active); return stack;}, sync: () => {},
    selectSubtitle: async (_fence: unknown, id: string) => {events.push(['select', id]);}});
  return {runtime: runtime as WebShellPlaybackRuntime, events, stack, retire: () => {active = false;}, move: () => {moved = true;}, back: () => {moved = false;}};
}

test('an offered subtitle downloads for the playing version, refreshes options, then turns on', async () => {
  const calls: unknown[][] = [];
  const f = fixture(async (path, method, body) => {
    calls.push([path, method, body]);
    return method === 'GET' ? results() : {id: 'downloaded'};
  });
  await f.runtime.searchSubtitles(fence, 'en');
  assert.equal(f.runtime.subtitleSearch(fence)?.candidates[0]?.attribution, 'OpenSubtitles.com');
  await assert.rejects(f.runtime.downloadSubtitle(fence, 'invented'));
  assert.equal(calls.length, 1, 'a result not offered sends nothing');
  await f.runtime.downloadSubtitle(fence, 'offered');
  assert.deepEqual(calls, [['/v1/items/film/subtitles/search?languages=en', 'GET', undefined],
    ['/v1/items/film/subtitles', 'POST', {versionId: 'version', ref: 'offered'}]]);
  assert.deepEqual(f.events, [['refresh'], ['select', 'downloaded']]);
  assert.equal(f.runtime.subtitleSearch(fence)?.busy, false);
});

test('an older search cannot replace newer results, and a retired download cannot select a track', async () => {
  const older = deferred(), download = deferred();
  let searches = 0;
  const f = fixture(async (_path, method) => method === 'POST' ? download.promise : ++searches === 1 ? older.promise : results('newer'));
  const first = f.runtime.searchSubtitles(fence, 'en');
  await f.runtime.searchSubtitles(fence, 'fr');
  older.resolve(results('older')); await first;
  assert.equal(f.runtime.subtitleSearch(fence)?.candidates[0]?.id, 'newer');
  const choosing = f.runtime.downloadSubtitle(fence, 'newer');
  f.retire(); download.resolve({id: 'late'}); await choosing;
  assert.deepEqual(f.events, []);
  assert.equal(f.runtime.subtitleSearch(fence), undefined);
});

test('provider refusal and an options-refresh failure finish without leaving the search busy', async () => {
  const off = fixture(async () => {throw {code: 'subtitle_search_not_set_up'};});
  await off.runtime.searchSubtitles(fence, 'en');
  assert.equal(off.runtime.subtitleSearch(fence)?.off, true);
  assert.equal(off.runtime.subtitleSearch(fence)?.busy, false);
  const f = fixture(async (_path, method) => method === 'GET' ? results() : {id: 'downloaded'});
  await f.runtime.searchSubtitles(fence, 'en');
  f.stack.offers.refresh = async () => {throw new Error('offline');};
  await f.runtime.downloadSubtitle(fence, 'offered');
  const state = f.runtime.subtitleSearch(fence);
  assert.equal(state?.busy, false);
  assert.equal(state?.canRetry, true);
  assert.equal(state?.candidates[0]?.id, 'offered');
  assert.deepEqual(f.events, []);
});

test('a playback that moves on during a download leaves the results to choose again, not a search stuck busy', async () => {
  const download = deferred();
  const f = fixture(async (_path, method) => method === 'GET' ? results() : download.promise);
  await f.runtime.searchSubtitles(fence, 'en');
  const choosing = f.runtime.downloadSubtitle(fence, 'offered');
  f.move(); download.resolve({id: 'downloaded'}); await choosing;
  f.back();
  const state = f.runtime.subtitleSearch(fence);
  assert.equal(state?.busy, false);
  assert.equal(state?.candidates[0]?.id, 'offered');
  assert.deepEqual(f.events, []);
});
