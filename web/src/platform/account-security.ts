import {HostedAccountClient, type AccountScope, type RecentProof, type SecurityState} from '@core/hosted-security.ts';
import type {HostedSession, LocalSession} from '@core/index.ts';
import {accountSecurityUIURL} from './account-security-policy';
import {BrowserAccountApi} from './account';
import {currentAccountWork, type AccountIdentity} from './account-work';
import type {ServicesAccount} from '../services/session';
import {browserSecurityJournal, parseSecurityJournal, securityCoordinatorStamp, securityCoordinatorStillHolds, type AccountSecurityJournal, type AccountSecurityPending, type CredentialSecurityPurpose, type OriginalSecurityAccount} from './account-security-journal';

/** A return starts only on a token with its whole window (five minutes) and a minute more left. */
export const CAPTURE_MIN_TOKEN_MS = 6 * 60_000;
function ended(): never {throw new Error('This account security continuation no longer matches its original opening.');}
const noRefresh = <T extends HostedSession | LocalSession>(session: T): T => {
  const {refreshToken: _unused, ...original} = session;
  return JSON.parse(JSON.stringify(original)) as T;
};
const checkDeadline = (original: OriginalSecurityAccount) => {
  // Bound to the Portico Account sign-in, not one stored session: another tab's routine refresh keeps it.
  if (Date.parse(original.expiresAt) <= Date.now() || !securityCoordinatorStillHolds(original.hostedOrigin, original.coordinator)) ended();
};
/** The latest a Security return may finish: five minutes after it opens, never past the Portico
 * Account session that opened it. A date that doesn't parse ends the return at once: a NaN
 * deadline would never compare as passed. */
export function securityReturnDeadline(now: number, accountExpiresAt: string): number {
  const account = Date.parse(accountExpiresAt);
  if (Number.isNaN(account)) return now;
  return Math.min(now + 300_000, account);
}
export function originalSecurityClient(origin: string) {
  // Inert transport only. The protected return never starts the singleton's
  // synchronize/accessSession/refresh/keepAlive or a sign-in admission path.
  const api = new BrowserAccountApi(origin);
  return new HostedAccountClient(origin, 'browser', (path, method, body, signal, token) => api.send(path, method, body, signal, token));
}
const identity = (o: OriginalSecurityAccount): AccountScope => ({accountId: o.session.account.id, familyId: o.session.familyId!});

/** Capture happens on the existing Account Settings opening before any prepare
 * request. A selected local profile is UI return context only. */
export async function captureAccountSecurity(input: {
  account: ServicesAccount; identity: AccountIdentity; state: SecurityState;
  purpose: CredentialSecurityPurpose; target: string; providerId: string;
  assertCurrent: () => void;
}, journal = browserSecurityJournal()): Promise<AccountSecurityPending> {
  journal.assertAvailable(); input.assertCurrent();
  const account = input.account, expectedIdentity = Object.freeze({...input.identity}), assertCurrent = input.assertCurrent, providerId = input.providerId, hostedOrigin = input.account.api.origin;
  const state: SecurityState = JSON.parse(JSON.stringify(input.state)), purpose = input.purpose, target = input.target;
  const provider = state.providers.find(v => v.id === providerId);
  if (state.hasPassword || !provider?.revision || state.accountId !== expectedIdentity.accountId || state.familyId !== expectedIdentity.familyId) ended();
  const capture = () => currentAccountWork(account, async session => {assertCurrent(); return noRefresh(session);}, expectedIdentity);
  let captured = await capture();
  // The return never refreshes on its own and lasts five minutes on the token it starts with: a token with less
  // than that (and a little) left is renewed the ordinary way first, so the return gets its whole window.
  if (Date.parse(captured.expiresAt) - Date.now() < CAPTURE_MIN_TOKEN_MS) {
    await account.service.accessSession(true);
    assertCurrent();
    captured = await capture();
  }
  assertCurrent();
  const coordinator = securityCoordinatorStamp(hostedOrigin), now = Date.now(), expiry = securityReturnDeadline(now, captured.expiresAt);
  const original: OriginalSecurityAccount = {version: 1, kind: 'hosted-account', hostedOrigin, session: captured, coordinator, state,
    openingId: crypto.randomUUID(), taskId: crypto.randomUUID(), requestId: crypto.randomUUID(), intent: {purpose, target, operationId: crypto.randomUUID()}, provider: {...provider, revision: provider.revision}, createdAt: new Date(now).toISOString(), expiresAt: new Date(expiry).toISOString()};
  const pending: AccountSecurityPending = {original};
  return journal.create(pending, () => {assertCurrent(); checkDeadline(original);});
}

/** Exact retry of one recorded start. Every uncertain result keeps its original
 * request key. There is no latest transaction lookup. */
export async function leaveForSecurityProvider(pending: AccountSecurityPending, assertCurrent: () => void, journal = browserSecurityJournal(), beforeNavigate?: () => void) {
  const predecessor = parseSecurityJournal(pending);
  const originalFingerprint = JSON.stringify(predecessor.original);
  const saved = await journal.read();
  if (!saved || JSON.stringify(saved.original) !== originalFingerprint) ended();
  let held = saved;
  const check = () => {assertCurrent(); checkDeadline(held.original);};
  const client = originalSecurityClient(held.original.hostedOrigin), original = held.original;
  check(); await journal.assertOriginal(held); check();
  const started = await client.start(original.provider.provider, {mode: 'reauthenticate', requestId: original.requestId, intent: original.intent, expectedIdentity: {id: original.provider.id, revision: original.provider.revision}}, undefined, original.session.accessToken);
  if (started.mode !== 'reauthenticate' || !started.authorizationUrl || held.transactionId && held.transactionId !== started.transactionId) ended();
  check();
  held = await journal.replace(held, {...held, transactionId: started.transactionId}, check);
  check(); await journal.assertOriginal(held); check();
  beforeNavigate?.();location.assign(started.authorizationUrl);
  return held;
}

/** A verified provider proof remains memory-only. Canonical SecurityTask uses
 * this closed port, which always sends the captured access token and intent. */
export class OriginalProviderSecurity {
  private live = true;
  private constructor(readonly pending: AccountSecurityPending, readonly proof: RecentProof, readonly client: HostedAccountClient, private readonly journal: AccountSecurityJournal) {}
  get original() {return this.pending.original;}
  acceptsUIURL(href: string) {
    return accountSecurityUIURL(this.original.hostedOrigin, href);
  }
  current = () => this.live && Date.parse(this.original.expiresAt) > Date.now() && this.acceptsUIURL(location.href);
  private async assertOriginal(signal?: AbortSignal) {
    if (!this.current()) ended();
    signal?.throwIfAborted(); checkDeadline(this.original);
    await this.journal.assertOriginal(this.pending);
    signal?.throwIfAborted();
    if (!this.current()) ended(); checkDeadline(this.original);
  }
  async withUIRead<T>(work: () => Promise<T>, signal?: AbortSignal): Promise<T> {
    await this.assertOriginal(signal);
    const result = await work();
    await this.assertOriginal(signal);
    return result;
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
      // Only while it still holds this sign-in (a refresh may have rewritten its session): never a successor.
      if (!securityCoordinatorStillHolds(this.original.hostedOrigin, this.original.coordinator)) return;
      const value = JSON.parse(stamp) as {version: 1; revocations: unknown[]};
      localStorage.setItem(key, JSON.stringify({version: 1, revocations: value.revocations}));
    });} finally {await this.journal.clear(this.pending);}
  }
  async close() {
    this.live = false;
    await cancelOriginalAccountSecurity(this.pending, this.journal);
  }
  static async complete(pending: AccountSecurityPending, factor: string, signal: AbortSignal, journal = browserSecurityJournal()) {
    pending = parseSecurityJournal(pending);
    const original = pending.original, client = originalSecurityClient(original.hostedOrigin);
    checkDeadline(original); await journal.assertOriginal(pending); signal.throwIfAborted();
    let proof: RecentProof;
    if (!pending.transactionId) ended();
    const tx = await client.transaction(pending.transactionId, signal);
    if (!tx || tx.mode !== 'reauthenticate' || tx.provider !== original.provider.provider || !['verified', 'proof_issued'].includes(tx.status)) ended();
    const completed = await client.completeSecurity(tx.transactionId, factor, identity(original), original.session.accessToken, signal);
    if (!completed.recentProof || completed.securityResult) ended(); proof = completed.recentProof;
    signal.throwIfAborted(); checkDeadline(original); await journal.assertOriginal(pending);
    if (Date.parse(proof.expiresAt) <= Date.now()) ended();
    return new OriginalProviderSecurity(pending, Object.freeze({...proof}), client, journal);
  }
}

export async function cancelOriginalAccountSecurity(pending: AccountSecurityPending, journal = browserSecurityJournal()) {
  pending = parseSecurityJournal(pending, true);
  const original = pending.original, client = originalSecurityClient(original.hostedOrigin);
  // Recover only this operation's own published receipts, never another opening.
  try {const saved = await journal.read(); if (saved && JSON.stringify(saved.original) === JSON.stringify(original)) pending = saved;} catch {/* Expired originals can still be retired locally. */}
  // This request refers only to the captured original receipt. It never cancels
  // a binding, discovers a current transaction or refreshes an ended session.
  const requests: Promise<unknown>[] = [];
  if (pending.transactionId) requests.push(client.cancelSecurityTransaction(pending.transactionId));
  await Promise.allSettled(requests);
  await journal.retireAccount(original);
}
