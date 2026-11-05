import type {ProfileAPI} from '../profile-management.ts';
import {routePath} from '../../../contracts/src/routes.ts';

/** Original account-security commands preserve their known terminal ACK even if
 * the resulting password/factor change retires the Settings view. Reads use the
 * ordinary guarded transport; this cannot become a general unfenced API. */
export function profileSecurityTransport(original:ProfileAPI,assert:()=>void,signal:(input?:AbortSignal|null)=>AbortSignal):ProfileAPI{
 const routes=new Map<string,string>([
  [routePath('post_direct_password'),'POST'],[routePath('post_direct_two_factor_enrol'),'POST'],
  [routePath('post_direct_two_factor_verify'),'POST'],[routePath('delete_direct_two_factor'),'DELETE'],
  [routePath('post_direct_two_factor_recovery_codes'),'POST'],
 ]);
 return Object.freeze({request:async<T,>(path:string,method?:string,body?:unknown,input?:AbortSignal):Promise<T>=>{
  if(routes.get(path)!==method)throw new Error('Not an account security command.');
  assert();const captured=signal(input);captured.throwIfAborted();
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
