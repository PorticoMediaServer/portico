import {HttpLocalApi, type LocalSession} from '@core/index.ts';
import {type AppSessionStore, knownServerRoutes} from '@core/app-sessions.ts';
import {parseClaimStatus, type ClaimStatus} from '@core/claim-onboarding.ts';
import {installationIdentity} from '@core/installation.ts';
import {prepareSetupClaimHandoff} from '@core/setup-claim-client.ts';
import {verifySetupClaimStart} from '@core/setup-claim-start.ts';
import {createSetupAttempt, preparedSetupOperation, setupAttemptLive, setupContinuation, setupHandoffBound, setupOwnerGrantValid, setupProofMatches, type SetupAttempt} from '@core/setup-claim-attempt.ts';
import {resumeServerSetup, type PendingAccountSetup, type SetupDraft, type SetupDraftStore} from '@core/setup-onboarding.ts';
import {parseDirectSignIn, type DirectSignIn} from '@core/profile-management.ts';
import {assertFirstContactIdentity, routeBase64, routeBytes, routeOrigin, sameServerPin, webRouteCrypto, type ServerPin} from '@core/route-identity.ts';
import {connectPairedServer, connectPairedPorticoServer, rememberNativeSession, type ConnectionEnvironment} from '@core/server-connections.ts';
import {CodedError} from '@core/server-messages.ts';
import {routePath} from '../../../packages/contracts/src/routes.ts';
import {dispatchPorticoReturn, type CapturedPorticoReturn} from './portico-return-dispatch';
import {setupBrowserPending} from './setup-pending';
import {BrowserProtectedStore, browserAppSessions, browserConnectionEnvironment} from './server-connections';

/** Reuse the original setup draft namespace. Passwords are never stored here. */
export const browserSetupDraft = (serverId: string): SetupDraftStore => new BrowserProtectedStore<SetupDraft>('portico.initialize.' + serverId);
type Pending = SetupAttempt;
type Journal = Readonly<{assertAvailable: () => void; read: () => Promise<Pending | undefined>;
  change: (update: (old: Pending | undefined) => Pending | undefined) => Promise<void>}>;
type Ports = Readonly<{
  journal: Journal; draft: (serverId: string) => SetupDraftStore; sessions: Pick<AppSessionStore, 'read' | 'connectionStorage'>;
  environment: (serverId: string) => ConnectionEnvironment; installationId: () => string; pageOrigin: () => string;
  now?: () => number; prepare?: typeof prepareSetupClaimHandoff;
  connect?: typeof connectPairedServer; connectPortico?: typeof connectPairedPorticoServer; remember?: typeof rememberNativeSession;
  retire?: (api: HttpLocalApi, session: LocalSession) => Promise<void>;
}>;
type Completion = Readonly<{api: HttpLocalApi; result: Extract<DirectSignIn, {kind: 'signed-in'}>}>;
const invalid = () => new CodedError('invalid_return', 'This setup continuation is no longer available.');
const cancelled = () => new CodedError('cancelled', 'The pending setup or server selection changed.');
const same = (a: Pending | undefined, b: Pending) => !!a && JSON.stringify(a) === JSON.stringify(b);
export type SetupClaimRecovery = Readonly<{retry: () => Promise<string>; useLocal: () => Promise<void>}>;
export type PrepareOpening = Readonly<{setup: PendingAccountSetup; signal: AbortSignal; assertOriginal: () => Promise<void>; isCurrent: () => boolean;
  capture: (receipt: SetupClaimRecovery) => void}>;
type RecoveryReceipt = Readonly<{origin: string; pin: ServerPin; installationId: string; selectionRevision: number;
  draft: Readonly<SetupDraft>; setupRevision: string; pending?: Pending; identity?: Readonly<ClaimStatus['identity']>}>;
const copyPending = (value: Pending): Pending => Object.freeze({...value, pin: Object.freeze({...value.pin}),
  ...(value.expected ? {expected: Object.freeze({...value.expected})} : {}),
  ...(value.handoff ? {handoff: Object.freeze({...value.handoff,
    expected: Object.freeze({...value.handoff.expected}),
    binding: Object.freeze({...value.handoff.binding, pin: Object.freeze({...value.handoff.binding.pin})}),
    claim: Object.freeze({...value.handoff.claim}), start: Object.freeze({...value.handoff.start}),
  })} : {})});
const equal = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);
const absentClaim = (status: ClaimStatus) => status.state === 'unconfigured' && !status.operation && !status.accountId &&
  !status.installationAcknowledged && !status.approvalRequired && !status.approvalRequest && status.actions.length === 1 && status.actions[0] === 'prepare';

/** Initial setup has no Local owner or authorization family. Its only authority
 * is the original protected setup request/token and Server-supplied revision. */
export class BrowserSetupClaim {
  private epoch = 0;
  private controller?: AbortController;
  private attemptId?: string;
  private busy = false;
  constructor(private readonly ports: Ports) {}
  private now() {return (this.ports.now ?? Date.now)();}
  private operation() {
    if (this.busy) throw new CodedError('move_request_in_progress', 'Wait for this setup request to finish.');
    this.busy = true;
    this.controller?.abort(); const epoch = ++this.epoch, controller = this.controller = new AbortController();
    return {signal: controller.signal, current: () => epoch === this.epoch && !controller.signal.aborted};
  }
  private async current(record: Pending, operation: ReturnType<BrowserSetupClaim['operation']>, journal = true, expired = false) {
    this.ports.journal.assertAvailable();
    if (journal && !same(await this.ports.journal.read(), record)) throw cancelled();
    if (!operation.current()) throw cancelled();
    const draft = await this.ports.draft(record.pin.serverId).read();
    if (!operation.current()) throw cancelled();
    const selection = await this.ports.sessions.read();
    // `expired` keeps every binding check but reads the window at creation: a record a slow
    // installation outlived is still this page's own, to be renewed or restarted, never used.
    if (!operation.current() || !draft || !setupAttemptLive(record, {draft, selectionRevision: selection.revision, origin: this.ports.pageOrigin(),
      installationId: this.ports.installationId(), now: expired ? record.createdAt : this.now()})) throw cancelled();
    return draft;
  }
  private expired(record: Pending) {return Date.parse(record.expiresAt) <= this.now();}
  /** Retire only this browser attempt. It cannot undo an installed claim. */
  async cancel() {
    ++this.epoch; this.controller?.abort();
    const id = this.attemptId; this.attemptId = undefined;
    if (id) await this.ports.journal.change(old => old?.attemptId === id ? undefined : old);
  }
  async dispose() {await this.cancel();}
  /** A saved Portico Account setup whose attempt expired and was never approved is dropped (its
   * prepared operation cancelled, the draft and journal cleared) so setup starts cleanly. Anything
   * that might have been approved, a live attempt, or a read that fails leaves everything as it is. */
  async discardStale(api: HttpLocalApi): Promise<boolean> {
    this.ports.journal.assertAvailable();
    const route = api.getRouteConnection(), record = await this.ports.journal.read();
    // The attempt record belongs to the tab that started it; another tab of the same browser
    // has none. Then the draft's own setup secret is the authority: the server shows that
    // pending setup's claim, and only it, to that secret.
    if (!route || record && (record.kind !== 'setup' || record.pin.serverId !== route.pin.serverId)) return false;
    const store = this.ports.draft(route.pin.serverId), draft = await store.read();
    if (!draft || draft.version !== 1 || draft.authMode !== 'hosted' || draft.serverId !== route.pin.serverId || record && draft.requestId !== record.setupRequestId) return false;
    if (this.busy) return false;
    const owned = this.operation();
    try {
      const captured = new HttpLocalApi(api.baseUrl, draft.setupToken); captured.useRoutes(route);
      const raw = await captured.request<unknown>(routePath('get_networking_claim'), 'GET', undefined, owned.signal);
      const status = parseClaimStatus(raw, route.pin);
      if (!owned.current() || status.state === 'approved' || status.state === 'finalizing' || status.state === 'retrieving' || status.state === 'installed') return false;
      // Expired by this attempt's own record, or by the server's own setup start (whose clock
      // decides): a never-approved start past its window can't finish, whatever this tab thinks.
      const continuation = raw && typeof raw === 'object' && 'setupContinuation' in raw ? (raw as {setupContinuation?: {phase?: unknown; expiresAt?: unknown}}).setupContinuation : undefined;
      const lapsed = continuation?.phase === 'prepared' && typeof continuation.expiresAt === 'string' && Date.parse(continuation.expiresAt) <= this.now();
      if (!(record && this.expired(record)) && !lapsed) return false;
      const mine = !!status.operation && (!record ? lapsed : !!record.expected && status.operation.operationId === record.expected.operationId && status.operation.localGeneration === record.expected.localGeneration);
      if (status.operation && !mine) return false;
      if (mine && status.state === 'prepared') {
        if (!status.actions.includes('cancel')) return false;
        if (!absentClaim(parseClaimStatus(await captured.request(routePath('post_networking_claim_cancel'), 'POST', {expected: status.operation}, owned.signal), route.pin))) return false;
      }
      if (!owned.current()) return false;
      if (record) await this.ports.journal.change(old => same(old, record) ? undefined : old);
      await store.change(old => old?.requestId === draft.requestId && old.setupToken === draft.setupToken ? undefined : old);
      return true;
    } catch {return false;} finally {this.busy = false;}
  }
  async prepare(api: HttpLocalApi, hostedOrigin: string, opening?: PrepareOpening): Promise<string> {
    return this.prepareOriginal(api, hostedOrigin, opening);
  }
  private async prepareOriginal(api: HttpLocalApi, hostedOrigin: string, opening?: PrepareOpening, original?: RecoveryReceipt): Promise<string> {
    this.ports.journal.assertAvailable();
    const origin = this.ports.pageOrigin(), route = api.getRouteConnection();
    if (api.baseUrl !== origin || routeOrigin(origin) !== origin || !route || route.getSnapshot().phase !== 'ready' ||
      routeOrigin(hostedOrigin) !== hostedOrigin || !hostedOrigin.startsWith('https://')) throw invalid();
    const owned = this.operation();
    const signal = opening ? AbortSignal.any([owned.signal, opening.signal]) : owned.signal;
    const operation = {signal, current: () => owned.current() && !signal.aborted && (!opening || opening.isCurrent())};
    const checkOpening = async () => {if (!operation.current()) throw cancelled(); await opening?.assertOriginal(); if (!operation.current()) throw cancelled();};
    try {
      await checkOpening();
      if (original) await this.assertRecovery(api, original, operation, opening, true);
      const draft = await this.ports.draft(route.pin.serverId).read();
      await checkOpening();
      const selection = await this.ports.sessions.read();
      await checkOpening();
      if (!operation.current() || !draft || draft.version !== 1 || draft.serverId !== route.pin.serverId || draft.authMode !== 'hosted' || !draft.setupToken) throw invalid();
      assertFirstContactIdentity(origin, route.pin, {knownRoutes: knownServerRoutes(selection)});
      const captured = new HttpLocalApi(origin, draft.setupToken); captured.useRoutes(route);
      const resumed = await resumeServerSetup(captured, this.ports.draft(route.pin.serverId), operation.signal);
      await checkOpening();
      if (!('claimRequired' in resumed) || resumed.requestId !== draft.requestId || opening &&
        (resumed.serverId !== opening.setup.serverId || resumed.requestId !== opening.setup.requestId || resumed.revision !== opening.setup.revision)) throw invalid();
      let pending = await this.ports.journal.read();
      await checkOpening();
      if (original && !equal(pending, original.pending)) throw cancelled();
      let identity: Readonly<ClaimStatus['identity']> | undefined = original?.identity;
      const captureRecovery = () => {
        if (!opening) return;
        const receipt: RecoveryReceipt = Object.freeze({origin, pin: Object.freeze({...route.pin}), installationId: this.ports.installationId(),
          selectionRevision: selection.revision, draft: Object.freeze({...draft}), setupRevision: resumed.revision,
          ...(pending ? {pending: copyPending(pending)} : {}), ...(identity ? {identity: Object.freeze({...identity})} : {})});
        opening.capture(Object.freeze({retry: () => this.prepareOriginal(api, hostedOrigin, opening, receipt),
          useLocal: () => this.recoverOriginalLocal(api, receipt, opening)}));
      };
      if (pending && (pending.origin !== origin || pending.hostedOrigin !== hostedOrigin || !sameServerPin(pending.pin, route.pin) ||
        pending.setupRequestId !== resumed.requestId || pending.setupRevision !== resumed.revision)) throw invalid();
      captureRecovery();
      // A slow installation (a certificate issued after several attempts) can outlive the
      // five-minute attempt. Its own expired record, or none at all, then meets the claim as
      // it now is: installed for this setup → renew its start with a fresh attempt (no second
      // approval; owner admission is unchanged); never approved → cancel this record's own
      // operation and start cleanly; still installing → say so. Anything else is as before.
      let renewal = false;
      if (!pending || this.expired(pending)) {
        if (pending) await this.current(pending, operation, true, true);
        await checkOpening();
        const raw = await captured.request<unknown>(routePath('get_networking_claim'), 'GET', undefined, operation.signal);
        await checkOpening();
        const status = parseClaimStatus(raw, route.pin), now = this.now();
        const continuation = raw && typeof raw === 'object' && 'setupContinuation' in raw ? (raw as {setupContinuation?: {phase?: unknown; expiresAt?: unknown}}).setupContinuation : undefined;
        const lapsed = !!continuation && (continuation.phase === 'prepared' || continuation.phase === 'installed') && typeof continuation.expiresAt === 'string' && Date.parse(continuation.expiresAt) <= now;
        const mine = !pending?.expected || !!status.operation && status.operation.operationId === pending.expected.operationId && status.operation.localGeneration === pending.expected.localGeneration;
        if (status.state === 'installed' && status.installationAcknowledged && status.operation && lapsed && mine) renewal = true;
        else if (pending?.expected && status.state === 'prepared' && mine && status.actions.includes('cancel')) {
          // Never approved: retire only the operation this record itself captured.
          const result = parseClaimStatus(await captured.request(routePath('post_networking_claim_cancel'), 'POST', {expected: status.operation}, operation.signal), route.pin);
          await checkOpening();
          if (!absentClaim(result)) throw invalid();
        } else if (status.operation && mine && (['approved', 'finalizing', 'retrieving'].includes(status.state) || status.state === 'installed' && !status.installationAcknowledged)) {
          throw new CodedError('setup_installing', 'Portico is still finishing this server\'s secure connection. Try again in a minute.');
        } else if (pending && !absentClaim(status)) throw invalid();
        if (pending || renewal) {
          const old = pending, fresh: Pending = await createSetupAttempt({origin, hostedOrigin, pin: route.pin, selectionRevision: selection.revision, draft, setupRevision: resumed.revision,
            installationId: this.ports.installationId(), now: this.now(), crypto: webRouteCrypto});
          const next: Pending = renewal ? {...fresh, expected: {...status.operation!}} : fresh;
          await checkOpening();
          await this.ports.journal.change(current => {if (!operation.current() || (old ? !same(current, old) : !!current)) throw cancelled(); return next;});
          pending = next;
          await checkOpening(); captureRecovery();
        }
      }
      // A live attempt that already names an installed operation is a renewal being retried
      // (its Hosted call or bind failed): ask for the same renewal again.
      if (!renewal && pending?.expected && !this.expired(pending)) {
        const status = parseClaimStatus(await captured.request(routePath('get_networking_claim'), 'GET', undefined, operation.signal), route.pin);
        await checkOpening();
        renewal = status.state === 'installed' && status.installationAcknowledged && status.operation?.operationId === pending.expected.operationId
          && status.operation.localGeneration === pending.expected.localGeneration && status.operation.revision === pending.expected.revision;
      }
      if (!pending) {
        pending = await createSetupAttempt({origin, hostedOrigin, pin: route.pin, selectionRevision: selection.revision, draft, setupRevision: resumed.revision,
          installationId: this.ports.installationId(), now: this.now(), crypto: webRouteCrypto});
        await checkOpening();
        const fresh = pending;
        await this.ports.journal.change(old => {if (!operation.current() || old) throw cancelled(); return fresh;});
        await checkOpening(); captureRecovery();
      }
      if (pending.origin !== origin || pending.hostedOrigin !== hostedOrigin || !sameServerPin(pending.pin, route.pin) || pending.setupRequestId !== resumed.requestId || pending.setupRevision !== resumed.revision) throw invalid();
      this.attemptId = pending.attemptId;
      await this.current(pending, operation);
      await checkOpening();
      if (!pending.expected) {
        let status = parseClaimStatus(await captured.request(routePath('get_networking_claim'), 'GET', undefined, operation.signal), pending.pin);
        await this.current(pending, operation); await checkOpening();
        // Without a private original operation receipt, an existing operation is
        // unknown. A read must never turn it into this attempt's cancel target.
        if (!absentClaim(status) || identity && !equal(identity, status.identity)) throw invalid();
        identity = Object.freeze({...status.identity}); captureRecovery();
        if (status.actions.includes('prepare')) {
          status = parseClaimStatus(await captured.request(routePath('post_networking_claim_prepare'), 'POST', {purpose: 'setup', expected: status.identity}, operation.signal), pending.pin);
          await this.current(pending, operation); await checkOpening();
        }
        const prepared = preparedSetupOperation(status);
        if (!prepared) throw invalid();
        const old = pending, updated = {...pending, expected: {...prepared}};
        await this.ports.journal.change(current => {if (!operation.current() || !same(current, old)) throw cancelled(); return updated;});
        pending = updated;
        captureRecovery();
      }
      const binding = {pin: pending.pin, origin, hostedOrigin, operationId: pending.expected!.operationId, localGeneration: pending.expected!.localGeneration,
        setupRequestId: pending.setupRequestId, setupRevision: pending.setupRevision, state: pending.state, codeChallenge: pending.codeChallenge, installationId: pending.installationId};
      return await (this.ports.prepare ?? prepareSetupClaimHandoff)(captured, pending.expected!, binding, {
        assertPendingCurrent: async () => {await this.current(pending!, operation); await checkOpening();},
        bindPending: async handoff => {
          await this.current(pending!, operation); await checkOpening();
          const old = pending!, updated = {...old, handoff, expiresAt: new Date(Math.min(Date.parse(old.expiresAt), Date.parse(handoff.expiresAt))).toISOString()};
          await this.ports.journal.change(current => {if (!operation.current() || !same(current, old)) throw cancelled(); return updated;});
          pending = updated;
          await checkOpening(); captureRecovery();
        },
      }, {signal: operation.signal, now: () => this.now(), ...(renewal ? {renewal: true as const} : {})});
    } finally {this.busy = false;}
  }
  /** `retrying` alone may meet its own lapsed record (to renew or restart it); Use local never does. */
  private async assertRecovery(api: HttpLocalApi, receipt: RecoveryReceipt, operation: {signal: AbortSignal; current: () => boolean}, opening?: PrepareOpening, retrying = false) {
    const live = () => {
      const route = api.getRouteConnection();
      if (!operation.current() || receipt.origin !== this.ports.pageOrigin() || api.baseUrl !== receipt.origin ||
        receipt.installationId !== this.ports.installationId() || !route || route.getSnapshot().phase !== 'ready' || !sameServerPin(route.pin, receipt.pin)) throw cancelled();
    };
    live(); await opening?.assertOriginal(); live();
    this.ports.journal.assertAvailable();
    const record = await this.ports.journal.read(); live();
    if (!equal(record, receipt.pending)) throw cancelled();
    if (receipt.pending) {await this.current(receipt.pending, operation, true, retrying && this.expired(receipt.pending)); live();}
    const draft = await this.ports.draft(receipt.pin.serverId).read(); live();
    if (!equal(draft, receipt.draft)) throw cancelled();
    const selection = await this.ports.sessions.read(); live();
    if (selection.revision !== receipt.selectionRevision) throw cancelled();
    await opening?.assertOriginal(); live();
  }
  /** Unlike cancel(), this is a server operation. Its target is captured during
   * prepare/resume, never acquired by reading the latest operation on click. */
  private async recoverOriginalLocal(api: HttpLocalApi, receipt: RecoveryReceipt, opening: PrepareOpening): Promise<void> {
    const owned = this.operation(), signal = AbortSignal.any([owned.signal, opening.signal]);
    const operation = {signal, current: () => owned.current() && !signal.aborted && opening.isCurrent()};
    const current = () => this.assertRecovery(api, receipt, operation, opening);
    const captured = new HttpLocalApi(receipt.origin, receipt.draft.setupToken), route = api.getRouteConnection();
    if (!route) {this.busy = false; throw cancelled();} captured.useRoutes(route);
    const resume = async () => {
      await current();
      const setup = await resumeServerSetup(captured, {read: async () => receipt.draft, change: async () => {throw cancelled();}}, operation.signal);
      await current();
      if (!('claimRequired' in setup) || setup.serverId !== receipt.pin.serverId || setup.requestId !== receipt.draft.requestId || setup.revision !== receipt.setupRevision) throw cancelled();
    };
    const read = async () => {await current(); const status = parseClaimStatus(await captured.request(routePath('get_networking_claim'), 'GET', undefined, operation.signal), receipt.pin); await current(); return status;};
    try {
      await resume();
      const status = await read(), expected = receipt.pending?.expected;
      let absenceGeneration: string;
      if (expected) {
        const approval = status.approvalRequest;
        if (status.state !== 'prepared' || status.installationAcknowledged || status.accountId !== 'account-unbound' || !equal(status.operation, expected) ||
          status.identity.localGeneration !== expected.localGeneration || !status.actions.includes('cancel') || !approval || approval.purpose !== 'setup' ||
          approval.operationId !== expected.operationId || approval.localGeneration !== expected.localGeneration || approval.currentOwnerAccountId !== '' ||
          approval.previousOwnerRole !== '' || approval.returnRegistrationId !== '') throw cancelled();
        await current();
        const result = parseClaimStatus(await captured.request(routePath('post_networking_claim_cancel'), 'POST', {expected}, operation.signal), receipt.pin);
        await current();
        absenceGeneration = (BigInt(expected.localGeneration) + 1n).toString();
        // CancelAcknowledged clears the active pointer and advances the reset
        // generation. A pending/ambiguous response is not completed recovery.
        if (!absentClaim(result) || result.identity.localGeneration !== absenceGeneration) throw cancelled();
      } else {
        if (!absentClaim(status) || receipt.identity && !equal(status.identity, receipt.identity)) throw cancelled();
        absenceGeneration = status.identity.localGeneration;
      }
      await resume();
      const after = await read();
      if (!absentClaim(after) || after.identity.localGeneration !== absenceGeneration) throw cancelled();
      await current();
      // Keep the original setup proof. Retire only this exact browser record
      // after positive server evidence; cancel() remains browser-only cleanup.
      await this.ports.journal.change(old => {if (!operation.current() || !equal(old, receipt.pending)) throw cancelled(); return undefined;});
      if (!operation.current()) throw cancelled();
      await opening.assertOriginal();
      if (receipt.pending) await this.current(receipt.pending, operation, false);
      if (!operation.current()) throw cancelled();
      if (this.attemptId === receipt.pending?.attemptId) this.attemptId = undefined;
    } finally {this.busy = false;}
  }
  async pendingReturn() {
    this.ports.journal.assertAvailable(); const record = await this.ports.journal.read();
    if (!record || record.kind !== 'setup' || !record.handoff) return;
    // A read-only probe must not create or consume an attempt.
    const epoch = this.epoch;
    try {await this.current(record, {signal: new AbortController().signal, current: () => epoch === this.epoch});} catch {
      await this.ports.journal.change(old => same(old, record) ? undefined : old); return;
    }
    this.attemptId = record.attemptId;
    return {kind: 'setup' as const, transactionId: record.handoff.transactionId};
  }
  private async retire(api: HttpLocalApi, session: LocalSession) {
    if (this.ports.retire) return this.ports.retire(api, session);
    const temporary = new HttpLocalApi(api.baseUrl, session.accessToken), routes = api.getRouteConnection();
    if (routes) temporary.useRoutes(routes);
    await temporary.request(routePath('delete_sessions_current'), 'DELETE', undefined, AbortSignal.timeout(5000)).catch(() => {});
  }
  async consume(captured: CapturedPorticoReturn) {return this.finish(captured);}
  /** A valid opaque callback consumes the protected attempt before network I/O.
   * A lost completion is terminal; ordinary sign-in recovers the installed owner. */
  async finish(captured: CapturedPorticoReturn): Promise<Completion> {
    this.ports.journal.assertAvailable(); const operation = this.operation();
    let connected: Awaited<ReturnType<typeof connectPairedServer>> | undefined, bound: Awaited<ReturnType<typeof connectPairedPorticoServer>> | undefined;
    let grant: DirectSignIn | undefined, adopted = false, admissionServer: string | undefined;
    try {
      const record = await this.ports.journal.read();
      if (record?.version !== 1 || record.kind !== 'setup' || !record.handoff || !record.expected) throw invalid();
      const handoff = record.handoff;
      const callback = dispatchPorticoReturn(captured, {kind: 'setup', transactionId: handoff.transactionId});
      if (new URL(callback.href).origin !== record.origin || record.origin !== this.ports.pageOrigin()) throw invalid();
      await this.ports.journal.change(old => {if (!operation.current() || !same(old, record)) throw cancelled(); return undefined;});
      this.attemptId = undefined;
      const current = () => this.current(record, operation, false);
      await current();
      if (!setupHandoffBound(record)) throw invalid();
      const proof = await verifySetupClaimStart(handoff.claim, handoff.start, handoff.binding, this.now());
      if (!await setupProofMatches(record, proof, webRouteCrypto)) throw invalid();
      await current();
      const environment = this.ports.environment(record.pin.serverId);
      connected = await (this.ports.connect ?? connectPairedServer)(record.origin, record.pin, environment, operation.signal);
      await current(); connected.api.setAccessToken(record.setupToken);
      const raw = await connected.api.request<unknown>(routePath('get_networking_claim'), 'GET', undefined, operation.signal);
      await current();
      // Only an exact installed continuation completes admission.
      const continuation = setupContinuation(raw, parseClaimStatus(raw, record.pin), record);
      if (continuation.phase !== 'installed') throw invalid();
      grant = parseDirectSignIn(await connected.api.request(routePath('post_auth_portico_return_exchange'), 'POST', {transactionId: handoff.transactionId, state: record.state,
        verifier: record.verifier, installationId: record.installationId, serverChallenge: proof.start.serverChallenge, purpose: 'setup'}, operation.signal));
      await current();
      if (!setupOwnerGrantValid(grant, record)) throw invalid();
      const accountId = grant.snapshot.account.hostedAccountId!;
      bound = await (this.ports.connectPortico ?? connectPairedPorticoServer)(record.origin, record.pin,
        {origin: record.hostedOrigin, accountId, request: async () => {throw invalid();}}, environment, operation.signal);
      await current(); bound.api.setAccessToken(grant.session.accessToken);
      const admissionCurrent = () => operation.current() && this.now() < Date.parse(record.expiresAt) && record.origin === this.ports.pageOrigin() && record.installationId === this.ports.installationId();
      admissionServer = record.pin.serverId;
      await (this.ports.remember ?? rememberNativeSession)(bound.api, grant.session,
        {...environment, storage: this.ports.sessions.connectionStorage(record.pin.serverId, {signIn: 'portico', hostedAccount: bound.hosted, expectedRevision: record.expectedRevision})}, bound.hosted, admissionCurrent);
      const stored = await this.ports.sessions.read(), entry = stored.servers.find(value => value.connection.pin.serverId === stored.selectedServerId);
      if (!admissionCurrent() || !entry || entry.signIn !== 'portico' || !sameServerPin(entry.connection.pin, record.pin) || entry.connection.session.sessionFamilyId !== grant.session.sessionFamilyId ||
        entry.connection.session.viewer.accountId !== grant.snapshot.account.id || entry.connection.hosted?.origin !== record.hostedOrigin || entry.connection.hosted.accountId !== accountId) throw cancelled();
      await this.ports.draft(record.pin.serverId).change(old => {
        if (!admissionCurrent() || !old || old.requestId !== record.setupRequestId || old.setupToken !== record.setupToken || old.authMode !== 'hosted') throw cancelled();
        return undefined;
      });
      adopted = true; return {api: bound.api, result: grant};
    } finally {
      this.busy = false;
      try {
        if (!adopted && grant?.kind === 'signed-in') {
          const family = grant.session.sessionFamilyId;
          if (admissionServer) await this.ports.sessions.connectionStorage(admissionServer).change(old => old?.session.sessionFamilyId === family ? undefined : old).catch(() => {});
          if (connected) await this.retire(connected.api, grant.session);
        }
      } finally {connected?.connection.dispose(); if (!adopted) bound?.connection.dispose();}
    }
  }
}

export function createBrowserSetupClaim() {
  const guard = setupBrowserPending<Pending>('portico.setup-claim.guard'); guard.assertAvailable();
  const key = 'portico.setup-claim.tab.v1'; let tab = sessionStorage.getItem(key);
  if (tab !== null) routeBytes(tab, 32);
  else {tab = routeBase64(crypto.getRandomValues(new Uint8Array(32))); sessionStorage.setItem(key, tab);}
  return new BrowserSetupClaim({journal: setupBrowserPending<Pending>('portico.setup-claim.v1:' + tab),
    draft: id => setupBrowserPending<SetupDraft>('portico.initialize.' + id), sessions: browserAppSessions(), environment: browserConnectionEnvironment,
    installationId: () => {const value = installationIdentity(); if (!value) throw invalid(); return value.installationId;}, pageOrigin: () => location.origin});
}
