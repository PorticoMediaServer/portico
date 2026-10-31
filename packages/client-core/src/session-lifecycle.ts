/**
 * The one owner of "which server, which account, which profile, which sign-in" on a device, from
 * launch to sign-out (planning/v3/rebuilds/C-DESIGN.md §3). This file is the pure part: a state,
 * the events that move it, and the effects a move asks for. `session-lifecycle-runtime.ts` runs the
 * effects through platform ports. Every row of the design's transition table is a case here and a
 * test in session-lifecycle.test.ts; any other (state, event) pair is ignored and logged.
 */
import type {SignInKind} from './app-sessions.ts';

export type ServerRef = Readonly<{serverId: string; name: string; signIn: SignInKind}>;
export type ViewerRef = Readonly<{accountId: string; profileId: string; role: string; familyId: string}>;
export type PickerProfile = Readonly<{id: string; name: string; primary: boolean; pinRequired: boolean}>;
export type IdentityFailureCode = 'identity_mismatch' | 'certificate_unverified' | 'certificate_untrusted';

/** What the device holds for the selected server, read from the protected record at launch. */
export type HeldSignIn = Readonly<{
  server: ServerRef; viewer: ViewerRef;
  /** A valid remember marker for exactly this sign-in (remember-marker.ts rememberedFor). */
  remembered: boolean;
  /** The held profile's PIN as last seen. Unknown counts as having one: offline it stays locked. */
  profilePinRequired?: boolean;
}>;

export type LifecycleState =
  | Readonly<{kind: 'starting'}>
  | Readonly<{kind: 'recordsLost'}>
  | Readonly<{kind: 'signedOut'; notice?: 'recordsReset'}>
  | Readonly<{kind: 'choosingServer'}>
  /** `admitted`: a sign-in that already chose its profile (continuation 'viewer') opens it once connected. */
  | Readonly<{kind: 'connecting'; server: ServerRef; held?: HeldSignIn; admitted?: true}>
  | Readonly<{kind: 'offline'; server: ServerRef; held?: HeldSignIn; viewer?: ViewerRef; downloads: boolean}>
  | Readonly<{kind: 'identityFailed'; server: ServerRef; held?: HeldSignIn; code: IdentityFailureCode}>
  | Readonly<{kind: 'signInEnded'; server: ServerRef; held?: HeldSignIn; username: string}>
  | Readonly<{kind: 'choosingProfile'; server: ServerRef; viewer: ViewerRef; profiles: readonly PickerProfile[]; focus?: string;
      switching: boolean; canRemember: boolean; resume?: Readonly<{remembered: boolean; manageLocked: boolean}>}>
  | Readonly<{kind: 'enteringPin'; server: ServerRef; viewer: ViewerRef; profiles: readonly PickerProfile[]; profile: PickerProfile;
      reason: 'pick' | 'switch' | 'reentry'; canRemember: boolean; switching: boolean; waitUntil?: number; rejected?: boolean;
      resume?: Readonly<{remembered: boolean; manageLocked: boolean}>}>
  | Readonly<{kind: 'ready'; server: ServerRef; viewer: ViewerRef; profiles: readonly PickerProfile[]; remembered: boolean;
      manageLocked: boolean; canRemember: boolean}>;

export type LifecyclePending = 'pick' | 'pin' | 'unlock' | 'switchServer' | 'signOut' | 'signOutAccount' | 'forget' | 'reset';
export type LifecycleSnapshot = Readonly<{generation: number; state: LifecycleState; pending?: LifecyclePending}>;

/** A connection to the held server succeeded and its account was read. */
export type ConnectedFacts = Readonly<{profiles: readonly PickerProfile[]; rememberProfiles: boolean}>;
/** A profile choice the server accepted: the new sign-in, and whether it remembers. */
export type SelectedFacts = Readonly<{viewer: ViewerRef; remembered: boolean}>;
export type ConnectFailure =
  | Readonly<{reason: 'offline'}>
  | Readonly<{reason: 'identity'; code: IdentityFailureCode}>
  | Readonly<{reason: 'signInEnded'; username: string}>
  | Readonly<{reason: 'pinReentry'}>;

export type LifecycleEvent =
  | Readonly<{type: 'launch'}>
  | Readonly<{type: 'recordsRead'; outcome: 'ok' | 'lost' | 'reset'; servers: number; held?: HeldSignIn}>
  | Readonly<{type: 'recordsResetConfirmed'}>
  | Readonly<{type: 'connected'; facts: ConnectedFacts}>
  | Readonly<{type: 'connectFailed'; failure: ConnectFailure}>
  | Readonly<{type: 'retry'}>
  | Readonly<{type: 'profileTapped'; profileId: string}>
  | Readonly<{type: 'pinSubmitted'; pin: string; remember: boolean}>
  | Readonly<{type: 'selected'; facts: SelectedFacts}>
  | Readonly<{type: 'pinRejected'; waitUntil?: number}>
  | Readonly<{type: 'selectFailed'}>
  | Readonly<{type: 'otherProfiles'}>
  | Readonly<{type: 'switchProfile'}>
  | Readonly<{type: 'cancelSwitch'}>
  | Readonly<{type: 'unlockSubmitted'; pin: string}>
  | Readonly<{type: 'managementUnlocked'; facts: SelectedFacts}>
  | Readonly<{type: 'gated'}>
  | Readonly<{type: 'signInEnded'; username: string}>
  | Readonly<{type: 'identityFailed'; code: IdentityFailureCode}>
  | Readonly<{type: 'networkLost'}>
  | Readonly<{type: 'switchServer'; server: ServerRef}>
  | Readonly<{type: 'signOutServer'; removeDownloads: boolean}>
  | Readonly<{type: 'signOutAccount'}>
  | Readonly<{type: 'signedOut'; remaining: number}>
  | Readonly<{type: 'forget'}>
  | Readonly<{type: 'forgotten'; remaining: number}>
  | Readonly<{type: 'signedIn'; held: HeldSignIn; continuation: 'viewer' | 'profile-selection'}>
  | Readonly<{type: 'envelopeChanged'; held?: HeldSignIn; servers: number}>
  | Readonly<{type: 'actionFailed'}>;

export type LifecycleEffect =
  | Readonly<{kind: 'readRecords'}>
  | Readonly<{kind: 'resetRecords'}>
  | Readonly<{kind: 'connect'; serverId: string}>
  | Readonly<{kind: 'select'; serverId: string; profileId: string; pin?: string; remember: boolean}>
  | Readonly<{kind: 'storeSelect'; serverId: string}>
  | Readonly<{kind: 'signOutServer'; serverId: string; familyId: string; removeDownloads: boolean}>
  | Readonly<{kind: 'signOutAccount'}>
  | Readonly<{kind: 'forgetServer'; serverId: string}>
  | Readonly<{kind: 'clearMarker'; serverId: string}>
  | Readonly<{kind: 'fenceShell'}>
  | Readonly<{kind: 'drainRevocations'; serverId: string}>;

export type Transition = Readonly<{snapshot: LifecycleSnapshot; effects: readonly LifecycleEffect[]; row: number}>;

const freeze = <T extends object>(value: T): T => Object.freeze(value);
/** Events that report how a pending action ended. */
const RESULTS = new Set<LifecycleEvent['type']>(['recordsRead', 'connected', 'connectFailed', 'selected', 'pinRejected', 'selectFailed', 'actionFailed',
  'managementUnlocked', 'signedOut', 'forgotten', 'envelopeChanged']);
const same = (a: ViewerRef, b: ViewerRef) => a.accountId === b.accountId && a.profileId === b.profileId && a.familyId === b.familyId;
const signedInStates = new Set(['connecting', 'offline', 'choosingProfile', 'enteringPin', 'ready', 'identityFailed', 'signInEnded']);

/** Where a held sign-in goes once its server has answered (rows 7–9). */
function afterConnect(server: ServerRef, held: HeldSignIn, facts: ConnectedFacts, admitted = false): Pick<Transition, 'effects'> & {state: LifecycleState; row: number} {
  const profile = facts.profiles.find(value => value.id === held.viewer.profileId);
  const canRemember = facts.rememberProfiles;
  // Row 33: a sign-in that already chose its profile (one profile, no PIN; or the server picked it) opens it.
  if (admitted && profile)
    return {row: 33, state: freeze({kind: 'ready', server, viewer: held.viewer, profiles: facts.profiles, remembered: held.remembered,
      manageLocked: false, canRemember}), effects: [freeze({kind: 'drainRevocations', serverId: server.serverId})]};
  if (held.remembered && profile)
    return {row: 7, state: freeze({kind: 'ready', server, viewer: held.viewer, profiles: facts.profiles, remembered: true,
      manageLocked: profile.primary && profile.pinRequired, canRemember}), effects: [freeze({kind: 'drainRevocations', serverId: server.serverId})]};
  if (facts.profiles.length === 1 && profile && !profile.pinRequired)
    return {row: 8, state: freeze({kind: 'ready', server, viewer: held.viewer, profiles: facts.profiles, remembered: false,
      manageLocked: false, canRemember}), effects: [freeze({kind: 'drainRevocations', serverId: server.serverId})]};
  return {row: 9, state: freeze({kind: 'choosingProfile', server, viewer: held.viewer, profiles: facts.profiles, focus: held.viewer.profileId,
    switching: false, canRemember}), effects: [freeze({kind: 'drainRevocations', serverId: server.serverId})]};
}

/**
 * One move. `undefined` means the event does not apply to this state and is ignored (and logged
 * by the runtime). `generation` advances whenever the server, account, profile or sign-in changes,
 * or the device signs out; never on a token refresh.
 */
export function nextLifecycle(current: LifecycleSnapshot, event: LifecycleEvent): Transition | undefined {
  const s = current.state;
  // A pending action ends only with its own result, or when the sign-in it acts for changes (its result is then dropped).
  const keepPending = !RESULTS.has(event.type);
  const to = (state: LifecycleState, row: number, effects: readonly LifecycleEffect[] = [], options: {authority?: boolean} = {}): Transition => {
    const pending = keepPending && !options.authority ? current.pending : undefined;
    return freeze({row, effects: freeze([...effects]), snapshot: freeze({generation: current.generation + (options.authority ? 1 : 0), state,
      ...(pending ? {pending} : {})})});
  };
  const busy = (pending: LifecyclePending, effects: readonly LifecycleEffect[], row: number): Transition =>
    freeze({row, effects: freeze([...effects]), snapshot: freeze({...current, pending})});

  // A pending action ignores further commands until its result arrives (no double submit).
  const command = ['profileTapped', 'pinSubmitted', 'unlockSubmitted', 'switchServer', 'signOutServer', 'signOutAccount', 'forget', 'recordsResetConfirmed'].includes(event.type);
  if (command && current.pending) return undefined;

  switch (event.type) {
  case 'launch':
    return s.kind === 'starting' ? to(s, 0, [{kind: 'readRecords'}]) : undefined;
  case 'recordsRead':
    if (s.kind !== 'starting') return undefined;
    if (event.outcome === 'lost') return to({kind: 'recordsLost'}, 3);
    if (event.outcome === 'reset') return to({kind: 'signedOut', notice: 'recordsReset'}, 4, [], {authority: true});
    if (event.held) return to({kind: 'connecting', server: event.held.server, held: event.held}, 5, [{kind: 'connect', serverId: event.held.server.serverId}], {authority: true});
    return event.servers > 0 ? to({kind: 'choosingServer'}, 2) : to({kind: 'signedOut'}, 1);
  case 'recordsResetConfirmed':
    return s.kind === 'recordsLost' ? busy('reset', [{kind: 'resetRecords'}], 6) : undefined;
  case 'connected':
    if (s.kind !== 'connecting' || !s.held) return undefined;
    {const next = afterConnect(s.server, s.held, event.facts, !!s.admitted); return to(next.state, next.row, next.effects);}
  case 'connectFailed': {
    if (s.kind !== 'connecting') return undefined;
    const f = event.failure;
    if (f.reason === 'pinReentry' && s.held) return to({kind: 'enteringPin', server: s.server, viewer: s.held.viewer, profiles: [],
      profile: {id: s.held.viewer.profileId, name: '', primary: false, pinRequired: true}, reason: 'reentry', canRemember: false, switching: false},
      10, [{kind: 'clearMarker', serverId: s.server.serverId}]);
    if (f.reason === 'signInEnded') return to({kind: 'signInEnded', server: s.server, ...(s.held ? {held: s.held} : {}), username: f.username}, 11, [], {authority: true});
    if (f.reason === 'identity') return to({kind: 'identityFailed', server: s.server, ...(s.held ? {held: s.held} : {}), code: f.code}, 12);
    if (f.reason === 'offline') return to({kind: 'offline', server: s.server, ...(s.held ? {held: s.held, viewer: s.held.viewer} : {}),
      downloads: !!s.held && (s.held.remembered || s.held.profilePinRequired === false)}, 13);
    return undefined;
  }
  case 'retry':
    if (s.kind === 'offline') return to({kind: 'connecting', server: s.server, ...(s.held ? {held: s.held} : {})}, 14, [{kind: 'connect', serverId: s.server.serverId}]);
    if (s.kind === 'identityFailed') return to({kind: 'connecting', server: s.server, ...(s.held ? {held: s.held} : {})}, 32, [{kind: 'connect', serverId: s.server.serverId}]);
    return undefined;
  case 'profileTapped': {
    if (s.kind !== 'choosingProfile') return undefined;
    const profile = s.profiles.find(value => value.id === event.profileId);
    if (!profile) return undefined;
    if (profile.pinRequired) return to({kind: 'enteringPin', server: s.server, viewer: s.viewer, profiles: s.profiles, profile,
      reason: s.switching || profile.id !== s.viewer.profileId ? 'switch' : 'pick', canRemember: s.canRemember, switching: s.switching,
      ...(s.resume ? {resume: s.resume} : {})}, 17);
    if (profile.id === s.viewer.profileId) return to({kind: 'ready', server: s.server, viewer: s.viewer, profiles: s.profiles,
      remembered: s.resume?.remembered ?? false, manageLocked: s.resume?.manageLocked ?? false, canRemember: s.canRemember}, 15);
    return busy('pick', [{kind: 'select', serverId: s.server.serverId, profileId: profile.id, remember: false}], 16);
  }
  case 'pinSubmitted':
    if (s.kind !== 'enteringPin' || s.waitUntil !== undefined && event.pin === '') return undefined;
    return busy('pin', [{kind: 'select', serverId: s.server.serverId, profileId: s.profile.id, pin: event.pin, remember: event.remember && s.canRemember}], 19);
  case 'selected':
    if (current.pending !== 'pick' && current.pending !== 'pin' || s.kind !== 'choosingProfile' && s.kind !== 'enteringPin') return undefined;
    // Every accepted choice is a new sign-in (a successor family), so authority always moves.
    return to({kind: 'ready', server: s.server, viewer: event.facts.viewer, profiles: s.profiles, remembered: event.facts.remembered,
      manageLocked: false, canRemember: s.canRemember}, current.pending === 'pin' ? 19 : 16,
      [{kind: 'fenceShell'}, ...(event.facts.remembered ? [] : [{kind: 'clearMarker', serverId: s.server.serverId} as const])], {authority: true});
  case 'pinRejected':
    if (current.pending !== 'pin' && current.pending !== 'unlock' || s.kind !== 'enteringPin' && s.kind !== 'ready') return undefined;
    if (s.kind === 'ready') return to(s, 20);
    {const {waitUntil: _stale, ...rest} = s;
      return to({...rest, rejected: true, ...(event.waitUntil !== undefined ? {waitUntil: event.waitUntil} : {})}, 20);}
  case 'selectFailed':
  case 'actionFailed':
    // A failed action leaves the screen it started on; its UserAction shows the error.
    return current.pending ? to(s, 36) : undefined;
  case 'otherProfiles':
    if (s.kind !== 'enteringPin') return undefined;
    return to({kind: 'choosingProfile', server: s.server, viewer: s.viewer, profiles: s.profiles, focus: s.profile.id, switching: s.switching,
      canRemember: s.canRemember, ...(s.resume ? {resume: s.resume} : {})}, 21);
  case 'switchProfile':
    if (s.kind !== 'ready') return undefined;
    return to({kind: 'choosingProfile', server: s.server, viewer: s.viewer, profiles: s.profiles, focus: s.viewer.profileId, switching: true,
      canRemember: s.canRemember, resume: {remembered: s.remembered, manageLocked: s.manageLocked}}, 23);
  case 'cancelSwitch':
    if (s.kind !== 'choosingProfile' && s.kind !== 'enteringPin' || !s.switching || !s.resume) return undefined;
    return to({kind: 'ready', server: s.server, viewer: s.viewer, profiles: s.profiles, remembered: s.resume.remembered,
      manageLocked: s.resume.manageLocked, canRemember: s.canRemember}, 18);
  case 'unlockSubmitted':
    if (s.kind !== 'ready' || !s.manageLocked) return undefined;
    return busy('unlock', [{kind: 'select', serverId: s.server.serverId, profileId: s.viewer.profileId, pin: event.pin, remember: true}], 35);
  case 'managementUnlocked':
    if (s.kind !== 'ready' || current.pending !== 'unlock') return undefined;
    return to({...s, viewer: event.facts.viewer, remembered: event.facts.remembered, manageLocked: false}, 35, [],
      {authority: !same(event.facts.viewer, s.viewer)});
  case 'gated':
    if (s.kind !== 'ready') return undefined;
    {const profile = s.profiles.find(value => value.id === s.viewer.profileId) ?? {id: s.viewer.profileId, name: '', primary: false, pinRequired: true};
      return to({kind: 'enteringPin', server: s.server, viewer: s.viewer, profiles: s.profiles, profile, reason: 'reentry', canRemember: s.canRemember,
        switching: false}, 24, [{kind: 'fenceShell'}, {kind: 'clearMarker', serverId: s.server.serverId}]);}
  case 'signInEnded':
    if (s.kind !== 'ready' && s.kind !== 'choosingProfile' && s.kind !== 'enteringPin') return undefined;
    return to({kind: 'signInEnded', server: s.server, held: {server: s.server, viewer: s.viewer, remembered: false}, username: event.username}, 25, [{kind: 'fenceShell'}], {authority: true});
  case 'identityFailed':
    if (s.kind !== 'ready' && s.kind !== 'choosingProfile' && s.kind !== 'enteringPin') return undefined;
    return to({kind: 'identityFailed', server: s.server, held: {server: s.server, viewer: s.viewer, remembered: false}, code: event.code}, 26, [{kind: 'fenceShell'}], {authority: true});
  case 'networkLost':
    return s.kind === 'ready' ? to(s, 27) : undefined;
  case 'switchServer':
    if (s.kind !== 'ready' && s.kind !== 'choosingProfile' && s.kind !== 'offline' && s.kind !== 'choosingServer' && s.kind !== 'signInEnded' && s.kind !== 'identityFailed') return undefined;
    if ('server' in s && s.server.serverId === event.server.serverId && s.kind !== 'offline' && s.kind !== 'signInEnded' && s.kind !== 'identityFailed') return undefined;
    return busy('switchServer', [{kind: 'fenceShell'}, {kind: 'storeSelect', serverId: event.server.serverId}], 28);
  case 'signOutServer':
    if (!signedInStates.has(s.kind) || !('server' in s)) return undefined;
    {const viewer = 'viewer' in s && s.viewer ? s.viewer : 'held' in s ? s.held?.viewer : undefined;
      if (!viewer) return undefined;
      return busy('signOut', [{kind: 'fenceShell'}, {kind: 'clearMarker', serverId: s.server.serverId},
        {kind: 'signOutServer', serverId: s.server.serverId, familyId: viewer.familyId, removeDownloads: event.removeDownloads}], 29);}
  case 'signOutAccount':
    if (!signedInStates.has(s.kind) && s.kind !== 'choosingServer') return undefined;
    return busy('signOutAccount', [{kind: 'fenceShell'}, {kind: 'signOutAccount'}], 30);
  case 'signedOut':
    if (current.pending !== 'signOut' && current.pending !== 'signOutAccount' && current.pending !== 'reset') return undefined;
    return to(event.remaining > 0 ? {kind: 'choosingServer'} : {kind: 'signedOut'}, current.pending === 'reset' ? 6 : current.pending === 'signOutAccount' ? 30 : 29, [], {authority: true});
  case 'forget':
    if (s.kind !== 'identityFailed') return undefined;
    return busy('forget', [{kind: 'clearMarker', serverId: s.server.serverId}, {kind: 'forgetServer', serverId: s.server.serverId}], 31);
  case 'forgotten':
    if (current.pending !== 'forget') return undefined;
    return to(event.remaining > 0 ? {kind: 'choosingServer'} : {kind: 'signedOut'}, 31, [], {authority: true});
  case 'signedIn':
    if (s.kind !== 'signedOut' && s.kind !== 'choosingServer' && s.kind !== 'signInEnded') return undefined;
    return to({kind: 'connecting', server: event.held.server, held: event.held, ...(event.continuation === 'viewer' ? {admitted: true as const} : {})}, 33,
      [{kind: 'connect', serverId: event.held.server.serverId}], {authority: true});
  case 'envelopeChanged': {
    // Row 34: another owner (another tab, a setup claim, a sign-out elsewhere) changed the record.
    if (s.kind === 'starting' || s.kind === 'recordsLost') return undefined;
    // Our own action is writing the record; its result event, not this echo, moves the state.
    if (current.pending && current.pending !== 'switchServer') return undefined;
    const server = 'server' in s ? s.server : undefined;
    const held = event.held;
    if (!held) {
      if (!server && current.pending !== 'switchServer') return undefined;
      return to(event.servers > 0 ? {kind: 'choosingServer'} : {kind: 'signedOut'}, 34, [{kind: 'fenceShell'}], {authority: true});
    }
    if (current.pending === 'switchServer' || !server || held.server.serverId !== server.serverId)
      return to({kind: 'connecting', server: held.server, held}, current.pending === 'switchServer' ? 28 : 34,
        [{kind: 'connect', serverId: held.server.serverId}], {authority: true});
    const viewer = 'viewer' in s ? s.viewer : undefined;
    if (!viewer || same(viewer, held.viewer)) return undefined;
    // Same server, another sign-in (another tab picked a profile): follow it, never sign out.
    if (s.kind === 'ready') return to({...s, viewer: held.viewer, remembered: held.remembered, manageLocked: false}, 34, [{kind: 'fenceShell'}], {authority: true});
    if (s.kind === 'choosingProfile' || s.kind === 'enteringPin' || s.kind === 'offline') return to({...s, viewer: held.viewer}, 34, [], {authority: true});
    return undefined;
  }
  }
  return undefined;
}

export const initialLifecycle: LifecycleSnapshot = freeze({generation: 0, state: freeze({kind: 'starting'})});
