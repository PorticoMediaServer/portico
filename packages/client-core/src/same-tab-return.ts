import {throwIfAborted} from './abort-lifetime.ts';
import {routePath as hostedRoutePath,encodedRoutePath as hostedEncodedRoutePath} from '../../contracts/src/hosted/routes.ts';
import {routePath} from '../../contracts/src/route-paths.ts';
import {requestPorticoChallenge,type HostedRequestApi} from './portico-servers.ts';
import {parseDirectSignIn} from './profile-management.ts';
import type {HttpLocalApi,LocalSession} from './index.ts';
import {CodedError} from './server-messages.ts';
import {routeBase64,routeBytes,routeOrigin,sameServerPin,serverPin,webRouteCrypto,type RouteCrypto} from './route-identity.ts';
import {validInstallationId} from './installation.ts';
import {verifyRegisteredSignInContext, type RegisteredSignInContext} from './registered-sign-in-context.ts';

export type ReturnPurpose='setup'|'switch'|'change-owner'|'sign-in';
export type ReturnPending=Readonly<{serverId:string;purpose:ReturnPurpose;claimOperationId:string;registrationId:string;state:string;verifier:string;serverChallenge:string;installationId:string;createdAt:number;registrationOrigin?:string;moveTicket?:string;moveCode?:string}>;
export type SameTabReturnStart=Omit<ReturnPending,'verifier'|'createdAt'|'registrationOrigin'|'moveTicket'|'moveCode'>&{codeChallenge:string};
/** Implementations must use protected credential storage. Plain persistent
 * drafts, localStorage and URLs are not storage for the verifier. */
export interface ReturnPendingStore {read():Promise<ReturnPending|undefined>;change(update:(old:ReturnPending|undefined)=>ReturnPending|undefined):Promise<void>}
const token=()=>{const b=new Uint8Array(32);globalThis.crypto.getRandomValues(b);return b64(b);};
const b64=(b:Uint8Array)=>btoa(String.fromCharCode(...b)).replace(/\+/g,'-').replace(/\//g,'_').replace(/=+$/,'');
function validMemberProof(input:Pick<ReturnPending,'purpose'|'claimOperationId'|'moveTicket'|'moveCode'>){
 if(!['setup','switch','change-owner','sign-in'].includes(input.purpose)||input.moveTicket!==undefined&&input.moveCode!==undefined||input.purpose!=='sign-in'&&(input.moveTicket!==undefined||input.moveCode!==undefined)||typeof input.claimOperationId!=='string'||input.claimOperationId!==''&&!/^[A-Za-z0-9_-]{1,128}$/.test(input.claimOperationId))return false;
 return [input.moveTicket,input.moveCode].every(value=>value===undefined||/^[A-Za-z0-9_-]{43}$/.test(value));
}
export async function prepareSameTabReturn(api:HttpLocalApi,store:ReturnPendingStore,input:Pick<ReturnPending,'purpose'|'claimOperationId'|'registrationId'|'installationId'|'moveTicket'|'moveCode'>,signal?:AbortSignal):Promise<SameTabReturnStart>{
 const serverId=api.getRouteConnection()?.pin.serverId;if(!serverId)throw new Error('Verify the server before continuing.');
 if(!validMemberProof(input)||!/^[A-Za-z0-9_-]{32,128}$/.test(input.installationId)||!/^ret_[A-Za-z0-9_-]{43}$/.test(input.registrationId))throw new Error('The registered server return is invalid.');
 const serverChallenge=await requestPorticoChallenge(api,serverId,signal),verifier=token(),state=token();
 const pending:ReturnPending={...input,serverId,serverChallenge,verifier,state,createdAt:Date.now()};
 await store.change(()=>pending);
 const codeChallenge=b64(new Uint8Array(await globalThis.crypto.subtle.digest('SHA-256',new TextEncoder().encode(verifier))));
 // The server's one-use move proof never travels to Hosted, including inside its URL.
 const {verifier:_,createdAt:__,registrationOrigin:___,moveTicket:____,moveCode:_____,...body}=pending;return {...body,codeChallenge};
}
export type RegisteredSignIn=Readonly<{start:SameTabReturnStart;registration:{id:string;origin:string};hostedOrigin:string;expiresAt:string}>;
export type RegisteredSignInWithContext=RegisteredSignIn&Readonly<{context:RegisteredSignInContext}>;
/** Ordinary member sign-in uses the registration resolved by the installed
 * server. Local owner claims have a separate producer and cannot call this. */
export async function prepareRegisteredSignIn(api:Pick<HttpLocalApi,'request'|'getRouteConnection'>,store:ReturnPendingStore,input:{origin:string;hostedOrigin:string;installationId:string;moveTicket?:string;moveCode?:string},options:{signal?:AbortSignal;isCurrent?:()=>boolean;crypto?:RouteCrypto}={}):Promise<RegisteredSignInWithContext>{
 const invalid=()=>new CodedError('invalid_return_preparation','The server could not prepare this sign-in.');
 const pin=api.getRouteConnection()?.pin,crypto=options.crypto??webRouteCrypto;
 if(!pin||!input.origin.startsWith('https://')||routeOrigin(input.origin)!==input.origin||!input.hostedOrigin.startsWith('https://')||routeOrigin(input.hostedOrigin)!==input.hostedOrigin||!validMemberProof({...input,purpose:'sign-in',claimOperationId:''}))throw invalid();
 if(!validInstallationId(input.installationId))throw invalid();
 const current=()=>!options.signal?.aborted&&(options.isCurrent?.()??true),check=()=>{if(!current())throw new CodedError('cancelled','This server sign-in changed.');};
 check();const nonce=routeBase64(crypto.random(32));
 const raw:unknown=await api.request(routePath('post_auth_portico_return_prepare'),'POST',{origin:input.origin,nonce},options.signal);check();
 const obj=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
 if(!obj(raw)||raw.serverId!==pin.serverId||raw.hostedOrigin!==input.hostedOrigin||typeof raw.claimOperationId!=='string'||!/^[A-Za-z0-9_-]{1,128}$/.test(raw.claimOperationId)||!obj(raw.registration)||typeof raw.registration.id!=='string'||!/^ret_[A-Za-z0-9_-]{43}$/.test(raw.registration.id)||raw.registration.origin!==input.origin||typeof raw.challenge!=='string'||!/^[A-Za-z0-9_-]{32,128}$/.test(raw.challenge)||typeof raw.expiresAt!=='string'||!obj(raw.proof))throw invalid();
 const now=Date.now();let proof:Awaited<ReturnType<typeof verifyRegisteredSignInContext>>;
 try{proof=await verifyRegisteredSignInContext(raw.context,now,crypto);}catch{check();throw invalid();}
 if(proof.serverId!==pin.serverId||proof.nonce!==nonce||proof.installationId!==input.installationId||proof.challenge!==raw.challenge||proof.registrationId!==raw.registration.id||proof.registrationOrigin!==input.origin||proof.hostedOrigin!==input.hostedOrigin||proof.claimOperationId!==raw.claimOperationId||proof.expiresAt!==raw.expiresAt)throw invalid();
 const actual=await serverPin(pin.serverId,proof.publicKey,proof.fingerprint,crypto);
 if(!sameServerPin(actual,pin))throw invalid();check();
 const verifier=routeBase64(crypto.random(32)),state=routeBase64(crypto.random(32));
 const pending:ReturnPending={serverId:pin.serverId,purpose:'sign-in',claimOperationId:raw.claimOperationId,registrationId:raw.registration.id,registrationOrigin:input.origin,installationId:input.installationId,serverChallenge:raw.challenge,verifier,state,createdAt:now,...(input.moveTicket?{moveTicket:input.moveTicket}:{}),...(input.moveCode?{moveCode:input.moveCode}:{})};
 const codeChallenge=routeBase64(await crypto.sha256(new TextEncoder().encode(verifier)));check();
 await store.change(()=>{check();return pending;});
 if(!current()){await store.change(old=>old?.state===state?undefined:old);check();}
 const {verifier:_,createdAt:__,registrationOrigin:___,moveTicket:____,moveCode:_____,...body}=pending;
 const context=raw.context as Record<'payload'|'signature',string>;
 return {start:{...body,codeChallenge},registration:{id:pending.registrationId,origin:input.origin},hostedOrigin:input.hostedOrigin,expiresAt:raw.expiresAt,context:Object.freeze({payload:context.payload,signature:context.signature})};
}
// The Hosted account client calls this only after its lookup exposes an actual
// current registered route. No return URL is accepted or inferred here.
export function validateRegisteredReturnLocation(location:string,registration:{id:string;origin:string},state:string,transaction:string):string {
 const origin=registration.origin;
 if(!/^https:\/\/(?:[a-z0-9.-]+|\[[0-9a-f:]+\])(?::[0-9]{1,5})?$/.test(origin))throw new Error('The registered server address is invalid.');
 const expected=new URL(origin),url=new URL(location),values=new URLSearchParams(url.hash.slice(1));
 if(expected.protocol!=='https:'||expected.username||expected.password||expected.search||expected.hash||!location.startsWith(origin+'/auth/portico-return#')||url.pathname!=='/auth/portico-return'||url.search||url.username||url.password||[...values.keys()].sort().join(',')!=='code,state,transaction'||values.get('state')!==state||values.get('transaction')!==transaction||!/^src_[A-Za-z0-9_-]{20,128}$/.test(values.get('code')??''))throw new Error('The return did not match the registered server.');
 return location;
}
export async function completeRegisteredReturn(hosted:HostedRequestApi,start:SameTabReturnStart,registration:{id:string;origin:string},signal?:AbortSignal){
 if(['verifier','registrationOrigin','moveTicket','moveCode'].some(key=>Object.hasOwn(start,key))||!validMemberProof(start))throw new Error('The registered return start is invalid.');
 if(registration.id!==start.registrationId)throw new Error('The return registration changed.');
 const begun=await hosted.request<{transactionId:string}>(hostedRoutePath('server_return_start'),'POST',start,signal);
 if(!/^srt_[A-Za-z0-9_-]{20,128}$/.test(begun.transactionId))throw new Error('The registered return did not start.');
 const out=await hosted.request<{location:string}>(hostedEncodedRoutePath('server_return_complete',{id:encodeURIComponent(begun.transactionId)}),'POST',{},signal);
 return validateRegisteredReturnLocation(out.location,registration,start.state,begun.transactionId);
}
/** Scrub synchronously, before reading protected state or awaiting any network
 * work. Both invalid and failed exchanges leave a clean history entry. */
export async function consumeSameTabReturn(api:Pick<HttpLocalApi,'request'|'getRouteConnection'>,store:ReturnPendingStore,location:Pick<Location,'href'>,history:Pick<History,'replaceState'>,continuation:{requestId?:string;setupToken?:string;expectedRevision?:string;isCurrent?:()=>boolean;retireLateGrant?:(session:LocalSession)=>Promise<void>}={},signal?:AbortSignal){
 const url=new URL(location.href),fragment=url.hash;url.hash='';history.replaceState(null,'',url.pathname+url.search);
 const params=new URLSearchParams(fragment.slice(1));let pending:ReturnPending|undefined;
 try{
  pending=await store.read();await store.change(()=>undefined);
  if(url.pathname!=='/auth/portico-return'||url.search||!pending||Date.now()-pending.createdAt>5*60_000||pending.createdAt>Date.now()+60_000||api.getRouteConnection()?.pin.serverId!==pending.serverId||[...params.keys()].sort().join(',')!=='code,state,transaction'||params.getAll('state').length!==1||params.get('state')!==pending.state||!/^src_[A-Za-z0-9_-]{20,128}$/.test(params.get('code')??'')||!/^srt_[A-Za-z0-9_-]{20,128}$/.test(params.get('transaction')??''))throw new Error('This return is no longer valid. Start again.');
  if(!validMemberProof(pending)||pending.registrationOrigin!==undefined&&pending.registrationOrigin!==url.origin)throw new Error('This return is no longer valid. Start again.');
  if(pending.purpose==='sign-in'&&(!continuation.isCurrent||!continuation.retireLateGrant))throw new Error('Capture the server session before completing sign-in.');
  throwIfAborted(signal);if(continuation.isCurrent&&!continuation.isCurrent())throw new CodedError('cancelled','This server sign-in changed.');
  const out=parseDirectSignIn(await api.request(routePath('post_auth_portico_return_exchange'),'POST',{code:params.get('code'),transactionId:params.get('transaction'),verifier:pending.verifier,registrationId:pending.registrationId,serverChallenge:pending.serverChallenge,installationId:pending.installationId,purpose:pending.purpose,expectedRevision:continuation.expectedRevision??'0',...(pending.moveTicket?{moveTicket:pending.moveTicket}:{}),...(pending.moveCode?{moveCode:pending.moveCode}:{}),...(continuation.requestId?{requestId:continuation.requestId}:{}),...(continuation.setupToken?{setupToken:continuation.setupToken}:{})},signal));
  if(signal?.aborted||continuation.isCurrent&&!continuation.isCurrent()){
   if(out.kind==='signed-in')await continuation.retireLateGrant?.(out.session);throw new CodedError('cancelled','This server sign-in changed.');
  }
  if(out.kind==='signed-in'&&(out.snapshot.serverId!==pending.serverId||out.resetFileOwnerSession))throw new Error('The server return did not match this server.');return out;
 }finally{pending=undefined;await store.change(()=>undefined);}
}
