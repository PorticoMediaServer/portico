import {ApiError, HttpLocalApi, type LocalSession} from '../../../../packages/client-core/src';
import type {AppServerSession, AppSessionStorage, AppSessionStore} from '../../../../packages/client-core/src/app-sessions';
import {releaseQuietly} from '../../../../packages/client-core/src/cleanup';
import {installationIdentity} from '../../../../packages/client-core/src/installation';
import {parseDirectSnapshot, selectDirectProfile, type DirectSnapshot} from '../../../../packages/client-core/src/profile-management';
import {rememberedFor} from '../../../../packages/client-core/src/remember-marker';
import type {RememberedServer} from '../../../../packages/client-core/src/server-connections';
import {restoreNativeSession} from '../../../../packages/client-core/src/server-connections';
import type {HeldRead, LifecyclePorts, RecordsRead, SelectInput, TransitionRecord} from '../../../../packages/client-core/src/session-lifecycle-runtime';
import type {ConnectFailure, ConnectedFacts, HeldSignIn, IdentityFailureCode, PickerProfile, SelectedFacts} from '../../../../packages/client-core/src/session-lifecycle';
import {wait, type Scheduler} from '../../../../packages/client-core/src/timer';
import {routePath} from '../../../../packages/contracts/src/routes';
import {offlineDownloads, offlineOwnerKey} from '../../platform/offline-downloads';
import {removeCapturedDownloads} from '../screens/accounts/sign-out-downloads';
import {createNativeShellAccountBinding, profileSessionRole} from '../shell/account-binding';
import {nativeAccounts} from './accounts';
import {hostedUrl} from './account-authority';
import {nativeAppSessionStorage, nativeAppSessions, readNativeSelectedViewer, subscribeNativeAppSessions} from './app-session-storage';
import {journal} from './error-capture';
import {nativeIdentityFailure, nativeRouteIdentityFailure, type NativeIdentityFailure, type NativeIdentityServer} from './identity-failure';
import {beginNativeInstallation} from './installation';
import {logPickFailure, provenIdentityMismatch} from './pick-failure';
import {isSecureRecordsLost, prepareSecureRecords, resetSecureRecords} from './secure-records';
import {nativeNetworkFetch, nativeServerEnvironment, restoreNativeConnection} from './server-connections';
import {readNativeServerDisplays} from './server-display';
import {drainRevocations, revokeInBackground} from './session-revocation';
import {nativeSignInEnded, readNativeSignInHint, type NativeSignInEnded, type NativeSignInHint} from './sign-in-ended';
import {purgeStaleRoutes} from './stale-routes';

/** Queued sign-outs start this long after a restore, never during the reads that open Home
 * (E-mobile-01). Mirrors shell/bootstrap.ts REVOCATION_DRAIN_DELAY_MS, which C2b deletes with the rest. */
export const LIFECYCLE_REVOCATION_DRAIN_DELAY_MS = 10_000;

/** Everything the native ports touch on the device. Defaults wire the real modules; tests hand in fakes. */
export type NativeLifecycleDeps = Readonly<{
  /** The mounted shell's private state is retired before the sign-in it was built on changes. Supplied by the caller. */
  fenceShell: () => void;
  sessions: Pick<AppSessionStore, 'read' | 'selected' | 'setRemembered' | 'setProfilePinRequired'>;
  storage: Pick<AppSessionStorage, 'change'>;
  accounts: Pick<typeof nativeAccounts, 'admission' | 'remember' | 'switchServer' | 'signOutServer' | 'signOutPorticoAccount' | 'forgetServer' | 'accountCredentials' | 'onCancel'>;
  subscribeRecords: (listener: () => void) => () => void;
  prepareSecureRecords: () => Promise<boolean>;
  isSecureRecordsLost: (error: unknown) => boolean;
  resetSecureRecords: () => Promise<boolean>;
  readServerDisplays: typeof readNativeServerDisplays;
  readSignInHint: typeof readNativeSignInHint;
  beginInstallation: typeof beginNativeInstallation;
  installationIdentity: typeof installationIdentity;
  restoreConnection: typeof restoreNativeConnection;
  restoreSession: typeof restoreNativeSession;
  environment: typeof nativeServerEnvironment;
  readSelectedViewer: typeof readNativeSelectedViewer;
  purgeStaleRoutes: typeof purgeStaleRoutes;
  hostedUrl: string;
  selectProfile: typeof selectDirectProfile;
  /** Ends a freshly issued session that must not be used (a proven identity mismatch), over the route that issued it. */
  logoutIssued: (api: HttpLocalApi, issued: LocalSession) => Promise<void>;
  revokeInBackground: typeof revokeInBackground;
  drainRevocations: typeof drainRevocations;
  /** Removes the captured viewer's downloads, as the sign-out sheet does. */
  removeDownloads: (captured: AppServerSession) => Promise<unknown>;
  /** Wall clock, for marker expiry. */
  now: () => number;
  scheduler?: Scheduler;
  drainDelayMs: number;
  /** One line of codes only; goes to the native error journal. */
  note: (line: string) => void;
}>;

/** A server connection the lifecycle opened and the shell may adopt. Released by `releaseConnection`. */
export type NativeConnected = Readonly<{
  serverId: string; api: HttpLocalApi; session: LocalSession; installation: ReturnType<typeof beginNativeInstallation>;
  /** What the shell would otherwise read again: the prepared installation, the held entry, its server and sign-in hint. */
  prepared: Awaited<ReturnType<ReturnType<typeof beginNativeInstallation>['prepare']>>;
  selected: AppServerSession; server: NativeIdentityServer; hint?: NativeSignInHint;
  /** The account as `connect` read it: the picker's faces and art, and Forgot PIN's proof. */
  account: DirectSnapshot;
}>;
export type NativeLifecyclePorts = LifecyclePorts & Readonly<{
  /** The live connection `connect` opened for this server, until it is released or the sign-in changes. */
  connection(serverId: string): NativeConnected | undefined;
  /** Hands the live connection to the shell, which owns and disposes it from then on. */
  adopt(serverId: string): NativeConnected | undefined;
  /** Opens the held sign-in's connection again and hands it over: a pick or a switch publishes a new
   *  sign-in without one, and the shell always opens from the lifecycle's connection. */
  reconnect(serverId: string, signal: AbortSignal): Promise<NativeConnected | undefined>;
  /** What ended the last connect to `serverId` (identityFailed, signInEnded), for the screen that names it. */
  failure(serverId: string): NativeIdentityFailure | NativeSignInEnded | undefined;
  /** The root lends its installation: installation generations are app-wide, so the ports must never
   *  begin a competing one while a root holds its own (that would retire the root). */
  lendInstallation(owner: ReturnType<typeof beginNativeInstallation> | undefined): void;
  releaseConnection(): void;
}>;

const defaults = (): Omit<NativeLifecycleDeps, 'fenceShell'> => ({
  sessions: nativeAppSessions, storage: nativeAppSessionStorage, accounts: nativeAccounts, subscribeRecords: subscribeNativeAppSessions,
  prepareSecureRecords, isSecureRecordsLost, resetSecureRecords, readServerDisplays: readNativeServerDisplays, readSignInHint: readNativeSignInHint,
  beginInstallation: beginNativeInstallation, installationIdentity, restoreConnection: restoreNativeConnection, restoreSession: restoreNativeSession,
  environment: nativeServerEnvironment, readSelectedViewer: readNativeSelectedViewer, purgeStaleRoutes, hostedUrl, selectProfile: selectDirectProfile,
  logoutIssued: async (api, issued) => {
    const candidate = new HttpLocalApi(api.baseUrl, issued.accessToken, nativeNetworkFetch);
    candidate.useRoutes(api.getRouteConnection()!); await candidate.logout();
  },
  revokeInBackground, drainRevocations,
  removeDownloads: captured => removeCapturedDownloads(captured, {
    selected: () => nativeAppSessions.selected(),
    entries: () => offlineDownloads.getSnapshot().entries,
    remove: async entry => {
      const owner = offlineOwnerKey(captured.connection.session.viewer);
      const live = offlineDownloads.getSnapshot().entries.find(value => value.id === entry.id
        && (value.receipt ? offlineOwnerKey(value.receipt.claims.viewer) : value.owner) === owner);
      if (live) await offlineDownloads.remove(live);
    },
  }),
  now: Date.now, drainDelayMs: LIFECYCLE_REVOCATION_DRAIN_DELAY_MS,
  note: line => journal.note('warn', line),
});

/** The shell ended because this profile's PIN changed or its remembered devices were forgotten
 * (profile_pin_reentry): the device asks for the PIN, never for a sign-in. */
export class NativeProfileGated extends Error {
  readonly code = 'profile_pin_reentry';
  constructor() {super('native_profile_gated'); this.name = 'NativeProfileGated';}
}
export const nativeProfileGated = (error: unknown): boolean => (error as {code?: unknown} | null)?.code === 'profile_pin_reentry';

const cancelled = () => Object.assign(new Error('native_session_lifecycle_cancelled'), {name: 'AbortError', code: 'cancelled'});
// CC-03: the server may change this account's role; it is reconciled into the session, so it is not another viewer.
const sameViewer = (a: LocalSession, b: LocalSession): boolean => a.sessionFamilyId === b.sessionFamilyId
  && (['serverId', 'accountId', 'profileId', 'authority'] as const).every(key => a.viewer[key] === b.viewer[key]);
const viewerRef = (session: LocalSession) => Object.freeze({accountId: session.viewer.accountId, profileId: session.viewer.profileId,
  role: session.viewer.role, familyId: session.sessionFamilyId});

/** What the last `connect` learned, which `classifyConnect` needs to read the failure that follows it. */
type ConnectContext = Readonly<{server: NativeIdentityServer; hint?: NativeSignInHint; expiresAt: string; facts?: ConnectedFacts}>;

/**
 * The native side of the session lifecycle (planning/v3/rebuilds/C-DESIGN.md §10, C2a): wraps today's
 * owners (accounts, profile selection, revocation, forget, sign-in ended, identity failure, protected
 * records) as `LifecyclePorts`. Nothing calls it yet; C2b moves the shell onto it.
 */
export function createNativeLifecyclePorts(overrides: Pick<NativeLifecycleDeps, 'fenceShell'> & Partial<NativeLifecycleDeps>): NativeLifecyclePorts {
  const deps: NativeLifecycleDeps = {...defaults(), ...overrides};
  let connected: NativeConnected | undefined, context: ConnectContext | undefined, drainAbort: AbortController | undefined;
  let lent: ReturnType<typeof beginNativeInstallation> | undefined;
  let failed: Readonly<{serverId: string; error: NativeIdentityFailure | NativeSignInEnded}> | undefined;
  // Writes the ports make themselves must not wake `observeRecords`: that is for other owners.
  let ownWrites = 0;
  const own = async <T>(work: () => Promise<T>): Promise<T> => {ownWrites++; try {return await work();} finally {ownWrites--;}};

  const releaseConnection = () => {
    const held = connected; connected = undefined;
    drainAbort?.abort(); drainAbort = undefined;
    if (!held) return;
    releaseQuietly('lifecycle route', () => held.api.getRouteConnection()?.dispose());
    releaseQuietly('lifecycle token', () => held.api.setAccessToken(''));
    if (held.installation !== lent) releaseQuietly('lifecycle installation', () => held.installation.dispose());
  };

  const holding = (entry: AppServerSession, name: string): HeldSignIn => {
    const {pin, session} = entry.connection;
    return Object.freeze({
      server: Object.freeze({serverId: pin.serverId, name, signIn: entry.signIn}),
      viewer: viewerRef(session),
      remembered: rememberedFor(entry.remembered && {serverId: pin.serverId, ...entry.remembered},
        {serverId: pin.serverId, familyId: session.sessionFamilyId, profileId: session.viewer.profileId}, deps.now()),
      ...(entry.profilePinRequired === undefined ? {} : {profilePinRequired: entry.profilePinRequired}),
    });
  };

  const readHeld = async (): Promise<HeldRead> => {
    const record = await deps.sessions.read();
    const entry = record.servers.find(value => value.connection.pin.serverId === record.selectedServerId);
    if (!entry) return {servers: record.servers.length};
    // A missing display name is an incomplete record, not a lost one: it must never read as "records lost"
    // (which offers to reset the device). connect re-learns the name from the server.
    const [display] = await deps.readServerDisplays([entry]).catch(() => []);
    return {servers: record.servers.length, held: holding(entry, display?.name ?? '')};
  };

  const connect = async (serverId: string, signal: AbortSignal): Promise<ConnectedFacts> => {
    releaseConnection(); context = undefined;
    const check = () => {if (signal.aborted) throw cancelled();};
    const borrowed = lent, installation = borrowed ?? deps.beginInstallation(signal);
    let api: HttpLocalApi | undefined;
    try {
      check();
      const prepared = await installation.prepare(); check();
      const {saved, connection, env} = prepared;
      if (!saved.viewer) throw cancelled();
      const original = saved.viewer;
      // E-mobile-01: routes to the retired `c-…` direct names can never answer; forget them once.
      const beforePurge = await deps.sessions.read(); check();
      if (deps.purgeStaleRoutes(beforePurge) !== beforePurge) {await deps.storage.change(record => deps.purgeStaleRoutes(record as typeof beforePurge)).catch(() => {}); check();}
      const selected = await deps.sessions.selected(); check();
      if (!selected || selected.connection.pin.serverId !== serverId || !sameViewer(original.session, selected.connection.session)
        || selected.connection.logicalOrigin !== original.serverUrl) throw cancelled();
      const [display] = await deps.readServerDisplays([selected]).catch(() => []); check();
      const server: NativeIdentityServer = Object.freeze({serverId, familyId: selected.connection.session.sessionFamilyId,
        fingerprint: selected.connection.pin.fingerprint, signIn: selected.signIn, name: display?.name ?? ''});
      const hint = await deps.readSignInHint(original.session, original.serverUrl, selected.connection.pin.fingerprint, selected.signIn).catch(() => undefined); check();
      context = Object.freeze({server, ...(hint ? {hint} : {}), expiresAt: original.session.expiresAt});
      // A server-local session renews locally even while Portico Services is unreachable.
      const hostedCredentials = connection?.hosted?.origin === deps.hostedUrl ? () => deps.accounts.accountCredentials() : undefined;
      const restored = await deps.restoreConnection(connection?.hosted?.origin ?? original.serverUrl, hostedCredentials, original.session, env, signal);
      api = restored?.api; check();
      if (!restored || !sameViewer(original.session, restored.session)) throw Object.assign(new Error('native_viewer_changed'), {code: 'identity_mismatch'});
      const reread = await deps.readSelectedViewer(); check();
      if (!reread || reread.serverUrl !== original.serverUrl || !sameViewer(reread.session, restored.session)) throw cancelled();
      const route = restored.api.getRouteConnection()?.getSnapshot();
      const routeFailure = route ? nativeRouteIdentityFailure(route, server) : undefined;
      if (routeFailure) throw routeFailure;

      const snapshot = parseDirectSnapshot(await restored.api.request(routePath('get_direct_account'), 'GET', undefined, signal),
        {authority: 'local', serverId, accountId: restored.session.viewer.accountId}, true); check();
      const profiles: readonly PickerProfile[] = Object.freeze(snapshot.profiles.map(value => Object.freeze({
        id: value.id, name: value.name, primary: value.primary, pinRequired: value.pinRequired})));
      const facts: ConnectedFacts = Object.freeze({profiles, rememberProfiles: snapshot.rememberProfiles === true});
      // A refusal after this point (profile_pin_reentry) names its PIN screen from this account.
      context = Object.freeze({...context!, facts});
      const held = profiles.find(value => value.id === restored.session.viewer.profileId);
      // The last-seen PIN state is a cache for offline openings; failing to save it never fails the connection.
      if (held) await own(() => deps.sessions.setProfilePinRequired(serverId, restored.session.sessionFamilyId, held.pinRequired)).catch(() => {});
      connected = Object.freeze({serverId, api: restored.api, session: restored.session, installation, prepared, selected, server, account: snapshot, ...(hint ? {hint} : {})});
      return facts;
    } catch (error) {
      releaseQuietly('lifecycle failed route', () => api?.getRouteConnection()?.dispose());
      releaseQuietly('lifecycle failed token', () => api?.setAccessToken(''));
      if (!borrowed) releaseQuietly('lifecycle failed installation', () => installation.dispose());
      throw error;
    }
  };

  const classifyConnect = (error: unknown): ConnectFailure | undefined => {
    failed = undefined;
    // The order bootstrap.ts uses: a server that changed under the sign-in outranks the sign-in ending.
    if (context) {
      const identity = nativeIdentityFailure(error, context.server);
      if (identity) {failed = Object.freeze({serverId: context.server.serverId, error: identity}); return {reason: 'identity', code: identity.code as IdentityFailureCode};}
      const ended = nativeSignInEnded(error, context.hint, context.expiresAt);
      if (ended) {failed = Object.freeze({serverId: context.server.serverId, error: ended}); return {reason: 'signInEnded', username: ended.hint.username};}
    }
    if (error instanceof ApiError && error.code === 'profile_pin_reentry') return {reason: 'pinReentry', ...(context?.facts ? {facts: context.facts} : {})};
    return undefined;
  };

  /** profile-selection.ts `select`, step for step. */
  const select = async (input: SelectInput, signal: AbortSignal): Promise<SelectedFacts> => {
    const {serverId, profileId, pin, remember} = input;
    const abort = new AbortController(), installed = deps.installationIdentity();
    const onAbort = () => abort.abort();
    signal.addEventListener('abort', onAbort, {once: true}); if (signal.aborted) abort.abort();
    const off = deps.accounts.onCancel(() => abort.abort());
    const current = () => !abort.signal.aborted && !!installed && deps.installationIdentity() === installed;
    const check = () => {if (!current()) throw Object.assign(new Error('native_profile_selection_cancelled'), {name: 'AbortError'});};
    const familyId = connected?.serverId === serverId ? connected.session.sessionFamilyId : undefined;
    let revoking: Promise<void> | undefined, api: HttpLocalApi | undefined, issued: LocalSession | undefined, published = false, refused = false, step = 'read';
    let facts: SelectedFacts | undefined;
    try {
      check();
      const captured = await deps.sessions.selected(); check();
      if (!captured || captured.connection.pin.serverId !== serverId || familyId !== undefined && captured.connection.session.sessionFamilyId !== familyId)
        throw Object.assign(new Error('native_profile_selection_changed'), {code: 'cancelled'});
      const record: RememberedServer = captured.connection;
      const restored = await deps.restoreSession(deps.environment(serverId), undefined, record.session); api = restored?.api; check();
      if (!restored || !api) throw Object.assign(new Error('native_profile_selection_changed'), {code: 'cancelled'});
      const accountBinding = createNativeShellAccountBinding(captured, restored.session, {
        current, session: () => restored.session, pin: () => api?.getRouteConnection()?.pin,
        readSelected: () => deps.sessions.selected(),
      });
      const admission = await deps.accounts.admission(captured.signIn, record.hosted && {origin: record.hosted.origin, accountId: record.hosted.accountId}); check();
      await accountBinding.assertCurrent(); check();
      step = 'select';
      const selected = await deps.selectProfile(api, {authority: 'local', serverId, accountId: record.session.viewer.accountId},
        profileId, {pin, installationId: installed?.installationId, remember}, abort.signal);
      issued = selected.session; step = 'verify'; check();
      api.setAccessToken(issued.accessToken);
      // Selection returns a session, not an account snapshot. Read the actual
      // account under that fresh grant before allowing it into protected storage.
      const account = parseDirectSnapshot(await api.request(routePath('get_direct_account'), 'GET', undefined, abort.signal),
        {authority: 'local', serverId, accountId: record.session.viewer.accountId}, true);
      // E-tv-41: identity only. The snapshot was read under the new profile's session, so its account
      // role is that session's role; the issued session's role is checked against it just below.
      check(); accountBinding.assertSameAccount(account);
      if (profileSessionRole(account.account, issued.viewer.profileId) !== issued.viewer.role || !account.profiles.some(value => value.id === issued!.viewer.profileId))
        throw Object.assign(new Error('native_profile_selection_changed'), {code: 'identity_mismatch'});
      await accountBinding.assertCurrent(); check();
      step = 'publish';
      let rememberedMarker = false;
      await own(async () => {
        await deps.accounts.remember(api!, issued!, admission, record.hosted, current); published = true;
        // From here the pick is published. What follows can fail without failing it.
        const picked = account.profiles.find(value => value.id === issued!.viewer.profileId);
        if (picked) await deps.sessions.setProfilePinRequired(serverId, issued!.sessionFamilyId, picked.pinRequired).catch(() => {});
        // The marker is written after the new family, in its own write. A crash between the two leaves the
        // profile not remembered, which is the safe direction: the picker shows at the next launch.
        if (selected.remembered) {
          try {
            await deps.sessions.setRemembered(serverId, issued!.sessionFamilyId, {profileId: issued!.viewer.profileId, expiresAt: selected.remembered.expiresAt});
            rememberedMarker = true;
          } catch { /* Not remembered; the pick stands. */ }
        }
      });
      facts = Object.freeze({viewer: viewerRef(issued), remembered: rememberedMarker});
      // The previous family belongs to this server only. A switch to another server never
      // takes this path and never ends that server's family. Ending it is mandatory but never
      // blocks or fails the pick: queued durably first, then one bounded try over the live route.
      if (restored.session.sessionFamilyId !== issued.sessionFamilyId)
        revoking = deps.revokeInBackground({...record, session: restored.session}, api.getRouteConnection());
    } catch (error) {
      // E-mobile-01/25: only a proven different identity ends the picked session on the server.
      if (issued && !published) {refused = provenIdentityMismatch(error); logPickFailure('switch', step, error);}
      throw error;
    } finally {
      if (issued && !published && refused && api) await deps.logoutIssued(api, issued).catch(() => {});
      // The revocation try uses this route, so it is disposed once that bounded try settles.
      const route = api?.getRouteConnection();
      if (revoking) void revoking.then(() => route?.dispose()); else route?.dispose();
      off(); signal.removeEventListener('abort', onAbort);
      // The mounted connection was for the family this pick replaced.
      if (published) releaseConnection();
    }
    return facts!;
  };

  const afterEnding = async (): Promise<HeldRead> => {releaseConnection(); return readHeld();};

  return Object.freeze({
    connection: (serverId: string) => connected?.serverId === serverId ? connected : undefined,
    lendInstallation(owner) {lent = owner;},
    adopt(serverId: string) {
      const held = connected?.serverId === serverId ? connected : undefined;
      if (held) connected = undefined;
      return held;
    },
    failure: (serverId: string) => failed?.serverId === serverId ? failed.error : undefined,
    async reconnect(serverId: string, signal: AbortSignal) {
      await connect(serverId, signal);
      const held = connected?.serverId === serverId ? connected : undefined;
      if (held) connected = undefined;
      return held;
    },
    releaseConnection,
    async readRecords(): Promise<RecordsRead> {
      let reset: boolean;
      try {reset = await deps.prepareSecureRecords();}
      catch (error) {
        if (deps.isSecureRecordsLost(error)) return {outcome: 'lost', servers: 0};
        throw error;
      }
      const read = await readHeld();
      return {outcome: reset ? 'reset' : 'ok', ...read};
    },
    // False means the key answered after all; either way the device is reopened from the top (C2b).
    async resetRecords() {await deps.resetSecureRecords();},
    readHeld,
    connect,
    select,
    classifyConnect,
    storeSelect: async (serverId: string) => {await deps.accounts.switchServer(serverId);},
    async signOutServer(serverId: string, familyId: string, removeDownloads: boolean) {
      if (removeDownloads) {
        const captured = (await deps.sessions.read()).servers.find(value => value.connection.pin.serverId === serverId && value.connection.session.sessionFamilyId === familyId);
        if (captured) await deps.removeDownloads(captured);
      }
      await deps.accounts.signOutServer(serverId, familyId);
      return afterEnding();
    },
    async signOutAccount() {
      const credentials = deps.accounts.accountCredentials();
      await credentials.synchronize();
      const account = credentials.getSnapshot().session;
      if (!account) throw Object.assign(new Error('native_account_changed'), {code: 'account_changed'});
      await deps.accounts.signOutPorticoAccount({accountId: account.account.id, familyId: account.familyId});
      return afterEnding();
    },
    async forgetServer(serverId: string) {
      const entry = (await deps.sessions.read()).servers.find(value => value.connection.pin.serverId === serverId);
      if (!entry) throw cancelled();
      await deps.accounts.forgetServer({serverId, familyId: entry.connection.session.sessionFamilyId, fingerprint: entry.connection.pin.fingerprint});
      return afterEnding();
    },
    async clearMarker(serverId: string) {
      const family = connected?.serverId === serverId ? connected.session.sessionFamilyId
        : (await deps.sessions.read()).servers.find(value => value.connection.pin.serverId === serverId)?.connection.session.sessionFamilyId;
      if (family) await own(() => deps.sessions.setRemembered(serverId, family, undefined));
    },
    fenceShell: () => deps.fenceShell(),
    drainRevocations(serverId: string) {
      const held = connected, live = held?.serverId === serverId ? held.api.getRouteConnection() : undefined;
      if (!live) return;
      drainAbort?.abort();
      const controller = drainAbort = new AbortController();
      // Never blocks: the drain starts after Home has had its turn, and a release or a new connection cancels it.
      void wait(deps.drainDelayMs, {signal: controller.signal, ...(deps.scheduler ? {scheduler: deps.scheduler} : {})})
        .then(() => {if (!controller.signal.aborted) return deps.drainRevocations(serverId, live);})
        .catch(() => {});
    },
    observeRecords: (listener: () => void) => deps.subscribeRecords(() => {if (!ownWrites) listener();}),
    log: (record: TransitionRecord) => {deps.note(`portico_session ${JSON.stringify({t: record.t, from: record.from, event: record.event, to: record.to, row: record.row, generation: record.generation})}`);},
  }) as NativeLifecyclePorts;
}
