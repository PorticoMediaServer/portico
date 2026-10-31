import {ApiError} from '@core/index.ts';
import {serverFloorFrom, systemScheduler, wait, type Scheduler} from '@core/timer.ts';
import {accountIdentity, currentAccountWork, sameAccount, type AccountIdentity, type ServicesAccount} from './session';
import type {HostedSetupClaimPhase, HostedSetupClaimReview} from './setup-claim';
import type {scopedHostedSetupClaim} from './setup-pending';

type Controller = ReturnType<typeof scopedHostedSetupClaim>;
export type SetupApprovalDisplay = Readonly<{
  name: string; address: string; accountName: string; requestedAt: string; expiresAt: string;
}>;
export type SetupApprovalSnapshot = Readonly<{
  phase: 'loading' | 'review' | 'approved' | 'finishing' | 'denied' | 'returning' | 'error' | 'expired';
  busy: boolean; approved?: true; /** Hosted reports the claim installed: only then is Return offered. */ installed?: true; display?: SetupApprovalDisplay; error?: unknown;
  /** Hosted says it is still finishing well after Approve with no word from the server: the page says so, with what to check. */ slow?: true;
  /** Seconds until a refused (429) lookup may be tried again; Try again is held until then. */ retryIn?: number;
}>;
const changed = () => new ApiError(409, 'claim_stale', 'claim_stale', false);
const KNOWN_PHASES = new Set(['review', 'approved', 'finishing', 'installed']);
/** A phase this page does not know reads as stale, never as a guess about the claim. */
function knownPhase(review: HostedSetupClaimReview) {
  if (review.phase && !KNOWN_PHASES.has(review.phase.variant)) throw changed();
}
/** Slow counts only while Hosted has heard nothing from the server since the approval. */
function slowWording(phase: HostedSetupClaimPhase | undefined) {
  if (!phase?.slow) return false;
  const seen = Date.parse(phase.serverSeenAt ?? ''), approved = Date.parse(phase.approvedAt ?? '');
  return !(seen >= approved);
}
const rateLimited = (error: unknown) => {
  const value = error as {status?: unknown; code?: unknown} | null;
  return value?.status === 429 || value?.code === 'rate_limited';
};

/** View lifecycle over the one existing protected setup controller. No cookie,
 * credential, journal or claim code is copied into this public snapshot. */
export class SetupApprovalFlow {
  private state: SetupApprovalSnapshot = Object.freeze({phase: 'loading', busy: false});
  private readonly listeners = new Set<() => void>();
  private epoch = 0;
  private disposed = false;
  private review?: HostedSetupClaimReview;
  private identity?: AccountIdentity;
  private lifetime?: AbortController;
  private holding?: AbortController;
  constructor(private readonly account: ServicesAccount, private readonly controller: Controller,
    private readonly navigate: (location: string) => void, private readonly now = Date.now, private readonly scheduler: Scheduler = systemScheduler) {}
  readonly getSnapshot = () => this.state;
  readonly subscribe = (listener: () => void) => {this.listeners.add(listener); return () => this.listeners.delete(listener);};
  private publish(state: SetupApprovalSnapshot) {
    if (this.disposed) return;
    this.state = Object.freeze(state); this.listeners.forEach(listener => listener());
  }
  /** The page ends at the claim's own expiry, on the monotonic clock. */
  private watchExpiry(expiresAt: string) {
    this.lifetime?.abort();
    const lifetime = this.lifetime = new AbortController();
    wait(Math.max(0, Date.parse(expiresAt) - this.now()), {signal: lifetime.signal, scheduler: this.scheduler}).then(() => this.expire(), () => {});
  }
  /** Honest wait after a 429: counts down the server's Retry-After, never shortened,
   * and nothing looks up again until it has passed. */
  private holdFor(ms: number) {
    this.holding?.abort();
    const holding = this.holding = new AbortController(), end = this.scheduler.now() + ms;
    const count = async () => {
      for (let left = ms; left > 0; left = end - this.scheduler.now()) {
        this.publish({...this.state, retryIn: Math.ceil(left / 1000)});
        await wait(Math.min(left, 1000), {signal: holding.signal, scheduler: this.scheduler});
      }
      this.holding = undefined;
      const {retryIn: _held, ...rest} = this.state;
      this.publish(rest);
    };
    count().catch(() => {});
  }
  private stopTimers() {this.lifetime?.abort(); this.holding?.abort(); this.lifetime = this.holding = undefined;}
  private current(generation: number, identity: AccountIdentity) {
    if (this.disposed || this.epoch !== generation) throw changed();
    const session = this.account.service.getSnapshot().session;
    if (!session || !sameAccount(accountIdentity(session), identity)) throw changed();
    if (this.review && Date.parse(this.review.expiresAt) <= this.now()) throw changed();
    return session;
  }
  private async run(work: (generation: number, identity: AccountIdentity) => Promise<void>) {
    if (this.disposed || this.state.busy || this.state.retryIn !== undefined || this.state.phase === 'expired' || this.state.phase === 'denied' || this.state.phase === 'returning') return;
    const session = this.account.service.getSnapshot().session;
    if (!session) {this.publish({phase: 'error', busy: false, error: changed()}); return;}
    let identity: AccountIdentity;
    try {identity = this.identity ?? Object.freeze(accountIdentity(session));}
    catch (error) {this.publish({...this.state, phase: 'error', busy: false, error}); return;}
    const generation = ++this.epoch;
    this.publish({...this.state, busy: true, error: undefined});
    try {this.current(generation, identity); await work(generation, identity);}
    catch (error) {
      if (!this.disposed && generation === this.epoch) {
        this.publish({...this.state, phase: 'error', busy: false, error});
        const floor = rateLimited(error) ? serverFloorFrom(error)?.retryAfterMs : undefined;
        if (floor) this.holdFor(floor);
      }
    } finally {
      if (!this.disposed && generation === this.epoch && this.state.busy) this.publish({...this.state, busy: false});
    }
  }
  async start() {
    return this.run(async (generation, identity) => {
      const review = await this.controller.review();
      const session = this.current(generation, identity);
      if (!sameAccount(review.identity, identity) || !Number.isFinite(Date.parse(review.expiresAt)) || Date.parse(review.expiresAt) <= this.now()) throw changed();
      knownPhase(review);
      this.identity = identity; this.review = review;
      const display = Object.freeze({name: review.name, address: review.address,
        accountName: session.account.displayName || session.account.username,
        requestedAt: review.requestedAt, expiresAt: review.expiresAt});
      const approved = review.status === 'approved';
      this.publish({phase: approved ? 'approved' : 'review', busy: false, ...(approved ? {approved: true as const} : {}), ...(approved && review.installed ? {installed: true as const} : {}),
        ...(approved && !review.installed && slowWording(review.phase) ? {slow: true as const} : {}), display});
      this.watchExpiry(review.expiresAt);
    });
  }
  async decide(decision: 'approve' | 'deny') {
    if (decision !== 'approve' && decision !== 'deny' || this.state.phase !== 'review' || !this.review) return;
    return this.run(async (generation, identity) => {
      await this.controller.decide(decision);
      this.current(generation, identity);
      if (decision === 'deny') {
        // The protected controller has forgotten this exact attempt. Recheck
        // family after that asynchronous write, before publishing the result.
        await currentAccountWork(this.account, async () => {this.current(generation, identity);}, identity);
        this.current(generation, identity);
        this.publish({phase: 'denied', busy: false, display: this.state.display});
      } else {
        this.publish({phase: 'approved', busy: false, approved: true, display: this.state.display});
      }
    });
  }
  /** A denial has no credential exchange to finish. Return only to the original
   * reviewed server, on the viewer's explicit action and under the same family. */
  returnDenied() {if (this.state.phase === 'denied') this.returnReviewed();}
  /** A refused attempt may return to its original public server address, never
   * bypass a confirmed approval's protected completion or use a successor family. */
  returnReviewed() {
    if (this.disposed || !['denied', 'error'].includes(this.state.phase) || this.state.approved || this.state.busy || !this.review || !this.identity) return;
    const generation = this.epoch, identity = this.identity, address = this.review.address;
    try {
      this.current(generation, identity);
      this.publish({...this.state, busy: true});
      this.current(generation, identity);
      this.navigate(address);
    } catch (error) {
      if (!this.disposed && generation === this.epoch) this.publish({...this.state, busy: false, error});
    }
  }
  async complete() {
    if (!this.state.approved || !['approved', 'finishing', 'error'].includes(this.state.phase) || !this.review) return;
    return this.run(async (generation, identity) => {
      const done = await this.controller.complete();
      this.current(generation, identity);
      // Not finished yet: never a silent no-op. The same lookup says how far along it is, and
      // the page says the server is finishing, with the same Return to try again.
      if (!done.location) {
        const review = done.review;
        if (!review) throw changed();
        knownPhase(review);
        this.review = review;
        this.publish({phase: 'finishing', busy: false, approved: true, display: this.state.display, ...(review.installed ? {installed: true as const} : {}),
          ...(!review.installed && slowWording(review.phase) ? {slow: true as const} : {})});
        return;
      }
      const location = done.location;
      // The protected controller already performs the final authenticated check
      // before spending this attempt. Do not add fallible I/O after that spend.
      this.lifetime?.abort();
      this.publish({phase: 'returning', busy: false, approved: true, display: this.state.display});
      this.current(generation, identity);
      this.navigate(location);
    });
  }
  expire() {
    if (this.disposed) return;
    this.stopTimers();
    ++this.epoch; this.review = undefined; this.controller.dispose();
    this.publish({phase: 'expired', busy: false});
  }
  dispose() {
    if (this.disposed) return;
    this.disposed = true; this.stopTimers(); ++this.epoch; this.review = undefined; this.identity = undefined;
    this.controller.dispose(); this.listeners.clear();
  }
}
