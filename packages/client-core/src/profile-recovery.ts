import type {HttpLocalApi, LocalSession} from './index.ts';
import type {DirectSnapshot} from './profile-management.ts';
import {IdentityClient} from './identity-client.ts';
import type {porticoSignIn, HostedRequestApi} from './portico-servers.ts';
import type {SignInKind} from './app-sessions.ts';
import {CodedError} from './server-messages.ts';

export type PorticoProofSource = () => Promise<Readonly<{hosted: HostedRequestApi; origin: string; accountCurrent: () => boolean}>>;
export type ProfileRecoveryPhase = 'password' | 'portico' | 'challenge' | 'pin' | 'done';
export type ProfileRecoveryState = Readonly<{phase: ProfileRecoveryPhase; busy?: boolean; error?: unknown}>;

export type ProfileRecoveryPorts = Readonly<{
  api: HttpLocalApi;
  snapshot: DirectSnapshot;
  profileId: string;
  /** The device's own sign-in. Forgot PIN never ends it. */
  selectedFamilyId: string;
  kind: SignInKind;
  /** The same server, account and sign-in as when the flow opened, checked after every await. */
  authorityCurrent: () => boolean;
  /** Kept for callers; the rebuilt server needs no second sign-in for Forgot PIN. */
  temporary?: (session: LocalSession) => HttpLocalApi;
  portico?: PorticoProofSource;
  signIn?: typeof porticoSignIn;
}>;

const refused = (error: unknown, code: string) => (error as {code?: unknown} | null)?.code === code;

/**
 * Forgot PIN, shared by web and the phone and TV apps. On a server with local sign-in the account's password,
 * and the two-step code when that is on, set a new PIN from any profile, or from Who's watching
 * (`PUT /v1/profiles/{id}/pin`): the device keeps its sign-in and stays where it was. The account's main
 * profile needs neither. The password and code are held only until the PIN is saved or the flow closes.
 *
 * Phases: `password`, then `pin`, then `challenge` only when the server asks for the two-step code, then `done`.
 * A server that uses Portico Accounts is not handled here yet (`portico` answers that it is not available).
 */
export class ProfileRecoveryController {
  private state: ProfileRecoveryState;
  private readonly listeners = new Set<() => void>();
  private readonly management: boolean;
  private epoch = 0;
  private controller?: AbortController;
  private password_?: string;
  private pin_?: string;
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
  private operation() {
    this.controller?.abort(); const epoch = ++this.epoch, controller = this.controller = new AbortController();
    return {signal: controller.signal, current: () => !this.disposed && this.epoch === epoch && !controller.signal.aborted && this.ports.authorityCurrent()};
  }
  /** Keeps the password for the save; nothing is sent until the new PIN is chosen. */
  async password(password: string) {
    if (this.disposed || !this.ports.authorityCurrent() || this.state.phase !== 'password' || !password || this.state.busy) return;
    this.password_ = password;
    this.publish({phase: 'pin'});
  }
  async portico(_supply: PorticoProofSource | undefined = this.ports.portico) {
    if (this.disposed || this.state.phase !== 'portico') return;
    this.publish({phase: 'portico', error: new CodedError('not_available', 'This is not available on this server yet.')});
  }
  async code(code: string) {
    if (this.disposed || this.state.phase !== 'challenge' || this.state.busy || !this.pin_ || !code.trim()) return;
    await this.save(this.pin_, code.trim());
  }
  async reset(pin: string) {
    if (this.disposed || this.state.phase !== 'pin' || !/^\d{4}$/.test(pin) || this.state.busy) return;
    await this.save(pin);
  }
  private async save(pin: string, code?: string) {
    if (!this.ports.authorityCurrent()) return;
    if (!this.management && !this.password_) {this.publish({phase: 'password'}); return;}
    const operation = this.operation(), phase = this.state.phase;
    this.publish({phase, busy: true});
    try {
      const proof = this.management ? undefined : {currentPassword: this.password_!, ...(code ? {code} : {})};
      await new IdentityClient({request: (path, method, body) => this.ports.api.request(path, method, body, operation.signal)}).resetPIN(this.ports.profileId, pin, proof, operation.signal);
      if (!operation.current()) return;
      this.password_ = undefined; this.pin_ = undefined;
      this.publish({phase: 'done'});
    } catch (error) {
      if (!operation.current()) return;
      if (refused(error, 'two_factor_invalid') && !code) {this.pin_ = pin; this.publish({phase: 'challenge'}); return;}
      if (refused(error, 'current_password_incorrect')) {this.password_ = undefined; this.pin_ = undefined; this.publish({phase: 'password', error}); return;}
      this.publish({phase, error});
    }
  }
  dispose() {
    if (this.disposed) return;
    this.disposed = true; ++this.epoch; this.controller?.abort(); this.password_ = undefined; this.pin_ = undefined;
    this.listeners.clear();
  }
}
