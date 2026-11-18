import {serverNow} from '../../../../packages/client-core/src/server-clock';
import {NativeModules, Linking} from 'react-native';
import {MFARequiredError, parseNativeAccountContinuation, parseAccountReturn,
  type IdentityProvider, type IdentityTransaction, type MFAChallenge, type NativeAccountContinuation, type Registration} from '../../../../packages/client-core/src/hosted-security';
import type {PorticoServicesAccountClient, PorticoProviderProof} from '../../../../packages/client-core/src/portico-services-native';
import {CodedError} from '../../../../packages/client-core/src/server-messages';
import {ApiError, routeBase64, type HostedSession} from '../../../../packages/client-core/src';
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
 * session credential. Provider proofs stay only in the current opening's memory. */
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
  private offered: readonly IdentityProvider[] = [];
  private providerStarting = false;
  private providerFlow?: {provider: IdentityProvider; proof: PorticoProviderProof; expiresAt: string; suggestedName?: string};
  private providerResult?: HostedSession;
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
    this.offered = options.providers.filter(value => value.enabled).map(value => value.id);
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
  /** The hosted page owns the emailed code and new password. Return to ordinary app sign-in;
   * resetting never grants this app a session or needs an invented callback. */
  async forgot(callerCurrent?: () => boolean) {
    this.checkCaller(callerCurrent);
    await Linking.openURL(hostedUrl + '/reset');
    this.checkCaller(callerCurrent);
  }
  /** The same entry point used by phone/tablet and TV account buttons. */
  async provider(provider: IdentityProvider, callerCurrent?: () => boolean): Promise<IdentityTransaction> {
    this.checkCaller(callerCurrent);
    if (!this.offered.includes(provider)) throw new CodedError('unavailable', 'native_provider_unavailable');
    if (this.providerStarting || this.authenticating) throw new CodedError('busy', 'native_account_attempt_active');
    const nativeApple = provider === 'apple' && this.bridge.canSignInApple === true;
    if (!nativeApple && this.bridge.canOpenBrowser !== true) throw new CodedError('unavailable', 'native_provider_unavailable');
    this.providerStarting = true; this.providerFlow = undefined; this.providerResult = undefined;
    try {
      await this.checkpoint({mfa: undefined}, callerCurrent);
      const secret: string = nativeApple ? '' : await this.bridge.newBinding(); this.checkCaller(callerCurrent);
      if (!nativeApple && !/^[A-Za-z0-9_-]{43}$/.test(secret)) throw new CodedError('invalid_response', 'native_provider_binding_invalid');
      const hash: string = nativeApple ? '' : await NativeModules.PorticoNetwork.sha256(routeBase64(new TextEncoder().encode(secret))); this.checkCaller(callerCurrent);
      const started = await this.api().startProvider(provider, hash, nativeApple, this.abort.signal); this.checkCaller(callerCurrent);
      let proof: PorticoProviderProof, suggestedName: string | undefined;
      if (nativeApple && typeof started.nonce === 'string') {
        const assertion = await this.bridge.signInApple(started.nonce); this.checkCaller(callerCurrent);
        proof = {id: started.id, identityToken: assertion.idToken}; suggestedName = assertion.displayName;
      } else if (typeof started.authorizeUrl === 'string') {
        const returned = await this.bridge.openBrowser(started.authorizeUrl, started.id); this.checkCaller(callerCurrent);
        const parsed = parseAccountReturn(returned, started.id, hostedUrl);
        if (!parsed.completionCode) throw new CodedError('invalid_return', 'native_provider_return_incomplete');
        proof = {id: started.id, code: parsed.completionCode, secret};
      } else throw new CodedError('invalid_response', 'native_provider_start_invalid');
      const flow = {provider, proof, expiresAt: started.expiresAt, suggestedName};
      this.providerFlow = flow;
      let needsUsername = false, mfaRequired = false;
      try {await this.finishProvider(undefined, suggestedName, undefined, {signal: this.abort.signal, isCurrent: callerCurrent ?? (() => true)});}
      catch (error) {
        this.checkCaller(callerCurrent);
        if (error instanceof ApiError && error.code === 'username_needed') needsUsername = true;
        else if (error instanceof MFARequiredError) mfaRequired = true;
        else throw error;
      }
      this.checkCaller(callerCurrent);
      return {transactionId: proof.id, provider, mode: 'login', status: needsUsername ? 'onboarding' : 'verified', expiresAt: flow.expiresAt,
        contactVerified: true, needsUsername, mfaRequired, ...(suggestedName ? {suggestedName} : {})};
    } finally {this.providerStarting = false;}
  }
  /** `acceptTerms`: a new account's form had the 13-or-older and Terms of Use box ticked (definition 01 rule 55). */
  async finishProvider(username?: string, displayName?: string, factor?: string,
    caller?: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>, acceptTerms = false): Promise<HostedSession> {
    const predicate = caller?.isCurrent.bind(caller);
    this.checkCaller(predicate);
    if (caller?.signal.aborted) throw new CodedError('cancelled', 'native_provider_changed');
    // An existing account may have completed during provider(); the button's follow-up only opens its directory.
    if (this.providerResult) return this.providerResult;
    const flow = this.providerFlow;
    if (!flow || Date.parse(flow.expiresAt) <= serverNow()) throw new CodedError('expired', 'native_provider_expired');
    const current = () => this.providerFlow === flow && !caller?.signal.aborted && (!predicate || predicate());
    const result = await this.authenticate(async signal => {
      const linked = linkNativeAbortSignals([this.abort.signal, signal, ...(caller ? [caller.signal] : [])]);
      try {
        const challenge = this.continuation?.mfa;
        return challenge ? await this.api().mfa(challenge, factor ?? '')
          : await this.api().completeProvider(flow.proof, {username, displayName: displayName ?? flow.suggestedName, ...(acceptTerms ? {acceptTerms} : {})}, linked.signal);
      } finally {linked.dispose();}
    }, current);
    this.providerResult = result; this.providerFlow = undefined;
    return result;
  }
  async sendContact(_address: string): Promise<{challengeId: string; maskedAddress: string; expiresAt: string}> {
    throw new CodedError('unavailable', 'native_provider_unavailable');
  }
  async verifyContact(_challengeId: string, _code: string): Promise<void> {
    throw new CodedError('unavailable', 'native_provider_unavailable');
  }
  async register(username: string, displayName: string, password: string, contactAddress: string, acceptTerms: boolean) {
    this.registrationCollision = undefined;
    this.check();
    const value = await this.api().register({username, displayName, password, contactAddress, acceptTerms});
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
  dispose() {if (this.abort.signal.aborted) return; this.abort.abort(); this.bridge?.cancel(); this.off(); this.providerFlow = undefined; this.providerResult = undefined; this.registrationCollision = undefined; this.factorWitness = undefined; if (this.authenticating) nativeAccounts.cancel();}
}
