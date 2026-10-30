import {linkNativeAbortSignals} from './abort-signals';
import {accountExpectation, nativeAccountWork} from './account-work';
import {HttpLocalApi, transportJSON, assertFirstContactIdentity, type HostedServer, type HostedSession, type LocalSession} from '../../../../packages/client-core/src';
import {ServerSignInProbe, parseServerSignIn, type ServerSignIn} from '../../../../packages/client-core/src/sign-in';
import {negotiateNativeServer} from './negotiation';
import {boundedPick, type PickTrace} from './pick-deadline';
import {logPickFailure, provenIdentityMismatch} from './pick-failure';
import {directSignIn, directSignInContinuation, completeDirectSignInChallenge, selectDirectProfile, parseDirectSnapshot, type DirectSignIn, type DirectSnapshot} from '../../../../packages/client-core/src/profile-management';
import {connectHostedServer, connectPairedServer, connectPairedPorticoServer, inspectDirectServer} from '../../../../packages/client-core/src/server-connections';
import {knownServerRoutes, type NewServerConnection, type AppServerSession} from '../../../../packages/client-core/src/app-sessions';
import {porticoSignIn, acceptPorticoInvitation, acceptShortPorticoInvitation} from '../../../../packages/client-core/src/portico-servers';
import {routePath} from '../../../../packages/contracts/src/routes';
import {REVOCATION_TIMEOUT_MS, revokeInBackground} from './session-revocation';
import {createNativeLocalTVSignIn} from './tv-sign-in';
import type {ServerPin} from '../../../../packages/client-core/src/route-identity';
import {installationIdentity} from '../../../../packages/client-core/src/installation';
import {nativeAccounts, type NativeAccounts} from './accounts';
import {hostedUrl} from './account-authority';
import {nativeNetworkFetch, nativeServerEnvironment, nativeHostedSource, nativeRouteCrypto} from './server-connections';
import {rememberNativeServerDisplay} from './server-display';
import {nativeAppSessions, subscribeNativeAppSessions} from './app-session-storage';
import {NativeMemberAccountMove} from './member-account-move';
import {NativeLocalInvitation} from './local-invitation';
import {NativePorticoInvitation, NativeInvitationUnavailable, readPorticoInvitationPreview} from './portico-invitation';
import {nativeCertificatePlatform} from './server-connections';
import {pendingCertificate, confirmIdentityCode, clearPendingCertificate, type PendingCertificate} from './certificate-trust';
import {routeOrigin} from '../../../../packages/client-core/src/route-identity';
import {NativeProfileRecovery} from './profile-recovery';
import {hostedSignInUnavailable} from './hosted-sign-in-unavailable';
import {startAppSetup, initializeServer} from '../../../../packages/client-core/src/setup-onboarding';
import {rememberNativeSignInHint} from './sign-in-ended';
import {createNativeOwnerRecoveryContact} from './owner-recovery-contact';
import {requestOwnerResetFile, verifyOwnerResetFile} from '../../../../packages/client-core/src/account-reset';
import {createNativeSetupClaim} from './setup-claim';
import {nativeSetupDraft} from './setup-draft';
import type {HostedRequestApi} from '../../../../packages/client-core/src/portico-servers';

const cancelled = () => Object.assign(new Error('native_sign_in_cancelled'), {name: 'AbortError'});
const mismatch = () => Object.assign(new Error('native_sign_in_authority_changed'), {code: 'identity_mismatch'});
const captureAccount = (session: Omit<HostedSession, 'refreshToken'>) => Object.freeze({...session, account: Object.freeze({...session.account})});
// Preserve exactly the JSON wire value before canonical admission can await storage.
const captureCommandBody = (body: unknown): unknown => body === undefined ? undefined : JSON.parse(transportJSON(body));

/** Current product sign-in commands. UI frames are separate; no retired screen is imported.
 * Every path uses the canonical probe, real verified routes, account/profile API and secure store. */
export class NativeServerSignIn {
  static ownerRecoveryContact = createNativeOwnerRecoveryContact;
  static parseInvitationEntry = NativeLocalInvitation.parseEntry;
  static parseShortInvitationEntry = NativeLocalInvitation.parseShortEntry;
  static formatInvitationCode = NativeLocalInvitation.formatCode;
  static invitationUnavailable(error: unknown) {return error instanceof NativeInvitationUnavailable;}
  readonly probe = new ServerSignInProbe(nativeNetworkFetch);
  private readonly controller = new AbortController();
  private readonly installation = installationIdentity();
  private api?: HttpLocalApi;
  private server?: ServerSignIn;
  private pendingIdentity?: PendingCertificate;
  private admission?: NewServerConnection;
  private hosted?: AppServerSession['connection']['hosted'];
  private snapshot?: DirectSnapshot;
  private account?: LocalSession;
  private challenge?: string;
  private busy = false;
  private running?: Promise<unknown>;
  private readonly unfence: () => void;
  private readonly owner: NativeAccounts;
  private complete = false;
  private publishing = false;
  private disposeRequested = false;
  private readonly issued = new Map<string, LocalSession>();
  private readonly setupIssuers = new Map<string, {origin: string; routes: NonNullable<ReturnType<HttpLocalApi['getRouteConnection']>>; pin: ServerPin}>();
  private readonly requiredSetupHandoff: boolean;
  private readonly expectedInvitation?: Readonly<{origin: string; serverId?: string; fingerprint?: string; signIn?: 'local' | 'portico'; expiresAt?: string}>;
  private readonly expectedTarget?: Readonly<{serverId: string; fingerprint: string}>;
  private ownerResetReady = false;
  private ownerResetGrant?: Extract<DirectSignIn, {kind: 'signed-in'}>;
  private hostedSetup?: ReturnType<typeof createNativeSetupClaim>;
  private hostedLease?: () => boolean;
  private setupFinishLife?: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>;
  private setupFinishCurrent() {
    const life = this.setupFinishLife;
    try {return !life || !life.signal.aborted && life.isCurrent() === true;} catch {return false;}
  }
  private directoryFactor?: Readonly<{api: HttpLocalApi; token: string; expiresAt: string; assert: () => void; retire: (fresh: LocalSession) => Promise<void>}>;
  private memberMove?: {dispose: () => void};
  private porticoInvitation?: NativePorticoInvitation<Awaited<ReturnType<NativeServerSignIn['signInLocal']>>>;
  private invitationBinding?: () => boolean;
  private localInvitation?: NativeLocalInvitation<Awaited<ReturnType<NativeServerSignIn['signInLocal']>>>;
  private memberMoveFactor?: Readonly<{api: HttpLocalApi; token: string; expiresAt: string; isCurrent: () => boolean; assertCurrent: () => Promise<void>; retire: (fresh: LocalSession) => Promise<void>}>;
  constructor(owner: NativeAccounts = nativeAccounts, options: {expectedInvitation?: Readonly<{origin: string; serverId?: string; fingerprint?: string; signIn?: 'local' | 'portico'; expiresAt?: string}>; requiredSetupHandoff?: boolean; expectedTarget?: Readonly<{serverId: string; fingerprint: string}>} = {}) {
    this.expectedInvitation = options.expectedInvitation ? Object.freeze({...options.expectedInvitation}) : undefined;
    this.expectedTarget = options.expectedTarget ? Object.freeze({...options.expectedTarget}) : undefined;
    this.owner = owner; this.requiredSetupHandoff = options.requiredSetupHandoff === true;
    this.unfence = owner.onCancel(() => this.cancel());
  }
  private current = (): boolean => !this.controller.signal.aborted && this.setupFinishCurrent() && installationIdentity() === this.installation && (!this.hostedLease || this.hostedLease()) && (!this.localInvitation || this.localInvitation.current()) && (!this.invitationBinding || this.invitationBinding()) && (!this.porticoInvitation || this.porticoInvitation.current());
  private assertCurrent = () => {if (!this.current()) throw cancelled(); if (!this.installation) throw mismatch();};
  /** Read-only lifetime fence for the original phone directory contact; never admission evidence. */
  ownerRecoveryCurrent() {return this.current() && !this.complete;}
  serverDescriptor() {this.assertCurrent(); if (!this.server) throw mismatch(); return this.server;}
  /** Public hand-offs use the connected, identity-verified server origin. The
   * address being typed or an unconfirmed discovery/proof is never a source. */
  serverOrigin(): string {
    this.assertCurrent();
    if (!this.api || !this.server) throw mismatch();
    return routeOrigin(this.api.baseUrl);
  }
  assertSignInTarget(serverId: string, fingerprint: string) {
    this.assertCurrent(); const pin = this.api?.getRouteConnection()?.pin;
    if (!pin || this.server?.id !== serverId || pin.serverId !== serverId || pin.fingerprint !== fingerprint) throw mismatch();
  }
  needsIdentityCode() {this.assertCurrent(); return !!this.pendingIdentity && pendingCertificate(this.pendingIdentity.origin) === this.pendingIdentity;}
  confirmIdentityCode(code: string) {return this.run(async () => {
    if (!this.pendingIdentity) throw mismatch();
    const accepted = await confirmIdentityCode(this.pendingIdentity.origin, code, nativeCertificatePlatform, this.controller.signal, this.pendingIdentity);
    this.assertCurrent(); return accepted;
  });}
  private async run<T>(work: () => Promise<T>): Promise<T> {
    this.assertCurrent(); if (this.busy || this.complete) throw cancelled(); this.busy = true;
    const pending = this.running = work();
    try {return await pending;} finally {this.busy = false; if (this.running === pending) this.running = undefined; if (this.disposeRequested) this.cancel();}
  }
  /** The returned kind drives the credential destination. Local pairing is explicit afterward. */
  inspect(address: string) {
    return this.run(async () => {
      if (this.expectedInvitation && routeOrigin(address) !== this.expectedInvitation.origin) throw mismatch();
      const server = await this.probe.inspect(address); this.assertCurrent();
      if (!server && (this.probe.getSnapshot().error as {code?: string})?.code === 'identity_code_needed') {
        const captured = pendingCertificate(routeOrigin(address));
        if (!captured) throw mismatch();
        // Before the code matches, the proof is the only permitted exchange. No
        // system response, sign-in kind or unconfirmed name exists at this stage.
        this.pendingIdentity = captured; return undefined;
      }
      if (!server) throw this.probe.getSnapshot().error ?? mismatch();
      // Before any credential: a server this app can't talk to gets its named page (O-159).
      negotiateNativeServer(server);
      if (this.expectedTarget && (server.id !== this.expectedTarget.serverId || server.signIn !== 'local' || server.setupRequired)) throw mismatch();
      if (this.expectedInvitation && (server.setupRequired || this.expectedInvitation.signIn && server.signIn !== this.expectedInvitation.signIn || this.expectedInvitation.serverId && server.id !== this.expectedInvitation.serverId)) throw mismatch();
      this.server = server; return server;
    });
  }
  /** Setup is admitted only after the same explicit first-contact proof as sign-in. */
  prepareSetup() {
    return this.run(async () => {
      const server = this.server, origin = this.probe.getSnapshot().origin;
      if (!server?.setupRequired || !origin || this.api) throw mismatch();
      const pin = await inspectDirectServer(origin, nativeServerEnvironment(server.id), this.controller.signal); this.assertCurrent();
      if (pin.serverId !== server.id || this.expectedTarget && (pin.serverId !== this.expectedTarget.serverId || pin.fingerprint !== this.expectedTarget.fingerprint)) throw mismatch();
      assertFirstContactIdentity(origin, pin, {knownRoutes: knownServerRoutes(await nativeAppSessions.read())}); this.assertCurrent();
      this.admission = await this.owner.admission('local'); this.assertCurrent();
      const connected = await connectPairedServer(origin, pin, nativeServerEnvironment(server.id), this.controller.signal);
      this.api = connected.api; this.assertCurrent();
      const checked = await rememberNativeServerDisplay(connected.api, {serverId: server.id, signIn: server.signIn,
        signal: this.controller.signal, isCurrent: this.current, setup: true}); this.assertCurrent();
      if (!checked.setupRequired) throw mismatch();
      this.server = checked; return checked;
    });
  }
  setupLocal(name: string, username: string, password: string) {
    return this.run(async () => {
      const api = this.api, server = this.server;
      if (!api || !server?.setupRequired || !this.admission) throw mismatch();
      const store = nativeSetupDraft(server.id);
      const saved = await store.read(); this.assertCurrent();
      if (saved && saved.authMode !== 'local') throw mismatch();
      const token = saved?.setupToken ?? (await startAppSetup(api, this.controller.signal)).setupToken; this.assertCurrent();
      const retained = await nativeAppSessions.read(); this.assertCurrent();
      const issued = await initializeServer(api, store, {setupToken: token, name: name.trim(), username: username.trim(), password,
        authMode: 'local', interactive: true}, this.controller.signal);
      // Pending Hosted claims are never sessions. Retain a fresh local grant for
      // cancellation cleanup before any asynchronous snapshot or publication.
      if ('claimRequired' in issued) throw mismatch();
      const pin = api.getRouteConnection()?.pin;
      if (!pin || issued.viewer.authority !== 'local' || issued.viewer.role !== 'owner' || issued.viewer.serverId !== server.id || Date.parse(issued.expiresAt) <= Date.now()
        || issued.installationId !== this.installation?.installationId
        || issued.serverIdentity?.publicKey !== pin.publicKey || issued.serverIdentity.fingerprint !== pin.fingerprint
        || retained.servers.some(value => value.connection.session.sessionFamilyId === issued.sessionFamilyId)
        || this.account?.sessionFamilyId === issued.sessionFamilyId) throw mismatch();
      const nowStored = await nativeAppSessions.read();
      if (nowStored.servers.some(value => value.connection.session.sessionFamilyId === issued.sessionFamilyId)) throw mismatch();
      const routes = api.getRouteConnection();
      if (!routes || routes.pin.serverId !== pin.serverId || routes.pin.publicKey !== pin.publicKey || routes.pin.fingerprint !== pin.fingerprint) throw mismatch();
      this.setupIssuers.set(issued.sessionFamilyId, {origin: api.baseUrl, routes, pin: {...pin}});
      this.issued.set(issued.sessionFamilyId, issued);
      try {
        const completed = await store.read();
        this.assertCurrent(); api.setAccessToken(issued.accessToken);
        const snapshot = parseDirectSnapshot(await api.request(routePath('get_direct_account'), 'GET', undefined, this.controller.signal),
          {authority: 'local', serverId: server.id, accountId: issued.viewer.accountId}, true);
        this.assertCurrent();
        if (snapshot.account.role !== 'owner' || snapshot.account.disabled || !snapshot.capabilities.manageServer
          || snapshot.account.primaryProfileId !== issued.viewer.profileId) throw mismatch();
        this.server = {...server, name: name.trim(), signIn: 'local', setupRequired: false};
        const result = await this.consume({kind: 'signed-in', session: issued, snapshot,
          accountScoped: false, passwordChangeRequired: false});
        await store.change(old => {
          this.assertCurrent();
          if (!completed || old?.requestId !== completed.requestId || old.setupToken !== completed.setupToken || old.authMode !== 'local') throw mismatch();
          return undefined;
        });
        return result;
      } catch (error) {try {await this.retire(issued);} catch { /* the original error wins */ } throw error;}
    });
  }
  prepareHostedSetup(name: string) {
    return this.run(async () => {
      const api = this.api, server = this.server;
      if (!api || !server?.setupRequired) throw mismatch();
      const origin = new URL(api.baseUrl);
      if (origin.origin !== api.baseUrl || (origin.protocol !== 'https:' && !(origin.protocol === 'http:' && origin.hostname === 'localhost'))) throw mismatch();
      const claim = this.hostedSetup ??= createNativeSetupClaim(api.baseUrl, server.id);
      const existing = await claim.resumePrepared(); this.assertCurrent();
      if (existing) return existing.url;
      const store = nativeSetupDraft(server.id), saved = await store.read(); this.assertCurrent();
      if (saved && saved.authMode !== 'hosted') throw mismatch();
      const token = saved?.setupToken ?? (await startAppSetup(api, this.controller.signal)).setupToken; this.assertCurrent();
      const pending = await initializeServer(api, store, {setupToken: token, name: name.trim(), authMode: 'hosted', interactive: true}, this.controller.signal);
      this.assertCurrent(); if (!('claimRequired' in pending)) throw mismatch();
      const url = await claim.prepare(api, hostedUrl); this.assertCurrent(); return url;
    });
  }
  reviewHostedSetup(url: string, opening: {signal: AbortSignal; current: () => boolean; canApprove: () => boolean}) {
    return this.run(async () => {
      if (!this.hostedSetup) throw mismatch();
      const review = await this.hostedSetup.accountApproval(url, opening); this.assertCurrent(); return review;
    });
  }
  restartEndedSetup() {
    return this.run(async () => {
      const claim = this.hostedSetup, api = this.api, server = this.server;
      if (!claim || !api || !server?.setupRequired) throw mismatch();
      await claim.abandonEnded(); this.assertCurrent();
      const checked = await rememberNativeServerDisplay(api, {serverId: server.id, signIn: server.signIn, signal: this.controller.signal, isCurrent: this.current, setup: true});
      this.assertCurrent(); if (!checked.setupRequired) throw mismatch();
      this.hostedSetup = undefined; this.server = checked;
    });
  }
  finishHostedSetup(lifetime?: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>) {
    return this.run(async () => {
      const retire = () => this.dispose();
      this.setupFinishLife = lifetime;
      lifetime?.signal.addEventListener('abort', retire, {once: true});
      try {
      this.assertCurrent();
      const claim = this.hostedSetup;
      if (!claim || !this.server) throw mismatch();
      const completed = await claim.finishInstalled();
      if (!completed) {this.assertCurrent(); return undefined;}
      try {
      const {api, result, admission, hosted, draft} = completed, routes = api.getRouteConnection();
      this.assertCurrent();
      const retained = await nativeAppSessions.read(); this.assertCurrent();
      if (!routes || retained.servers.some(value => value.connection.session.sessionFamilyId === result.session.sessionFamilyId)) throw mismatch();
      this.api = api; this.admission = admission; this.hosted = hosted;
      this.setupIssuers.set(result.session.sessionFamilyId, {origin: api.baseUrl, routes, pin: {...routes.pin}});
      this.issued.set(result.session.sessionFamilyId, result.session); completed.accept();
      try {
        this.assertCurrent(); this.server = {...this.server, signIn: 'portico', setupRequired: false};
        const accepted = await this.consume(result);
        await nativeSetupDraft(result.snapshot.serverId).change(old => {
          // An accepted publication can itself unmount this form. Its cleanup
          // spends only the original setup draft, never successor authority.
          if (!this.complete) this.assertCurrent();
          if (!old || old.requestId !== draft.requestId || old.setupToken !== draft.setupToken || old.authMode !== 'hosted') throw mismatch();
          return undefined;
        });
        return accepted;
      } catch (error) {try {await this.retire(result.session);} catch { /* the original error wins */ } throw error;}
      } catch (error) {await completed.release(); throw error;}
      } finally {
        lifetime?.signal.removeEventListener('abort', retire);
        if (this.setupFinishLife === lifetime) this.setupFinishLife = undefined;
      }
    });
  }
  prepareOwnerReset() {
    return this.run(async () => {
      const server = this.server, origin = this.probe.getSnapshot().origin;
      if (!server || server.setupRequired || server.signIn !== 'portico' || !origin || this.api) throw mismatch();
      const pin = await inspectDirectServer(origin, nativeServerEnvironment(server.id), this.controller.signal); this.assertCurrent();
      if (pin.serverId !== server.id || this.expectedTarget && (pin.serverId !== this.expectedTarget.serverId || pin.fingerprint !== this.expectedTarget.fingerprint)) throw mismatch();
      assertFirstContactIdentity(origin, pin, {knownRoutes: knownServerRoutes(await nativeAppSessions.read())}); this.assertCurrent();
      const captured = await this.owner.admission('local'); this.assertCurrent();
      const connected = await connectPairedServer(origin, pin, nativeServerEnvironment(server.id), this.controller.signal);
      this.api = connected.api; this.assertCurrent();
      this.server = await rememberNativeServerDisplay(connected.api, {serverId: server.id, signIn: 'portico', signal: this.controller.signal, isCurrent: this.current});
      this.assertCurrent(); if (this.server.setupRequired) throw mismatch();
      this.admission = {signIn: 'portico', resetFileOwner: true, expectedRevision: captured.expectedRevision};
      this.ownerResetReady = true;
    });
  }
  requestOwnerReset() {
    return this.run(async () => {
      if (!this.ownerResetReady || !this.api || this.ownerResetGrant) throw mismatch();
      const location = await requestOwnerResetFile(this.api, undefined, this.controller.signal); this.assertCurrent(); return location;
    });
  }
  verifyOwnerReset(code: string) {
    return this.run(async () => {
      const api = this.api, server = this.server, routes = api?.getRouteConnection();
      if (!this.ownerResetReady || !api || !server || !routes || this.ownerResetGrant) throw mismatch();
      const original = {origin: api.baseUrl, routes, pin: {...routes.pin}};
      const before = await nativeAppSessions.read(); this.assertCurrent();
      const result = await verifyOwnerResetFile(api, code, false, this.controller.signal);
      if (result.kind !== 'signed-in' || !result.resetFileOwnerSession || result.passwordChangeRequired || result.accountScoped
        || result.session.refreshToken || result.session.viewer.serverId !== server.id || result.session.viewer.role !== 'owner'
        || result.session.installationId !== this.installation?.installationId || Date.parse(result.session.expiresAt) <= Date.now()
        || result.session.serverIdentity?.publicKey !== original.pin.publicKey || result.session.serverIdentity.fingerprint !== original.pin.fingerprint
        || result.snapshot.account.role !== 'owner' || result.snapshot.account.disabled || !result.snapshot.capabilities.manageServer
        || result.snapshot.account.primaryProfileId !== result.session.viewer.profileId
        || before.servers.some(value => value.connection.session.sessionFamilyId === result.session.sessionFamilyId)) throw mismatch();
      let handed = false;
      try {
        const current = await nativeAppSessions.read();
        if (current.servers.some(value => value.connection.session.sessionFamilyId === result.session.sessionFamilyId)) throw mismatch();
        this.setupIssuers.set(result.session.sessionFamilyId, original); this.issued.set(result.session.sessionFamilyId, result.session); handed = true;
        this.assertCurrent(); this.ownerResetGrant = result;
      } catch (error) {
        if (handed) {try {await this.retire(result.session);} catch { /* the original error wins */ }}
        else {
          // Cleanup belongs to this original issuer only. A concurrent retained
          // family is never revoked, even when this opening was cancelled.
          const current = await nativeAppSessions.read();
          if (!current.servers.some(value => value.connection.session.sessionFamilyId === result.session.sessionFamilyId)) {
            const cleanup = new HttpLocalApi(original.origin, result.session.accessToken, nativeNetworkFetch); cleanup.useRoutes(original.routes); await cleanup.logout().catch(() => { /* the original error wins */ });
          }
        }
        throw error;
      }
    });
  }
  acceptOwnerReset() {
    return this.run(async () => {
      const grant = this.ownerResetGrant;
      if (!grant || !this.ownerResetReady) throw mismatch();
      this.ownerResetGrant = undefined; return this.consume(grant);
    });
  }
  inspectLocalIdentity(): Promise<ServerPin> {
    return this.run(async () => {
      const server = this.server, origin = this.probe.getSnapshot().origin;
      if (!server || !origin || server.signIn !== 'local') throw Object.assign(new Error('sign_in_kind_portico'), {code: 'sign_in_kind_portico'});
      const pin = await inspectDirectServer(origin, nativeServerEnvironment(server.id), this.controller.signal); this.assertCurrent();
      if (pin.serverId !== server.id || this.expectedTarget && (pin.serverId !== this.expectedTarget.serverId || pin.fingerprint !== this.expectedTarget.fingerprint)) throw mismatch();
      assertFirstContactIdentity(origin, pin, {knownRoutes: knownServerRoutes(await nativeAppSessions.read())}); this.assertCurrent();
      return pin;
    });
  }
  confirmLocal(pin: ServerPin) {
    return this.run(async () => {
      const server = this.server, origin = this.probe.getSnapshot().origin;
      if (!server || !origin || server.signIn !== 'local' || pin.serverId !== server.id || this.api
        || this.expectedTarget && (pin.serverId !== this.expectedTarget.serverId || pin.fingerprint !== this.expectedTarget.fingerprint)) throw mismatch();
      this.admission = await this.owner.admission('local'); this.assertCurrent();
      const connected = await connectPairedServer(origin, pin, nativeServerEnvironment(server.id), this.controller.signal);
      this.api = connected.api; this.assertCurrent();
      this.server = await rememberNativeServerDisplay(connected.api, {serverId: server.id, signIn: 'local',
        signal: this.controller.signal, isCurrent: this.current});
      this.assertCurrent();
    });
  }
  /** Explicit Portico path: identity and directory transport are opened only here. */
  connectPortico(server: HostedServer) {
    return this.run(async () => {
      if (this.api) throw mismatch();
      const credentials = this.owner.accountCredentials();
      await credentials.synchronize(); this.assertCurrent();
      const account = captureAccount(await credentials.accessSession()); this.assertCurrent();
      this.admission = await this.owner.admission('portico', {origin: hostedUrl, accountId: account.account.id});
      const connected = await connectHostedServer(server, nativeHostedSource(hostedUrl, credentials, account.account.id, accountExpectation(account)), nativeServerEnvironment(server.id), this.controller.signal);
      this.api = connected.api; this.hosted = connected.hosted; this.assertCurrent();
      const system = await rememberNativeServerDisplay(connected.api, {serverId: server.id, signIn: 'portico',
        signal: this.controller.signal, isCurrent: this.current}); this.assertCurrent();
      this.server = system;
      return this.consumeHostedCommand(connected, credentials, account,
        (hosted, api, signal) => porticoSignIn(hosted, hostedUrl, api, server.id, {signal, crypto: nativeRouteCrypto}));
    });
  }
  /** Shared directory/invitation handoff: preserve the exact account family through
   * exchange, repeated factors, profile choice and atomic publication. */
  private async consumeHostedCommand(connected: Awaited<ReturnType<typeof connectHostedServer>>,
    credentials: ReturnType<NativeAccounts['accountCredentials']>, account: Omit<HostedSession, 'refreshToken'>,
    command: (hosted: HostedRequestApi, api: HttpLocalApi, signal: AbortSignal) => Promise<DirectSignIn>) {
    const signal = this.controller.signal, expected = accountExpectation(account);
    const api = connected.api, routes = connected.connection, origin = api.baseUrl, pin = routes.pin;
    const samePin = () => {const live = api.getRouteConnection()?.pin; return !!live && live.serverId === pin.serverId && live.publicKey === pin.publicKey && live.fingerprint === pin.fingerprint;};
    this.hostedLease = () => {
      const live = credentials.getSnapshot().session;
      return this.api === api && live?.account.id === account.account.id && live.familyId === account.familyId && samePin();
    };
    const assertHostedCurrent = () => {this.assertCurrent();};
    const before = await nativeAppSessions.read(); assertHostedCurrent();
    const protectedFamilies = new Set(before.servers.map(value => value.connection.session.sessionFamilyId));
    if (this.account) protectedFamilies.add(this.account.sessionFamilyId);
    const retireFresh = async (fresh: LocalSession) => {
      if (fresh.viewer.authority !== 'local' || fresh.viewer.serverId !== pin.serverId
        || fresh.installationId !== this.installation?.installationId || protectedFamilies.has(fresh.sessionFamilyId)
        || fresh.sessionFamilyId === this.account?.sessionFamilyId) return;
      const stored = await nativeAppSessions.read();
      if (stored.servers.some(value => value.connection.session.sessionFamilyId === fresh.sessionFamilyId) || !samePin()) return;
      // Cleanup uses only this command's original issuer and proven routes,
      // never the now-selected server or its credential. Normal consume owns
      // retirement after the synchronous handoff below.
      const cleanup = new HttpLocalApi(origin, fresh.accessToken, nativeNetworkFetch); cleanup.useRoutes(routes);
      await cleanup.logout();
    };
    const officialHosted: HostedRequestApi = {request: async <T,>(path: string, method?: string, body?: unknown, callerSignal?: AbortSignal) => {
      assertHostedCurrent(); const originalBody = captureCommandBody(body), linked = linkNativeAbortSignals([signal, callerSignal]);
      try {const result = await nativeAccountWork(credentials, session =>
        new HttpLocalApi(hostedUrl, session.accessToken, nativeNetworkFetch).request<T>(path, method, originalBody, linked.signal),
        {expected, signal: linked.signal, isCurrent: this.current}); assertHostedCurrent(); return result;}
      catch (error) {assertHostedCurrent(); throw hostedSignInUnavailable(error) ?? error;}
      finally {linked.dispose();}
    }};
    const result = await command(officialHosted, api, signal);
    try {assertHostedCurrent();}
    catch (error) {if (result.kind === 'signed-in') await retireFresh(result.session); throw error;}
    if (result.kind === 'challenge') this.directoryFactor = {api, token: result.token, expiresAt: result.expiresAt, assert: assertHostedCurrent, retire: retireFresh};
    return this.consume(result);

  }
  previewPorticoInvitation(raw: string) {
    return this.run(async () => {
      const server = this.server, origin = this.probe.getSnapshot().origin;
      if (!server || server.signIn !== 'portico' || server.setupRequired || !origin || this.api || this.expectedTarget) throw mismatch();
      const input = NativeLocalInvitation.parseEntry(raw, origin);
      if (!input || input.server !== origin || input.id && input.id !== server.id) throw mismatch();
      const pin = await inspectDirectServer(origin, nativeServerEnvironment(server.id), this.controller.signal); this.assertCurrent();
      if (pin.serverId !== server.id || this.expectedInvitation?.fingerprint && pin.fingerprint !== this.expectedInvitation.fingerprint) throw mismatch();
      assertFirstContactIdentity(origin, pin, {knownRoutes: knownServerRoutes(await nativeAppSessions.read())}); this.assertCurrent();
      const paired = await connectPairedServer(origin, pin, nativeServerEnvironment(server.id), this.controller.signal);
      try {
        this.assertCurrent();
        const current = () => {const live = paired.api.getRouteConnection()?.pin; return this.current() && !!live
          && live.serverId === pin.serverId && live.publicKey === pin.publicKey && live.fingerprint === pin.fingerprint;};
        return await readPorticoInvitationPreview(paired.api, raw, {origin, id: pin.serverId, fingerprint: pin.fingerprint}, this.controller.signal, current);
      } finally {paired.connection.dispose();}
    });
  }
  preparePorticoInvitation() {
    return this.run(async () => {
      const server = this.server, origin = this.probe.getSnapshot().origin;
      if (!server || server.signIn !== 'portico' || server.setupRequired || !origin || this.api || this.expectedTarget || this.porticoInvitation) throw mismatch();
      const pin = await inspectDirectServer(origin, nativeServerEnvironment(server.id), this.controller.signal); this.assertCurrent();
      if (pin.serverId !== server.id || this.expectedInvitation?.fingerprint && pin.fingerprint !== this.expectedInvitation.fingerprint) throw mismatch();
      assertFirstContactIdentity(origin, pin, {knownRoutes: knownServerRoutes(await nativeAppSessions.read())}); this.assertCurrent();
      const credentials = this.owner.accountCredentials(); await credentials.synchronize(); this.assertCurrent();
      const account = captureAccount(await credentials.accessSession()); this.assertCurrent();
      const accountCurrent = () => {const live = credentials.getSnapshot().session; return live?.account.id === account.account.id && live.familyId === account.familyId;};
      this.hostedLease = accountCurrent; this.assertCurrent();
      this.admission = await this.owner.admission('portico', {origin: hostedUrl, accountId: account.account.id}); this.assertCurrent();
      const connected = await connectPairedPorticoServer(origin, pin, nativeHostedSource(hostedUrl, credentials, account.account.id, accountExpectation(account)), nativeServerEnvironment(server.id), this.controller.signal);
      try {
        this.assertCurrent(); this.api = connected.api; this.hosted = connected.hosted;
        this.hostedLease = () => {
          const live = connected.api.getRouteConnection()?.pin;
          return accountCurrent() && this.api === connected.api && !!live && live.serverId === pin.serverId && live.publicKey === pin.publicKey && live.fingerprint === pin.fingerprint;
        };
        this.assertCurrent();
        this.server = await rememberNativeServerDisplay(connected.api, {serverId: server.id, signIn: 'portico', signal: this.controller.signal, isCurrent: this.current});
        this.assertCurrent(); if (this.server.setupRequired) throw mismatch();
        const anonymous = new HttpLocalApi(connected.api.baseUrl, '', nativeNetworkFetch); anonymous.useRoutes(connected.connection);
        this.porticoInvitation = new NativePorticoInvitation({api: anonymous, origin: routeOrigin(origin), serverId: pin.serverId,
          fingerprint: pin.fingerprint, username: account.account.username, scope: {accountId: account.account.id, familyId: account.familyId},
          expiresAt: this.expectedInvitation?.expiresAt, current: this.current,
          accept: capability => this.run(() => this.consumeHostedCommand(connected, credentials, account, (hosted, api, signal) =>
            'shortCode' in capability
              ? acceptShortPorticoInvitation(hosted, hostedUrl, api, server.id, capability.shortCode, signal, nativeRouteCrypto)
              : acceptPorticoInvitation(hosted, hostedUrl, api, server.id, capability.code, signal, nativeRouteCrypto)))});
        return this.porticoInvitation;
      } catch (error) {connected.connection.dispose(); throw error;}
    });
  }
  localTVSignIn() {
    this.assertCurrent(); if (!this.server || !this.api || !this.admission) throw mismatch();
    return createNativeLocalTVSignIn(this.server, this.api);
  }
  acceptLocalTVSession(session: LocalSession) {
    return this.run(async () => {
      if (!this.api || !this.server || this.server.signIn !== 'local' || !this.admission) throw mismatch();
      this.issued.set(session.sessionFamilyId, session); this.api.setAccessToken(session.accessToken);
      try {
        const snapshot = parseDirectSnapshot(await this.api.request(routePath('get_direct_account'), 'GET', undefined, this.controller.signal),
          {authority: 'local', serverId: this.server.id, accountId: session.viewer.accountId});
        return await this.consume({kind: 'signed-in', snapshot, session, accountScoped: session.viewer.role === 'account', passwordChangeRequired: false});
      } catch (error) {try {await this.retire(session);} catch { /* the original error wins */ } throw error;}
    });
  }
  createLocalInvitation() {
    this.assertCurrent();
    const api = this.api, server = this.server, routes = api?.getRouteConnection(), pin = routes?.pin;
    if (!api || !server || !routes || !pin || server.signIn !== 'local' || server.setupRequired || !this.admission
      || this.account || this.expectedTarget || this.localInvitation || this.complete || this.busy) throw mismatch();
    this.invitationBinding = () => {
      const live = api.getRouteConnection()?.pin;
      return this.api === api && this.server === server && !!live
        && live.serverId === pin.serverId && live.publicKey === pin.publicKey && live.fingerprint === pin.fingerprint;
    };
    const invitationApi = new HttpLocalApi(api.baseUrl, '', nativeNetworkFetch); invitationApi.useRoutes(routes);
    this.localInvitation = new NativeLocalInvitation({api: invitationApi, origin: routeOrigin(api.baseUrl), serverId: pin.serverId, current: this.current, signIn: (username, password) => this.signInLocal(username, password)});
    return this.localInvitation;
  }
  signInLocal(username: string, password: string) {
    return this.run(async () => {
      if (this.server?.signIn !== 'local' || !this.api || this.account || !this.admission) throw mismatch();
      if (this.expectedTarget) this.assertSignInTarget(this.expectedTarget.serverId, this.expectedTarget.fingerprint);
      return this.consume(await directSignIn(this.api, username, password, this.controller.signal));
    });
  }
  completeSecondFactor(code: string) {
    return this.run(async () => {
      if (!this.api || !this.challenge || this.server?.signIn !== 'local') throw mismatch();
      return this.consume(await completeDirectSignInChallenge(this.api, this.challenge, code, this.controller.signal));
    });
  }
  directoryChallengePending() {return this.directoryFactor !== undefined;}
  /** Only the challenge returned by this exact directory sign-in can continue
   * with its captured Hosted family and original server issuer. */
  completeDirectorySecondFactor(code: string) {
    return this.run(async () => {
      const factor = this.directoryFactor;
      if (!factor || this.api !== factor.api || this.challenge !== factor.token || Date.parse(factor.expiresAt) <= Date.now()) throw cancelled();
      factor.assert();
      const result = await completeDirectSignInChallenge(factor.api, factor.token, code, this.controller.signal);
      try {factor.assert(); if (this.directoryFactor !== factor || this.challenge !== factor.token) throw cancelled();}
      catch (error) {if (result.kind === 'signed-in') await factor.retire(result.session); throw error;}
      this.directoryFactor = result.kind === 'challenge' ? {...factor, token: result.token, expiresAt: result.expiresAt} : undefined;
      return this.consume(result);
    });
  }
  private async consume(result: DirectSignIn) {
    if (result.kind !== 'signed-in') {
      this.assertCurrent();
      if (result.kind === 'challenge') {this.challenge = result.token; return {kind: 'challenge' as const, expiresAt: result.expiresAt};}
      // Retired device-approval answers never grant a session or create a waiting gate.
      throw mismatch();
    }
    const session = result.session; this.issued.set(session.sessionFamilyId, session);
    try {
      this.assertCurrent();
      if (!this.api || !this.server || result.snapshot.serverId !== this.server.id || session.installationId !== this.installation?.installationId) throw mismatch();
      this.api.setAccessToken(session.accessToken); this.snapshot = result.snapshot; this.challenge = undefined;
      const continuation = directSignInContinuation(result);
      if (continuation === 'password-change' || continuation === 'two-step-setup') {
        const requirement = continuation === 'password-change' ? 'password' as const : 'two-step' as const;
        const publicServer = this.requiredSetupHandoff && this.server.signIn === 'local'
          ? {serverName: this.server.name, serverOrigin: this.serverOrigin()} : undefined;
        // Ruling 31: a TV keeps no restricted grant while showing the public
        // hand-off. Logout uses the issued credential, never the selected viewer.
        this.api.setAccessToken(''); this.account = undefined; this.snapshot = undefined; this.challenge = undefined;
        await this.retire(session).catch(() => {});
        this.assertCurrent();
        if (!publicServer) throw Object.assign(new Error('native_required_account_setup'),
          {code: requirement === 'password' ? 'password_change_required' : 'two_step_setup_required'});
        return {kind: 'required-setup' as const, requirement, ...publicServer};
      }
      if (result.accountScoped) {this.account = session; return {kind: 'profiles' as const, profiles: result.snapshot.profiles};}
      await this.persist(session); return {kind: 'complete' as const};
    } catch (error) {try {await this.retire(session);} catch { /* the original error wins */ } throw error;}
  }
  chooseProfile(profileId: string, pin?: string) {
    return this.run(async () => {
      if (!this.api || !this.snapshot || !this.account || !this.snapshot.profiles.some(profile => profile.id === profileId)) throw mismatch();
      const api = this.api, snapshot = this.snapshot;
      await boundedPick(async (signal, trace) => {
        trace.mark('select');
        const result = await selectDirectProfile(api, {authority: 'local', accountId: snapshot.account.id, serverId: snapshot.serverId},
          profileId, {pin, installationId: this.installation?.installationId}, signal);
        this.issued.set(result.session.sessionFamilyId, result.session);
        try {await this.persist(result.session, trace);} catch (error) {
          // E-mobile-01: only a proven different identity ends the picked session; anything else leaves it unpublished.
          logPickFailure('sign-in', 'persist', error);
          if (provenIdentityMismatch(error)) {try {await this.retire(result.session);} catch { /* the original error wins */ }}
          else this.issued.delete(result.session.sessionFamilyId);
          throw error;
        }
      }, this.controller.signal);
    });
  }
  /** Explicit Waiting-member entry. The chosen server is proven before any
   * password/code is sent. Ordinary Local/Portico sign-in remains unchanged. */
  createMemberMove(initial: 'password' | 'code') {
    return this.run(async () => {
      this.memberMove?.dispose(); this.memberMove = undefined; this.memberMoveFactor = undefined;
      const descriptor = this.server, origin = this.probe.getSnapshot().origin;
      if (!descriptor || !origin || descriptor.move === null || this.account || this.challenge
        || initial === 'password' && descriptor.move !== 'to_portico') throw mismatch();
      const before = await nativeAppSessions.read(); this.assertCurrent();
      if (!this.api) {
        const pin = await inspectDirectServer(origin, nativeServerEnvironment(descriptor.id), this.controller.signal); this.assertCurrent();
        if (pin.serverId !== descriptor.id) throw mismatch();
        assertFirstContactIdentity(origin, pin, {knownRoutes: knownServerRoutes(before)});
        const connected = await connectPairedServer(origin, pin, nativeServerEnvironment(descriptor.id), this.controller.signal);
        if (!this.current()) {connected.connection.dispose(); throw cancelled();}
        this.api = connected.api;
      }
      const api = this.api, route = api.getRouteConnection(), pin = route?.pin;
      if (!route || !pin || pin.serverId !== descriptor.id) throw mismatch();
      const server = parseServerSignIn(await api.request(routePath('get_system'), 'GET', undefined, this.controller.signal)); this.assertCurrent();
      if (server.id !== descriptor.id || server.signIn !== descriptor.signIn || server.move !== descriptor.move) throw mismatch();
      // Before release an app opens only a server of its own API level (P2-66 G-2).
      negotiateNativeServer(server);
      this.server = server;
      const now = await nativeAppSessions.read(); this.assertCurrent();
      if (now.revision !== before.revision) throw cancelled();
      const retained = before.servers.find(value => value.connection.pin.serverId === before.selectedServerId);
      const originalAccount = this.account;
      let preCurrent = true;
      let releaseAccepted: (() => void) | undefined;
      const off = subscribeNativeAppSessions(() => {preCurrent = false;});
      this.controller.signal.addEventListener('abort', off, {once: true});
      const samePin = () => {
        const live = api.getRouteConnection()?.pin;
        return !!live && live.serverId === pin.serverId && live.publicKey === pin.publicKey && live.fingerprint === pin.fingerprint;
      };
      const isCurrent = () => preCurrent && this.current() && this.api === api && this.server === server && this.account === originalAccount && samePin();
      const assertCurrent = async () => {
        if (!isCurrent()) throw cancelled();
        const state = await nativeAppSessions.read();
        if (!isCurrent() || state.revision !== before.revision) throw cancelled();
      };
      if (server.signIn === 'local') {
        this.admission = await this.owner.admission('local'); await assertCurrent();
      }
      const retireCaptured = async (fresh: LocalSession) => {
        if (fresh.viewer.serverId !== pin.serverId || fresh.installationId !== this.installation!.installationId
          || fresh.sessionFamilyId === retained?.connection.session.sessionFamilyId || fresh.sessionFamilyId === this.account?.sessionFamilyId) return;
        // Cancellation may already have disposed the operation route. Re-prove
        // only the ORIGINAL issuing origin/pin, never a selected/default route.
        const cleanup = new AbortController(), timeout = setTimeout(() => cleanup.abort(), 15000);
        let connection: Awaited<ReturnType<typeof connectPairedServer>> | undefined;
        try {
          connection = await connectPairedServer(origin, pin, nativeServerEnvironment(pin.serverId), cleanup.signal);
          connection.api.setAccessToken(fresh.accessToken); await connection.api.logout();
        } finally {clearTimeout(timeout); connection?.connection.dispose();}
      };
      const move = new NativeMemberAccountMove({api, server, pin, origin, crypto: nativeRouteCrypto,
        installationId: this.installation!.installationId, signal: this.controller.signal, isCurrent, assertCurrent,
        retainedAppFamilyId: retained?.connection.session.sessionFamilyId, operationAccountFamilyId: () => this.account?.sessionFamilyId,
        retireLateGrant: retireCaptured,
        release: () => {off(); this.controller.signal.removeEventListener('abort', off); releaseAccepted?.(); releaseAccepted = undefined;},
        captureHosted: async () => {
          await assertCurrent();
          const credentials = this.owner.accountCredentials();
          await credentials.synchronize(); await assertCurrent();
          const captured = credentials.getSnapshot().session;
          if (!captured) throw cancelled();
          const accountId = captured.account.id, familyId = captured.familyId;
          const hostedCurrent = () => {
            const live = credentials.getSnapshot().session;
            return !!live && live.account.id === accountId && live.familyId === familyId;
          };
          const account = captureAccount(await credentials.accessSession()); await assertCurrent();
          if (!hostedCurrent() || account.account.id !== accountId || account.familyId !== familyId) throw cancelled();
          this.admission = await this.owner.admission('portico', {origin: hostedUrl, accountId}); await assertCurrent();
          if (!hostedCurrent()) throw cancelled();
          const paired = await connectPairedPorticoServer(origin, pin, nativeHostedSource(hostedUrl, credentials, accountId, accountExpectation(account)), nativeServerEnvironment(server.id), this.controller.signal);
          try {await assertCurrent(); if (!hostedCurrent()) throw cancelled(); this.hosted = paired.hosted;}
          finally {paired.connection.dispose();}
          const expected = accountExpectation(account);
          const hosted: HostedRequestApi = {request: async <T,>(path: string, method?: string, body?: unknown, callerSignal?: AbortSignal) => {
            const originalBody = captureCommandBody(body), linked = linkNativeAbortSignals([this.controller.signal, callerSignal]);
            try {return await nativeAccountWork(credentials,
              issued => new HttpLocalApi(hostedUrl, issued.accessToken, nativeNetworkFetch).request<T>(path, method, originalBody, linked.signal),
              {expected, signal: linked.signal, isCurrent: () => this.current() && hostedCurrent()});}
            finally {linked.dispose();}
          }};
          return {api: hosted, origin: hostedUrl, accountId, familyId,
            isCurrent: hostedCurrent, assertCurrent: async () => {if (!hostedCurrent()) throw cancelled(); await assertCurrent(); if (!hostedCurrent()) throw cancelled();}};
        },
        adopt: async (result, hosted) => {
          let handedToConsume = false;
          let accepted: Awaited<ReturnType<NativeServerSignIn['consume']>>;
          try {
            await assertCurrent();
            accepted = await this.run(() => {handedToConsume = true; return this.consume(result);});
          } catch (error) {
            if (!handedToConsume && result.kind === 'signed-in') await retireCaptured(result.session);
            throw error;
          }
          // Normal consume owns cleanup from here, including profile/challenge
          // continuations. Its successful writes intentionally retire preCurrent.
          off(); this.controller.signal.removeEventListener('abort', off);
          const acceptedAccount = this.account, acceptedSnapshot = this.snapshot, acceptedChallenge = this.challenge;
          let lease = this.current() && this.api === api && this.server === server && samePin();
          const unsubscribe = subscribeNativeAppSessions(() => {lease = false;});
          const release = () => {lease = false; unsubscribe(); this.controller.signal.removeEventListener('abort', release);};
          releaseAccepted = release;
          this.controller.signal.addEventListener('abort', release, {once: true});
          const state = await nativeAppSessions.read();
          if (accepted.kind === 'complete' && result.kind === 'signed-in') {
            const selected = state.servers.find(value => value.connection.pin.serverId === state.selectedServerId);
            const actual = selected?.connection;
            lease = lease && !!actual && actual.session.sessionFamilyId === result.session.sessionFamilyId
              && actual.pin.serverId === pin.serverId && actual.pin.publicKey === pin.publicKey && actual.pin.fingerprint === pin.fingerprint
              && (['serverId', 'accountId', 'profileId', 'authority', 'role'] as const).every(key => actual.session.viewer[key] === result.session.viewer[key]);
          } else {
            lease = lease && state.revision === before.revision;
            if (accepted.kind === 'profiles' && result.kind === 'signed-in')
              lease = lease && acceptedAccount === result.session && acceptedSnapshot === result.snapshot;
          }
          // Store notifications invalidate this exact revision's lease; no
          // selected/default getter may broaden it after normal adoption.
          const acceptedCurrent = () => lease && this.current() && this.api === api
            && this.server === server && this.account === acceptedAccount && this.snapshot === acceptedSnapshot && this.challenge === acceptedChallenge && samePin();
          if (accepted.kind === 'challenge' && acceptedChallenge) {
            // Keep the SAME official Hosted account/family captured by the
            // redeem command throughout this server's factor continuation.
            const factorCurrent = () => acceptedCurrent() && (!hosted || hosted.isCurrent());
            const assertFactorCurrent = async () => {
              if (!factorCurrent()) throw cancelled();
              if (hosted) {await hosted.assertCurrent(); if (!factorCurrent()) throw cancelled();}
              const stored = await nativeAppSessions.read();
              if (!factorCurrent() || stored.revision !== state.revision) throw cancelled();
            };
            this.memberMoveFactor = {api, token: acceptedChallenge, expiresAt: accepted.expiresAt,
              isCurrent: factorCurrent, assertCurrent: assertFactorCurrent, retire: retireCaptured};
          }
          return {result: accepted, isCurrent: acceptedCurrent};
        },
      }, initial);
      this.memberMove = move;
      return move;
    });
  }
  /** Only the exact challenge adopted by this member move may use this
   * continuation. Ordinary Local/TV factor admission is unchanged. */
  completeMemberMoveFactor(code: string) {
    return this.run(async () => {
      const factor = this.memberMoveFactor;
      if (!factor || !factor.isCurrent() || this.api !== factor.api || this.challenge !== factor.token
        || Date.parse(factor.expiresAt) <= Date.now()) throw cancelled();
      await factor.assertCurrent();
      if (this.memberMoveFactor !== factor || !factor.isCurrent()) throw cancelled();
      const result = await completeDirectSignInChallenge(factor.api, factor.token, code, this.controller.signal);
      try {
        await factor.assertCurrent();
        if (!factor.isCurrent() || this.memberMoveFactor !== factor || this.api !== factor.api || this.challenge !== factor.token) throw cancelled();
      } catch (error) {
        if (result.kind === 'signed-in') await factor.retire(result.session);
        throw error;
      }
      // No await separates the final captured Hosted/issuer fence and normal
      // consume; from that handoff onward the operation owns grant cleanup.
      const accepted = await this.consume(result);
      this.memberMoveFactor = undefined;
      return accepted;
    });
  }
  /** Auth18 on the sign-in picker. The same account-scoped sign-in that opened the
   * picker supplies a proven management snapshot. This seam never opens the
   * temporary-proof path, and disposal closes the recovery fence. */
  profileRecovery(profileId: string) {
    this.assertCurrent();
    const api = this.api, snapshot = this.snapshot, account = this.account, server = this.server;
    if (!api || !snapshot || !account || !server || !snapshot.canManage || !snapshot.capabilities.manageAccount
      || account.viewer.role !== 'account' || account.viewer.authority !== snapshot.authority
      || account.viewer.serverId !== snapshot.serverId || account.viewer.accountId !== snapshot.account.id) throw mismatch();
    const pin = api.getRouteConnection()?.pin;
    if (!pin) throw mismatch();
    const authorityCurrent = () => {
      if (!this.current() || this.api !== api || this.snapshot !== snapshot || this.account !== account) return false;
      const live = api.getRouteConnection()?.pin;
      return !!live && live.serverId === pin.serverId && live.publicKey === pin.publicKey && live.fingerprint === pin.fingerprint;
    };
    return new NativeProfileRecovery({api, snapshot, profileId, selectedFamilyId: account.sessionFamilyId, kind: server.signIn,
      authorityCurrent,
      // This seam already has an authenticated management grant. Never mint or
      // clean up a proof family here: both the account grant and any installed
      // viewer family remain retained until normal sign-in publication/cancel.
      temporary: () => {throw mismatch();}});

  }
  private async persist(session: LocalSession, trace?: PickTrace) {
    this.assertCurrent();
    if (!this.api || !this.admission || session.viewer.authority !== 'local' || session.viewer.role === 'account' || session.installationId !== this.installation?.installationId) throw mismatch();
    this.api.setAccessToken(session.accessToken);
    this.publishing = true;
    try {
    const previous = await this.owner.remember(this.api, session, this.admission, this.hosted, this.current, trace);
    trace?.mark('published');
    this.issued.delete(session.sessionFamilyId); this.setupIssuers.delete(session.sessionFamilyId); this.complete = true;
    if (this.snapshot && this.server) {
      const pin = this.api.getRouteConnection()?.pin;
      // A convenience for the next sign-in: never awaited on the way to Home.
      if (pin) void rememberNativeSignInHint({serverId: pin.serverId, accountId: session.viewer.accountId, familyId: session.sessionFamilyId,
        fingerprint: pin.fingerprint, origin: this.api.baseUrl, serverName: this.server.name, username: this.snapshot.account.username, signIn: this.server.signIn}).catch(() => {});
    }
    const temporary = this.account; this.account = undefined;
    // E-mobile-01: ending the replaced and temporary sessions is mandatory but never blocks Home.
    // Both go over the route that just served this sign-in; a slow or failed try is queued.
    const live = this.api.getRouteConnection();
    void this.owner.retireReplacedServerSession(previous, live);
    if (temporary) {
      let settled = false;
      const queued = () => {if (settled) return; settled = true; if (live) void revokeInBackground({version: '1', logicalOrigin: live.logicalOrigin, session: temporary, pin: live.pin, routes: {paired: []}}, live);};
      const timer = setTimeout(queued, REVOCATION_TIMEOUT_MS);
      void this.retire(temporary).then(() => {clearTimeout(timer); settled = true;}, () => {clearTimeout(timer); queued();});
    }
    } finally {
      this.publishing = false;
      // A pick that outlived its deadline has already released its caller.
      if (this.disposeRequested && !this.busy) this.cancel();
    }
  }
  private async retire(session: LocalSession) {
    if (!this.issued.delete(session.sessionFamilyId) || !this.api) return;
    const setup = this.setupIssuers.get(session.sessionFamilyId);
    this.setupIssuers.delete(session.sessionFamilyId);
    if (setup) {
      // Initialization/resume may replay the same grant to another operation.
      // A failed turn owns no cleanup of a family another turn has retained.
      const stored = await nativeAppSessions.read();
      if (stored.servers.some(value => value.connection.session.sessionFamilyId === session.sessionFamilyId)
        || setup.routes.pin.serverId !== setup.pin.serverId || setup.routes.pin.publicKey !== setup.pin.publicKey
        || setup.routes.pin.fingerprint !== setup.pin.fingerprint
        || session.viewer.serverId !== setup.pin.serverId || session.serverIdentity?.publicKey !== setup.pin.publicKey
        || session.serverIdentity.fingerprint !== setup.pin.fingerprint) return;
      const cleanup = new HttpLocalApi(setup.origin, session.accessToken, nativeNetworkFetch);
      cleanup.useRoutes(setup.routes); await cleanup.logout(); return;
    }
    const retired = new HttpLocalApi(this.api.baseUrl, session.accessToken, nativeNetworkFetch);
    const routes = this.api.getRouteConnection(); if (routes) retired.useRoutes(routes);
    await retired.logout();
  }
  dispose() {
    // A shell fence can unmount the initiating view during the atomic publication.
    // The headless command owns that completion; explicit account cancellation still
    // calls cancel immediately and is compensated by the canonical store.
    if (this.publishing) {this.disposeRequested = true; return;}
    this.cancel();
  }
  private cancel() {
    if (this.controller.signal.aborted) return;
    this.localInvitation?.dispose(); this.porticoInvitation?.dispose(); this.hostedSetup?.dispose(); this.directoryFactor = undefined; this.controller.abort(); if (this.pendingIdentity) clearPendingCertificate(this.pendingIdentity.origin, this.pendingIdentity); this.pendingIdentity = undefined;
    this.probe.dispose(); this.unfence();
    const routes = this.api?.getRouteConnection();
    void Promise.resolve(this.running).catch(() => {}).then(() => Promise.allSettled([...this.issued.values()].map(session => this.retire(session)))).finally(() => routes?.dispose());
    this.ownerResetGrant = undefined; this.account = undefined; this.snapshot = undefined; this.challenge = undefined;
  }
}
