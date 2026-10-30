import {HttpLocalApi, type LocalSession} from '@core/index.ts';
import {type AppSessionStore, knownServerRoutes} from '@core/app-sessions.ts';
import {installationIdentity} from '@core/installation.ts';
import {type RegisteredSignIn, type ReturnPending, type ReturnPendingStore, prepareRegisteredSignIn, consumeSameTabReturn} from '@core/same-tab-return.ts';
import {REGISTERED_SIGN_IN_CLOCK_SKEW_MS, registeredSignInExpiryWithinLifetime} from '@core/registered-sign-in-context.ts';
import {assertFirstContactIdentity, routeBase64, routeBytes, routeOrigin, serverPin, sameServerPin, type ServerPin} from '@core/route-identity.ts';
import {connectPairedServer, connectPairedPorticoServer, rememberNativeSession, type ConnectionEnvironment} from '@core/server-connections.ts';
import {CodedError} from '@core/server-messages.ts';
import {completeDirectSignInChallenge, type DirectSignIn} from '@core/profile-management.ts';
import {routePath} from '../../../packages/contracts/src/routes.ts';
import {secureBrowserPending} from './secure-pending';
import {browserAppSessions, browserConnectionEnvironment} from './server-connections';
import {type CapturedPorticoReturn, dispatchPorticoReturn} from './portico-return-dispatch';

type Pending = Readonly<{
  version: 1; kind: 'registered'; origin: string; hostedOrigin: string; pin: ServerPin;
  memberMove?: true; expectedRevision: number; attemptId: string; expiresAt: string; pending?: ReturnPending;
}>;
type Journal = Readonly<{
  assertAvailable: () => void;
  read: () => Promise<Pending | undefined>;
  change: (update: (old: Pending | undefined) => Pending | undefined) => Promise<void>;
}>;
type Ports = Readonly<{
  journal: Journal; sessions: Pick<AppSessionStore, 'read' | 'connectionStorage'>;
  environment: (serverId: string) => ConnectionEnvironment;
  installationId: () => string;
  pageOrigin: () => string;
  /** Only Services' checked canonical builder may be supplied by production. */
  startURL: (value: RegisteredSignIn) => string;
  prepare?: typeof prepareRegisteredSignIn; consume?: typeof consumeSameTabReturn;
  connect?: typeof connectPairedServer; connectPortico?: typeof connectPairedPorticoServer; remember?: typeof rememberNativeSession;
  retire?: (api: HttpLocalApi, session: LocalSession) => Promise<void>;
}>;
const cancelled = () => new CodedError('cancelled', 'This server sign-in changed.');
const invalid = () => new CodedError('invalid_return', 'This sign-in is no longer available.');
type Completion = Readonly<{api: HttpLocalApi; result: DirectSignIn; memberMove?: true}>;

/** One tab's private verifier and captured selection revision. The Hosted handoff
 * receives only RegisteredSignIn. This class never requests a Hosted credential. */
export class BrowserRegisteredSignIn {
  private epoch = 0;
  private controller?: AbortController;
  private attemptId?: string;
  private challenge?: {complete: (code: string) => Promise<Completion>; close: () => void};
  constructor(private readonly ports: Ports) {}
  private operation() {
    this.challenge?.close(); this.challenge = undefined;
    this.controller?.abort(); const epoch = ++this.epoch, controller = this.controller = new AbortController();
    return {signal: controller.signal, current: () => this.epoch === epoch && !controller.signal.aborted};
  }
  async cancel() {
    ++this.epoch; this.controller?.abort();
    this.challenge?.close(); this.challenge = undefined;
    const attemptId = this.attemptId; this.attemptId = undefined;
    if (attemptId) await this.ports.journal.change(old => old?.attemptId === attemptId ? undefined : old);
  }
  async prepare(api: HttpLocalApi, origin: string, hostedOrigin: string, installationId: string,
    move: Readonly<{moveTicket?: string; moveCode?: string}> = {}) {
    this.ports.journal.assertAvailable();
    if (routeOrigin(origin) !== origin || origin !== this.ports.pageOrigin() || !origin.startsWith('https://') || installationId !== this.ports.installationId()) throw invalid();
    const route = api.getRouteConnection();
    if (!route || route.getSnapshot().phase !== 'ready') throw invalid();
    const operation = this.operation(), state = await this.ports.sessions.read();
    if (!operation.current()) throw cancelled();
    assertFirstContactIdentity(origin, route.pin, {knownRoutes: knownServerRoutes(state)});
    const attemptId = routeBase64(crypto.getRandomValues(new Uint8Array(32)));
    // Supersede before network work. A second controller sharing this tab's
    // vault wins atomically, even if the earlier prepare answers much later.
    await this.ports.journal.change(() => {
      if (!operation.current()) throw cancelled();
      this.attemptId = attemptId;
      return {version: 1, kind: 'registered', origin, hostedOrigin, pin: route.pin,
        ...(move.moveCode || move.moveTicket ? {memberMove: true as const} : {}), expectedRevision: state.revision, attemptId, expiresAt: ''};
    });
    const port: ReturnPendingStore = {
      read: async () => (await this.ports.journal.read())?.pending,
      change: async update => this.ports.journal.change(old => {
        if (!operation.current() || old?.attemptId !== attemptId) throw cancelled();
        const pending = update(old?.pending);
        if (!pending) return undefined;
        // prepareRegisteredSignIn provides the actual producer expiry after the
        // private write. Until then the record is deliberately not consumable.
        return {version: 1, kind: 'registered', origin, hostedOrigin, pin: route.pin,
          ...(move.moveCode || move.moveTicket ? {memberMove: true as const} : {}), expectedRevision: state.revision, attemptId, expiresAt: '', pending};
      }),
    };
    try {
      const value = await (this.ports.prepare ?? prepareRegisteredSignIn)(api, port,
        {origin, hostedOrigin, installationId, ...move}, {signal: operation.signal, isCurrent: operation.current});
      const expiresAt = Date.parse(value.expiresAt);
      const fresh = () => Number.isFinite(expiresAt) && expiresAt > Date.now();
      if (!fresh()) throw cancelled();
      const url = this.ports.startURL(value);
      if (!operation.current() || (await this.ports.sessions.read()).revision !== state.revision) throw cancelled();
      await this.ports.journal.change(old => {
        if (!operation.current() || !old || old.attemptId !== attemptId || old.pending?.state !== value.start.state) throw cancelled();
        return {...old, expiresAt: value.expiresAt};
      });
      // IndexedDB/tab-lock waits may outlast the signed prepare. Never hand
      // an expired ordinary/member move off after its durable private write.
      if (!operation.current() || !fresh()) throw cancelled();
      return url;
    } catch (error) {
      await this.ports.journal.change(old => old?.attemptId === attemptId ? undefined : old);
      if (this.attemptId === attemptId) this.attemptId = undefined;
      throw error;
    }
  }
  private async retire(api: HttpLocalApi, session: LocalSession) {
    if (this.ports.retire) return this.ports.retire(api, session);
    const temporary = new HttpLocalApi(api.baseUrl, session.accessToken), route = api.getRouteConnection();
    if (route) temporary.useRoutes(route);
    await temporary.request(routePath('delete_sessions_current'), 'DELETE', undefined, AbortSignal.timeout(5000)).catch(() => {});
  }
  /** capturePorticoReturn must already have removed the fragment synchronously.
   * Taking the journal is atomic; another tab/reload cannot reuse this attempt. */
  async finish(captured: CapturedPorticoReturn): Promise<Completion> {
    this.ports.journal.assertAvailable();
    const operation = this.operation(); let record: Pending | undefined;
    await this.ports.journal.change(old => {record = old; return undefined;});
    this.attemptId = undefined;
    if (!record || record.version !== 1 || record.kind !== 'registered' || !record.pending) throw invalid();
    const saved = {...record, pending: record.pending}, url = new URL(captured.href), expires = Date.parse(saved.expiresAt), now = Date.now();
    // createdAt is this browser's clock, expires is the signing Server's. The
    // signed prepare still bounds itself; this record adds only the same bounded
    // skew and never accepts an expiry this browser already reached.
    if (!Number.isSafeInteger(saved.pending.createdAt) || saved.pending.createdAt < 0 || saved.pending.createdAt > now + REGISTERED_SIGN_IN_CLOCK_SKEW_MS ||
      !registeredSignInExpiryWithinLifetime(expires, saved.pending.createdAt, now) ||
      url.origin !== this.ports.pageOrigin() || url.origin !== saved.origin || saved.pending.registrationOrigin !== saved.origin || saved.pending.purpose !== 'sign-in' ||
      saved.pending.serverId !== saved.pin.serverId || saved.pending.installationId !== this.ports.installationId() ||
      !Number.isSafeInteger(saved.expectedRevision) || saved.expectedRevision < 0) throw invalid();
    const dispatch = dispatchPorticoReturn(captured, {kind: 'registered'});
    const actualPin = await serverPin(saved.pin.serverId, saved.pin.publicKey, saved.pin.fingerprint);
    if (!sameServerPin(actualPin, saved.pin)) throw invalid();
    const current = async () => {
      const state = await this.ports.sessions.read();
      if (!operation.current() || this.ports.pageOrigin() !== saved.origin || this.ports.installationId() !== saved.pending.installationId ||
        Date.now() >= expires || state.revision !== saved.expectedRevision) throw cancelled();
    };
    await current();
    assertFirstContactIdentity(saved.origin, saved.pin, {knownRoutes: knownServerRoutes(await this.ports.sessions.read())});
    const environment = this.ports.environment(saved.pin.serverId);
    const connected = await (this.ports.connect ?? connectPairedServer)(saved.origin, saved.pin, environment, operation.signal);
    let grant: Awaited<ReturnType<typeof consumeSameTabReturn>> | undefined, adopted = false, keepForChallenge = false;
    let bound: Awaited<ReturnType<typeof connectPairedPorticoServer>> | undefined;
    const admit = async (grant: Extract<DirectSignIn, {kind: 'signed-in'}>): Promise<Completion> => {
      await current();
      const accountId = grant.snapshot.account.hostedAccountId;
      if (!accountId || grant.snapshot.serverId !== saved.pin.serverId || grant.resetFileOwnerSession || grant.passwordChangeRequired) throw invalid();
      // A challenge never selects an account or enters durable session storage.
      // Only this genuine, admitted Server grant supplies the Hosted account id.
      bound = await (this.ports.connectPortico ?? connectPairedPorticoServer)(saved.origin, saved.pin,
        {origin: saved.hostedOrigin, accountId, request: async () => {throw new CodedError('account_signed_out', 'Use Portico Account to refresh its directory.');}}, environment, operation.signal);
      await current();
      bound.api.setAccessToken(grant.session.accessToken);
      const admission = {...environment, storage: this.ports.sessions.connectionStorage(saved.pin.serverId,
        {signIn: 'portico', hostedAccount: bound.hosted, expectedRevision: saved.expectedRevision})};
      await (this.ports.remember ?? rememberNativeSession)(bound.api, grant.session, admission, bound.hosted, operation.current);
      if (!operation.current()) throw cancelled();
      adopted = true;
      return {api: bound.api, result: grant, ...(saved.memberMove === true ? {memberMove: true as const} : {})};
    };
    try {
      await current();
      let pending: ReturnPending | undefined = saved.pending;
      const port: ReturnPendingStore = {read: async () => pending, change: async update => {pending = update(pending);}};
      grant = await (this.ports.consume ?? consumeSameTabReturn)(connected.api, port, {href: dispatch.href}, {replaceState: () => {}},
        {isCurrent: operation.current, retireLateGrant: session => this.retire(connected.api, session)}, operation.signal);
      if (grant.kind === 'signed-in') return await admit(grant);
      await current();
      const challenge = grant;
      if (Date.parse(challenge.expiresAt) <= Date.now()) throw invalid();
      let busy = false;
      const capturedChallenge = {
        close: () => {if (!busy) connected.connection.dispose();},
        complete: async (code: string): Promise<Completion> => {
          if (busy || this.challenge !== capturedChallenge) throw invalid();
          busy = true; let factorGrant: DirectSignIn | undefined, mayRetry = false;
          try {
            await current();
            if (Date.parse(challenge.expiresAt) <= Date.now()) throw invalid();
            mayRetry = true;
            factorGrant = await completeDirectSignInChallenge(connected.api, challenge.token, code, operation.signal);
            mayRetry = false;
            if (factorGrant.kind !== 'signed-in') throw invalid();
            return await admit(factorGrant);
          } finally {
            busy = false;
            if (!adopted && factorGrant?.kind === 'signed-in') await this.retire(connected.api, factorGrant.session);
            let stillCurrent = false;
            if (mayRetry) {try {await current(); stillCurrent = true;} catch { /* Closed authority cannot retain this challenge. */ }}
            if (adopted || !stillCurrent || Date.now() >= Date.parse(challenge.expiresAt)) {
              if (this.challenge === capturedChallenge) this.challenge = undefined;
              connected.connection.dispose();
              if (!adopted) bound?.connection.dispose();
            }
          }
        },
      };
      this.challenge = capturedChallenge; keepForChallenge = true;
      return {api: connected.api, result: challenge};
    } finally {
      if (!adopted && grant?.kind === 'signed-in') await this.retire(connected.api, grant.session);
      if (!keepForChallenge) connected.connection.dispose();
      if (!adopted) bound?.connection.dispose();
    }
  }
  /** The Server challenge stays only in this controller. No exchange replay,
   * Hosted credential or selected-viewer publication is used for this step. */
  async completeChallenge(code: string): Promise<Completion> {
    if (!this.challenge) throw invalid();
    return this.challenge.complete(code);
  }
}

/** The opaque tab locator contains no verifier, account, server or credential. */
export function createBrowserRegisteredSignIn(startURL: Ports['startURL']) {
  const guard = secureBrowserPending<Pending>('portico.registered-sign-in.guard'); guard.assertAvailable();
  const key = 'portico.registered-sign-in.tab.v1'; let tab = sessionStorage.getItem(key);
  if (tab !== null) routeBytes(tab, 32);
  else {tab = routeBase64(crypto.getRandomValues(new Uint8Array(32))); sessionStorage.setItem(key, tab);}
  const journal = secureBrowserPending<Pending>('portico.registered-sign-in.v1:' + tab);
  const installationId = () => {const current = installationIdentity(); if (!current) throw invalid(); return current.installationId;};
  const controller = new BrowserRegisteredSignIn({journal, sessions: browserAppSessions(), environment: browserConnectionEnvironment, startURL, installationId, pageOrigin: () => location.origin});
  return {controller, installationId};
}
