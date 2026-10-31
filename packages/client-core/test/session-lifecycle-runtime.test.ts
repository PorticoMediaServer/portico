import test from 'node:test';
import assert from 'node:assert/strict';
import {ApiError} from '../src/index.ts';
import {setCleanupLog} from '../src/cleanup.ts';
import {createManualScheduler} from '../src/timer.ts';
import {CONNECT_DEADLINE_MS, SessionLifecycle, type LifecyclePorts, type SelectInput} from '../src/session-lifecycle-runtime.ts';
import type {ConnectedFacts, ConnectFailure, HeldSignIn, PickerProfile, SelectedFacts, ServerRef, ViewerRef} from '../src/session-lifecycle.ts';

// ---- fakes ----------------------------------------------------------------------------------

const S1: ServerRef = {serverId: 's1', name: 'Home', signIn: 'local'};
const S2: ServerRef = {serverId: 's2', name: 'Cabin', signIn: 'local'};
const viewer = (profileId = 'main', familyId = 'f1'): ViewerRef => ({accountId: 'a1', profileId, role: 'owner', familyId});
const MAIN: PickerProfile = {id: 'main', name: 'Main', primary: true, pinRequired: true};
const KIDS: PickerProfile = {id: 'kids', name: 'Kids', primary: false, pinRequired: false};
const TEEN: PickerProfile = {id: 'teen', name: 'Teen', primary: false, pinRequired: true};
const PROFILES = [MAIN, KIDS, TEEN];
const held = (over: Partial<HeldSignIn> = {}): HeldSignIn => ({server: S1, viewer: viewer(), remembered: false, profilePinRequired: true, ...over});
const facts = (): ConnectedFacts => ({profiles: PROFILES, rememberProfiles: true});

const flush = async () => {for (let i = 0; i < 6; i++) await new Promise(resolve => setImmediate(resolve));};

function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {resolve = res; reject = rej;});
  return {promise, resolve, reject};
}
/** A connect that only ends when its signal aborts, like a real request. */
const hang = (signal: AbortSignal) => new Promise<never>((_, reject) => signal.addEventListener('abort', () => reject(new Error('aborted')), {once: true}));

type Fake = LifecyclePorts & {calls: string[]; selects: SelectInput[]; reads: {n: number}};

function fake(over: Partial<LifecyclePorts> = {}): Fake {
  const calls: string[] = [], selects: SelectInput[] = [], reads = {n: 0};
  const base: LifecyclePorts = {
    readRecords: async () => ({outcome: 'ok' as const, servers: 1, held: held()}),
    resetRecords: async () => {calls.push('resetRecords');},
    readHeld: async () => {reads.n++; calls.push('readHeld'); return {servers: 1, held: held()};},
    connect: async serverId => {calls.push('connect:' + serverId); return facts();},
    select: async input => {
      selects.push(input); calls.push(`select:${input.profileId}`);
      const selected: SelectedFacts = {viewer: viewer(input.profileId, 'f2'), remembered: input.remember};
      return selected;
    },
    storeSelect: async serverId => {calls.push('storeSelect:' + serverId);},
    signOutServer: async (serverId, familyId, removeDownloads) => {calls.push(`signOutServer:${serverId}:${familyId}:${removeDownloads}`); return {servers: 0};},
    signOutAccount: async () => {calls.push('signOutAccount'); return {servers: 0};},
    forgetServer: async serverId => {calls.push('forgetServer:' + serverId); return {servers: 0};},
    clearMarker: async serverId => {calls.push('clearMarker:' + serverId);},
    fenceShell: () => {calls.push('fence');},
    drainRevocations: serverId => {calls.push('drain:' + serverId);},
    observeRecords: () => () => {},
    classifyConnect: () => undefined,
  };
  return Object.assign({}, base, over, {calls, selects, reads});
}

const kind = (life: SessionLifecycle) => life.getSnapshot().state.kind;
const state = <K extends string>(life: SessionLifecycle, expected: K) => {
  const s = life.getSnapshot().state;
  assert.equal(s.kind, expected);
  return s as Extract<ReturnType<SessionLifecycle['getSnapshot']>['state'], {kind: K}>;
};

async function launched(over: Partial<LifecyclePorts> = {}) {
  const ports = fake(over), life = new SessionLifecycle(ports);
  life.start(); await flush();
  return {ports, life};
}

// ---- connect --------------------------------------------------------------------------------

test('the 15 s connect deadline leads to offline', async () => {
  const scheduler = createManualScheduler(0);
  const {life} = await launched({scheduler, connect: (_id, signal) => hang(signal)});
  assert.equal(kind(life), 'connecting');
  await scheduler.advance(CONNECT_DEADLINE_MS - 1);
  assert.equal(kind(life), 'connecting', 'not yet at 14.999 s');
  await scheduler.advance(1);
  const s = state(life, 'offline');
  assert.equal(s.downloads, false);
  assert.deepEqual(s.viewer, viewer());
  assert.equal(scheduler.pending(), 0, 'the deadline timer is cleared');
  assert.equal(life.history().at(-1)?.row, 13);
  life.dispose();
});

test('the deadline is 15 s', () => assert.equal(CONNECT_DEADLINE_MS, 15_000));

test('a connect that answers in time cancels the deadline', async () => {
  const scheduler = createManualScheduler(0);
  const {life} = await launched({scheduler});
  assert.equal(kind(life), 'choosingProfile');
  assert.equal(scheduler.pending(), 0);
  await scheduler.advance(60_000);
  assert.equal(kind(life), 'choosingProfile');
});

const classified: ReadonlyArray<readonly [string, ConnectFailure, string]> = [
  ['identity', {reason: 'identity', code: 'certificate_untrusted'}, 'identityFailed'],
  ['sign-in ended', {reason: 'signInEnded', username: 'justin'}, 'signInEnded'],
  ['offline', {reason: 'offline'}, 'offline'],
];
for (const [name, failure, expected] of classified) {
  test(`classifyConnect: ${name} → ${expected}`, async () => {
    const {life} = await launched({connect: async () => {throw new Error('boom');}, classifyConnect: () => failure});
    const s = state(life, expected);
    if (failure.reason === 'identity') assert.equal((s as {code: string}).code, failure.code);
    if (failure.reason === 'signInEnded') assert.equal((s as {username: string}).username, 'justin');
    life.dispose();
  });
}

test('classifyConnect: pinReentry → enteringPin(reentry), marker cleared, same viewer', async () => {
  const {life, ports} = await launched({readRecords: async () => ({outcome: 'ok', servers: 1, held: held({remembered: true})}),
    connect: async () => {throw new ApiError(403, 'profile_pin_reentry', 'again');}, classifyConnect: () => ({reason: 'pinReentry'})});
  const s = state(life, 'enteringPin');
  assert.equal(s.reason, 'reentry');
  assert.deepEqual(s.viewer, viewer());
  assert.ok(ports.calls.includes('clearMarker:s1'));
  life.dispose();
});

test('an unclassified or throwing classifier reads as offline', async () => {
  const a = await launched({connect: async () => {throw new Error('boom');}, classifyConnect: () => undefined});
  assert.equal(kind(a.life), 'offline');
  const b = await launched({connect: async () => {throw new Error('boom');}, classifyConnect: () => {throw new Error('classifier bug');}});
  assert.equal(kind(b.life), 'offline');
});

test('a connect result that arrives after the generation moved is dropped', async () => {
  const first = deferred<ConnectedFacts>(), second = deferred<ConnectedFacts>();
  let listener = () => {};
  let n = 0, readNow: () => Promise<{servers: number; held?: HeldSignIn}> = async () => ({servers: 1, held: held()});
  const {life} = await launched({
    connect: async serverId => {n++; return (serverId === 's1' ? first : second).promise;},
    observeRecords: l => {listener = l; return () => {};},
    readHeld: () => readNow(),
  });
  assert.equal(kind(life), 'connecting');
  const before = life.getSnapshot().generation;
  // Another owner switched the selected server while s1 was still connecting.
  readNow = async () => ({servers: 2, held: held({server: S2, viewer: viewer('main', 'g1')})});
  listener(); await flush();
  const s = state(life, 'connecting');
  assert.equal(s.server.serverId, 's2');
  assert.equal(life.getSnapshot().generation, before + 1);
  assert.equal(n, 2);
  // The old server answers late with profiles: it must not publish.
  first.resolve(facts()); await flush();
  assert.equal(state(life, 'connecting').server.serverId, 's2');
  assert.equal(life.history().filter(r => r.event === 'connected').length, 0);
  // The new server answers: that one does.
  second.resolve(facts()); await flush();
  assert.equal(state(life, 'choosingProfile').server.serverId, 's2');
  assert.equal(life.history().filter(r => r.event === 'connected').length, 1);
  life.dispose();
});

test('a connect failure that arrives after the generation moved is dropped too', async () => {
  const first = deferred<ConnectedFacts>(), second = deferred<ConnectedFacts>();
  let listener = () => {}, target = 's1';
  const {life} = await launched({
    connect: async serverId => (serverId === 's1' ? first : second).promise,
    observeRecords: l => {listener = l; return () => {};},
    readHeld: async () => ({servers: 2, held: held({server: target === 's1' ? S1 : S2, viewer: viewer('main', 'g1')})}),
  });
  target = 's2'; listener(); await flush();
  first.reject(new Error('late')); await flush();
  assert.equal(state(life, 'connecting').server.serverId, 's2');
  assert.equal(life.history().filter(r => r.event === 'connectFailed').length, 0);
  life.dispose();
});

test('resume() from offline connects again', async () => {
  let n = 0;
  const {life} = await launched({connect: async () => {n++; if (n === 1) throw new Error('down'); return facts();}});
  assert.equal(kind(life), 'offline');
  life.resume(); await flush();
  assert.equal(n, 2);
  life.dispose();
});

// Rows 14 and 32: offline and identityFailed keep the held sign-in, so a successful retry moves on.
test('a successful retry from offline reaches the profile picker (the held sign-in survives offline)', async () => {
  let n = 0;
  const {life} = await launched({connect: async () => {n++; if (n === 1) throw new Error('down'); return facts();}});
  assert.equal(kind(life), 'offline');
  life.resume(); await flush();
  assert.equal(kind(life), 'choosingProfile');
  life.dispose();
});

test('a successful retry from identityFailed reaches the profile picker (row 32)', async () => {
  let n = 0;
  const {life} = await launched({connect: async () => {n++; if (n === 1) throw new Error('bad cert'); return facts();},
    classifyConnect: () => ({reason: 'identity', code: 'identity_mismatch'})});
  assert.equal(kind(life), 'identityFailed');
  life.resume(); await flush();
  assert.equal(kind(life), 'choosingProfile');
  life.dispose();
});

// Row 33: a sign-in that already chose its profile (continuation 'viewer') opens it once connected.
test("adoptSignedIn with continuation 'viewer' ends in ready (row 33)", async () => {
  const {life} = await launched({readRecords: async () => ({outcome: 'ok', servers: 0})});
  assert.equal(kind(life), 'signedOut');
  life.actions.adoptSignedIn.start(held(), 'viewer'); await flush();
  assert.equal(kind(life), 'ready');
  life.dispose();
});

test('adoptSignedIn from signedOut connects and then shows the picker for profile-selection', async () => {
  const {life, ports} = await launched({readRecords: async () => ({outcome: 'ok', servers: 0})});
  assert.equal(kind(life), 'signedOut');
  const before = life.getSnapshot().generation;
  life.actions.adoptSignedIn.start(held({server: S2}), 'profile-selection'); await flush();
  assert.equal(state(life, 'choosingProfile').server.serverId, 's2');
  assert.equal(life.getSnapshot().generation, before + 1);
  assert.ok(ports.calls.includes('connect:s2'));
  assert.equal(life.actions.adoptSignedIn.getSnapshot().phase, 'done');
  life.dispose();
});

// ---- selecting and PINs -----------------------------------------------------------------------

test('a select result after dispose() does not publish', async () => {
  const pending = deferred<SelectedFacts>();
  const {life} = await launched({select: () => pending.promise});
  let notified = 0;
  life.subscribe(() => {notified++;});
  life.actions.pickProfile.start('kids'); await flush();
  assert.equal(life.getSnapshot().pending, 'pick');
  const snapshot = life.getSnapshot();
  const historyLength = life.history().length;
  notified = 0;
  life.dispose();
  pending.resolve({viewer: viewer('kids', 'f2'), remembered: false}); await flush();
  assert.equal(life.getSnapshot(), snapshot, 'snapshot untouched');
  assert.equal(life.history().length, historyLength, 'nothing recorded');
  assert.equal(notified, 0, 'no listener called');
  assert.equal(life.subscribe(() => {notified++;})(), undefined);
  life.start(); life.resume(); await flush();
  assert.equal(life.history().length, historyLength, 'a disposed lifecycle stays inert');
});

test('a select failure after dispose() does not publish or throw', async () => {
  const pending = deferred<SelectedFacts>();
  const {life} = await launched({select: () => pending.promise});
  life.actions.pickProfile.start('kids'); await flush();
  life.dispose();
  const length = life.history().length;
  pending.reject(new ApiError(500, 'server_error', 'no')); await flush();
  assert.equal(life.history().length, length);
});

test('picking another profile with no PIN signs in as it with remember false and fences the shell', async () => {
  const {life, ports} = await launched({readRecords: async () => ({outcome: 'ok', servers: 1, held: held({remembered: true})})});
  assert.equal(kind(life), 'ready');
  life.switchProfile();
  life.actions.pickProfile.start('kids'); await flush();
  assert.equal(ports.selects.length, 1);
  assert.equal(ports.selects[0]!.serverId, 's1');
  assert.equal(ports.selects[0]!.profileId, 'kids');
  assert.equal(ports.selects[0]!.remember, false);
  assert.equal(ports.selects[0]!.pin, undefined);
  const s = state(life, 'ready');
  assert.deepEqual(s.viewer, viewer('kids', 'f2'));
  assert.equal(s.remembered, false);
  assert.ok(ports.calls.includes('fence') && ports.calls.includes('clearMarker:s1'));
  assert.deepEqual(life.authority(), {serverId: 's1', accountId: 'a1', profileId: 'kids', familyId: 'f2'});
  life.dispose();
});

const wrong: ReadonlyArray<readonly [string, ApiError, number | undefined]> = [
  ['403 profile_pin_invalid with a floor', new ApiError(403, 'profile_pin_invalid', 'no', false, 2), 2000],
  ['429 profile_pin_locked with retryAfterSeconds', new ApiError(429, 'profile_pin_locked', 'locked', true, 300), 300_000],
  ['403 profile_pin_invalid with no floor', new ApiError(403, 'profile_pin_invalid', 'no'), undefined],
];
for (const [name, error, floorMs] of wrong) {
  test(`wrong PIN: ${name} → enteringPin with waitUntil = now + floor; submitPin fails`, async () => {
    const scheduler = createManualScheduler(1000);
    const {life} = await launched({scheduler, select: async () => {throw error;}});
    life.actions.pickProfile.start('teen'); await flush();
    assert.equal(kind(life), 'enteringPin');
    await scheduler.advance(500);
    const now = scheduler.now();
    life.actions.submitPin.start('0000', false); await flush();
    const s = state(life, 'enteringPin');
    assert.equal(s.rejected, true);
    assert.equal(s.waitUntil, floorMs === undefined ? undefined : now + floorMs);
    assert.equal(life.getSnapshot().pending, undefined);
    const action = life.actions.submitPin.getSnapshot();
    assert.equal(action.phase, 'failed');
    if (action.phase === 'failed') {
      assert.equal(action.failure.cause, error);
      assert.equal(action.failure.retryAfterMs, floorMs);
    }
    assert.equal(life.history().filter(r => r.event === 'pinRejected').length, 1);
    life.dispose();
  });
}

test('a wrong PIN can be retried: the second try succeeds and the screen becomes ready', async () => {
  let n = 0;
  const {life} = await launched({select: async input => {
    if (++n === 1) throw new ApiError(403, 'profile_pin_invalid', 'no');
    return {viewer: viewer(input.profileId, 'f2'), remembered: input.remember};
  }});
  life.actions.pickProfile.start('teen'); await flush();
  life.actions.submitPin.start('0000', true); await flush();
  assert.equal(kind(life), 'enteringPin');
  life.actions.submitPin.start('1234', true); await flush();
  const s = state(life, 'ready');
  assert.equal(s.remembered, true);
  assert.equal(life.actions.submitPin.getSnapshot().phase, 'done');
  life.dispose();
});

test('a server error on select leaves the PIN screen (row 36) and fails the action', async () => {
  const {life} = await launched({select: async () => {throw new ApiError(500, 'server_error', 'oops', true);}});
  life.actions.pickProfile.start('teen'); await flush();
  life.actions.submitPin.start('1234', false); await flush();
  const s = state(life, 'enteringPin');
  assert.equal(s.rejected, undefined);
  assert.equal(life.getSnapshot().pending, undefined);
  assert.equal(life.actions.submitPin.getSnapshot().phase, 'failed');
  assert.equal(life.history().at(-1)?.row, 36);
  life.dispose();
});

test('pinSubmitted with remember sends remember: false when the server does not offer remembering', async () => {
  const {life, ports} = await launched({connect: async () => ({profiles: PROFILES, rememberProfiles: false})});
  life.actions.pickProfile.start('teen'); await flush();
  assert.equal(state(life, 'enteringPin').canRemember, false);
  life.actions.submitPin.start('1234', true); await flush();
  assert.equal(ports.selects.at(-1)?.remember, false);
  assert.equal(state(life, 'ready').remembered, false);
  life.dispose();
});

test('a double tap on submitPin selects once', async () => {
  const pending = deferred<SelectedFacts>();
  let selects = 0;
  const {life} = await launched({select: () => {selects++; return pending.promise;}});
  life.actions.pickProfile.start('teen'); await flush();
  life.actions.submitPin.start('1234', true);
  life.actions.submitPin.start('1234', true); await flush();
  assert.equal(selects, 1);
  pending.resolve({viewer: viewer('teen', 'f2'), remembered: true}); await flush();
  assert.equal(kind(life), 'ready');
  life.dispose();
});

test('unlockManagement sends a same-profile select with remember true and clears manageLocked', async () => {
  const {life, ports} = await launched({readRecords: async () => ({outcome: 'ok', servers: 1, held: held({remembered: true})})});
  assert.equal(state(life, 'ready').manageLocked, true);
  const before = life.getSnapshot().generation;
  life.actions.unlockManagement.start('4321'); await flush();
  assert.equal(ports.selects.length, 1);
  assert.equal(ports.selects[0]!.profileId, 'main');
  assert.equal(ports.selects[0]!.pin, '4321');
  assert.equal(ports.selects[0]!.remember, true);
  assert.equal(ports.selects[0]!.serverId, 's1');
  const s = state(life, 'ready');
  assert.equal(s.manageLocked, false);
  assert.equal(s.remembered, true);
  assert.equal(s.viewer.familyId, 'f2');
  assert.equal(life.getSnapshot().generation, before + 1, 'the successor family moves authority');
  assert.equal(life.actions.unlockManagement.getSnapshot().phase, 'done');
  life.dispose();
});

test('a wrong management PIN keeps ready locked and fails the action', async () => {
  const scheduler = createManualScheduler(0);
  const {life} = await launched({scheduler, readRecords: async () => ({outcome: 'ok', servers: 1, held: held({remembered: true})}),
    select: async () => {throw new ApiError(403, 'profile_pin_invalid', 'no');}});
  life.actions.unlockManagement.start('0000'); await flush();
  const s = state(life, 'ready');
  assert.equal(s.manageLocked, true);
  assert.equal(life.getSnapshot().pending, undefined);
  assert.equal(life.actions.unlockManagement.getSnapshot().phase, 'failed');
  life.dispose();
});

test('unlockManagement is refused when management is not locked', async () => {
  const {life, ports} = await launched({readRecords: async () => ({outcome: 'ok', servers: 1, held: held({viewer: viewer('teen'), remembered: true})})});
  assert.equal(state(life, 'ready').manageLocked, false);
  life.actions.unlockManagement.start('1234'); await flush();
  assert.equal(ports.selects.length, 0);
  assert.equal(life.actions.unlockManagement.getSnapshot().phase, 'failed');
  life.dispose();
});

// ---- servers and sign-out ------------------------------------------------------------------------

test('switchServer runs fenceShell, then storeSelect, re-reads readHeld and connects the new server', async () => {
  const {life, ports} = await launched({
    readRecords: async () => ({outcome: 'ok', servers: 2, held: held({remembered: true})}),
    readHeld: async () => {ports.calls.push('readHeld'); return {servers: 2, held: held({server: S2, viewer: viewer('main', 'g1')})};},
  });
  assert.equal(state(life, 'ready').server.serverId, 's1');
  ports.calls.length = 0;
  const before = life.getSnapshot().generation;
  life.actions.switchServer.start(S2); await flush();
  assert.deepEqual(ports.calls, ['fence', 'storeSelect:s2', 'readHeld', 'connect:s2', 'drain:s2']);
  assert.equal(state(life, 'choosingProfile').server.serverId, 's2');
  assert.equal(life.getSnapshot().generation, before + 1);
  assert.equal(life.getSnapshot().pending, undefined);
  assert.equal(life.actions.switchServer.getSnapshot().phase, 'done');
  assert.equal(life.authority(), undefined, 'no profile in use until one is picked');
  life.dispose();
});

test('a switchServer whose storeSelect fails stays on its screen and fails visibly', async () => {
  const {life} = await launched({readRecords: async () => ({outcome: 'ok', servers: 2, held: held({remembered: true})}),
    storeSelect: async () => {throw new Error('disk full');}});
  life.actions.switchServer.start(S2); await flush();
  assert.equal(kind(life), 'ready');
  assert.equal(life.getSnapshot().pending, undefined);
  assert.equal(life.actions.switchServer.getSnapshot().phase, 'failed');
  assert.equal(life.history().at(-1)?.row, 36);
  life.dispose();
});

test('signing out of the last server goes to signedOut', async () => {
  const {life, ports} = await launched({readRecords: async () => ({outcome: 'ok', servers: 1, held: held({remembered: true})})});
  ports.calls.length = 0;
  const before = life.getSnapshot().generation;
  life.actions.signOutServer.start(true); await flush();
  assert.deepEqual(ports.calls, ['fence', 'clearMarker:s1', 'signOutServer:s1:f1:true']);
  assert.equal(kind(life), 'signedOut');
  assert.equal(life.getSnapshot().generation, before + 1);
  assert.equal(life.authority(), undefined);
  assert.equal(life.actions.signOutServer.getSnapshot().phase, 'done');
  life.dispose();
});

test('signing out of one of two servers goes to choosingServer', async () => {
  const {life} = await launched({readRecords: async () => ({outcome: 'ok', servers: 2, held: held({remembered: true})}),
    signOutServer: async () => ({servers: 1})});
  life.actions.signOutServer.start(false); await flush();
  assert.equal(kind(life), 'choosingServer');
  life.dispose();
});

test('signing out of the Portico Account: none left → signedOut, some local ones left → choosingServer', async () => {
  const a = await launched({signOutAccount: async () => ({servers: 0})});
  a.life.actions.signOutPorticoAccount.start(); await flush();
  assert.equal(kind(a.life), 'signedOut');
  const b = await launched({signOutAccount: async () => ({servers: 2})});
  b.life.actions.signOutPorticoAccount.start(); await flush();
  assert.equal(kind(b.life), 'choosingServer');
  assert.ok(b.ports.calls.includes('fence'));
});

test('a failed sign-out stays put and fails visibly', async () => {
  const {life} = await launched({readRecords: async () => ({outcome: 'ok', servers: 1, held: held({remembered: true})}),
    signOutServer: async () => {throw new Error('network');}});
  life.actions.signOutServer.start(false); await flush();
  assert.equal(kind(life), 'ready');
  assert.equal(life.getSnapshot().pending, undefined);
  assert.equal(life.actions.signOutServer.getSnapshot().phase, 'failed');
  life.dispose();
});

test('identityFailed → forgetServer clears the marker, forgets and leaves the right screen', async () => {
  const {life, ports} = await launched({connect: async () => {throw new Error('cert');}, classifyConnect: () => ({reason: 'identity', code: 'identity_mismatch'}),
    forgetServer: async serverId => {ports.calls.push('forgetServer:' + serverId); return {servers: 1};}});
  assert.equal(kind(life), 'identityFailed');
  life.actions.forgetServer.start(); await flush();
  assert.ok(ports.calls.includes('clearMarker:s1') && ports.calls.includes('forgetServer:s1'));
  assert.equal(kind(life), 'choosingServer');
  life.dispose();
});

test("recordsRead 'lost' then resetRecords ends in signedOut", async () => {
  const {life, ports} = await launched({readRecords: async () => ({outcome: 'lost', servers: 0})});
  assert.equal(kind(life), 'recordsLost');
  const before = life.getSnapshot().generation;
  life.actions.resetRecords.start(); await flush();
  assert.deepEqual(ports.calls, ['resetRecords']);
  assert.equal(kind(life), 'signedOut');
  assert.equal(life.getSnapshot().generation, before + 1);
  assert.equal(life.getSnapshot().pending, undefined);
  assert.equal(life.actions.resetRecords.getSnapshot().phase, 'done');
  life.dispose();
});

test('a readRecords that throws (not a proven lost key) reads as signed out, never as records lost', async () => {
  const {life} = await launched({readRecords: async () => {throw new Error('keystore');}});
  assert.equal(kind(life), 'signedOut');
  life.dispose();
});

// A failed reset releases its pending action, so the person can try again.
test('a failed reset stays on recordsLost and fails visibly', async () => {
  const {life} = await launched({readRecords: async () => ({outcome: 'lost', servers: 0}), resetRecords: async () => {throw new Error('nope');}});
  life.actions.resetRecords.start(); await flush();
  assert.equal(kind(life), 'recordsLost');
  assert.equal(life.getSnapshot().pending, undefined);
  assert.equal(life.actions.resetRecords.getSnapshot().phase, 'failed');
  life.dispose();
});

// ---- history, logging, listeners --------------------------------------------------------------------

test('history() is bounded at 200 and its records carry only t, from, event, to, row, generation', async () => {
  const scheduler = createManualScheduler(5000);
  const {life} = await launched({scheduler});
  for (let i = 0; i < 250; i++) life.otherProfiles(); // ignored on the picker, but each is recorded
  await scheduler.advance(1234);
  life.otherProfiles(); await flush();
  const history = life.history();
  assert.equal(history.length, 200);
  for (const record of history) assert.deepEqual(Object.keys(record).sort(), ['event', 'from', 'generation', 'row', 't', 'to']);
  assert.equal(history.at(-1)?.event, 'otherProfiles');
  assert.equal(history.at(-1)?.row, 'ignored');
  assert.equal(history.at(-1)?.t, 1234, 'milliseconds since launch');
  assert.ok(history.every(r => r.event === 'otherProfiles'), 'the oldest records were dropped');
  life.dispose();
});

test('history records carry no ids, names, addresses or secrets', async () => {
  const {life} = await launched({readRecords: async () => ({outcome: 'ok', servers: 1, held: held({remembered: true})})});
  life.actions.unlockManagement.start('4321'); await flush();
  life.report({type: 'signInEnded', username: 'justin'}); await flush();
  const text = JSON.stringify(life.history());
  for (const secret of ['s1', 'Home', 'justin', 'a1', 'f1', 'f2', 'main', '4321']) assert.ok(!text.includes(`"${secret}"`) && !text.includes(`:${secret}`), `no ${secret}`);
  for (const record of life.history()) for (const value of Object.values(record)) assert.ok(typeof value === 'number' || /^[A-Za-z]+$/.test(String(value)), `code-like: ${value}`);
  life.dispose();
});

test('history() returns a frozen copy', async () => {
  const {life} = await launched();
  const copy = life.history();
  assert.ok(Object.isFrozen(copy));
  assert.ok(Object.isFrozen(copy[0]));
  const length = copy.length;
  life.otherProfiles(); await flush();
  assert.equal(copy.length, length, 'an earlier copy does not grow');
  life.dispose();
});

test('a throwing or rejecting log port or listener never breaks a transition', async () => {
  const warnings: string[] = [];
  setCleanupLog(label => {warnings.push(label);});
  try {
    let n = 0;
    const {life} = await launched({
      readRecords: async () => ({outcome: 'ok', servers: 1, held: held({remembered: true})}),
      log: () => {if (n++ % 2) throw new Error('log sink'); return Promise.reject(new Error('async log sink')) as never;},
    });
    let heard = 0;
    life.subscribe(() => {throw new Error('bad listener');});
    life.subscribe(() => {heard++;});
    assert.equal(kind(life), 'ready');
    life.actions.unlockManagement.start('1234'); await flush();
    assert.equal(state(life, 'ready').manageLocked, false);
    assert.equal(life.actions.unlockManagement.getSnapshot().phase, 'done');
    assert.ok(heard > 0, 'a later listener is still called');
    assert.ok(warnings.includes('lifecycle log') && warnings.includes('lifecycle listener'));
    assert.ok(life.history().length > 0, 'the ring still records');
    life.dispose();
  } finally {
    setCleanupLog((label, error) => console.warn(`Cleanup failed: ${label}`, error));
  }
});

test('a throwing fenceShell or drainRevocations does not stop the move', async () => {
  setCleanupLog(() => {});
  try {
    const {life} = await launched({readRecords: async () => ({outcome: 'ok', servers: 1, held: held({remembered: true})}),
      fenceShell: () => {throw new Error('fence');}, drainRevocations: () => {throw new Error('drain');}});
    assert.equal(kind(life), 'ready');
    life.report({type: 'gated'}); await flush();
    assert.equal(kind(life), 'enteringPin');
    life.dispose();
  } finally {
    setCleanupLog((label, error) => console.warn(`Cleanup failed: ${label}`, error));
  }
});

// ---- records changed by another owner ---------------------------------------------------------------------

test('observeRecords echoes while an action is pending are ignored', async () => {
  const pending = deferred<SelectedFacts>();
  let listener = () => {};
  const {life, ports} = await launched({select: () => pending.promise, observeRecords: l => {listener = l; return () => {};}});
  life.actions.pickProfile.start('kids'); await flush();
  assert.equal(life.getSnapshot().pending, 'pick');
  const reads = ports.reads.n;
  listener(); listener(); await flush();
  assert.equal(ports.reads.n, reads, 'the record is not even re-read');
  assert.equal(kind(life), 'choosingProfile');
  pending.resolve({viewer: viewer('kids', 'f2'), remembered: false}); await flush();
  assert.equal(state(life, 'ready').viewer.familyId, 'f2');
  life.dispose();
});

test('an echo whose read finishes after an action started is ignored', async () => {
  const read = deferred<{servers: number; held?: HeldSignIn}>(), pick = deferred<SelectedFacts>();
  let listener = () => {}, calls = 0;
  const {life} = await launched({select: () => pick.promise, observeRecords: l => {listener = l; return () => {};},
    readHeld: () => {calls++; return read.promise;}});
  listener(); await flush();
  assert.equal(calls, 1);
  life.actions.pickProfile.start('kids'); await flush();
  read.resolve({servers: 1, held: held({viewer: viewer('teen', 'f9')})}); await flush();
  assert.equal(life.history().filter(r => r.event === 'envelopeChanged').length, 0);
  assert.equal(kind(life), 'choosingProfile');
  pick.resolve({viewer: viewer('kids', 'f2'), remembered: false}); await flush();
  assert.equal(state(life, 'ready').viewer.familyId, 'f2');
  life.dispose();
});

test('a readHeld that throws on an echo is ignored', async () => {
  let listener = () => {};
  const {life} = await launched({observeRecords: l => {listener = l; return () => {};}, readHeld: async () => {throw new Error('locked');}});
  const before = life.history().length;
  listener(); await flush();
  assert.equal(life.history().length, before);
  assert.equal(kind(life), 'choosingProfile');
  life.dispose();
});

test("another tab's pick on a ready shell is followed with a fence and no sign-out", async () => {
  let listener = () => {}, now: HeldSignIn = held({remembered: true});
  const {life, ports} = await launched({readRecords: async () => ({outcome: 'ok', servers: 1, held: held({remembered: true})}),
    observeRecords: l => {listener = l; return () => {};}, readHeld: async () => ({servers: 1, held: now})});
  assert.equal(kind(life), 'ready');
  ports.calls.length = 0;
  now = held({viewer: viewer('kids', 'f9'), remembered: false});
  listener(); await flush();
  const s = state(life, 'ready');
  assert.equal(s.viewer.familyId, 'f9');
  assert.equal(s.remembered, false);
  assert.deepEqual(ports.calls, ['fence']);
  assert.ok(!ports.calls.some(c => c.startsWith('signOut') || c === 'clearMarker:s1'));
  life.dispose();
});

test('dispose() stops observing the record and ends every action', async () => {
  let stopped = 0;
  const {life} = await launched({observeRecords: () => () => {stopped++;}});
  life.dispose();
  life.dispose();
  assert.equal(stopped, 1);
  life.actions.pickProfile.start('kids'); await flush();
  assert.equal(life.actions.pickProfile.getSnapshot().phase, 'idle');
});

test('authority() is the shell\'s sign-in: kept while switching, gone when gated, and a token refresh does not move it', async () => {
  const {life} = await launched({readRecords: async () => ({outcome: 'ok', servers: 1, held: held({remembered: true})})});
  assert.deepEqual(life.authority(), {serverId: 's1', accountId: 'a1', profileId: 'main', familyId: 'f1'});
  life.report({type: 'networkLost'}); await flush();
  assert.equal(kind(life), 'ready');
  const generation = life.getSnapshot().generation;
  life.report({type: 'networkLost'}); await flush();
  assert.equal(life.getSnapshot().generation, generation);
  life.switchProfile(); await flush();
  assert.deepEqual(life.authority(), {serverId: 's1', accountId: 'a1', profileId: 'main', familyId: 'f1'});
  life.cancelSwitch(); await flush();
  life.report({type: 'gated'}); await flush();
  assert.equal(kind(life), 'enteringPin');
  assert.equal(life.authority(), undefined);
  life.dispose();
});

test('the connect deadline wins even when the port ignores its signal; its late answer is dropped', async () => {
  const scheduler = createManualScheduler(0);
  let answer!: (value: ConnectedFacts) => void;
  const {life} = await launched({scheduler, connect: () => new Promise(resolve => {answer = resolve;})});
  await scheduler.advance(CONNECT_DEADLINE_MS); await flush();
  assert.equal(kind(life), 'offline');
  answer(facts()); await flush();
  assert.equal(kind(life), 'offline');
  life.dispose();
});

test('lane A bindAuthority over the lifecycle: survives nothing but the same sign-in, ends on another', async () => {
  const {bindAuthority} = await import('../src/session-authority.ts');
  let listener = () => {};
  let next: HeldSignIn | undefined;
  const {life} = await launched({readRecords: async () => ({outcome: 'ok', servers: 1, held: held({remembered: true})}),
    observeRecords: l => {listener = l; return () => {};}, readHeld: async () => ({servers: 1, ...(next ? {held: next} : {})})});
  assert.equal(kind(life), 'ready');
  const binding = bindAuthority(life);
  assert.equal(binding.isCurrent(), true);
  life.switchProfile(); life.cancelSwitch(); await flush();
  assert.equal(binding.isCurrent(), true, 'opening and cancelling the picker keeps the same sign-in');
  next = held({viewer: viewer('kids', 'f9')});
  listener(); await flush();
  assert.equal(binding.isCurrent(), false);
  assert.equal(binding.signal.aborted, true);
  life.dispose();
});
