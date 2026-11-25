import test from 'node:test';
import assert from 'node:assert/strict';
import {FakePlaybackServer} from '../../../packages/client-core/src/playback-v1/testing/fake-server.ts';
import {NativePlaybackRuntime} from '../src-next/platform/playback/runtime.ts';
import type {NativePlaybackTransport} from '../src-next/platform/playback/NativePlaybackCoordinator.ts';

// #2945: "Play at the end of a film plays it again from its start" — once playback reaches
// `ended`, capabilities.play is offered and toggle(fence) replays the title from 0 through replay().

const viewer = {authority: 'local' as const, serverId: 'srv_1', accountId: 'acc_1', profileId: 'prof_1', role: 'member' as const};
const authority = {serverId: 'srv_1', accountId: 'acc_1', profileId: 'prof_1', epoch: '["fam_1",false]', viewerFence: 'vf_1', privacyFence: 'pf_1'};

function playbackHarness() {
  const backend = new FakePlaybackServer({viewer, deviceId: 'dev_1', reportEveryMs: 1000});
  const factListeners = new Set<(fact: any) => void>();
  const commands: any[] = [];
  let leases = 0, reqId = 0, factSeq = 0, lastCommandRevision = 0, currentBinding: any = null, currentInstance = '';

  const transport: NativePlaybackTransport = {
    acquire: async () => {
      const id = ++leases;
      currentInstance = `inst_${id}`;
      factSeq = 0;
      return {lease: `lease_${id}`, instanceId: currentInstance};
    },
    command: async cmd => {
      commands.push(cmd);
      if (cmd.binding) currentBinding = cmd.binding;
      if (typeof cmd.commandRevision === 'number') lastCommandRevision = cmd.commandRevision;
    },
    release: async () => {},
    subscribe: listener => {factListeners.add(listener); return () => {factListeners.delete(listener);};},
  };

  const emitFact = (fact: Record<string, unknown>) => {
    const payload = {instanceId: currentInstance, binding: currentBinding, factSequence: ++factSeq, lastCommandRevision, ...fact};
    for (const listener of [...factListeners]) listener(payload);
  };

  let queues = 0;
  const api: any = {
    baseUrl: 'https://server.invalid',
    requestRaw: async (path: string, method = 'GET', opts: {headers?: Record<string, string>; body?: any} = {}) => {
      if (path === '/v1/capabilities') return {status: 200, headers: {}, body: {features: {playback_v1_kinds: 'movie,episode,song,audiobook_file'}}};
      if (method === 'POST' && path === '/v1/queues') {
        const items: string[] = opts.body?.source?.items ?? ['movie_1'];
        const created = await backend.handle({
          method: 'POST',
          path: '/v1/queues',
          headers: opts.headers ?? {},
          body: {segments: [{source: {items: {ids: items}}}]},
        } as any);
        const q = (created.body as any)?.queue;
        const rawWin = (created.body as any)?.window;
        const win: any[] = Array.isArray(rawWin) ? rawWin : Array.isArray(rawWin?.entries) ? rawWin.entries : Array.isArray(rawWin?.items) ? rawWin.items : [];
        const entries = (win.length ? win : items.map((itemId, position) => ({entryId: `${q.id}_e${position}`, position, itemId}))).map((e: any, position: number) => ({
          entryId: e.entryId ?? e.id,
          position: e.position ?? position,
          itemId: e.itemId ?? items[position] ?? 'movie_1',
          kind: 'movie',
          title: 'Test Film',
          available: true,
          durationMs: 180_000,
        }));
        return {
          status: 201,
          headers: {},
          body: {
            queue: {id: q.id, revision: String(q.revision ?? '1'), total: entries.length, medium: 'video', current: {entryId: entries[0]!.entryId, position: 0}, repeat: 'off', shuffle: false},
            window: entries,
          },
        };
      }
      const res = await backend.handle({method, path, body: opts.body, headers: opts.headers ?? {}} as any);
      return {status: res.status, headers: res.headers ?? {}, body: res.body};
    },
    request: async (path: string, method = 'GET', body?: unknown) => {
      const res = await api.requestRaw(path, method, {body});
      if (res.status >= 400) throw Object.assign(new Error(res.body?.error?.message ?? 'http_error'), {status: res.status, code: res.body?.error?.code});
      return res.body;
    },
    mediaUrl: (p: string) => `https://server.invalid${p}`,
    getRouteConnection: () => undefined,
    getRouteSnapshot: () => ({phase: 'ready'}),
    getCertificateSessionEpoch: () => 0,
    recoverRoute: async () => {},
    subscribeRoute: () => () => {},
  };

  const runtime = new NativePlaybackRuntime({
    api,
    authority: () => authority,
    session: () => ({accessToken: 'a', installationId: 'dev_1', expiresAt: '2099-01-01T00:00:00Z', sessionFamilyId: 'fam_1', tokenGeneration: '1', authorizationHorizon: '2099-01-02T00:00:00Z', viewer}),
    subscribeAuthority: () => () => {},
    events: {on: () => () => {}, onResync: () => () => {}, subscribe: () => () => {}, prime: async () => {}} as any,
    settings: () => ({engine: 'native', videoRate: 1, audiobookRate: 1, skips: {intro: 'off', recap: 'off', credits: 'off'}, subtitles: {size: 'medium', background: 'translucent', color: 'white', position: 'bottom', font: 'default', keepStyling: true, useSystemCaptionStyle: false}, autoPictureInPicture: false}),
    subscribeSettings: () => () => {},
    requestId: () => `req_${++reqId}`,
    transport,
    engines: {initial: 'native', transport: () => transport, choose: () => 'native'},
    engineFeatures: () => ({airPlayVideo: false, subtitleRendering: 'native'} as any),
    capabilitiesReady: async () => {},
    disposeCapabilities: () => {},
  });

  return {runtime, commands, emitFact};
}

test('Play at the end of a film is offered and toggle() replays the title from its start', async () => {
  const {runtime, commands, emitFact} = playbackHarness();
  const detachSurface = runtime.attachSurface({});
  try {
    const movie: any = {
      id: 'movie_1',
      libraryId: 'movies',
      kind: 'movie',
      title: 'Test Film',
      available: true,
      durationMs: 180_000,
      progressMs: 0,
      playback: {itemId: 'movie_1', startMs: 0},
    };
    await runtime.start({entry: movie, target: movie.playback, startIntent: 'start-over'});
    // Wait for coordinator lease + initial playback command.
    for (let i = 0; i < 20 && commands.length === 0; i++) await new Promise(r => setImmediate(r));
    const initialCmd = commands.at(-1);
    assert.ok(initialCmd?.binding, 'initial playback command dispatched');
    const initialSessionId = initialCmd.binding.sessionId;

    emitFact({type: 'ready', position: 0, duration: 180, paused: false});
    const pendingSeek = runtime.getSnapshot().playback.pendingSeek;
    if (pendingSeek) emitFact({type: 'seekApplied', seekRevision: pendingSeek.revision, position: pendingSeek.positionSeconds, paused: false});
    const playingSnap = runtime.getSnapshot();
    const fence = playingSnap.fence!;
    assert.ok(fence && fence.kind === 'queue');
    assert.equal(playingSnap.playback.phase, 'ready');
    assert.equal(playingSnap.capabilities.play, false, 'playing title offers Pause, not Play');
    assert.equal(playingSnap.capabilities.pause, true);

    // Pause -> Play is offered; resume restores playing.
    await runtime.toggle(fence);
    assert.equal(runtime.getSnapshot().capabilities.play, true, 'paused title offers Play');
    await runtime.toggle(fence);
    assert.equal(runtime.getSnapshot().capabilities.play, false);

    // Film reaches natural end -> capabilities.play becomes true.
    emitFact({type: 'ended', position: 180, duration: 180, paused: true});
    const endedSnap = runtime.getSnapshot();
    assert.equal(endedSnap.playback.phase, 'ended');
    assert.equal(endedSnap.capabilities.play, true, 'ended film offers Play again');
    assert.equal(endedSnap.capabilities.pause, false);

    // Calling toggle(fence) at the end calls replay(fence) instead of playback.resume(),
    // starting a fresh session from position 0.
    let replayedWith: unknown;
    const originalReplay = runtime.replay;
    runtime.replay = async (targetFence) => {
      replayedWith = targetFence;
      return originalReplay(targetFence);
    };
    const countBefore = commands.length;
    await runtime.toggle(endedSnap.fence!);
    assert.strictEqual(replayedWith, endedSnap.fence, 'toggle() at end delegates to replay(fence)');
    for (let i = 0; i < 20 && commands.length === countBefore; i++) await new Promise(r => setImmediate(r));
    const replayCmd = commands.at(-1);
    assert.ok(replayCmd?.binding, 'replay dispatches a new playback command');
    assert.notEqual(replayCmd.binding.sessionId, initialSessionId, 'replay starts a fresh playback session');
    assert.equal(runtime.getSnapshot().playback.positionSeconds, 0, 'replay starts from 0');
    assert.notEqual(runtime.getSnapshot().playback.phase, 'ended', 'playback leaves ended phase on replay');
  } finally {
    detachSurface();
    runtime.dispose();
  }
});
