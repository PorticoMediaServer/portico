import type {LibraryContentApi} from '@core/library-content.ts';
import {ApiError, HttpLocalApi, type LocalSession} from '@core/index.ts';
import type {AppSessionStore} from '@core/app-sessions.ts';
import {parseLocalSession} from '@core/local-session.ts';
import {parseDirectSnapshot} from '@core/profile-management.ts';
import {parseSetupSession} from '@core/setup-onboarding.ts';
import {readSetupState} from '@core/setup-state.ts';
import type {ServerSignIn} from '@core/sign-in.ts';
import {routeBase64, sameServerPin, webRouteCrypto, type RouteCrypto, type ServerPin} from '@core/route-identity.ts';
import type {ConnectionEnvironment, rememberNativeSession} from '@core/server-connections.ts';
import {CodedError} from '@core/server-messages.ts';
import {setupAddressAllowed} from '@core/setup-address.ts';
import {SETUP_CLAIM_LIFETIME_MS} from '@core/setup-claim-attempt.ts';
import {requestSetupApproval} from '@core/setup-approval-client.ts';
import {randomId} from '@core/random-id.ts';
import type {SetupCredentials, SetupLibrary, SetupPortContext, SetupPorts, SetupRead} from '@core/setup-controller.ts';
import type {SetupAttempt, SetupLocal, SetupRecord, SetupStore} from '@core/setup-store.ts';
import {decodePostLibrariesResponse} from '../../../packages/contracts/src/decoders.ts';
import {encodedRoutePath, routeBody, routePath} from '../../../packages/contracts/src/routes.ts';
import type {restoreBrowserConnection} from './server-connections';

/** The verified pairing of the server this page was served by (`createBrowserSetupPorts().connect`). */
export type SetupPairing = Readonly<{server: ServerSignIn; api: HttpLocalApi; pin: ServerPin; dispose: () => void}>;
export type WebSetupAdapterDeps = Readonly<{
  hostedOrigin: string;
  pageOrigin: () => string;
  pair: (signal: AbortSignal) => Promise<SetupPairing>;
  sessions: () => Pick<AppSessionStore, 'read' | 'selected' | 'connectionStorage'>;
  environment: (serverId: string) => ConnectionEnvironment;
  restore: typeof restoreBrowserConnection;
  remember: typeof rememberNativeSession;
  store: (serverId: string) => SetupStore;
  installationId: () => string | undefined;
  navigate: (url: string) => void;
  /** The page that opened this setup is still the one showing. */
  current?: () => boolean;
  now?: () => number;
  crypto?: RouteCrypto;
  newRequestId?: () => string;
}>;
export type WebSetupAdapter = Readonly<{ports: SetupPorts; libraryFolders: LibraryContentApi; dispose: () => void}>;

type Paired = SetupPairing & {revision: number; installationId: string};
type Own = {requestId: string; revision: string};
const changed = () => new CodedError('cancelled', 'The pending setup or selected server changed.');
const refused = (error: unknown, ...codes: string[]) => error instanceof ApiError && codes.includes(error.code);

/** The browser's half of first-run setup for the controller (B-DESIGN §6): pairing, the setup routes under the setup token,
 * the v2 records, the PKCE attempt and owner adoption. The server decides every phase; this only carries its answers.
 * Every await re-checks that the page, the installation, the selected-server revision and the pairing are still the
 * ones captured, so a late answer can never write into a successor. */
export function createWebSetupAdapter(deps: WebSetupAdapterDeps): WebSetupAdapter {
  const now = deps.now ?? Date.now, crypto = deps.crypto ?? webRouteCrypto, newRequestId = deps.newRequestId ?? randomId;
  const life = new AbortController();
  let paired: Paired | undefined, own: Own | undefined, issued: {serverId: string; setupToken: string} | undefined;
  let owner: {api: HttpLocalApi; session: LocalSession} | undefined, pendingLocal: LocalSession | undefined;

  const live = () => {
    let opening = true;
    try {opening = deps.current?.() ?? true;} catch {opening = false;}
    if (life.signal.aborted || !opening) throw changed();
  };
  const installation = () => {const id = deps.installationId(); if (!id) throw changed(); return id;};
  /** The captured pairing is still this page's: same installation, same selection revision, route ready on the same pin. */
  const assertPaired = async (p: Paired) => {
    live();
    const route = p.api.getRouteConnection();
    if (paired !== p || installation() !== p.installationId || !route || route.getSnapshot().phase !== 'ready' || !sameServerPin(route.pin, p.pin)) throw changed();
    const selection = await deps.sessions().read();
    live();
    if (paired !== p || selection.revision !== p.revision) throw changed();
  };
  async function pair(signal: AbortSignal): Promise<Paired> {
    live();
    if (paired) {await assertPaired(paired); return paired;}
    const before = await deps.sessions().read(), installationId = installation();
    live();
    const pairing = await deps.pair(AbortSignal.any([signal, life.signal]));
    try {
      live();
      const after = await deps.sessions().read();
      live();
      if (after.revision !== before.revision || installation() !== installationId) throw changed();
      paired = Object.freeze({...pairing, revision: after.revision, installationId}) as Paired;
      return paired;
    } catch (error) {pairing.dispose(); throw error;}
  }
  /** An api that carries this setup's token, over the verified route. */
  const tokenApi = (p: Paired, token: string) => {
    const api = new HttpLocalApi(p.api.baseUrl, token), route = p.api.getRouteConnection();
    if (!route) throw changed();
    api.useRoutes(route);
    return api;
  };
  const records = (p: Paired) => deps.store(p.pin.serverId);
  const ownRecord = async (p: Paired): Promise<SetupRecord> => {
    const local = await records(p).read();
    await assertPaired(p);
    if (!local.record || local.record.serverId !== p.pin.serverId) throw changed();
    return local.record;
  };
  const ownRevision = () => {if (!own) throw changed(); return own;};

  /** The selected session for this server is its owner's, restored and checked against the server. */
  async function ownerSnapshot(api: HttpLocalApi, session: LocalSession, signal: AbortSignal) {
    if (session.viewer.role !== 'owner') throw changed();
    const snapshot = parseDirectSnapshot(await api.request(routePath('get_direct_account'), 'GET', undefined, signal),
      {authority: 'local', serverId: session.viewer.serverId, accountId: session.viewer.accountId}, true);
    if (snapshot.account.role !== 'owner' || snapshot.account.disabled || !snapshot.capabilities.manageServer || snapshot.account.primaryProfileId !== session.viewer.profileId) throw changed();
  }
  async function heldOwner(p: Paired, signal: AbortSignal): Promise<boolean> {
    if (owner) return true;
    const state = await deps.sessions().read();
    live();
    const selected = state.servers.find(value => value.connection.pin.serverId === state.selectedServerId);
    if (!selected || !sameServerPin(selected.connection.pin, p.pin) || selected.connection.session.viewer.role !== 'owner') return false;
    const restored = await deps.restore(selected.connection.session);
    await assertPaired(p);
    if (!restored || restored.session.viewer.role !== 'owner') return false;
    try {
      await ownerSnapshot(restored.api, restored.session, signal);
      await assertPaired(p);
    } catch (error) {restored.api.getRouteConnection()?.dispose(); if (error instanceof CodedError) return false; throw error;}
    owner = restored;
    return true;
  }

  const ports: SetupPorts = {
    // The store is per server: before pairing there is nothing to read or retire.
    store: {
      read: async () => paired ? records(paired).read() : {},
      change: async (expect, next) => paired ? records(paired).change(expect, next) : false,
    },
    async readState(context: SetupPortContext): Promise<SetupRead> {
      const p = await pair(context.signal);
      const local: SetupLocal = await records(p).read();
      await assertPaired(p);
      const record = local.record?.serverId === p.pin.serverId ? local.record : undefined;
      try {
        const state = await readSetupState(record ? tokenApi(p, record.setupToken) : p.api, context.signal);
        await assertPaired(p);
        own = typeof state.setup === 'object' ? {requestId: state.setup.requestId, revision: state.setup.revision} : undefined;
        const ownerSessionHeld = state.status === 'complete' ? await heldOwner(p, context.signal) : undefined;
        return {server: {kind: 'state', state, ...(ownerSessionHeld !== undefined ? {ownerSessionHeld} : {})}, local};
      } catch (error) {
        if (record && refused(error, 'unauthorized')) return {server: {kind: 'tokenRefused'}, local};
        if (refused(error, 'setup_source_not_local')) return {server: {kind: 'notLocalSource'}, local};
        throw error;
      }
    },
    async start(context) {
      const p = await pair(context.signal);
      // The same rule the server and Portico Services apply: a public http address is refused before any request.
      if (!setupAddressAllowed(p.api.baseUrl)) return {kind: 'addressNotAllowed'};
      try {
        const raw = await p.api.request<{serverId?: unknown; setupToken?: unknown}>(routePath('start_browser_setup'), 'POST', {}, context.signal);
        await assertPaired(p);
        if (raw?.serverId !== p.pin.serverId || typeof raw.setupToken !== 'string' || !raw.setupToken) throw Object.assign(new Error('The setup answer did not match this server.'), {code: 'identity_mismatch'});
        issued = {serverId: raw.serverId, setupToken: raw.setupToken};
        return {kind: 'startDone'};
      } catch (error) {
        if (refused(error, 'setup_in_progress')) return {kind: 'setupInProgress', ...((error as ApiError).retryAfterSeconds ? {retryAfterSeconds: (error as ApiError).retryAfterSeconds} : {})};
        if (refused(error, 'setup_source_not_local')) return {kind: 'notLocalSource'};
        if (refused(error, 'setup_complete', 'not_setup_required', 'conflict')) return {kind: 'alreadyComplete'};
        throw error;
      }
    },
    async initializeHosted(name, context) {
      const p = await pair(context.signal);
      if (!issued || issued.serverId !== p.pin.serverId) throw changed();
      const token = issued.setupToken, store = records(p);
      // The record is written before the request, so an answer lost on the way still reconciles; a Try again reuses it.
      let record = (await store.read()).record;
      await assertPaired(p);
      if (!record || record.setupToken !== token || record.authMode !== 'hosted') {
        const fresh: SetupRecord = Object.freeze({version: 2, serverId: p.pin.serverId, requestId: newRequestId(), setupToken: token, tokenAt: now(), name, authMode: 'hosted'});
        if (!await store.change({requestId: record?.requestId}, {record: fresh})) throw changed();
        record = fresh;
      }
      await assertPaired(p);
      try {
        const session = parseSetupSession(await tokenApi(p, token).request(routePath('setup_server'), 'POST',
          routeBody('setup_server', {requestId: record.requestId, setupToken: token, name: record.name, authMode: 'hosted', interactive: true}), context.signal), p.pin.serverId);
        await assertPaired(p);
        if (!('claimRequired' in session) || session.requestId !== record.requestId) throw changed();
        own = {requestId: session.requestId, revision: session.revision};
        return {kind: 'initializeDone'};
      } catch (error) {
        if (refused(error, 'unauthorized')) return {kind: 'tokenRefused'};
        if (refused(error, 'setup_complete', 'not_setup_required')) return {kind: 'alreadyComplete'};
        throw error;
      }
    },
    async initializeLocal(name, credentials: SetupCredentials, context) {
      const p = await pair(context.signal);
      if (!issued || issued.serverId !== p.pin.serverId) throw changed();
      try {
        const session = parseSetupSession(await tokenApi(p, issued.setupToken).request(routePath('setup_server'), 'POST',
          routeBody('setup_server', {requestId: newRequestId(), setupToken: issued.setupToken, name, username: credentials.username.trim(), password: credentials.password,
            authMode: 'local', interactive: true}), context.signal), p.pin.serverId);
        await assertPaired(p);
        if ('claimRequired' in session) throw changed();
        pendingLocal = session;
        return {kind: 'initializeDone'};
      } catch (error) {
        if (refused(error, 'unauthorized')) return {kind: 'tokenRefused'};
        if (refused(error, 'setup_complete', 'not_setup_required')) return {kind: 'alreadyComplete'};
        throw error;
      }
    },
    requestApproval: (_navigate, context) => approval(context, false),
    async approveInApp() {throw new CodedError('unsupported', 'Approve on Portico Services.');},
    async abandon(context) {
      const p = await pair(context.signal), record = await ownRecord(p), {requestId, revision} = ownRevision();
      if (requestId !== record.requestId) throw changed();
      try {
        await tokenApi(p, record.setupToken).request(routePath('post_setup_abandon'), 'POST', routeBody('post_setup_abandon', {requestId, revision, setupToken: record.setupToken}), context.signal);
        await assertPaired(p);
        own = undefined;
        return {kind: 'abandoned'};
      } catch (error) {
        if (refused(error, 'setup_state_changed')) return {kind: 'abandonRefused'};
        throw error;
      }
    },
    async retryDelivery(context) {
      const p = await pair(context.signal), record = await ownRecord(p), {requestId, revision} = ownRevision();
      if (requestId !== record.requestId) throw changed();
      try {
        await tokenApi(p, record.setupToken).request(routePath('post_setup_retry'), 'POST', routeBody('post_setup_retry', {requestId, revision, setupToken: record.setupToken}), context.signal);
        await assertPaired(p);
        return {kind: 'deliveryRetried'};
      } catch (error) {
        if (refused(error, 'unauthorized')) return {kind: 'tokenRefused'};
        throw error;
      }
    },
    /** On the web the exchange needs the person's Return on Portico Services, which account entry consumes and admits.
     * Reaching it here means they came back without pressing Return: send them back to that page for it. */
    async exchange(context) {
      const p = await pair(context.signal), local = await records(p).read();
      await assertPaired(p);
      const link = local.attempt?.approvalUrl;
      if (!link) throw new CodedError('setup_return_required', 'Finish on Portico Services, then press Return.');
      deps.navigate(link);
      return leaving(context.signal);
    },
    renew: async context => {await approval(context, true); return {kind: 'renewDone'};},
    async adoptOwner(context) {
      const p = await pair(context.signal), value = pendingLocal;
      if (!value) throw changed();
      const session = parseLocalSession(value, {serverId: p.pin.serverId, authority: 'local'});
      if (session.viewer.role !== 'owner' || Date.parse(session.expiresAt) <= now()) throw changed();
      const sessions = deps.sessions(), environment = deps.environment(p.pin.serverId);
      let adopted = false;
      try {
        await deps.remember(p.api, session, {...environment, storage: sessions.connectionStorage(p.pin.serverId, {signIn: 'local', expectedRevision: p.revision})}, undefined,
          () => !life.signal.aborted && deps.installationId() === p.installationId);
        live();
        const selected = await sessions.selected();
        live();
        if (!selected || selected.signIn !== 'local' || !sameServerPin(selected.connection.pin, p.pin) || selected.connection.session.sessionFamilyId !== session.sessionFamilyId) throw changed();
        p.api.setAccessToken(session.accessToken);
        await ownerSnapshot(p.api, session, context.signal);
        live();
        owner = {api: p.api, session}; pendingLocal = undefined; adopted = true;
        return {kind: 'ownerAdopted'};
      } finally {
        if (!adopted) {
          await sessions.connectionStorage(p.pin.serverId).change(old => old?.session.sessionFamilyId === session.sessionFamilyId ? undefined : old).catch(() => {});
          await tokenApi(p, session.accessToken).request(routePath('delete_sessions_current'), 'DELETE', undefined, AbortSignal.timeout(5000)).catch(() => {});
        }
      }
    },
    async addLibrary(library: SetupLibrary, context) {
      const api = await ownerApi();
      const created = decodePostLibrariesResponse(await api.request(routePath('post_libraries'), 'POST', routeBody('post_libraries',
        {kind: library.kind, name: library.name, ...(library.folders?.length ? {folders: library.folders} : {path: library.path})}), context.signal));
      await ownerApi();
      await api.request(encodedRoutePath('post_libraries_id_scans', {id: encodeURIComponent(created.id)}), 'POST', {}, context.signal);
      await ownerApi();
      return {kind: 'libraryAdded'};
    },
    navigate: url => {live(); deps.navigate(url);},
  };

  /** S6 (or S7 when `renewal`): a fresh PKCE attempt in this tab, the one server call, the verified handoff bound to it. */
  async function approval(context: SetupPortContext, renewal: boolean) {
    const p = await pair(context.signal), record = await ownRecord(p), {requestId, revision} = ownRevision();
    if (requestId !== record.requestId || record.authMode !== 'hosted') throw changed();
    const store = records(p), before = (await store.read()).attempt;
    await assertPaired(p);
    const origin = deps.pageOrigin();
    if (p.api.baseUrl !== origin) throw changed();
    // A live attempt for this setup in this tab is asked again with the same start, so the server's stored pair still matches.
    const reuse = !renewal && before && before.requestId === requestId && Date.parse(before.expiresAt) > now() + 60_000 ? before : undefined;
    let attempt = reuse;
    if (!attempt) {
      const verifier = routeBase64(crypto.random(32)), created = now();
      attempt = Object.freeze({version: 2, serverId: p.pin.serverId, requestId, attemptId: routeBase64(crypto.random(32)), origin, hostedOrigin: deps.hostedOrigin,
        installationId: p.installationId, state: routeBase64(crypto.random(32)), verifier, codeChallenge: routeBase64(await crypto.sha256(new TextEncoder().encode(verifier))),
        createdAt: created, expiresAt: new Date(created + SETUP_CLAIM_LIFETIME_MS).toISOString()} satisfies SetupAttempt);
      if (!await store.change({attemptId: before?.attemptId}, {attempt})) throw changed();
    }
    const mine = attempt;
    const assertCurrent = async () => {
      await assertPaired(p);
      const local = await store.read();
      await assertPaired(p);
      if (local.record?.requestId !== requestId || local.attempt?.attemptId !== mine.attemptId) throw changed();
    };
    const {handoff, approvalUrl} = await requestSetupApproval(tokenApi(p, record.setupToken),
      {pin: p.pin, origin, hostedOrigin: deps.hostedOrigin, setupRequestId: requestId, setupRevision: revision, state: mine.state, codeChallenge: mine.codeChallenge, installationId: mine.installationId},
      {assertCurrent, bind: async (bound, link) => {
        const next: SetupAttempt = Object.freeze({...mine, handoff: bound, approvalUrl: link, expiresAt: new Date(Math.min(Date.parse(mine.expiresAt), Date.parse(bound.expiresAt))).toISOString()});
        if (!await store.change({attemptId: mine.attemptId}, {attempt: next})) throw changed();
      }}, {signal: context.signal, now, crypto, ...(renewal ? {renewal: true as const} : {})});
    return {kind: 'approvalReady' as const, approvalUrl, expiresAt: handoff.expiresAt};
  }

  /** The admitted owner's api, re-checked against the selected session before and after every owner call. */
  async function ownerApi(): Promise<HttpLocalApi> {
    live();
    const held = owner;
    if (!held) throw changed();
    const selected = await deps.sessions().selected();
    live();
    const route = held.api.getRouteConnection();
    if (owner !== held || !selected || !route || selected.connection.session.sessionFamilyId !== held.session.sessionFamilyId || !sameServerPin(selected.connection.pin, route.pin) ||
      selected.connection.session.viewer.role !== 'owner') throw changed();
    held.api.setAccessToken(selected.connection.session.accessToken);
    return held.api;
  }

  /** Only the admitted owner's folder listing, never setup-token IO. */
  const libraryFolders: LibraryContentApi = {request: async <T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> => {
    const route = routePath('get_admin_filesystem');
    if (method !== 'GET' || body !== undefined || path !== route && !path.startsWith(route + '?')) throw changed();
    const api = await ownerApi();
    const result = await api.request<T>(path, 'GET', undefined, AbortSignal.any([life.signal, ...(signal ? [signal] : [])]));
    await ownerApi();
    return result;
  }};

  return Object.freeze({ports: Object.freeze(ports), libraryFolders, dispose: () => {
    if (life.signal.aborted) return;
    life.abort();
    const p = paired;
    paired = undefined; owner = undefined; pendingLocal = undefined; issued = undefined; own = undefined;
    p?.dispose();
  }});
}

/** The page is leaving for Portico Services: settle only if this run is cancelled first. */
const leaving = (signal: AbortSignal) => new Promise<never>((_resolve, reject) => {
  if (signal.aborted) reject(signal.reason);
  else signal.addEventListener('abort', () => reject(signal.reason), {once: true});
});
