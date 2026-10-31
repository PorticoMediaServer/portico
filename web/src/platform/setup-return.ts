import {HttpLocalApi, type LocalSession} from '@core/index.ts';
import type {AppSessionStore} from '@core/app-sessions.ts';
import {parseDirectSignIn, type DirectSignIn} from '@core/profile-management.ts';
import {sameServerPin, webRouteCrypto, type RouteCrypto} from '@core/route-identity.ts';
import {connectPairedPorticoServer, connectPairedServer, rememberNativeSession, type ConnectionEnvironment} from '@core/server-connections.ts';
import {CodedError} from '@core/server-messages.ts';
import {setupAttemptBound, setupAttemptProofMatches, setupOwnerGrantFor} from '@core/setup-custody.ts';
import {verifySetupClaimStart} from '@core/setup-claim-start.ts';
import {readSetupState} from '@core/setup-state.ts';
import {isUnreadable, parseSetupAttempt, type SetupStore} from '@core/setup-store.ts';
import {installationIdentity} from '@core/installation.ts';
import {routePath} from '../../../packages/contracts/src/routes.ts';
import {dispatchPorticoReturn, type CapturedPorticoReturn} from './portico-return-dispatch';
import {browserAppSessions, browserConnectionEnvironment} from './server-connections';
import {browserAttemptSlot, browserSetupStore} from './setup-store';

type Slot = ReturnType<typeof browserAttemptSlot>;
type Ports = Readonly<{
  attempt: Slot; store: (serverId: string) => SetupStore; sessions: Pick<AppSessionStore, 'read' | 'connectionStorage'>;
  environment: (serverId: string) => ConnectionEnvironment; installationId: () => string | undefined; pageOrigin: () => string;
  now?: () => number; crypto?: RouteCrypto;
  connect?: typeof connectPairedServer; connectPortico?: typeof connectPairedPorticoServer; remember?: typeof rememberNativeSession;
  retire?: (api: HttpLocalApi, session: LocalSession) => Promise<void>;
}>;
type Completion = Readonly<{api: HttpLocalApi; result: Extract<DirectSignIn, {kind: 'signed-in'}>}>;
const invalid = () => new CodedError('invalid_return', 'This setup continuation is no longer available.');
const cancelled = () => new CodedError('cancelled', 'The pending setup or server selection changed.');

/** Portico Services' Return for a Portico Account setup, in the tab that started it (B-DESIGN §6.2, web). Account entry
 * chooses it only when this tab holds a bound attempt; the callback must name that attempt's transaction. The attempt
 * is consumed before any network I/O, so a callback can be used once; a lost completion is final, and ordinary
 * sign-in then admits the installed owner. */
export class BrowserSetupReturn {
  private epoch = 0;
  constructor(private readonly ports: Ports) {}
  private now() {return (this.ports.now ?? Date.now)();}
  private async readAttempt() {
    const raw = await this.ports.attempt.read().catch(() => undefined);
    const attempt = raw === undefined ? undefined : parseSetupAttempt(raw);
    return attempt && !isUnreadable(attempt) ? attempt : undefined;
  }
  /** A read-only probe: never creates or consumes an attempt. */
  async pendingReturn() {
    this.ports.attempt.assertAvailable();
    const attempt = await this.readAttempt();
    if (!attempt?.handoff || attempt.origin !== this.ports.pageOrigin() || Date.parse(attempt.expiresAt) <= this.now()) return;
    return {kind: 'setup' as const, transactionId: attempt.handoff.transactionId};
  }
  /** This page no longer waits on a Return; the attempt stays for the setup page to read. */
  async cancel() {++this.epoch;}
  async finish(captured: CapturedPorticoReturn): Promise<Completion> {
    this.ports.attempt.assertAvailable();
    const epoch = ++this.epoch, controller = new AbortController(), signal = controller.signal;
    const current = () => {if (epoch !== this.epoch || signal.aborted) throw cancelled();};
    let connected: Awaited<ReturnType<typeof connectPairedServer>> | undefined, bound: Awaited<ReturnType<typeof connectPairedPorticoServer>> | undefined;
    let grant: DirectSignIn | undefined, adopted = false, admissionServer: string | undefined;
    try {
      const attempt = await this.readAttempt();
      if (!attempt?.handoff) throw invalid();
      const callback = dispatchPorticoReturn(captured, {kind: 'setup', transactionId: attempt.handoff.transactionId});
      const origin = this.ports.pageOrigin(), installation = this.ports.installationId();
      if (new URL(callback.href).origin !== attempt.origin || attempt.origin !== origin || installation !== attempt.installationId || Date.parse(attempt.expiresAt) <= this.now()) throw invalid();
      const store = this.ports.store(attempt.serverId), local = await store.read();
      current();
      const record = local.record;
      if (!record || !setupAttemptBound(attempt, record) || record.authMode !== 'hosted') throw invalid();
      // Spent before any network I/O: this callback can never be replayed.
      if (!await store.change({attemptId: attempt.attemptId}, {attempt: null})) throw cancelled();
      current();
      const selection = await this.ports.sessions.read();
      current();
      const proof = await verifySetupClaimStart(attempt.handoff.claim, attempt.handoff.start, attempt.handoff.binding, this.now());
      if (!await setupAttemptProofMatches(attempt, proof, this.ports.crypto ?? webRouteCrypto)) throw invalid();
      current();
      const pin = attempt.handoff.binding.pin, environment = this.ports.environment(attempt.serverId);
      connected = await (this.ports.connect ?? connectPairedServer)(attempt.origin, pin, environment, signal);
      current();
      connected.api.setAccessToken(record.setupToken);
      // Only this setup, installed, completes admission.
      const state = await readSetupState(connected.api, signal);
      current();
      if (typeof state.setup !== 'object' || state.setup.phase !== 'installed' || state.setup.requestId !== record.requestId) throw invalid();
      grant = parseDirectSignIn(await connected.api.request(routePath('post_auth_portico_return_exchange'), 'POST', {transactionId: attempt.handoff.transactionId, state: attempt.state,
        verifier: attempt.verifier, installationId: attempt.installationId, serverChallenge: proof.start.serverChallenge, purpose: 'setup'}, signal));
      current();
      if (!setupOwnerGrantFor(grant, attempt, this.now())) throw invalid();
      const accountId = grant.snapshot.account.hostedAccountId!;
      bound = await (this.ports.connectPortico ?? connectPairedPorticoServer)(attempt.origin, pin,
        {origin: attempt.hostedOrigin, accountId, request: async () => {throw invalid();}}, environment, signal);
      current();
      bound.api.setAccessToken(grant.session.accessToken);
      const admissionCurrent = () => epoch === this.epoch && !signal.aborted && attempt.origin === this.ports.pageOrigin() && attempt.installationId === this.ports.installationId();
      admissionServer = attempt.serverId;
      await (this.ports.remember ?? rememberNativeSession)(bound.api, grant.session,
        {...environment, storage: this.ports.sessions.connectionStorage(attempt.serverId, {signIn: 'portico', hostedAccount: bound.hosted, expectedRevision: selection.revision})}, bound.hosted, admissionCurrent);
      const stored = await this.ports.sessions.read(), entry = stored.servers.find(value => value.connection.pin.serverId === stored.selectedServerId);
      if (!admissionCurrent() || !entry || entry.signIn !== 'portico' || !sameServerPin(entry.connection.pin, pin) || entry.connection.session.sessionFamilyId !== grant.session.sessionFamilyId ||
        entry.connection.session.viewer.accountId !== grant.snapshot.account.id || entry.connection.hosted?.origin !== attempt.hostedOrigin || entry.connection.hosted.accountId !== accountId) throw cancelled();
      // Admitted: the setup record has done its work.
      await store.change({requestId: record.requestId}, {record: null});
      adopted = true;
      return {api: bound.api, result: grant};
    } finally {
      try {
        if (!adopted && grant?.kind === 'signed-in') {
          const family = grant.session.sessionFamilyId;
          if (admissionServer) await this.ports.sessions.connectionStorage(admissionServer).change(old => old?.session.sessionFamilyId === family ? undefined : old).catch(() => {});
          if (connected) await this.retire(connected.api, grant.session);
        }
      } finally {connected?.connection.dispose(); if (!adopted) bound?.connection.dispose();}
    }
  }
  private async retire(api: HttpLocalApi, session: LocalSession) {
    if (this.ports.retire) return this.ports.retire(api, session);
    const temporary = new HttpLocalApi(api.baseUrl, session.accessToken), routes = api.getRouteConnection();
    if (routes) temporary.useRoutes(routes);
    await temporary.request(routePath('delete_sessions_current'), 'DELETE', undefined, AbortSignal.timeout(5000)).catch(() => {});
  }
}

export function createBrowserSetupReturn() {
  return new BrowserSetupReturn({attempt: browserAttemptSlot(), store: browserSetupStore, sessions: browserAppSessions(), environment: browserConnectionEnvironment,
    installationId: () => installationIdentity()?.installationId, pageOrigin: () => location.origin});
}
