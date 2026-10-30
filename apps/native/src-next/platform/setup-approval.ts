import {HttpLocalApi, throwIfAborted} from '../../../../packages/client-core/src';
import {currentUTCTimestamp} from '../../../../packages/client-core/src/local-session';
import {routeBytes, routeOrigin} from '../../../../packages/client-core/src/route-identity';
import {CodedError} from '../../../../packages/client-core/src/server-messages';
import {decodeServerClaimLookupResponse, decodeServerClaimApproveCodeResponse} from '../../../../packages/contracts/src/hosted/decoders';
import {routePath} from '../../../../packages/contracts/src/hosted/routes';
import {NativeSetupEnded} from './setup-outcome';
import {nativeAccounts} from './accounts';
import {nativeNetworkFetch} from './server-connections';
import {hostedUrl} from './account-authority';

const invalid = () => new CodedError('invalid_return', 'This setup approval no longer matches its original request.');
type Claim = ReturnType<typeof decodeServerClaimLookupResponse>;
/** In-app approval uses the same official lookup/decision as the Hosted page.
 * Public display never carries the claim code, token, state or verifier. */
export class NativeSetupApproval {
  private claim?: Claim;
  private deciding = false;
  private readonly controller = new AbortController();
  private readonly signal: AbortSignal;
  private readonly off: () => void;
  private account?: {id: string; family: string; name: string};
  constructor(private readonly expected: {code: string; serverId: string; name: string; origin: string; transactionId: string; expiresAt: string},
    private readonly sourceCurrent: () => Promise<void>, sourceSignal: AbortSignal,
    private readonly opening: {signal: AbortSignal; current: () => boolean; canApprove: () => boolean}, private readonly declined: () => void) {
    const abort = () => this.controller.abort();
    sourceSignal.addEventListener('abort', abort, {once: true}); opening.signal.addEventListener('abort', abort, {once: true});
    if (sourceSignal.aborted || opening.signal.aborted) abort();
    this.signal = this.controller.signal;
    this.off = () => {sourceSignal.removeEventListener('abort', abort); opening.signal.removeEventListener('abort', abort);};
  }
  dispose() {this.controller.abort(); this.off();}
  private assertOpening() {
    throwIfAborted(this.signal);
    if (!this.opening.current() || this.deciding && !this.opening.canApprove()) throw invalid();
  }
  private async access() {
    await this.sourceCurrent(); this.assertOpening();
    const credentials = nativeAccounts.accountCredentials();
    await credentials.synchronize(); await this.sourceCurrent(); this.assertOpening();
    const session = await credentials.accessSession(); await this.sourceCurrent(); this.assertOpening();
    if (this.account && (session.account.id !== this.account.id || session.familyId !== this.account.family)) throw invalid();
    return session;
  }
  private async current() {
    await this.sourceCurrent(); this.assertOpening();
    const session = nativeAccounts.accountCredentials().getSnapshot().session;
    if (!this.account || session?.account.id !== this.account.id || session.familyId !== this.account.family
      || Date.parse(this.expected.expiresAt) <= Date.now()) throw invalid();
  }
  private async lookup(): Promise<Claim> {
    const session = await this.access();
    this.account ??= {id: session.account.id, family: session.familyId, name: session.account.displayName || session.account.username};
    await this.current();
    const raw = await new HttpLocalApi(hostedUrl, session.accessToken, nativeNetworkFetch)
      .request(routePath('server_claim_lookup'), 'POST', {code: this.expected.code}, this.signal);
    await this.current();
    const value = decodeServerClaimLookupResponse(raw), expected = this.expected, now = Date.now();
    if (value.code !== expected.code || value.purpose !== 'setup' || value.ownerTransactionId !== undefined
      || value.setupTransactionId !== expected.transactionId || value.serverId !== expected.serverId
      || value.name !== expected.name || value.address !== expected.origin || routeOrigin(value.address) !== expected.origin
      || !currentUTCTimestamp(value.requestedAt) || Date.parse(value.requestedAt) > now + 60_000
      || !currentUTCTimestamp(value.expiresAt) || Date.parse(value.expiresAt) !== Date.parse(expected.expiresAt)
      || Date.parse(value.expiresAt) <= now || Date.parse(value.expiresAt) <= Date.parse(value.requestedAt)
      || value.currentOwnerUsername !== undefined || value.previousOwnerRole !== undefined || value.otherPeopleCount !== undefined) throw invalid();
    routeBytes(value.serverId.slice(4), 32);
    // Setup has no return registration (O-163); `setupInstalled` is strictly true, only on approval.
    if (value.returnRegistration !== undefined || value.setupInstalled !== undefined && (value.setupInstalled !== true || value.status !== 'approved')) throw invalid();
    if (value.status === 'denied') {this.declined(); throw new NativeSetupEnded('declined');}
    if (value.status !== 'pending' && value.status !== 'approved') throw invalid();
    return value;
  }
  async review() {
    this.claim = await this.lookup();
    return {approved: this.claim.status === 'approved', serverName: this.claim.name, address: this.claim.address, accountName: this.account!.name};
  }
  async approve() {
    if (this.deciding || !this.opening.canApprove()) throw invalid();
    this.deciding = true;
    try {
    const previous = this.claim;
    if (!previous) throw invalid();
    const value = await this.lookup();
    if (value.status !== 'pending' || value.requestedAt !== previous.requestedAt || value.expiresAt !== previous.expiresAt) throw invalid();
    const session = await this.access(); await this.current();
    const raw = await new HttpLocalApi(hostedUrl, session.accessToken, nativeNetworkFetch)
      .request(routePath('server_claim_approve_code'), 'POST', {code: this.expected.code}, this.signal);
    await this.current();
    const result = decodeServerClaimApproveCodeResponse(raw);
    if (result.status !== 'decided' || result.code !== 'claim_decided') throw invalid();
    } finally {this.deciding = false;}
  }
}
