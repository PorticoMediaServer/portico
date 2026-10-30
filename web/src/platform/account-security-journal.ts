import {parseAccountSession, parseSecurityState, type HostedSecurityReturnStart, type IdentityProvider, type SecurityState} from '@core/hosted-security.ts';
import type {HostedSession, LocalSession} from '@core/index.ts';
import {currentUTCTimestamp, parseLocalSession} from '@core/local-session.ts';
import {parseBrowserCredentialRecord} from '@core/credential-format.ts';
import {routeBase64, routeBytes, routeOrigin, type ServerPin} from '@core/route-identity.ts';
import {parseSecuritySigned, type CredentialSecurityPurpose, type SecurityPrepare, type SecuritySigned} from '@core/security-return.ts';
import {secureBrowserPending, type SecurePendingStorage} from './secure-pending';

export type OriginalSecurityServer = Readonly<{
  revision: number; selectionId: string; pin: ServerPin; session: LocalSession;
}>;
export type OriginalSecurityAccount = Readonly<{
  version: 1; kind: 'hosted-account' | 'server-account';
  hostedOrigin: string; session: HostedSession; coordinator: string; state: SecurityState;
  openingId: string; taskId: string; requestId: string;
  intent: Readonly<{purpose: CredentialSecurityPurpose; target: string; operationId: string}>;
  provider: Readonly<{id: string; provider: IdentityProvider; revision: string}>;
  createdAt: string; expiresAt: string; server?: OriginalSecurityServer;
}>;
export type AccountSecurityPending = Readonly<{
  original: OriginalSecurityAccount;
  transactionId?: string; context?: SecuritySigned; prepare?: SecurityPrepare;
  intentId?: string; state?: string; verifier?: string;
  returned?: Readonly<{code: string; transactionId: string; confirmation: SecuritySigned}>;
}>;
export type HostedSecurityBridge = Readonly<{
  version: 1; kind: 'hosted-bridge'; intentId: string; requestId: string;
  hostedOrigin: string; createdAt: string; expiresAt: string; started?: HostedSecurityReturnStart;
}>;
export type SecurityJournalRecord = AccountSecurityPending | HostedSecurityBridge;
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
const nonce = (v: unknown): string => {if (typeof v !== 'string') invalid(); routeBytes(v, 32); return v;};
const copy = <T,>(v: T): T => JSON.parse(JSON.stringify(v)) as T;
function frozen<T>(v: T): T {
  if (v && typeof v === 'object') {for (const item of Object.values(v)) frozen(item); Object.freeze(v);}
  return v;
}
const fingerprint = (v: SecurityJournalRecord) => JSON.stringify(v);
export const securityRecordId = (v: SecurityJournalRecord) => 'original' in v ? v.original.openingId : v.intentId;

/** Validate the encrypted journal as data. It never discovers another opening,
 * renews credentials, or treats a URL hint as a source of account authority. */
export function parseSecurityJournal(raw: unknown, allowExpired = false): SecurityJournalRecord {
  if (!object(raw)) invalid();
  // Snapshot getters/caller-owned data once before validation. All subsequent
  // reads use this plain, private JSON value.
  try {raw = copy(raw);} catch {invalid();}
  if (!object(raw)) invalid();
  if (raw.kind === 'hosted-bridge') {
    exactKeys(raw, ['version', 'kind', 'intentId', 'requestId', 'hostedOrigin', 'createdAt', 'expiresAt'], ['started']);
    if (raw.version !== 1) invalid();
    const out: HostedSecurityBridge = {version: 1, kind: 'hosted-bridge', intentId: reference(raw.intentId, 'sri'), requestId: id(raw.requestId), hostedOrigin: origin(raw.hostedOrigin), createdAt: deadline(raw.createdAt, true), expiresAt: deadline(raw.expiresAt, allowExpired)};
    if (Date.parse(out.expiresAt) > Date.parse(out.createdAt) + 300000 || Date.parse(out.expiresAt) <= Date.parse(out.createdAt) || !allowExpired && Date.parse(out.createdAt) > Date.now() + 60000) invalid();
    if (raw.started !== undefined) {
      if (!object(raw.started)) invalid();
      const s = raw.started;
      exactKeys(s, ['transactionId', 'expiresAt', 'mode', 'provider', 'intentId', 'registeredOrigin', 'state', 'context', 'authorizationUrl']);
      if (s.mode !== 'reauthenticate' || s.intentId !== out.intentId || !['apple', 'google'].includes(String(s.provider)) || typeof s.authorizationUrl !== 'string') invalid();
      // Provider navigation is independently validated by HostedAccountClient.
      const url = new URL(s.authorizationUrl);
      if (url.protocol !== 'https:' || url.username || url.password || url.port || url.hostname !== (s.provider === 'apple' ? 'appleid.apple.com' : 'accounts.google.com')) invalid();
      return frozen({...out, started: {transactionId: reference(s.transactionId, 'oid'), expiresAt: deadline(s.expiresAt, allowExpired), mode: 'reauthenticate', provider: s.provider as IdentityProvider, intentId: out.intentId, registeredOrigin: origin(s.registeredOrigin), state: nonce(s.state), context: parseSecuritySigned(s.context), authorizationUrl: s.authorizationUrl}});
    }
    return frozen(out);
  }
  exactKeys(raw, ['original'], ['transactionId', 'context', 'prepare', 'intentId', 'state', 'verifier', 'returned']);
  const a = raw.original;
  if (!object(a)) invalid();
  exactKeys(a, ['version', 'kind', 'hostedOrigin', 'session', 'coordinator', 'state', 'openingId', 'taskId', 'requestId', 'intent', 'provider', 'createdAt', 'expiresAt'], ['server']);
  if (a.version !== 1 || !['hosted-account', 'server-account'].includes(String(a.kind)) || !object(a.session) || Object.hasOwn(a.session, 'refreshToken') || typeof a.coordinator !== 'string' || a.coordinator.length > 131072 || !object(a.intent) || !object(a.provider)) invalid();
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
  const original: OriginalSecurityAccount = {version: 1, kind: a.kind as OriginalSecurityAccount['kind'], hostedOrigin: origin(a.hostedOrigin), session, coordinator: a.coordinator, state,
    openingId: id(a.openingId), taskId: id(a.taskId), requestId: id(a.requestId), intent: {purpose: a.intent.purpose as CredentialSecurityPurpose, target: a.intent.target, operationId: id(a.intent.operationId)},
    provider: {id: providerId, provider: a.provider.provider as IdentityProvider, revision: a.provider.revision}, createdAt, expiresAt};
  let server: OriginalSecurityServer | undefined;
  if (a.kind === 'server-account') {
    const v = a.server;
    if (!object(v)) invalid();
    exactKeys(v, ['revision', 'selectionId', 'pin', 'session']);
    if (!Number.isSafeInteger(v.revision) || Number(v.revision) < 0 || typeof v.selectionId !== 'string' || !/^[-_A-Za-z0-9]{32}$/.test(v.selectionId) || !object(v.pin) || !object(v.session) || Object.hasOwn(v.session, 'refreshToken')) invalid();
    exactKeys(v.pin, ['serverId', 'publicKey', 'fingerprint']);
    const local = parseLocalSession(v.session, {}, {stored: allowExpired});
    if (local.viewer.authority !== 'local' || local.viewer.serverId !== v.pin.serverId || Date.parse(expiresAt) > Math.min(Date.parse(local.expiresAt), Date.parse(local.authorizationHorizon))) invalid();
    const pin = {serverId: id(v.pin.serverId, 1), publicKey: nonce(v.pin.publicKey), fingerprint: nonce(v.pin.fingerprint)};
    server = {revision: Number(v.revision), selectionId: v.selectionId, pin, session: local};
  } else if (a.server !== undefined) invalid();
  const out: AccountSecurityPending = {original: {...original, ...(server ? {server} : {})}};
  const extra: {transactionId?: string; context?: SecuritySigned; prepare?: SecurityPrepare; intentId?: string; state?: string; verifier?: string; returned?: AccountSecurityPending['returned']} = {};
  if (raw.transactionId !== undefined) extra.transactionId = reference(raw.transactionId, 'oid');
  if (raw.context !== undefined) extra.context = parseSecuritySigned(raw.context);
  if (raw.intentId !== undefined) extra.intentId = reference(raw.intentId, 'sri');
  if (raw.state !== undefined) extra.state = nonce(raw.state);
  if (raw.verifier !== undefined) extra.verifier = nonce(raw.verifier);
  if (raw.prepare !== undefined) {
    const q = raw.prepare;
    if (!object(q)) invalid();
    exactKeys(q, ['purpose', 'target', 'origin', 'openingId', 'taskId', 'operationId', 'providerIdentityId', 'providerRevision', 'nonce', 'expiresAt']);
    if (q.purpose !== original.intent.purpose || q.target !== original.intent.target || q.openingId !== original.openingId || q.taskId !== original.taskId || q.operationId !== original.intent.operationId || q.providerIdentityId !== original.provider.id || q.providerRevision !== original.provider.revision || q.expiresAt !== original.expiresAt) invalid();
    extra.prepare = {purpose: original.intent.purpose, target: original.intent.target, origin: origin(q.origin), openingId: original.openingId, taskId: original.taskId, operationId: original.intent.operationId, providerIdentityId: original.provider.id, providerRevision: original.provider.revision, nonce: nonce(q.nonce), expiresAt};
  }
  if (raw.returned !== undefined) {
    const r = raw.returned;
    if (!object(r) || !server || !extra.context || !extra.intentId) invalid();
    exactKeys(r, ['code', 'transactionId', 'confirmation']);
    extra.returned = {code: reference(r.code, 'scr'), transactionId: reference(r.transactionId, 'oid'), confirmation: parseSecuritySigned(r.confirmation)};
    if (extra.transactionId !== extra.returned.transactionId) invalid();
  }
  if (!server && (extra.context || extra.prepare || extra.intentId || extra.state || extra.verifier || extra.returned)) invalid();
  if (extra.intentId && (!extra.context || !extra.prepare || !extra.state || !extra.verifier)) invalid();
  return frozen(copy({...out, ...extra}));
}

/** One encrypted, per-tab original; writes compare the complete predecessor and
 * recheck the mounted lifetime inside the final encrypted storage commit. */
export class AccountSecurityJournal {
  constructor(private readonly store: Pick<SecurePendingStorage<unknown>, 'read' | 'change' | 'assertAvailable'>) {}
  assertAvailable() {this.store.assertAvailable();}
  async read(allowExpired = false) {const raw = await this.store.read(); return raw === undefined ? undefined : parseSecurityJournal(raw, allowExpired);}
  async create(value: SecurityJournalRecord, assertCurrent: () => void) {
    const next = parseSecurityJournal(value);
    const commit = () => {parseSecurityJournal(next); assertCurrent();};
    await this.store.change(old => {commit(); if (old !== undefined) invalid(); return next;}, commit);
    return next;
  }
  async replace(previous: SecurityJournalRecord, next: SecurityJournalRecord, assertCurrent: () => void) {
    // Capture each canonical complete operand before the storage call. Never
    // reread a caller-owned predecessor after waiting for a lock/encryption.
    const predecessor = parseSecurityJournal(previous, true), expected = fingerprint(predecessor);
    const value = parseSecurityJournal(next);
    if (securityRecordId(predecessor) !== securityRecordId(value) || ('original' in predecessor ? !('original' in value) || JSON.stringify(predecessor.original) !== JSON.stringify(value.original) : 'original' in value || JSON.stringify({...predecessor, started: undefined}) !== JSON.stringify({...value, started: undefined}))) invalid();
    const commit = () => {parseSecurityJournal(value); assertCurrent();};
    await this.store.change(old => {commit(); if (fingerprint(parseSecurityJournal(old)) !== expected) invalid(); return value;}, commit);
    return value;
  }
  async assertOriginal(expected: SecurityJournalRecord) {
    const captured = fingerprint(parseSecurityJournal(expected));
    const value = await this.read();
    if (!value || fingerprint(value) !== captured) invalid();
  }
  async retireAccount(original: OriginalSecurityAccount) {
    const captured = parseSecurityJournal({original}, true);
    if (!('original' in captured)) invalid();
    const expected = JSON.stringify(captured.original);
    await this.store.change(old => {
      if (old === undefined) return old;
      const current = parseSecurityJournal(old, true);
      return 'original' in current && JSON.stringify(current.original) === expected ? undefined : old;
    });
  }
  async clear(expected: SecurityJournalRecord) {
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
