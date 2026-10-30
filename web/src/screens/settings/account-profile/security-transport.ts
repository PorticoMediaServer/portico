import type {ProfileAPI} from '@core/profile-management.ts';
import {routePath} from '../../../../../packages/contracts/src/routes.ts';

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
