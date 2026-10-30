import {HttpLocalApi, type LocalSession} from '../../../../packages/client-core/src/index.ts';
import {type AppSessionStore, knownServerRoutes} from '../../../../packages/client-core/src/app-sessions.ts';
import {parseClaimStatus, type ClaimStatus} from '../../../../packages/client-core/src/claim-onboarding.ts';
import {installationIdentity} from '../../../../packages/client-core/src/installation.ts';
import {prepareSetupClaimHandoff} from '../../../../packages/client-core/src/setup-claim-client.ts';
import {verifySetupClaimStart} from '../../../../packages/client-core/src/setup-claim-start.ts';
import {createSetupAttempt, preparedSetupOperation, setupApprovalCode, setupAttemptLive, setupContinuation, setupGrantSessionBound, setupHandoffBound, setupOwnerGrantValid, setupProofMatches, type SetupAttempt} from '../../../../packages/client-core/src/setup-claim-attempt.ts';
import {resumeServerSetup, type SetupDraft} from '../../../../packages/client-core/src/setup-onboarding.ts';
import {parseDirectSignIn, type DirectSignIn} from '../../../../packages/client-core/src/profile-management.ts';
import {assertFirstContactIdentity, routeOrigin, sameServerPin, type RouteCrypto} from '../../../../packages/client-core/src/route-identity.ts';
import {connectPairedServer, connectPairedPorticoServer, type ConnectionEnvironment} from '../../../../packages/client-core/src/server-connections.ts';
import {NativeSetupEnded} from './setup-outcome';
import {NativeSetupApproval} from './setup-approval';
import {CodedError} from '../../../../packages/client-core/src/server-messages.ts';
import {routePath} from '../../../../packages/contracts/src/routes.ts';

type Pending = SetupAttempt & Readonly<{approvalURL?: string; retiredDraft?: SetupDraft}>;
type Journal = Readonly<{assertAvailable: () => void; read: () => Promise<Pending | undefined>;
  change: (update: (old: Pending | undefined) => Pending | undefined, beforeCommit?: () => void) => Promise<void>}>;
type Ports = Readonly<{
  journal: Journal; draft: (serverId: string) => NativeSetupDraftStore;
  selectionLease: typeof withNativeSelectionLease; sessions: Pick<AppSessionStore, 'read'>;
  environment: (serverId: string) => ConnectionEnvironment; installationId: () => string; pageOrigin: () => string;
  crypto: RouteCrypto; fetcher: typeof fetch;
  now?: () => number; prepare?: typeof prepareSetupClaimHandoff;
  connect?: typeof connectPairedServer; connectPortico?: typeof connectPairedPorticoServer;
}>;
type Completion = Readonly<{api: HttpLocalApi; result: Extract<DirectSignIn, {kind: 'signed-in'}>;
  hosted: {origin: string; accountId: string; key: import('../../../../packages/client-core/src/route-identity').RouteKey};
  accept: () => void; release: () => Promise<void>;
  draft: {requestId: string; setupToken: string};
  admission: import('../../../../packages/client-core/src/app-sessions').NewServerConnection}>;
const invalid = () => new CodedError('invalid_return', 'This setup continuation is no longer available.');
const cancelled = () => new CodedError('cancelled', 'The pending setup or server selection changed.');
const same = (a: Pending | undefined, b: Pending) => !!a && JSON.stringify(a) === JSON.stringify(b);

/** Native setup resumes from the original server's installed status, never a callback URL.
 * Initial setup has no Local owner or authorization family. Its only authority
 * is the original protected setup request/token and Server-supplied revision. */
const absentClaim = (status: ClaimStatus) => status.state === 'unconfigured' && !status.operation && !status.accountId &&
  !status.installationAcknowledged && !status.approvalRequired && !status.approvalRequest && status.actions.length === 1 && status.actions[0] === 'prepare';
export class NativeSetupClaim {
  private ended?: Pending;
  private endedDraft?: SetupDraft;
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
  private async current(record: Pending, operation: ReturnType<NativeSetupClaim['operation']>, journal = true, expired = false) {
    this.ports.journal.assertAvailable();
    if (journal && !same(await this.ports.journal.read(), record)) throw cancelled();
    const draft = await this.ports.draft(record.pin.serverId).read(), selection = await this.ports.sessions.read();
    // `expired` keeps every binding check but reads the window at creation: a record a slow
    // installation outlived is still this app's own, to be renewed or restarted, never used.
    if (!operation.current() || !setupAttemptLive(record, {draft, selectionRevision: selection.revision, origin: this.ports.pageOrigin(),
      installationId: this.ports.installationId(), now: expired ? record.createdAt : this.now()})) throw cancelled();
    return draft;
  }
  private expired(record: Pending) {return !record.retired && Date.parse(record.expiresAt) <= this.now();}
  /** Explicit cancellation retires only this native attempt, never an installed claim. */
  async cancel() {
    ++this.epoch; this.controller?.abort();
    const id = this.attemptId; this.attemptId = undefined;
    if (id) await this.ports.journal.change(old => old?.attemptId === id ? undefined : old);
  }
  dispose() {++this.epoch; this.controller?.abort();}
  async resumePrepared(): Promise<{url?: string} | undefined> {
    const operation = this.operation();
    try {
      const record = await this.ports.journal.read();
      if (!record) return undefined;
      // A lapsed attempt goes to prepare, which renews or restarts it against the claim as it is.
      if (this.expired(record)) return undefined;
      if (!record.handoff || !record.expected) throw invalid();
      await this.current(record, operation);
      if (!setupHandoffBound(record)) throw invalid();
      const proof = await verifySetupClaimStart(record.handoff.claim, record.handoff.start, record.handoff.binding, this.now(), this.ports.crypto);
      await this.current(record, operation);
      if (record.handoff.expiresAt !== proof.start.expiresAt || Date.parse(record.expiresAt) > Date.parse(proof.start.expiresAt)) throw invalid();
      if (record.retired) {this.ended = record; this.endedDraft = record.retiredDraft; throw new NativeSetupEnded('cancelled');}
      if (record.approvalURL && !setupApprovalCode(record.approvalURL, {hostedOrigin: record.hostedOrigin, operationId: record.expected.operationId, serverName: proof.claim.name})) throw invalid();
      this.attemptId = record.attemptId; return {url: record.approvalURL};
    } finally {this.busy = false;}
  }
  async prepare(api: HttpLocalApi, hostedOrigin: string): Promise<string | undefined> {
    this.ports.journal.assertAvailable();
    const existing = await this.resumePrepared();
    if (existing) return existing.url;
    const origin = this.ports.pageOrigin(), route = api.getRouteConnection();
    if (api.baseUrl !== origin || routeOrigin(origin) !== origin || !route || route.getSnapshot().phase !== 'ready' ||
      routeOrigin(hostedOrigin) !== hostedOrigin || !hostedOrigin.startsWith('https://')) throw invalid();
    const operation = this.operation();
    try {
      const draft = await this.ports.draft(route.pin.serverId).read(), selection = await this.ports.sessions.read();
      if (!operation.current() || !draft || draft.version !== 1 || draft.serverId !== route.pin.serverId || draft.authMode !== 'hosted' || !draft.setupToken) throw invalid();
      assertFirstContactIdentity(origin, route.pin, {knownRoutes: knownServerRoutes(selection)});
      const captured = new HttpLocalApi(origin, draft.setupToken, this.ports.fetcher); captured.useRoutes(route);
      const resumed = await resumeServerSetup(captured, this.ports.draft(route.pin.serverId), operation.signal);
      if (!('claimRequired' in resumed) || resumed.requestId !== draft.requestId) throw invalid();
      let pending = await this.ports.journal.read();
      if (pending && (pending.origin !== origin || pending.hostedOrigin !== hostedOrigin || !sameServerPin(pending.pin, route.pin) || pending.setupRequestId !== resumed.requestId || pending.setupRevision !== resumed.revision)) throw invalid();
      // A slow installation (a certificate issued after several attempts) can outlive the
      // five-minute attempt. Its own expired record, or none at all, then meets the claim as it
      // now is: installed for this setup → renew its start with a fresh attempt (no second
      // approval; owner admission is unchanged); never approved → cancel this record's own
      // operation and start cleanly; still installing → say so. Anything else is as before.
      let renewal = false;
      if (!pending || this.expired(pending)) {
        if (pending) await this.current(pending, operation, true, true);
        const raw = await captured.request<unknown>(routePath('get_networking_claim'), 'GET', undefined, operation.signal);
        if (!operation.current()) throw cancelled();
        const status = parseClaimStatus(raw, route.pin), now = this.now();
        const continuation = raw && typeof raw === 'object' && 'setupContinuation' in raw ? (raw as {setupContinuation?: {phase?: unknown; expiresAt?: unknown}}).setupContinuation : undefined;
        const lapsed = !!continuation && (continuation.phase === 'prepared' || continuation.phase === 'installed') && typeof continuation.expiresAt === 'string' && Date.parse(continuation.expiresAt) <= now;
        const mine = !pending?.expected || !!status.operation && status.operation.operationId === pending.expected.operationId && status.operation.localGeneration === pending.expected.localGeneration;
        if (status.state === 'installed' && status.installationAcknowledged && status.operation && lapsed && mine) renewal = true;
        else if (pending?.expected && status.state === 'prepared' && mine && status.actions.includes('cancel')) {
          // Never approved: retire only the operation this record itself captured.
          const result = parseClaimStatus(await captured.request(routePath('post_networking_claim_cancel'), 'POST', {expected: status.operation}, operation.signal), route.pin);
          if (!operation.current()) throw cancelled();
          if (!absentClaim(result)) throw invalid();
        } else if (status.operation && mine && (['approved', 'finalizing', 'retrieving'].includes(status.state) || status.state === 'installed' && !status.installationAcknowledged)) {
          throw new CodedError('setup_installing', 'Portico is still finishing this server\'s secure connection. Try again in a minute.');
        } else if (pending && !absentClaim(status)) throw invalid();
        if (pending || renewal) {
          const old = pending, fresh: Pending = await createSetupAttempt({origin, hostedOrigin, pin: route.pin, selectionRevision: selection.revision, draft, setupRevision: resumed.revision,
            installationId: this.ports.installationId(), now: this.now(), crypto: this.ports.crypto});
          const next: Pending = renewal ? {...fresh, expected: {...status.operation!}} : fresh;
          await this.ports.journal.change(current => {if (!operation.current() || (old ? !same(current, old) : !!current)) throw cancelled(); return next;});
          pending = next;
        }
      }
      // A live attempt that already names an installed operation is a renewal being retried
      // (its Hosted call or bind failed): ask for the same renewal again.
      if (!renewal && pending?.expected && !this.expired(pending)) {
        const status = parseClaimStatus(await captured.request(routePath('get_networking_claim'), 'GET', undefined, operation.signal), route.pin);
        if (!operation.current()) throw cancelled();
        renewal = status.state === 'installed' && status.installationAcknowledged && status.operation?.operationId === pending.expected.operationId
          && status.operation.localGeneration === pending.expected.localGeneration && status.operation.revision === pending.expected.revision;
      }
      if (!pending) {
        pending = await createSetupAttempt({origin, hostedOrigin, pin: route.pin, selectionRevision: selection.revision, draft, setupRevision: resumed.revision,
          installationId: this.ports.installationId(), now: this.now(), crypto: this.ports.crypto});
        const fresh = pending;
        await this.ports.journal.change(old => {if (!operation.current() || old) throw cancelled(); return fresh;});
      }
      if (pending.origin !== origin || pending.hostedOrigin !== hostedOrigin || !sameServerPin(pending.pin, route.pin) || pending.setupRequestId !== resumed.requestId || pending.setupRevision !== resumed.revision) throw invalid();
      this.attemptId = pending.attemptId;
      await this.current(pending, operation);
      if (!pending.expected) {
        let status = parseClaimStatus(await captured.request(routePath('get_networking_claim'), 'GET', undefined, operation.signal), pending.pin);
        await this.current(pending, operation);
        if (status.actions.includes('prepare')) {
          status = parseClaimStatus(await captured.request(routePath('post_networking_claim_prepare'), 'POST', {purpose: 'setup', expected: status.identity}, operation.signal), pending.pin);
          await this.current(pending, operation);
        }
        const prepared = preparedSetupOperation(status);
        if (!prepared) throw invalid();
        const old = pending, updated = {...pending, expected: {...prepared}};
        await this.ports.journal.change(current => {if (!operation.current() || !same(current, old)) throw cancelled(); return updated;});
        pending = updated;
      }
      const binding = {pin: pending.pin, origin, hostedOrigin, operationId: pending.expected!.operationId, localGeneration: pending.expected!.localGeneration,
        setupRequestId: pending.setupRequestId, setupRevision: pending.setupRevision, state: pending.state, codeChallenge: pending.codeChallenge, installationId: pending.installationId};
      const url = await (this.ports.prepare ?? prepareSetupClaimHandoff)(captured, pending.expected!, binding, {
        assertPendingCurrent: async () => {await this.current(pending!, operation);},
        bindPending: async handoff => {
          await this.current(pending!, operation);
          const old = pending!, updated = {...old, handoff, expiresAt: new Date(Math.min(Date.parse(old.expiresAt), Date.parse(handoff.expiresAt))).toISOString()};
          await this.ports.journal.change(current => {if (!operation.current() || !same(current, old)) throw cancelled(); return updated;});
          pending = updated;
        },
      }, {signal: operation.signal, now: () => this.now(), crypto: this.ports.crypto, ...(renewal ? {renewal: true as const} : {})});
      await this.current(pending, operation);
      const beforeURL = pending, withURL = {...pending, approvalURL: url};
      await this.ports.journal.change(old => {if (!operation.current() || !same(old, beforeURL)) throw cancelled(); return withURL;});
      await this.current(withURL, operation);
      return url;
    } finally {this.busy = false;}
  }
  async accountApproval(url: string, opening: {signal: AbortSignal; current: () => boolean; canApprove: () => boolean}) {
    const resumed = await this.resumePrepared();
    if (!resumed?.url || resumed.url !== url) throw invalid();
    const operation = this.operation();
    try {
      const record = await this.ports.journal.read();
      if (!record?.handoff || !record.expected) throw invalid();
      await this.current(record, operation);
      const proof = await verifySetupClaimStart(record.handoff.claim, record.handoff.start, record.handoff.binding, this.now(), this.ports.crypto);
      await this.current(record, operation);
      const code = setupApprovalCode(url, {hostedOrigin: record.hostedOrigin, operationId: record.expected.operationId, serverName: proof.claim.name});
      if (!code) throw invalid();
      const originalDraft = await this.current(record, operation);
      const approval = new NativeSetupApproval({code, serverId: record.pin.serverId, name: proof.claim.name, origin: record.origin,
        transactionId: record.handoff.transactionId, expiresAt: record.handoff.expiresAt},
        async () => {await this.current(record, operation);}, operation.signal, opening, () => {if (!operation.current() || !opening.current()) throw cancelled(); this.ended = record; this.endedDraft = originalDraft ? Object.freeze({...originalDraft}) : undefined;});
      try {const display = await approval.review(); return {approval, display};}
      catch (error) {approval.dispose(); throw error;}
    } finally {this.busy = false;}
  }
  /** Explicit user restart retires only this terminal local journal/draft.
   * A tombstone prevents a crash between keys from reviving the old handoff. */
  async abandonEnded() {
    const operation = this.operation(), record = this.ended, capturedDraft = this.endedDraft;
    try {
      if (!record || !capturedDraft) throw invalid();
      let ownedRecord = record;
      const assert = () => {
        if (!operation.current() || record.installationId !== this.ports.installationId() || this.ended !== ownedRecord) throw cancelled();
      };
      await this.ports.selectionLease(record.expectedRevision, assert, async (check, beforeCommit) => {
        const stored = await this.ports.journal.read(); await check();
        if (!same(stored, record)) throw cancelled();
        const retired: Pending = {...record, retired: true, retiredDraft: {...capturedDraft}};
        await this.ports.journal.change(old => {beforeCommit(); if (!same(old, record)) throw cancelled(); return retired;}, beforeCommit);
        ownedRecord = retired; this.ended = retired; await check();
        await this.ports.draft(record.pin.serverId).change(old => {
          beforeCommit();
          if (old === undefined ? !record.retired : JSON.stringify(old) !== JSON.stringify(capturedDraft)) throw cancelled();
          return undefined;
        }, beforeCommit);
        await check();
        await this.ports.journal.change(old => {beforeCommit(); if (!same(old, retired)) throw cancelled(); return undefined;}, beforeCommit);
        await check(); this.ended = undefined;
        this.endedDraft = undefined; this.attemptId = undefined;
      });
    } finally {this.busy = false;}
  }
  private async retire(api: HttpLocalApi, session: LocalSession, initial: ReadonlySet<string>) {
    if (initial.has(session.sessionFamilyId) || session.installationId !== this.ports.installationId()) return;
    const temporary = new HttpLocalApi(api.baseUrl, session.accessToken, this.ports.fetcher), routes = api.getRouteConnection();
    if (routes) temporary.useRoutes(routes);
    const retained = await this.ports.sessions.read();
    if (retained.servers.some(value => value.connection.session.sessionFamilyId === session.sessionFamilyId)) return;
    if (!routes || session.viewer.serverId !== routes.pin.serverId || session.serverIdentity?.publicKey !== routes.pin.publicKey || session.serverIdentity.fingerprint !== routes.pin.fingerprint) return;
    await temporary.logout().catch(() => {});
  }
  /** A wake only reads the original installed continuation. Consume the exact
   * journal once, immediately before exchange; a lost exchange is terminal. */
  async finishInstalled(): Promise<Completion | undefined> {
    this.ports.journal.assertAvailable(); const operation = this.operation();
    let connected: Awaited<ReturnType<typeof connectPairedServer>> | undefined, bound: Awaited<ReturnType<typeof connectPairedPorticoServer>> | undefined;
    let grant: DirectSignIn | undefined, adopted = false;
    const initial = new Set<string>();
    try {
      for (const value of (await this.ports.sessions.read()).servers) initial.add(value.connection.session.sessionFamilyId);
      const record = await this.ports.journal.read();
      if (record?.version !== 1 || record.kind !== 'setup' || !record.handoff || !record.expected) throw invalid();
      const handoff = record.handoff;
      if (record.origin !== this.ports.pageOrigin()) throw invalid();
      let consumed = false;
      const current = () => this.current(record, operation, !consumed);
      await current();
      if (!setupHandoffBound(record)) throw invalid();
      const proof = await verifySetupClaimStart(handoff.claim, handoff.start, handoff.binding, this.now(), this.ports.crypto);
      if (!await setupProofMatches(record, proof, this.ports.crypto)) throw invalid();
      if (record.retired) {this.ended = record; this.endedDraft = record.retiredDraft; throw new NativeSetupEnded('cancelled');}
      await current();
      const environment = this.ports.environment(record.pin.serverId);
      connected = await (this.ports.connect ?? connectPairedServer)(record.origin, record.pin, environment, operation.signal);
      await current(); connected.api.setAccessToken(record.setupToken);
      const raw = await connected.api.request<unknown>(routePath('get_networking_claim'), 'GET', undefined, operation.signal);
      await current();
      const continuation = setupContinuation(raw, parseClaimStatus(raw, record.pin), record);
      if (continuation.phase === 'cancelled') {this.ended = record; const draft = await current(); this.endedDraft = draft ? Object.freeze({...draft}) : undefined; throw new NativeSetupEnded('cancelled');}
      // Not installed yet: that grants nothing and retains PKCE. An installed claim
      // completes owner admission with no route or return registration (O-163).
      if (continuation.phase === 'pending') return undefined;
      if (continuation.phase === 'invalid') throw invalid();
      await this.ports.journal.change(old => {if (!operation.current() || !same(old, record)) throw cancelled(); return undefined;});
      consumed = true; this.attemptId = undefined;
      grant = parseDirectSignIn(await connected.api.request(routePath('post_auth_portico_return_exchange'), 'POST', {transactionId: handoff.transactionId, state: record.state,
        verifier: record.verifier, installationId: record.installationId, serverChallenge: proof.start.serverChallenge, purpose: 'setup'}, operation.signal));
      await current();
      if (!setupOwnerGrantValid(grant, record) || !setupGrantSessionBound(grant, record, this.now())) throw invalid();
      const accountId = grant.snapshot.account.hostedAccountId!;
      bound = await (this.ports.connectPortico ?? connectPairedPorticoServer)(record.origin, record.pin,
        {origin: record.hostedOrigin, accountId, request: async () => {throw invalid();}}, environment, operation.signal);
      await current(); bound.api.setAccessToken(grant.session.accessToken);
      const returnedFamily = grant.session.sessionFamilyId;
      const retained = await this.ports.sessions.read(); await current();
      if (initial.has(returnedFamily) || retained.servers.some(value => value.connection.session.sessionFamilyId === returnedFamily)) throw invalid();
      const original = bound.api, session = grant.session, connection = bound.connection;
      let custody = true;
      const release = async () => {if (!custody) return; custody = false; try {await this.retire(original, session, initial);} finally {connection.dispose();}};
      adopted = true; return {api: bound.api, result: grant, hosted: bound.hosted,
        accept: () => {custody = false;}, release,
        draft: {requestId: record.setupRequestId, setupToken: record.setupToken},
        admission: {signIn: 'portico', hostedAccount: {origin: record.hostedOrigin, accountId}, expectedRevision: record.expectedRevision}};
    } finally {
      this.busy = false;
      try {
        if (!adopted && grant?.kind === 'signed-in') {
          if (connected) await this.retire(connected.api, grant.session, initial);
        }
      } finally {connected?.connection.dispose(); if (!adopted) bound?.connection.dispose();}
    }
  }
}

import {NativeQueueStore} from './queue-storage';
import {nativeAppSessions, withNativeSelectionLease} from './app-session-storage';
import {nativeSetupDraft, type NativeSetupDraftStore} from './setup-draft';
import {nativeNetworkFetch, nativeRouteCrypto, nativeServerEnvironment} from './server-connections';

export function createNativeSetupClaim(origin: string, serverId: string) {
  const storage = new NativeQueueStore(), key = 'native.setup-claim.v1:' + serverId;
  return new NativeSetupClaim({
    journal: {assertAvailable: () => {},
      read: () => storage.change(key, raw => ({record: raw, value: raw == null ? undefined : raw as Pending})),
      change: async (update, beforeCommit) => {await storage.change(key, raw => ({record: update(raw == null ? undefined : raw as Pending) ?? null, value: undefined}), undefined, beforeCommit);}},
    selectionLease: withNativeSelectionLease, draft: nativeSetupDraft, sessions: nativeAppSessions, environment: nativeServerEnvironment,
    crypto: nativeRouteCrypto, fetcher: nativeNetworkFetch, pageOrigin: () => origin,
    installationId: () => {const value = installationIdentity(); if (!value) throw invalid(); return value.installationId;},
  });
}
