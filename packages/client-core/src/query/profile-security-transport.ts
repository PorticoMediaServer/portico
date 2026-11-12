import type {ProfileAPI} from '../profile-management.ts';
import {serverPath} from './server-api.ts';
import {throwIfAborted} from '../abort-lifetime.ts';

/** The account-security commands (password, two-step, recovery codes) keep their answer even if what they
 * changed retires the Settings view. Reads use the ordinary guarded transport; this is no general API. */
export function profileSecurityTransport(original:ProfileAPI,assert:()=>void,signal:(input?:AbortSignal|null)=>AbortSignal):ProfileAPI{
 const routes=new Set(['POST /v1/auth/password','POST /v1/me/two-factor','POST /v1/me/two-factor/confirm','DELETE /v1/me/two-factor','POST /v1/me/two-factor/recovery-codes'] as const);
 return Object.freeze({request:async<T,>(path:string,method?:string,body?:unknown,input?:AbortSignal):Promise<T>=>{
  if(![...routes].some(route=>route===`${method} ${path}`&&serverPath(route)===path))throw new Error('Not an account security command.');
  assert();const captured=signal(input);throwIfAborted(captured);
  return original.request<T>(path,method,body,captured);
 }});
}

/** Profile avatar uploads retain their captured opening and the exact core-built
 * raw headers/body; only the lifetime's linked signal is substituted. */
export function createProfileAvatarOpeningTransport(api: Pick<import('../index.ts').HttpLocalApi, 'requestRaw'>, opening: {
  current: () => boolean; signal: AbortSignal; cancelled: () => Error;
  run: <T>(read: (signal: AbortSignal) => Promise<T>, signal: AbortSignal) => Promise<T>;
}) {
  return {requestRaw: async (path: string, method: string, options: Readonly<{headers?: Readonly<Record<string, string>>; body?: unknown; signal?: AbortSignal}> = {}) => {
    if (!opening.current() || opening.signal.aborted) throw opening.cancelled();
    const raw = await opening.run(next => api.requestRaw(path, method, {...options, signal: next}), opening.signal);
    if (!opening.current() || opening.signal.aborted) throw opening.cancelled();
    return raw;
  }};
}
