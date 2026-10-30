import {parseAccountReturn, securityPurposeNeedsProof, type AccountScope, type HostedAccountClient, type IdentityProvider, type RecentProof, type SecurityIntent} from './hosted-security.ts';
import {CodedError} from './server-messages.ts';

/**
 * Root §70 / A80 in an app: an account that signs in only with Google or Apple
 * proves itself for one credential change (password, email, two-step) by signing
 * in with that provider again. The app keeps the original Settings opening in
 * memory while the system browser (or Apple's own sheet) is up; only the opaque
 * transaction ID and one-use completion code come back through the return link.
 * The proof is issued for this account, session and intent only, never a session.
 */

/** What the platform supplies: request IDs, the system browser and native Apple. */
export type ProviderProofPorts = Readonly<{
  newRequestId: () => Promise<string>;
  /** Opens the provider page and resolves with the app's return link. */
  openBrowser: (url: string, transactionId: string) => Promise<string>;
  /** Present only where native Sign in with Apple is offered for this app. */
  signInApple?: (nonce: string | undefined) => Promise<{idToken: string}>;
}>;

/** A provider re-confirmation that has come back and awaits its completion. */
export type ProviderReconfirmation = Readonly<{
  transactionId: string; provider: IdentityProvider; intent: SecurityIntent; completionCode?: string;
}>;

const failed = () => new CodedError('provider_reconfirm_failed', 'The sign-in service did not confirm this account. Try again.');

/** Leave for the provider and come back, for exactly this intent on this session. */
export async function reconfirmWithProvider(client: HostedAccountClient, input: {provider: IdentityProvider; intent: SecurityIntent; nativeApple: boolean; accountOrigin: string; expectedIdentity?: Readonly<{id: string; revision: string}>},
  token: string, ports: ProviderProofPorts, signal?: AbortSignal): Promise<ProviderReconfirmation> {
  const {provider, nativeApple, accountOrigin} = input, intent = Object.freeze({...input.intent});
  if (client.mode !== 'native' || !securityPurposeNeedsProof(intent.purpose) || nativeApple && (provider !== 'apple' || !ports.signInApple)) throw failed();
  const expectedIdentity = input.expectedIdentity ? Object.freeze({...input.expectedIdentity}) : undefined;
  const requestId = await ports.newRequestId();
  const start = await client.start(provider, {mode: 'reauthenticate', requestId, intent, nativeApple, ...(expectedIdentity ? {expectedIdentity} : {}), ...(!nativeApple ? {completion: 'code' as const} : {})}, signal, token);
  if (start.mode !== 'reauthenticate') throw failed();
  let completionCode: string | undefined;
  if (nativeApple) {
    const assertion = await ports.signInApple!(start.nativeNonce);
    signal?.throwIfAborted();
    await client.nativeApple(start.transactionId, assertion.idToken, '', signal);
  } else {
    if (!start.authorizationUrl) throw failed();
    const returned = parseAccountReturn(await ports.openBrowser(start.authorizationUrl, start.transactionId), start.transactionId, accountOrigin);
    signal?.throwIfAborted();
    if (!returned.completionCode) throw failed();
    completionCode = returned.completionCode;
  }
  const transaction = await client.transaction(start.transactionId, signal, completionCode);
  if (!transaction || transaction.mode !== 'reauthenticate' || transaction.provider !== provider || transaction.status !== 'verified') throw failed();
  return Object.freeze({transactionId: start.transactionId, provider, intent, ...(completionCode ? {completionCode} : {})});
}

/** Finish the re-confirmation (with the second factor when it is on) into the one-use proof. */
export async function providerRecentProof(client: HostedAccountClient, pending: ProviderReconfirmation, factor: string, scope: AccountScope,
  token: string, signal?: AbortSignal): Promise<RecentProof> {
  const out = await client.completeSecurity(pending.transactionId, factor, scope, token, signal, pending.completionCode);
  if (!out.recentProof) throw failed();
  return out.recentProof;
}
