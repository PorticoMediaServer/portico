import {linkNativeAbortSignals} from '../native-abort-signals.ts';
import {nativeAccountRead, nativeAccountWork, accountExpectation} from './native-account-work.ts';
import type {CredentialAccessExpectation} from '../credentials.ts';
/** Authenticated Hosted transports use the canonical credential API's own origin
 * and the exact session admitted by its coordinator. Apps provide platform IO. */
import {HttpLocalApi, transportJSON, type HostedSession} from '../index.ts';
import type {CredentialSessionService} from '../credentials.ts';
import type {BrowserAuthority} from './browser-account-api.ts';
import {AccountNotificationsClient} from '../account-notifications.ts';
import {BrowserAccountApi} from './browser-account-api.ts';
import {HostedAccountClient} from '../hosted-security.ts';

export function createHostedAccountApi<Authority>(owner: CredentialSessionService<Authority>, session: Pick<HostedSession, 'accessToken'>, fetcher: typeof fetch): HttpLocalApi {
  return new HttpLocalApi(owner.authorityOrigin(), session.accessToken, fetcher);
}
export function createHostedNotificationClient<Authority>(owner: CredentialSessionService<Authority>, session: Pick<HostedSession, 'accessToken'>, fetcher: typeof fetch, fallbackSignal?: AbortSignal): AccountNotificationsClient {
  return new AccountNotificationsClient((path, method, body, signal) => createHostedAccountApi(owner, session, fetcher).request(path, method, body, signal ?? fallbackSignal));
}
/** Freeze an already-built command before credential synchronization can yield. */
export function captureHostedCommandBody(body: unknown): unknown {
  return body === undefined ? undefined : JSON.parse(transportJSON(body));
}
export function createBrowserHostedClient(api: BrowserAccountApi): HostedAccountClient {
  return new HostedAccountClient(api.origin, 'browser', (path, method, body, signal, token) => api.send(path, method, body, signal, token));
}
/** Inert account API for the existing protected original-account security return. */
export function createOriginalBrowserSecurityClient(origin: string): HostedAccountClient {
  return createBrowserHostedClient(new BrowserAccountApi(origin));
}

export type BrowserHostedAccount = {api: BrowserAccountApi; service: CredentialSessionService<BrowserAuthority>; client: HostedAccountClient};

/** The original no-replay sign-in command policy, including input capture before
 * renewal yields and listener cleanup on every outcome. */
export function createNativeHostedCommandTransport(owner: CredentialSessionService, fetcher: typeof fetch,
  expected: CredentialAccessExpectation, signal: AbortSignal, isCurrent: () => boolean,
  hooks: {assertCurrent?: () => void; unavailable?: (error: unknown) => unknown} = {}) {
  return {request: async <T,>(path: string, method?: string, body?: unknown, callerSignal?: AbortSignal): Promise<T> => {
    hooks.assertCurrent?.();
    const originalBody = captureHostedCommandBody(body), linked = linkNativeAbortSignals([signal, callerSignal]);
    try {
      const result = await nativeAccountWork(owner, session => createHostedAccountApi(owner, session, fetcher).request<T>(path, method, originalBody, linked.signal),
        {expected, signal: linked.signal, isCurrent});
      hooks.assertCurrent?.(); return result;
    } catch (error) {
      if (hooks.unavailable) {hooks.assertCurrent?.(); throw hooks.unavailable(error) ?? error;}
      throw error;
    } finally {linked.dispose();}
  }};
}
/** Workspace operations retain their existing GET-only renewal permission;
 * all commands reconcile refusal without replaying their original callback. */
export function createNativeHostedWorkspaceTransport(owner: CredentialSessionService, fetcher: typeof fetch,
  opening: {expected: CredentialAccessExpectation; signal: AbortSignal; isCurrent: () => boolean}) {
  return {request: <T,>(path: string, method: string, body: unknown, signal: AbortSignal): Promise<T> =>
    (method === 'GET' ? nativeAccountRead : nativeAccountWork)(owner,
      session => createHostedAccountApi(owner, session, fetcher).request<T>(path, method, body, signal), opening)};
}

export function createNativeAccountWorkspaceRequest(owner: CredentialSessionService, fetcher: typeof fetch, options: {assertCurrent: () => void; current: () => boolean; sameEpoch: () => boolean; signal: AbortSignal; requests: Set<AbortController>; changed: () => Error}, commands = false) {
  return async <T,>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> => {
    options.assertCurrent(); if (!commands && (method !== 'GET' || body !== undefined) || method === 'GET' && body !== undefined) throw options.changed();
    const request = new AbortController(), abort = () => request.abort(); options.requests.add(request);
    options.signal.addEventListener('abort', abort, {once: true}); signal?.addEventListener('abort', abort, {once: true});
    if (signal?.aborted) abort(); const timer = setTimeout(abort, 15000);
    try {
      const original = owner.getSnapshot().session;
      if (!original) throw options.changed();
      const result = await createNativeHostedWorkspaceTransport(owner, fetcher,
        {expected: accountExpectation(original), signal: request.signal, isCurrent: options.current})
        .request<T>(path, method, body, request.signal);
      options.assertCurrent();
      if (!options.sameEpoch() || request.signal.aborted) throw options.changed(); return result;
    } finally {options.requests.delete(request); clearTimeout(timer); options.signal.removeEventListener('abort', abort); signal?.removeEventListener('abort', abort);}
  };
}

export function requestHostedAccountExport(owner: CredentialSessionService, fetcher: typeof fetch,
  opening: {expected: CredentialAccessExpectation; signal: AbortSignal; isCurrent: () => boolean}, operationId: string, signal: AbortSignal) {
  return nativeAccountWork(owner, session => createHostedNotificationClient(owner, session, fetcher).requestExport(operationId, signal), opening);
}
