import {ApiError} from '@core/index.ts';
import {accountIdentity, currentAccountWork, sameAccount, type AccountIdentity, type ServicesAccount} from './session';
import type {HostedOwnerClaimReview} from './owner-claim';
import type {scopedHostedOwnerClaim} from './owner-pending';

type Controller = ReturnType<typeof scopedHostedOwnerClaim>;
export type OwnerApprovalDisplay = Readonly<{
  name: string; address: string; accountName: string; requestedAt: string; expiresAt: string;
  /** The signed claim's own reason; a plain switch carries no previous-owner role.
   * start-over is the distinct signed transition mode the server binds to the move. */
  reason: 'switch' | 'change-owner' | 'start-over'; currentOwnerUsername?: string; previousOwnerRole?: 'admin' | 'member' | 'remove';
  /** Signed other-people count when the producer supplied one; absent otherwise. */
  otherPeopleCount?: number;
}>;
export type OwnerApprovalSnapshot = Readonly<{
  phase: 'loading' | 'review' | 'approved' | 'finishing' | 'denied' | 'returning' | 'error' | 'expired';
  busy: boolean; display?: OwnerApprovalDisplay; error?: unknown;
}>;
const changed = () => new ApiError(409, 'claim_stale', 'claim_stale', false);

/** View lifecycle over the one existing protected owner controller. No cookie,
 * credential, journal or claim code is copied into this public snapshot. */
export class OwnerApprovalFlow {
  private state: OwnerApprovalSnapshot = Object.freeze({phase: 'loading', busy: false});
  private readonly listeners = new Set<() => void>();
  private epoch = 0;
  private disposed = false;
  private review?: HostedOwnerClaimReview;
  private identity?: AccountIdentity;
  constructor(private readonly account: ServicesAccount, private readonly controller: Controller,
    private readonly navigate: (location: string) => void, private readonly now = Date.now) {}
  readonly getSnapshot = () => this.state;
  readonly subscribe = (listener: () => void) => {this.listeners.add(listener); return () => this.listeners.delete(listener);};
  private publish(state: OwnerApprovalSnapshot) {
    if (this.disposed) return;
    this.state = Object.freeze(state); this.listeners.forEach(listener => listener());
  }
  private current(generation: number, identity: AccountIdentity) {
    if (this.disposed || this.epoch !== generation) throw changed();
    const session = this.account.service.getSnapshot().session;
    if (!session || !sameAccount(accountIdentity(session), identity)) throw changed();
    if (this.review && Date.parse(this.review.expiresAt) <= this.now()) throw changed();
    return session;
  }
  private async run(work: (generation: number, identity: AccountIdentity) => Promise<void>) {
    if (this.disposed || this.state.busy || this.state.phase === 'expired' || this.state.phase === 'denied' || this.state.phase === 'returning') return;
    const session = this.account.service.getSnapshot().session;
    if (!session) {this.publish({phase: 'error', busy: false, error: changed()}); return;}
    let identity: AccountIdentity;
    try {identity = this.identity ?? Object.freeze(accountIdentity(session));}
    catch (error) {this.publish({phase: 'error', busy: false, error}); return;}
    const generation = ++this.epoch;
    this.publish({...this.state, busy: true, error: undefined});
    try {this.current(generation, identity); await work(generation, identity);}
    catch (error) {
      if (!this.disposed && generation === this.epoch) this.publish({phase: 'error', busy: false, error});
    } finally {
      if (!this.disposed && generation === this.epoch && this.state.busy) this.publish({...this.state, busy: false});
    }
  }
  async start() {
    return this.run(async (generation, identity) => {
      const review = await this.controller.review();
      const session = this.current(generation, identity);
      if (!sameAccount(review.identity, identity) || !Number.isFinite(Date.parse(review.expiresAt)) || Date.parse(review.expiresAt) <= this.now()) throw changed();
      this.identity = identity; this.review = review;
      const display = Object.freeze({name: review.name, address: review.address,
        accountName: session.account.displayName || session.account.username,
        requestedAt: review.requestedAt, expiresAt: review.expiresAt, reason: review.reason,
        ...(review.previousOwnerRole === undefined ? {} : {previousOwnerRole: review.previousOwnerRole}),
        ...(review.currentOwnerUsername === undefined ? {} : {currentOwnerUsername: review.currentOwnerUsername}),
        ...(review.otherPeopleCount === undefined ? {} : {otherPeopleCount: review.otherPeopleCount})});
      this.publish({phase: review.status === 'approved' ? 'approved' : 'review', busy: false, display});
    });
  }
  async decide(decision: 'approve' | 'deny') {
    if (decision !== 'approve' && decision !== 'deny' || this.state.phase !== 'review' || !this.review) return;
    const review = this.review;
    return this.run(async (generation, identity) => {
      await this.controller.decide(decision);
      this.current(generation, identity);
      if (decision === 'deny') {
        // The protected controller has forgotten this exact attempt. Recheck
        // family after that asynchronous write, before navigating its origin.
        await currentAccountWork(this.account, async () => {this.current(generation, identity);}, identity);
        this.current(generation, identity);
        this.publish({phase: 'denied', busy: false});
        this.current(generation, identity);
        this.navigate(review.address);
      } else this.publish({phase: 'approved', busy: false, display: this.state.display});
    });
  }
  async complete() {
    if (this.state.phase !== 'approved' && this.state.phase !== 'finishing' || !this.review) return;
    return this.run(async (generation, identity) => {
      const location = await this.controller.complete();
      this.current(generation, identity);
      // Approved, but the server's secure return isn't ready yet. Say so, and let the same
      // Return try again: never a silent no-op.
      if (!location) {this.publish({phase: 'finishing', busy: false, display: this.state.display}); return;}
      // The protected controller already performs the final authenticated check
      // before spending this attempt. Do not add fallible I/O after that spend.
      this.publish({phase: 'returning', busy: false, display: this.state.display});
      this.current(generation, identity);
      this.navigate(location);
    });
  }
  expire() {
    if (this.disposed) return;
    ++this.epoch; this.review = undefined; this.controller.dispose();
    this.publish({phase: 'expired', busy: false});
  }
  dispose() {
    if (this.disposed) return;
    this.disposed = true; ++this.epoch; this.review = undefined; this.identity = undefined;
    this.controller.dispose(); this.listeners.clear();
  }
}
