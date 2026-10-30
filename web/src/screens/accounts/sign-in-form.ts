import type {BrowserAccountEntry} from '../../platform/account-entry';
import {RequiredFactorForm} from './required-factor';
import {signInRetryDeadline, signInRetrySeconds} from '../../kit/accounts/sign-in-retry';

type SignInPort = Pick<BrowserAccountEntry, 'getSnapshot' | 'subscribeAuthorityRetired' | 'signIn' | 'redeemLocalMove' | 'resetLocalPassword' | 'completeChallenge' | 'beginRequiredTwoStep' | 'confirmRequiredTwoStep'>;
export type SignInFormState = Readonly<{username: string; inputEpoch: number; busy: boolean; retryAt?: number; factor?: RequiredFactorForm}>;

/** Only the live submitted password bridges password/MFA to required setup.
 * Entry remains the sole owner of sessions, proofs, currentness and admission. */
export class AccountSignInForm {
  private state: SignInFormState = Object.freeze({username: '', inputEpoch: 0, busy: false});
  private readonly listeners = new Set<() => void>();
  private readonly off: () => void;
  private epoch = 0;
  private disposed = false;
  #password = '';
  constructor(private readonly entry: SignInPort) {this.off = entry.subscribeAuthorityRetired(this.clear);}
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private publish(value: SignInFormState) {if (!this.disposed) {this.state = Object.freeze(value); for (const listener of this.listeners) listener();}}
  clear = () => {
    ++this.epoch; this.#password = ''; this.state.factor?.dispose();
    this.publish({username: this.state.username, inputEpoch: this.state.inputEpoch + 1, busy: false});
  };
  setUsername(username: string) {if (!this.disposed) this.publish({...this.state, username});}
  private rememberRetry(error: unknown, epoch: number) {
    if (this.disposed || this.epoch !== epoch || this.entry.getSnapshot().phase !== 'password') return;
    const retryAt = signInRetryDeadline(error);
    if (retryAt !== undefined) this.publish({...this.state, retryAt});
  }
  private async continuation(epoch: number) {
    if (this.disposed || this.epoch !== epoch) return;
    const phase = this.entry.getSnapshot().phase;
    if (phase === 'two-step-setup' && this.#password) {
      const factor = new RequiredFactorForm(this.entry, this.#password, () => !this.disposed && this.epoch === epoch);
      this.#password = ''; this.publish({...this.state, factor});
      await factor.enrol();
    } else if (phase !== 'challenge') this.#password = '';
  }
  signIn(username: string, password: string) {return this.submit(username, password, 'password', this.entry.signIn.bind(this.entry));}
  /** The new Local password belongs only to the already-proved member move. */
  redeemLocalMove(username: string, password: string) {return this.submit(username, password, 'move-local', this.entry.redeemLocalMove.bind(this.entry));}
  /** Entry owns the one-use proof, temporary family and ordinary sign-in after reset. */
  resetMemberPassword(username: string, code: string, password: string, secondFactorCode = '') {
    if (!code) return Promise.resolve();
    return this.submit(username, password, 'password', (name, next) => this.entry.resetLocalPassword({kind: 'member', username: name, code}, next, secondFactorCode));
  }
  private async submit(username: string, password: string, phase: 'password' | 'move-local', command: (username: string, password: string) => Promise<void>) {
    if (this.disposed || this.state.busy || signInRetrySeconds(this.state.retryAt) > 0 || !username || !password || this.entry.getSnapshot().phase !== phase) return;
    this.clear(); const epoch = this.epoch; this.#password = password;
    this.publish({...this.state, username, busy: true});
    if (this.disposed || this.epoch !== epoch) return;
    try {
      await command(username, password);
      this.rememberRetry(this.entry.getSnapshot().error, epoch);
      await this.continuation(epoch);
    }
    catch (error) {if (!this.disposed && this.epoch === epoch) {this.#password = ''; this.rememberRetry(error, epoch);}throw error;}
    finally {if (!this.disposed && this.epoch === epoch) this.publish({...this.state, busy: false});}
  }
  async completeChallenge(code: string) {
    if (this.disposed || this.state.busy || this.entry.getSnapshot().phase !== 'challenge' || !code.trim()) return;
    const epoch = this.epoch; this.publish({...this.state, busy: true});
    try {await this.entry.completeChallenge(code); await this.continuation(epoch);}
    finally {if (!this.disposed && this.epoch === epoch) this.publish({...this.state, busy: false});}
  }
  dispose() {if (this.disposed) return; this.clear(); this.disposed = true; this.off(); this.listeners.clear();}
}
