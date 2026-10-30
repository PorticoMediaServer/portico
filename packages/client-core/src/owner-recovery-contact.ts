import {routePath} from '../../contracts/src/route-paths.ts';
import {RouteError, probeRoute, routeOrigin, routeJSON, serverPin, type RouteCrypto} from './route-identity.ts';
import {parseServerSignIn} from './sign-in.ts';

/** Previously authenticated directory facts captured before ordinary open begins. A link,
 * typed address or newly fetched replacement record cannot supply this recovery authority. */
export type OwnerRecoveryServer = Readonly<{baseUrl:string;serverId:string;publicKey:string}>;
/** Purpose-bound navigation only: never route freshness, membership or a saved connection. */
export type OwnerRecoveryContact = Readonly<{purpose:'owner-recovery';origin:string;serverId:string;signInURL:string}>;

/** Explicit emergency contact with the original selected server. The caller supplies the
 * normal platform TLS/egress transport, and checks the SAME lifetime before opening the URL.
 * No Hosted source, credential store, authenticated API or route memory participates. */
export async function contactKnownServerForOwnerRecovery(
 captured:OwnerRecoveryServer,
 options:Readonly<{fetcher?:typeof fetch;crypto?:RouteCrypto;signal:AbortSignal;isCurrent:()=>boolean}>,
):Promise<OwnerRecoveryContact> {
 // Copy the selected scalar facts before any asynchronous work; never reread a mutable row.
 const {baseUrl,serverId,publicKey}=captured;
 const controller=new AbortController();
 const cancelled=()=>new RouteError('cancelled','Server recovery contact was cancelled.');
 const check=()=>{if(controller.signal.aborted||options.signal.aborted||!options.isCurrent())throw cancelled();};
 const abort=()=>controller.abort();
 options.signal.addEventListener('abort',abort,{once:true});
 let rejectAbort:()=>void=()=>{};
 const stopped=new Promise<never>((_,reject)=>{rejectAbort=()=>reject(cancelled());controller.signal.addEventListener('abort',rejectAbort,{once:true});});
 const timer=setTimeout(abort,8000);
 try {
  check();
  const origin=routeOrigin(baseUrl),transport=options.fetcher??globalThis.fetch;
  // Only these two public endpoints may be contacted, always without ambient credentials.
  const anonymous:typeof fetch=async(input,init)=>{
   check();
   const url=String(input);
   if(!((url===origin+routePath('prove_network_identity')&&init?.method==='POST')||(url===origin+routePath('get_system')&&init?.method==='GET'))||init?.credentials!=='omit'||init.redirect!=='error')throw new RouteError('invalid_address','Invalid recovery contact request.');
   const response=await transport(input,{...init,cache:'no-store'});
   try {
    check();
    // Also refuse a transport that reports following a redirect despite redirect:error.
    if(response.redirected||response.url&&response.url!==String(input))throw new RouteError('identity_mismatch','The recovery address redirected.');
    return response;
   }catch(error){void response.body?.cancel().catch(()=>{});throw error;}
  };
  const contact=async():Promise<OwnerRecoveryContact>=>{
   const pin=await serverPin(serverId,publicKey,undefined,options.crypto);check();
   await probeRoute(origin,pin,{fetcher:anonymous,crypto:options.crypto,signal:controller.signal});check();
   const response=await anonymous(origin+routePath('get_system'),{method:'GET',redirect:'error',credentials:'omit',referrerPolicy:'no-referrer',headers:{Accept:'application/json'},signal:controller.signal});check();
   const system=parseServerSignIn(await routeJSON(response,controller.signal,8192));check();
   if(system.id!==pin.serverId)throw new RouteError('identity_mismatch','The server identity changed during recovery contact.');
   return Object.freeze({purpose:'owner-recovery',origin,serverId:pin.serverId,signInURL:origin+'/sign-in'});
  };
  return await Promise.race([contact(),stopped]);
 } finally {
  clearTimeout(timer);options.signal.removeEventListener('abort',abort);
  controller.signal.removeEventListener('abort',rejectAbort);controller.abort();
 }
}
