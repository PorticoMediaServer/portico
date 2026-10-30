import type {TwoFactorEnrolment} from '@core/identity-client.ts';

export type RequiredFactorState = Readonly<{
  phase: 'enrolling' | 'code' | 'recovery' | 'closed';
  busy: boolean;
  enrolment?: TwoFactorEnrolment;
  codes?: readonly string[];
  error?: unknown;
}>;
export type RequiredFactorPort = Readonly<{
  beginRequiredTwoStep(password: string): Promise<TwoFactorEnrolment | undefined>;
  confirmRequiredTwoStep(password: string, code: string): Promise<boolean>;
}>;

/** Live form lifetime only. The account-entry controller owns all family,
 * restricted-session and server checks; this class neither reads nor writes
 * credentials. Its only proof is the password submitted on this mounted page. */
export class RequiredFactorForm {
  private state: RequiredFactorState = Object.freeze({phase: 'enrolling', busy: false});
  private readonly listeners = new Set<() => void>();
  private epoch = 0;
  private disposed = false;
  #password: string;
  constructor(private readonly port: RequiredFactorPort, password: string, private readonly isCurrent: () => boolean) {this.#password = password;}
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private current(epoch: number) {return !this.disposed && epoch === this.epoch && this.isCurrent();}
  private publish(state: RequiredFactorState) {
    if (this.disposed) return;
    this.state = Object.freeze(state);
    for (const listener of this.listeners) listener();
  }
  async enrol() {
    if (!this.#password || this.disposed || this.state.busy || this.state.phase !== 'enrolling' || !this.isCurrent()) return;
    const epoch = ++this.epoch;
    this.publish({phase: 'enrolling', busy: true});
    try {
      const enrolment = await this.port.beginRequiredTwoStep(this.#password);
      if (!this.current(epoch)) {this.close(); return;}
      this.publish(enrolment ? {phase: 'code', busy: false, enrolment} : {phase: 'enrolling', busy: false});
    } catch (error) {if (this.current(epoch)) this.publish({phase: 'enrolling', busy: false, error});}
  }
  async confirm(code: string) {
    const enrolment = this.state.enrolment;
    if (this.disposed || this.state.phase !== 'code' || this.state.busy || !enrolment || !this.#password || !/^\d{6}$/.test(code) || !this.isCurrent()) return;
    const epoch = ++this.epoch;
    this.publish({phase: 'code', busy: true, enrolment});
    try {
      const confirmed = await this.port.confirmRequiredTwoStep(this.#password, code);
      if (!this.current(epoch)) {this.close(); return;}
      if (confirmed) {
        this.#password = '';
        // The key/URI retire at confirmation. Only the server-issued, original
        // recovery-code sequence remains until Done or this form's retirement.
        this.publish({phase: 'recovery', busy: false, codes: enrolment.recoveryCodes});
      } else this.publish({phase: 'code', busy: false, enrolment});
    } catch (error) {if (this.current(epoch)) this.publish({phase: 'code', busy: false, enrolment, error});}
  }
  done() {
    if (this.disposed || this.state.phase !== 'recovery' || !this.isCurrent()) return false;
    this.close(); return true;
  }
  close() {
    ++this.epoch; this.#password = '';
    this.publish({phase: 'closed', busy: false});
  }
  dispose() {
    this.close(); this.disposed = true; this.listeners.clear();
  }
}
