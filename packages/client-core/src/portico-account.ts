/** A Portico Account at Portico Services: one named operation for each route the account pages use.
 * Every route, its body and its refusals are the Services contract's
 * (`hosted-services/hosted-next/API.md` in the private repository); nothing here invents one.
 * The caller keeps the sign-in (`HeldSignIn`). An app keeps both tokens in its own storage. The
 * account pages at web.getportico.tv keep only the access token, in memory: Services holds the
 * refresh token in an HttpOnly cookie that no script on the page can read (`cookie: true`). */
import {servicesRoute, type ServicesRoute} from '../../contracts/src/services/routes.ts';

export class ServicesError extends Error {
  constructor(readonly status: number, readonly code: string, readonly fields: Readonly<Record<string, string>> = {}) {
    super(code);
  }
}

/** `refreshToken` is absent in a browser, where Services keeps it in a cookie. */
export type Tokens = Readonly<{accessToken: string; refreshToken?: string; expiresAt: string}>;
export type Device = Readonly<{name: string; kind: string}>;
/** What a change that could take the account away asks for: the password typed now, and the two-step code when it is on. */
export type Proof = Readonly<{currentPassword?: string; twoStepCode?: string}>;
export type SignInAnswer = Readonly<{tokens?: Tokens; twoStep?: {challengeId: string}}>;
export type Provider = 'google' | 'apple';
export type Account = Readonly<{
  id: string; email: string; newEmailWaiting?: string; username: string; displayName: string;
  hasPassword: boolean; providers: readonly string[]; twoStep: boolean; ownsServer: boolean;
}>;
export type SignedInDevice = Readonly<{id: string; name: string; kind: string; createdAt: string; lastUsedAt: string; current: boolean}>;
export type ApprovalPurpose = 'setup' | 'switch' | 'change_owner';
export type Approval = Readonly<{id: string; serverId: string; serverName: string; purpose: ApprovalPurpose; state?: 'waiting' | 'approved' | 'declined'}>;
export type ApprovalDecision = Readonly<{state: 'approved' | 'declined'; returnUrl: string; approval?: string}>;
export type ListedServer = Readonly<{serverId: string; name: string; role: string; owner: boolean}>;
export type ProviderCompletion = Readonly<{tokens?: Tokens; twoStep?: {challengeId: string}; connected?: Provider; created?: boolean}>;
export type TVCode = Readonly<{id: string; device: Device}>;

/** The sign-in this device holds. `forget(held)` forgets it only if it is still the one held.
 * `kept` says a sign-in is held where this page cannot read it (the browser's cookie), so an
 * access token can be had by renewing even when none is in memory. */
export type HeldSignIn = Readonly<{current: () => Tokens | undefined; keep: (tokens: Tokens) => void; forget: (held: Tokens | undefined) => void; kept?: () => boolean}>;

type Options = Readonly<{token?: string; create?: boolean; signal?: AbortSignal}>;
type Transport = Readonly<{origin: string; cookie: boolean; fetcher?: typeof fetch}>;

/** A signed document's contents, for showing. The apps and servers that act on one check its signature. */
export function envelopePayload<T>(envelope: {payload: string}): T {
  const base64 = envelope.payload.replace(/-/g, '+').replace(/_/g, '/');
  const bytes = Uint8Array.from(atob(base64 + '='.repeat((4 - base64.length % 4) % 4)), c => c.charCodeAt(0));
  return JSON.parse(new TextDecoder().decode(bytes)) as T;
}

/** One call. A POST that makes something (`create`) carries a fresh Idempotency-Key, so a repeat
 * of the same press is answered once; a new press is a new request. */
async function send<T>({origin, cookie, fetcher = fetch}: Transport, route: ServicesRoute, values: Readonly<Record<string, string>>, body: unknown, options: Options): Promise<T> {
  const {method, path} = servicesRoute(route, values);
  const headers: Record<string, string> = {Accept: 'application/json'};
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (options.token) headers.Authorization = `Bearer ${options.token}`;
  if (options.create) headers['Idempotency-Key'] = crypto.randomUUID();
  // The header asks Services to set, read or clear its refresh-token cookie
  // (`__Host-portico_refresh`), so the browser must be allowed to send and keep cookies. Only the
  // routes that do that are sent this way (`cookieRoutes`).
  if (cookie) headers['Portico-Refresh'] = 'cookie';
  let response: Response;
  try {
    response = await fetcher(origin + path, {method, headers, body: body === undefined ? undefined : JSON.stringify(body), signal: options.signal, credentials: cookie ? 'same-origin' : 'omit', cache: 'no-store'}); // query-read: kept (Portico Services, the person's Portico Account: not a media server document)
  } catch (error) {
    if (options.signal?.aborted) throw error;
    throw new ServicesError(0, 'unreachable');
  }
  if (response.status === 204) return undefined as T;
  const text = await response.text();
  let value: unknown;
  try { value = text ? JSON.parse(text) : undefined; } catch { value = undefined; }
  if (response.ok) return value as T;
  const problem = (value as {error?: {code?: unknown; fields?: unknown}} | undefined)?.error;
  const code = typeof problem?.code === 'string' ? problem.code : response.status >= 500 ? 'internal' : 'bad_request';
  const fields = problem?.fields && typeof problem.fields === 'object' ? problem.fields as Record<string, string> : {};
  throw new ServicesError(response.status, code, fields);
}

/** The routes where Portico Services sets, reads or clears its sign-in cookie for a browser. */
const cookieRoutes: ReadonlySet<ServicesRoute> = new Set<ServicesRoute>(['signIn', 'twoStepSignIn', 'confirm', 'providerComplete', 'refresh', 'signOut', 'deleteAccount', 'passwordResetConfirm']);

/** The account's operations. `origin` is Portico Services' own (empty on its own pages);
 * `cookie` is for those pages, where Services keeps the refresh token in its cookie. */
export function porticoAccount(held: HeldSignIn, origin = '', {cookie = false, fetcher}: Readonly<{cookie?: boolean; fetcher?: typeof fetch}> = {}) {
  type Values = Readonly<Record<string, string>>;
  const anyone = <T>(route: ServicesRoute, values: Values = {}, body?: unknown, options: Options = {}) => send<T>({origin, cookie: cookie && cookieRoutes.has(route), fetcher}, route, values, body, options);
  let renewing: Promise<Tokens | undefined> | undefined;
  /** Renews the access token once it has run out. A renewal sent twice (two tabs) gets the same
   * answer from Services, so the tabs never sign each other out. */
  const renew = (): Promise<Tokens | undefined> => {
    const before = held.current();
    if (!before?.refreshToken && !held.kept?.()) return Promise.resolve(undefined);
    const body = before?.refreshToken ? {refreshToken: before.refreshToken} : undefined;
    // A renewal whose answer was lost may have rotated the token already; Services answers a
    // repeat within a minute the same way, so it is sent again at once rather than later.
    renewing ??= anyone<Tokens>('refresh', {}, body)
      .catch(error => { if (error instanceof ServicesError && error.status === 0) return anyone<Tokens>('refresh', {}, body); throw error; })
      .then(tokens => { held.keep(tokens); return tokens; })
      .catch(error => { if (error instanceof ServicesError && error.code === 'signed_out') held.forget(before); throw error; })
      .finally(() => { renewing = undefined; });
    return renewing;
  };
  /** A call as the signed-in person: a token that has run out is renewed first; a sign-in that has
   * ended (signed out elsewhere, password changed) is forgotten here too. */
  const person = async <T>(route: ServicesRoute, values: Values = {}, body?: unknown, options: Omit<Options, 'token'> = {}): Promise<T> => {
    let tokens = held.current();
    if (tokens ? Date.parse(tokens.expiresAt) - Date.now() < 60_000 : held.kept?.()) tokens = await renew();
    if (!tokens) throw new ServicesError(401, 'signed_out');
    try {
      return await anyone<T>(route, values, body, {...options, token: tokens.accessToken});
    } catch (error) {
      if (!(error instanceof ServicesError) || error.code !== 'signed_out') throw error;
      // Another tab may have renewed already: a different held sign-in is the newer one.
      const now = held.current();
      // compareSessionAuthority is for media server sign-ins; a Portico Services sign-in has no session family or generation, so the sign-in held is compared as itself.
      const renewed = now && now !== tokens ? now : await renew().catch(() => undefined);
      if (!renewed) { held.forget(tokens); throw error; }
      return anyone<T>(route, values, body, {...options, token: renewed.accessToken});
    }
  };
  const tokensKept = <T extends {tokens?: Tokens}>(answer: T): T => { if (answer.tokens) held.keep(answer.tokens); return answer; };

  return {
    capabilities: () => anyone<{capabilities?: string[]}>('system').then(value => value.capabilities ?? []),
    signUp: (details: {email: string; username: string; password: string; displayName: string}) =>
      anyone<{accountId: string; email: string}>('signUp', {}, details, {create: true}),
    confirmSignUp: (accountId: string, code: string, device: Device) =>
      anyone<{tokens: Tokens}>('confirm', {}, {accountId, code, device}).then(tokensKept),
    resendConfirmation: (accountId: string) => anyone<void>('resendConfirmation', {}, {accountId}),
    signIn: (login: string, password: string, device: Device) =>
      anyone<SignInAnswer>('signIn', {}, {login, password, device}, {create: true}).then(tokensKept),
    finishTwoStep: (challengeId: string, code: string) =>
      anyone<{tokens: Tokens}>('twoStepSignIn', {}, {challengeId, code}, {create: true}).then(tokensKept),
    askPasswordReset: (email: string) => anyone<void>('passwordReset', {}, {email}),
    finishPasswordReset: (email: string, code: string, password: string) =>
      anyone<{providers?: string[]}>('passwordResetConfirm', {}, {email, code, password}),
    /** Starts Sign in with Google or Apple; with `proof`, connects it to the signed-in account instead. */
    startProvider: (provider: Provider, secretHash: string, proof?: Proof) => proof
      ? person<{id: string; authorizeUrl: string}>('providerStart', {}, {provider, secretHash, ...proof})
      : anyone<{id: string; authorizeUrl: string}>('providerStart', {}, {provider, secretHash}),
    completeProvider: (flow: string, code: string, secret: string, device: Device, chosen: {username?: string; displayName?: string} = {}) =>
      anyone<ProviderCompletion>('providerComplete', {}, {id: flow, code, secret, device, ...chosen}, {create: true}).then(tokensKept),
    /** For an app's own sign-in store, which renews and ends the sign-ins it keeps itself. */
    renewWith: (refreshToken: string, signal?: AbortSignal) => anyone<Tokens>('refresh', {}, {refreshToken}, {signal}),
    endWith: (accessToken: string, signal?: AbortSignal) => anyone<void>('signOut', {}, undefined, {token: accessToken, signal}),
    signOut: async () => {
      // Whatever is held now: the call may have renewed the access token to send it.
      try { await person<void>('signOut'); } finally { held.forget(held.current()); }
    },
    account: (signal?: AbortSignal) => person<Account>('account', {}, undefined, {signal}),
    changePassword: (proof: Proof, newPassword: string) => person<void>('password', {}, {...proof, newPassword}),
    askEmailChange: (proof: Proof, newEmail: string) => person<{email: string}>('email', {}, {...proof, newEmail}),
    confirmEmailChange: (email: string, code: string) => anyone<{email: string}>('confirm', {}, {email, code}),
    deleteAccount: async (proof: Proof) => {
      await person<void>('deleteAccount', {}, proof);
      held.forget(held.current());
    },
    devices: (signal?: AbortSignal) => person<{items: SignedInDevice[]}>('sessions', {}, undefined, {signal}).then(answer => answer.items),
    signOutDevice: (device: string, signal?: AbortSignal) => person<void>('endSession', {id: device}, undefined, {signal}),
    signOutOthers: (signal?: AbortSignal) => person<void>('endOtherSessions', {}, undefined, {signal}),
    disconnectProvider: (provider: Provider, proof: Proof) => person<void>('disconnectProvider', {provider}, proof),
    twoStep: () => person<{on: boolean; recoveryCodesLeft: number}>('twoStep'),
    beginTwoStep: (proof: Proof) => person<{secret: string; qrAddress: string}>('twoStepBegin', {}, proof),
    confirmTwoStep: (code: string) => person<{recoveryCodes: string[]}>('twoStepConfirm', {}, {code}),
    turnOffTwoStep: (proof: Proof) => person<void>('twoStepOff', {}, proof),
    newRecoveryCodes: (proof: Proof) => person<{recoveryCodes: string[]}>('recoveryCodes', {}, proof),
    approval: (approval: string, signal?: AbortSignal) => person<Approval>('approval', {id: approval}, undefined, {signal}),
    lookupApproval: (code: string) => person<Approval>('approvalLookup', {}, {code}),
    decideApproval: (approval: string, approve: boolean) => person<ApprovalDecision>('approvalDecision', {id: approval}, {approve}),
    /** The account's signed server list as Services sends it, for an app to check (`portico-server-list.ts`). */
    serverList: (signal?: AbortSignal) => person<{list: unknown; listToken: string; listVersion: number}>('servers', {}, undefined, {signal}),
    /** The account's servers, read from its signed list for showing; an app that opens one checks the signature. */
    servers: () => person<{list: {payload: string}}>('servers').then(answer => envelopePayload<{servers: ListedServer[]}>(answer.list).servers),
    /** A sign-in note for one server, answering the challenge it gave (`portico.identity.v1`, five
     * minutes); `note` is the server's news when it is behind, for the app to carry there. */
    serverIdentity: (server: string, challenge: string, signal?: AbortSignal) =>
      person<{identity: unknown; note?: unknown}>('serverIdentity', {id: server}, {challenge}, {signal}),
    leaveServer: (server: string) => person<void>('leaveServer', {id: server}),
    removeServer: (server: string) => person<void>('removeServer', {id: server}),
    lookupTV: (code: string) => person<TVCode>('tvLookup', {}, {code}),
    decideTV: (code: string, approve: boolean) => person<{state: string}>('tvDecision', {id: code}, {approve}),
  };
}
export type PorticoAccount = ReturnType<typeof porticoAccount>;
