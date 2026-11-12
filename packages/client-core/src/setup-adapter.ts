import type {LibraryContentApi} from './library-content.ts';
import {linkAbortSignals} from './abort-lifetime.ts';
import {releaseThenRethrow} from './cleanup.ts';
import {ApiError, HttpLocalApi, type LocalSession} from './index.ts';
import type {AppSessionStore, PorticoAccountScope} from './app-sessions.ts';
import type {PorticoApproval} from '../../contracts/src/next/api.ts';
import {parseLocalSession} from './local-session.ts';
import {completeOwnerSetup, openOnlyProfile, parseDirectSignIn, parseDirectSnapshot, signInDevice} from './profile-management.ts';
import {callServer} from './query/server-api.ts';
import {cleanServerPath, typedServerPath} from './library-management.ts';
import {readDirectAccount, readServerSystem, revokeCurrentSession} from './query/connection-operations.ts';
import {parseServerSignIn} from './sign-in.ts';
import {parseSetupSession} from './setup-onboarding.ts';
import type {SetupState} from './setup-state.ts';
import type {ServerSignIn} from './sign-in.ts';
import {routeBase64, sameServerPin, webRouteCrypto, type RouteCrypto, type ServerPin} from './route-identity.ts';
import type {ConnectionEnvironment, rememberNativeSession} from './server-connections.ts';
import {CodedError, unreadableServerResponse} from './server-messages.ts';
import {setupApprovalAccount, setupReturnUrl, type SetupPageReturn} from './setup-portico.ts';
import {SETUP_CLAIM_LIFETIME_MS} from './setup-claim-attempt.ts';
import {requestSetupApproval} from './setup-approval-client.ts';
import {randomId} from './random-id.ts';
import type {SetupCredentials, SetupLibrary, SetupPortContext, SetupPorts, SetupRead} from './setup-controller.ts';
import type {SetupInput} from './setup-machine.ts';
import {isUnreadable, type SetupAttempt, type SetupLocal, type SetupRecord, type SetupStore} from './setup-store.ts';
import {serverNow} from './server-clock.ts';
import {encodedRoutePath, routeBody, routePath} from '../../contracts/src/routes.ts';
import {finishSetupExchange, type SetupExchangePorts} from './setup-exchange.ts';

/** The verified pairing of the server being set up: the one a page was served by, or the one an app proved at first contact. */
export type SetupPairing = Readonly<{server: ServerSignIn; api: HttpLocalApi; pin: ServerPin; dispose: () => void}>;
export type SetupAdapterDeps = Readonly<{
  /** The server route that issues a setup token: a browser page, or an app. */
  tokenRoute: 'start_browser_setup' | 'start_app_setup';
  /** Where a Portico Account setup finishes. `return-page`: the person's Return on Portico Services, which the web's
   * account entry consumes; reaching the exchange here sends them back for it. `here`: the app finishes from its bound
   * attempt once the server reads installed, and admits the owner itself. */
  finish: Readonly<{kind: 'return-page'}> | Readonly<{kind: 'here'; ports: SetupExchangePorts}>;
  hostedOrigin: string;
  pageOrigin: () => string;
  pair: (signal: AbortSignal) => Promise<SetupPairing>;
  sessions: () => Pick<AppSessionStore, 'read' | 'selected' | 'connectionStorage'>;
  environment: (serverId: string) => ConnectionEnvironment;
  /** The selected session for this server, restored on its own connection. */
  restore: (session: LocalSession) => Promise<{api: HttpLocalApi; session: LocalSession} | undefined>;
  remember: typeof rememberNativeSession;
  store: (serverId: string) => SetupStore;
  installationId: () => string | undefined;
  navigate: (url: string) => void;
  /** The app's own Portico Account sign-in approves this setup's claim, with no trip to Portico Services. Absent on the web. */
  approveInApp?: (input: Readonly<{attempt: SetupAttempt; approvalUrl: string; signal: AbortSignal; current: () => Promise<void>}>) => Promise<void>;
  /** The web: what this page's own address carries back from Portico Services (read once, then cleared from the
   * address). Absent in the apps, which ask the server how the approval stands instead. */
  pageReturn?: () => SetupPageReturn | undefined;
  /** The page that opened this setup is still the one showing. */
  current?: () => boolean;
  now?: () => number;
  crypto?: RouteCrypto;
  newRequestId?: () => string;
}>;
/** `signIn`: the paired server's kind of sign-in, from its system answer, so a reload at the library step still knows a local owner. */
export type SetupAdapter = Readonly<{ports: SetupPorts; libraryFolders: LibraryContentApi; serverName: () => string | undefined; signIn: () => 'local' | 'portico' | undefined; dispose: () => void}>;

type Paired = SetupPairing & {revision: number; installationId: string};
type Of<K extends SetupInput['kind']> = Extract<SetupInput, {kind: K}>;
type Own = {requestId: string; revision: string};
const changed = () => new CodedError('cancelled', 'The pending setup or selected server changed.');
/** A best-effort cleanup request, ended after five seconds; its failure is ignored. */
const bounded = async (send: (signal: AbortSignal) => Promise<unknown>) => {
  const linked = linkAbortSignals([], 5000);
  try {await send(linked.signal);} catch { /* best effort */ } finally {linked.dispose();}
};
const refused = (error: unknown, ...codes: string[]) => error instanceof ApiError && codes.includes(error.code);

/** The client's half of first-run setup for the controller (B-DESIGN §6), for web and native alike: pairing, the setup
 * routes under the setup token, the v2 records, the PKCE attempt and owner adoption. The server decides every phase; this only carries its answers.
 * Every await re-checks that the page, the installation, the selected-server revision and the pairing are still the
 * ones captured, so a late answer can never write into a successor. */
export function createSetupAdapter(deps: SetupAdapterDeps): SetupAdapter {
  const now = deps.now ?? (() => serverNow()), crypto = deps.crypto ?? webRouteCrypto, newRequestId = deps.newRequestId ?? randomId;
  const life = new AbortController();
  let paired: Paired | undefined, own: Own | undefined, issued: {serverId: string; setupToken: string} | undefined;
  let setupCode: string | undefined;
  let owner: {api: HttpLocalApi; session: LocalSession} | undefined, pendingLocal: LocalSession | undefined, exchanged: {api: HttpLocalApi; session: LocalSession} | undefined;
  /** Portico Accounts (rebuilt server), in memory only: the approval this page asked for, the one that came back, and
   * the Portico Account the owner's sign-in is filed under. A second tab or a stranger holds none of it. */
  let porticoApproval: PorticoApproval | undefined, returnedApproval: string | undefined, pendingPortico: PorticoAccountScope | undefined;
  /** How a Portico Account approval stands while setup still needs its owner: it came back to this page, the page came
   * back without it, or (when this page asked for one) Portico Services has it answered, not yet, or not at all. */
  async function porticoReading(p: Paired, signal: AbortSignal): Promise<Of<'approvalReturned' | 'approvalWaiting' | 'approvalNotGiven'> | undefined> {
    const back = deps.pageReturn?.();
    if (back?.approval) {returnedApproval = back.approval; return {kind: 'approvalReturned'};}
    if (back?.returned) {porticoApproval = undefined; return {kind: 'approvalNotGiven'};}
    const asked = porticoApproval;
    if (!asked) return undefined;
    try {
      const answer = await callServer(p.api, 'POST /v1/hosted/approvals/{id}/poll', {id: asked.approvalId, body: {pollToken: asked.pollToken}, signal});
      await assertPaired(p);
      if (porticoApproval !== asked) throw changed();
      if (answer?.state === 'approved' && typeof answer.approval === 'string' && answer.approval) {returnedApproval = answer.approval; return {kind: 'approvalReturned'};}
      return {kind: 'approvalWaiting', pollAfterMs: typeof answer?.pollAfterMs === 'number' ? answer.pollAfterMs : undefined};
    } catch (error) {
      if (refused(error, 'approval_declined')) {porticoApproval = undefined; return {kind: 'approvalNotGiven'};}
      throw error;
    }
  }

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
    // React Native has no AbortSignal.any/timeout: a linked signal, released after.
    const linked = linkAbortSignals([signal, life.signal]);
    const pairing = await deps.pair(linked.signal).finally(linked.dispose);
    try {
      live();
      const after = await deps.sessions().read();
      live();
      if (after.revision !== before.revision || installation() !== installationId) throw changed();
      paired = Object.freeze({...pairing, revision: after.revision, installationId}) as Paired;
      return paired;
    } catch (error) {return releaseThenRethrow(error, 'setup pairing', () => pairing.dispose());}
  }
  /** An api that carries this setup's token, over the verified route. */
  /** This run's own admission changed the selection: the pairing continues from the revision that write made, once the
   * selected sign-in for this server is the one just admitted. Anything else is a successor and ends this run. */
  const continueFromAdmission = async (p: Paired, family: string) => {
    live();
    const state = await deps.sessions().read();
    live();
    const entry = state.servers.find(value => value.connection.pin.serverId === state.selectedServerId);
    if (paired !== p || installation() !== p.installationId || !entry || !sameServerPin(entry.connection.pin, p.pin) || entry.connection.session.sessionFamilyId !== family) throw changed();
    paired = Object.freeze({...p, revision: state.revision}) as Paired;
  };
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
    const snapshot = parseDirectSnapshot(await readDirectAccount(api, signal),
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
        // The new server has one setup fact, told to anyone: whether it still needs its owner.
        const state: SetupState = Object.freeze({status: parseServerSignIn(await readServerSystem(p.api, context.signal)).setupRequired ? 'ready' : 'complete'});
        await assertPaired(p);
        if (state.status === 'ready') {
          const portico = await porticoReading(p, context.signal);
          if (portico) return {server: portico, local};
        }
        own = typeof state.setup === 'object' ? {requestId: state.setup.requestId, revision: state.setup.revision} : undefined;
        const ownerSessionHeld = state.status === 'complete' ? await heldOwner(p, context.signal) : undefined;
        return {server: {kind: 'state', state, ...(ownerSessionHeld !== undefined ? {ownerSessionHeld} : {})}, local};
      } catch (error) {
        if (record && refused(error, 'unauthorized')) return {server: {kind: 'tokenRefused'}, local};
        if (refused(error, 'setup_source_not_local')) return {server: {kind: 'notLocalSource'}, local};
        throw error;
      }
    },
    /** The server asks for its setup code with the owner's details, not before: the code is kept
     * here until then. The server wrote it to its log and to setup-code.txt in its data folder. */
    async start(context, code) {
      await pair(context.signal);
      // Every address, plain HTTP included, for both kinds of sign-in (decision O11): the setup code is the proof.
      if (!code) return {kind: 'setupCodeRequired'};
      setupCode = code;
      return {kind: 'startDone'};
    },
    /** Remote access is chosen in Settings after setup on the new server; there is nothing to check yet. */
    async remoteCheck() {return {kind: 'remoteOk'};},
    /** Portico Accounts (rebuilt server): the setup code and the server's name go to the server, which asks Portico
     * Services for an approval address. Nothing is saved on the server, or in this browser, until the approval comes back. */
    async initializeHosted(name, _remote, context) {
      const p = await pair(context.signal);
      if (!setupCode) throw changed();
      try {
        const approval = await callServer(p.api, 'POST /v1/setup/portico', {body: {setupCode, serverName: name.trim() || undefined, returnUrl: setupReturnUrl(deps.pageOrigin())}, signal: context.signal});
        await assertPaired(p);
        if (!approval?.approvalUrl || !/^https:\/\//.test(approval.approvalUrl) || !approval.approvalId || !approval.pollToken) throw new CodedError('invalid_response', unreadableServerResponse);
        porticoApproval = approval; returnedApproval = undefined;
        return {kind: 'initializeDone'};
      } catch (error) {
        if (refused(error, 'setup_code_invalid')) return {kind: 'setupCodeInvalid'};
        if (refused(error, 'setup_complete', 'not_setup_required')) return {kind: 'alreadyComplete'};
        throw error;
      }
    },
    /** Creates the owner (`POST /v1/setup/owner`) and opens the owner's main profile, then saves the server's name the owner
     * typed as that owner (`PUT /v1/admin/settings/general`): the setup route takes no name. A name the server refuses leaves
     * "Portico", which the owner can change in Settings › General. Remote access is set in Settings. */
    async initializeLocal(name, credentials: SetupCredentials, _remote, context) {
      const p = await pair(context.signal);
      if (!setupCode) throw changed();
      try {
        const grant = await completeOwnerSetup(p.api, {setupCode, username: credentials.username.trim(), password: credentials.password}, context.signal);
        await assertPaired(p);
        if (grant.kind !== 'signed-in' || grant.snapshot.serverId !== p.pin.serverId) throw changed();
        pendingLocal = grant.session;
        if (name?.trim()) await callServer(tokenApi(p, grant.session.accessToken), 'PUT /v1/admin/settings/{name}', {id: 'general', body: {name: name.trim()}, signal: context.signal}).catch(() => {});
        return {kind: 'initializeDone'};
      } catch (error) {
        if (refused(error, 'setup_code_invalid')) return {kind: 'setupCodeInvalid'};
        if (refused(error, 'setup_complete', 'not_setup_required')) return {kind: 'alreadyComplete'};
        throw error;
      }
    },
    /** The approval address the server asked for: the reducer opens it (the web leaves for it) and then asks how it stands. */
    async requestApproval(_navigate, context) {
      await pair(context.signal);
      if (!porticoApproval) throw changed();
      return {kind: 'approvalReady', approvalUrl: porticoApproval.approvalUrl, expiresAt: porticoApproval.expiresAt};
    },
    async approveInApp(context) {
      if (!deps.approveInApp) throw new CodedError('unsupported', 'Approve on Portico Services.');
      const p = await pair(context.signal), store = records(p), local = await store.read();
      await assertPaired(p);
      const attempt = local.attempt;
      if (!attempt || isUnreadable(attempt) || !attempt.handoff || !attempt.approvalUrl || local.record?.requestId !== attempt.requestId) throw changed();
      // Only while this attempt is still the one bound to this setup.
      const current = async () => {
        await assertPaired(p);
        const now = await store.read();
        await assertPaired(p);
        if (now.attempt?.attemptId !== attempt.attemptId || now.record?.requestId !== attempt.requestId) throw changed();
      };
      await deps.approveInApp({attempt, approvalUrl: attempt.approvalUrl, signal: context.signal, current});
      await current();
      return {kind: 'approvedInApp'};
    },
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
    async startOver(context, code) {
      const p = await pair(context.signal);
      // From outside, the setup token a setup code issued carries its admission: started over with it, nobody is asked for a new code.
      const stored = issued?.serverId === p.pin.serverId ? undefined : (await records(p).read()).record;
      const held = issued?.serverId === p.pin.serverId ? issued.setupToken : stored?.serverId === p.pin.serverId ? stored.setupToken : undefined;
      await assertPaired(p);
      try {
        const raw = await (held ? tokenApi(p, held) : p.api).request<{serverId?: unknown; setupToken?: unknown}>(routePath('post_setup_start_over'), 'POST', code ? {setupCode: code} : {}, context.signal);
        await assertPaired(p);
        if (raw?.serverId !== p.pin.serverId || typeof raw.setupToken !== 'string' || !raw.setupToken) throw Object.assign(new Error('The setup answer did not match this server.'), {code: 'identity_mismatch'});
        issued = {serverId: raw.serverId, setupToken: raw.setupToken};
        own = undefined;
        return {kind: 'startedOver'};
      } catch (error) {
        if (refused(error, 'setup_code_required')) return {kind: 'setupCodeRequired'};
        if (refused(error, 'setup_code_invalid')) return {kind: 'setupCodeInvalid'};
        if (refused(error, 'setup_source_not_local')) return {kind: 'notLocalSource'};
        if (refused(error, 'setup_already_complete', 'setup_complete', 'not_setup_required', 'conflict')) return {kind: 'alreadyComplete'};
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
    /** The approval that came back goes to the server once (`POST /v1/setup/portico/finish`), which checks it and makes the
     * approving Portico Account the owner. It is forgotten here whatever the answer: an approval is used once. */
    async exchange(context) {
      const p = await pair(context.signal), approval = returnedApproval;
      returnedApproval = undefined; porticoApproval = undefined;
      if (!approval) throw changed();
      const accountId = setupApprovalAccount(approval, p.pin.serverId);
      if (!accountId) return {kind: 'exchangeRefused', lapsed: false, error: {code: 'approval_declined', retryable: false}};
      try {
        const grant = await openOnlyProfile(p.api, parseDirectSignIn(await callServer(p.api, 'POST /v1/setup/portico/finish', {body: {approval, device: signInDevice()}, signal: context.signal})), context.signal);
        await assertPaired(p);
        if (grant.kind !== 'signed-in' || grant.snapshot.serverId !== p.pin.serverId) throw changed();
        pendingLocal = grant.session; pendingPortico = Object.freeze({origin: deps.hostedOrigin, accountId});
        return {kind: 'exchangeDone'};
      } catch (error) {
        if (refused(error, 'approval_declined')) return {kind: 'exchangeRefused', lapsed: false, error: {code: 'approval_declined', retryable: false}};
        if (refused(error, 'setup_complete', 'not_setup_required')) return {kind: 'alreadyComplete'};
        throw error;
      }
    },
    renew: async context => {await approval(context, true); return {kind: 'renewDone'};},
    async adoptOwner(context) {
      const p = await pair(context.signal);
      if (exchanged) {
        // Admitted by the exchange: hold it as the owner once the selected session is still that sign-in.
        const held = exchanged, selected = await deps.sessions().selected();
        live();
        if (!selected || selected.signIn !== 'portico' || !sameServerPin(selected.connection.pin, p.pin) || selected.connection.session.sessionFamilyId !== held.session.sessionFamilyId) throw changed();
        await ownerSnapshot(held.api, held.session, context.signal);
        live();
        owner = held; exchanged = undefined;
        return {kind: 'ownerAdopted'};
      }
      const value = pendingLocal, portico = pendingPortico;
      if (!value) throw changed();
      // A server set up with a Portico Account keeps the owner's sign-in under that account, as the apps file one opened from
      // the account's list: the server's own session, labelled with the account (no route key).
      const signIn = portico ? 'portico' as const : 'local' as const;
      const session = parseLocalSession(value, {serverId: p.pin.serverId, authority: 'local'});
      if (session.viewer.role !== 'owner' || Date.parse(session.expiresAt) <= now()) throw changed();
      const sessions = deps.sessions(), environment = deps.environment(p.pin.serverId);
      let adopted = false;
      try {
        await deps.remember(p.api, session, {...environment, storage: sessions.connectionStorage(p.pin.serverId, {signIn, ...(portico ? {hostedAccount: portico} : {}), expectedRevision: p.revision})}, portico,
          () => !life.signal.aborted && deps.installationId() === p.installationId);
        live();
        const selected = await sessions.selected();
        live();
        if (!selected || selected.signIn !== signIn || !sameServerPin(selected.connection.pin, p.pin) || selected.connection.session.sessionFamilyId !== session.sessionFamilyId) throw changed();
        await continueFromAdmission(p, session.sessionFamilyId);
        p.api.setAccessToken(session.accessToken);
        await ownerSnapshot(p.api, session, context.signal);
        live();
        owner = {api: p.api, session}; pendingLocal = undefined; pendingPortico = undefined; adopted = true;
        return {kind: 'ownerAdopted'};
      } finally {
        if (!adopted) {
          await sessions.connectionStorage(p.pin.serverId).change(old => old?.session.sessionFamilyId === session.sessionFamilyId ? undefined : old).catch(() => {});
          await bounded(signal => revokeCurrentSession(tokenApi(p, session.accessToken), signal));
        }
      }
    },
    /** Creates the first library (`POST /v1/admin/libraries`); the server starts its scan. */
    async addLibrary(library: SetupLibrary, context) {
      const api = await ownerApi();
      const kinds: Readonly<Record<string, string>> = {movie: 'movies', tv: 'shows', anime: 'anime', music: 'music', audiobook: 'audiobooks', book: 'books', comic: 'comics'};
      const created = await callServer(api, 'POST /v1/admin/libraries', {body: {kind: kinds[library.kind] ?? library.kind, name: library.name,
        folders: (library.folders?.length ? [...library.folders] : [typedServerPath(library.path)]).map(cleanServerPath)}, signal: context.signal});
      if (!created?.id) throw new CodedError('invalid_response', unreadableServerResponse);
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
    const linked = linkAbortSignals([life.signal, signal]);
    const result = await api.request<T>(path, 'GET', undefined, linked.signal).finally(linked.dispose);
    await ownerApi();
    return result;
  }};

  return Object.freeze({ports: Object.freeze(ports), libraryFolders, serverName: () => paired?.server.name, signIn: () => paired?.server.signIn === 'local' ? 'local' : paired?.server.signIn === 'portico' ? 'portico' : undefined, dispose: () => {
    if (life.signal.aborted) return;
    life.abort();
    const p = paired;
    const held = owner ?? exchanged;
    paired = undefined; owner = undefined; pendingLocal = undefined; exchanged = undefined; issued = undefined; own = undefined;
    if (held && held.api !== p?.api) held.api.getRouteConnection()?.dispose();
    p?.dispose();
  }});
}

/** The page is leaving for Portico Services: settle only if this run is cancelled first. */
const leaving = (signal: AbortSignal) => new Promise<never>((_resolve, reject) => {
  if (signal.aborted) reject(signal.reason);
  else signal.addEventListener('abort', () => reject(signal.reason), {once: true});
});
