import type {ShellAssemblyContext} from '../../../shell/bootstrap';
import {guardedSettingsRequest} from '../settings-request';

/**
 * Fence Settings transport entry points explicitly while preserving their original method owner
 * and authentication behavior. Binding a method alone does not fence internal transport calls.
 */
export function createFencedSettingsApi(
  originalApi: ShellAssemblyContext['api'],
  assert: () => void,
  signal: (input?: AbortSignal | null) => AbortSignal,
): ShellAssemblyContext['api'] {
  const request = guardedSettingsRequest(originalApi, assert, signal);
  const requestRaw: typeof originalApi.requestRaw = async (path, method, options) => {
    assert();
    const value = await originalApi.requestRaw(path, method, {...options, signal: signal(options?.signal)});
    assert();
    return value;
  };
  const requestMetadataUpload: typeof originalApi.requestMetadataUpload = async <T,>(path: string, body: Blob, input?: AbortSignal): Promise<T> => {
    assert();
    const value = await originalApi.requestMetadataUpload<T>(path, body, signal(input));
    assert();
    return value;
  };
  const requestBounded: typeof originalApi.requestBounded = async <T,>(path: string, maxBytes: number, input: AbortSignal): Promise<T> => {
    assert();
    const value = await originalApi.requestBounded<T>(path, maxBytes, signal(input));
    assert();
    return value;
  };
  const requestLyrics: typeof originalApi.requestLyrics = async <T,>(path: string, method?: string, body?: unknown, input?: AbortSignal): Promise<T> => {
    assert();
    const value = await originalApi.requestLyrics<T>(path, method, body, signal(input));
    assert();
    return value;
  };
  const readReportLog: typeof originalApi.readReportLog = async (id, input) => {
    assert();
    const value = await originalApi.readReportLog(id, signal(input));
    assert();
    return value;
  };
  const routeFetch: typeof originalApi.routeFetch = async (input, options) => {
    assert();
    const value = await originalApi.routeFetch(input, {...options, signal: signal(options?.signal)});
    assert();
    return value;
  };
  const methods = new Map<PropertyKey, Readonly<{original: unknown; bound: unknown}>>();
  return new Proxy(originalApi, {
    get(target, property) {
      if (property === 'request') return request;
      if (property === 'requestRaw') return requestRaw;
      if (property === 'requestMetadataUpload') return requestMetadataUpload;
      if (property === 'requestBounded') return requestBounded;
      if (property === 'requestLyrics') return requestLyrics;
      if (property === 'routeFetch') return routeFetch;
      if (property === 'readReportLog') return readReportLog;
      const value = Reflect.get(target, property, target);
      if (typeof value !== 'function') return value;
      const held = methods.get(property);
      if (held && held.original === value) return held.bound;
      const bound = value.bind(target);
      methods.set(property, {original: value, bound});
      return bound;
    },
  });
}
