import type {HttpLocalApi, LocalSession} from './index.ts';
import type {DirectSnapshot} from './profile-management.ts';
import {IdentityClient} from './identity-client.ts';
import type {porticoSignIn, HostedRequestApi} from './portico-servers.ts';
import type {SignInKind} from './app-sessions.ts';
import {CodedError} from './server-messages.ts';
import {callServer} from './query/server-api.ts';
import {envelopePayload, type Proof} from './portico-account.ts';

export type PorticoProofSource = () => Promise<Readonly<{hosted: HostedRequestApi; origin: string; accountCurrent: () => boolean}>>;
/** Captured signed-in Portico Account. A platform keeps renewal and account-switch fencing here. */
export type PorticoPINProofSource = Readonly<{
  accountId: string; isCurrent: () => boolean;
  identity: (serverId: string, challenge: string, proof: Proof, signal: AbortSignal) => Promise<{identity: unknown}>;
}>;
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
  porticoPIN?: PorticoPINProofSource;
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
 * Portico recovery uses `porticoPassword` to obtain Services’ password-checked note before choosing the PIN.
 * The old `portico` entry remains unavailable until the screen supplies the typed-password form.
 */
export class ProfileRecoveryController {
  private state: ProfileRecoveryState;
  private readonly listeners = new Set<() => void>();
  private readonly management: boolean;
  private epoch = 0;
  private controller?: AbortController;
  private password_?: string;
  private pin_?: string;
  private porticoProof?: Readonly<{challenge: string; identity: unknown; source: PorticoPINProofSource}>;
  private disposed = false;
  private readonly ports: ProfileRecoveryPorts;
  constructor(ports: ProfileRecoveryPorts) {
    this.ports = ports;
    if (!ports.snapshot.profiles.some(profile => profile.id === ports.profileId)) throw new Error('This profile is no longer available.');
    this.management = ports.kind === 'local' && ports.snapshot.canManage;
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
  /** Passwords go only to Services. The signed note stays private until the person chooses a PIN. */
  async porticoPassword(password: string, twoStepCode = '', source = this.ports.porticoPIN) {
    if (this.disposed || this.state.phase !== 'portico' || this.state.busy || !password || !source || !this.ports.authorityCurrent()) return;
    const operation = this.operation(); this.porticoProof = undefined;
    const check = () => {
      if (!operation.current() || !source.isCurrent() || this.ports.snapshot.account.hostedAccountId && this.ports.snapshot.account.hostedAccountId !== source.accountId)
        throw new CodedError('account_changed', 'The Portico Account changed. Try again.');
    };
    this.publish({phase: 'portico', busy: true});
    try {
      check();
      const asked = await callServer(this.ports.api, 'POST /v1/auth/portico/challenge', {signal: operation.signal});
      check();
      if (asked.serverId !== this.ports.snapshot.serverId || !asked.challenge) throw new CodedError('server_mismatch', 'The server answered as a different server.');
      const answer = await source.identity(asked.serverId, asked.challenge, {currentPassword: password, ...(twoStepCode ? {twoStepCode} : {})}, operation.signal);
      check();
      const note = envelopePayload<{accountId: string; serverId: string; challenge: string; passwordChecked?: boolean}>(answer.identity as {payload: string});
      if (note.accountId !== source.accountId || note.serverId !== asked.serverId || note.challenge !== asked.challenge || note.passwordChecked !== true)
        throw new CodedError('invalid_response', 'The server sign-in note did not confirm the password.');
      this.porticoProof = {challenge: asked.challenge, identity: answer.identity, source};
      this.publish({phase: 'pin'});
    } catch (error) {if (operation.current()) this.publish({phase: 'portico', error});}
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
    if (this.ports.kind === 'portico') {await this.savePortico(pin); return;}
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
  private async savePortico(pin: string) {
    const proof = this.porticoProof;
    if (!proof || !proof.source.isCurrent()) {this.porticoProof = undefined; this.publish({phase: 'portico'}); return;}
    const operation = this.operation(); this.porticoProof = undefined;
    this.publish({phase: 'pin', busy: true});
    try {
      if (!proof.source.isCurrent() || !operation.current()) throw new CodedError('account_changed', 'The Portico Account changed. Try again.');
      await new IdentityClient(this.ports.api).resetPorticoPIN(this.ports.profileId, pin, proof, operation.signal);
      if (operation.current() && proof.source.isCurrent()) this.publish({phase: 'done'});
      else if (operation.current()) this.publish({phase: 'portico'});
    } catch (error) {if (operation.current()) this.publish({phase: 'portico', error});}
  }
  dispose() {
    if (this.disposed) return;
    this.disposed = true; ++this.epoch; this.controller?.abort(); this.password_ = undefined; this.pin_ = undefined; this.porticoProof = undefined;
    this.listeners.clear();
  }
}
