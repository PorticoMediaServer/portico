import test from 'node:test';
import assert from 'node:assert/strict';
import {
  initialLifecycle, nextLifecycle,
  type ConnectedFacts, type HeldSignIn, type LifecycleEffect, type LifecycleEvent, type LifecyclePending, type LifecycleSnapshot, type LifecycleState,
  type PickerProfile, type ServerRef, type ViewerRef,
} from '../src/session-lifecycle.ts';

// ---- builders -------------------------------------------------------------------------------

const S1: ServerRef = {serverId: 's1', name: 'Home', signIn: 'local'};
const S2: ServerRef = {serverId: 's2', name: 'Cabin', signIn: 'local'};
const viewer = (profileId = 'main', familyId = 'f1'): ViewerRef => ({accountId: 'a1', profileId, role: 'owner', familyId});
const MAIN: PickerProfile = {id: 'main', name: 'Main', primary: true, pinRequired: true};
const KIDS: PickerProfile = {id: 'kids', name: 'Kids', primary: false, pinRequired: false};
const TEEN: PickerProfile = {id: 'teen', name: 'Teen', primary: false, pinRequired: true};
const SOLO: PickerProfile = {id: 'main', name: 'Main', primary: true, pinRequired: false};
const PROFILES = [MAIN, KIDS, TEEN];
const held = (over: Partial<HeldSignIn> = {}): HeldSignIn => ({server: S1, viewer: viewer(), remembered: false, profilePinRequired: true, ...over});
const facts = (profiles: readonly PickerProfile[] = PROFILES, rememberProfiles = true): ConnectedFacts => ({profiles, rememberProfiles});

const snap = (state: LifecycleState, over: {generation?: number; pending?: LifecyclePending} = {}): LifecycleSnapshot =>
  ({generation: over.generation ?? 7, state, ...(over.pending ? {pending: over.pending} : {})});
const G = 7;

type Of<K extends LifecycleState['kind']> = Extract<LifecycleState, {kind: K}>;
const connecting = (over: Partial<Of<'connecting'>> = {}): Of<'connecting'> => ({kind: 'connecting', server: S1, held: held(), ...over});
const offline = (over: Partial<Of<'offline'>> = {}): Of<'offline'> => ({kind: 'offline', server: S1, viewer: viewer(), downloads: false, ...over});
const choosing = (over: Partial<Of<'choosingProfile'>> = {}): Of<'choosingProfile'> =>
  ({kind: 'choosingProfile', server: S1, viewer: viewer(), profiles: PROFILES, focus: 'main', switching: false, canRemember: true, ...over});
const entering = (over: Partial<Of<'enteringPin'>> = {}): Of<'enteringPin'> =>
  ({kind: 'enteringPin', server: S1, viewer: viewer(), profiles: PROFILES, profile: TEEN, reason: 'switch', canRemember: true, switching: false, ...over});
const ready = (over: Partial<Of<'ready'>> = {}): Of<'ready'> =>
  ({kind: 'ready', server: S1, viewer: viewer(), profiles: PROFILES, remembered: false, manageLocked: false, canRemember: true, ...over});
const identityFailed = (): Of<'identityFailed'> => ({kind: 'identityFailed', server: S1, code: 'identity_mismatch'});
const signInEnded = (): Of<'signInEnded'> => ({kind: 'signInEnded', server: S1, username: 'justin'});

const fence: LifecycleEffect = {kind: 'fenceShell'};
const clear = (serverId = 's1'): LifecycleEffect => ({kind: 'clearMarker', serverId});
const drain: LifecycleEffect = {kind: 'drainRevocations', serverId: 's1'};
const connectS1: LifecycleEffect = {kind: 'connect', serverId: 's1'};

// ---- table-driven rows ----------------------------------------------------------------------

/** Every key in `expected` must be present and equal in `actual` (recursively); other keys are free. */
function assertSubset(actual: unknown, expected: unknown, path = 'state'): void {
  if (expected && typeof expected === 'object' && !Array.isArray(expected)) {
    assert.ok(actual && typeof actual === 'object', `${path}: expected an object`);
    for (const [key, value] of Object.entries(expected)) assertSubset((actual as Record<string, unknown>)[key], value, `${path}.${key}`);
    return;
  }
  assert.deepEqual(actual, expected, path);
}

type Case = Readonly<{
  name: string; row: number; from: LifecycleSnapshot; event: LifecycleEvent;
  /** Subset of the next state. */
  state: Record<string, unknown>;
  /** Exact effect list. Default none. */
  effects?: readonly LifecycleEffect[];
  /** Did `generation` advance? Default no. */
  advances?: boolean;
  /** `pending` on the next snapshot. Default none (cleared). */
  pending?: LifecyclePending;
  /** Keys that must be absent on the next state. */
  absent?: readonly string[];
}>;

function check(c: Case) {
  const t = nextLifecycle(c.from, c.event);
  assert.ok(t, `${c.event.type} applies`);
  assert.equal(t.row, c.row, 'row');
  assertSubset(t.snapshot.state, c.state);
  for (const key of c.absent ?? []) assert.ok(!(key in t.snapshot.state), `state has no ${key}`);
  assert.deepEqual(t.effects, c.effects ?? [], 'effects');
  assert.equal(t.snapshot.generation, c.from.generation + (c.advances ? 1 : 0), c.advances ? 'generation advances' : 'generation holds');
  assert.equal(t.snapshot.pending, c.pending, 'pending');
}

const cases: readonly Case[] = [
  // 0–6: launch
  {name: 'row 0: launch reads the records', row: 0, from: snap({kind: 'starting'}), event: {type: 'launch'}, state: {kind: 'starting'}, effects: [{kind: 'readRecords'}]},
  {name: 'row 1: records ok, no servers → signedOut', row: 1, from: snap({kind: 'starting'}), event: {type: 'recordsRead', outcome: 'ok', servers: 0},
    state: {kind: 'signedOut'}, absent: ['notice']},
  {name: 'row 2: records ok, servers but none held → choosingServer', row: 2, from: snap({kind: 'starting'}), event: {type: 'recordsRead', outcome: 'ok', servers: 2},
    state: {kind: 'choosingServer'}},
  {name: 'row 3: key lost → recordsLost', row: 3, from: snap({kind: 'starting'}), event: {type: 'recordsRead', outcome: 'lost', servers: 0}, state: {kind: 'recordsLost'}},
  {name: 'row 4: system reset the records → signedOut with the one-time notice', row: 4, from: snap({kind: 'starting'}),
    event: {type: 'recordsRead', outcome: 'reset', servers: 0}, state: {kind: 'signedOut', notice: 'recordsReset'}, advances: true},
  {name: 'row 5: a held sign-in → connecting', row: 5, from: snap({kind: 'starting'}), event: {type: 'recordsRead', outcome: 'ok', servers: 1, held: held()},
    state: {kind: 'connecting', server: S1, held: {viewer: viewer()}}, effects: [connectS1], advances: true},
  {name: 'row 6: reset confirmed asks the port to reset', row: 6, from: snap({kind: 'recordsLost'}), event: {type: 'recordsResetConfirmed'},
    state: {kind: 'recordsLost'}, effects: [{kind: 'resetRecords'}], pending: 'reset'},
  {name: 'row 6: reset done → signedOut', row: 6, from: snap({kind: 'recordsLost'}, {pending: 'reset'}), event: {type: 'signedOut', remaining: 0},
    state: {kind: 'signedOut'}, advances: true},

  // 7–9: connected
  {name: 'row 7: connected with a valid marker on a main profile with a PIN → ready, remembered, management locked', row: 7,
    from: snap(connecting({held: held({remembered: true})})), event: {type: 'connected', facts: facts()},
    state: {kind: 'ready', remembered: true, manageLocked: true, canRemember: true, viewer: viewer()}, effects: [drain]},
  {name: 'row 7: a remembered non-main profile with a PIN → manageLocked false', row: 7,
    from: snap(connecting({held: held({viewer: viewer('teen'), remembered: true})})), event: {type: 'connected', facts: facts()},
    state: {kind: 'ready', remembered: true, manageLocked: false}, effects: [drain]},
  {name: 'row 7: a remembered main profile without a PIN → manageLocked false', row: 7,
    from: snap(connecting({held: held({remembered: true})})), event: {type: 'connected', facts: facts([SOLO, KIDS])},
    state: {kind: 'ready', remembered: true, manageLocked: false}, effects: [drain]},
  {name: 'row 7: canRemember follows rememberProfiles (off)', row: 7,
    from: snap(connecting({held: held({remembered: true})})), event: {type: 'connected', facts: facts(PROFILES, false)},
    state: {kind: 'ready', canRemember: false}, effects: [drain]},
  {name: 'row 8: connected, the only profile has no PIN → ready, not remembered', row: 8,
    from: snap(connecting()), event: {type: 'connected', facts: facts([SOLO])},
    state: {kind: 'ready', remembered: false, manageLocked: false}, effects: [drain]},
  {name: 'row 9: connected otherwise → choosingProfile focused on the held profile', row: 9,
    from: snap(connecting({held: held({viewer: viewer('kids', 'f4')})})), event: {type: 'connected', facts: facts()},
    state: {kind: 'choosingProfile', focus: 'kids', switching: false, canRemember: true, viewer: viewer('kids', 'f4')}, effects: [drain]},
  {name: 'row 9: a single profile that has a PIN still shows the picker', row: 9,
    from: snap(connecting()), event: {type: 'connected', facts: facts([MAIN])}, state: {kind: 'choosingProfile', focus: 'main'}, effects: [drain]},
  {name: 'row 9: a marker whose profile no longer exists shows the picker', row: 9,
    from: snap(connecting({held: held({viewer: viewer('gone'), remembered: true})})), event: {type: 'connected', facts: facts()},
    state: {kind: 'choosingProfile', focus: 'gone'}, effects: [drain]},
  {name: 'row 9: canRemember follows rememberProfiles (off)', row: 9,
    from: snap(connecting()), event: {type: 'connected', facts: facts(PROFILES, false)}, state: {kind: 'choosingProfile', canRemember: false}, effects: [drain]},

  // 10–13: connect failures
  {name: 'row 10: connecting, profile_pin_reentry → enteringPin(reentry), marker cleared', row: 10,
    from: snap(connecting({held: held({remembered: true})})), event: {type: 'connectFailed', failure: {reason: 'pinReentry'}},
    state: {kind: 'enteringPin', reason: 'reentry', canRemember: false, switching: false, viewer: viewer(), profile: {id: 'main', pinRequired: true}}, effects: [clear()]},
  {name: 'row 11: refresh refused → signInEnded keeps the username hint', row: 11,
    from: snap(connecting()), event: {type: 'connectFailed', failure: {reason: 'signInEnded', username: 'justin'}},
    state: {kind: 'signInEnded', server: S1, username: 'justin'}, advances: true},
  {name: 'row 12: identity failure → identityFailed', row: 12,
    from: snap(connecting()), event: {type: 'connectFailed', failure: {reason: 'identity', code: 'certificate_untrusted'}},
    state: {kind: 'identityFailed', server: S1, code: 'certificate_untrusted'}},
  {name: 'row 13: network failure with a remembered profile → offline, downloads open', row: 13,
    from: snap(connecting({held: held({remembered: true})})), event: {type: 'connectFailed', failure: {reason: 'offline'}},
    state: {kind: 'offline', downloads: true, viewer: viewer()}},
  {name: 'row 13: not remembered, profile has no PIN → downloads open', row: 13,
    from: snap(connecting({held: held({viewer: viewer('kids'), profilePinRequired: false})})), event: {type: 'connectFailed', failure: {reason: 'offline'}},
    state: {kind: 'offline', downloads: true}},
  {name: 'row 13: not remembered, profile has a PIN → downloads locked', row: 13,
    from: snap(connecting()), event: {type: 'connectFailed', failure: {reason: 'offline'}}, state: {kind: 'offline', downloads: false}},
  {name: 'row 13: not remembered, PIN state unknown → counts as locked', row: 13,
    from: snap(connecting({held: held({profilePinRequired: undefined})})), event: {type: 'connectFailed', failure: {reason: 'offline'}},
    state: {kind: 'offline', downloads: false}},
  {name: 'row 13: no held sign-in → offline with no viewer and no downloads', row: 13,
    from: snap(connecting({held: undefined})), event: {type: 'connectFailed', failure: {reason: 'offline'}}, state: {kind: 'offline', downloads: false}, absent: ['viewer']},

  // 14: retry
  {name: 'row 14: offline, retry → connecting', row: 14, from: snap(offline()), event: {type: 'retry'}, state: {kind: 'connecting', server: S1}, effects: [connectS1]},

  // 15–18: profile picker
  {name: 'row 15: tapping the current profile when it has no PIN → ready, no select effect', row: 15,
    from: snap(choosing({viewer: viewer('kids')})), event: {type: 'profileTapped', profileId: 'kids'},
    state: {kind: 'ready', remembered: false, manageLocked: false, viewer: viewer('kids')}},
  {name: 'row 15: the same profile while switching returns the resumed flags', row: 15,
    from: snap(choosing({viewer: viewer('kids'), switching: true, resume: {remembered: true, manageLocked: true}})), event: {type: 'profileTapped', profileId: 'kids'},
    state: {kind: 'ready', remembered: true, manageLocked: true}},
  {name: 'row 16: tapping another profile with no PIN selects it, remember false, pending pick', row: 16,
    from: snap(choosing()), event: {type: 'profileTapped', profileId: 'kids'},
    state: {kind: 'choosingProfile'}, effects: [{kind: 'select', serverId: 's1', profileId: 'kids', remember: false}], pending: 'pick'},
  {name: 'row 16: select result → ready, new family, shell fenced, marker cleared', row: 16,
    from: snap(choosing(), {pending: 'pick'}), event: {type: 'selected', facts: {viewer: viewer('kids', 'f2'), remembered: false}},
    state: {kind: 'ready', viewer: viewer('kids', 'f2'), remembered: false, manageLocked: false}, effects: [fence, clear()], advances: true},
  {name: 'row 17: a profile with a PIN → enteringPin(switch) when it is another profile', row: 17,
    from: snap(choosing()), event: {type: 'profileTapped', profileId: 'teen'},
    state: {kind: 'enteringPin', reason: 'switch', profile: {id: 'teen'}, canRemember: true, switching: false}},
  {name: 'row 17: the current profile with a PIN → enteringPin(pick)', row: 17,
    from: snap(choosing()), event: {type: 'profileTapped', profileId: 'main'}, state: {kind: 'enteringPin', reason: 'pick', profile: {id: 'main'}}},
  {name: 'row 17: the current profile with a PIN while switching → reason switch, resume carried', row: 17,
    from: snap(choosing({switching: true, resume: {remembered: true, manageLocked: true}})), event: {type: 'profileTapped', profileId: 'main'},
    state: {kind: 'enteringPin', reason: 'switch', switching: true, resume: {remembered: true, manageLocked: true}}},
  {name: 'row 17: canRemember carried from the picker', row: 17,
    from: snap(choosing({canRemember: false})), event: {type: 'profileTapped', profileId: 'teen'}, state: {kind: 'enteringPin', canRemember: false}},
  {name: 'row 18: cancel switching from the picker → ready as it was', row: 18,
    from: snap(choosing({switching: true, resume: {remembered: true, manageLocked: false}})), event: {type: 'cancelSwitch'},
    state: {kind: 'ready', remembered: true, manageLocked: false, viewer: viewer()}},
  {name: 'row 18: cancel switching from the PIN screen → ready as it was', row: 18,
    from: snap(entering({switching: true, resume: {remembered: false, manageLocked: true}})), event: {type: 'cancelSwitch'},
    state: {kind: 'ready', remembered: false, manageLocked: true}},

  // 19–21: PIN screen
  {name: 'row 19: PIN submitted with remember (and allowed) → select with remember true, pending pin', row: 19,
    from: snap(entering()), event: {type: 'pinSubmitted', pin: '1234', remember: true},
    state: {kind: 'enteringPin'}, effects: [{kind: 'select', serverId: 's1', profileId: 'teen', pin: '1234', remember: true}], pending: 'pin'},
  {name: 'row 19: remember unticked → remember false', row: 19,
    from: snap(entering()), event: {type: 'pinSubmitted', pin: '1234', remember: false},
    state: {kind: 'enteringPin'}, effects: [{kind: 'select', serverId: 's1', profileId: 'teen', pin: '1234', remember: false}], pending: 'pin'},
  {name: 'row 19: remember ticked but canRemember is false → remember false', row: 19,
    from: snap(entering({canRemember: false})), event: {type: 'pinSubmitted', pin: '1234', remember: true},
    state: {kind: 'enteringPin'}, effects: [{kind: 'select', serverId: 's1', profileId: 'teen', pin: '1234', remember: false}], pending: 'pin'},
  {name: 'row 19: PIN accepted and remembered → ready(remembered), marker kept (written by the port)', row: 19,
    from: snap(entering(), {pending: 'pin'}), event: {type: 'selected', facts: {viewer: viewer('teen', 'f2'), remembered: true}},
    state: {kind: 'ready', remembered: true, manageLocked: false, viewer: viewer('teen', 'f2')}, effects: [fence], advances: true},
  {name: 'row 19: PIN accepted, not remembered → ready, marker cleared', row: 19,
    from: snap(entering(), {pending: 'pin'}), event: {type: 'selected', facts: {viewer: viewer('teen', 'f2'), remembered: false}},
    state: {kind: 'ready', remembered: false}, effects: [fence, clear()], advances: true},
  {name: 'row 20: wrong PIN with a floor → enteringPin with waitUntil, rejected, pending cleared', row: 20,
    from: snap(entering(), {pending: 'pin'}), event: {type: 'pinRejected', waitUntil: 31_000},
    state: {kind: 'enteringPin', rejected: true, waitUntil: 31_000, profile: {id: 'teen'}}},
  {name: 'row 20: wrong PIN with no floor → rejected, no waitUntil', row: 20,
    from: snap(entering(), {pending: 'pin'}), event: {type: 'pinRejected'}, state: {kind: 'enteringPin', rejected: true}, absent: ['waitUntil']},
  {name: 'row 20: a wrong PIN on management unlock leaves ready as it was', row: 20,
    from: snap(ready({manageLocked: true}), {pending: 'unlock'}), event: {type: 'pinRejected'}, state: {kind: 'ready', manageLocked: true}},
  {name: 'row 21: Other profiles → choosingProfile focused on the PIN profile, switching kept', row: 21,
    from: snap(entering({switching: true, resume: {remembered: true, manageLocked: false}})), event: {type: 'otherProfiles'},
    state: {kind: 'choosingProfile', focus: 'teen', switching: true, resume: {remembered: true, manageLocked: false}}},
  {name: 'row 21: Other profiles when not switching', row: 21, from: snap(entering({reason: 'reentry'})), event: {type: 'otherProfiles'},
    state: {kind: 'choosingProfile', focus: 'teen', switching: false}},
  // Row 22 (Forgot PIN completes) is run by ProfileRecoveryController outside the reducer: no event here produces it.

  // 23–27: ready
  {name: 'row 23: switch profile → choosingProfile(switching), resume remembers the flags', row: 23,
    from: snap(ready({remembered: true, manageLocked: true})), event: {type: 'switchProfile'},
    state: {kind: 'choosingProfile', switching: true, focus: 'main', resume: {remembered: true, manageLocked: true}}},
  {name: 'row 24: ready, profile_pin_reentry → enteringPin(reentry), shell fenced, marker cleared, same generation', row: 24,
    from: snap(ready({remembered: true})), event: {type: 'gated'},
    state: {kind: 'enteringPin', reason: 'reentry', profile: {id: 'main', pinRequired: true}, viewer: viewer(), switching: false}, effects: [fence, clear()]},
  {name: 'row 24: a profile missing from the list still gets a PIN screen', row: 24,
    from: snap(ready({viewer: viewer('gone'), remembered: true})), event: {type: 'gated'},
    state: {kind: 'enteringPin', reason: 'reentry', profile: {id: 'gone', pinRequired: true}}, effects: [fence, clear()]},
  {name: 'row 25: ready, refresh refused → signInEnded, shell fenced', row: 25, from: snap(ready()), event: {type: 'signInEnded', username: 'justin'},
    state: {kind: 'signInEnded', username: 'justin'}, effects: [fence], advances: true},
  {name: 'row 25: also from the picker', row: 25, from: snap(choosing()), event: {type: 'signInEnded', username: 'justin'}, state: {kind: 'signInEnded'}, effects: [fence], advances: true},
  {name: 'row 26: ready, identity failure → identityFailed, shell fenced', row: 26, from: snap(ready()), event: {type: 'identityFailed', code: 'certificate_unverified'},
    state: {kind: 'identityFailed', code: 'certificate_unverified'}, effects: [fence], advances: true},
  {name: 'row 27: ready, network lost → stays ready, nothing happens', row: 27, from: snap(ready()), event: {type: 'networkLost'}, state: {kind: 'ready'}},

  // 28–31: server and sign-out
  {name: 'row 28: switch server → fence then storeSelect, pending switchServer', row: 28,
    from: snap(ready()), event: {type: 'switchServer', server: S2}, state: {kind: 'ready'}, effects: [fence, {kind: 'storeSelect', serverId: 's2'}], pending: 'switchServer'},
  {name: 'row 28: switch server also from the picker, offline, and the server list', row: 28,
    from: snap({kind: 'choosingServer'}), event: {type: 'switchServer', server: S2}, state: {kind: 'choosingServer'}, effects: [fence, {kind: 'storeSelect', serverId: 's2'}], pending: 'switchServer'},
  {name: 'row 28: the re-read record connects the new server', row: 28,
    from: snap(ready(), {pending: 'switchServer'}), event: {type: 'envelopeChanged', servers: 2, held: held({server: S2, viewer: viewer('main', 'g1')})},
    state: {kind: 'connecting', server: S2, held: {viewer: viewer('main', 'g1')}}, effects: [{kind: 'connect', serverId: 's2'}], advances: true},
  {name: 'row 29: sign out of this server → fence, clear marker, sign out with the family; pending signOut', row: 29,
    from: snap(ready()), event: {type: 'signOutServer', removeDownloads: true}, state: {kind: 'ready'},
    effects: [fence, clear(), {kind: 'signOutServer', serverId: 's1', familyId: 'f1', removeDownloads: true}], pending: 'signOut'},
  {name: 'row 29: while connecting the held family is used', row: 29,
    from: snap(connecting({held: held({viewer: viewer('main', 'fz')})})), event: {type: 'signOutServer', removeDownloads: false}, state: {kind: 'connecting'},
    effects: [fence, clear(), {kind: 'signOutServer', serverId: 's1', familyId: 'fz', removeDownloads: false}], pending: 'signOut'},
  {name: 'row 29: another server remains → choosingServer', row: 29, from: snap(ready(), {pending: 'signOut'}), event: {type: 'signedOut', remaining: 1},
    state: {kind: 'choosingServer'}, advances: true},
  {name: 'row 29: the last server → signedOut', row: 29, from: snap(ready(), {pending: 'signOut'}), event: {type: 'signedOut', remaining: 0},
    state: {kind: 'signedOut'}, advances: true},
  {name: 'row 30: sign out of the Portico Account → fence, account sign-out, pending signOut', row: 30,
    from: snap(ready()), event: {type: 'signOutAccount'}, state: {kind: 'ready'}, effects: [fence, {kind: 'signOutAccount'}], pending: 'signOut'},
  {name: 'row 30: also from the server list', row: 30,
    from: snap({kind: 'choosingServer'}), event: {type: 'signOutAccount'}, state: {kind: 'choosingServer'}, effects: [fence, {kind: 'signOutAccount'}], pending: 'signOut'},
  {name: 'row 31: forget (identityFailed) → clear marker and forget the server; pending forget', row: 31,
    from: snap(identityFailed()), event: {type: 'forget'}, state: {kind: 'identityFailed'},
    effects: [clear(), {kind: 'forgetServer', serverId: 's1'}], pending: 'forget'},
  {name: 'row 31: forgotten with servers left → choosingServer', row: 31, from: snap(identityFailed(), {pending: 'forget'}), event: {type: 'forgotten', remaining: 2},
    state: {kind: 'choosingServer'}, advances: true},
  {name: 'row 31: forgotten, none left → signedOut', row: 31, from: snap(identityFailed(), {pending: 'forget'}), event: {type: 'forgotten', remaining: 0},
    state: {kind: 'signedOut'}, advances: true},
  {name: 'row 32: identityFailed, retry → connecting', row: 32, from: snap(identityFailed()), event: {type: 'retry'}, state: {kind: 'connecting', server: S1}, effects: [connectS1]},

  // 33–36
  {name: 'row 33: signed in from signedOut → connecting with the new sign-in', row: 33,
    from: snap({kind: 'signedOut'}), event: {type: 'signedIn', held: held({server: S2}), continuation: 'profile-selection'},
    state: {kind: 'connecting', server: S2, held: {server: S2}}, effects: [{kind: 'connect', serverId: 's2'}], advances: true},
  {name: 'row 33: also from choosingServer and signInEnded', row: 33,
    from: snap(signInEnded()), event: {type: 'signedIn', held: held(), continuation: 'viewer'}, state: {kind: 'connecting', server: S1}, effects: [connectS1], advances: true},
  {name: 'row 34: another tab picked a profile → ready follows the new family, not remembered, no sign-out', row: 34,
    from: snap(ready({remembered: true})), event: {type: 'envelopeChanged', servers: 1, held: held({viewer: viewer('kids', 'f9'), remembered: false})},
    state: {kind: 'ready', viewer: viewer('kids', 'f9'), remembered: false, manageLocked: false}, effects: [fence], advances: true},
  {name: 'row 34: the picker follows the new family without a fence', row: 34,
    from: snap(choosing()), event: {type: 'envelopeChanged', servers: 1, held: held({viewer: viewer('kids', 'f9')})},
    state: {kind: 'choosingProfile', viewer: viewer('kids', 'f9')}, advances: true},
  {name: 'row 34: the PIN screen follows the new family', row: 34,
    from: snap(entering()), event: {type: 'envelopeChanged', servers: 1, held: held({viewer: viewer('kids', 'f9')})},
    state: {kind: 'enteringPin', viewer: viewer('kids', 'f9'), profile: {id: 'teen'}}, advances: true},
  {name: 'row 34: offline follows the new family', row: 34,
    from: snap(offline()), event: {type: 'envelopeChanged', servers: 1, held: held({viewer: viewer('kids', 'f9')})},
    state: {kind: 'offline', viewer: viewer('kids', 'f9')}, advances: true},
  {name: 'row 34: the server was removed elsewhere → signedOut, shell fenced', row: 34,
    from: snap(ready()), event: {type: 'envelopeChanged', servers: 0}, state: {kind: 'signedOut'}, effects: [fence], advances: true},
  {name: 'row 34: the server was removed elsewhere but others remain → choosingServer', row: 34,
    from: snap(ready()), event: {type: 'envelopeChanged', servers: 2}, state: {kind: 'choosingServer'}, effects: [fence], advances: true},
  {name: 'row 34: another owner selected another server → connect it', row: 34,
    from: snap(ready()), event: {type: 'envelopeChanged', servers: 2, held: held({server: S2, viewer: viewer('main', 'g1')})},
    state: {kind: 'connecting', server: S2}, effects: [{kind: 'connect', serverId: 's2'}], advances: true},
  {name: 'row 34: a sign-in appeared while signed out → connect it', row: 34,
    from: snap({kind: 'signedOut'}), event: {type: 'envelopeChanged', servers: 1, held: held()}, state: {kind: 'connecting', server: S1}, effects: [connectS1], advances: true},
  {name: 'row 35: unlock management → same-profile select with remember true, pending unlock', row: 35,
    from: snap(ready({remembered: true, manageLocked: true})), event: {type: 'unlockSubmitted', pin: '4321'},
    state: {kind: 'ready', manageLocked: true}, effects: [{kind: 'select', serverId: 's1', profileId: 'main', pin: '4321', remember: true}], pending: 'unlock'},
  {name: 'row 35: unlocked → manageLocked false on the successor family, authority moves', row: 35,
    from: snap(ready({remembered: true, manageLocked: true}), {pending: 'unlock'}), event: {type: 'managementUnlocked', facts: {viewer: viewer('main', 'f2'), remembered: true}},
    state: {kind: 'ready', manageLocked: false, remembered: true, viewer: viewer('main', 'f2')}, advances: true},
  {name: 'row 35: unlocked on the same family → generation holds', row: 35,
    from: snap(ready({remembered: true, manageLocked: true}), {pending: 'unlock'}), event: {type: 'managementUnlocked', facts: {viewer: viewer('main', 'f1'), remembered: true}},
    state: {kind: 'ready', manageLocked: false, viewer: viewer('main', 'f1')}},
];

for (const c of cases) test(c.name, () => check(c));

// Row 36: a failed action clears pending and stays on the screen it started on.
const PENDINGS: readonly LifecyclePending[] = ['pick', 'pin', 'unlock', 'switchServer', 'signOut', 'forget', 'reset'];
for (const pending of PENDINGS) for (const type of ['selectFailed', 'actionFailed'] as const) {
  test(`row 36: ${type} while ${pending} clears pending and stays put`, () => {
    const from = snap(entering(), {pending});
    check({name: '', row: 36, from, event: {type}, state: {kind: 'enteringPin', profile: {id: 'teen'}}});
  });
}
test('row 36: a failure with nothing pending is ignored', () => {
  assert.equal(nextLifecycle(snap(entering()), {type: 'selectFailed'}), undefined);
  assert.equal(nextLifecycle(snap(entering()), {type: 'actionFailed'}), undefined);
});

// ---- rules ---------------------------------------------------------------------------------

test('the initial snapshot is generation 0 in starting', () => {
  assert.deepEqual(initialLifecycle, {generation: 0, state: {kind: 'starting'}});
});

test('commands are ignored while pending is set (no double submit)', () => {
  const commands: ReadonlyArray<readonly [string, LifecycleState, LifecycleEvent]> = [
    ['profileTapped', choosing(), {type: 'profileTapped', profileId: 'kids'}],
    ['pinSubmitted', entering(), {type: 'pinSubmitted', pin: '1', remember: false}],
    ['unlockSubmitted', ready({manageLocked: true}), {type: 'unlockSubmitted', pin: '1'}],
    ['switchServer', ready(), {type: 'switchServer', server: S2}],
    ['signOutServer', ready(), {type: 'signOutServer', removeDownloads: false}],
    ['signOutAccount', ready(), {type: 'signOutAccount'}],
    ['forget', identityFailed(), {type: 'forget'}],
    ['recordsResetConfirmed', {kind: 'recordsLost'}, {type: 'recordsResetConfirmed'}],
  ];
  for (const [name, state, event] of commands) {
    assert.ok(nextLifecycle(snap(state), event), `${name} applies when nothing is pending`);
    for (const pending of PENDINGS) assert.equal(nextLifecycle(snap(state, {pending}), event), undefined, `${name} is ignored while ${pending}`);
  }
});

test('a double submit: the PIN command applies once, then the second is ignored', () => {
  const first = nextLifecycle(snap(entering()), {type: 'pinSubmitted', pin: '1234', remember: false});
  assert.ok(first);
  assert.equal(nextLifecycle(first.snapshot, {type: 'pinSubmitted', pin: '1234', remember: false}), undefined);
});

test('envelopeChanged is ignored while an action other than switchServer is pending', () => {
  const event: LifecycleEvent = {type: 'envelopeChanged', servers: 1, held: held({viewer: viewer('kids', 'f9')})};
  assert.ok(nextLifecycle(snap(ready()), event), 'applies when idle');
  for (const pending of PENDINGS.filter(p => p !== 'switchServer')) assert.equal(nextLifecycle(snap(ready(), {pending}), event), undefined, `ignored while ${pending}`);
  assert.equal(nextLifecycle(snap(ready(), {pending: 'switchServer'}), event)?.row, 28);
});

test('a gated ready goes to enteringPin(reentry) with fenceShell and clearMarker, never to signInEnded', () => {
  const t = nextLifecycle(snap(ready({remembered: true})), {type: 'gated'});
  assert.ok(t);
  assert.equal(t.snapshot.state.kind, 'enteringPin');
  assert.equal(t.snapshot.state.kind === 'enteringPin' && t.snapshot.state.reason, 'reentry');
  assert.deepEqual(t.effects, [fence, clear()]);
  assert.equal(t.snapshot.generation, G, 'the family is unchanged, so authority does not move');
  // `gated` has no route to signInEnded from any state.
  for (const state of allStates()) {
    const next = nextLifecycle(snap(state), {type: 'gated'});
    if (next) assert.notEqual(next.snapshot.state.kind, 'signInEnded');
  }
});

test("another tab's new family on the same server is followed: ready(new viewer, remembered false), no sign-out effect", () => {
  const t = nextLifecycle(snap(ready({remembered: true, manageLocked: true})),
    {type: 'envelopeChanged', servers: 1, held: held({viewer: viewer('kids', 'f9'), remembered: false})});
  assert.ok(t);
  assert.equal(t.row, 34);
  assertSubset(t.snapshot.state, {kind: 'ready', viewer: viewer('kids', 'f9'), remembered: false, manageLocked: false});
  assert.ok(t.effects.every(e => e.kind === 'fenceShell'), 'only the shell fence');
  assert.ok(!t.effects.some(e => e.kind === 'signOutServer' || e.kind === 'signOutAccount' || e.kind === 'forgetServer'));
  assert.equal(t.snapshot.generation, G + 1);
});

test('the same family in the envelope is not a change', () => {
  assert.equal(nextLifecycle(snap(ready()), {type: 'envelopeChanged', servers: 1, held: held()}), undefined);
});

test('canRemember follows rememberProfiles on every path out of connecting (rows 7, 8, 9)', () => {
  const paths: ReadonlyArray<readonly [number, LifecycleState, readonly PickerProfile[]]> = [
    [7, connecting({held: held({remembered: true})}), PROFILES],
    [8, connecting(), [SOLO]],
    [9, connecting(), PROFILES],
  ];
  for (const rememberProfiles of [true, false]) for (const [row, from, profiles] of paths) {
    const t = nextLifecycle(snap(from), {type: 'connected', facts: facts(profiles, rememberProfiles)});
    assert.equal(t?.row, row);
    assert.equal((t?.snapshot.state as {canRemember: boolean}).canRemember, rememberProfiles, `row ${row}, rememberProfiles ${rememberProfiles}`);
  }
});

test('tapping the current no-PIN profile goes to ready with no select effect and no pending', () => {
  const t = nextLifecycle(snap(choosing({viewer: viewer('kids')})), {type: 'profileTapped', profileId: 'kids'});
  assert.ok(t);
  assert.equal(t.snapshot.state.kind, 'ready');
  assert.deepEqual(t.effects, []);
  assert.equal(t.snapshot.pending, undefined);
});

test('tapping another no-PIN profile emits select with remember false and pending pick', () => {
  const t = nextLifecycle(snap(choosing({viewer: viewer('main')})), {type: 'profileTapped', profileId: 'kids'});
  assert.ok(t);
  assert.deepEqual(t.effects, [{kind: 'select', serverId: 's1', profileId: 'kids', remember: false}]);
  assert.equal(t.snapshot.pending, 'pick');
});

test('tapping a profile that is not in the list is ignored', () => {
  assert.equal(nextLifecycle(snap(choosing()), {type: 'profileTapped', profileId: 'nobody'}), undefined);
});

test('an empty PIN is ignored while the wait is on', () => {
  assert.equal(nextLifecycle(snap(entering({waitUntil: 99})), {type: 'pinSubmitted', pin: '', remember: false}), undefined);
});

test('selected applies only for pick and pin, and only on the picker or PIN screen', () => {
  const event: LifecycleEvent = {type: 'selected', facts: {viewer: viewer('kids', 'f2'), remembered: false}};
  assert.equal(nextLifecycle(snap(choosing()), event), undefined, 'no pending');
  for (const pending of PENDINGS.filter(p => p !== 'pick' && p !== 'pin')) assert.equal(nextLifecycle(snap(choosing(), {pending}), event), undefined, pending);
  assert.equal(nextLifecycle(snap(ready(), {pending: 'pick'}), event), undefined, 'ready');
});

test('generation advances on a change of server, account, profile or sign-in and on sign-out, never otherwise', () => {
  const advanced = cases.filter(c => c.advances).map(c => c.row);
  const holds = cases.filter(c => !c.advances).map(c => c.row);
  // Rows that move authority at least once…
  for (const row of [4, 5, 11, 16, 19, 25, 26, 29, 31, 33, 34, 35]) assert.ok(advanced.includes(row), `row ${row} advances`);
  // …and rows that must never.
  for (const row of [0, 1, 2, 3, 7, 8, 9, 10, 12, 13, 14, 15, 17, 18, 20, 21, 23, 24, 27, 30, 32]) {
    assert.ok(holds.includes(row), `row ${row} has a case`);
    assert.ok(!advanced.includes(row), `row ${row} never advances`);
  }
});

// ---- generated (state × event) check ---------------------------------------------------------

const DOCUMENTED_ROWS = new Set([0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36]);

const STATES = {
  starting: {kind: 'starting'},
  recordsLost: {kind: 'recordsLost'},
  signedOut: {kind: 'signedOut'},
  choosingServer: {kind: 'choosingServer'},
  connecting: connecting(),
  offline: offline(),
  identityFailed: identityFailed(),
  signInEnded: signInEnded(),
  choosingProfile: choosing({switching: true, resume: {remembered: true, manageLocked: false}}),
  enteringPin: entering({switching: true, resume: {remembered: true, manageLocked: false}}),
  ready: ready({remembered: true, manageLocked: true}),
} satisfies Record<LifecycleState['kind'], LifecycleState>;

function allStates(): LifecycleState[] {return Object.values(STATES);}

const EVENTS = {
  launch: {type: 'launch'},
  recordsRead: {type: 'recordsRead', outcome: 'ok', servers: 1, held: held()},
  recordsResetConfirmed: {type: 'recordsResetConfirmed'},
  connected: {type: 'connected', facts: facts()},
  connectFailed: {type: 'connectFailed', failure: {reason: 'offline'}},
  retry: {type: 'retry'},
  profileTapped: {type: 'profileTapped', profileId: 'kids'},
  pinSubmitted: {type: 'pinSubmitted', pin: '1234', remember: true},
  selected: {type: 'selected', facts: {viewer: viewer('kids', 'f2'), remembered: false}},
  pinRejected: {type: 'pinRejected', waitUntil: 5000},
  selectFailed: {type: 'selectFailed'},
  otherProfiles: {type: 'otherProfiles'},
  switchProfile: {type: 'switchProfile'},
  cancelSwitch: {type: 'cancelSwitch'},
  unlockSubmitted: {type: 'unlockSubmitted', pin: '4321'},
  managementUnlocked: {type: 'managementUnlocked', facts: {viewer: viewer('main', 'f2'), remembered: true}},
  gated: {type: 'gated'},
  signInEnded: {type: 'signInEnded', username: 'justin'},
  identityFailed: {type: 'identityFailed', code: 'identity_mismatch'},
  networkLost: {type: 'networkLost'},
  switchServer: {type: 'switchServer', server: S2},
  signOutServer: {type: 'signOutServer', removeDownloads: false},
  signOutAccount: {type: 'signOutAccount'},
  signedOut: {type: 'signedOut', remaining: 0},
  forget: {type: 'forget'},
  forgotten: {type: 'forgotten', remaining: 0},
  signedIn: {type: 'signedIn', held: held({server: S2}), continuation: 'profile-selection'},
  envelopeChanged: {type: 'envelopeChanged', servers: 1, held: held({viewer: viewer('kids', 'f9')})},
  actionFailed: {type: 'actionFailed'},
} satisfies Record<LifecycleEvent['type'], LifecycleEvent>;

/** `event @ state[+pending] → row` with a trailing ` ↑` when generation advanced. */
function appliesTable(): string[] {
  const out: string[] = [];
  for (const [kind, state] of Object.entries(STATES)) for (const pending of [undefined, ...PENDINGS]) for (const [type, event] of Object.entries(EVENTS)) {
    const from = snap(state, pending ? {pending} : {});
    const t = nextLifecycle(from, event);
    if (!t) continue;
    assert.ok(DOCUMENTED_ROWS.has(t.row), `${type} @ ${kind}${pending ? '+' + pending : ''} reports undocumented row ${t.row}`);
    assert.ok(t.snapshot.generation === from.generation || t.snapshot.generation === from.generation + 1, 'generation moves by at most one');
    out.push(`${type} @ ${kind}${pending ? '+' + pending : ''} → ${t.row}${t.snapshot.generation > from.generation ? ' ↑' : ''}`);
  }
  return out.sort();
}

/** Written out by hand from the transition table (C-DESIGN §3, plus rows 0, 35 and 36). A change to which pairs apply fails here. */
const EXPECTED_APPLIES: readonly string[] = [
  // 0 launch, and records read at launch (rows 1–5; the representative event has a held sign-in, so row 5). Neither is a command, so a stray pending value doesn't stop them.
  ...['', ...PENDINGS.map(p => '+' + p)].flatMap(p => [`launch @ starting${p} → 0`, `recordsRead @ starting${p} → 5 ↑`]),
  // row 6: records lost, reset confirmed
  'recordsResetConfirmed @ recordsLost → 6',
  // rows 7–9 (the representative connected event has a held sign-in that isn't remembered, three profiles → row 9); every pending value applies, connected is not a command
  'connected @ connecting → 9',
  'connected @ connecting+forget → 9',
  'connected @ connecting+pick → 9',
  'connected @ connecting+pin → 9',
  'connected @ connecting+reset → 9',
  'connected @ connecting+signOut → 9',
  'connected @ connecting+switchServer → 9',
  'connected @ connecting+unlock → 9',
  // row 13 (the representative failure is offline)
  'connectFailed @ connecting → 13',
  'connectFailed @ connecting+forget → 13',
  'connectFailed @ connecting+pick → 13',
  'connectFailed @ connecting+pin → 13',
  'connectFailed @ connecting+reset → 13',
  'connectFailed @ connecting+signOut → 13',
  'connectFailed @ connecting+switchServer → 13',
  'connectFailed @ connecting+unlock → 13',
  // rows 14, 32 retry
  'retry @ identityFailed → 32',
  'retry @ identityFailed+forget → 32',
  'retry @ identityFailed+pick → 32',
  'retry @ identityFailed+pin → 32',
  'retry @ identityFailed+reset → 32',
  'retry @ identityFailed+signOut → 32',
  'retry @ identityFailed+switchServer → 32',
  'retry @ identityFailed+unlock → 32',
  'retry @ offline → 14',
  'retry @ offline+forget → 14',
  'retry @ offline+pick → 14',
  'retry @ offline+pin → 14',
  'retry @ offline+reset → 14',
  'retry @ offline+signOut → 14',
  'retry @ offline+switchServer → 14',
  'retry @ offline+unlock → 14',
  // rows 15–17: profile tapped (kids: no PIN, not the held profile → row 16 select)
  'profileTapped @ choosingProfile → 16',
  // row 19: PIN submitted
  'pinSubmitted @ enteringPin → 19',
  // rows 16, 19: selected, only with pending pick or pin
  'selected @ choosingProfile+pick → 16 ↑',
  'selected @ choosingProfile+pin → 19 ↑',
  'selected @ enteringPin+pick → 16 ↑',
  'selected @ enteringPin+pin → 19 ↑',
  // row 20: wrong PIN, only with pending pin or unlock, on the PIN screen or ready
  'pinRejected @ enteringPin+pin → 20',
  'pinRejected @ enteringPin+unlock → 20',
  'pinRejected @ ready+pin → 20',
  'pinRejected @ ready+unlock → 20',
  // row 36: a failed action while anything is pending, in any state
  ...[
    'choosingProfile', 'choosingServer', 'connecting', 'enteringPin', 'identityFailed', 'offline', 'ready', 'recordsLost', 'signInEnded', 'signedOut', 'starting',
  ].flatMap(kind => PENDINGS.flatMap(pending => ['selectFailed', 'actionFailed'].map(type => `${type} @ ${kind}+${pending} → 36`))),
  // row 21, 23, 18: navigation (not commands, so they apply under any pending value)
  ...['', ...PENDINGS.map(p => '+' + p)].map(p => `otherProfiles @ enteringPin${p} → 21`),
  ...['', ...PENDINGS.map(p => '+' + p)].map(p => `switchProfile @ ready${p} → 23`),
  ...['', ...PENDINGS.map(p => '+' + p)].flatMap(p => [`cancelSwitch @ choosingProfile${p} → 18`, `cancelSwitch @ enteringPin${p} → 18`]),
  // row 35: unlock management
  'unlockSubmitted @ ready → 35',
  'managementUnlocked @ ready+unlock → 35 ↑',
  // row 24: gated; rows 25, 26: refresh refused, identity failure; row 27
  ...['', ...PENDINGS.map(p => '+' + p)].flatMap(p => [
    `gated @ ready${p} → 24`,
    `signInEnded @ ready${p} → 25 ↑`, `signInEnded @ choosingProfile${p} → 25 ↑`, `signInEnded @ enteringPin${p} → 25 ↑`,
    `identityFailed @ ready${p} → 26 ↑`, `identityFailed @ choosingProfile${p} → 26 ↑`, `identityFailed @ enteringPin${p} → 26 ↑`,
    `networkLost @ ready${p} → 27`,
  ]),
  // row 28: switch server to s2 (not the server in use)
  'switchServer @ choosingProfile → 28',
  'switchServer @ choosingServer → 28',
  'switchServer @ identityFailed → 28',
  'switchServer @ offline → 28',
  'switchServer @ ready → 28',
  'switchServer @ signInEnded → 28',
  // row 29: sign out of this server (needs a held viewer)
  'signOutServer @ choosingProfile → 29',
  'signOutServer @ connecting → 29',
  'signOutServer @ enteringPin → 29',
  'signOutServer @ offline → 29',
  'signOutServer @ ready → 29',
  // row 30: sign out of the Portico Account
  'signOutAccount @ choosingProfile → 30',
  'signOutAccount @ choosingServer → 30',
  'signOutAccount @ connecting → 30',
  'signOutAccount @ enteringPin → 30',
  'signOutAccount @ identityFailed → 30',
  'signOutAccount @ offline → 30',
  'signOutAccount @ ready → 30',
  'signOutAccount @ signInEnded → 30',
  // rows 29, 6, 31: a sign-out, reset or forget finished
  ...['choosingProfile', 'choosingServer', 'connecting', 'enteringPin', 'identityFailed', 'offline', 'ready', 'recordsLost', 'signInEnded', 'signedOut', 'starting']
    .flatMap(kind => [`signedOut @ ${kind}+signOut → 29 ↑`, `signedOut @ ${kind}+reset → 6 ↑`, `forgotten @ ${kind}+forget → 31 ↑`]),
  // row 31: forget
  'forget @ identityFailed → 31',
  // row 33: signed in
  ...['', ...PENDINGS.map(p => '+' + p)].flatMap(p => [`signedIn @ choosingServer${p} → 33 ↑`, `signedIn @ signInEnded${p} → 33 ↑`, `signedIn @ signedOut${p} → 33 ↑`]),
  // row 34: another owner changed the record (same server, another family); row 28 when our own switchServer is pending
  'envelopeChanged @ choosingProfile → 34 ↑',
  'envelopeChanged @ choosingServer → 34 ↑',
  'envelopeChanged @ choosingServer+switchServer → 28 ↑',
  'envelopeChanged @ choosingProfile+switchServer → 28 ↑',
  'envelopeChanged @ connecting+switchServer → 28 ↑',
  'envelopeChanged @ enteringPin → 34 ↑',
  'envelopeChanged @ enteringPin+switchServer → 28 ↑',
  'envelopeChanged @ identityFailed+switchServer → 28 ↑',
  'envelopeChanged @ offline → 34 ↑',
  'envelopeChanged @ offline+switchServer → 28 ↑',
  'envelopeChanged @ ready → 34 ↑',
  'envelopeChanged @ ready+switchServer → 28 ↑',
  'envelopeChanged @ signInEnded+switchServer → 28 ↑',
  'envelopeChanged @ signedOut → 34 ↑',
  'envelopeChanged @ signedOut+switchServer → 28 ↑',
];

test('generated (state × event) check: every pair is ignored or a documented row, and the applying pairs are exactly the written table', () => {
  assert.deepEqual(appliesTable(), [...EXPECTED_APPLIES].sort());
});

test('no pair ever reports row 22 (Forgot PIN runs outside the reducer)', () => {
  assert.ok(!appliesTable().some(line => / → 22\b/.test(line)));
});

test('every documented row 0–36 (except 22) is exercised by a table case', () => {
  const seen = new Set<number>([...cases.map(c => c.row), 36]);
  for (const row of DOCUMENTED_ROWS) assert.ok(seen.has(row), `row ${row} has a test`);
});
