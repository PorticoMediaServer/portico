import {ApiError} from '@core/index.ts';
import {currentUTCTimestamp} from '@core/local-session.ts';
import {routeBytes, routeOrigin} from '@core/route-identity.ts';
import {decodeServerClaimLookupResponse, decodeServerClaimApproveCodeResponse, decodeServerClaimDenyResponse, decodeServerReturnCompleteResponse} from '../../../packages/contracts/src/hosted/decoders.ts';
import {routePath, encodedRoutePath} from '../../../packages/contracts/src/hosted/routes.ts';
import {accountIdentity, currentAccountWork, type AccountIdentity, type ServicesAccount} from './session';

/** The code identifies an already registered signed owner start. URL name and
 * operation hints grant no authority; presentation uses authenticated lookup. */
export type HostedOwnerClaimLink = Readonly<{hostedOrigin: string; code: string; operation: string; nameHint: string}>;
export type HostedOwnerClaimCapture = Readonly<{request?: HostedOwnerClaimLink}>;
export type HostedOwnerClaimReview = Readonly<{
  request: HostedOwnerClaimLink; identity: AccountIdentity; serverId: string; name: string; address: string;
  transactionId: string; requestedAt: string; expiresAt: string; status: 'pending' | 'approved';
  /** The signed claim's own reason; a plain switch carries no previous-owner fact. */
  reason: 'switch' | 'change-owner' | 'start-over'; transitionMode?: 'switch' | 'start-over'; previousOwnerRole?: 'admin' | 'member' | 'remove'; currentOwnerUsername?: string;
  /** Signed informational count of the other people on the server. Absent when
   * the producer's exact count is over its bound: never read as zero. */
  otherPeopleCount?: number;
  registration?: Readonly<{id: string; origin: string}>;
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
function fresh(review: Pick<HostedOwnerClaimReview, 'expiresAt'>, now = Date.now()) {
  if (!currentUTCTimestamp(review.expiresAt) || Date.parse(review.expiresAt) <= now) throw unavailable();
}
function captureLink(value: HostedOwnerClaimLink): HostedOwnerClaimLink {
  return Object.freeze({hostedOrigin: value.hostedOrigin, code: value.code, operation: value.operation, nameHint: value.nameHint});
}
/** Validate persisted link scalars without trusting its display hint or issuing work. */
export function checkedHostedOwnerClaimLink(value: HostedOwnerClaimLink, expectedHostedOrigin: string): HostedOwnerClaimLink {
  if (typeof value?.hostedOrigin !== 'string' || value.hostedOrigin !== expectedHostedOrigin || !expectedHostedOrigin.startsWith('https://') ||
      routeOrigin(expectedHostedOrigin) !== expectedHostedOrigin || typeof value.code !== 'string' || typeof value.operation !== 'string' ||
      !identifier(value.operation) || typeof value.nameHint !== 'string' || !value.nameHint || new TextEncoder().encode(value.nameHint).length > 120) throw invalid();
  claimCode(value.code);
  return captureLink(value);
}
function captureReview(value: HostedOwnerClaimReview): HostedOwnerClaimReview {
  // The reason and the previous-owner pair are part of the claim this decision
  // re-checks against, so they are copied exactly and only when the claim carried
  // them; dropping them would make every later sameClaim differ. They are
  // validated exactly as supplied: nothing unknown becomes 'switch', and an
  // invalid role or name is refused, never stripped, matching the lookup.
  const suppliedReason: unknown = value.reason;
  if (suppliedReason !== 'switch' && suppliedReason !== 'change-owner' && suppliedReason !== 'start-over') throw invalid();
  const reason = suppliedReason;
  const transitionMode = value.transitionMode;
  if (transitionMode !== undefined && transitionMode !== 'switch' && transitionMode !== 'start-over') throw invalid();
  if ((reason === 'start-over') !== (transitionMode === 'start-over')) throw invalid();
  const suppliedRole: unknown = value.previousOwnerRole;
  let previousOwnerRole: 'admin' | 'member' | 'remove' | undefined;
  if (suppliedRole !== undefined) {
    if (suppliedRole !== 'admin' && suppliedRole !== 'member' && suppliedRole !== 'remove') throw invalid();
    previousOwnerRole = suppliedRole;
  }
  const suppliedName: unknown = value.currentOwnerUsername;
  let currentOwnerUsername: string | undefined;
  if (suppliedName !== undefined) {
    if (typeof suppliedName !== 'string' || !suppliedName || new TextEncoder().encode(suppliedName).length > 64) throw invalid();
    currentOwnerUsername = suppliedName;
  }
  if ((previousOwnerRole === undefined) !== (currentOwnerUsername === undefined) || (reason === 'change-owner') !== (previousOwnerRole !== undefined) || previousOwnerRole !== undefined && transitionMode !== undefined) throw invalid();
  return Object.freeze({request: captureLink(value.request), identity: Object.freeze({accountId: value.identity.accountId, familyId: value.identity.familyId}),
    serverId: value.serverId, name: value.name, address: value.address, transactionId: value.transactionId,
    requestedAt: value.requestedAt, expiresAt: value.expiresAt, status: value.status, reason,
    ...(transitionMode === undefined ? {} : {transitionMode}),
    ...(previousOwnerRole === undefined ? {} : {previousOwnerRole}),
    ...(currentOwnerUsername === undefined ? {} : {currentOwnerUsername}),
    ...(value.otherPeopleCount === undefined ? {} : {otherPeopleCount: value.otherPeopleCount}),
    ...(value.registration ? {registration: Object.freeze({id: value.registration.id, origin: value.registration.origin})} : {})});
}

/** Synchronous capture only: no store, session restoration or request starts. */
export function captureHostedOwnerClaim(page: Pick<Location, 'href' | 'pathname' | 'search' | 'hash'>,
  history: Pick<History, 'state' | 'replaceState'>, expectedHostedOrigin: string): HostedOwnerClaimCapture | undefined {
  if (page.pathname !== '/claim' || !page.hash) return;
  const href = page.href;
  history.replaceState(history.state, '', page.pathname + page.search);
  try {
    const url = new URL(href), fields = new URLSearchParams(url.hash.slice(1));
    if (href.length > 2048 || !expectedHostedOrigin.startsWith('https://') || routeOrigin(expectedHostedOrigin) !== expectedHostedOrigin ||
        url.origin !== expectedHostedOrigin || url.pathname !== '/claim' || url.search || url.username || url.password ||
        [...fields.keys()].sort().join(',') !== 'code,operation,server') throw invalid();
    const code = fields.get('code') ?? '', operation = fields.get('operation') ?? '', nameHint = fields.get('server') ?? '';
    return {request: checkedHostedOwnerClaimLink({hostedOrigin: expectedHostedOrigin, code, operation, nameHint}, expectedHostedOrigin)};
  } catch {return {};}
}

async function lookup(account: ServicesAccount, request: HostedOwnerClaimLink, signal: AbortSignal, expected?: AccountIdentity) {
  if (account.api.origin !== request.hostedOrigin || !request.hostedOrigin.startsWith('https://') ||
      routeOrigin(request.hostedOrigin) !== request.hostedOrigin || !identifier(request.operation)) throw invalid();
  claimCode(request.code); signal.throwIfAborted();
  return currentAccountWork(account, async session => {
    signal.throwIfAborted();
    const raw = await account.api.send(routePath('server_claim_lookup'), 'POST', {code: request.code}, signal, session.accessToken);
    signal.throwIfAborted();
    const claim = decodeServerClaimLookupResponse(raw), now = Date.now();
    if (claim.code !== request.code || claim.purpose !== 'switch' || claim.setupTransactionId !== undefined || !claim.ownerTransactionId || !identifier(claim.ownerTransactionId) ||
        claim.status !== 'pending' && claim.status !== 'approved' || claim.name !== request.nameHint || !claim.name ||
        new TextEncoder().encode(claim.name).length > 120 || !claim.serverId.startsWith('srv_') ||
        !currentUTCTimestamp(claim.requestedAt) || Date.parse(claim.requestedAt) > now + 60000 ||
        !currentUTCTimestamp(claim.expiresAt) || Date.parse(claim.expiresAt) - now > 300000 ||
        Date.parse(claim.expiresAt) <= Date.parse(claim.requestedAt)) throw invalid();
    // A change-owner claim names what the previous owner becomes; a plain switch
    // carries neither field. Anything else is refused rather than guessed.
    const previousOwnerRole = claim.previousOwnerRole;
    if (previousOwnerRole !== undefined && previousOwnerRole !== 'admin' && previousOwnerRole !== 'member' && previousOwnerRole !== 'remove') throw invalid();
    const currentOwnerUsername = claim.currentOwnerUsername;
    if (currentOwnerUsername !== undefined && (!currentOwnerUsername || new TextEncoder().encode(currentOwnerUsername).length > 64)) throw invalid();
    if ((previousOwnerRole === undefined) !== (currentOwnerUsername === undefined)) throw invalid();
    const transitionMode = claim.transitionMode;
    if (transitionMode !== undefined && transitionMode !== 'switch' && transitionMode !== 'start-over' || previousOwnerRole !== undefined && transitionMode !== undefined) throw invalid();
    routeBytes(claim.serverId.slice(4), 32);
    ownerOrigin(claim.address);
    // Presentation only: the count never authorizes anything, and a value the
    // decoder or this bound refuses is absent, never coerced to zero.
    const otherPeopleCount = claim.otherPeopleCount;
    if (otherPeopleCount !== undefined && (!Number.isInteger(otherPeopleCount) || otherPeopleCount < 0 || otherPeopleCount > 10000)) throw invalid();
    let registration: HostedOwnerClaimReview['registration'];
    if (claim.returnRegistration) {
      const value = claim.returnRegistration;
      if (claim.status !== 'approved' || !value.id.startsWith('ret_') || !value.origin.startsWith('https://') || routeOrigin(value.origin) !== value.origin) throw invalid();
      routeBytes(value.id.slice(4), 32);
      registration = Object.freeze({id: value.id, origin: value.origin});
    }
    const review: HostedOwnerClaimReview = Object.freeze({request, identity: Object.freeze(accountIdentity(session)),
      serverId: claim.serverId, name: claim.name, address: claim.address, transactionId: claim.ownerTransactionId,
      requestedAt: claim.requestedAt, expiresAt: claim.expiresAt, status: claim.status,
      reason: previousOwnerRole !== undefined ? 'change-owner' : transitionMode === 'start-over' ? 'start-over' : 'switch',
      ...(transitionMode === undefined ? {} : {transitionMode}),
      ...(previousOwnerRole === undefined ? {} : {previousOwnerRole}), ...(currentOwnerUsername === undefined ? {} : {currentOwnerUsername}),
      ...(otherPeopleCount === undefined ? {} : {otherPeopleCount}), ...(registration ? {registration} : {})});
    fresh(review, now);
    return review;
  }, expected);
}
/** Lookup never approves, creates a return, or exchanges a credential. */
export function reviewHostedOwnerClaim(account: ServicesAccount, request: HostedOwnerClaimLink, signal: AbortSignal) {
  // Capture scalars before the first await, including caller-owned link objects.
  return lookup(account, captureLink(request), signal);
}
function sameClaim(prior: HostedOwnerClaimReview, current: HostedOwnerClaimReview) {
  if (prior.serverId !== current.serverId || prior.name !== current.name || prior.address !== current.address ||
      prior.transactionId !== current.transactionId || prior.requestedAt !== current.requestedAt || prior.expiresAt !== current.expiresAt ||
      prior.otherPeopleCount !== current.otherPeopleCount || prior.reason !== current.reason || prior.transitionMode !== current.transitionMode ||
      prior.previousOwnerRole !== current.previousOwnerRole || prior.currentOwnerUsername !== current.currentOwnerUsername ||
      prior.registration && (!current.registration || prior.registration.id !== current.registration.id || prior.registration.origin !== current.registration.origin)) throw invalid();
}

/** Explicit approval/denial uses the same current Hosted account and family
 * that reviewed the immutable claim. There is no replacement-server option. */
export async function decideHostedOwnerClaim(account: ServicesAccount, review: HostedOwnerClaimReview, decision: 'approve' | 'deny', signal: AbortSignal) {
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
 * installed HTTPS registration may be a different managed address. Never
 * substitute that registration, a URL hint or an arbitrary return parameter. */
export function validateHostedOwnerReturn(location: string, review: Pick<HostedOwnerClaimReview, 'address' | 'transactionId'>) {
  if (!identifier(review.transactionId)) throw invalid();
  const origin = ownerOrigin(review.address);
  if (location !== origin + '/auth/portico-return#transaction=' + encodeURIComponent(review.transactionId)) throw invalid();
  return location;
}

/** Explicit completion only after lookup resolves the genuine installed
 * registration. No polling, ordinary Start, exchange or Server session write. */
export async function completeHostedOwnerClaim(account: ServicesAccount, review: HostedOwnerClaimReview, signal: AbortSignal) {
  review = captureReview(review);
  fresh(review);
  const current = await lookup(account, review.request, signal, review.identity);
  sameClaim(review, current);
  if (current.status !== 'approved' || !current.registration) return;
  const location = await currentAccountWork(account, async session => {
    signal.throwIfAborted(); fresh(current);
    const raw = await account.api.send(encodedRoutePath('server_return_complete', {id: encodeURIComponent(current.transactionId)}), 'POST', {}, signal, session.accessToken);
    signal.throwIfAborted(); fresh(current);
    return validateHostedOwnerReturn(decodeServerReturnCompleteResponse(raw).location, current);
  }, review.identity);
  signal.throwIfAborted(); fresh(current);
  return location;
}
