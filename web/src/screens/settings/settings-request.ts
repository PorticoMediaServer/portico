import type {ProfileAPI} from '@core/profile-management.ts';

type AssertCurrent = () => void;
type SettingsSignal = (input?: AbortSignal | null) => AbortSignal;

/** A request's own options, as `HttpLocalApi.request` takes them (`manageGate: false`: an optional
 * read whose refusal never asks for the management PIN). */
export type SettingsRequestOptions = Readonly<{manageGate?: false}>;
type SettingsRequestApi = {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal, options?: SettingsRequestOptions): Promise<T>};

/** Ordinary Settings reads/writes publish only through their original build. A request's own
 * options reach the original API unchanged (#595: the away-limit read's `manageGate: false`). */
export function guardedSettingsRequest(original: SettingsRequestApi, assert: AssertCurrent, signal: SettingsSignal): SettingsRequestApi['request'] {
  return async <T,>(path: string, method?: string, body?: unknown, input?: AbortSignal, options?: SettingsRequestOptions): Promise<T> => {
    assert();
    const value = options === undefined ? await original.request<T>(path, method, body, signal(input)) : await original.request<T>(path, method, body, signal(input), options);
    assert();
    return value;
  };
}

/** The local ownership POST may deliver its known acknowledgement after the Settings
 * view retires. Only that POST is sent this way, and its method is typed so:
 * client-core's `localOwnerChangeOnly` refuses anything else before it reaches here.
 * The source then spends its captured context; its separate publication guard still
 * refuses stale UI/reconciliation. */
export function localOwnerTransferTransport(original: ProfileAPI, assert: AssertCurrent, signal: SettingsSignal): ProfileAPI {
  return Object.freeze({request: async <T,>(path: string, method?: 'POST', body?: unknown, input?: AbortSignal): Promise<T> => {
    assert();
    const captured = signal(input);
    if (captured.aborted) throw new DOMException('Settings build retired', 'AbortError');
    // Preserve the original API's family/route transport and error identity. A
    // rejection is uncertain, never an acknowledgement or a fresh retry.
    return original.request<T>(path, method, body, captured);
  }});
}
