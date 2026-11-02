import test from 'node:test';
import assert from 'node:assert/strict';
import {createSetupController, setupErrorOf, type SetupPortContext, type SetupPorts, type SetupServerRead} from '../src/setup-controller.ts';
import {createMemorySetupStore, type SetupAttempt, type SetupLocal, type SetupRecord} from '../src/setup-store.ts';
import type {SetupView} from '../src/setup-machine.ts';
import {createManualScheduler} from '../src/timer.ts';
import {setCleanupLog} from '../src/cleanup.ts';
import {ApiError} from '../src/index.ts';

const WALL = Date.parse('2026-10-31T00:00:00Z');
const SECRET = 'hunter2-secret';
const LIBRARY = {kind: 'movie', name: 'Films', path: '/media/films'};

const record = (requestId = 'r1', authMode: SetupRecord['authMode'] = 'hosted'): SetupRecord =>
  ({version: 2, serverId: 's1', requestId, setupToken: 'token-1', tokenAt: 1, name: 'Den', authMode});
const attempt = (requestId = 'r1', attemptId = 'a1'): SetupAttempt => ({version: 2, serverId: 's1', requestId, attemptId, origin: 'https://den.example', hostedOrigin: 'https://hosted.example',
  installationId: 'i1', state: 'st', verifier: 'v', codeChallenge: 'c', createdAt: 1, expiresAt: '2026-10-31T01:00:00Z'});
const held = (): SetupLocal => ({record: record(), attempt: attempt()});

type Phase = 'started' | 'awaiting_approval' | 'expired' | 'approved' | 'finishing' | 'exhausted' | 'installed' | 'installed_lapsed';
const own = (phase: Phase): SetupServerRead => ({kind: 'state', state: {status: phase === 'expired' ? 'ready' : 'in_progress',
  setup: {phase, requestId: 'r1', revision: '1', cancelling: false, renewable: false}}});
const busy = (retryAfterSeconds?: number): SetupServerRead => ({kind: 'state', state: {status: 'in_progress', ...(retryAfterSeconds ? {retryAfterSeconds} : {})}});
const open: SetupServerRead = {kind: 'state', state: {status: 'ready'}};

function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {resolve = res; reject = rej;});
  return {promise, resolve, reject};
}
const never = () => new Promise<never>(() => {});

type Handlers = Record<string, (...args: any[]) => unknown>;
function rig(options: {local?: SetupLocal; reads?: SetupServerRead[]; handlers?: Handlers; foreground?: boolean; random?: () => number; diagnostic?: (code: string) => void} = {}) {
  const clock = createManualScheduler(), store = createMemorySetupStore(options.local ?? {});
  const calls: {name: string; context?: SetupPortContext; args: unknown[]}[] = [];
  const reads = [...(options.reads ?? [open])];
  const defaults: Handlers = {
    readState: () => reads.length > 1 ? reads.shift() : reads[0],
    start: () => ({kind: 'startDone'}), initializeHosted: () => ({kind: 'initializeDone'}), initializeLocal: () => ({kind: 'initializeDone'}),
    requestApproval: () => ({kind: 'approvalReady', approvalUrl: 'https://hosted.example/claim#x', expiresAt: '2026-10-31T00:30:00Z'}),
    approveInApp: () => ({kind: 'approvedInApp'}), abandon: () => ({kind: 'abandoned'}), startOver: () => ({kind: 'startedOver'}), retryDelivery: () => ({kind: 'deliveryRetried'}),
    exchange: () => ({kind: 'exchangeDone'}), renew: () => ({kind: 'renewDone'}), adoptOwner: () => ({kind: 'ownerAdopted'}), addLibrary: () => ({kind: 'libraryAdded'}),
    navigate: () => undefined,
  };
  const handlers = {...defaults, ...options.handlers};
  const port = (name: string) => (...args: unknown[]) => {
    calls.push({name, args, context: name === 'navigate' ? undefined : args[args.length - 1] as SetupPortContext});
    return Promise.resolve().then(() => handlers[name]!(...args));
  };
  const ports = {store, readState: async (context: SetupPortContext) => ({server: await port('readState')(context), local: await store.read()}), start: port('start'), initializeHosted: port('initializeHosted'), initializeLocal: port('initializeLocal'),
    requestApproval: port('requestApproval'), approveInApp: port('approveInApp'), abandon: port('abandon'), startOver: port('startOver'), retryDelivery: port('retryDelivery'),
    exchange: port('exchange'), renew: port('renew'), adoptOwner: port('adoptOwner'), addLibrary: port('addLibrary'),
    navigate: (url: string) => {calls.push({name: 'navigate', args: [url]}); handlers.navigate!(url);}} as unknown as SetupPorts;
  let ids = 0;
  const controller = createSetupController(ports, {scheduler: clock, wallNow: () => WALL, random: options.random ?? (() => 0), newIntentId: () => `intent-${++ids}`,
    foreground: options.foreground, diagnostic: options.diagnostic as never});
  const seen: SetupView[] = [];
  controller.subscribe(() => seen.push(controller.getSnapshot()));
  const count = (name: string) => calls.filter(c => c.name === name).length;
  const kind = () => controller.getSnapshot().kind;
  const settle = () => clock.advance(0);
  return {clock, store, calls, controller, seen, handlers, count, kind, settle};
}

// ---- the paths ------------------------------------------------------------------------------------------------------------
test('a fresh server shows the form; a hosted submit runs start, initialize and request approval under one intentId, then leaves and polls', async () => {
  const t = rig();
  t.controller.start();
  await t.settle();
  assert.deepEqual(t.controller.getSnapshot(), {kind: 'form', mode: 'choose', name: ''});
  t.controller.submitHosted('Den', {navigate: true});
  await t.settle();
  assert.deepEqual(t.calls.map(c => c.name), ['readState', 'start', 'initializeHosted', 'requestApproval', 'navigate']);
  assert.equal(t.calls[4]!.args[0], 'https://hosted.example/claim#x');
  const keys = t.calls.filter(c => ['start', 'requestApproval'].includes(c.name)).map(c => c.context!.intentId);
  assert.deepEqual(keys, ['intent-2', 'intent-2'], 'one intent, one key (the read before it was the system lane)');
  assert.deepEqual(t.controller.getSnapshot(), {kind: 'awaiting_approval', name: 'Den', serverPhase: 'awaiting_approval', polls: 0,
    approvalUrl: 'https://hosted.example/claim#x', expiresAt: '2026-10-31T00:30:00Z'});
  assert.equal(t.clock.pending(), 1, 'only the next read is pending');
});

test('a local submit runs start, initialize and adopt owner, shows the library and retires the record', async () => {
  const t = rig({local: {record: record('r1', 'local')}, reads: [open]});
  t.controller.start();
  await t.settle();
  t.controller.submitLocal('Den', 'alice', SECRET);
  await t.settle();
  assert.deepEqual(t.calls.map(c => c.name), ['readState', 'start', 'initializeLocal', 'adoptOwner']);
  assert.deepEqual(t.calls[2]!.args.slice(0, 2), ['Den', {username: 'alice', password: SECRET}]);
  assert.equal(t.kind(), 'library');
  assert.deepEqual(t.store.peek(), {});
  t.controller.skip();
  assert.equal(t.kind(), 'done');
});

test('polling carries a hosted setup from waiting through the exchange to the library', async () => {
  const t = rig({local: held(), reads: [own('started'), own('installed')]});
  t.controller.start();
  await t.settle();
  assert.equal(t.kind(), 'awaiting_approval');
  await t.clock.advance(2000);
  assert.deepEqual(t.calls.map(c => c.name), ['readState', 'readState', 'exchange', 'adoptOwner']);
  assert.equal(t.kind(), 'library');
  assert.deepEqual(t.store.peek(), {}, 'the record and the attempt are gone');
  t.controller.addLibrary(LIBRARY);
  await t.settle();
  assert.equal(t.count('addLibrary'), 1);
  assert.deepEqual(t.calls.find(c => c.name === 'addLibrary')!.args[0], LIBRARY, 'the library as entered goes to the port');
  assert.equal(t.kind(), 'done');
});

test('an unreadable record is retired with a diagnostic and the form shows fresh', async () => {
  const notes: string[] = [];
  const t = rig({local: {unreadable: true}, reads: [open], diagnostic: code => notes.push(code)});
  t.controller.start();
  await t.settle();
  assert.equal(t.kind(), 'form');
  assert.deepEqual(notes, ['setup_record_unreadable']);
  assert.deepEqual(t.store.peek(), {});
});

test('a stale own record (the server is open) is retired and the form shows without the earlier-expired line', async () => {
  const t = rig({local: held(), reads: [open]});
  t.controller.start();
  await t.settle();
  assert.deepEqual(t.controller.getSnapshot(), {kind: 'form', mode: 'choose', name: 'Den'});
  assert.deepEqual(t.store.peek(), {});
});

test('Back while Continue is in flight drops the call and abandons what the server may hold; its late answer changes nothing', async () => {
  const late = deferred<unknown>();
  const t = rig({handlers: {requestApproval: () => late.promise}});
  t.controller.start();
  await t.settle();
  t.controller.submitHosted('Den', {navigate: true});
  await t.settle();
  assert.equal(t.kind(), 'starting');
  t.controller.back();
  await t.settle();
  assert.deepEqual(t.calls.map(c => c.name), ['readState', 'start', 'initializeHosted', 'requestApproval', 'abandon']);
  assert.ok(t.calls[3]!.context!.signal.aborted, 'the approval request is dropped');
  assert.deepEqual(t.controller.getSnapshot(), {kind: 'form', mode: 'choose', name: 'Den'});
  late.resolve({kind: 'approvalReady', approvalUrl: 'https://hosted.example/claim#late'});
  await t.clock.advance(60_000);
  assert.equal(t.count('navigate'), 0, 'a late approval link never leaves the page');
  assert.equal(t.kind(), 'form');
});

test('Back while Continue is in flight, and the server had already taken the approval request: the refused abandon re-reads and shows the waiting', async () => {
  const late = deferred<unknown>();
  const t = rig({reads: [open, own('awaiting_approval')], handlers: {requestApproval: () => late.promise, abandon: () => ({kind: 'abandonRefused'})}});
  t.handlers.start = async () => {await t.store.change({}, {record: record()}); return {kind: 'startDone'};};
  t.controller.start();
  await t.settle();
  t.controller.submitHosted('Den', {navigate: false});
  await t.settle();
  t.controller.back();
  await t.settle();
  assert.equal(t.count('abandon'), 1);
  assert.equal(t.kind(), 'awaiting_approval', 'the server\'s word, not the form');
});

test('Back while a local owner is being created is not taken: that runs on to the library', async () => {
  const late = deferred<unknown>();
  const t = rig({handlers: {initializeLocal: () => late.promise}});
  t.controller.start();
  await t.settle();
  t.controller.submitLocal('Den', 'alice', SECRET);
  await t.settle();
  t.controller.back();
  await t.settle();
  assert.equal(t.kind(), 'starting');
  late.resolve({kind: 'initializeDone'});
  await t.settle();
  assert.equal(t.count('abandon'), 0);
  assert.equal(t.kind(), 'library');
});

// ---- cancel, dispose, watchdog --------------------------------------------------------------------------------------------
test('a result that arrives after dispose is ignored, and nothing further runs', async () => {
  const late = deferred<unknown>();
  const t = rig({handlers: {initializeHosted: () => late.promise}});
  t.controller.start();
  await t.settle();
  t.controller.submitHosted('Den', {navigate: true});
  await t.settle();
  assert.equal(t.kind(), 'starting');
  const before = t.controller.getSnapshot();
  t.controller.dispose();
  late.resolve({kind: 'initializeDone'});
  await t.clock.advance(60_000);
  assert.equal(t.controller.getSnapshot(), before);
  assert.equal(t.count('requestApproval'), 0);
  assert.equal(t.clock.pending(), 0, 'no timer survives dispose');
  t.controller.submitHosted('Again', {navigate: true});
  t.controller.back();
  await t.settle();
  assert.equal(t.controller.getSnapshot(), before, 'intents after dispose do nothing');
  t.controller.dispose();
});

test('dispose clears a pending read and a hung read\'s late answer changes nothing', async () => {
  const late = deferred<SetupServerRead>();
  const t = rig({local: held(), reads: [own('started')]});
  t.controller.start();
  await t.settle();
  assert.equal(t.clock.pending(), 1);
  t.handlers.readState = () => late.promise;
  await t.clock.advance(2000);
  assert.equal(t.count('readState'), 2);
  const before = t.controller.getSnapshot();
  t.controller.dispose();
  late.resolve(own('installed'));
  await t.clock.advance(60_000);
  assert.equal(t.controller.getSnapshot(), before);
  assert.equal(t.count('exchange'), 0);
  assert.deepEqual(t.store.peek(), held(), 'the store is untouched by a dropped result');
});

test('the watchdog turns a hung request into the retryable failure; its late result is dropped; Try again keeps the intentId', async () => {
  const first = deferred<unknown>();
  let asked = 0;
  const t = rig({handlers: {requestApproval: () => ++asked === 1 ? first.promise : {kind: 'approvalReady', approvalUrl: 'https://hosted.example/claim#y'}}});
  t.controller.start();
  await t.settle();
  t.controller.submitHosted('Den', {navigate: true});
  await t.settle();
  assert.deepEqual(t.controller.getSnapshot(), {kind: 'starting', mode: 'hosted', name: 'Den', navigate: true, step: 'approval'});
  await t.clock.advance(29_999);
  assert.equal(t.kind(), 'starting', 'the slow flag shows nothing new');
  await t.clock.advance(1);
  const failed = t.controller.getSnapshot() as Extract<SetupView, {kind: 'failed'}>;
  assert.equal(failed.kind, 'failed');
  assert.deepEqual(failed.error, {code: 'unconfirmed', retryable: true});
  assert.equal(failed.hold, 'unapproved');
  first.resolve({kind: 'approvalReady', approvalUrl: 'https://hosted.example/claim#late'});
  await t.settle();
  assert.equal(t.controller.getSnapshot(), failed, 'the late result never publishes');
  assert.equal(t.count('navigate'), 0);
  t.controller.retry();
  await t.settle();
  assert.equal(t.kind(), 'awaiting_approval');
  assert.equal((t.controller.getSnapshot() as {approvalUrl?: string}).approvalUrl, 'https://hosted.example/claim#y');
  const approvals = t.calls.filter(c => c.name === 'requestApproval');
  assert.equal(approvals.length, 2);
  assert.equal(approvals[0]!.context!.intentId, approvals[1]!.context!.intentId, 'Try again reuses the intentId');
  assert.equal(approvals[0]!.context!.signal.aborted, true, 'the abandoned attempt was aborted');
});

test('a thrown failure is shown with its floor and Try again repeats that one step under the same intentId; a new intent gets a new one', async () => {
  let starts = 0;
  const t = rig({handlers: {start: () => {if (++starts === 1) throw new ApiError(503, 'start_unavailable', 'x', true, 4); return {kind: 'startDone'};}}});
  t.controller.start();
  await t.settle();
  t.controller.submitHosted('Den', {navigate: false});
  await t.settle();
  const failed = t.controller.getSnapshot() as Extract<SetupView, {kind: 'failed'}>;
  assert.deepEqual(failed.error, {code: 'start_unavailable', retryable: true, retryAfterMs: 4000});
  t.controller.retry();
  await t.settle();
  assert.equal(t.kind(), 'awaiting_approval');
  const [a, b] = t.calls.filter(c => c.name === 'start');
  assert.equal(a!.context!.intentId, b!.context!.intentId);
  t.controller.back();
  await t.settle();
  t.controller.submitHosted('Den', {navigate: false});
  await t.settle();
  const keys = t.calls.filter(c => c.name === 'start').map(c => c.context!.intentId);
  assert.equal(new Set(keys).size, 2);
});

test('a read that never answers does not spin forever: the first read times out into Try again', async () => {
  let reads = 0;
  const t = rig({handlers: {readState: () => ++reads === 1 ? never() : open}});
  t.controller.start();
  await t.settle();
  assert.equal(t.kind(), 'connecting');
  await t.clock.advance(30_000);
  const failed = t.controller.getSnapshot() as Extract<SetupView, {kind: 'failed'}>;
  assert.equal(failed.kind, 'failed');
  assert.equal(failed.error.code, 'unconfirmed');
  t.controller.retry();
  await t.settle();
  assert.equal(t.kind(), 'form');
});

test('a double tap submits once', async () => {
  const gate = deferred<unknown>();
  const t = rig({handlers: {start: () => gate.promise}});
  t.controller.start();
  await t.settle();
  t.controller.submitHosted('Den', {navigate: true});
  t.controller.submitHosted('Other', {navigate: true});
  await t.settle();
  assert.equal(t.count('start'), 1);
  gate.resolve({kind: 'startDone'});
  await t.settle();
  assert.equal(t.count('initializeHosted'), 1);
  assert.equal(t.calls.find(c => c.name === 'initializeHosted')!.args[0], 'Den');
});

// ---- the read timer -------------------------------------------------------------------------------------------------------
test('a server Retry-After is a floor: no read before it, with or without jitter on top', async () => {
  for (const [random, due] of [[() => 0, 30_000], [() => 1, 33_000]] as const) {
    const t = rig({reads: [busy(30)], random});
    t.controller.start();
    await t.settle();
    assert.deepEqual(t.controller.getSnapshot(), {kind: 'busy_elsewhere', polls: 0, retryAt: WALL + 30_000});
    await t.clock.advance(due - 1);
    assert.equal(t.count('readState'), 1, `still waiting at ${due - 1} ms`);
    await t.clock.advance(1);
    assert.equal(t.count('readState'), 2, `read at ${due} ms`);
    t.controller.dispose();
  }
});

test('with no floor from the server the reducer\'s own backoff paces the reads: 2 s, then 4 s', async () => {
  const t = rig({local: held(), reads: [own('started')]});
  t.controller.start();
  await t.settle();
  await t.clock.advance(1999);
  assert.equal(t.count('readState'), 1);
  await t.clock.advance(1);
  assert.equal(t.count('readState'), 2);
  await t.clock.advance(3999);
  assert.equal(t.count('readState'), 2);
  await t.clock.advance(1);
  assert.equal(t.count('readState'), 3);
});

test('no read runs while backgrounded; the held read is neither lost nor early when the app comes back', async () => {
  const t = rig({local: held(), reads: [own('started')]});
  t.controller.start();
  await t.settle();
  t.controller.setForeground(false);
  await t.clock.advance(60_000);
  assert.equal(t.count('readState'), 1, 'nothing while backgrounded');
  assert.equal(t.clock.pending(), 0, 'and no timer is left running');
  t.controller.setForeground(true);
  await t.settle();
  assert.equal(t.count('readState'), 2, 'overdue, so it reads at once');

  const early = rig({local: held(), reads: [own('started')]});
  early.controller.start();
  await early.settle();
  early.controller.setForeground(false);
  await early.clock.advance(1000);
  early.controller.setForeground(true);
  await early.clock.advance(999);
  assert.equal(early.count('readState'), 1, 'back at 1 s: the 2 s still has 1 s to go');
  await early.clock.advance(1);
  assert.equal(early.count('readState'), 2);
});

test('a new schedule replaces the pending read: one timer, the later due time', async () => {
  const t = rig({local: held(), reads: [own('started'), own('approved')]});
  t.controller.start();
  await t.settle();
  await t.clock.advance(1000);
  t.controller.approveInApp();
  await t.settle();
  assert.equal(t.kind(), 'finishing');
  assert.equal(t.clock.pending(), 1);
  await t.clock.advance(1000);
  assert.equal(t.count('readState'), 1, 'the replaced 2 s read never runs');
  await t.clock.advance(1000);
  assert.equal(t.count('readState'), 2, 'the new one runs 2 s after the approve');
});

// ---- Back ----------------------------------------------------------------------------------------------------------------
test('Back on an unapproved attempt abandons it under the intentId, retires the record and attempt, and shows the form', async () => {
  const t = rig({local: held(), reads: [own('awaiting_approval')]});
  t.controller.start();
  await t.settle();
  assert.equal(t.kind(), 'awaiting_approval');
  t.controller.back();
  await t.settle();
  assert.equal(t.count('abandon'), 1);
  assert.match(t.calls.find(c => c.name === 'abandon')!.context!.intentId, /^intent-\d+$/);
  assert.deepEqual(t.store.peek(), {});
  assert.deepEqual(t.controller.getSnapshot(), {kind: 'form', mode: 'choose', name: 'Den'});
  t.controller.chooseKind('local');
  assert.deepEqual(t.controller.getSnapshot(), {kind: 'form', mode: 'local', name: 'Den'}, 'both kinds are open to pick');
});

test('Back after approval is refused by the server\'s phase: nothing is abandoned and the server\'s phase is shown', async () => {
  const t = rig({local: held(), reads: [own('approved'), own('exhausted')]});
  t.controller.start();
  await t.settle();
  assert.equal(t.kind(), 'finishing');
  t.controller.back();
  await t.settle();
  assert.equal(t.count('abandon'), 0);
  assert.equal(t.kind(), 'exhausted');
  assert.deepEqual(t.store.peek(), held(), 'the approved attempt keeps its record');
});

test('a Back whose abandon the server refuses re-reads and shows the phase it is really in', async () => {
  const t = rig({local: held(), reads: [own('awaiting_approval'), own('finishing')], handlers: {abandon: () => ({kind: 'abandonRefused'})}});
  t.controller.start();
  await t.settle();
  t.controller.back();
  await t.settle();
  assert.equal(t.count('abandon'), 1);
  assert.equal(t.kind(), 'finishing');
  assert.deepEqual(t.store.peek(), held());
});

test('a failed abandon is a failure with Try again, which repeats the abandon under the same intentId', async () => {
  let abandons = 0;
  const t = rig({local: held(), reads: [own('awaiting_approval')], handlers: {abandon: () => {if (++abandons === 1) throw new TypeError('offline'); return {kind: 'abandoned'};}}});
  t.controller.start();
  await t.settle();
  t.controller.back();
  await t.settle();
  const failed = t.controller.getSnapshot() as Extract<SetupView, {kind: 'failed'}>;
  assert.deepEqual([failed.kind, failed.error.code, failed.hold], ['failed', 'network', 'unapproved']);
  t.controller.retry();
  await t.settle();
  const [a, b] = t.calls.filter(c => c.name === 'abandon');
  assert.equal(a!.context!.intentId, b!.context!.intentId);
  assert.equal(t.kind(), 'form');
  assert.deepEqual(t.store.peek(), {});
});

test('compare-and-swap: a record another tab wrote in the meantime survives the retire', async () => {
  const t = rig({local: held(), reads: [own('awaiting_approval')], handlers: {}});
  t.handlers.abandon = async () => {
    assert.equal(await t.store.change({requestId: 'r1'}, {record: record('r2')}), true, 'the other tab wins the swap');
    return {kind: 'abandoned'};
  };
  t.controller.start();
  await t.settle();
  t.controller.back();
  await t.settle();
  assert.equal(t.store.peek().record?.requestId, 'r2', 'the successor is preserved');
  assert.equal(t.store.peek().attempt, undefined, 'the attempt we knew was ours to retire');
  assert.equal(t.kind(), 'form');
});

test('the memory store refuses a change when the expected id is not the stored one, and writes nothing', async () => {
  const store = createMemorySetupStore(held());
  assert.equal(await store.change({requestId: 'other', attemptId: 'a1'}, {record: null, attempt: null}), false);
  assert.deepEqual(store.peek(), held());
  assert.equal(await store.change({attemptId: 'a1'}, {attempt: null}), true);
  assert.deepEqual(store.peek(), {record: record()});
  assert.equal(await store.change({}, {record: null}), false, 'a record is there, so "expect none" fails');
  assert.equal(await createMemorySetupStore({unreadable: true}).change({}, {record: null}), true, 'unreadable bytes may be replaced');
});

// ---- credentials ---------------------------------------------------------------------------------------------------------
test('credentials reach only the S5 port call: never a snapshot, the store, a log or an error code, and Try again still has them', async () => {
  const logged: unknown[][] = [];
  const spies = (['log', 'warn', 'error', 'info', 'debug'] as const).map(name => {const original = console[name]; console[name] = (...args: unknown[]) => {logged.push(args);}; return () => {console[name] = original;};});
  try {
    let tries = 0;
    const t = rig({handlers: {initializeLocal: () => {if (++tries === 1) throw new TypeError(`the server said ${SECRET} is not allowed`); return {kind: 'initializeDone'};}}});
    t.controller.start();
    await t.settle();
    t.controller.submitLocal('Den', 'alice', SECRET);
    await t.settle();
    assert.equal(t.kind(), 'failed');
    t.controller.retry();
    await t.settle();
    assert.equal(t.kind(), 'library');
    const seen = [...t.seen, t.controller.getSnapshot()];
    assert.ok(seen.length >= 4);
    const text = JSON.stringify([seen, t.store.peek(), logged]);
    assert.ok(!text.includes(SECRET) && !text.includes('alice'), 'no snapshot, record or log carries the credentials');
    const tried = t.calls.filter(c => c.name === 'initializeLocal');
    assert.deepEqual(tried.map(c => c.args[1]), [{username: 'alice', password: SECRET}, {username: 'alice', password: SECRET}]);
    assert.ok(Object.isFrozen(seen[1]), 'snapshots are frozen');
  } finally {
    spies.forEach(restore => restore());
  }
});

test('leaving a failed local submit forgets the credentials', async () => {
  const t = rig({handlers: {initializeLocal: () => {throw new TypeError('offline');}}});
  t.controller.start();
  await t.settle();
  t.controller.submitLocal('Den', 'alice', SECRET);
  await t.settle();
  assert.equal(t.kind(), 'failed');
  t.controller.back();
  await t.settle();
  assert.equal(t.kind(), 'form');
  t.handlers.initializeLocal = () => ({kind: 'initializeDone'});
  t.controller.submitHosted('Den', {navigate: false});
  await t.settle();
  assert.equal(t.count('initializeLocal'), 1, 'it was only ever asked for the first time');
});

test('setupErrorOf keeps a plain code, the retry hint and the floor, and never the message', () => {
  assert.deepEqual(setupErrorOf(new ApiError(429, 'rate_limited', `leaks ${SECRET}`, true, 7)), {code: 'rate_limited', retryable: true, retryAfterMs: 7000});
  assert.deepEqual(setupErrorOf(new TypeError('fetch failed')), {code: 'network', retryable: true});
  assert.deepEqual(setupErrorOf(new Error('boom')), {code: 'failed', retryable: false});
  assert.deepEqual(setupErrorOf(Object.assign(new Error('x'), {code: 'Not A Token!'})), {code: 'failed', retryable: false});
  assert.deepEqual(setupErrorOf(undefined), {code: 'failed', retryable: false});
});

// ---- the rest ------------------------------------------------------------------------------------------------------------
test('a failed add-library is shown, and Try again repeats it under the same intentId', async () => {
  let adds = 0;
  const t = rig({handlers: {addLibrary: () => {if (++adds === 1) throw new ApiError(500, 'library_failed', 'x', true); return {kind: 'libraryAdded'};}}});
  t.controller.start();
  await t.settle();
  t.controller.submitLocal('Den', 'alice', SECRET);
  await t.settle();
  t.controller.addLibrary(LIBRARY);
  await t.settle();
  assert.equal(t.kind(), 'failed');
  t.controller.retry();
  await t.settle();
  assert.equal(t.kind(), 'done');
  const [a, b] = t.calls.filter(c => c.name === 'addLibrary');
  assert.equal(a!.context!.intentId, b!.context!.intentId);
});

test('the native in-app approve leaves for finishing, and an exhausted delivery retries under one key', async () => {
  const t = rig({local: held(), reads: [own('started'), own('exhausted'), own('finishing')]});
  t.controller.start();
  await t.settle();
  t.controller.approveInApp();
  await t.settle();
  assert.equal(t.count('approveInApp'), 1);
  assert.equal(t.kind(), 'finishing');
  await t.clock.advance(2000);
  assert.equal(t.kind(), 'exhausted');
  t.controller.retry();
  await t.settle();
  assert.equal(t.count('retryDelivery'), 1);
  assert.equal(t.kind(), 'finishing');
  assert.equal(t.controller.getSnapshot().kind === 'finishing' && (t.controller.getSnapshot() as {slow: boolean}).slow, true);
});

test('the snapshot is frozen, identity-stable between changes, and a listener that throws or a navigate that throws breaks nothing', async () => {
  setCleanupLog(() => {});
  try {
    const t = rig({handlers: {navigate: () => {throw new Error('no window');}}});
    t.controller.subscribe(() => {throw new Error('bad listener');});
    let notified = 0;
    const off = t.controller.subscribe(() => {notified++;});
    t.controller.start();
    await t.settle();
    const form = t.controller.getSnapshot();
    assert.equal(t.controller.getSnapshot(), form);
    assert.ok(Object.isFrozen(form));
    const before = notified;
    t.controller.chooseKind('choose');
    assert.equal(t.controller.getSnapshot(), form, 'no change, no new snapshot');
    assert.equal(notified, before);
    t.controller.chooseKind('local');
    assert.notEqual(t.controller.getSnapshot(), form);
    assert.equal(notified, before + 1);
    t.controller.chooseKind('hosted');
    t.controller.submitHosted('Den', {navigate: true});
    await t.settle();
    assert.equal(t.kind(), 'awaiting_approval');
    assert.ok(Object.isFrozen(t.controller.getSnapshot()));
    off();
    const quiet = notified;
    t.controller.back();
    await t.settle();
    assert.equal(notified, quiet, 'unsubscribed');
    t.controller.dispose();
  } finally {
    setCleanupLog((label, error) => console.warn(`Cleanup failed: ${label}`, error));
  }
});

test('the port is told when a read fails to even start: a store that throws is a read failure, not a crash', async () => {
  const t = rig();
  t.store.read = () => Promise.reject(new Error('disk'));
  t.controller.start();
  await t.settle();
  const failed = t.controller.getSnapshot() as Extract<SetupView, {kind: 'failed'}>;
  assert.deepEqual([failed.kind, failed.error.code, failed.hold], ['failed', 'failed', 'none']);
});

test('O-169: Back after initialize, a changed remote choice and Continue send the new choice', async () => {
  const t = rig();
  t.controller.start();
  await t.settle();
  t.controller.submitHosted('Den', {navigate: false, remote: {remoteAccess: 'on', remoteSecurity: 'enhanced'}});
  await t.settle();
  const inits = () => t.calls.filter(c => c.name === 'initializeHosted');
  assert.equal(inits().length, 1);
  assert.deepEqual(inits()[0]!.args.slice(0, 2), ['Den', {remoteAccess: 'on', remoteSecurity: 'enhanced'}]);
  t.controller.back();
  await t.settle();
  assert.equal(t.kind(), 'form', 'Back returns to the form');
  t.controller.submitHosted('Den', {navigate: false, remote: {remoteAccess: 'private', remoteSecurity: 'standard'}});
  await t.settle();
  assert.equal(inits().length, 2);
  assert.deepEqual(inits()[1]!.args.slice(0, 2), ['Den', {remoteAccess: 'private', remoteSecurity: 'standard'}]);
});

test('start over from an approved setup that can no longer finish: the port ends it, the record and attempt retire, and the form shows both kinds', async () => {
  const t = rig({local: held(), reads: [own('exhausted')]});
  t.controller.start();
  await t.settle();
  assert.equal(t.kind(), 'exhausted');
  t.controller.startOver();
  await t.settle();
  assert.equal(t.count('startOver'), 1);
  assert.deepEqual(t.controller.getSnapshot(), {kind: 'form', mode: 'choose', name: ''});
  assert.deepEqual(t.store.peek(), {}, 'the old record and attempt are gone');
});

test('a failed start over offers Try again, which repeats it under the same intentId', async () => {
  let calls = 0;
  const t = rig({local: held(), reads: [own('exhausted')], handlers: {startOver: () => {if (++calls === 1) throw new TypeError('offline'); return {kind: 'startedOver'};}}});
  t.controller.start();
  await t.settle();
  t.controller.startOver();
  await t.settle();
  const failed = t.controller.getSnapshot() as Extract<SetupView, {kind: 'failed'}>;
  assert.deepEqual([failed.kind, failed.error.code], ['failed', 'network']);
  t.controller.retry();
  await t.settle();
  const [a, b] = t.calls.filter(c => c.name === 'startOver');
  assert.equal(a!.context!.intentId, b!.context!.intentId);
  assert.equal(t.kind(), 'form');
});

test('start over on a server that was set up meanwhile says so', async () => {
  const t = rig({local: held(), reads: [own('exhausted')], handlers: {startOver: () => ({kind: 'alreadyComplete'})}});
  t.controller.start();
  await t.settle();
  t.controller.startOver();
  await t.settle();
  assert.deepEqual(t.controller.getSnapshot(), {kind: 'refused', reason: 'already_set_up'});
});
