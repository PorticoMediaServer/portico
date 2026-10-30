import type {HttpLocalApi, LocalSession} from './index.ts';
import {directSignIn, completeDirectSignInChallenge, type DirectSignIn, type DirectSnapshot} from './profile-management.ts';
import {IdentityClient} from './identity-client.ts';
import {porticoSignIn, type HostedRequestApi} from './portico-servers.ts';
import type {SignInKind} from './app-sessions.ts';
import {routePath} from '../../contracts/src/routes.ts';

type Grant = Extract<DirectSignIn, {kind: 'signed-in'}>;
type Attempt = Readonly<{grant: Grant; hostedCurrent?: () => boolean}>;
export type PorticoProofSource = () => Promise<Readonly<{hosted: HostedRequestApi; origin: string; accountCurrent: () => boolean}>>;
export type ProfileRecoveryPhase = 'password' | 'portico' | 'challenge' | 'pin' | 'done';
export type ProfileRecoveryState = Readonly<{phase: ProfileRecoveryPhase; busy?: boolean; error?: unknown}>;

export type ProfileRecoveryPorts = Readonly<{
  api: HttpLocalApi;
  snapshot: DirectSnapshot;
  profileId: string;
  /** The selected app session's own family. Cleanup must never revoke it. */
  selectedFamilyId: string;
  kind: SignInKind;
  /** Exact selected pin, family and viewer, checked after every await. */
  authorityCurrent: () => boolean;
  /** A fresh API for the proof family on the selected, pinned connection. */
  temporary: (session: LocalSession) => HttpLocalApi;
  /** Hosted current request supplier; absent for a local server. */
  portico?: PorticoProofSource;
  signIn?: typeof porticoSignIn;
}>;

/** Shared by web and native (each supplies its own temporary connection and hosted
 * account). One temporary account-proof family resets one profile PIN. It never enters the
 * app-session store, never replaces the selected viewer, never revokes the selected
 * app family and never accepts a child viewer credential. Only the reset proof's own
 * fresh family is retired. */
export class ProfileRecoveryController {
  private state: ProfileRecoveryState;
  private readonly listeners = new Set<() => void>();
  private readonly management: boolean;
  private epoch = 0;
  private controller?: AbortController;
  private attempt?: Attempt;
  private challenge?: Extract<DirectSignIn, {kind: 'challenge'}>;
  private pendingFence?: () => boolean;
  private disposed = false;
  private readonly ports: ProfileRecoveryPorts;
  constructor(ports: ProfileRecoveryPorts) {
    this.ports = ports;
    if (!ports.snapshot.profiles.some(profile => profile.id === ports.profileId)) throw new Error('This profile is no longer available.');
    this.management = ports.snapshot.canManage;
    this.state = Object.freeze({phase: this.management ? 'pin' : ports.kind === 'local' ? 'password' : 'portico'});
  }
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private publish(value: ProfileRecoveryState) {if (this.disposed) return; this.state = Object.freeze(value); for (const listener of this.listeners) listener();}
  private proofCurrent() {const fence = this.attempt?.hostedCurrent ?? this.pendingFence; return fence ? fence() : true;}
  /** The fence is captured per operation, so a late cleanup cannot change it. */
  private operation(hostedCurrent?: () => boolean) {
    this.controller?.abort(); const epoch = ++this.epoch, controller = this.controller = new AbortController();
    return {signal: controller.signal, current: () => !this.disposed && this.epoch === epoch && !controller.signal.aborted
      && this.ports.authorityCurrent() && (!hostedCurrent || hostedCurrent())};
  }
  private changed() {return new Error('The account changed. Prove it again.');}
  private async retire(grant: Grant) {
    // The selected app family is never a proof family, even if a malformed response
    // names it: cleaning up that credential would revoke the user's current session.
    if (grant.session.sessionFamilyId === this.ports.selectedFamilyId) return;
    // Bounded: a hung cleanup never holds the flow, and the family expires by itself.
    const abort = new AbortController(), timer = setTimeout(() => abort.abort(), 5000);
    try {await this.ports.temporary(grant.session).request(routePath('delete_sessions_current'), 'DELETE', undefined, abort.signal);}
    catch { /* the temporary family still expires by itself */ } finally {clearTimeout(timer);}
  }
  /** Clear the attempt and leave an actionable proof phase *before* awaiting cleanup.
   * A second reset therefore sees a non-PIN phase while the DELETE is pending and
   * can never fall through to the selected app family. Late cleanup only touches the
   * captured grant. */
  private async leaveAttempt(error = this.changed()) {
    const attempt = this.attempt;
    this.attempt = undefined; this.challenge = undefined; this.pendingFence = undefined;
    this.publish({phase: this.ports.kind === 'local' ? 'password' : 'portico', busy: false, error});
    if (attempt) await this.retire(attempt.grant);
  }
  private async accept(result: DirectSignIn, current: () => boolean, hostedCurrent?: () => boolean) {
    if (result.kind === 'challenge') {
      if (current()) {this.challenge = result; this.pendingFence = hostedCurrent; this.publish({phase: 'challenge'});}
      return;
    }
    const sameFamily = result.session.sessionFamilyId === this.ports.selectedFamilyId;
    const valid = !sameFamily && current() && result.accountScoped && result.session.viewer.role === 'account'
      && result.session.viewer.authority === this.ports.snapshot.authority
      && result.session.viewer.serverId === this.ports.snapshot.serverId
      && result.session.viewer.accountId === this.ports.snapshot.account.id
      && result.snapshot.serverId === this.ports.snapshot.serverId
      && result.snapshot.account.id === this.ports.snapshot.account.id
      && result.snapshot.profiles.some(profile => profile.id === this.ports.profileId)
      && result.snapshot.capabilities.manageAccount
      && !result.passwordChangeRequired && !result.resetFileOwnerSession;
    if (!valid) {
      if (!sameFamily) await this.retire(result);
      throw new Error(sameFamily ? 'This device\'s current sign-in cannot prove the account.' : 'Sign in to the account that owns this profile.');
    }
    this.challenge = undefined; this.pendingFence = undefined;
    this.attempt = Object.freeze({grant: result, ...(hostedCurrent ? {hostedCurrent} : {})});
    this.publish({phase: 'pin'});
  }
  async password(password: string) {
    if (this.disposed || !this.ports.authorityCurrent() || this.state.phase !== 'password' || !password || this.state.busy) return;
    this.attempt = undefined; this.challenge = undefined; this.pendingFence = undefined;
    const operation = this.operation(); this.publish({...this.state, busy: true, error: undefined});
    try {await this.accept(await directSignIn(this.ports.api, this.ports.snapshot.account.username, password, operation.signal), operation.current);}
    catch (error) {if (operation.current()) this.publish({...this.state, busy: false, error}); else if (!this.disposed) await this.leaveAttempt();}
  }
  /** `supply` names the hosted account for this one attempt (web passes it at the
   * click); otherwise the ports' supplier is used. */
  async portico(supply: PorticoProofSource | undefined = this.ports.portico) {
    if (this.disposed || !this.ports.authorityCurrent() || this.state.phase !== 'portico' || this.state.busy) return;
    this.attempt = undefined; this.challenge = undefined; this.pendingFence = undefined;
    const operation = this.operation(); this.publish({...this.state, busy: true, error: undefined});
    try {
      if (!supply) throw new Error('Portico Account sign-in is unavailable.');
      const source = await supply();
      if (!operation.current()) return;
      const result = await (this.ports.signIn ?? porticoSignIn)(source.hosted, source.origin, this.ports.api, this.ports.snapshot.serverId,
        {signal: operation.signal});
      await this.accept(result, () => operation.current() && source.accountCurrent(), source.accountCurrent);
    } catch (error) {if (operation.current()) this.publish({...this.state, busy: false, error}); else if (!this.disposed) await this.leaveAttempt();}
  }
  async code(code: string) {
    const challenge = this.challenge, fence = this.pendingFence;
    if (this.disposed || this.state.phase !== 'challenge' || !challenge || this.state.busy) return;
    if (!this.ports.authorityCurrent()) return;
    if (fence && !fence()) {await this.leaveAttempt(); return;}
    const operation = this.operation(fence); this.publish({...this.state, busy: true, error: undefined});
    try {await this.accept(await completeDirectSignInChallenge(this.ports.api, challenge.token, code, operation.signal), operation.current, fence);}
    catch (error) {if (operation.current()) this.publish({...this.state, busy: false, error}); else if (!this.disposed) await this.leaveAttempt();}
  }
  async reset(pin: string) {
    if (this.disposed || this.state.phase !== 'pin' || !/^\d{4}$/.test(pin) || this.state.busy) return;
    if (!this.ports.authorityCurrent()) return;
    const attempt = this.attempt;
    if (!attempt && !this.management) {await this.leaveAttempt(); return;}
    if (attempt?.hostedCurrent && !attempt.hostedCurrent()) {await this.leaveAttempt(); return;}
    const operation = this.operation(attempt?.hostedCurrent), api = attempt ? this.ports.temporary(attempt.grant.session) : this.ports.api;
    this.publish({...this.state, busy: true, error: undefined});
    let completed = false, drift = false;
    try {
      if (!operation.current()) drift = true;
      else {
        await new IdentityClient({request: (path, method, body) => api.request(path, method, body, operation.signal)}).resetPIN(this.ports.profileId, pin);
        completed = true;
        if (operation.current()) this.publish({...this.state, phase: 'done', busy: false});
        else drift = true;
      }
    } catch (error) {
      if (operation.current()) this.publish({...this.state, busy: false, error});
      else drift = true;
    }
    if (drift) {await this.leaveAttempt(); return;}
    if (attempt && completed) {
      // The proof family is no longer needed; clear it before cleanup so a late
      // DELETE cannot overwrite a successor attempt.
      if (this.attempt === attempt) this.attempt = undefined;
      await this.retire(attempt.grant);
    }
  }
  dispose() {
    if (this.disposed) return;
    this.disposed = true; ++this.epoch; this.controller?.abort(); this.challenge = undefined; this.pendingFence = undefined;
    const attempt = this.attempt; this.attempt = undefined; if (attempt) void this.retire(attempt.grant);
    this.listeners.clear();
  }
}
