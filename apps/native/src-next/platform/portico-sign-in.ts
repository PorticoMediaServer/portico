import {serverNow} from '../../../../packages/client-core/src/server-clock';
import {NativeModules} from 'react-native';
import {MFARequiredError, parseNativeAccountContinuation,
  type IdentityProvider, type IdentityTransaction, type MFAChallenge, type NativeAccountContinuation, type Registration} from '../../../../packages/client-core/src/hosted-security';
import type {PorticoServicesAccountClient} from '../../../../packages/client-core/src/portico-services-native';
import {CodedError} from '../../../../packages/client-core/src/server-messages';
import {ApiError, type HostedSession} from '../../../../packages/client-core/src';
import {nativeAccounts} from './accounts';
import {linkNativeAbortSignals} from './abort-signals';
import {hostedUrl} from './account-authority';
import {serializeContinuation, observeContinuationBinding, continuationBindingCurrent} from './continuation-queue';

/** An opaque, frozen witness for the private factor challenge this port holds. It
 * carries no challenge token and cannot be forged: only this exact cached object is
 * accepted, and only while it is bound to the exact challenge the port still holds.
 * Callers hand the reference back; they cannot build an interchangeable one. */
declare const nativeFactorWitness: unique symbol;
export type NativeFactorWitness = Readonly<{readonly [nativeFactorWitness]: true}>;

/** Root 43: the one typed early outcome the signup form may put on the username
 * field. Only a confirmed public-handle collision carries this closed code; a
 * generic conflict, a transport failure or an email/provider answer never does. */
export const porticoUsernameUnavailable = (error: unknown): boolean =>
  error instanceof ApiError && error.status === 409 && error.code === 'username_unavailable';

/** Explicit Portico sign-in only, at Portico Services (`PorticoServicesAccountClient`). Native
 * continuation storage contains a binding and the two-step challenge, never a password or a
 * session credential. Google and Apple wait for their own follow-up: the options offer neither. */
export class NativePorticoSignIn {
  private readonly isCurrent: () => boolean;
  constructor(isCurrent: () => boolean = () => true) {this.isCurrent = isCurrent;}
  private readonly abort = new AbortController();
  private readonly bridge = NativeModules.PorticoAccountAuth;
  private client?: PorticoServicesAccountClient;
  private continuation?: NativeAccountContinuation;
  private bindingWitness?: ReturnType<typeof observeContinuationBinding>;
  private registration?: Registration;
  private registrationCollision?: Readonly<{original: Registration; verified: Registration}>;
  /** The one issued factor witness and the exact private challenge it was bound to. */
  private factorWitness?: Readonly<{witness: NativeFactorWitness; challenge: MFAChallenge}>;
  private authenticating = false;
  private readonly off = nativeAccounts.onCancel(() => this.dispose());
  private check() {if (this.abort.signal.aborted || !this.isCurrent() || this.abort.signal.aborted || (this.bindingWitness && !continuationBindingCurrent(hostedUrl, this.bindingWitness))) throw Object.assign(new Error('native_portico_cancelled'), {name: 'AbortError'});}
  private checkCaller(callerCurrent?: () => boolean) {
    this.check();
    if (callerCurrent && !callerCurrent()) throw new CodedError('cancelled', 'native_portico_changed');
    this.check();
  }
  private async checkpoint(update: Partial<NativeAccountContinuation>, callerCurrent?: () => boolean, settledCurrent = callerCurrent) {
    this.checkCaller(callerCurrent);
    const original = this.continuation;
    if (!original) throw new Error('native_continuation_missing');
    const next = {...original, ...update}, bytes = JSON.stringify(next), binding = original.binding;
    const current = () => {this.checkCaller(callerCurrent); if (this.continuation !== original || this.continuation.binding !== binding) throw new CodedError('cancelled', 'native_portico_changed');};
    await serializeContinuation(hostedUrl, async () => {
      current(); await this.bridge.saveContinuation(hostedUrl, bytes); current();
      this.continuation = next;
    });
    this.checkCaller(settledCurrent);
  }
  async prepare() {
    await serializeContinuation(hostedUrl, async () => {
      this.check(); const raw: unknown = await this.bridge.loadContinuation(hostedUrl); this.check();
      let saved: NativeAccountContinuation | undefined;
      if (typeof raw === 'string') {try {saved = parseNativeAccountContinuation(raw);} catch { /* Invalid noncredential continuation is replaced. */ }}
      const binding: unknown = saved?.binding ?? await this.bridge.newBinding(); this.check();
      if (typeof binding !== 'string' || !/^[A-Za-z0-9_-]{43}$/.test(binding)) throw new Error('native_continuation_invalid');
      const continuation = saved ?? {version: 1 as const, binding, expiresAt: new Date(serverNow() + 15 * 60000).toISOString()};
      const bytes = JSON.stringify(continuation);
      this.check(); await this.bridge.saveContinuation(hostedUrl, bytes); this.check();
      this.continuation = continuation;
      this.bindingWitness = observeContinuationBinding(hostedUrl, binding);
      this.client = nativeAccounts.porticoClient();
    });
    this.check(); if (!this.client) throw new Error('native_continuation_missing');
    const options = await this.client.options(); this.check();
    return options;
  }
  private api() {this.check(); if (!this.client) throw new Error('native_continuation_missing'); return this.client;}
  private async authenticate(work: (signal: AbortSignal) => Promise<HostedSession>, callerCurrent?: () => boolean, settledCurrent = callerCurrent) {
    this.check(); if (this.authenticating) throw Object.assign(new Error('native_account_attempt_active'), {name: 'AbortError'});
    this.authenticating = true;
    // Every admission carries this source's own lifetime, whether or not the caller
    // supplied a predicate, and a supplied predicate is combined with it rather than
    // replacing it: a retired source can never adopt a known result.
    const witness = this.bindingWitness;
    const sourceCurrent = () => !this.abort.signal.aborted && this.isCurrent() && !this.abort.signal.aborted
      && !!witness && this.bindingWitness === witness && continuationBindingCurrent(hostedUrl, witness);
    const admitted = () => {
      try {return sourceCurrent() && (!callerCurrent || callerCurrent() === true) && sourceCurrent();} catch {return false;}
    };
    try {
      const result = await nativeAccounts.authenticatePorticoAccount(async signal => {this.check(); return work(signal);}, admitted);
      await this.checkpoint({mfa: undefined, transactionId: undefined, completionCode: undefined}, callerCurrent, settledCurrent); return result;
    } catch (error) {
      if (error instanceof MFARequiredError) await this.checkpoint({mfa: error.challenge}, callerCurrent, settledCurrent);
      throw error;
    } finally {this.authenticating = false;}
  }
  async password(username: string, password: string, callerCurrent?: () => boolean) {
    this.checkCaller(callerCurrent);
    return this.authenticate(() => this.api().login(username, password), callerCurrent);
  }
  /** Issue, or reuse, the witness for the private challenge this port holds. The
   * returned object is frozen and opaque; the token and challenge never leave here. */
  factorChallengeIdentity(): NativeFactorWitness | undefined {
    const challenge = this.continuation?.mfa;
    if (!challenge) {this.factorWitness = undefined; return undefined;}
    if (this.factorWitness?.challenge !== challenge) {
      this.factorWitness = Object.freeze({witness: Object.freeze({}) as NativeFactorWitness, challenge});
    }
    return this.factorWitness.witness;
  }
  factor(code: string, caller?: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>, witness?: NativeFactorWitness) {
    const challenge = this.continuation?.mfa;
    if (!challenge) throw new Error('native_factor_missing');
    // The old one-argument behaviour exists only when neither a caller nor a witness is
    // supplied; a caller without a witness is a forged new-path attempt and is refused.
    if (!witness && !caller) return this.authenticate(() => this.api().mfa(challenge, code));
    const issued = this.factorWitness;
    if (!witness || !caller || !issued || witness !== issued.witness || issued.challenge !== challenge
      || !(Date.parse(challenge.expiresAt) > serverNow())) throw new CodedError('cancelled', 'native_factor_changed');
    const formLive = () => !this.abort.signal.aborted && this.isCurrent() && caller.isCurrent() && !caller.signal.aborted;
    const callerLive = () => formLive() && this.continuation?.mfa === challenge;
    // Admission and the pre-write checks require the original challenge. Our own
    // successful checkpoint consumes it; settlement still requires the same live
    // form, source and signal, without requiring the challenge we just cleared.
    return this.authenticate(async signal => {
      if (!callerLive()) throw new CodedError('cancelled', 'native_factor_changed');
      const linked = linkNativeAbortSignals([this.abort.signal, caller.signal, signal]);
      try {return await this.api().mfa(challenge, code);} finally {linked.dispose();}
    }, callerLive, formLive);
  }
  /** Account help sends a reset code to the account's confirmed email address; the new
   * password is chosen at web.getportico.tv. Services answers the same way whether or not
   * the address has an account. */
  async forgot(email: string) {
    return this.api().recover(email.trim().toLowerCase());
  }
  /** Google and Apple are not offered yet (their own follow-up). */
  async provider(_provider: IdentityProvider, _callerCurrent?: () => boolean): Promise<IdentityTransaction> {
    throw new CodedError('unavailable', 'native_provider_unavailable');
  }
  async finishProvider(_username?: string, _displayName?: string, _factor?: string,
    _caller?: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>): Promise<HostedSession> {
    throw new CodedError('unavailable', 'native_provider_unavailable');
  }
  async sendContact(_address: string): Promise<{challengeId: string; maskedAddress: string; expiresAt: string}> {
    throw new CodedError('unavailable', 'native_provider_unavailable');
  }
  async verifyContact(_challengeId: string, _code: string): Promise<void> {
    throw new CodedError('unavailable', 'native_provider_unavailable');
  }
  async register(username: string, displayName: string, password: string, contactAddress: string) {
    this.registrationCollision = undefined;
    this.check();
    const value = await this.api().register({username, displayName, password, contactAddress});
    this.check(); this.registration = Object.freeze({...value}); return this.registration;
  }
  async resendRegistration(original: Registration, caller: Readonly<{isCurrent: () => boolean}>) {
    const current = () => caller.isCurrent() && this.registration === original;
    this.checkCaller(current);
    if (original.contactVerified || original.status !== 'contact_pending') throw new CodedError('cancelled', 'native_registration_changed');
    const fresh = await this.api().resendRegistration(original);
    this.checkCaller(current);
    if (fresh.registrationId !== original.registrationId || fresh.maskedAddress !== original.maskedAddress
      || fresh.status === 'completed') throw new CodedError('account_changed', 'native_registration_changed');
    this.registrationCollision = undefined;
    this.registration = Object.freeze({...fresh}); return this.registration;
  }
  async verifyRegistration(code: string, original: Registration, caller: Readonly<{isCurrent: () => boolean}>, _replacement?: Readonly<{username: string; displayName: string}>) {
    const registration = original;
    if (!registration || this.registration !== registration) throw new Error('native_registration_missing');
    const callerCurrent = caller.isCurrent.bind(caller);
    const binding = this.continuation?.binding, witness = this.bindingWitness;
    const current = () => {
      try {this.checkCaller(callerCurrent);} catch {return false;}
      return !!witness && this.bindingWitness === witness && continuationBindingCurrent(hostedUrl, witness)
        && this.registration === registration && this.continuation?.binding === binding;
    };
    if (!binding || !current()) throw new CodedError('cancelled', 'native_registration_changed');
    return this.authenticate(() => this.api().verifyRegistration(registration, code), current);
  }
  registrationAfterCollision(original: Registration) {
    const collision = this.registrationCollision;
    return collision?.original === original && collision.verified === this.registration ? collision.verified : undefined;
  }
  dispose() {if (this.abort.signal.aborted) return; this.abort.abort(); this.bridge?.cancel(); this.off(); this.registrationCollision = undefined; this.factorWitness = undefined; if (this.authenticating) nativeAccounts.cancel();}
}
