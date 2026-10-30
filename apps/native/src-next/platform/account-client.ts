import {ApiError} from '../../../../packages/client-core/src';
import {HostedAccountClient, type AccountTransport} from '../../../../packages/client-core/src/hosted-security';
import {nativeNetworkFetch} from './server-connections';

const cancelled = () => Object.assign(new Error('native_account_cancelled'), {name: 'AbortError'});

/** Account passwords, factors and provider completions use the redirect-refusing native
 * transport. This factory performs no request; only an explicit account action contacts it. */
export function createNativeAccountClient(origin: string, binding: string, isCurrent: () => boolean = () => true, ownsResult: () => boolean = () => false): HostedAccountClient {
  // Already-issued session results stay with the canonical credential coordinator,
  // so cancellation can retire its fresh family instead of losing cleanup custody.
  const transport: AccountTransport = async (path, method, body, signal, token, completion) => {
    const controller = new AbortController(), abort = () => controller.abort();
    signal?.addEventListener('abort', abort, {once: true});
    if (signal?.aborted) abort();
    const timeout = setTimeout(abort, 15000);
    try {
      if (controller.signal.aborted || !isCurrent()) throw cancelled();
      const response = await nativeNetworkFetch(origin + path, {
        method, signal: controller.signal,
        headers: {'Content-Type': 'application/json', 'X-Portico-Continuation': binding,
          ...(token ? {Authorization: `Bearer ${token}`} : {}),
          ...(completion ? {'X-Portico-Completion': completion} : {})},
        ...(body === undefined ? {} : {body: JSON.stringify(body)}),
      });
      if (controller.signal.aborted || !isCurrent() && !ownsResult()) throw cancelled();
      const raw = await response.text();
      if (controller.signal.aborted || !isCurrent() && !ownsResult()) throw cancelled();
      if (raw.length > 262144) throw Object.assign(new Error('native_account_response_too_large'), {code: 'invalid_response'});
      let value: unknown;
      try {value = raw ? JSON.parse(raw) : undefined;}
      catch {throw Object.assign(new Error('native_account_response_unreadable'), {code: 'invalid_response'});}
      if (!response.ok) {
        const object = (v: unknown): v is Record<string, unknown> => v !== null && typeof v === 'object' && !Array.isArray(v);
        const error = object(value) && object(value.error) ? value.error : {};
        throw new ApiError(response.status, typeof error.code === 'string' ? error.code : 'request_failed',
          typeof error.message === 'string' ? error.message : 'native_account_request_failed', error.retryable === true || response.status >= 500);
      }
      return value;
    } catch (error) {if (!isCurrent() && !ownsResult()) throw cancelled(); throw error;} finally {clearTimeout(timeout); signal?.removeEventListener('abort', abort);}
  };
  return new HostedAccountClient(origin, 'native', transport, binding);
}
