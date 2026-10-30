import {ApiError} from '@core/index.ts';
import {currentUTCTimestamp} from '@core/local-session.ts';
import {routeBytes, routeOrigin} from '@core/route-identity.ts';
import {decodeServerClaimLookupResponse, decodeServerClaimApproveCodeResponse, decodeServerClaimDenyResponse, decodeServerReturnCompleteResponse} from '../../../packages/contracts/src/hosted/decoders.ts';
import {routePath, encodedRoutePath} from '../../../packages/contracts/src/hosted/routes.ts';
import {accountIdentity, currentAccountWork, type AccountIdentity, type ServicesAccount} from './session';

/** The code identifies an already registered signed initial setup start. URL name and
 * operation hints grant no authority; presentation uses authenticated lookup. */
export type HostedSetupClaimLink = Readonly<{hostedOrigin: string; code: string; operation: string; nameHint: string}>;
export type HostedSetupClaimCapture = Readonly<{request?: HostedSetupClaimLink}>;
export type HostedSetupClaimReview = Readonly<{
  request: HostedSetupClaimLink; identity: AccountIdentity; serverId: string; name: string; address: string;
  transactionId: string; requestedAt: string; expiresAt: string; status: 'pending' | 'approved';
  /** The claim is installed (Hosted `setupInstalled`); only then is Return offered. */
  installed: boolean;
}>;
const invalid = () => new ApiError(400, 'invalid_return', 'invalid_return', false);
const unavailable = () => new ApiError(409, 'claim_stale', 'claim_stale', false);
const identifier = (value: string) => /^[A-Za-z0-9_-]{1,128}$/.test(value);
function claimCode(value: string) {
  if (!value.startsWith('claim_')) throw invalid();
  routeBytes(value.slice(6), 32);
  return value;
}
function ownerOrigin(value: string) {
  if (routeOrigin(value) !== value) throw invalid();
  const url = new URL(value);
  if (url.protocol !== 'https:' && !(url.protocol === 'http:' && url.hostname === 'localhost')) throw invalid();
  return value;
}
function fresh(review: Pick<HostedSetupClaimReview, 'expiresAt'>, now = Date.now()) {
  if (!currentUTCTimestamp(review.expiresAt) || Date.parse(review.expiresAt) <= now) throw unavailable();
}
function captureLink(value: HostedSetupClaimLink): HostedSetupClaimLink {
  return Object.freeze({hostedOrigin: value.hostedOrigin, code: value.code, operation: value.operation, nameHint: value.nameHint});
}
/** Validate persisted link scalars without trusting its display hint or issuing work. */
export function checkedHostedSetupClaimLink(value: HostedSetupClaimLink, expectedHostedOrigin: string): HostedSetupClaimLink {
  if (typeof value?.hostedOrigin !== 'string' || value.hostedOrigin !== expectedHostedOrigin || !expectedHostedOrigin.startsWith('https://') ||
      routeOrigin(expectedHostedOrigin) !== expectedHostedOrigin || typeof value.code !== 'string' || typeof value.operation !== 'string' ||
      !identifier(value.operation) || typeof value.nameHint !== 'string' || !value.nameHint || new TextEncoder().encode(value.nameHint).length > 120) throw invalid();
  claimCode(value.code);
  return captureLink(value);
}
function captureReview(value: HostedSetupClaimReview): HostedSetupClaimReview {
  return Object.freeze({request: captureLink(value.request), identity: Object.freeze({accountId: value.identity.accountId, familyId: value.identity.familyId}),
    serverId: value.serverId, name: value.name, address: value.address, transactionId: value.transactionId,
    requestedAt: value.requestedAt, expiresAt: value.expiresAt, status: value.status,
    installed: value.installed === true});
}

/** Synchronous capture only: no store, session restoration or request starts. */
export function captureHostedSetupClaim(page: Pick<Location, 'href' | 'pathname' | 'search' | 'hash'>,
  history: Pick<History, 'state' | 'replaceState'>, expectedHostedOrigin: string): HostedSetupClaimCapture | undefined {
  if (page.pathname !== '/claim' || !page.hash) return;
  const href = page.href;
  history.replaceState(history.state, '', page.pathname + page.search);
  try {
    const url = new URL(href), fields = new URLSearchParams(url.hash.slice(1));
    if (href.length > 2048 || !expectedHostedOrigin.startsWith('https://') || routeOrigin(expectedHostedOrigin) !== expectedHostedOrigin ||
        url.origin !== expectedHostedOrigin || url.pathname !== '/claim' || url.search || url.username || url.password ||
        [...fields.keys()].sort().join(',') !== 'code,operation,server') throw invalid();
    const code = fields.get('code') ?? '', operation = fields.get('operation') ?? '', nameHint = fields.get('server') ?? '';
    return {request: checkedHostedSetupClaimLink({hostedOrigin: expectedHostedOrigin, code, operation, nameHint}, expectedHostedOrigin)};
  } catch {return {};}
}

async function lookup(account: ServicesAccount, request: HostedSetupClaimLink, signal: AbortSignal, expected?: AccountIdentity) {
  if (account.api.origin !== request.hostedOrigin || !request.hostedOrigin.startsWith('https://') ||
      routeOrigin(request.hostedOrigin) !== request.hostedOrigin || !identifier(request.operation)) throw invalid();
  claimCode(request.code); signal.throwIfAborted();
  return currentAccountWork(account, async session => {
    signal.throwIfAborted();
    const raw = await account.api.send(routePath('server_claim_lookup'), 'POST', {code: request.code}, signal, session.accessToken);
    signal.throwIfAborted();
    const claim = decodeServerClaimLookupResponse(raw), now = Date.now();
    if (claim.code !== request.code || claim.transitionMode !== undefined || claim.purpose !== 'setup' || claim.ownerTransactionId !== undefined || !claim.setupTransactionId || !identifier(claim.setupTransactionId) ||
        claim.status !== 'pending' && claim.status !== 'approved' || claim.name !== request.nameHint || !claim.name ||
        new TextEncoder().encode(claim.name).length > 120 || !claim.serverId.startsWith('srv_') ||
        !currentUTCTimestamp(claim.requestedAt) || Date.parse(claim.requestedAt) > now + 60000 ||
        !currentUTCTimestamp(claim.expiresAt) || Date.parse(claim.expiresAt) - now > 300000 ||
        Date.parse(claim.expiresAt) <= Date.parse(claim.requestedAt) || claim.currentOwnerUsername !== undefined || claim.previousOwnerRole !== undefined) throw invalid();
    routeBytes(claim.serverId.slice(4), 32);
    ownerOrigin(claim.address);
    if (claim.otherPeopleCount !== undefined) throw invalid();
    // Setup has no return registration; `setupInstalled` is strictly true, only on approval.
    if (claim.returnRegistration !== undefined || claim.setupInstalled !== undefined && (claim.setupInstalled !== true || claim.status !== 'approved')) throw invalid();
    const review: HostedSetupClaimReview = Object.freeze({request, identity: Object.freeze(accountIdentity(session)),
      serverId: claim.serverId, name: claim.name, address: claim.address, transactionId: claim.setupTransactionId,
      requestedAt: claim.requestedAt, expiresAt: claim.expiresAt, status: claim.status,
      installed: claim.setupInstalled === true});
    fresh(review, now);
    return review;
  }, expected);
}
/** Lookup never approves, creates a return, or exchanges a credential. */
export function reviewHostedSetupClaim(account: ServicesAccount, request: HostedSetupClaimLink, signal: AbortSignal) {
  // Capture scalars before the first await, including caller-owned link objects.
  return lookup(account, captureLink(request), signal);
}
function sameClaim(prior: HostedSetupClaimReview, current: HostedSetupClaimReview) {
  if (prior.serverId !== current.serverId || prior.name !== current.name || prior.address !== current.address ||
      prior.transactionId !== current.transactionId || prior.requestedAt !== current.requestedAt || prior.expiresAt !== current.expiresAt ||
      prior.installed && !current.installed) throw invalid();
}

/** Explicit approval/denial uses the same current Hosted account and family
 * that reviewed the immutable claim. There is no replacement-server option. */
export async function decideHostedSetupClaim(account: ServicesAccount, review: HostedSetupClaimReview, decision: 'approve' | 'deny', signal: AbortSignal) {
  review = captureReview(review);
  if (decision !== 'approve' && decision !== 'deny') throw invalid();
  fresh(review);
  const current = await lookup(account, review.request, signal, review.identity);
  sameClaim(review, current);
  if (current.status !== 'pending') throw unavailable();
  await currentAccountWork(account, async session => {
    signal.throwIfAborted(); fresh(current);
    const raw = await account.api.send(routePath(decision === 'approve' ? 'server_claim_approve_code' : 'server_claim_deny'), 'POST', {code: current.request.code}, signal, session.accessToken);
    signal.throwIfAborted(); fresh(current);
    const result = decision === 'approve' ? decodeServerClaimApproveCodeResponse(raw) : decodeServerClaimDenyResponse(raw);
    if (result.status !== 'decided' || result.code !== 'claim_decided') throw invalid();
  }, review.identity);
}

/** The private producer returns to the original signed address, while its
 * HTTPS registration, once attached, may be a different managed address. Never
 * substitute that registration, a URL hint or an arbitrary return parameter. */
export function validateHostedSetupReturn(location: string, review: Pick<HostedSetupClaimReview, 'address' | 'transactionId'>) {
  if (!identifier(review.transactionId)) throw invalid();
  const origin = ownerOrigin(review.address);
  if (location !== origin + '/auth/portico-return#transaction=' + encodeURIComponent(review.transactionId)) throw invalid();
  return location;
}

/** Explicit completion only after lookup reports the claim installed.
 * No polling, ordinary Start, exchange or Server session write. */
export async function completeHostedSetupClaim(account: ServicesAccount, review: HostedSetupClaimReview, signal: AbortSignal) {
  review = captureReview(review);
  fresh(review);
  const current = await lookup(account, review.request, signal, review.identity);
  sameClaim(review, current);
  if (current.status !== 'approved' || !current.installed) return;
  const location = await currentAccountWork(account, async session => {
    signal.throwIfAborted(); fresh(current);
    const raw = await account.api.send(encodedRoutePath('server_return_complete', {id: encodeURIComponent(current.transactionId)}), 'POST', {}, signal, session.accessToken);
    signal.throwIfAborted(); fresh(current);
    return validateHostedSetupReturn(decodeServerReturnCompleteResponse(raw).location, current);
  }, review.identity);
  signal.throwIfAborted(); fresh(current);
  return location;
}
