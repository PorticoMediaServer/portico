import test from 'node:test';
import assert from 'node:assert/strict';
import {WebCastSender, loadCastSdk, type CastBrowser, type CastSession, type CastSessionEvent, type CastFramework} from '../src/platform/cast/index';

const settle = async () => { for (let i = 0; i < 8; i++) await new Promise(setImmediate); };
function sdk() {
  const messages: Record<string, unknown>[] = [], requests: unknown[] = [], options: unknown[] = [];
  const listeners = new Set<(namespace: string, body: string) => void>();
  const contextListeners = new Set<(event: CastSessionEvent) => void>();
  const timers = new Map<number, () => void>(), idle = new Map<number, IdleRequestCallback>();
  const scripts: {src: string; async: boolean; errors: Set<() => void>; remove(): void}[] = [];
  let timer = 0, picks = 0, current: CastSession | null = null, pick: (() => Promise<unknown>) | undefined;
  let name = '  Living room  ', acknowledge = false, ended: boolean | undefined;
  const reply = (body: unknown) => { for (const listener of [...listeners]) listener('urn:x-cast:tv.getportico.portico', JSON.stringify(body)); };
  const session: CastSession = {
    getCastDevice: () => ({friendlyName: name, label: 'actual-sdk-id'}),
    sendMessage: async (_namespace, command) => {
      const body = command as Record<string, unknown>;
      messages.push(body);
      if (body.type === 'pair') queueMicrotask(() => reply({type: 'paired'}));
      if (body.type === 'load' && acknowledge) queueMicrotask(() => reply({type: 'loaded', itemId: body.itemId, requestId: body.requestId}));
    },
    addMessageListener: (_namespace, listener) => { listeners.add(listener); },
    removeMessageListener: (_namespace, listener) => { listeners.delete(listener); },
    endSession: value => { ended = value; },
  };
  const context = {
    setOptions: (value: unknown) => { options.push(value); },
    requestSession: () => { picks++; return pick ? pick() : Promise.resolve().then(() => { current = session; }); },
    getCurrentSession: () => current,
    addEventListener: (_type: string, listener: (event: CastSessionEvent) => void) => { contextListeners.add(listener); },
    removeEventListener: (_type: string, listener: (event: CastSessionEvent) => void) => { contextListeners.delete(listener); },
  };
  const framework: CastFramework = {CastContext: {getInstance: () => context}, CastContextEventType: {SESSION_STATE_CHANGED: 'session'}, SessionState: {SESSION_RESUMED: 'resumed', SESSION_ENDED: 'ended', SESSION_START_FAILED: 'failed'}};
  const document = {
    querySelector: () => scripts[0],
    createElement: () => {
      const errors = new Set<() => void>();
      const script = {src: '', async: false, errors, addEventListener: (_type: string, fn: () => void) => errors.add(fn), removeEventListener: (_type: string, fn: () => void) => errors.delete(fn), remove: () => { const i = scripts.indexOf(script); if (i >= 0) scripts.splice(i, 1); }};
      return script;
    },
    head: {appendChild: (script: typeof scripts[number]) => { scripts.push(script); }},
  } as unknown as Document;
  const browser: CastBrowser = {document, navigator: {userAgent: 'Mozilla/5.0 Chrome/150.0 Linux'}, chrome: {},
    setTimeout: fn => { timers.set(++timer, fn); return timer; }, clearTimeout: id => { timers.delete(id); },
    requestIdleCallback: fn => { idle.set(++timer, fn); return timer; }, cancelIdleCallback: id => { idle.delete(id); },
  };
  const api = {request: async <T,>(path: string, method?: string, body?: unknown) => { requests.push({path, method, body}); return {bootstrap: {code: 'ABC123'}} as T; }};
  return {browser, framework, messages, requests, options, scripts, timers, idle, api, reply, listeners, contextListeners,
    get picks() { return picks; }, get ended() { return ended; },
    set name(value: string) { name = value; }, set acknowledge(value: boolean) { acknowledge = value; },
    set picker(value: () => Promise<unknown>) { pick = value; },
    select: () => { current = session; },
    ready: () => { browser.cast = {framework}; browser.__onGCastApiAvailable?.(true); },
    event: (sessionState: string) => { for (const listener of [...contextListeners]) listener({sessionState}); },
  };
}
function sender(t: test.TestContext, h = sdk()) {
  const service = new WebCastSender({api: h.api, serverOrigin: 'https://server.example/path', castApplicationId: 'APP123', browser: h.browser});
  t.after(() => service.dispose());
  return {h, service};
}
async function prepared(t: test.TestContext) {
  const result = sender(t);
  const ready = result.service.prepareForPlayer();
  result.h.ready();
  assert.equal(await ready, true);
  return result;
}

test('construction makes no SDK or API request; idle and player-open share one lazy load', async t => {
  const {h, service} = sender(t);
  assert.equal(h.scripts.length, 0); assert.equal(h.requests.length, 0); assert.equal(h.picks, 0);
  service.prepareOnIdle(); service.prepareOnIdle();
  assert.equal(h.idle.size, 1); assert.equal(h.scripts.length, 0);
  const ready = service.prepareForPlayer();
  assert.equal(h.idle.size, 0); assert.equal(h.scripts.length, 1);
  assert.equal(service.prepareForPlayer(), ready);
  assert.equal(h.scripts[0].src, 'https://www.gstatic.com/cv/js/sender/v1/cast_sender.js?loadCastFramework=1');
  h.ready(); assert.equal(await ready, true);
  assert.equal(service.getSnapshot().available, true); assert.equal(h.picks, 0);
  assert.deepEqual(h.options, [{receiverApplicationId: 'APP123', autoJoinPolicy: 'origin_scoped'}]);
});

test('ineligible origins, unconfigured apps and unsupported browsers never load the SDK', async t => {
  for (const [origin, app, ua] of [['http://server.example', 'APP', 'Chrome/1'], ['https://u:p@server.example', 'APP', 'Chrome/1'], ['https://server.example', '', 'Chrome/1'], ['https://server.example', 'APP', 'Version/1 Safari/1']]) {
    const h = sdk(); h.browser.navigator.userAgent = ua;
    const service = new WebCastSender({api: h.api, serverOrigin: origin, castApplicationId: app, browser: h.browser});
    t.after(() => service.dispose()); service.prepareOnIdle();
    assert.equal(await service.prepareForPlayer(), false); assert.equal(h.scripts.length, 0); assert.equal(h.requests.length, 0); assert.equal(h.idle.size, 0);
  }
});

test('without an idle API there is no launch timer; explicit player-open loads', async t => {
  const h = sdk(); delete h.browser.requestIdleCallback;
  const {service} = sender(t, h); service.prepareOnIdle();
  assert.equal(h.timers.size, 0); assert.equal(h.scripts.length, 0);
  const ready = service.prepareForPlayer(); h.ready(); assert.equal(await ready, true);
});

test('SDK timeout and script failure clean only owned state, preserve callbacks and allow retry', async () => {
  const h = sdk(), calls: boolean[] = [];
  const prior = (ready: boolean) => { calls.push(ready); };
  h.browser.__onGCastApiAvailable = prior;
  const first = loadCastSdk(h.browser);
  for (const fail of [...h.timers.values()]) fail();
  assert.equal(await first, undefined); assert.equal(h.scripts.length, 0); assert.equal(h.browser.__onGCastApiAvailable, prior);
  const second = loadCastSdk(h.browser);
  for (const fail of [...h.scripts[0].errors]) fail();
  assert.equal(await second, undefined); assert.equal(h.scripts.length, 0);
  const third = loadCastSdk(h.browser); h.ready();
  assert.equal(await third, h.framework); assert.deepEqual(calls, [true]); assert.equal(h.timers.size, 0); assert.equal(h.browser.__onGCastApiAvailable, prior);
});

test('disposing before SDK completion never attaches context listeners', async t => {
  const {h, service} = sender(t);
  const shared = loadCastSdk(h.browser);
  for (const fail of [...h.scripts[0].errors]) fail();
  await shared;
  const ready = service.prepareForPlayer(); service.dispose(); h.ready();
  assert.equal(await ready, false); assert.equal(h.options.length, 0); assert.equal(h.contextListeners.size, 0);
});

test('a script belonging to another SDK consumer survives a failed load', async () => {
  const h = sdk();
  h.browser.document.head.appendChild(h.browser.document.createElement('script'));
  const script = h.scripts[0];
  const ready = loadCastSdk(h.browser);
  for (const fail of [...script.errors]) fail();
  assert.equal(await ready, undefined);
  assert.equal(h.scripts[0], script, 'a failed loader does not remove a script it did not create');
  assert.equal(script.errors.size, 0);
});

test('the native picker opens synchronously; casting waits for the correlated receiver ACK', async t => {
  const {h, service} = await prepared(t);
  const started = service.start('item-1', {title: 'Pilot', kind: 'episode', mediaUrl: 'https://secret.example/grant'} as never, 42, {queue: {queueId: 'q-1', entryId: 'entry-1'}});
  assert.equal(h.picks, 1, 'picker called inside the gesture before awaiting');
  assert.equal(service.getSnapshot().phase, 'connecting');
  await settle();
  const load = h.messages.find(m => m.type === 'load')!;
  assert(load); assert.notEqual(service.getSnapshot().phase, 'casting');
  assert.deepEqual(load.queue, {queueId: 'q-1', entryId: 'entry-1'});
  assert(!JSON.stringify(h.messages).includes('secret.example'));
  assert.deepEqual(h.requests[0], {path: '/v1/cast/bootstrap', method: 'POST', body: {protocolVersion: '1.0', receiverId: 'actual-sdk-id', displayName: 'Living room'}});
  h.reply({type: 'loaded', requestId: 'stale-id', itemId: 'item-1'});
  assert.notEqual(service.getSnapshot().phase, 'casting');
  h.reply({type: 'loaded', requestId: load.requestId, itemId: 'item-1'});
  assert.equal(await started, true); assert.equal(service.getSnapshot().phase, 'casting');
  assert.deepEqual(service.getSnapshot().device, {name: 'Living room', id: 'actual-sdk-id'});
  assert.equal(service.getSnapshot().receiver, undefined, 'ACK is not a receiver clock');
});

test('SDK names are trimmed and bounded; a missing name omits registration and confirmed device', async t => {
  for (const name of ['   ', `  ${'x'.repeat(80)}  `]) {
    const {h, service} = await prepared(t); h.name = name; h.acknowledge = true;
    assert.equal(await service.start('item', {title: 'Song'}, 0), true);
    const body = (h.requests[0] as {body: Record<string, unknown>}).body;
    if (!name.trim()) { assert(!('displayName' in body)); assert.equal(service.getSnapshot().device, undefined); }
    else { assert.equal(body.displayName, 'x'.repeat(64)); assert.equal(service.getSnapshot().device?.name, 'x'.repeat(64)); }
    service.stop();
  }
});

test('cancel while picker is open resolves immediately and a late selection cannot pair or load', async t => {
  const {h, service} = await prepared(t); let finish!: () => void;
  h.picker = () => new Promise<void>(resolve => { finish = resolve; });
  const started = service.start('item', {title: 'Song'}, 0);
  assert.equal(await service.start('other', {title: 'Other'}, 0), false); assert.equal(h.picks, 1);
  service.cancelConnecting(); assert.equal(await started, false); assert.equal(service.getSnapshot().phase, 'off');
  h.select(); finish(); await settle();
  assert.equal(h.requests.length, 0); assert.equal(h.messages.length, 0);
});

test('picker rejection and canceled pairing never report a successful cast', async t => {
  const {h, service} = await prepared(t);
  h.picker = () => Promise.reject(new Error('picker canceled'));
  assert.equal(await service.start('item', {title: 'Song'}, 0), false); assert.equal(service.getSnapshot().phase, 'off');
  h.picker = async () => { h.select(); };
  const started = service.start('item', {title: 'Song'}, 0); await settle();
  const load = h.messages.find(m => m.type === 'load')!;
  service.stop(); assert.equal(await started, false);
  h.reply({type: 'loaded', itemId: 'item', requestId: load.requestId});
  assert.equal(service.getSnapshot().phase, 'off'); assert.equal(h.listeners.size, 0);
});

test('continuation retains only complete receiver clock facts, never requested seek or idle defaults', async t => {
  const {h, service} = await prepared(t); h.acknowledge = true;
  assert.equal(await service.start('item', {title: 'Song'}, 42), true);
  h.reply({type: 'status', itemId: 'item', positionSeconds: 61, durationSeconds: 180, paused: false});
  assert.equal(service.getSnapshot().receiver?.positionSeconds, 61);
  service.seek(130); assert.equal(service.getSnapshot().positionSeconds, 61);
  for (const status of [
    {itemId: 'item', positionSeconds: 0, paused: false},
    {itemId: 'item', positionSeconds: -1, durationSeconds: 180, paused: false},
    {itemId: 'item', positionSeconds: 0, durationSeconds: 180, paused: 'false'},
  ]) h.reply({type: 'status', ...status});
  assert.equal(service.getSnapshot().receiver?.positionSeconds, 61, 'an incomplete or malformed status is not a clock fact');
  h.reply({type: 'status', itemId: 'next-item', positionSeconds: 3, durationSeconds: 200, paused: true});
  assert.equal(service.getSnapshot().receiver?.itemId, 'next-item');
  // An idle status has no item identity: it ends the cast but supplies no new
  // continuation clock, so the last complete facts remain.
  h.reply({type: 'status', itemId: '', positionSeconds: 0, durationSeconds: 0, paused: false});
  assert.equal(service.getSnapshot().phase, 'off');
  assert.equal(service.getSnapshot().receiver?.itemId, 'next-item');
  service.stop(); assert.deepEqual(service.getSnapshot().receiver, {itemId: 'next-item', positionSeconds: 3, durationSeconds: 200, paused: true});
  assert.equal(service.getSnapshot().positionSeconds, 3);
});

test('takeover replacement retains the last real clock; disposal removes SDK and transport listeners', async t => {
  const {h, service} = await prepared(t); h.acknowledge = true;
  await service.start('item', {title: 'Song'}, 0);
  h.reply({type: 'status', itemId: 'item', positionSeconds: 9, durationSeconds: 180, paused: false});
  h.reply({type: 'replaced'});
  assert.equal(service.getSnapshot().phase, 'replaced'); assert.equal(service.getSnapshot().receiver?.positionSeconds, 9);
  service.dispose(); assert.equal(h.listeners.size, 0); assert.equal(h.contextListeners.size, 0);
});

test('a same-item successor occurrence never borrows the predecessor’s clock', async t => {
  const {h, service} = await prepared(t); h.acknowledge = true;
  assert.equal(await service.start('item', {title: 'Song'}, 0), true);
  const controller = {bindingId: 'binding', controllerGeneration: 1, connectionGeneration: 1};
  const occurrence = {sessionId: 'receiver', generation: 1, itemId: 'item'};
  h.reply({type: 'status', controller, occurrence, itemId: 'item', positionSeconds: 61, durationSeconds: 180, paused: false});
  assert.deepEqual(service.getSnapshot().remoteIdentity, {controller, occurrence});
  assert.deepEqual(service.getSnapshot().receiver, {itemId: 'item', positionSeconds: 61, durationSeconds: 180, paused: false});
  assert.equal(service.getSnapshot().positionSeconds, 61);

  // The receiver's next occurrence of the same item reports without a complete clock. The exact
  // stamps name a different fact, so the predecessor's 61 seconds are not its position.
  const successor = {...occurrence, generation: 2};
  h.reply({type: 'status', controller, occurrence: successor, itemId: 'item'});
  assert.deepEqual(service.getSnapshot().remoteIdentity, {controller, occurrence: successor});
  assert.equal(service.getSnapshot().receiver, undefined);
  assert.equal(service.getSnapshot().positionSeconds, 0);

  // That occurrence's own incomplete status still is not a clock, and cannot erase the complete
  // clock it did report.
  h.reply({type: 'status', controller, occurrence: successor, itemId: 'item', positionSeconds: 12, durationSeconds: 180, paused: false});
  assert.equal(service.getSnapshot().receiver?.positionSeconds, 12);
  h.reply({type: 'status', controller, occurrence: successor, itemId: 'item'});
  assert.equal(service.getSnapshot().receiver?.positionSeconds, 12, 'the same occurrence keeps its last complete clock');

  // A new controller is a different occurrence witness even when it names the same session/item.
  const successorController = {bindingId: 'binding', controllerGeneration: 2, connectionGeneration: 1};
  h.reply({type: 'status', controller: successorController, occurrence: successor, itemId: 'item'});
  assert.deepEqual(service.getSnapshot().remoteIdentity, {controller: successorController, occurrence: successor});
  assert.equal(service.getSnapshot().receiver, undefined, 'a new controller’s clock is unknown until it reports');

  // Stop/replacement still retains the last complete clock for the continuation offer.
  h.reply({type: 'status', controller: successorController, occurrence: successor, itemId: 'item', positionSeconds: 20, durationSeconds: 180, paused: true});
  h.reply({type: 'replaced'});
  assert.equal(service.getSnapshot().phase, 'replaced');
  assert.deepEqual(service.getSnapshot().receiver, {itemId: 'item', positionSeconds: 20, durationSeconds: 180, paused: true});
});

test('a delayed picker reads the current source clock only immediately before starting', async t => {
  const {h, service} = await prepared(t);let finish!: () => void, position = 12, reads = 0;
  h.picker = () => new Promise<void>(resolve => {finish = resolve;});h.acknowledge = true;
  const started = service.start('item', {title: 'Song'}, 12, undefined, () => {reads++;return position;});
  assert.equal(h.picks, 1);assert.equal(reads, 0);
  for (let i = 0; i < 20; i++) {position += 5;await Promise.resolve();}
  assert.equal(reads, 0);assert.equal(h.messages.length, 0);
  position = 900; // A seek while the native picker is still open.
  h.select();finish();assert.equal(await started, true);
  assert.equal(reads, 1);assert.equal(h.messages.find(message => message.type === 'load')?.startSeconds, 900);
});

test('retired source or canceled picker cannot dispatch a start or consult an obsolete source', async t => {
  for (const cancel of [false, true]) {
    const {h, service} = await prepared(t);let finish!: () => void, reads = 0;
    h.picker = () => new Promise<void>(resolve => {finish = resolve;});
    const started = service.start('item', {title: 'Song'}, 12, undefined, () => {reads++;return undefined;});
    if (cancel) service.cancelConnecting();
    h.select();finish();assert.equal(await started, false);await settle();
    assert.equal(reads, cancel ? 0 : 1);assert.equal(h.requests.length, 0);assert.equal(h.messages.length, 0);
    assert.equal(service.getSnapshot().phase, 'off');
  }
});
