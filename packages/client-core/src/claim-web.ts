import {routePrefix} from '../../contracts/src/routes.ts';
import {routePath} from '../../contracts/src/route-paths.ts';
import {parseClaimStatus, type ClaimExpected, type ClaimStatus} from './claim-onboarding.ts';
import type {HttpLocalApi} from './index.ts';
import type {ServerPin} from './route-identity.ts';

/**
 * Connecting a server to a Portico Account with a claim code. One flow for every client:
 *
 *   1. The server prepares an unbound claim (`/v1/networking/claim/prepare`, no account:
 *      BE-hosted d37e441, Plex-style; whoever approves the code becomes the owner), then registers a signed pending claim
 *      with Hosted (`/v1/networking/claim/web-approval`), which answers with a
 *      49-character claim code and `approvalUrl`
 *      (`https://web.getportico.tv/claim#code=…&server=…&return=…`).
 *   2. The client opens `approvalUrl` (the same browser tab, or the system browser from an app);
 *      the person signs in there if needed and approves or denies. That site never talks to
 *      the client.
 *   3. Meanwhile the client holds one request open
 *      (`/v1/networking/claim/await-approval`): Hosted pushes the decision to
 *      the server, and the server completes the claim before answering. No
 *      polling, no credentials or envelopes cross origins.
 */
export type WebClaimStatus = ClaimStatus & {approvalUrl?: string; claimCode?: string};
type Api = Pick<HttpLocalApi, 'request'>;

const post = async (api: Api, pin: ServerPin, action: string, body: unknown, signal?: AbortSignal): Promise<WebClaimStatus> => {
  const raw = await api.request<Record<string, unknown>>(routePrefix('post_networking_claim_approve', 3) + action, 'POST', body, signal);
  const status = parseClaimStatus(raw, pin) as WebClaimStatus;
  const approvalUrl = typeof raw?.approvalUrl === 'string' ? raw.approvalUrl : undefined;
  const claimCode = typeof raw?.claimCode === 'string' ? raw.claimCode : undefined;
  return {...status, ...(approvalUrl ? {approvalUrl} : {}), ...(claimCode ? {claimCode} : {})};
};

/** Only an approval page on the Portico Account site may be opened. */
export function safeApprovalUrl(url: string | undefined, hostedOrigin: string): string | undefined {
  if (!url) return undefined;
  try {
    const u = new URL(url);
    const hosted = new URL(hostedOrigin);
    if(hosted.protocol!=='https:'||hosted.username||hosted.password||hosted.pathname!=='/'||hosted.search||hosted.hash||u.username||u.password||u.protocol!=='https:'||u.search)return undefined;
    const fragment=new URLSearchParams(u.hash.slice(1));
    return u.origin===hosted.origin&&u.pathname==='/claim'&&fragment.getAll('code').length===1&&!!fragment.get('code') ? u.href : undefined;
  } catch { return undefined; }
}

/** Steps 1 and 2: prepare (when needed) and register the claim; returns the approval URL and the expected operation. */
export async function beginWebClaim(api: Api, pin: ServerPin, signal?: AbortSignal): Promise<{status: WebClaimStatus; expected: ClaimExpected}> {
  let status = parseClaimStatus(await api.request(routePath('get_networking_claim'), 'GET', undefined, signal), pin) as WebClaimStatus;
  // No account here: the approver on web.getportico.tv becomes the owner.
  if (status.actions.includes('prepare')) status = await post(api, pin, 'prepare', {expected: status.identity}, signal);
  if (!status.operation) throw new ClaimError('claim_unavailable');
  const expected = status.operation;
  status = await post(api, pin, 'web-approval', {expected}, signal);
  return {status, expected: status.operation ?? expected};
}

/** Step 3: wait for the decision (the server completes the claim), then take any remaining step. */
export async function awaitWebClaim(api: Api, pin: ServerPin, expected: ClaimExpected, signal?: AbortSignal): Promise<WebClaimStatus> {
  // The server answers with the final claim status, or `{state: 'approval_closed' | 'approval_changed'}`
  // when the person denied, the code expired, or the claim changed underneath.
  const raw = await api.request<Record<string, unknown>>(routePath('post_networking_claim_await_approval'), 'POST', {expected}, signal);
  if (raw?.state === 'approval_closed' || raw?.state === 'approval_changed') throw new ClaimError(raw.state);
  let status = parseClaimStatus(raw, pin) as WebClaimStatus;
  if (status.operation && status.actions.includes('continue') && !status.approvalRequired) status = await post(api, pin, 'continue', {expected: status.operation}, signal);
  return status;
}

/**
 * The catalogue message for a failed connection (BE-hosted d37e441): 400 `invalid_request`,
 * 429 when more than 3 claims are pending, 409 `claim_stale` (already decided or expired), and the
 * await's `approval_closed` / `approval_changed`. `undefined` means "use the shared presenter".
 */
export function claimErrorMessage(e: unknown): 'web.claim.errorInvalid' | 'web.claim.errorTooMany' | 'web.claim.errorStale' | 'web.claim.closed' | 'web.claim.errorUnavailable' | undefined {
  const code = (e as {code?: string} | undefined)?.code, status = (e as {status?: number} | undefined)?.status;
  if (code === 'approval_closed' || code === 'approval_changed') return 'web.claim.closed';
  if (code === 'claim_stale' || status === 409) return 'web.claim.errorStale';
  if (status === 429) return 'web.claim.errorTooMany';
  if (code === 'invalid_request' || status === 400) return 'web.claim.errorInvalid';
  if (code === 'claim_unavailable') return 'web.claim.errorUnavailable';
  return undefined;
}

export class ClaimError extends Error {
  code: string;
  constructor(code: string) { super(code); this.code = code; }
}
