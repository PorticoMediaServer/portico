import {HttpLocalApi, type LocalSession} from '@core/index.ts';
import {type AppSessionRecord, type AppSessionStore, knownServerRoutes} from '@core/app-sessions.ts';
import {parseClaimStatus, type ClaimExpected} from '@core/claim-onboarding.ts';
import {installationIdentity} from '@core/installation.ts';
import {prepareOwnerClaimHandoff, type OwnerClaimHandoff} from '@core/owner-claim-client.ts';
import {verifyOwnerClaimStart} from '@core/owner-claim-start.ts';
import {parseDirectSignIn, type DirectSignIn} from '@core/profile-management.ts';
import {assertFirstContactIdentity, routeBase64, routeBytes, routeOrigin, sameServerPin, type ServerPin} from '@core/route-identity.ts';
import {connectPairedServer, connectPairedPorticoServer, rememberNativeSession, type ConnectionEnvironment} from '@core/server-connections.ts';
import {CodedError} from '@core/server-messages.ts';
import {routePath} from '../../../packages/contracts/src/routes.ts';
import {dispatchPorticoReturn, type CapturedPorticoReturn} from './portico-return-dispatch';
import {secureBrowserPending} from './secure-pending';
import {browserAppSessions, browserConnectionEnvironment} from './server-connections';

type Pending = Readonly<{
  version: 1; kind: 'owner'; attemptId: string; origin: string; hostedOrigin: string; pin: ServerPin;
  expectedRevision: number; familyId: string; tokenGeneration: string; accountId: string; profileId: string;
  installationId: string; state: string; verifier: string; codeChallenge: string; createdAt: number; expiresAt: string;
  /** Absent on old switch records; its presence is the purpose the Server approved. */
  purpose?: 'switch' | 'change-owner'; currentOwnerAccountId?: string; previousOwnerRole?: 'admin' | 'member' | 'remove';
  expected?: ClaimExpected; handoff?: OwnerClaimHandoff;
}>;
export type OwnerClaimCaller = Readonly<{isCurrent: () => boolean; signal?: AbortSignal}>;
/** The exact attempt this preparation owns. Its cancel clears or cancels only
 * that journal/server attempt, never whichever attempt is current later. */
export type OwnerClaimLease = Readonly<{
  attemptId: string; origin: string; operationId?: string; localGeneration?: string;
  cancel: () => Promise<void>;
}>;
export type OwnerClaimAttempt = {lease?: OwnerClaimLease};
type Journal = Readonly<{assertAvailable: () => void; read: () => Promise<Pending | undefined>;
  change: (update: (old: Pending | undefined) => Pending | undefined) => Promise<void>}>;
type Ports = Readonly<{
  journal: Journal; sessions: Pick<AppSessionStore, 'read' | 'connectionStorage'>;
  environment: (serverId: string) => ConnectionEnvironment; installationId: () => string; pageOrigin: () => string;
  now?: () => number; prepare?: typeof prepareOwnerClaimHandoff;
  connect?: typeof connectPairedServer; connectPortico?: typeof connectPairedPorticoServer; remember?: typeof rememberNativeSession;
  retire?: (api: HttpLocalApi, session: LocalSession) => Promise<void>;
}>;
type Completion = Readonly<{api: HttpLocalApi; result: Extract<DirectSignIn, {kind: 'signed-in'}>}>;
const invalid = () => new CodedError('invalid_return', 'This owner continuation is no longer available.');
const cancelled = () => new CodedError('cancelled', 'The selected owner or server changed.');
const same = (a: Pending | undefined, b: Pending) => !!a && JSON.stringify(a) === JSON.stringify(b);
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);

/** Separate owner KIND and encrypted journal. This never restores its narrowed
 * family with /me, renews it, or substitutes an ordinary member sign-in. The
 * caller confirms the existing move first; Server guards own its lifecycle. */
export class BrowserOwnerClaim {
  private epoch = 0;
  private controller?: AbortController;
  private attemptId?: string;
  private busy = false;
  constructor(private readonly ports: Ports) {}
  private now() {return (this.ports.now ?? Date.now)();}
  private operation() {
    if (this.busy) throw new CodedError('move_request_in_progress', 'Wait for this account move request to finish.');
    this.busy = true;
    this.controller?.abort(); const epoch = ++this.epoch, controller = this.controller = new AbortController();
    return {signal: controller.signal, current: () => epoch === this.epoch && !controller.signal.aborted};
  }
  private selected(state: AppSessionRecord, pin: ServerPin, purpose: 'switch' | 'change-owner' = 'switch') {
    const selected = state.servers.find(entry => entry.connection.pin.serverId === state.selectedServerId);
    const direction = !!selected && (purpose === 'switch'
      ? selected.signIn === 'local' && !selected.connection.hosted
      : selected.signIn === 'portico' && !!selected.connection.hosted);
    if (!selected || !direction || !sameServerPin(selected.connection.pin, pin) ||
      selected.connection.session.viewer.authority !== 'local' || selected.connection.session.viewer.role !== 'owner') throw invalid();
    return selected.connection;
  }
  private async current(record: Pending, operation: ReturnType<BrowserOwnerClaim['operation']>, journal = true) {
    this.ports.journal.assertAvailable();
    if (journal && !same(await this.ports.journal.read(), record)) throw cancelled();
    const state = await this.ports.sessions.read();
    if (!operation.current()) throw cancelled();
    return this.captured(record, state);
  }
  private captured(record: Pending, state: AppSessionRecord) {
    const selected = this.selected(state, record.pin, record.purpose ?? 'switch'), session = selected.session;
    if (record.version !== 1 || record.kind !== 'owner' || record.origin !== this.ports.pageOrigin() ||
      record.installationId !== this.ports.installationId() || state.revision !== record.expectedRevision || session.sessionFamilyId !== record.familyId ||
      session.tokenGeneration !== record.tokenGeneration || session.viewer.accountId !== record.accountId || session.viewer.profileId !== record.profileId ||
      !Number.isSafeInteger(record.createdAt) || record.createdAt < 0 || record.createdAt > this.now() + 60_000 || !Number.isFinite(Date.parse(record.expiresAt)) ||
      Date.parse(record.expiresAt) <= this.now() || Date.parse(record.expiresAt) > record.createdAt + 300_000 || Date.parse(session.expiresAt) <= this.now()) throw cancelled();
    return selected;
  }
  /** Forget this browser attempt only. Explicit Server cancellation is a
   * separate command below; installed claims never implicitly roll back. */
  async cancel() {
    ++this.epoch; this.controller?.abort();
    const id = this.attemptId; this.attemptId = undefined;
    if (id) await this.ports.journal.change(old => old?.attemptId === id ? undefined : old);
  }
  async dispose() {await this.cancel();}
  /** Existing guarded claim cancel: before install the Server persists its
   * original-key denial; after install it retires only the continuation. This
   * never performs a signed leave or changes kind on the client's authority. */
  async cancelOperation(api: HttpLocalApi): Promise<void> {
    this.ports.journal.assertAvailable(); const operation = this.operation();
    try {
      const record = await this.ports.journal.read(), routes = api.getRouteConnection();
      if (record?.kind !== 'owner' || !record.expected || api.baseUrl !== record.origin || !routes || !sameServerPin(routes.pin, record.pin)) throw invalid();
      const selected = await this.current(record, operation);
      const captured = new HttpLocalApi(record.origin, selected.session.accessToken); captured.useRoutes(routes);
      const status = parseClaimStatus(await captured.request(routePath('get_networking_claim'), 'GET', undefined, operation.signal), record.pin);
      await this.current(record, operation);
      if (!status.operation || status.operation.operationId !== record.expected.operationId || status.operation.localGeneration !== record.expected.localGeneration || !status.actions.includes('cancel')) throw invalid();
      parseClaimStatus(await captured.request(routePath('post_networking_claim_cancel'), 'POST', {expected: status.operation}, operation.signal), record.pin);
      // Cancellation may advance the Server generation. Do not compare that
      // terminal identity with the old operation or clear a successor journal.
      await this.ports.journal.change(old => same(old, record) ? undefined : old);
      if (this.attemptId === record.attemptId) this.attemptId = undefined;
    } finally {this.busy = false;}
  }
  /** Same-origin private preparation only, after the actual move confirmation.
   * Ambiguous network failures retain the exact verifier/proofs for that attempt. */
  async prepare(api: HttpLocalApi, hostedOrigin: string, purpose: 'switch' | 'change-owner' = 'switch',
    previousOwnerRole: 'admin' | 'member' | 'remove' = 'admin', caller?: OwnerClaimCaller, attempt?: OwnerClaimAttempt): Promise<string> {
    this.ports.journal.assertAvailable();
    const origin = this.ports.pageOrigin(), route = api.getRouteConnection();
    if (api.baseUrl !== origin || routeOrigin(origin) !== origin || !route || route.getSnapshot().phase !== 'ready' ||
      routeOrigin(hostedOrigin) !== hostedOrigin || !hostedOrigin.startsWith('https://')) throw invalid();
    if (purpose === 'change-owner' && previousOwnerRole !== 'admin' && previousOwnerRole !== 'member' && previousOwnerRole !== 'remove') throw invalid();
    const operation = this.operation(), holder = attempt ?? {};
    const callerLive = () => !caller || (caller.isCurrent() && caller.signal?.aborted !== true);
    const signal = caller?.signal ? AbortSignal.any([operation.signal, caller.signal]) : operation.signal;
    try {
      if (!callerLive()) throw invalid();
      const state = await this.ports.sessions.read(), selected = this.selected(state, route.pin, purpose);
      if (!operation.current() || !callerLive()) throw cancelled();
      // Capture the intended Hosted owner and role before the protected pending
      // write or any network IO. A returned prepared intent must match exactly.
      const intendedOwner = purpose === 'change-owner' ? selected.hosted?.accountId : undefined;
      const intendedRole = purpose === 'change-owner' ? previousOwnerRole : undefined;
      if (purpose === 'change-owner' && (!intendedOwner || !/^[A-Za-z0-9_-]{1,128}$/.test(intendedOwner))) throw invalid();
      assertFirstContactIdentity(origin, route.pin, {knownRoutes: knownServerRoutes(state)});
      // Capture exactly the durable selected family, without inheriting a
      // caller API's mutable bearer or generic refresh/retry policy.
      const captured = new HttpLocalApi(origin, selected.session.accessToken); captured.useRoutes(route);
      let pending = await this.ports.journal.read();
      if (!pending) {
        const verifier = routeBase64(crypto.getRandomValues(new Uint8Array(32))), createdAt = this.now();
        const codeChallenge = routeBase64(new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(verifier))));
        pending = {version: 1, kind: 'owner', attemptId: routeBase64(crypto.getRandomValues(new Uint8Array(32))), origin, hostedOrigin, pin: {...route.pin},
          expectedRevision: state.revision, familyId: selected.session.sessionFamilyId, tokenGeneration: selected.session.tokenGeneration,
          accountId: selected.session.viewer.accountId, profileId: selected.session.viewer.profileId, installationId: this.ports.installationId(),
          purpose, ...(purpose === 'change-owner' ? {currentOwnerAccountId: intendedOwner, previousOwnerRole: intendedRole} : {}),
          state: routeBase64(crypto.getRandomValues(new Uint8Array(32))), verifier, codeChallenge, createdAt, expiresAt: new Date(createdAt + 300_000).toISOString()};
        const fresh = pending;
        if (!callerLive()) throw invalid();
        await this.ports.journal.change(old => {if (!operation.current() || !callerLive() || old) throw cancelled(); return fresh;});
      }
      if (pending.origin !== origin || pending.hostedOrigin !== hostedOrigin || !sameServerPin(pending.pin, route.pin) ||
        (pending.purpose ?? 'switch') !== purpose) throw invalid();
      if (purpose === 'change-owner'
        ? pending.currentOwnerAccountId !== intendedOwner || pending.previousOwnerRole !== intendedRole
        : pending.currentOwnerAccountId !== undefined || pending.previousOwnerRole !== undefined) throw invalid();
      this.attemptId = pending.attemptId;
      // Bind cleanup to this exact pending identity before any more IO.
      const bound = pending;
      const lease = (expected: ClaimExpected | undefined): OwnerClaimLease => Object.freeze({
        attemptId: bound.attemptId, origin: bound.origin,
        ...(expected ? {operationId: expected.operationId, localGeneration: expected.localGeneration} : {}),
        cancel: () => this.cancelAttempt(bound.attemptId, expected, api),
      });
      holder.lease = lease(bound.expected);
      if (!callerLive()) throw invalid();
      await this.current(pending, operation);
      if (!callerLive()) throw invalid();
      if (!pending.expected) {
        let status = parseClaimStatus(await captured.request(routePath('get_networking_claim'), 'GET', undefined, signal), pending.pin);
        await this.current(pending, operation);
        if (!callerLive()) throw invalid();
        // A portico-account server already has an installed claim, so its snapshot
        // has no 'prepare' action; the explicit change-owner purpose still posts.
        const already = status.state === 'prepared' && status.approvalRequest?.purpose === purpose;
        if (!already && (purpose === 'change-owner' || status.actions.includes('prepare'))) {
          status = parseClaimStatus(await captured.request(routePath('post_networking_claim_prepare'), 'POST',
            {purpose, ...(purpose === 'change-owner' ? {previousOwnerRole: intendedRole} : {}), expected: status.identity}, signal), pending.pin);
          await this.current(pending, operation);
          if (!callerLive()) throw invalid();
        }
        const request = status.approvalRequest;
        const prepared = purpose === 'switch'
          ? request?.purpose === 'switch' && request.currentOwnerAccountId === '' && request.previousOwnerRole === '' && request.returnRegistrationId === ''
          : request?.purpose === 'change-owner' && request.currentOwnerAccountId === intendedOwner &&
            request.previousOwnerRole === intendedRole && request.returnRegistrationId === '';
        if (status.state !== 'prepared' || !status.operation || !prepared) throw invalid();
        const old = pending, updated = {...pending, expected: {...status.operation}};
        if (!callerLive()) throw invalid();
        await this.ports.journal.change(current => {if (!operation.current() || !callerLive() || !same(current, old)) throw cancelled(); return updated;});
        pending = updated;
        holder.lease = lease(updated.expected);
      }
      const binding = {pin: pending.pin, origin, hostedOrigin, operationId: pending.expected!.operationId, localGeneration: pending.expected!.localGeneration,
        state: pending.state, codeChallenge: pending.codeChallenge, installationId: pending.installationId,
        purpose: pending.purpose ?? 'switch',
        ...(pending.purpose === 'change-owner' ? {currentOwnerAccountId: pending.currentOwnerAccountId, previousOwnerRole: pending.previousOwnerRole} : {})};
      const url = await (this.ports.prepare ?? prepareOwnerClaimHandoff)(captured, pending.expected!, binding, {
        assertPendingCurrent: async () => {if (!callerLive()) throw invalid(); await this.current(pending!, operation); if (!callerLive()) throw invalid();},
        bindPending: async handoff => {
          if (!callerLive()) throw invalid();
          await this.current(pending!, operation);
          if (!callerLive()) throw invalid();
          // The Server challenge begins after our private journal was written.
          // Keep the earlier client deadline; never extend it to the later
          // signed expiry or change the producer's exact signed bytes.
          const old = pending!, updated = {...old, handoff, expiresAt: new Date(Math.min(Date.parse(old.expiresAt), Date.parse(handoff.expiresAt))).toISOString()};
          await this.ports.journal.change(current => {if (!operation.current() || !callerLive() || !same(current, old)) throw cancelled(); return updated;});
          pending = updated;
        },
      }, {signal, now: () => this.now()});
      if (!callerLive()) throw invalid();
      return url;
    } catch (error) {
      // A retired caller leaves no delivered handoff behind. Cleanup is scoped
      // to the exact attempt this call bound, never a later successor.
      if (!callerLive()) await holder.lease?.cancel().catch(() => {});
      throw error;
    } finally {this.busy = false;}
  }
  /** Cancel only the exact journal attempt this lease names. If a successor
   * already replaced it, do nothing at all. The Server cancellation uses the
   * original issuer route and only while the captured owner family is still
   * selected; otherwise the exact local attempt is dropped and the Server
   * intent expires. */
  private async cancelAttempt(attemptId: string, expected: ClaimExpected | undefined, api: HttpLocalApi): Promise<void> {
    const record = await this.ports.journal.read();
    if (!record || record.attemptId !== attemptId) return;
    if (expected && (!record.expected || record.expected.operationId !== expected.operationId ||
      record.expected.localGeneration !== expected.localGeneration)) return;
    try {
      const state = await this.ports.sessions.read();
      const selected = this.selected(state, record.pin, record.purpose ?? 'switch');
      const route = api.getRouteConnection();
      if (record.expected && route && sameServerPin(route.pin, record.pin) && selected.session.sessionFamilyId === record.familyId &&
        selected.session.tokenGeneration === record.tokenGeneration && state.revision === record.expectedRevision) {
        const captured = new HttpLocalApi(record.origin, selected.session.accessToken); captured.useRoutes(route);
        const status = parseClaimStatus(await captured.request(routePath('get_networking_claim'), 'GET', undefined, AbortSignal.timeout(5000)), record.pin);
        if (status.operation && status.operation.operationId === record.expected.operationId &&
          status.operation.localGeneration === record.expected.localGeneration && status.actions.includes('cancel')) {
          parseClaimStatus(await captured.request(routePath('post_networking_claim_cancel'), 'POST', {expected: status.operation}, AbortSignal.timeout(5000)), record.pin);
        }
      }
    } catch { /* the Server attempt expires; the exact local attempt is still cleared */ }
    await this.ports.journal.change(old => old?.attemptId === attemptId ? undefined : old);
  }
  /** Only protected KIND selects this consumer. The URL cannot select owner
   * authority and supplies no state, code, registration or credentials. */
  async pendingReturn() {
    this.ports.journal.assertAvailable(); const record = await this.ports.journal.read();
    if (!record || record.kind !== 'owner' || !record.handoff) return;
    const state = await this.ports.sessions.read();
    try {this.captured(record, state);} catch {
      await this.ports.journal.change(old => same(old, record) ? undefined : old); return;
    }
    this.attemptId = record.attemptId;
    return {kind: 'owner' as const, transactionId: record.handoff.transactionId};
  }
  private async retire(api: HttpLocalApi, session: LocalSession) {
    if (this.ports.retire) return this.ports.retire(api, session);
    const temporary = new HttpLocalApi(api.baseUrl, session.accessToken), routes = api.getRouteConnection();
    if (routes) temporary.useRoutes(routes);
    await temporary.request(routePath('delete_sessions_current'), 'DELETE', undefined, AbortSignal.timeout(5000)).catch(() => {});
  }
  /** One-shot exchange: lost response is terminal. Recovery is fresh ordinary
   * admission to the already-bound owner, never another claim or owner grant. */
  async finish(captured: CapturedPorticoReturn): Promise<Completion> {
    this.ports.journal.assertAvailable(); const operation = this.operation();
    let connected: Awaited<ReturnType<typeof connectPairedServer>> | undefined, bound: Awaited<ReturnType<typeof connectPairedPorticoServer>> | undefined;
    let grant: DirectSignIn | undefined, adopted = false, admissionServer: string | undefined;
    try {
      const record = await this.ports.journal.read();
      if (record?.version !== 1 || record.kind !== 'owner' || !record.handoff || !record.expected) throw invalid();
      const handoff = record.handoff, pending = record, purpose = record.purpose ?? 'switch';
      const callback = dispatchPorticoReturn(captured, {kind: 'owner', transactionId: handoff.transactionId});
      if (new URL(callback.href).origin !== record.origin || record.origin !== this.ports.pageOrigin()) throw invalid();
      // A valid owner-only callback consumes this attempt before any proof,
      // network or admission await. Malformed/mixed callbacks above preserve
      // the legitimate pending; a successor can never be consumed here.
      await this.ports.journal.change(old => {if (!operation.current() || !same(old, pending)) throw cancelled(); return undefined;});
      this.attemptId = undefined;
      const current = () => this.current(record, operation, false);
      const selected = await current();
      const expected = handoff.binding;
      if (expected.origin !== record.origin || expected.hostedOrigin !== record.hostedOrigin || !sameServerPin(expected.pin, record.pin) || expected.state !== record.state ||
        expected.installationId !== record.installationId || expected.codeChallenge !== record.codeChallenge || expected.operationId !== record.expected.operationId ||
        expected.localGeneration !== record.expected.localGeneration || JSON.stringify(handoff.expected) !== JSON.stringify(record.expected) || Date.parse(record.expiresAt) > Date.parse(handoff.expiresAt) ||
        (expected.purpose ?? 'switch') !== purpose || expected.currentOwnerAccountId !== record.currentOwnerAccountId ||
        expected.previousOwnerRole !== record.previousOwnerRole) throw invalid();
      const proof = await verifyOwnerClaimStart(handoff.claim, handoff.start, expected, this.now());
      const digest = routeBase64(new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(record.verifier))));
      routeBytes(record.verifier, 32);
      if (digest !== proof.start.codeChallenge || handoff.expiresAt !== proof.start.expiresAt) throw invalid();
      await current();
      const environment = this.ports.environment(record.pin.serverId);
      connected = await (this.ports.connect ?? connectPairedServer)(record.origin, record.pin, environment, operation.signal);
      await current(); connected.api.setAccessToken(selected.session.accessToken);
      const raw = await connected.api.request<unknown>(routePath('get_networking_claim'), 'GET', undefined, operation.signal);
      await current();
      const status = parseClaimStatus(raw, record.pin), owner = object(raw) ? raw.ownerContinuation : undefined;
      if (status.state !== 'installed' || status.identity.localGeneration !== record.expected.localGeneration || status.operation?.operationId !== record.expected.operationId ||
        status.operation.localGeneration !== record.expected.localGeneration || BigInt(status.operation.revision) < BigInt(record.expected.revision) || !object(owner) || owner.phase !== 'installed' || owner.transactionId !== handoff.transactionId ||
        Object.keys(owner).sort().join(',') !== 'expiresAt,phase,returnRegistration,transactionId' || typeof owner.expiresAt !== 'string' || Date.parse(owner.expiresAt) !== Date.parse(handoff.expiresAt) ||
        !object(owner.returnRegistration) || Object.keys(owner.returnRegistration).sort().join(',') !== 'id,origin' || typeof owner.returnRegistration.id !== 'string' ||
        !/^ret_[A-Za-z0-9_-]{43}$/.test(owner.returnRegistration.id) || typeof owner.returnRegistration.origin !== 'string' ||
        !owner.returnRegistration.origin.startsWith('https://') || routeOrigin(owner.returnRegistration.origin) !== owner.returnRegistration.origin) throw invalid();
      grant = parseDirectSignIn(await connected.api.request(routePath('post_auth_portico_return_exchange'), 'POST', {transactionId: handoff.transactionId, state: record.state,
        verifier: record.verifier, registrationId: owner.returnRegistration.id, installationId: record.installationId, serverChallenge: proof.start.serverChallenge, purpose}, operation.signal));
      await current();
      if (grant.kind !== 'signed-in' || grant.snapshot.serverId !== record.pin.serverId || grant.snapshot.account.role !== 'owner' ||
        grant.snapshot.account.disabled || grant.resetFileOwnerSession || grant.passwordChangeRequired || grant.snapshot.twoStepSetupRequired ||
        grant.session.sessionFamilyId === record.familyId) throw invalid();
      const accountId = grant.snapshot.account.hostedAccountId, sameOwner = purpose === 'switch';
      // A switch keeps the same local account/profile; change-owner creates the new
      // owner's account and links the different Hosted account that approved.
      if (!accountId || (sameOwner
        ? grant.snapshot.account.id !== record.accountId || grant.snapshot.account.primaryProfileId !== record.profileId
        : grant.snapshot.account.id === record.accountId || grant.snapshot.account.primaryProfileId === record.profileId || accountId === record.currentOwnerAccountId)) throw invalid();
      bound = await (this.ports.connectPortico ?? connectPairedPorticoServer)(record.origin, record.pin,
        {origin: record.hostedOrigin, accountId, request: async () => {throw invalid();}}, environment, operation.signal);
      await current(); bound.api.setAccessToken(grant.session.accessToken);
      const admissionCurrent = () => operation.current() && this.now() < Date.parse(record.expiresAt) && record.origin === this.ports.pageOrigin() && record.installationId === this.ports.installationId();
      admissionServer = record.pin.serverId;
      await (this.ports.remember ?? rememberNativeSession)(bound.api, grant.session,
        {...environment, storage: this.ports.sessions.connectionStorage(record.pin.serverId, {signIn: 'portico', hostedAccount: bound.hosted, expectedRevision: record.expectedRevision})}, bound.hosted, admissionCurrent);
      const stored = await this.ports.sessions.read(), entry = stored.servers.find(value => value.connection.pin.serverId === stored.selectedServerId);
      if (!admissionCurrent() || !entry || entry.signIn !== 'portico' || !sameServerPin(entry.connection.pin, record.pin) || entry.connection.session.sessionFamilyId !== grant.session.sessionFamilyId ||
        entry.connection.session.viewer.accountId !== grant.snapshot.account.id || entry.connection.session.viewer.profileId !== grant.snapshot.account.primaryProfileId ||
        entry.connection.hosted?.origin !== record.hostedOrigin || entry.connection.hosted.accountId !== accountId) throw cancelled();
      adopted = true; return {api: bound.api, result: grant};
    } finally {
      this.busy = false;
      try {
        if (!adopted && grant?.kind === 'signed-in') {
          const family = grant.session.sessionFamilyId;
          if (admissionServer) await this.ports.sessions.connectionStorage(admissionServer).change(old => old?.session.sessionFamilyId === family ? undefined : old).catch(() => {});
          if (connected) await this.retire(connected.api, grant.session);
        }
      }
      finally {connected?.connection.dispose(); if (!adopted) bound?.connection.dispose();}
    }
  }
}

/** No tab locator, DB or private pending is touched before secure prerequisites. */
export function createBrowserOwnerClaim() {
  const guard = secureBrowserPending<Pending>('portico.owner-claim.guard'); guard.assertAvailable();
  const key = 'portico.owner-claim.tab.v1'; let tab = sessionStorage.getItem(key);
  if (tab !== null) routeBytes(tab, 32);
  else {tab = routeBase64(crypto.getRandomValues(new Uint8Array(32))); sessionStorage.setItem(key, tab);}
  return new BrowserOwnerClaim({journal: secureBrowserPending<Pending>('portico.owner-claim.v1:' + tab), sessions: browserAppSessions(), environment: browserConnectionEnvironment,
    installationId: () => {const value = installationIdentity(); if (!value) throw invalid(); return value.installationId;}, pageOrigin: () => location.origin});
}
