import {test} from 'node:test';
import assert from 'node:assert/strict';
import {HandoffController, ReceiverHost, memoryAllowedDevices, parseReceivers, type ReceiverCommand, type ReceiverPlay, type RemoteEvents} from '../src/remote-playback.ts';
import {serverEventsFrom} from '../src/server-events.ts';

/** A stand-in for the rebuilt server's receiver and session routes (server-next
 * internal/api/receivers.go), with what each device is allowed to see. Each device talks to it
 * through its own `api`; every change is told to the account's devices on their feeds. */
type Device = {signIn: string; deviceId: string; name: string; profile: string; open: boolean; off?: boolean; allowed: Set<string>; feed: Feed};
type Request = {id: string; kind: 'play' | 'control'; from: Device; to: Device; state: string; failed?: string; play?: Record<string, unknown>; moving?: string; sessionId?: string; createdAt: number};
type Session = {id: string; signIn: string; itemId: string; state: 'playing' | 'paused' | 'ended'; positionMs: number; command?: Record<string, unknown>;
  /** What the device reports playing (#3180): its version, and its tracks in use there; subtitleId null is off. */
  playing?: {versionId: string; audioTrackId?: string; subtitleId: string | null}};
const fail = (status: number, code: string) => Object.assign(new Error(code), {status, code});

class Feed implements RemoteEvents {
  private listeners = new Map<string, Set<(e: {resource?: {kind: string; id: string}}) => void>>();
  on(type: string, fn: (e: {resource?: {kind: string; id: string}}) => void) {const set = this.listeners.get(type) ?? new Set(); set.add(fn); this.listeners.set(type, set); return () => {set.delete(fn);};}
  private resyncs = new Set<() => void>();
  onResync(fn: () => void) {this.resyncs.add(fn); return () => {this.resyncs.delete(fn);};}
  resync() {for (const fn of [...this.resyncs]) fn();}
  subscribeStatus() {return () => {};}
  getStatus() {return {connected: true};}
  emit(type: string, id: string) {for (const fn of [...this.listeners.get(type) ?? []]) fn({resource: {kind: type.split('.')[0]!, id}});}
}

class StandIn {
  devices: Device[] = [];
  requests = new Map<string, Request>();
  sessions = new Map<string, Session>();
  calls: {signIn: string; method: string; path: string; body?: any}[] = [];
  /** Titles each profile may see. A kids profile sees fewer. */
  visible: Record<string, Set<string>> = {adult: new Set(['movie', 'ep1', 'grown-up']), kids: new Set(['movie', 'ep1'])};
  private n = 0;
  device(name: string, profile = 'adult') {
    const d: Device = {signIn: 'sgn_' + name, deviceId: 'dev_' + name, name, profile, open: false, allowed: new Set(), feed: new Feed()};
    this.devices.push(d); return d;
  }
  private tell(type: string, id: string) {for (const d of this.devices) d.feed.emit(type, id);}
  private out(r: Request) {
    return {id: r.id, receiverId: r.to.deviceId, kind: r.kind, state: r.state, ...(r.failed ? {failed: r.failed} : {}), play: r.play,
      from: {deviceId: r.from.deviceId, deviceName: r.from.name, signInId: r.from.signIn}, ...(r.sessionId ? {sessionId: r.sessionId} : {}),
      createdAt: new Date(r.createdAt).toISOString(), expiresAt: new Date(r.createdAt + 120_000).toISOString()};
  }
  private finish(r: Request, state: string, failed?: string) {r.state = state; r.failed = failed; this.tell('receiver.changed', r.id);}
  /** The receiving device starts what was sent. */
  start(d: Device, itemId: string, requestId?: string) {
    const id = 'ps_' + ++this.n;
    for (const s of this.sessions.values()) if (s.signIn === d.signIn && s.state !== 'ended') s.state = 'ended';
    this.sessions.set(id, {id, signIn: d.signIn, itemId, state: 'playing', positionMs: 0});
    if (requestId) assert.ok(this.requests.get(requestId)?.to === d, 'the start names an open request to this device');
    return id;
  }
  /** The device stops being reachable: it stops saying it is open and is no longer listed. */
  goAway(d: Device) {
    d.open = false; d.allowed.clear();
    for (const r of this.requests.values()) if (r.to === d && (r.state === 'asking' || r.state === 'allowed' && r.kind === 'play')) this.finish(r, 'failed', 'not_accepting');
  }
  /** A restart forgets every device, request and who was allowed. */
  restart() {for (const d of this.devices) {d.open = false; d.allowed.clear();} this.requests.clear(); for (const d of this.devices) d.feed.resync();}
  api(d: Device) {
    return {request: async <T,>(path: string, method = 'GET', body?: any): Promise<T> => {
      this.calls.push({signIn: d.signIn, method, path, body});
      await Promise.resolve();
      return this.route(d, method, path.split('/').map(decodeURIComponent).join('/'), body) as T;
    }};
  }
  private route(d: Device, method: string, path: string, body: any): unknown {
    let m: RegExpMatchArray | null;
    if (path === '/v1/receivers' && method === 'GET')
      return {items: this.devices.filter(x => x.open).map(x => {
        const s = [...this.sessions.values()].find(v => v.signIn === x.signIn && v.state !== 'ended');
        return {id: x.deviceId, name: x.name, platform: 'androidtv', current: x === d, allowed: x.allowed.has(d.signIn),
          ...(s ? {playing: {id: s.id, deviceId: x.deviceId, deviceName: x.name, kind: 'vod', state: s.state, positionMs: s.positionMs, startedAt: 'x', current: false, ...(this.visible[d.profile]!.has(s.itemId) ? {itemId: s.itemId, title: 'Title ' + s.itemId} : {})}} : {})};
      })};
    if (path === '/v1/receivers/current' && method === 'PUT') {if (d.off) {d.open = false; throw fail(409, 'play_from_other_devices_off');} d.open = true; return {id: d.deviceId, renewEveryMs: 60_000};}
    if (path === '/v1/receivers/current' && method === 'DELETE') {this.goAway(d); return {};}
    if (path === '/v1/receivers/current/requests' && method === 'GET')
      return {items: [...this.requests.values()].filter(r => r.to === d && (r.state === 'asking' || r.state === 'allowed' && r.kind === 'play')).map(r => this.out(r))};
    if ((m = path.match(/^\/v1\/receivers\/current\/requests\/([^/]+)$/)) && method === 'PATCH') {
      const r = this.requests.get(m[1]!);
      if (!r || r.to !== d) throw fail(404, 'not_found');
      if (!d.open) throw fail(404, 'not_found');
      if (r.state !== 'asking' && !(r.state === 'allowed' && r.kind === 'play')) throw fail(409, 'request_over');
      if (body.answer === 'allowed') {d.allowed.add(r.from.signIn); this.finish(r, 'allowed');}
      else if (body.answer === 'declined') this.finish(r, 'declined');
      else if (body.answer === 'failed') this.finish(r, 'failed', 'couldnt_start');
      else if (body.answer === 'playing') {
        const s = this.sessions.get(body.sessionId);
        assert.ok(s && s.signIn === d.signIn && s.itemId === r.play?.itemId, 'playing names this device\'s session of what was asked');
        d.allowed.add(r.from.signIn); r.sessionId = body.sessionId; this.finish(r, 'playing');
        if (r.moving) {const moved = this.sessions.get(r.moving); if (moved) moved.state = 'ended';}
      }
      return this.out(r);
    }
    if ((m = path.match(/^\/v1\/receivers\/([^/]+)\/requests$/)) && method === 'POST') {
      const to = this.devices.find(x => x.open && x.deviceId === m![1]);
      if (!to || to === d) throw fail(404, 'not_found');
      if (body.kind === 'play' && (!this.visible[d.profile]!.has(body.play.itemId) || !this.visible[to.profile]!.has(body.play.itemId))) throw fail(404, 'not_found');
      const r: Request = {id: 'rcq_' + ++this.n, kind: body.kind, from: d, to, state: to.allowed.has(d.signIn) ? 'allowed' : 'asking', play: body.play, moving: body.sessionId, createdAt: Date.now()};
      this.requests.set(r.id, r); this.tell('receiver.changed', r.id);
      return this.out(r);
    }
    if ((m = path.match(/^\/v1\/receivers\/([^/]+)\/requests\/([^/]+)$/))) {
      const r = this.requests.get(m[2]!);
      if (!r || r.from !== d && r.to !== d) throw fail(404, 'not_found');
      if (method === 'DELETE' && (r.state === 'asking' || r.state === 'allowed' && r.kind === 'play')) this.finish(r, 'cancelled');
      return this.out(r);
    }
    if ((m = path.match(/^\/v1\/items\/([^/]+)\/playback-options$/)) && method === 'GET') {
      if (!this.visible[d.profile]!.has(m[1]!)) throw fail(404, 'not_found');
      return {itemId: m[1], kind: 'movie', versions: [{id: 'ver_1', label: '1080p', durationMs: 7_200_000, parts: [{id: 'p1', index: 0, durationMs: 7_200_000}], video: [],
        audio: [{id: 'a1', codec: 'aac', channels: 2, language: 'en', default: true, atmos: false, commentary: false, visualImpaired: false}, {id: 'a2', codec: 'ac3', channels: 6, language: 'fr', default: false, atmos: false, commentary: false, visualImpaired: false}],
        subtitles: [{id: 's1', format: 'srt', source: 'sidecar', language: 'en', forced: false, sdh: false, default: false}]}], chapters: [], markers: [], qualities: []};
    }
    if (path === '/v1/playback/sessions' && method === 'GET')
      return {items: [...this.sessions.values()].filter(s => s.state !== 'ended').map(s => {const dev = this.devices.find(x => x.signIn === s.signIn)!;
        return {id: s.id, deviceId: dev.deviceId, deviceName: dev.name, kind: 'vod', state: s.state, positionMs: s.positionMs, startedAt: 'x', current: s.signIn === d.signIn,
          ...(this.visible[d.profile]!.has(s.itemId) ? {itemId: s.itemId, title: 'Title ' + s.itemId, ...(s.playing ? {versionId: s.playing.versionId,
            ...(s.playing.audioTrackId ? {audioTrackId: s.playing.audioTrackId} : {}), ...(s.playing.subtitleId ? {subtitle: {trackId: s.playing.subtitleId, kind: 'text', delivery: 'sidecar'}} : {})} : {})} : {})};})};
    if ((m = path.match(/^\/v1\/playback\/sessions\/([^/]+)$/))) {
      const s = this.sessions.get(m[1]!);
      if (!s) throw fail(404, 'not_found');
      if (method === 'GET') {assert.equal(s.signIn, d.signIn, 'a device reads only its own session'); return {id: s.id, state: s.state, itemId: s.itemId, ...(s.command ? {command: s.command} : {})};}
      if (method === 'DELETE') {s.state = 'ended'; this.tell('playback.changed', s.id); return {};}
      if (method === 'PATCH') {
        if (s.signIn === d.signIn) throw new Error('not a remote change');
        const owner = this.devices.find(x => x.signIn === s.signIn)!;
        if (!owner.open || !owner.allowed.has(d.signIn)) throw fail(403, 'control_not_allowed');
        s.command = {id: 'cmd_' + ++this.n, ...(body.state ? {state: body.state} : {}), ...(body.seek ? {seekMs: body.seek.positionMs} : {}), ...(body.skip ? {skip: body.skip} : {}),
          ...(body.audio ? {audioTrackId: body.audio.trackId} : {}), ...(body.subtitles ? {subtitleTrackId: body.subtitles.trackId ?? ''} : {}),
          fromDeviceId: d.deviceId, at: new Date().toISOString()};
        this.tell('playback.changed', s.id);
        return {id: s.id, state: s.state, command: s.command};
      }
    }
    throw new Error(`unexpected ${method} ${path}`);
  }
}

const until = async (what: string, ok: () => boolean, ms = 3000) => {
  const end = Date.now() + ms;
  while (!ok()) {if (Date.now() > end) throw new Error('timed out waiting for ' + what); await new Promise(r => setTimeout(r, 5));}
};

/** A TV: its player plays what it is sent and records what it is told to do. */
function television(server: StandIn, d: Device, options: {allowed?: ReturnType<typeof memoryAllowedDevices>; loadFails?: boolean} = {}) {
  let now = {itemId: '', playbackId: '', playing: false, positionSeconds: 0};
  const loads: ReceiverPlay[] = [], commands: ReceiverCommand[] = [];
  const host = new ReceiverHost({api: server.api(d), events: d.feed, allowed: options.allowed ?? memoryAllowedDevices(), pollMs: 20, busyPollMs: 20,
    player: {
      load: async play => {loads.push(play); if (options.loadFails) throw new Error('cannot play'); const id = server.start(d, play.itemId, play.requestId); now = {itemId: play.itemId, playbackId: id, playing: true, positionSeconds: play.positionSeconds};},
      state: () => now,
      command: (_id, c) => {commands.push(c);},
    }});
  host.start();
  /** The TV's own person starts something on it. */
  const play = (itemId: string) => {const id = server.start(d, itemId); now = {itemId, playbackId: id, playing: true, positionSeconds: 0}; return id;};
  return {host, loads, commands, play};
}
function phone(server: StandIn, d: Device) {
  const sessionId = server.start(d, 'movie');
  return {controller: new HandoffController({api: server.api(d), events: d.feed, pollMs: 10}), sessionId};
}
const source = (sessionId: string, itemId = 'movie') => ({playbackId: sessionId, itemId, positionSeconds: 754.2, audioTrackId: 'a2', subtitleTrackId: '', queueId: 'que_1'});

test('a move: the TV asks, its person allows, it plays what was sent, and only then is the phone done', async t => {
  const server = new StandIn(), tvDevice = server.device('Living room'), phoneDevice = server.device('Phone');
  const tv = television(server, tvDevice); t.after(() => tv.host.dispose());
  await until('the TV to open', () => tvDevice.open);
  const {controller, sessionId} = phone(server, phoneDevice); t.after(() => controller.dispose());
  await controller.refresh();
  assert.deepEqual(controller.getSnapshot().receivers.map(r => r.displayName), ['Living room'], 'the phone sees the TV and not itself');
  const sending = controller.send(controller.getSnapshot().receivers[0]!, source(sessionId));
  await until('the TV to ask', () => !!tv.host.getSnapshot().asking);
  assert.equal(tv.host.getSnapshot().asking!.from.deviceName, 'Phone');
  assert.equal(controller.getSnapshot().phase, 'asking');
  assert.match(controller.getSnapshot().message!, /Approve this on Living room/);
  assert.equal(server.sessions.get(sessionId)!.state, 'playing', 'the phone keeps playing while the TV asks');
  await tv.host.decide(tv.host.getSnapshot().asking!, 'allow');
  assert.equal(await sending, true);
  assert.deepEqual(tv.loads[0], {requestId: tv.loads[0]!.requestId, itemId: 'movie', positionSeconds: 754.2, audioTrackId: 'a2', subtitleTrackId: '', queueId: 'que_1'},
    'the title, the place, the audio and subtitle picks and the queue move');
  const snapshot = controller.getSnapshot();
  assert.equal(snapshot.phase, 'done');
  assert.equal(snapshot.receiverPlaybackId, server.sessions.get(snapshot.receiverPlaybackId!)!.id);
  assert.equal(server.sessions.get(sessionId)!.state, 'ended', 'the server stopped the phone once the TV played');
  const post = server.calls.find(c => c.method === 'POST' && c.path.endsWith('/requests'))!;
  assert.deepEqual(post.body, {kind: 'play', play: {itemId: 'movie', positionMs: 754200, audioTrackId: 'a2', subtitleTrackId: '', queueId: 'que_1'}, sessionId});
});

test('a declined move leaves the phone playing and says so; nothing starts on the TV', async t => {
  const server = new StandIn(), tvDevice = server.device('Living room'), phoneDevice = server.device('Phone');
  const tv = television(server, tvDevice); t.after(() => tv.host.dispose());
  await until('the TV to open', () => tvDevice.open);
  const {controller, sessionId} = phone(server, phoneDevice); t.after(() => controller.dispose());
  await controller.refresh();
  const sending = controller.send(controller.getSnapshot().receivers[0]!, source(sessionId));
  await until('the TV to ask', () => !!tv.host.getSnapshot().asking);
  await tv.host.decide(tv.host.getSnapshot().asking!, 'decline');
  assert.equal(await sending, false);
  assert.equal(controller.getSnapshot().phase, 'failed');
  assert.equal(controller.getSnapshot().message, 'Living room declined.');
  assert.equal(tv.loads.length, 0);
  assert.equal(server.sessions.get(sessionId)!.state, 'playing');
});

test('a title the TV\'s profile may not see is refused in plain words, and the phone keeps playing', async t => {
  const server = new StandIn(), tvDevice = server.device('Kids TV', 'kids'), phoneDevice = server.device('Phone');
  const tv = television(server, tvDevice); t.after(() => tv.host.dispose());
  await until('the TV to open', () => tvDevice.open);
  const {controller, sessionId} = phone(server, phoneDevice); t.after(() => controller.dispose());
  await controller.refresh();
  assert.equal(await controller.send(controller.getSnapshot().receivers[0]!, source(sessionId, 'grown-up')), false);
  assert.equal(controller.getSnapshot().message, 'Kids TV couldn’t start this. Still playing here.');
  assert.equal(tv.host.getSnapshot().asking, undefined, 'the TV is never asked');
  assert.equal(server.sessions.get(sessionId)!.state, 'playing');
});

test('a TV that goes away mid-request: the phone says so and keeps playing', async t => {
  const server = new StandIn(), tvDevice = server.device('Living room'), phoneDevice = server.device('Phone');
  const tv = television(server, tvDevice); t.after(() => tv.host.dispose());
  await until('the TV to open', () => tvDevice.open);
  const {controller, sessionId} = phone(server, phoneDevice); t.after(() => controller.dispose());
  await controller.refresh();
  const sending = controller.send(controller.getSnapshot().receivers[0]!, source(sessionId));
  await until('the TV to ask', () => !!tv.host.getSnapshot().asking);
  tv.host.dispose();
  server.goAway(tvDevice);
  assert.equal(await sending, false);
  assert.equal(controller.getSnapshot().message, 'Living room isn’t accepting playback right now.');
  assert.equal(server.sessions.get(sessionId)!.state, 'playing');
});

test('a TV that cannot start what was sent says so, and the phone keeps playing', async t => {
  const server = new StandIn(), tvDevice = server.device('Living room'), phoneDevice = server.device('Phone');
  const allowed = memoryAllowedDevices(); allowed.add(phoneDevice.signIn);
  const tv = television(server, tvDevice, {allowed, loadFails: true}); t.after(() => tv.host.dispose());
  await until('the TV to open', () => tvDevice.open);
  const {controller, sessionId} = phone(server, phoneDevice); t.after(() => controller.dispose());
  await controller.refresh();
  assert.equal(await controller.send(controller.getSnapshot().receivers[0]!, source(sessionId)), false);
  assert.equal(controller.getSnapshot().message, 'Living room couldn’t start this. Still playing here.');
  assert.equal(server.sessions.get(sessionId)!.state, 'playing');
});

test('cancelling a move withdraws the request; the phone keeps playing', async t => {
  const server = new StandIn(), tvDevice = server.device('Living room'), phoneDevice = server.device('Phone');
  const tv = television(server, tvDevice); t.after(() => tv.host.dispose());
  await until('the TV to open', () => tvDevice.open);
  const {controller, sessionId} = phone(server, phoneDevice); t.after(() => controller.dispose());
  await controller.refresh();
  const sending = controller.send(controller.getSnapshot().receivers[0]!, source(sessionId));
  await until('the TV to ask', () => !!tv.host.getSnapshot().asking);
  controller.cancel();
  assert.equal(await sending, false);
  await until('the request to be withdrawn', () => [...server.requests.values()][0]!.state === 'cancelled');
  await until('the TV to stop asking', () => !tv.host.getSnapshot().asking);
  assert.equal(server.sessions.get(sessionId)!.state, 'playing');
});

test('two senders at once: the TV asks about one device at a time, and the last move plays', async t => {
  const server = new StandIn(), tvDevice = server.device('Living room'), a = server.device('Phone'), b = server.device('Laptop');
  const tv = television(server, tvDevice); t.after(() => tv.host.dispose());
  await until('the TV to open', () => tvDevice.open);
  const first = phone(server, a), second = phone(server, b); t.after(() => {first.controller.dispose(); second.controller.dispose();});
  await first.controller.refresh(); await second.controller.refresh();
  const sendingA = first.controller.send(first.controller.getSnapshot().receivers[0]!, source(first.sessionId));
  const sendingB = second.controller.send(second.controller.getSnapshot().receivers.find(r => r.displayName === 'Living room')!, source(second.sessionId, 'ep1'));
  await until('the TV to ask', () => !!tv.host.getSnapshot().asking);
  assert.equal(tv.host.getSnapshot().asking!.from.deviceName, 'Phone', 'the first to ask is asked about first');
  await tv.host.decide(tv.host.getSnapshot().asking!, 'allow');
  assert.equal(await sendingA, true);
  await until('the TV to ask about the second', () => tv.host.getSnapshot().asking?.from.deviceName === 'Laptop');
  await tv.host.decide(tv.host.getSnapshot().asking!, 'allow');
  assert.equal(await sendingB, true);
  assert.deepEqual(tv.loads.map(l => l.itemId), ['movie', 'ep1']);
  assert.equal(server.sessions.get(first.sessionId)!.state, 'ended');
  assert.equal(server.sessions.get(second.sessionId)!.state, 'ended');
});

test('the TV remembers whom it allowed: after a server restart it is not asked again, and control carries on', async t => {
  const server = new StandIn(), tvDevice = server.device('Living room'), phoneDevice = server.device('Phone');
  const allowed = memoryAllowedDevices();
  const tv = television(server, tvDevice, {allowed}); t.after(() => tv.host.dispose());
  await until('the TV to open', () => tvDevice.open);
  const {controller, sessionId} = phone(server, phoneDevice); t.after(() => controller.dispose());
  await controller.refresh();
  const sending = controller.send(controller.getSnapshot().receivers[0]!, source(sessionId));
  await until('the TV to ask', () => !!tv.host.getSnapshot().asking);
  await tv.host.decide(tv.host.getSnapshot().asking!, 'allow');
  assert.equal(await sending, true);
  assert.equal(allowed.has(phoneDevice.signIn), true);

  assert.equal(await controller.pause(), true);
  await until('the TV to carry out the pause', () => tv.commands.length === 1);
  assert.equal(tv.commands[0]!.state, 'paused');

  // The server restarts: it forgets the TV and that it allowed the phone. The TV opens itself
  // again; the phone's next command is refused once, the TV is asked and answers by itself.
  server.restart();
  await until('the TV to open again', () => tvDevice.open, 5000);
  assert.equal(await controller.seek(30), true);
  await until('the TV to carry out the seek', () => tv.commands.length === 2);
  assert.equal(tv.commands[1]!.seekMs, 30_000);
  assert.equal(tv.host.getSnapshot().asking, undefined, 'its person was not asked again');
});

test('a refused control says so in plain words', async t => {
  const server = new StandIn(), tvDevice = server.device('Living room'), phoneDevice = server.device('Phone');
  const tv = television(server, tvDevice); t.after(() => tv.host.dispose());
  await until('the TV to open', () => tvDevice.open);
  const {controller, sessionId} = phone(server, phoneDevice); t.after(() => controller.dispose());
  await controller.refresh();
  const sending = controller.send(controller.getSnapshot().receivers[0]!, source(sessionId));
  await until('the TV to ask', () => !!tv.host.getSnapshot().asking);
  await tv.host.decide(tv.host.getSnapshot().asking!, 'allow');
  assert.equal(await sending, true);
  // The TV is switched off: nobody may control it any more.
  tv.host.dispose(); server.goAway(tvDevice);
  assert.equal(await controller.pause(), false);
  assert.ok(controller.getSnapshot().message, 'a message is shown');
  assert.doesNotMatch(controller.getSnapshot().message!, /control_not_allowed|403/);
});

test('a TV with Play from other devices off on the server stops trying and asks nothing', async t => {
  const server = new StandIn(), tvDevice = server.device('Living room'); tvDevice.off = true;
  const tv = television(server, tvDevice); t.after(() => tv.host.dispose());
  await until('the TV to hear it is off', () => tv.host.getSnapshot().phase === 'turned-off');
  const puts = server.calls.filter(c => c.method === 'PUT').length;
  await new Promise(r => setTimeout(r, 80));
  assert.equal(server.calls.filter(c => c.method === 'PUT').length, puts, 'it does not keep asking');
});

test('a stale command is not carried out again when the TV reads its session later', async t => {
  const server = new StandIn(), tvDevice = server.device('Living room'), phoneDevice = server.device('Phone');
  const allowed = memoryAllowedDevices(); allowed.add(phoneDevice.signIn);
  const tv = television(server, tvDevice, {allowed}); t.after(() => tv.host.dispose());
  await until('the TV to open', () => tvDevice.open);
  const {controller, sessionId} = phone(server, phoneDevice); t.after(() => controller.dispose());
  await controller.refresh();
  assert.equal(await controller.send(controller.getSnapshot().receivers[0]!, source(sessionId)), true);
  assert.equal(tv.host.getSnapshot().asking, undefined, 'a remembered device is not asked');
  assert.equal(await controller.next(), true);
  await until('the skip', () => tv.commands.length === 1);
  assert.equal(tv.commands[0]!.skip, 'next');
  tvDevice.feed.emit('playback.changed', controller.getSnapshot().receiverPlaybackId!);
  await new Promise(r => setTimeout(r, 60));
  assert.equal(tv.commands.length, 1, 'the same command once');
});

test('the list leaves this device out and keeps what another device plays, unnamed when this profile may not see it', () => {
  const list = parseReceivers({items: [
    {id: 'dev_me', name: 'Phone', current: true, allowed: false},
    {id: 'dev_tv', name: 'Living room', platform: 'androidtv', current: false, allowed: true, playing: {id: 'ps_1', deviceId: 'dev_tv', deviceName: 'Living room', kind: 'vod', state: 'paused', positionMs: 5000, startedAt: 'x', current: false,
      next: {itemId: 'ep2', title: 'Episode two'}}},
    {id: 'dev_kids', name: 'Kids room', current: false, allowed: false, playing: {id: 'ps_2', deviceId: 'dev_kids', deviceName: 'Kids room', kind: 'vod', state: 'playing', positionMs: 0, startedAt: 'x', current: false}},
  ]});
  assert.equal(list.length, 2);
  assert.deepEqual(list[0], {id: 'dev_tv', displayName: 'Living room', platform: 'androidtv', allowed: true,
    playing: {sessionId: 'ps_1', deviceName: 'Living room', state: 'paused', positionMs: 5000, next: {itemId: 'ep2', title: 'Episode two'}}});
  assert.deepEqual(list[1]!.playing, {sessionId: 'ps_2', deviceName: 'Kids room', state: 'playing', positionMs: 0}, 'no title and no next when this profile may not see them');
});

test('the change feed\'s receiver and playback topics reach remote playback', () => {
  assert.deepEqual(serverEventsFrom({id: '7', event: 'change', data: JSON.stringify({topic: 'receiver', op: 'updated', id: 'rcq_1'})}),
    [{id: '7', type: 'receiver.changed', resource: {kind: 'receiver', id: 'rcq_1'}}]);
  assert.deepEqual(serverEventsFrom({id: '8', event: 'change', data: JSON.stringify({topic: 'playback', op: 'updated', id: 'ps_1'})}),
    [{id: '8', type: 'playback.changed', resource: {kind: 'playback', id: 'ps_1'}}]);
  assert.deepEqual(serverEventsFrom({id: '9', event: 'change', data: JSON.stringify({topic: 'receiver', op: 'updated'})}),
    [{id: '9', type: 'receiver.changed'}]);
});

test('rule 28: a phone controls what the TV is playing by itself, after the TV\'s yes; its own playback carries on', async t => {
  const server = new StandIn(), tvDevice = server.device('Living room'), phoneDevice = server.device('Phone');
  const tv = television(server, tvDevice); t.after(() => tv.host.dispose());
  await until('the TV to open', () => tvDevice.open);
  const tvSession = tv.play('ep1');
  const {controller, sessionId} = phone(server, phoneDevice); t.after(() => controller.dispose());
  await controller.refresh();
  const target = controller.getSnapshot().receivers[0]!;
  assert.deepEqual(target.playing && {sessionId: target.playing.sessionId, title: target.playing.title}, {sessionId: tvSession, title: 'Title ep1'}, 'Play on shows what the TV plays');
  const controlling = controller.control(target);
  await until('the TV to ask', () => !!tv.host.getSnapshot().asking);
  assert.equal(tv.host.getSnapshot().asking!.kind, 'control');
  assert.match(controller.getSnapshot().message!, /Approve this on Living room/);
  await tv.host.decide(tv.host.getSnapshot().asking!, 'allow');
  assert.equal(await controlling, true);
  assert.equal(controller.getSnapshot().receiverPlaybackId, tvSession);
  assert.equal(server.sessions.get(sessionId)!.state, 'playing', 'the phone\'s own playback carries on');
  assert.equal(await controller.pause(), true);
  await until('the TV to pause', () => tv.commands.length === 1);
  assert.equal(tv.commands[0]!.state, 'paused');
  assert.equal(tv.loads.length, 0, 'nothing was started on the TV');
});

test('rule 28: a remembered phone is not asked again, and a declined one says so', async t => {
  const server = new StandIn(), tvDevice = server.device('Living room'), a = server.device('Phone'), b = server.device('Tablet');
  const allowed = memoryAllowedDevices(); allowed.add(a.signIn);
  const tv = television(server, tvDevice, {allowed}); t.after(() => tv.host.dispose());
  await until('the TV to open', () => tvDevice.open);
  tv.play('movie');
  const first = phone(server, a), second = phone(server, b); t.after(() => {first.controller.dispose(); second.controller.dispose();});
  await first.controller.refresh(); await second.controller.refresh();
  assert.equal(await first.controller.control(first.controller.getSnapshot().receivers.find(r => r.displayName === 'Living room')!), true);
  assert.equal(tv.host.getSnapshot().asking, undefined, 'the remembered phone is answered by the TV itself');
  const asking = second.controller.control(second.controller.getSnapshot().receivers.find(r => r.displayName === 'Living room')!);
  await until('the TV to ask about the tablet', () => tv.host.getSnapshot().asking?.from.deviceName === 'Tablet');
  await tv.host.decide(tv.host.getSnapshot().asking!, 'decline');
  assert.equal(await asking, false);
  assert.equal(second.controller.getSnapshot().message, 'Living room declined.');
});

test('a device forgotten in This device is asked again, and the TV lists whom it allowed by name', async t => {
  const server = new StandIn(), tvDevice = server.device('Living room'), phoneDevice = server.device('Phone');
  const allowed = memoryAllowedDevices();
  const tv = television(server, tvDevice, {allowed}); t.after(() => tv.host.dispose());
  await until('the TV to open', () => tvDevice.open);
  tv.play('movie');
  const {controller} = phone(server, phoneDevice); t.after(() => controller.dispose());
  await controller.refresh();
  const asking = controller.control(controller.getSnapshot().receivers[0]!);
  await until('the TV to ask', () => !!tv.host.getSnapshot().asking);
  await tv.host.decide(tv.host.getSnapshot().asking!, 'allow');
  assert.equal(await asking, true);
  assert.deepEqual(allowed.list!(), [{signInId: phoneDevice.signIn, name: 'Phone'}]);
  allowed.forget!(phoneDevice.signIn);
  await until('the TV to open again, forgetting whom the server knew it allowed', () => tvDevice.open && !tvDevice.allowed.has(phoneDevice.signIn) && server.calls.filter(c => c.signIn === tvDevice.signIn && c.method === 'DELETE').length === 1);
  await controller.refresh();
  const again = controller.control(controller.getSnapshot().receivers[0]!);
  await until('the TV to ask again', () => !!tv.host.getSnapshot().asking);
  await tv.host.decide(tv.host.getSnapshot().asking!, 'decline');
  assert.equal(await again, false);
});

test('the bar\'s audio and subtitles reach the TV, which carries them out, and the TV\'s tracks are named with the ones moved there marked', async t => {
  const server = new StandIn(), tvDevice = server.device('Living room'), phoneDevice = server.device('Phone');
  const allowed = memoryAllowedDevices(); allowed.add(phoneDevice.signIn);
  const tv = television(server, tvDevice, {allowed}); t.after(() => tv.host.dispose());
  await until('the TV to open', () => tvDevice.open);
  const {controller, sessionId} = phone(server, phoneDevice); t.after(() => controller.dispose());
  await controller.refresh();
  assert.equal(await controller.send(controller.getSnapshot().receivers[0]!, source(sessionId)), true);
  await until('the TV\'s title to be read', () => !!controller.getSnapshot().remote?.itemId);
  const tracks = await controller.remoteTracks();
  assert.deepEqual(tracks?.audio.map(track => track.id), ['a1', 'a2']);
  assert.match(tracks!.audio[1]!.name, /Dolby Digital/);
  assert.deepEqual([tracks?.subtitles.map(track => track.id), tracks?.audioId, tracks?.subtitleId], [['s1'], 'a2', null], 'what moved there is what it plays: French, subtitles off');
  assert.equal(await controller.audio('a1'), true);
  await until('the TV to switch audio', () => tv.commands.length === 1);
  assert.equal(tv.commands[0]!.audioTrackId, 'a1');
  assert.equal(await controller.subtitles('s1'), true);
  await until('the TV to switch subtitles', () => tv.commands.length === 2);
  assert.equal(tv.commands[1]!.subtitleTrackId, 's1');
  assert.deepEqual([(await controller.remoteTracks())?.audioId, (await controller.remoteTracks())?.subtitleId], ['a1', 's1'], 'the bar marks what was chosen');
  assert.equal(await controller.subtitles(null), true);
  await until('the TV to turn subtitles off', () => tv.commands.length === 3);
  assert.equal(tv.commands[2]!.subtitleTrackId, '');
});

test('a track choice belongs to its title: when the TV moves on to the next title, nothing it was told is shown as playing', async t => {
  const server = new StandIn(), tvDevice = server.device('Living room'), phoneDevice = server.device('Phone');
  const allowed = memoryAllowedDevices(); allowed.add(phoneDevice.signIn);
  const tv = television(server, tvDevice, {allowed}); t.after(() => tv.host.dispose());
  await until('the TV to open', () => tvDevice.open);
  const {controller, sessionId} = phone(server, phoneDevice); t.after(() => controller.dispose());
  await controller.refresh();
  assert.equal(await controller.send(controller.getSnapshot().receivers[0]!, source(sessionId)), true);
  await until('the TV\'s title to be read', () => controller.getSnapshot().remote?.itemId === 'movie');
  assert.equal(await controller.audio('a1'), true);
  assert.equal((await controller.remoteTracks())?.audioId, 'a1');
  // Its queue moves on by itself to the next title.
  server.sessions.get(controller.getSnapshot().receiverPlaybackId!)!.itemId = 'ep1';
  await controller.refreshRemote();
  assert.equal(controller.getSnapshot().remote?.itemId, 'ep1');
  const next = await controller.remoteTracks();
  assert.deepEqual([next?.audioId, next?.subtitleId], [undefined, undefined], 'nothing checked that it was not told on this title');
});


test('the bar starts from what the TV reports playing; a choice stands in until the TV reports it, or two reports pass without it', async t => {
  const server = new StandIn(), tvDevice = server.device('Living room'), phoneDevice = server.device('Phone');
  const allowed = memoryAllowedDevices(); allowed.add(phoneDevice.signIn);
  const tv = television(server, tvDevice, {allowed}); t.after(() => tv.host.dispose());
  await until('the TV to open', () => tvDevice.open);
  const {controller, sessionId} = phone(server, phoneDevice); t.after(() => controller.dispose());
  await controller.refresh();
  assert.equal(await controller.send(controller.getSnapshot().receivers[0]!, source(sessionId)), true);
  await until('the TV\'s title to be read', () => controller.getSnapshot().remote?.itemId === 'movie');
  const session = server.sessions.get(controller.getSnapshot().receiverPlaybackId!)!;
  // Someone on the TV switched to English audio with English subtitles.
  session.playing = {versionId: 'ver_1', audioTrackId: 'a1', subtitleId: 's1'};
  await controller.refreshRemote();
  assert.deepEqual(controller.inUse(), {audioId: 'a2', subtitleId: null}, 'one report against what was sent may be stale: it still stands');
  await controller.refreshRemote();
  assert.deepEqual(controller.getSnapshot().remote && [controller.getSnapshot().remote!.versionId, controller.getSnapshot().remote!.audioTrackId, controller.getSnapshot().remote!.subtitleId], ['ver_1', 'a1', 's1']);
  let tracks = await controller.remoteTracks();
  assert.deepEqual([tracks?.audioId, tracks?.subtitleId], ['a1', 's1'], 'what the TV reports, not what was sent to it');
  // A choice from the bar shows at once, before the TV reports it.
  assert.equal(await controller.subtitles(null), true);
  assert.equal(controller.inUse().subtitleId, null);
  await controller.refreshRemote();
  assert.equal(controller.inUse().subtitleId, null, 'one report without it: the choice still stands');
  session.playing = {versionId: 'ver_1', audioTrackId: 'a1', subtitleId: null};
  await controller.refreshRemote();
  assert.deepEqual(controller.inUse(), {audioId: 'a1', subtitleId: null}, 'reported: the TV\'s own report again');
  // A choice the TV never carries out gives way to its report after two reports.
  assert.equal(await controller.audio('a2'), true);
  await controller.refreshRemote(); assert.equal(controller.inUse().audioId, 'a2');
  await controller.refreshRemote(); assert.equal(controller.inUse().audioId, 'a1', 'the TV still plays English');
});
