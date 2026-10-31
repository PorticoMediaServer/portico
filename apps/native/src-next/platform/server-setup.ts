import {assertFirstContactIdentity, type HttpLocalApi, type LocalSession} from '../../../../packages/client-core/src';
import type {ServerSignIn} from '../../../../packages/client-core/src/sign-in';
import {parseDirectSnapshot, type DirectSignIn} from '../../../../packages/client-core/src/profile-management';
import {connectPairedServer, inspectDirectServer} from '../../../../packages/client-core/src/server-connections';
import {knownServerRoutes, type NewServerConnection, type AppServerSession} from '../../../../packages/client-core/src/app-sessions';
import {routePath} from '../../../../packages/contracts/src/routes';
import type {ServerPin} from '../../../../packages/client-core/src/route-identity';
import {startAppSetup, initializeServer} from '../../../../packages/client-core/src/setup-onboarding';
import type {NativeAccounts} from './accounts';
import {hostedUrl} from './account-authority';
import {nativeServerEnvironment} from './server-connections';
import {rememberNativeServerDisplay} from './server-display';
import {nativeAppSessions} from './app-session-storage';
import {createNativeSetupClaim} from './setup-claim';
import {nativeSetupDraft} from './setup-draft';

const mismatch = () => Object.assign(new Error('native_sign_in_authority_changed'), {code: 'identity_mismatch'});

/** The original issuer of a fresh grant, so cleanup can end it over the same route. */
export type NativeSetupIssuer = {origin: string; routes: NonNullable<ReturnType<HttpLocalApi['getRouteConnection']>>; pin: ServerPin};

/** What first-run setup reads and writes on the sign-in command that hosts it (C0 seam).
 * Setup's own state (`hostedSetup`, `setupFinishLife`) lives here; the connection, admission,
 * issued grants and publication stay with the sign-in owner. */
export interface NativeSetupHost<Result> {
  api?: HttpLocalApi;
  server?: ServerSignIn;
  admission?: NewServerConnection;
  hosted?: AppServerSession['connection']['hosted'];
  readonly account?: LocalSession;
  readonly installation?: Readonly<{installationId: string}>;
  readonly expectedTarget?: Readonly<{serverId: string; fingerprint: string}>;
  readonly owner: NativeAccounts;
  readonly signal: AbortSignal;
  readonly complete: boolean;
  readonly issued: Map<string, LocalSession>;
  readonly setupIssuers: Map<string, NativeSetupIssuer>;
  probeOrigin(): string | undefined;
  run<T>(work: () => Promise<T>): Promise<T>;
  current(): boolean;
  assertCurrent(): void;
  consume(result: DirectSignIn): Promise<Result>;
  retire(session: LocalSession): Promise<void>;
  dispose(): void;
}

/** First-run setup commands (lane B). Moved unchanged from NativeServerSignIn. */
export class NativeServerSetup<Result> {
  private hostedSetup?: ReturnType<typeof createNativeSetupClaim>;
  private setupFinishLife?: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>;
  constructor(private readonly host: NativeSetupHost<Result>) {}
  /** Part of the host's lifetime: a finishing setup whose caller retired ends it. */
  finishCurrent() {
    const life = this.setupFinishLife;
    try {return !life || !life.signal.aborted && life.isCurrent() === true;} catch {return false;}
  }
  dispose() {this.hostedSetup?.dispose();}
  /** Setup is admitted only after the same explicit first-contact proof as sign-in. */
  prepareSetup() {
    return this.host.run(async () => {
      const server = this.host.server, origin = this.host.probeOrigin();
      if (!server?.setupRequired || !origin || this.host.api) throw mismatch();
      const pin = await inspectDirectServer(origin, nativeServerEnvironment(server.id), this.host.signal); this.host.assertCurrent();
      if (pin.serverId !== server.id || this.host.expectedTarget && (pin.serverId !== this.host.expectedTarget.serverId || pin.fingerprint !== this.host.expectedTarget.fingerprint)) throw mismatch();
      assertFirstContactIdentity(origin, pin, {knownRoutes: knownServerRoutes(await nativeAppSessions.read())}); this.host.assertCurrent();
      this.host.admission = await this.host.owner.admission('local'); this.host.assertCurrent();
      const connected = await connectPairedServer(origin, pin, nativeServerEnvironment(server.id), this.host.signal);
      this.host.api = connected.api; this.host.assertCurrent();
      const checked = await rememberNativeServerDisplay(connected.api, {serverId: server.id, signIn: server.signIn,
        signal: this.host.signal, isCurrent: this.host.current, setup: true}); this.host.assertCurrent();
      if (!checked.setupRequired) throw mismatch();
      this.host.server = checked; return checked;
    });
  }
  setupLocal(name: string, username: string, password: string) {
    return this.host.run(async () => {
      const api = this.host.api, server = this.host.server;
      if (!api || !server?.setupRequired || !this.host.admission) throw mismatch();
      const store = nativeSetupDraft(server.id);
      const saved = await store.read(); this.host.assertCurrent();
      if (saved && saved.authMode !== 'local') throw mismatch();
      const token = saved?.setupToken ?? (await startAppSetup(api, this.host.signal)).setupToken; this.host.assertCurrent();
      const retained = await nativeAppSessions.read(); this.host.assertCurrent();
      const issued = await initializeServer(api, store, {setupToken: token, name: name.trim(), username: username.trim(), password,
        authMode: 'local', interactive: true}, this.host.signal);
      // Pending Hosted claims are never sessions. Retain a fresh local grant for
      // cancellation cleanup before any asynchronous snapshot or publication.
      if ('claimRequired' in issued) throw mismatch();
      const pin = api.getRouteConnection()?.pin;
      if (!pin || issued.viewer.authority !== 'local' || issued.viewer.role !== 'owner' || issued.viewer.serverId !== server.id || Date.parse(issued.expiresAt) <= Date.now()
        || issued.installationId !== this.host.installation?.installationId
        || issued.serverIdentity?.publicKey !== pin.publicKey || issued.serverIdentity.fingerprint !== pin.fingerprint
        || retained.servers.some(value => value.connection.session.sessionFamilyId === issued.sessionFamilyId)
        || this.host.account?.sessionFamilyId === issued.sessionFamilyId) throw mismatch();
      const nowStored = await nativeAppSessions.read();
      if (nowStored.servers.some(value => value.connection.session.sessionFamilyId === issued.sessionFamilyId)) throw mismatch();
      const routes = api.getRouteConnection();
      if (!routes || routes.pin.serverId !== pin.serverId || routes.pin.publicKey !== pin.publicKey || routes.pin.fingerprint !== pin.fingerprint) throw mismatch();
      this.host.setupIssuers.set(issued.sessionFamilyId, {origin: api.baseUrl, routes, pin: {...pin}});
      this.host.issued.set(issued.sessionFamilyId, issued);
      try {
        const completed = await store.read();
        this.host.assertCurrent(); api.setAccessToken(issued.accessToken);
        const snapshot = parseDirectSnapshot(await api.request(routePath('get_direct_account'), 'GET', undefined, this.host.signal),
          {authority: 'local', serverId: server.id, accountId: issued.viewer.accountId}, true);
        this.host.assertCurrent();
        if (snapshot.account.role !== 'owner' || snapshot.account.disabled || !snapshot.capabilities.manageServer
          || snapshot.account.primaryProfileId !== issued.viewer.profileId) throw mismatch();
        this.host.server = {...server, name: name.trim(), signIn: 'local', setupRequired: false};
        const result = await this.host.consume({kind: 'signed-in', session: issued, snapshot,
          accountScoped: false, passwordChangeRequired: false});
        await store.change(old => {
          this.host.assertCurrent();
          if (!completed || old?.requestId !== completed.requestId || old.setupToken !== completed.setupToken || old.authMode !== 'local') throw mismatch();
          return undefined;
        });
        return result;
      } catch (error) {try {await this.host.retire(issued);} catch { /* the original error wins */ } throw error;}
    });
  }
  prepareHostedSetup(name: string) {
    return this.host.run(async () => {
      const api = this.host.api, server = this.host.server;
      if (!api || !server?.setupRequired) throw mismatch();
      const origin = new URL(api.baseUrl);
      if (origin.origin !== api.baseUrl || (origin.protocol !== 'https:' && !(origin.protocol === 'http:' && origin.hostname === 'localhost'))) throw mismatch();
      const claim = this.hostedSetup ??= createNativeSetupClaim(api.baseUrl, server.id);
      const existing = await claim.resumePrepared(); this.host.assertCurrent();
      if (existing) return existing.url;
      const store = nativeSetupDraft(server.id), saved = await store.read(); this.host.assertCurrent();
      if (saved && saved.authMode !== 'hosted') throw mismatch();
      const token = saved?.setupToken ?? (await startAppSetup(api, this.host.signal)).setupToken; this.host.assertCurrent();
      const pending = await initializeServer(api, store, {setupToken: token, name: name.trim(), authMode: 'hosted', interactive: true}, this.host.signal);
      this.host.assertCurrent(); if (!('claimRequired' in pending)) throw mismatch();
      const url = await claim.prepare(api, hostedUrl); this.host.assertCurrent(); return url;
    });
  }
  reviewHostedSetup(url: string, opening: {signal: AbortSignal; current: () => boolean; canApprove: () => boolean}) {
    return this.host.run(async () => {
      if (!this.hostedSetup) throw mismatch();
      const review = await this.hostedSetup.accountApproval(url, opening); this.host.assertCurrent(); return review;
    });
  }
  restartEndedSetup() {
    return this.host.run(async () => {
      const claim = this.hostedSetup, api = this.host.api, server = this.host.server;
      if (!claim || !api || !server?.setupRequired) throw mismatch();
      await claim.abandonEnded(); this.host.assertCurrent();
      const checked = await rememberNativeServerDisplay(api, {serverId: server.id, signIn: server.signIn, signal: this.host.signal, isCurrent: this.host.current, setup: true});
      this.host.assertCurrent(); if (!checked.setupRequired) throw mismatch();
      this.hostedSetup = undefined; this.host.server = checked;
    });
  }
  finishHostedSetup(lifetime?: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>) {
    return this.host.run(async () => {
      const retire = () => this.host.dispose();
      this.setupFinishLife = lifetime;
      lifetime?.signal.addEventListener('abort', retire, {once: true});
      try {
      this.host.assertCurrent();
      const claim = this.hostedSetup;
      if (!claim || !this.host.server) throw mismatch();
      const completed = await claim.finishInstalled();
      if (!completed) {this.host.assertCurrent(); return undefined;}
      try {
      const {api, result, admission, hosted, draft} = completed, routes = api.getRouteConnection();
      this.host.assertCurrent();
      const retained = await nativeAppSessions.read(); this.host.assertCurrent();
      if (!routes || retained.servers.some(value => value.connection.session.sessionFamilyId === result.session.sessionFamilyId)) throw mismatch();
      this.host.api = api; this.host.admission = admission; this.host.hosted = hosted;
      this.host.setupIssuers.set(result.session.sessionFamilyId, {origin: api.baseUrl, routes, pin: {...routes.pin}});
      this.host.issued.set(result.session.sessionFamilyId, result.session); completed.accept();
      try {
        this.host.assertCurrent(); this.host.server = {...this.host.server, signIn: 'portico', setupRequired: false};
        const accepted = await this.host.consume(result);
        await nativeSetupDraft(result.snapshot.serverId).change(old => {
          // An accepted publication can itself unmount this form. Its cleanup
          // spends only the original setup draft, never successor authority.
          if (!this.host.complete) this.host.assertCurrent();
          if (!old || old.requestId !== draft.requestId || old.setupToken !== draft.setupToken || old.authMode !== 'hosted') throw mismatch();
          return undefined;
        });
        return accepted;
      } catch (error) {try {await this.host.retire(result.session);} catch { /* the original error wins */ } throw error;}
      } catch (error) {await completed.release(); throw error;}
      } finally {
        lifetime?.signal.removeEventListener('abort', retire);
        if (this.setupFinishLife === lifetime) this.setupFinishLife = undefined;
      }
    });
  }
}
