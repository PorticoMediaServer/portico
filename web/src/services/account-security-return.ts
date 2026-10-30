import {routeBase64, routeBytes, webRouteCrypto} from '@core/route-identity.ts';
import {verifySecurityContext, type CredentialSecurityPurpose, type SecurityPrepare} from '@core/security-return.ts';
import type {HostedSecurityReturnStart} from '@core/hosted-security.ts';
import {originalSecurityClient} from '../platform/account-security';
import {browserSecurityJournal, parseSecurityJournal, type AccountSecurityJournal, type HostedSecurityBridge} from '../platform/account-security-journal';

export type HostedSecurityArrival = Readonly<{kind: 'begin'; intentId: string} | {kind: 'transaction'; transactionId: string} | {kind: 'invalid'}>;
/** A URL is only a hint. Scrub both references before initialization/storage or
 * any cookie coordinator; protected original state chooses the consumer. */
export function captureHostedSecurityArrival(page: Pick<Location, 'href'>, history: Pick<History, 'replaceState'>): HostedSecurityArrival | undefined {
  const url = new URL(page.href);
  if (url.pathname !== '/account' || !url.searchParams.has('securityIntent') && !url.searchParams.has('identityTransaction')) return;
  const intent = url.searchParams.get('securityIntent'), transaction = url.searchParams.get('identityTransaction');
  const keys = [...url.searchParams.keys()];
  const valid = !url.hash && !url.username && !url.password && !keys.some(k => !['securityIntent', 'identityTransaction', 'tab'].includes(k)) && keys.filter(k => k === 'tab').length <= 1;
  const begin = valid && keys.filter(k => k === 'securityIntent').length === 1 && transaction === null && /^sri_[A-Za-z0-9_-]{43}$/.test(intent ?? '') && url.searchParams.toString() === 'securityIntent=' + intent;
  const callback = valid && intent === null && keys.filter(k => k === 'identityTransaction').length === 1 && /^oid_[A-Za-z0-9_-]{43}$/.test(transaction ?? '') && (!url.searchParams.has('tab') || url.searchParams.get('tab') === 'security');
  history.replaceState(null, '', '/account?tab=security');
  return begin ? Object.freeze({kind: 'begin', intentId: intent!}) : callback ? Object.freeze({kind: 'transaction', transactionId: transaction!}) : Object.freeze({kind: 'invalid'});
}

function invalid(): never {throw new Error('This account security continuation no longer matches its original opening.');}
/** Hosted already checks this key against its original installed registration.
 * Here the independently signed display must agree with every returned field
 * before the browser accepts a navigation target from its eventual receipt. */
async function verifyStart(started: HostedSecurityReturnStart, hostedOrigin: string) {
  let c: unknown;
  try {c = JSON.parse(new TextDecoder('utf-8', {fatal: true}).decode(routeBytes(started.context.payload, undefined, 6144)));} catch {invalid();}
  if (!c || typeof c !== 'object' || Array.isArray(c)) invalid();
  const raw = c as Record<string, unknown>;
  const string = (key: string) => {if (typeof raw[key] !== 'string') invalid(); return raw[key] as string;};
  const key = string('publicKey'), fingerprint = routeBase64(await webRouteCrypto.sha256(routeBytes(key, 32)));
  const prepare: SecurityPrepare = {purpose: string('purpose') as CredentialSecurityPurpose, target: string('target'), origin: started.registeredOrigin,
    openingId: string('openingId'), taskId: string('taskId'), operationId: string('operationId'), providerIdentityId: string('providerIdentityId'), providerRevision: string('providerRevision'), nonce: string('nonce'), expiresAt: string('expiresAt')};
  return verifySecurityContext(started.context, {serverId: string('serverId'), publicKey: key, fingerprint}, {prepare, hostedOrigin, accountId: string('accountId')});
}

export async function beginHostedSecurityBridge(intentId: string, signal: AbortSignal, journal = browserSecurityJournal(), beforeNavigate?: () => void, onOriginal?: (record: HostedSecurityBridge) => void) {
  journal.assertAvailable();
  if (!/^sri_[A-Za-z0-9_-]{43}$/.test(intentId) || location.protocol !== 'https:') invalid();
  const before = await journal.read(), now = Date.now(); signal.throwIfAborted();
  let record: HostedSecurityBridge;
  if (before) {
    if ('original' in before || before.intentId !== intentId || before.hostedOrigin !== location.origin) invalid();
    record = before;
  } else {
    record = await journal.create({version: 1, kind: 'hosted-bridge', intentId, requestId: crypto.randomUUID(), hostedOrigin: location.origin, createdAt: new Date(now).toISOString(), expiresAt: new Date(now + 300000).toISOString()}, () => signal.throwIfAborted()) as HostedSecurityBridge;
  }
  onOriginal?.(record);signal.throwIfAborted();
  const client = originalSecurityClient(record.hostedOrigin);
  // Retry only this stored request. Never choose the cookie's current operation.
  const started = await client.startSecurityReturn(record.intentId, record.requestId, signal);
  if (record.started) {
    const previous = record.started;
    if (previous.transactionId !== started.transactionId || previous.expiresAt !== started.expiresAt || previous.mode !== started.mode || previous.authorizationUrl !== started.authorizationUrl || previous.provider !== started.provider || previous.intentId !== started.intentId || previous.registeredOrigin !== started.registeredOrigin || previous.state !== started.state || previous.context.payload !== started.context.payload || previous.context.signature !== started.context.signature) invalid();
  }
  await verifyStart(started, record.hostedOrigin); signal.throwIfAborted();
  record = await journal.replace(record, {...record, started}, () => signal.throwIfAborted()) as HostedSecurityBridge;
  onOriginal?.(record);
  await journal.assertOriginal(record); signal.throwIfAborted();
  if (!started.authorizationUrl) invalid();
  beforeNavigate?.();location.assign(started.authorizationUrl);
}

export async function finishHostedSecurityBridge(record: HostedSecurityBridge, transactionId: string, signal: AbortSignal, journal: AccountSecurityJournal = browserSecurityJournal(), beforeNavigate?: () => void) {
  const captured = parseSecurityJournal(record);
  if ('original' in captured) invalid();
  record = captured;
  const started = record.started;
  if (!started || started.transactionId !== transactionId || record.hostedOrigin !== location.origin) invalid();
  signal.throwIfAborted(); await journal.assertOriginal(record);
  await verifyStart(started, record.hostedOrigin);
  const client = originalSecurityClient(record.hostedOrigin), tx = await client.transaction(transactionId, signal);
  if (!tx || tx.mode !== 'reauthenticate' || tx.securityReturn !== true || tx.provider !== started.provider || !['verified', 'proof_issued'].includes(tx.status)) invalid();
  const returned = await client.finishSecurityReturn(started, signal);
  await journal.assertOriginal(record); signal.throwIfAborted();
  await journal.clear(record); signal.throwIfAborted();
  beforeNavigate?.();location.assign(returned);
}

export async function cancelHostedSecurityBridge(record: HostedSecurityBridge, journal = browserSecurityJournal()) {
  const captured = parseSecurityJournal(record, true);
  if ('original' in captured) invalid();
  record = captured;
  const expected = JSON.stringify({...captured, started: undefined});
  try {
    const saved = await journal.read(true);
    if (saved && !('original' in saved) && JSON.stringify({...saved, started: undefined}) === expected) record = saved;
  } catch {/* Expired exact local cleanup remains possible. */}
  if (record.started) await originalSecurityClient(record.hostedOrigin).cancelSecurityTransaction(record.started.transactionId).catch(() => undefined);
  await journal.clear(record);
}
