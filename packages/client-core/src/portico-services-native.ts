/** The native apps' Portico Account at the new Portico Services (`hosted-services/hosted-next/API.md`
 * in the private repository), in the shapes the apps' sign-in screens and their sign-in store
 * already use. The apps keep both tokens in their own store (`CredentialSessionService`); there is
 * no cookie. The calls themselves are `porticoAccount`'s, in token mode. */
import {porticoAccount, ServicesError, type Device, type HeldSignIn, type Tokens} from './portico-account.ts';
import {ApiError, type HostedSession} from './http.ts';
import type {CredentialApi} from './credentials.ts';
import {MFARequiredError, type IdentityOptions, type MFAChallenge, type Registration} from './hosted-security.ts';
import {serverNow} from './server-clock.ts';

/** A sign-in lasts 90 days from last use; every renewal starts the 90 days again. */
export const PORTICO_SIGN_IN_LIFETIME_MS = 90 * 86400000;

/** What a person typed that Services refused, shown as "check what you typed" rather than by its status. */
const typedWrong = new Set(['credentials', 'two_step_wrong', 'code_wrong']);
/** A refusal from Portico Services as the apps' `ApiError`, with the codes their screens already know. */
export function porticoApiError(error: unknown): unknown {
  if (!(error instanceof ServicesError)) return error;
  if (error.code === 'unreachable') return new ApiError(0, 'network', 'portico_services_unreachable', true);
  if (error.code === 'username_taken') return new ApiError(409, 'username_unavailable', 'portico_services_refused');
  if (typedWrong.has(error.code)) return new ApiError(422, 'invalid_' + error.code, 'portico_services_refused');
  return new ApiError(error.status, error.code, 'portico_services_refused', error.status >= 500 || error.status === 429);
}
const refused = async <T>(work: Promise<T>): Promise<T> => { try { return await work; } catch (error) { throw porticoApiError(error); } };

/** The apps' session record for a Portico Services sign-in. `familyId` names this sign-in on this
 * device (Services' tokens carry no family); it stays the same across renewals. */
export function hostedSessionFrom(tokens: Tokens, account: HostedSession['account'], familyId: string, now = serverNow()): HostedSession {
  if (!tokens.refreshToken) throw new ApiError(502, 'invalid_response', 'portico_services_no_refresh_token');
  return {accessToken: tokens.accessToken, refreshToken: tokens.refreshToken, expiresAt: tokens.expiresAt,
    refreshExpiresAt: new Date(now + PORTICO_SIGN_IN_LIFETIME_MS).toISOString(), familyId, account};
}

/** An access token held for exactly one call. The apps' store decides when a sign-in is renewed
 * and ends it itself, so the call never renews it (its expiry is not this call's to judge). */
export const heldFor = (accessToken: string): HeldSignIn => {
  const tokens = {accessToken, expiresAt: '9999-12-31T00:00:00Z'};
  return {current: () => tokens, keep: () => {}, forget: () => {}};
};

/** `CredentialSessionService`'s account service for the apps: renewal and sign-out at Portico Services. */
export class PorticoServicesCredentialApi implements CredentialApi<string> {
  private readonly account: ReturnType<typeof porticoAccount>;
  constructor(readonly origin: string, fetcher?: typeof fetch) {
    this.account = porticoAccount(heldFor(''), origin, {fetcher});
  }
  authorityFromSession(session: HostedSession): string {
    if (!session.refreshToken) throw new Error('Native refresh credentials missing.');
    return session.refreshToken;
  }
  async refresh(refreshToken: string, _requestId: string, signal?: AbortSignal, previous?: HostedSession): Promise<HostedSession> {
    if (!previous) throw new ApiError(401, 'session_expired', 'portico_services_renewal_without_session');
    return hostedSessionFrom(await refused(this.account.renewWith(refreshToken, signal)), previous.account, previous.familyId);
  }
  /** Services ends a sign-in by its access token, so a kept refresh token is renewed once to end it.
   * A sign-in Services has already ended is ended. */
  async revoke(refreshToken: string, signal?: AbortSignal): Promise<void> {
    let tokens: Tokens;
    try { tokens = await this.account.renewWith(refreshToken, signal); } catch (error) {
      if (error instanceof ServicesError && error.code === 'signed_out') return;
      throw porticoApiError(error);
    }
    await refused(this.account.endWith(tokens.accessToken, signal));
  }
}

/** The sign-in steps the apps' screens take, at Portico Services. Google and Apple wait for their
 * own follow-up, so the options offer none. */
export class PorticoServicesAccountClient {
  private readonly anyone: ReturnType<typeof porticoAccount>;
  constructor(private readonly origin: string, private readonly device: Device, private readonly fetcher?: typeof fetch, private readonly newFamily: () => string = () => crypto.randomUUID()) {
    this.anyone = porticoAccount(heldFor(''), origin, {fetcher});
  }
  async options(): Promise<IdentityOptions> {
    const offered = await refused(this.anyone.capabilities());
    const email = offered.includes('email');
    return {password: true, registration: email, recovery: email, accountSecurity: false, verifiedContactRequired: true, providers: []};
  }
  /** Tokens to the apps' session record, with the account they belong to. */
  private async session(tokens: Tokens): Promise<HostedSession> {
    const account = await refused(porticoAccount(heldFor(tokens.accessToken), this.origin, {fetcher: this.fetcher}).account());
    return hostedSessionFrom(tokens, {id: account.id, username: account.username, displayName: account.displayName}, this.newFamily());
  }
  /** A password sign-in; an account with two-step on throws the challenge the screen asks the code for. */
  async login(login: string, password: string): Promise<HostedSession> {
    const answer = await refused(this.anyone.signIn(login, password, this.device));
    if (answer.twoStep) throw new MFARequiredError({mfaRequired: true, challengeId: answer.twoStep.challengeId, challengeToken: answer.twoStep.challengeId,
      expiresAt: new Date(serverNow() + 5 * 60000).toISOString()});
    if (!answer.tokens) throw new ApiError(502, 'invalid_response', 'portico_services_sign_in_answer');
    return this.session(answer.tokens);
  }
  async mfa(challenge: MFAChallenge, code: string): Promise<HostedSession> {
    return this.session((await refused(this.anyone.finishTwoStep(challenge.challengeId, code.trim()))).tokens);
  }
  /** Sign-up: the code is emailed to `contactAddress`, which is the account's email address. */
  async register(input: {username: string; displayName: string; password: string; contactAddress: string}): Promise<Registration> {
    const answer = await refused(this.anyone.signUp({email: input.contactAddress, username: input.username, password: input.password, displayName: input.displayName}));
    return pending(answer.accountId, answer.email);
  }
  async resendRegistration(original: Registration): Promise<Registration> {
    await refused(this.anyone.resendConfirmation(original.registrationId));
    return pending(original.registrationId, original.maskedAddress);
  }
  /** The emailed code confirms the account this device's own sign-up was answered with, and signs it in. */
  async verifyRegistration(registration: Registration, code: string): Promise<HostedSession> {
    return this.session((await refused(this.anyone.confirmSignUp(registration.registrationId, emailedCode(code), this.device))).tokens);
  }
  /** Account help: a reset code goes to the account's confirmed email address; Services answers the
   * same whether or not it has one. */
  async recover(email: string): Promise<void> { await refused(this.anyone.askPasswordReset(email)); }
}
/** A sign-up waiting for its code, in the shape the apps' screens show (an hour, as the email says). */
const pending = (accountId: string, address: string): Registration => Object.freeze({kind: 'registration', registrationId: accountId, status: 'contact_pending',
  revision: '1', maskedAddress: address, expiresAt: new Date(serverNow() + 3600000).toISOString(), contactVerified: false});

/** An emailed code as Services writes it (`ABCD-EFGH`), however it was typed. */
export function emailedCode(value: string): string {
  const plain = value.toUpperCase().replace(/[^A-Z0-9]/g, '').slice(0, 8);
  return plain.length > 4 ? `${plain.slice(0, 4)}-${plain.slice(4)}` : plain;
}
