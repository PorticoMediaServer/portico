import {ApiError} from '@core/index.ts';
import {CodedError} from '@core/server-messages.ts';
import {AccountAttemptIds, MFARequiredError, type IdentityOptions, type IdentityProvider, type IdentityTransaction, type MFAChallenge, type Registration} from '@core/hosted-security.ts';
import {routeBody, routePath} from '../../../packages/contracts/src/hosted/routes.ts';
import {accountIdentity, currentAccountWork, sameAccount, type AccountIdentity, type ServicesAccount} from './session';

export type AuthMode = 'signin' | 'create' | 'verify' | 'forgot' | 'sent' | 'factor' | 'recovery' | 'provider' | 'provider-stopped' | 'ready';
export type AuthSnapshot = Readonly<{initializing: boolean; providerNavigating?: boolean; mode: AuthMode; options?: IdentityOptions; optionsFailed?: boolean; registration?: Registration; provider?: IdentityProvider; suggestedName?: string; verifiedEmail?: string; busy: boolean; retryAt: number; resends: number; error?: unknown}>;
type InitialAuthContinuation = Readonly<{mode: AuthMode; registration?: Registration; transaction?: IdentityTransaction}>;
/** Local form controller. The singleton CredentialSessionService owns every account session. */
export class ServicesAuthFlow {
  private state: AuthSnapshot;
  private listeners = new Set<() => void>();
  private operation?: AbortController;
  private initialized?: Promise<void>;
  private initialContinuation?: InitialAuthContinuation;
  private initialContinuationRetired = false;
  private disposed = false;
  private challenge?: MFAChallenge;
  private transaction?: IdentityTransaction;
  private providerRefresh?: IdentityTransaction;
  private readyIdentity?: AccountIdentity;
  private ids = new AccountAttemptIds(async () => crypto.randomUUID());
  constructor(private readonly account: ServicesAccount, private readonly signedIn: (gesture?: boolean) => void,
    mode: 'signin' | 'create' | 'forgot' = 'signin', private readonly redirect = (url: string) => location.assign(url), private readonly providerFailure?: IdentityProvider | 'unknown') {
    this.state = {initializing: true, mode, busy: false, retryAt: 0, resends: 0};
  }
  getSnapshot = () => this.state;
  /** Presentation reads the canonical session only while its accepted family is current. */
  readyAccount() {
    if (this.state.mode !== 'ready' || !this.readyIdentity) return;
    const session = this.account.service.getSnapshot().session;
    if (!session?.familyId || !sameAccount(this.readyIdentity, accountIdentity(session))) return;
    return {name: session.account.displayName, username: session.account.username};
  }
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  private publish(value: Partial<AuthSnapshot>) { if (this.disposed) return; this.state = {...this.state, ...value}; for (const listener of this.listeners) listener(); }
  dispose() { this.disposed = true; this.operation?.abort(); this.listeners.clear(); this.initialContinuation = undefined; this.challenge = undefined; this.transaction = undefined; this.providerRefresh = undefined; this.ids.clear(); }
  go(mode: AuthMode) {
    if (this.operation) return;
    if (this.initialContinuation && mode !== this.state.mode) this.initialContinuationRetired = true;
    // An explicit Back/Start again abandons this password login's MFA challenge.
    // Retire only that challenge and its login request id (never another attempt's
    // ids), and keep any account-wide cool-off; the next explicit submit is a new intent.
    if (this.challenge !== undefined && mode !== 'factor' && mode !== 'recovery') this.ids.clear('login');
    this.challenge = undefined;
    // An account-wide cool-off is a deadline, not a mode: navigation never shortens it.
    this.publish({mode, error: undefined, retryAt: this.state.retryAt > Date.now() ? this.state.retryAt : 0});
  }
  recovery() { if (!this.operation && this.challenge) this.publish({mode: this.state.mode === 'factor' ? 'recovery' : 'factor', error: undefined}); }
  startAgain() { if (!this.operation) { this.publish({registration: undefined, resends: 0}); this.go('create'); } }
  initialize(): Promise<void> {
    if (this.initialized) return this.initialized;
    // Reserve before load publishes busy: a listener may synchronously retry.
    let resolve!: () => void, reject!: (error: unknown) => void;
    const attempt = new Promise<void>((done, fail) => {resolve = done; reject = fail;});
    this.initialized = attempt;
    const release = () => {if (this.initialized === attempt && !this.state.options) this.initialized = undefined;};
    void this.load().then(() => {release(); resolve();}, error => {release(); reject(error);});
    return attempt;
  }
  retryInitialization(): Promise<void> {
    if (this.disposed) return Promise.resolve();
    if (this.state.options || !this.state.optionsFailed || this.operation || this.state.retryAt > Date.now())
      return this.initialized ?? Promise.resolve();
    return this.initialize();
  }
  private async load() {
    try { await this.run(async signal => {
      let options: IdentityOptions;
      try {
        if (!this.providerFailure && !this.initialContinuation && this.state.mode === 'signin') {
          // Capture the original browser-bound results once, even if options
          // fail. Retrying capabilities must not discover a successor tab's flow.
          const mode = this.state.mode;
          const [read, registration, transaction] = await Promise.allSettled([
            this.account.client.options(signal), this.account.client.registration(undefined, signal), this.account.client.transaction(undefined, signal),
          ]);
          if (signal.aborted || this.disposed) return;
          this.initialContinuation = Object.freeze({mode,
            ...(registration.status === 'fulfilled' && registration.value ? {registration: Object.freeze({...registration.value})} : {}),
            ...(transaction.status === 'fulfilled' && transaction.value ? {transaction: Object.freeze({...transaction.value})} : {})});
          if (read.status === 'rejected') throw read.reason;
          options = read.value;
        } else {
          this.initialContinuation ??= Object.freeze({mode: this.state.mode});
          options = await this.account.client.options(signal);
        }
      } catch (error) {
        if (!signal.aborted) this.publish({optionsFailed: true});
        throw error;
      }
      if (signal.aborted || this.disposed) return;
      this.publish({options, optionsFailed: false});
      if (this.providerFailure) {
        this.publish(this.providerFailure === 'unknown' ? {error: new CodedError('request_failed', 'Sign-in could not be completed.')} : {mode: 'provider-stopped', provider: this.providerFailure});
        return;
      }
      const original = this.initialContinuation;
      if (this.initialContinuationRetired || original?.mode !== 'signin' || this.state.mode !== original.mode) return;
      const tx = original.transaction;
      if (tx?.mode === 'login') {
        if (tx.status === 'failed') {
          // The current browser-bound transaction is terminal. It proves no
          // email/account collision and cannot be completed or adopted.
          this.transaction = undefined; this.providerRefresh = undefined; this.ids.clear('provider');
          this.publish({mode: 'provider-stopped', provider: tx.provider}); return;
        }
        if (tx.needsUsername && tx.contactVerified) {
          this.transaction = tx; this.publish({mode: 'provider', provider: tx.provider, suggestedName: tx.suggestedName, verifiedEmail: tx.verifiedEmail}); return;
        }
        // Services also projects a verified, already linked login as
        // onboarding. That state needs completion, but no username form.
        if (!tx.needsUsername && (tx.status === 'verified' || tx.status === 'onboarding' && tx.contactVerified)) {
          await this.authenticate(inner => this.account.client.complete(tx.transactionId, {}, AbortSignal.any([inner, signal]))); return;
        }
      }
      if (original.registration && ['contact_pending', 'verified'].includes(original.registration.status))
        this.publish({mode: 'verify', registration: original.registration});
    }); } finally {this.publish({initializing: false});}
  }
  private async run(work: (signal: AbortSignal) => Promise<void>) {
    if (this.disposed || this.operation || this.state.retryAt > Date.now()) return;
    const controller = new AbortController(); this.operation = controller; this.publish({busy: true, error: undefined});
    try { await work(controller.signal); }
    catch (error) {
      if (!controller.signal.aborted) {
        if (error instanceof MFARequiredError) { this.challenge = error.challenge; this.publish({mode: 'factor'}); }
        else { const seconds = (error as {retryAfterSeconds?: unknown})?.retryAfterSeconds; this.publish({error, retryAt: typeof seconds === 'number' && Number.isFinite(seconds) && seconds > 0 ? Date.now() + seconds * 1000 : 0}); }
      }
    } finally { if (this.operation === controller) this.operation = undefined; if (!controller.signal.aborted) this.publish({busy: false}); }
  }
  private async authenticate(work: Parameters<ServicesAccount['service']['authenticateIfSignedOut']>[0], gesture = false) {
    const operation = this.operation;
    await this.account.service.authenticateIfSignedOut(work, {}, () => !this.disposed && this.operation === operation && !operation?.signal.aborted);
    this.ids.clear();
    if (!this.disposed) this.signedIn(gesture);
  }
  signIn(username: string, password: string, code: string) {
    return this.run(async signal => {
      if (this.challenge) { const challenge = this.challenge; await this.authenticate(inner => this.account.client.mfa(challenge, code.trim(), AbortSignal.any([inner, signal])), true); }
      else { const id = await this.ids.for('login', {username, password}); await this.authenticate(inner => this.account.client.login(username.trim(), password, id, AbortSignal.any([inner, signal])), true); }
    });
  }
  provider(provider: IdentityProvider) {
    // Navigate while the click still owns the gesture. Hosted creates the binding,
    // PKCE and nonce, then redirects; no fetch, CSRF metadata or saved session can
    // prevent this first-party navigation from reaching the provider.
    return this.run(async signal => {
      if (!signal.aborted) {
        this.publish({providerNavigating: true});
        try {this.redirect(this.account.api.origin + '/auth/sso/' + provider + '/start');}
        catch (error) {this.publish({providerNavigating: false});throw error;}
      }
    });
  }
  resumeProviderNavigation() {if (this.state.providerNavigating) this.publish({providerNavigating: false});}
  cancelProvider() {
    return this.run(async signal => {
      await this.account.client.request(routePath('identity_cancel'), 'POST', undefined, signal);
      if (!signal.aborted) {
        this.transaction = undefined; this.providerRefresh = undefined; this.ids.clear('provider');
        this.publish({mode: 'signin', provider: undefined, suggestedName: undefined, verifiedEmail: undefined});
      }
    });
  }
  create(input: {username: string; displayName: string; contactAddress: string; password: string}) {
    return this.run(async signal => {
      const value = await this.account.client.register({...input, username: input.username.trim().toLowerCase(), displayName: input.displayName.trim() || input.username.trim(), contactAddress: input.contactAddress.trim(), requestId: await this.ids.for('registration', input)}, signal);
      if (!signal.aborted) { this.ids.clear('registration'); this.publish({mode: 'verify', registration: Object.freeze({...value}), resends: 0}); }
    });
  }
  verify(code: string, username?: string, displayName?: string) {
    return this.run(async signal => {
      const registration = this.state.registration;
      if (this.state.mode !== 'verify' || !registration) return;
      // Freeze the issued scalar authority and request before coordinator admission can yield.
      const original = Object.freeze({...registration});
      const input = Object.freeze({code: code.trim(), ...(username !== undefined ? {username: username.trim().toLowerCase(), displayName: displayName?.trim()} : {})});
      const current = () => !this.disposed && !signal.aborted && this.state.mode === 'verify' && this.state.registration === registration;
      if (username !== undefined && (!original.contactVerified || !(Date.parse(original.expiresAt) > Date.now()))) return;
      try {
        await this.account.service.authenticateIfSignedOut(inner => {
          if (!current()) throw new CodedError('account_changed', 'The registration changed.');
          return this.account.client.verifyRegistration(original.registrationId, input, AbortSignal.any([signal, inner]));
        }, {}, current);
      } catch (error) {
        if (error instanceof ApiError && error.status === 409 && error.code === 'username_unavailable' && current()) {
          const fresh = await this.account.client.registration(original.registrationId, signal);
          if (!current()) return;
          if (!fresh || fresh.registrationId !== original.registrationId || fresh.revision !== original.revision || fresh.expiresAt !== original.expiresAt || fresh.maskedAddress !== original.maskedAddress || fresh.status === 'completed' || !fresh.contactVerified || !(Date.parse(fresh.expiresAt) > Date.now()))
            throw new CodedError('account_changed', 'The registration changed.');
          this.publish({registration: Object.freeze({...fresh})});
        }
        throw error;
      }
      if (current()) { this.ids.clear(); this.signedIn(true); }
    });
  }
  resend() {
    return this.run(async signal => {
      const registration = this.state.registration; if (!registration || registration.contactVerified || this.state.resends >= 5) return;
      const value = await this.account.client.resendRegistration(registration.registrationId, await this.ids.for('resend', {id: registration.registrationId, count: this.state.resends}), signal);
      if (!signal.aborted) this.publish({registration: value, resends: this.state.resends + 1});
    });
  }
  /** One field names the account: its username or the email on it, never both. */
  recover(usernameOrEmail: string) {
    const identifier = usernameOrEmail.trim().toLowerCase();
    if (this.state.mode !== 'forgot' || !identifier) return Promise.resolve();
    return this.run(async signal => {
      const requestId = await this.ids.for('recover', {identifier});
      if (signal.aborted) return;
      await this.account.client.request(routePath('account_recovery_request'), 'POST', routeBody('account_recovery_request', {identifier, requestId}), signal);
      if (!signal.aborted) { this.ids.clear('recover'); this.publish({mode: 'sent'}); }
    });
  }
  /** Only a confirmed failed username insert rotates onboarding proof. Read
   * that same transaction; never adopt the browser cookie's successor or retry
   * an uncertain completion with a newly substituted request/proof. */
  private async refreshProvider(original: IdentityTransaction, signal: AbortSignal) {
    const current = () => !this.disposed && !signal.aborted && this.transaction === original;
    if (!current()) throw new CodedError('cancelled', 'The provider sign-in changed.');
    const fresh = await this.account.client.transaction(undefined, signal);
    if (!current()) throw new CodedError('cancelled', 'The provider sign-in changed.');
    if (!fresh || fresh.transactionId !== original.transactionId || fresh.provider !== original.provider || fresh.mode !== 'login' || !fresh.needsUsername || !fresh.contactVerified
      || !['onboarding', 'verified'].includes(fresh.status) || fresh.verifiedEmail !== original.verifiedEmail || fresh.expiresAt !== original.expiresAt || Date.parse(fresh.expiresAt) <= Date.now() || !fresh.onboardingToken) {
      this.transaction = undefined; this.providerRefresh = undefined;
      this.publish({mode: 'signin', provider: undefined, verifiedEmail: undefined, suggestedName: undefined});
      throw new CodedError('account_changed', 'The provider sign-in changed.');
    }
    this.transaction = fresh; this.providerRefresh = undefined;
  }
  finishProvider(username: string, displayName: string) {
    return this.run(async signal => {
      if (this.providerRefresh) await this.refreshProvider(this.providerRefresh, signal);
      const transaction = this.transaction; if (!transaction?.needsUsername || !transaction.contactVerified) return;
      let accepted: Awaited<ReturnType<ServicesAccount['service']['authenticateIfSignedOut']>>;
      try {
        accepted = await this.account.service.authenticateIfSignedOut(inner => this.account.client.complete(transaction.transactionId,
          {username: username.trim(), displayName: displayName.trim(), ...(transaction.onboardingToken ? {onboardingToken: transaction.onboardingToken} : {})}, AbortSignal.any([inner, signal])), {}, () => !this.disposed && !signal.aborted && this.transaction === transaction);
      } catch (error) {
        if (error instanceof ApiError && error.status === 409 && error.code === 'username_unavailable' && !signal.aborted && this.transaction === transaction) {
          this.providerRefresh = transaction; await this.refreshProvider(transaction, signal);
        }
        throw error;
      }
      if (!signal.aborted) { this.readyIdentity = accountIdentity(accepted); this.transaction = undefined; this.providerRefresh = undefined; this.publish({mode: 'ready'}); }
    });
  }
  continue() {
    return this.run(async () => {
      if (this.state.mode !== 'ready' || !this.readyIdentity) return;
      await currentAccountWork(this.account, async () => undefined, this.readyIdentity);
      if (!this.disposed) this.signedIn(true);
    });
  }
}
