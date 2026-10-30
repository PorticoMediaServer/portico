import {routeBody, routePath} from '../../../packages/contracts/src/routes.ts';
import {HostedAccountClient, type AccountScope, type RecentProof, type SecurityState} from '@core/hosted-security.ts';
import type {HostedSession, LocalSession} from '@core/index.ts';
import {restoreExactNativeSession} from '@core/server-connections.ts';
import {routeBase64, routeBytes, webRouteCrypto} from '@core/route-identity.ts';
import {parseSecuritySigned, parseSecurityReturnCode, securityReturnEntry, verifySecurityConfirmation, verifySecurityContext, type CredentialSecurityPurpose, type SecurityPrepare} from '@core/security-return.ts';
import {accountSecurityRequest, accountSecurityUIURL} from './account-security-policy';
import {BrowserAccountApi} from './account';
import {browserAppSessions, browserConnectionEnvironment} from './server-connections';
import {currentAccountWork, type AccountIdentity} from './account-work';
import type {ServicesAccount} from '../services/session';
import {browserSecurityJournal, parseSecurityJournal, securityCoordinatorStamp, type AccountSecurityJournal, type AccountSecurityPending, type OriginalSecurityAccount} from './account-security-journal';

function ended(): never {throw new Error('This account security continuation no longer matches its original opening.');}
const random = () => routeBase64(crypto.getRandomValues(new Uint8Array(32)));
const noRefresh = <T extends HostedSession | LocalSession>(session: T): T => {
  const {refreshToken: _unused, ...original} = session;
  return JSON.parse(JSON.stringify(original)) as T;
};
const checkDeadline = (original: OriginalSecurityAccount) => {
  if (Date.parse(original.expiresAt) <= Date.now() || securityCoordinatorStamp(original.hostedOrigin) !== original.coordinator) ended();
};
export function originalSecurityClient(origin: string) {
  // Inert transport only. The protected return never starts the singleton's
  // synchronize/accessSession/refresh/keepAlive or a sign-in admission path.
  const api = new BrowserAccountApi(origin);
  return new HostedAccountClient(origin, 'browser', (path, method, body, signal, token) => api.send(path, method, body, signal, token));
}
const identity = (o: OriginalSecurityAccount): AccountScope => ({accountId: o.session.account.id, familyId: o.session.familyId!});

async function originalServer(pending: AccountSecurityPending, signal?: AbortSignal, beforeRead?: () => Promise<void>) {
  const original = pending.original, expected = original.server;
  if (!expected) ended();
  checkDeadline(original);
  const sessions = browserAppSessions(), current = await sessions.read();
  const entry = current.servers.find(v => v.connection.pin.serverId === expected.pin.serverId), connection = entry?.connection;
  if (current.revision !== expected.revision || current.selectedServerId !== expected.pin.serverId || entry?.signIn !== 'portico' || !connection || connection.selectionId !== expected.selectionId || connection.hosted?.origin !== original.hostedOrigin || connection.hosted.accountId !== original.session.account.id || connection.pin.serverId !== expected.pin.serverId || connection.pin.publicKey !== expected.pin.publicKey || connection.pin.fingerprint !== expected.pin.fingerprint || connection.pendingRefresh) ended();
  const pageOrigin = pending.prepare?.origin;
  if (!pageOrigin || pageOrigin !== location.origin || !pageOrigin.startsWith('https://')) ended();
  const environment = browserConnectionEnvironment(expected.pin.serverId), storage = environment.storage;
  // The registered browser origin is the only return target and the only
  // transport candidate for this UI continuation. A faster LAN/discovered route
  // cannot change its Origin/Host context, and no route hint is persisted.
  const scopedFetch: typeof fetch = async (input, init) => {
    const {message, request} = accountSecurityRequest(input, init, pageOrigin, Boolean(beforeRead));
    if (signal) request.signal = request.signal ? AbortSignal.any([signal, request.signal]) : signal;
    if (beforeRead) await beforeRead();signal?.throwIfAborted();
    const response = await (environment.fetcher ?? fetch)(message, request);
    if (beforeRead) await beforeRead();signal?.throwIfAborted();
    return response;
  };
  const restored = await restoreExactNativeSession({...environment, fetcher: scopedFetch, discover: undefined, storage: {
    read: async () => {
      const actual = await storage.read();
      if (!actual || actual.selectionId !== expected.selectionId || actual.pin.serverId !== expected.pin.serverId || actual.session.accessToken !== expected.session.accessToken || actual.session.tokenGeneration !== expected.session.tokenGeneration) ended();
      return {...actual, logicalOrigin: pageOrigin, routes: {paired: [pageOrigin]}};
    },
    change: async () => ended(),
  }}, expected.session);
  signal?.throwIfAborted();
  if (!restored || (await sessions.read()).revision !== expected.revision) ended();
  checkDeadline(original);
  return restored.api;
}

/** Capture happens on the existing Account Settings opening before any prepare
 * request. A selected local profile is UI return context only. */
export type AccountSecurityServerScope = Readonly<{serverId: string; familyId: string; accountId: string; profileId: string}>;
export async function captureAccountSecurity(input: {
  account: ServicesAccount; identity: AccountIdentity; state: SecurityState;
  purpose: CredentialSecurityPurpose; target: string; providerId: string;
  assertCurrent: () => void; server?: AccountSecurityServerScope;
}, journal = browserSecurityJournal()): Promise<AccountSecurityPending> {
  journal.assertAvailable(); input.assertCurrent();
  const account = input.account, expectedIdentity = Object.freeze({...input.identity}), assertCurrent = input.assertCurrent, providerId = input.providerId, hostedOrigin = input.account.api.origin;
  const state: SecurityState = JSON.parse(JSON.stringify(input.state)), purpose = input.purpose, target = input.target, originalServerScope = input.server ? {...input.server} : undefined;
  const provider = state.providers.find(v => v.id === providerId);
  if (state.hasPassword || !provider?.revision || state.accountId !== expectedIdentity.accountId || state.familyId !== expectedIdentity.familyId) ended();
  const captured = await currentAccountWork(account, async session => {assertCurrent(); return noRefresh(session);}, expectedIdentity);
  assertCurrent();
  const coordinator = securityCoordinatorStamp(hostedOrigin), now = Date.now();
  let server: OriginalSecurityAccount['server'];
  let expiry = Math.min(now + 300000, Date.parse(captured.expiresAt));
  if (originalServerScope) {
    const record = await browserAppSessions().read(); assertCurrent();
    const selected = record.servers.find(v => v.connection.pin.serverId === record.selectedServerId), connection = selected?.connection;
    if (!connection || record.selectedServerId !== originalServerScope.serverId || connection.pin.serverId !== originalServerScope.serverId || connection.session.viewer.serverId !== originalServerScope.serverId || connection.session.sessionFamilyId !== originalServerScope.familyId || selected?.signIn !== 'portico' || !connection.selectionId || connection.pendingRefresh || connection.hosted?.origin !== hostedOrigin || connection.hosted.accountId !== expectedIdentity.accountId || connection.session.viewer.authority !== 'local' || connection.session.viewer.accountId !== originalServerScope.accountId || connection.session.viewer.profileId !== originalServerScope.profileId) ended();
    server = {revision: record.revision, selectionId: connection.selectionId, pin: {...connection.pin}, session: noRefresh(connection.session)};
    expiry = Math.min(expiry, Date.parse(server.session.expiresAt), Date.parse(server.session.authorizationHorizon));
  }
  const original: OriginalSecurityAccount = {version: 1, kind: server ? 'server-account' : 'hosted-account', hostedOrigin, session: captured, coordinator, state,
    openingId: crypto.randomUUID(), taskId: crypto.randomUUID(), requestId: crypto.randomUUID(), intent: {purpose, target, operationId: crypto.randomUUID()}, provider: {...provider, revision: provider.revision}, createdAt: new Date(now).toISOString(), expiresAt: new Date(expiry).toISOString(), ...(server ? {server} : {})};
  const prepare: SecurityPrepare | undefined = server ? {...original.intent, origin: location.origin, openingId: original.openingId, taskId: original.taskId, providerIdentityId: provider.id, providerRevision: provider.revision, nonce: random(), expiresAt: original.expiresAt} : undefined;
  const pending: AccountSecurityPending = {original, ...(prepare ? {prepare, state: random(), verifier: random()} : {})};
  return await journal.create(pending, () => {assertCurrent(); checkDeadline(original);}) as AccountSecurityPending;
}

/** Exact retry of one recorded preparation/start. Every uncertain result keeps
 * its original operation/request key. There is no latest transaction lookup. */
export async function leaveForSecurityProvider(pending: AccountSecurityPending, assertCurrent: () => void, journal = browserSecurityJournal(), beforeNavigate?: () => void) {
  const predecessor = parseSecurityJournal(pending);
  if (!('original' in predecessor)) ended();
  const originalFingerprint = JSON.stringify(predecessor.original);
  const saved = await journal.read();
  if (!saved || !('original' in saved) || JSON.stringify(saved.original) !== originalFingerprint) ended();
  let held = saved;
  const check = () => {assertCurrent(); checkDeadline(held.original);};
  const client = originalSecurityClient(held.original.hostedOrigin), original = held.original;
  check(); await journal.assertOriginal(held); check();
  if (!original.server) {
    const started = await client.start(original.provider.provider, {mode: 'reauthenticate', requestId: original.requestId, intent: original.intent, expectedIdentity: {id: original.provider.id, revision: original.provider.revision}}, undefined, original.session.accessToken);
    if (started.mode !== 'reauthenticate' || !started.authorizationUrl || held.transactionId && held.transactionId !== started.transactionId) ended();
    check();
    held = await journal.replace(held, {...held, transactionId: started.transactionId}, check) as AccountSecurityPending;
    check(); await journal.assertOriginal(held); check();
    beforeNavigate?.();location.assign(started.authorizationUrl);
    return held;
  }
  if (!held.prepare || !held.state || !held.verifier) ended();
  const prepare = held.prepare, state = held.state, verifier = held.verifier;
  const api = await originalServer(held);
  try {
    if (!held.context) {
      const signed = parseSecuritySigned(await api.request(routePath('post_direct_security_return_prepare'), 'POST', routeBody('post_direct_security_return_prepare', prepare)));
      const facts = await verifySecurityContext(signed, original.server.pin, {prepare, hostedOrigin: original.hostedOrigin, accountId: original.session.account.id});
      check();
      // This is the actual signed expiry, possibly shorter than requested. The
      // immutable original requested deadline and operation are never renewed.
      if (Date.parse(facts.expiresAt) <= Date.now()) ended();
      held = await journal.replace(held, {...held, context: signed}, check) as AccountSecurityPending;
    }
    const facts = await verifySecurityContext(held.context, original.server.pin, {prepare, hostedOrigin: original.hostedOrigin, accountId: original.session.account.id});
    if (!held.intentId) {
      const challenge = routeBase64(await webRouteCrypto.sha256(new TextEncoder().encode(verifier)));
      const prepared = await client.prepareSecurityReturn({context: held.context!, state, codeChallenge: challenge}, facts.expiresAt, original.session.accessToken);
      check();
      held = await journal.replace(held, {...held, intentId: prepared.intentId}, check) as AccountSecurityPending;
    }
    check(); await journal.assertOriginal(held); check();
    beforeNavigate?.();location.assign(securityReturnEntry(original.hostedOrigin, held.intentId!));
    return held;
  } finally {api.getRouteConnection()?.dispose();}
}

/** A verified provider proof remains memory-only. Canonical SecurityTask uses
 * this closed port, which always sends the captured access token and intent. */
export class OriginalProviderSecurity {
  private live = true;
  private constructor(readonly pending: AccountSecurityPending, readonly proof: RecentProof, readonly client: HostedAccountClient, private readonly journal: AccountSecurityJournal) {}
  get original() {return this.pending.original;}
  acceptsUIURL(href: string) {
    const origin = this.original.kind === 'server-account' ? this.pending.prepare?.origin : this.original.hostedOrigin;
    if (!origin) return false;
    return accountSecurityUIURL(this.original.kind, origin, href);
  }
  current = () => this.live && Date.parse(this.original.expiresAt) > Date.now() && this.acceptsUIURL(location.href);
  private async assertOriginal(signal?: AbortSignal) {
    if (!this.current()) ended();
    signal?.throwIfAborted(); checkDeadline(this.original);
    await this.journal.assertOriginal(this.pending);
    signal?.throwIfAborted();
    if (this.original.server) {
      const api = await originalServer(this.pending, signal);
      try {await api.request(routePath('post_direct_security_return_check'), 'POST', routeBody('post_direct_security_return_check', {context: this.pending.context}), signal);}
      finally {api.getRouteConnection()?.dispose();}
    }
    if (!this.current()) ended(); checkDeadline(this.original);
  }
  /** The original page may read its existing authority while its credential
   * dialog is open. This never renews, persists, or authorizes a mutation. */
  private async assertUIOriginal(signal?: AbortSignal) {
    if (!this.current()) ended();checkDeadline(this.original);signal?.throwIfAborted();
    await this.journal.assertOriginal(this.pending);
    if (this.original.server) {
      const expected = this.original.server, selected = await browserAppSessions().read();
      const entry = selected.servers.find(v => v.connection.pin.serverId === expected.pin.serverId), record = entry?.connection;
      if (selected.revision !== expected.revision || selected.selectedServerId !== expected.pin.serverId || entry?.signIn !== 'portico' || !record || record.selectionId !== expected.selectionId || record.pendingRefresh || record.hosted?.origin !== this.original.hostedOrigin || record.hosted.accountId !== this.original.session.account.id || record.pin.publicKey !== expected.pin.publicKey || record.pin.fingerprint !== expected.pin.fingerprint || record.session.accessToken !== expected.session.accessToken || record.session.tokenGeneration !== expected.session.tokenGeneration || record.session.sessionFamilyId !== expected.session.sessionFamilyId || JSON.stringify(record.session.viewer) !== JSON.stringify(expected.session.viewer) || record.session.expiresAt !== expected.session.expiresAt || record.session.authorizationHorizon !== expected.session.authorizationHorizon) ended();
    }
    if (!this.current()) ended();checkDeadline(this.original);signal?.throwIfAborted();
  }
  async withUIRead<T>(work: () => Promise<T>, signal?: AbortSignal): Promise<T> {
    await this.assertUIOriginal(signal);
    const result = await work();
    await this.assertUIOriginal(signal);
    return result;
  }
  async openServerUI(signal: AbortSignal) {
    if (!this.original.server) ended();
    await this.assertOriginal(signal);
    const api = await originalServer(this.pending, signal, () => this.withUIRead(async () => undefined, signal));
    const read = api.request.bind(api);
    // Check after the complete JSON/body read too, not only after fetch headers.
    // This API belongs only to the readonly original workspace; credential ACKs
    // use the independent mutation client and are never post-filtered here.
    api.request = <T>(path: string, method = 'GET', body?: unknown, input?: AbortSignal): Promise<T> => {
      const capturedBody = body === undefined ? undefined : JSON.parse(JSON.stringify(body));
      const scoped = input ? AbortSignal.any([signal, input]) : signal;
      return this.withUIRead(() => read<T>(path, method, capturedBody, scoped), scoped);
    };
    try {await this.assertUIOriginal(signal);} catch (error) {api.getRouteConnection()?.dispose();throw error;}
    return {api, session: this.original.server.session};
  }
  async withToken<T>(work: (token: string) => Promise<T>, signal?: AbortSignal): Promise<T> {
    await this.assertOriginal(signal);
    return work(this.original.session.accessToken);
  }
  /** An accepted terminal mutation has already revoked this family. Remove
   * only its unchanged coordination record; never sign out a successor/server. */
  async terminal() {
    this.live = false;
    const key = 'portico.account.v1:' + this.original.hostedOrigin;
    try {await navigator.locks.request(key, async () => {
      let stamp: string | undefined;try {stamp=securityCoordinatorStamp(this.original.hostedOrigin);}catch {return;}
      if (stamp !== this.original.coordinator) return;
      const value = JSON.parse(this.original.coordinator) as {version: 1; revocations: unknown[]};
      localStorage.setItem(key, JSON.stringify({version: 1, revocations: value.revocations}));
    });} finally {await this.journal.clear(this.pending);}
  }
  async close() {
    this.live = false;
    await cancelOriginalAccountSecurity(this.pending, this.journal);
  }
  static async complete(pending: AccountSecurityPending, factor: string, signal: AbortSignal, journal = browserSecurityJournal()) {
    const captured = parseSecurityJournal(pending);
    if (!('original' in captured)) ended();
    pending = captured;
    const original = pending.original, client = originalSecurityClient(original.hostedOrigin);
    checkDeadline(original); await journal.assertOriginal(pending); signal.throwIfAborted();
    let proof: RecentProof;
    if (original.server) {
      if (!pending.returned || !pending.context || !pending.prepare || !pending.verifier) ended();
      const returned = {code: pending.returned.code, transactionId: pending.returned.transactionId, state: pending.state!};
      await verifySecurityContext(pending.context, original.server.pin, {prepare: pending.prepare, hostedOrigin: original.hostedOrigin, accountId: original.session.account.id});
      await verifySecurityConfirmation(pending.returned.confirmation, original.server.pin, pending.context, returned);
      const api = await originalServer(pending, signal); api.getRouteConnection()?.dispose();
      proof = await client.completeSecurityReturn(returned.transactionId, {code: returned.code, verifier: pending.verifier, confirmation: pending.returned.confirmation, factor}, identity(original), original.session.accessToken, signal);
      const confirmation: unknown = JSON.parse(new TextDecoder('utf-8', {fatal: true}).decode(routeBytes(pending.returned.confirmation.payload, undefined, 6144)));
      if (!confirmation || typeof confirmation !== 'object' || !('expiresAt' in confirmation) || typeof confirmation.expiresAt !== 'string' || Date.parse(proof.expiresAt) > Date.parse(confirmation.expiresAt)) ended();
    } else {
      if (!pending.transactionId) ended();
      const tx = await client.transaction(pending.transactionId, signal);
      if (!tx || tx.mode !== 'reauthenticate' || tx.securityReturn || tx.provider !== original.provider.provider || !['verified', 'proof_issued'].includes(tx.status)) ended();
      const completed = await client.completeSecurity(tx.transactionId, factor, identity(original), original.session.accessToken, signal);
      if (!completed.recentProof || completed.securityResult) ended(); proof = completed.recentProof;
    }
    signal.throwIfAborted(); checkDeadline(original); await journal.assertOriginal(pending);
    if (Date.parse(proof.expiresAt) <= Date.now()) ended();
    return new OriginalProviderSecurity(pending, Object.freeze({...proof}), client, journal);
  }
}

export async function acceptSecurityReturn(href: string, signal: AbortSignal, journal = browserSecurityJournal(), onOriginal?: (record: AccountSecurityPending) => void) {
  const record = await journal.read();
  if (!record || !('original' in record) || record.original.kind !== 'server-account' || !record.prepare || !record.context || !record.state || !record.intentId) ended();
  const original = record.original, returned = parseSecurityReturnCode(href, {origin: record.prepare.origin, state: record.state});
  if (record.transactionId && record.transactionId !== returned.transactionId) ended();
  onOriginal?.(record);signal.throwIfAborted();
  const api = await originalServer(record, signal);
  try {
    const context = await verifySecurityContext(record.context, original.server!.pin, {prepare: record.prepare, hostedOrigin: original.hostedOrigin, accountId: original.session.account.id});
    if (context.origin !== location.origin) ended();
    const confirmation = await api.request(routePath('post_direct_security_return_confirm'), 'POST', routeBody('post_direct_security_return_confirm', {context: record.context, transactionId: returned.transactionId, code: returned.code}), signal);
    const checked = await verifySecurityConfirmation(confirmation, original.server!.pin, record.context, returned);
    const next = {...record, transactionId: returned.transactionId, returned: {code: returned.code, transactionId: returned.transactionId, confirmation: checked}};
    return await journal.replace(record, next, () => {signal.throwIfAborted(); checkDeadline(original);}) as AccountSecurityPending;
  } finally {api.getRouteConnection()?.dispose();}
}

export async function cancelOriginalAccountSecurity(pending: AccountSecurityPending, journal = browserSecurityJournal()) {
  const captured = parseSecurityJournal(pending, true);
  if (!('original' in captured)) ended();
  pending = captured;
  const original = captured.original, client = originalSecurityClient(original.hostedOrigin);
  // Recover only this operation's own published receipts, never another opening.
  try {const saved = await journal.read(); if (saved && 'original' in saved && JSON.stringify(saved.original) === JSON.stringify(original)) pending = saved;} catch {/* Expired originals can still be retired locally. */}
  // These requests refer only to captured original receipts. They never cancel
  // a binding, discover a current transaction or refresh an ended session.
  const requests: Promise<unknown>[] = [];
  if (pending.intentId) requests.push(client.cancelSecurityReturn(pending.intentId, original.session.accessToken));
  else if (pending.transactionId && original.kind === 'hosted-account') requests.push(client.cancelSecurityTransaction(pending.transactionId));
  if (pending.context && original.server) requests.push((async () => {
    const api = await originalServer(pending);
    try {await api.request(routePath('post_direct_security_return_cancel'), 'POST', routeBody('post_direct_security_return_cancel', {context: pending.context}));}
    finally {api.getRouteConnection()?.dispose();}
  })());
  await Promise.allSettled(requests);
  await journal.retireAccount(original);
}
