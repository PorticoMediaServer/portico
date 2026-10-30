import {ApiError} from '@core/index.ts';
import {decodeServerClaimLookupResponse} from '../../../packages/contracts/src/hosted/decoders.ts';
import {routePath} from '../../../packages/contracts/src/hosted/routes.ts';
import {routeBase64, routeBytes} from '@core/route-identity.ts';
import {BrowserProtectedStore} from '../platform/server-connections';
import {SecurePendingStorage} from '../platform/secure-pending';
import {servicesTab} from './intake';
import {currentAccountWork, sameAccount, accountIdentity, type ServicesAccount} from './session';
import {checkedHostedOwnerClaimLink, reviewHostedOwnerClaim, decideHostedOwnerClaim, completeHostedOwnerClaim,
  type HostedOwnerClaimCapture, type HostedOwnerClaimLink, type HostedOwnerClaimReview} from './owner-claim';

/** Hosted's approval-code intake journal, distinct from the initiating Server's owner
 * journal. It contains no verifier, Server family, credential or signed proof.
 * Keep this handle private; presentation uses authenticated review facts. */
export type HostedOwnerPending = Readonly<{version: 1; kind: 'owner-approval'; attemptId: string; capturedAt: number; deadline: number; request: HostedOwnerClaimLink}>;
export type HostedOwnerPendingStore = Pick<SecurePendingStorage<HostedOwnerPending>, 'assertAvailable' | 'read' | 'change'>;
const lifetime = 300000;
const invalid = () => new ApiError(400, 'invalid_return', 'invalid_return', false);
const changed = () => new ApiError(409, 'cancelled', 'cancelled', false);
function same(a: HostedOwnerPending | undefined, b: HostedOwnerPending) {
  return a?.version === 1 && a.kind === 'owner-approval' && a.attemptId === b.attemptId && a.capturedAt === b.capturedAt && a.request?.hostedOrigin === b.request.hostedOrigin &&
    a.request?.code === b.request.code && a.request?.operation === b.request.operation && a.request?.nameHint === b.request.nameHint;
}
function checked(value: HostedOwnerPending, origin: string, now: number): HostedOwnerPending {
  if (!Number.isSafeInteger(now) || value?.version !== 1 || value.kind !== 'owner-approval' || !Number.isSafeInteger(value.capturedAt) ||
      value.capturedAt > now || !Number.isSafeInteger(value.deadline) || value.deadline <= now || value.deadline > value.capturedAt + lifetime) throw invalid();
  routeBytes(value.attemptId, 32);
  return Object.freeze({version: 1, kind: 'owner-approval', attemptId: value.attemptId, capturedAt: value.capturedAt, deadline: value.deadline,
    request: checkedHostedOwnerClaimLink(value.request, origin)});
}

/** Factory is inert. The canonical secure/native-lock guard runs before even
 * creating a tab locator; there is no HTTP/plaintext or legacy-store fallback. */
export function hostedOwnerPendingStore(): HostedOwnerPendingStore {
  return new SecurePendingStorage<HostedOwnerPending>(() => new BrowserProtectedStore<HostedOwnerPending>('portico.services-owner-approval.v1:' + servicesTab(), 8192, true));
}

/** Persist only a scrubbed capture. Reload/provider navigation retains its
 * original local budget; the authenticated producer may only shorten it. */
export async function restoreHostedOwnerPending(store: HostedOwnerPendingStore, origin: string, capture?: HostedOwnerClaimCapture, now = Date.now()) {
  store.assertAvailable();
  if (capture) {
    if (!capture.request) {await store.change(() => undefined); throw invalid();}
    let request: HostedOwnerClaimLink;
    try {request = checkedHostedOwnerClaimLink(capture.request, origin);} catch (error) {await store.change(() => undefined); throw error;}
    const pending = checked({version: 1, kind: 'owner-approval', attemptId: routeBase64(crypto.getRandomValues(new Uint8Array(32))),
      capturedAt: now, deadline: now + lifetime, request}, origin, now);
    await store.change(() => pending);
    return current(store, pending);
  }
  const saved = await store.read();
  if (!saved) return;
  try {return checked(saved, origin, now);}
  catch (error) {await store.change(current => current?.attemptId === saved.attemptId ? undefined : current); throw error;}
}
async function current(store: HostedOwnerPendingStore, pending: HostedOwnerPending) {
  store.assertAvailable();
  const saved = await store.read();
  if (!same(saved, pending) || !saved) throw changed();
  return checked(saved, pending.request.hostedOrigin, Date.now());
}
export async function forgetHostedOwnerPending(store: HostedOwnerPendingStore, pending: HostedOwnerPending) {
  store.assertAvailable();
  let forgotten = false;
  await store.change(saved => {if (same(saved, pending)) {forgotten = true; return undefined;} return saved;});
  return forgotten;
}

/** Headless operation scope, using the browserAccount singleton supplied by the
 * caller. No automatic approval, completion, redirect or second session store. */
type ApprovalReview = Pick<HostedOwnerClaimReview, 'request' | 'identity' | 'expiresAt'>;
type ApprovalOperations<Review extends ApprovalReview> = Readonly<{
  review: (account: ServicesAccount, request: HostedOwnerClaimLink, signal: AbortSignal) => Promise<Review>;
  decide: (account: ServicesAccount, review: Review, decision: 'approve' | 'deny', signal: AbortSignal) => Promise<void>;
  complete: (account: ServicesAccount, review: Review, signal: AbortSignal) => Promise<string | undefined>;
}>;
export function scopedHostedOwnerClaim(account: ServicesAccount, store: HostedOwnerPendingStore, value: HostedOwnerPending) {
  return scopedHostedApproval(account, store, value, {review: reviewHostedOwnerClaim, decide: decideHostedOwnerClaim, complete: completeHostedOwnerClaim});
}
/** Code/currentness mechanics only. Purpose-closed validators are fixed by the
 * caller module, never by the URL or an untrusted pending scalar. */
export function scopedHostedApproval<Review extends ApprovalReview>(account: ServicesAccount, store: HostedOwnerPendingStore, value: HostedOwnerPending, operations: ApprovalOperations<Review>) {
  const pending = checked(value, account.api.origin, Date.now());
  let epoch = 0, active: AbortController | undefined, reviewed: Review | undefined, disposed = false;
  const operation = () => {
    if (disposed) throw changed();
    active?.abort();
    const controller = active = new AbortController(), generation = ++epoch;
    const check = () => {controller.signal.throwIfAborted(); if (disposed || generation !== epoch) throw changed();};
    const assert = async () => {check(); const saved = await current(store, pending); check(); return saved;};
    return {signal: controller.signal, check, assert};
  };
  return {
    async review() {
      const op = operation(); await op.assert();
      const result = await operations.review(account, pending.request, op.signal); await op.assert();
      await store.change(saved => {
        op.check();
        if (!same(saved, pending) || !saved) throw changed();
        const valid = checked(saved, pending.request.hostedOrigin, Date.now());
        const deadline = Math.min(valid.deadline, Date.parse(result.expiresAt));
        if (!Number.isSafeInteger(deadline) || deadline <= Date.now()) throw invalid();
        return {...valid, deadline};
      });
      await op.assert();
      reviewed = result;
      return result;
    },
    async decide(decision: 'approve' | 'deny') {
      const result = reviewed;
      if (!result) throw invalid();
      const op = operation(); await op.assert();
      await operations.decide(account, result, decision, op.signal); await op.assert();
      if (decision === 'deny') {if (!await forgetHostedOwnerPending(store, pending)) throw changed(); reviewed = undefined;}
      op.check();
    },
    async complete() {
      const result = reviewed;
      if (!result) throw invalid();
      const op = operation(); await op.assert();
      const location = await operations.complete(account, result, op.signal); await op.assert();
      if (!location) return;
      // A transient final account recheck must not spend the original protected
      // attempt before its return can be delivered. Retry remains this operation.
      await currentAccountWork(account, async () => {op.check();}, result.identity);
      const finalPending = await op.assert();
      if (!await forgetHostedOwnerPending(store, pending)) throw changed();
      op.check();
      const session = account.service.getSnapshot().session;
      if (!session || !sameAccount(accountIdentity(session), result.identity)) throw new ApiError(409, 'account_changed', 'account_changed', false);
      if (Date.now() >= Math.min(finalPending.deadline, Date.parse(result.expiresAt))) throw changed();
      reviewed = undefined;
      return location;
    },
    async cancel() {if (disposed) return; ++epoch; active?.abort(); reviewed = undefined; await forgetHostedOwnerPending(store, pending);},
    // Document/provider navigation retires this instance, not its protected
    // pending code. Restoration rechecks the same fixed deadline and all facts.
    dispose() {disposed = true; ++epoch; active?.abort(); reviewed = undefined;},
  };
}

/** Select only a view from an authenticated lookup. This grants no approval or
 * completion: each selected controller independently validates its fixed purpose. */
export async function hostedApprovalPurpose(account: ServicesAccount, store: HostedOwnerPendingStore, pending: HostedOwnerPending, signal: AbortSignal): Promise<'setup'|'switch'> {
  if(account.api.origin!==pending.request.hostedOrigin)throw invalid();
  await current(store,pending); signal.throwIfAborted();
  const raw=await currentAccountWork(account,session=>account.api.send(routePath('server_claim_lookup'),'POST',{code:pending.request.code},signal,session.accessToken));
  signal.throwIfAborted();await current(store,pending);signal.throwIfAborted();
  const claim=decodeServerClaimLookupResponse(raw);
  if(claim.code!==pending.request.code)throw invalid();
  if(claim.purpose==='setup'&&claim.transitionMode===undefined&&claim.setupTransactionId&&claim.ownerTransactionId===undefined)return 'setup';
  if(claim.purpose==='switch'&&claim.ownerTransactionId&&claim.setupTransactionId===undefined)return 'switch';
  throw invalid();
}
