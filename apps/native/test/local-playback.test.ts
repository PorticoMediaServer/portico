/** OS-07: a download plays in Portico's own player with a session that never leaves the device. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackService, type PlaybackApi} from '../../../packages/client-core/src/index.ts';
import {LocalPlayback, isLocalSession, type LocalMedia, type LocalProgress} from '../platform/local-playback.ts';

const tick = () => new Promise(r => setImmediate(r));
const media: LocalMedia = {entryId: 'dl1', itemId: 'movie1', playId: 'play-1', fileUrl: 'file:///var/app/Library/Application%20Support/Downloads/dl1/movie.mp4', durationSeconds: 600, startSeconds: 42};

function setup(journal?: {entries: unknown[]}) {
  const server: string[] = [];
  const progress: LocalProgress[] = [];
  const inner: PlaybackApi = {
    createPlayback: async itemId => { server.push(`create ${itemId}`); return {id: 'srv1', generation: 1, streamUrl: '/media', mode: 'direct', duration: 100, resumeSeconds: 0}; },
    progressPlayback: async id => { server.push(`progress ${id}`); },
    stopPlayback: async id => { server.push(`stop ${id}`); },
    reportPlayerFailure: async id => { server.push(`failure ${id}`); return {escalates: true}; },
    markerSkipped: async id => { server.push(`skip ${id}`); },
  };
  const local = new LocalPlayback(p => progress.push(p));
  const store = journal ?? {entries: []};
  // As in the app: server plays require the device queue and server recovery
  // journal; actual local progress belongs to the explicit app-local owner.
  const api = local.wrap(inner);
  const service = new PlaybackService(api, () => 'r' + Math.random(), {
    queueRequired: true,
    seekTimeoutMs: 1000,
    journal: {load: async () => store.entries as never, save: async entries => { store.entries = entries as unknown[]; }},
  });
  return {service, local, api, server, progress, store};
}

async function playing(x: ReturnType<typeof setup>, at = 50) {
  await x.service.playLocal(media.itemId, x.local.session(media), media.startSeconds);
  const s = x.service.getSnapshot();
  x.service.ready(s.intentId);
  x.service.seekApplied(s.intentId, s.pendingSeek!.revision, media.startSeconds, 'playing');
  x.service.fact(x.service.getSnapshot().intentId, at, 'playing');
}

test('a download plays from its file where the viewer left off, with no queue and no server call', async () => {
  const x = setup();
  await x.service.playLocal(media.itemId, x.local.session(media), media.startSeconds);
  const s = x.service.getSnapshot();
  assert.equal(s.phase, 'starting');
  assert.ok(isLocalSession(s.session?.id));
  assert.equal(s.session?.streamUrl, media.fileUrl);
  assert.equal(s.duration, 600);
  assert.equal(s.pendingSeek?.positionSeconds, 42);
  assert.equal(s.error, undefined, 'the device queue is not needed');
  assert.deepEqual(x.server, []);
});

test('explicit local custody projects the actual journal handlers without a server fallback', async () => {
  const x = setup(), session = x.local.session(media)();
  const payload = {generation: session.generation, sequence: 1, positionSeconds: 57, state: 'paused' as const};
  await x.api.localPlayback.progressPlayback(session.id, payload);
  await x.api.localPlayback.stopPlayback(session.id);
  assert.equal(x.progress.length, 1); assert.equal(x.progress[0]!.playId, media.playId);
  assert.equal(x.progress[0]!.positionSeconds, 57);
  for (const id of ['unowned-session', 'local:unowned:9']) {
    await x.api.localPlayback.progressPlayback(id, payload);
    await x.api.localPlayback.stopPlayback(id);
  }
  assert.equal(x.progress.length, 1); assert.deepEqual(x.server, []);
  await x.api.progressPlayback('server-session', payload); await x.api.stopPlayback('server-session');
  assert.deepEqual(x.server, ['progress server-session', 'stop server-session']);
  x.service.leave();
});

test('progress goes to the offline journal, and leaving stops nothing on the server', async () => {
  const x = setup();
  await playing(x, 50);
  x.service.pause();
  await tick();
  x.service.leave();
  await tick(); await tick();
  assert.deepEqual(x.server, []);
  assert.ok(x.progress.length > 0);
  const last = x.progress.at(-1)!;
  assert.equal(last.entryId, 'dl1');
  assert.equal(last.itemId, 'movie1');
  assert.equal(last.playId, 'play-1', 'the play keeps the id its local session was made with');
  assert.equal(last.durationSeconds, 600);
  assert.equal(last.ended, false);
});

test('the end is recorded as ended', async () => {
  const x = setup();
  await playing(x, 590);
  x.service.fact(x.service.getSnapshot().intentId, 600, 'ended');
  await tick(); await tick();
  assert.equal(x.progress.at(-1)?.state, 'ended');
  assert.equal(x.progress.at(-1)?.ended, true);
  assert.deepEqual(x.server, []);
});

test('a retry after an error plays the file again as a new local session, never the server', async () => {
  const x = setup();
  await playing(x, 120);
  const first = x.service.getSnapshot().session!.id;
  x.service.fail(x.service.getSnapshot().intentId, 'The file could not be read.');
  assert.equal(x.service.getSnapshot().phase, 'error');
  await x.service.retry();
  await tick();
  const s = x.service.getSnapshot();
  assert.ok(isLocalSession(s.session?.id));
  assert.notEqual(s.session?.id, first);
  assert.equal(s.pendingSeek?.positionSeconds, 120, 'from where it stopped');
  assert.ok(!x.server.some(line => line.startsWith('create') || line.startsWith('stop')), x.server.join());
});

test('an engine failure on a local file fails at once (no route escalation to report)', async () => {
  const x = setup();
  await playing(x);
  x.service.engineFailed(x.service.getSnapshot().intentId, 'decode_error', '', 'This media could not be played.');
  await tick();
  assert.equal(x.service.getSnapshot().phase, 'error');
  assert.deepEqual(x.server, []);
});

test('a server recovery record retains server custody even when its ID looks local', async () => {
  const store = {entries: [{id: 'local:dl9:3', checkpoint: {generation: 1, sequence: 4, positionSeconds: 77, state: 'paused'}}]};
  const x = setup(store);
  await x.service.startRecovery();
  await tick(); await tick(); await tick();
  assert.deepEqual(x.server, ['progress local:dl9:3', 'stop local:dl9:3']);
  assert.deepEqual(x.progress, [], 'spelling cannot claim an app-local journal owner');
});

test('genuine local progress never enters the server recovery journal', async () => {
  const x = setup(); await playing(x, 77); x.service.pause();
  await tick(); await tick();
  assert.ok(x.progress.length > 0); assert.deepEqual(x.store.entries, []);
  x.service.leave(); await tick(); await tick(); assert.deepEqual(x.server, []);
});

test('base transport methods always preserve server custody, including an ID known to the local owner', async () => {
  const x = setup(), session = x.local.session(media)();
  const payload = {generation: 1, sequence: 1, positionSeconds: 17, state: 'paused' as const};
  await x.api.progressPlayback(session.id, payload); await x.api.stopPlayback(session.id);
  await x.api.reportPlayerFailure?.(session.id, 'decode_error', '', 17);
  await x.api.markerSkipped?.(session.id, {markerId: 'marker', mode: 'manual', positionMs: 17000});
  assert.deepEqual(x.server, [`progress ${session.id}`, `stop ${session.id}`, `failure ${session.id}`, `skip ${session.id}`]);
  assert.deepEqual(x.progress, []); x.service.leave();
});

test('a play from the server after a download still goes to the server', async () => {
  const x = setup();
  await playing(x);
  const wrapped = x.local.wrap({
    createPlayback: async () => ({id: 'srv2', generation: 1, streamUrl: '/m', mode: 'direct', duration: 1, resumeSeconds: 0}),
    progressPlayback: async () => {}, stopPlayback: async () => {},
  });
  const session = await wrapped.createPlayback('movie1', 'req');
  assert.equal(session.id, 'srv2');
});

test('only a file URL can be a local session', () => {
  const local = new LocalPlayback(() => {});
  assert.throws(() => local.session({...media, fileUrl: 'https://example.com/movie.mp4'}));
});
