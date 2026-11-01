import {parseAccountSession, parseSecurityState, type IdentityProvider, type SecurityState} from '@core/hosted-security.ts';
import type {HostedSession} from '@core/index.ts';
import {currentUTCTimestamp} from '@core/local-session.ts';
import {parseBrowserCredentialRecord} from '@core/credential-format.ts';
import {routeBase64, routeBytes, routeOrigin} from '@core/route-identity.ts';
import {secureBrowserPending, type SecurePendingStorage} from './secure-pending';

export type CredentialSecurityPurpose = 'password_set_or_change' | 'email_change' | 'mfa_enroll' | 'mfa_disable' | 'recovery_codes';
export type OriginalSecurityAccount = Readonly<{
  version: 1; kind: 'hosted-account';
  hostedOrigin: string; session: HostedSession; coordinator: string; state: SecurityState;
  openingId: string; taskId: string; requestId: string;
  intent: Readonly<{purpose: CredentialSecurityPurpose; target: string; operationId: string}>;
  provider: Readonly<{id: string; provider: IdentityProvider; revision: string}>;
  createdAt: string; expiresAt: string;
}>;
export type AccountSecurityPending = Readonly<{
  original: OriginalSecurityAccount;
  transactionId?: string;
}>;
const object = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
function invalid(): never {throw new Error('This account security continuation no longer matches its original opening.');}
const id = (v: unknown, min = 16): string => typeof v === 'string' && v.length >= min && v.length <= 128 && /^[A-Za-z0-9_-]+$/.test(v) ? v : invalid();
const exactKeys = (v: Record<string, unknown>, required: readonly string[], optional: readonly string[] = []) => {
  if (required.some(k => !Object.hasOwn(v, k)) || Object.keys(v).some(k => !required.includes(k) && !optional.includes(k))) invalid();
};
const origin = (v: unknown): string => typeof v === 'string' && v.startsWith('https://') && routeOrigin(v) === v ? v : invalid();
const deadline = (v: unknown, allowExpired: boolean): string => {
  if (typeof v !== 'string' || v.length > 64 || !currentUTCTimestamp(v) || !v.endsWith('Z') || !allowExpired && Date.parse(v) <= Date.now()) invalid();
  return v;
};
const reference = (v: unknown, prefix: string) => typeof v === 'string' && new RegExp('^' + prefix + '_[A-Za-z0-9_-]{43}$').test(v) ? v : invalid();
const copy = <T,>(v: T): T => JSON.parse(JSON.stringify(v)) as T;
function frozen<T>(v: T): T {
  if (v && typeof v === 'object') {for (const item of Object.values(v)) frozen(item); Object.freeze(v);}
  return v;
}
const fingerprint = (v: AccountSecurityPending) => JSON.stringify(v);
export const securityRecordId = (v: AccountSecurityPending) => v.original.openingId;

/** Validate the encrypted journal as data. It never discovers another opening,
 * renews credentials, or treats a URL hint as a source of account authority. */
export function parseSecurityJournal(raw: unknown, allowExpired = false): AccountSecurityPending {
  if (!object(raw)) invalid();
  // Snapshot getters/caller-owned data once before validation. All subsequent
  // reads use this plain, private JSON value.
  try {raw = copy(raw);} catch {invalid();}
  if (!object(raw)) invalid();
  exactKeys(raw, ['original'], ['transactionId']);
  const a = raw.original;
  if (!object(a)) invalid();
  exactKeys(a, ['version', 'kind', 'hostedOrigin', 'session', 'coordinator', 'state', 'openingId', 'taskId', 'requestId', 'intent', 'provider', 'createdAt', 'expiresAt']);
  if (a.version !== 1 || a.kind !== 'hosted-account' || !object(a.session) || Object.hasOwn(a.session, 'refreshToken') || typeof a.coordinator !== 'string' || a.coordinator.length > 131072 || !object(a.intent) || !object(a.provider)) invalid();
  const session = parseAccountSession(a.session, 'browser');
  const scope = {accountId: session.account.id, familyId: session.familyId!};
  const stored = parseBrowserCredentialRecord(JSON.parse(a.coordinator));
  if (!stored.active || stored.active.pendingRequestId || stored.active.session.account.id !== scope.accountId || stored.active.session.familyId !== scope.familyId || stored.active.session.expiresAt !== session.expiresAt || stored.revocations.some(v => v.familyId === scope.familyId)) invalid();
  exactKeys(a.intent, ['purpose', 'target', 'operationId']); exactKeys(a.provider, ['id', 'provider', 'revision']);
  if (!['password_set_or_change', 'email_change', 'mfa_enroll', 'mfa_disable', 'recovery_codes'].includes(String(a.intent.purpose)) || typeof a.intent.target !== 'string' || (a.intent.purpose === 'email_change' ? !a.intent.target || a.intent.target.length > 320 : a.intent.target !== '') || !['apple', 'google'].includes(String(a.provider.provider)) || typeof a.provider.revision !== 'string' || !/^[1-9][0-9]{0,18}$/.test(a.provider.revision) || BigInt(a.provider.revision) > 9223372036854775807n) invalid();
  const selectedProvider = a.provider;
  const state = parseSecurityState(a.state, scope), providerId = id(a.provider.id, 1);
  if (state.hasPassword || !state.providers.some(v => v.id === providerId && v.provider === selectedProvider.provider && v.revision === selectedProvider.revision)) invalid();
  const createdAt = deadline(a.createdAt, true), expiresAt = deadline(a.expiresAt, allowExpired);
  if (Date.parse(expiresAt) > Date.parse(createdAt) + 300000 || Date.parse(expiresAt) <= Date.parse(createdAt) || !allowExpired && Date.parse(createdAt) > Date.now() + 60000) invalid();
  if (Date.parse(expiresAt) > Date.parse(session.expiresAt)) invalid();
  const original: OriginalSecurityAccount = {version: 1, kind: 'hosted-account', hostedOrigin: origin(a.hostedOrigin), session, coordinator: a.coordinator, state,
    openingId: id(a.openingId), taskId: id(a.taskId), requestId: id(a.requestId), intent: {purpose: a.intent.purpose as CredentialSecurityPurpose, target: a.intent.target, operationId: id(a.intent.operationId)},
    provider: {id: providerId, provider: a.provider.provider as IdentityProvider, revision: a.provider.revision}, createdAt, expiresAt};
  const out: AccountSecurityPending = {original};
  const extra: {transactionId?: string} = {};
  if (raw.transactionId !== undefined) extra.transactionId = reference(raw.transactionId, 'oid');
  return frozen(copy({...out, ...extra}));
}

/** One encrypted, per-tab original; writes compare the complete predecessor and
 * recheck the mounted lifetime inside the final encrypted storage commit. */
export class AccountSecurityJournal {
  constructor(private readonly store: Pick<SecurePendingStorage<unknown>, 'read' | 'change' | 'assertAvailable'>) {}
  assertAvailable() {this.store.assertAvailable();}
  async read(allowExpired = false) {const raw = await this.store.read(); return raw === undefined ? undefined : parseSecurityJournal(raw, allowExpired);}
  async create(value: AccountSecurityPending, assertCurrent: () => void) {
    const next = parseSecurityJournal(value);
    const commit = () => {parseSecurityJournal(next); assertCurrent();};
    await this.store.change(old => {commit(); if (old !== undefined) invalid(); return next;}, commit);
    return next;
  }
  async replace(previous: AccountSecurityPending, next: AccountSecurityPending, assertCurrent: () => void) {
    // Capture each canonical complete operand before the storage call. Never
    // reread a caller-owned predecessor after waiting for a lock/encryption.
    const predecessor = parseSecurityJournal(previous, true), expected = fingerprint(predecessor);
    const value = parseSecurityJournal(next);
    if (securityRecordId(predecessor) !== securityRecordId(value) || JSON.stringify(predecessor.original) !== JSON.stringify(value.original)) invalid();
    const commit = () => {parseSecurityJournal(value); assertCurrent();};
    await this.store.change(old => {commit(); if (fingerprint(parseSecurityJournal(old)) !== expected) invalid(); return value;}, commit);
    return value;
  }
  async assertOriginal(expected: AccountSecurityPending) {
    const captured = fingerprint(parseSecurityJournal(expected));
    const value = await this.read();
    if (!value || fingerprint(value) !== captured) invalid();
  }
  async retireAccount(original: OriginalSecurityAccount) {
    const captured = parseSecurityJournal({original}, true);
    const expected = JSON.stringify(captured.original);
    await this.store.change(old => {
      if (old === undefined) return old;
      const current = parseSecurityJournal(old, true);
      return JSON.stringify(current.original) === expected ? undefined : old;
    });
  }
  async clear(expected: AccountSecurityPending) {
    const captured = fingerprint(parseSecurityJournal(expected, true));
    await this.store.change(old => {
      if (old === undefined) return old;
      // Expiry must not prevent exact cleanup, and a successor is never removed.
      return fingerprint(parseSecurityJournal(old, true)) === captured ? undefined : old;
    });
  }
}

export function browserSecurityJournal() {
  const guard = secureBrowserPending<unknown>('portico.account-security.guard'); guard.assertAvailable();
  const key = 'portico.account-security.tab.v1'; let tab = sessionStorage.getItem(key);
  if (tab !== null) routeBytes(tab, 32);
  else {tab = routeBase64(crypto.getRandomValues(new Uint8Array(32))); sessionStorage.setItem(key, tab);}
  return new AccountSecurityJournal(secureBrowserPending<unknown>('portico.account-security.v1:' + tab));
}

/** The coordinator record contains no credentials; comparing it is read-only.
 * Sensitive sends still use the captured access token and server-side family checks. */
/** Whether the browser's Portico Account coordination still holds the sign-in a Security return was captured
 * under: the same account and session family, no revocation of it, no request in flight. A routine refresh
 * rewrites the active session (its expiry) and keeps it; another sign-in, a sign-out or a revocation ends it. */
export function securityCoordinatorHolds(captured: string, current: string | null): boolean {
  try {
    if (current === null || current.length > 131072) return false;
    const was = parseBrowserCredentialRecord(JSON.parse(captured)), now = parseBrowserCredentialRecord(JSON.parse(current));
    const a = was.active, b = now.active;
    return Boolean(a && b) && !b!.pendingRequestId && b!.session.account.id === a!.session.account.id && b!.session.familyId === a!.session.familyId &&
      b!.authority.familyId === a!.authority.familyId && !now.revocations.some(v => v.familyId === a!.session.familyId) &&
      JSON.stringify(now.revocations) === JSON.stringify(was.revocations);
  } catch {return false;}
}
export function securityCoordinatorStillHolds(hostedOrigin: string, captured: string) {
  return securityCoordinatorHolds(captured, localStorage.getItem('portico.account.v1:' + origin(hostedOrigin)));
}

export function securityCoordinatorStamp(hostedOrigin: string) {
  const raw = localStorage.getItem('portico.account.v1:' + origin(hostedOrigin));
  if (raw === null || raw.length > 131072) invalid();
  return JSON.stringify(parseBrowserCredentialRecord(JSON.parse(raw)));
}

/** Opening Account Settings normally retires this tab's captured predecessor.
 * It clears only the exact encrypted record read here; no credential/network
 * action, current-transaction lookup or successor cleanup occurs. */
export async function retireOrdinarySecurityOpening() {
  if (!globalThis.isSecureContext || !globalThis.crypto?.subtle || !navigator.locks) return;
  const journal = browserSecurityJournal(), previous = await journal.read(true);
  if (previous) await journal.clear(previous);
}
