import {scopedHostedSetupClaim} from './setup-pending';
import type {ServicesAccount} from './session';
import {restoreServicesLink, type LinkCapture, type ServicesLink, type ServicesLinkStore} from './intake';
import {hostedSignInStore, restoreHostedSignIn, type HostedSignIn, type SignInCapture} from './sign-in';
import type {HostedOwnerClaimCapture} from './owner-claim';
import {hostedOwnerPendingStore, restoreHostedOwnerPending, scopedHostedOwnerClaim, hostedApprovalPurpose,
  type HostedOwnerPending, type HostedOwnerPendingStore} from './owner-pending';

export type ServicesBootstrapSnapshot = Readonly<{phase: 'restoring' | 'ready' | 'error'; link?: ServicesLink; signIn?: HostedSignIn; invalidSignIn?: boolean; ownerPending?: boolean; invalidOwner?: boolean; error?: unknown}>;
/** Account-only startup. The canonical cookie coordinator restores/renews its own family.
 * This root never constructs server bindings, loads a server or requests a library. */
export class ServicesBootstrap {
  private state: ServicesBootstrapSnapshot = {phase: 'restoring'};
  private readonly listeners = new Set<() => void>();
  private flight?: Promise<void>;
  private disposed = false;
  private ownerCaptureConsumed = false;
  private ownerState?: {store: HostedOwnerPendingStore; pending: HostedOwnerPending};
  // This collection owns teardown only. Each factory below retains its full,
  // purpose-specific review type; setup never needs an owner reason.
  private readonly approvalLifetimes = new Set<Pick<ReturnType<typeof scopedHostedOwnerClaim>, 'dispose'>>();
  constructor(private readonly account: Pick<ServicesAccount, 'service'>,
    private readonly store: () => ServicesLinkStore, private readonly capture?: LinkCapture,
    private readonly signIn?: {origin: string; capture?: SignInCapture},
    private readonly owner?: {origin: string; capture?: HostedOwnerClaimCapture; store?: () => HostedOwnerPendingStore}) {}
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  private publish(value: ServicesBootstrapSnapshot) { if (this.disposed) return; this.state = value; this.listeners.forEach(listener => listener()); }
  start = () => this.flight ??= this.restore().finally(() => { this.flight = undefined; });
  /** The approval code stays out of observable state. Creating this controlled
   * adapter performs no lookup, decision, completion or navigation. */
  ownerApproval(account: ServicesAccount) {
    if (this.disposed || this.state.phase !== 'ready' || !this.state.ownerPending || !this.ownerState || account.service !== this.account.service) return;
    const controller = scopedHostedOwnerClaim(account, this.ownerState.store, this.ownerState.pending);
    this.approvalLifetimes.add(controller);
    return controller;
  }
  approvalPurpose(account: ServicesAccount, signal: AbortSignal) {
    if (this.disposed || this.state.phase !== 'ready' || !this.state.ownerPending || !this.ownerState || account.service !== this.account.service) throw new Error('claim_stale');
    return hostedApprovalPurpose(account,this.ownerState.store,this.ownerState.pending,signal);
  }
  setupApproval(account: ServicesAccount) {
    if (this.disposed || this.state.phase !== 'ready' || !this.state.ownerPending || !this.ownerState || account.service !== this.account.service) return;
    const controller=scopedHostedSetupClaim(account,this.ownerState.store,this.ownerState.pending);
    this.approvalLifetimes.add(controller);return controller;
  }
  private async restore() {
    this.approvalLifetimes.forEach(controller => controller.dispose()); this.approvalLifetimes.clear();
    this.ownerState = undefined;
    this.publish({phase: 'restoring'});
    try {
      // A captured token is already scrubbed. Persist it before any provider navigation.
      let link: ServicesLink | undefined;
      let signIn: HostedSignIn | undefined;
      if (this.owner) {
        this.ownerState = undefined;
        try {
          const store = (this.owner.store ?? hostedOwnerPendingStore)();
          const pending = await restoreHostedOwnerPending(store, this.owner.origin, this.ownerCaptureConsumed ? undefined : this.owner.capture);
          this.ownerCaptureConsumed = true;
          if (this.disposed) return;
          if (pending) this.ownerState = {store, pending};
        } catch {
          this.publish({phase: 'ready', invalidOwner: true}); return;
        }
      } else {
        link = await restoreServicesLink(this.store(), this.capture);
      }
      if (this.signIn && !this.owner) {
        try {signIn = await restoreHostedSignIn(hostedSignInStore(), this.signIn.origin, this.signIn.capture);}
        catch {this.publish({phase: 'ready', invalidSignIn: true}); return;}
      }
      if (this.disposed) return;
      await this.account.service.synchronize();
      if (this.disposed) return;
      if (this.account.service.getSnapshot().session) {
        try {await this.account.service.accessSession();}
        catch (error) {if (this.account.service.getSnapshot().session) throw error;}
      }
      if (!this.disposed) this.publish({phase: 'ready', link, signIn, ...(this.owner ? {ownerPending: !!this.ownerState} : {})});
    } catch (error) { this.publish({phase: 'error', error}); }
  }
  dispose() {
    this.disposed = true; this.ownerState = undefined;
    this.approvalLifetimes.forEach(controller => controller.dispose()); this.approvalLifetimes.clear(); this.listeners.clear();
  }
}
