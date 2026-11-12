import {test} from 'node:test';
import assert from 'node:assert/strict';
import {HandoffController, ReceiverHost, memoryAllowedDevices, parseReceivers, type ReceiverCommand, type ReceiverPlay, type RemoteEvents} from '../src/remote-playback.ts';
import {serverEventsFrom} from '../src/server-events.ts';

/** A stand-in for the rebuilt server's receiver and session routes (server-next
 * internal/api/receivers.go), with what each device is allowed to see. Each device talks to it
 * through its own `api`; every change is told to the account's devices on their feeds. */
type Device = {signIn: string; deviceId: string; name: string; profile: string; open: boolean; off?: boolean; allowed: Set<string>; feed: Feed};
type Request = {id: string; kind: 'play' | 'control'; from: Device; to: Device; state: string; failed?: string; play?: Record<string, unknown>; moving?: string; sessionId?: string; createdAt: number};
type Session = {id: string; signIn: string; itemId: string; state: 'playing' | 'paused' | 'ended'; positionMs: number; command?: Record<string, unknown>};
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
      return {items: this.devices.filter(x => x.open).map(x => ({id: x.deviceId, name: x.name, platform: 'androidtv', current: x === d, allowed: x.allowed.has(d.signIn)}))};
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
    if (path === '/v1/playback/sessions' && method === 'GET')
      return {items: [...this.sessions.values()].filter(s => s.state !== 'ended').map(s => {const dev = this.devices.find(x => x.signIn === s.signIn)!;
        return {id: s.id, deviceId: dev.deviceId, deviceName: dev.name, kind: 'vod', state: s.state, positionMs: s.positionMs, startedAt: 'x', current: s.signIn === d.signIn,
          ...(this.visible[d.profile]!.has(s.itemId) ? {itemId: s.itemId, title: 'Title ' + s.itemId} : {})};})};
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
  return {host, loads, commands};
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
    {id: 'dev_tv', name: 'Living room', platform: 'androidtv', current: false, allowed: true, playing: {id: 'ps_1', deviceId: 'dev_tv', deviceName: 'Living room', kind: 'vod', state: 'paused', positionMs: 5000, startedAt: 'x', current: false}},
  ]});
  assert.equal(list.length, 1);
  assert.deepEqual(list[0], {id: 'dev_tv', displayName: 'Living room', platform: 'androidtv', allowed: true,
    playing: {sessionId: 'ps_1', deviceName: 'Living room', state: 'paused', positionMs: 5000}});
});

test('the change feed\'s receiver and playback topics reach remote playback', () => {
  assert.deepEqual(serverEventsFrom({id: '7', event: 'change', data: JSON.stringify({topic: 'receiver', op: 'updated', id: 'rcq_1'})}),
    [{id: '7', type: 'receiver.changed', resource: {kind: 'receiver', id: 'rcq_1'}}]);
  assert.deepEqual(serverEventsFrom({id: '8', event: 'change', data: JSON.stringify({topic: 'playback', op: 'updated', id: 'ps_1'})}),
    [{id: '8', type: 'playback.changed', resource: {kind: 'playback', id: 'ps_1'}}]);
  assert.deepEqual(serverEventsFrom({id: '9', event: 'change', data: JSON.stringify({topic: 'receiver', op: 'updated'})}),
    [{id: '9', type: 'receiver.changed'}]);
});
