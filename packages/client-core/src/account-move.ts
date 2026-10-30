import {throwIfAborted} from './abort-lifetime.ts';
import {routeBody} from '../../contracts/src/routes.ts';
import {routePath, encodedRoutePath, type RouteRequest} from '../../contracts/src/route-paths.ts';
import {CodedError,unreadableServerResponse} from './server-messages.ts';
import {currentUTCTimestamp} from './local-session.ts';
import {parseDirectSignIn,type DirectSignIn,type ProfileAPI,type ManagementCapabilities} from './profile-management.ts';
import type {LocalSession} from './index.ts';
import type {SignInKind} from './app-sessions.ts';
import {identityCode,routeOrigin} from './route-identity.ts';

export type MoveDirection='to_portico'|'to_local';
export type AccountMove=Readonly<{id:string;from:SignInKind;to:SignInKind;startOver:boolean;stage:'preview'|'moving_to_portico'|'moving_to_local'|'complete'|'cancelled';revision:number;people:number;profiles:number;invitations:number;waiting:number;retainedProfiles?:number;removedProfiles?:number;serverName:string;ownAddressRequired:boolean;devices?:number}>;
export type MovePasswordTicket=Readonly<{ticket:string;expiresAt:string}>;
export type MoveCodePreview=Readonly<{username:string;signIn:SignInKind;expiresAt:string}>;
export type AccountMoveCode=Readonly<{accountId:string;username:string;code:string;expiresAt:string;serverId?:string}>;
export type AccountMoveConfirmation=Readonly<{kind:'local';signIn:DirectSignIn}|{kind:'portico';move:AccountMove}>;
export type MoveCredential=Readonly<{ticket:string;code?:never}|{code:string;ticket?:never}>;
export type MoveCollisionDetails=Readonly<{existingUsername:string;serverName:string}>;
export type SignInSettings=Readonly<{signIn:SignInKind;owner:Readonly<{accountId:string;username:string;displayName:string;email?:string}>;move:AccountMove|null;webPath:'/settings?scope=server&section=signin'}>;
export type MoveClientOptions=Readonly<{
 serverId:string;isCurrent:()=>boolean;
 /** Platform uses its explicit transport and the captured issuing authority. Never another family. */
 retireLateGrant:(session:LocalSession)=>Promise<void>;
 timeoutMs?:number;
}>;
const obj=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const text=(v:unknown,max=256):v is string=>typeof v==='string'&&v.length>0&&v.length<=max&&!/[\p{Cc}\p{Cf}]/u.test(v);
const count=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
const secret=(v:unknown):v is string=>typeof v==='string'&&/^[A-Za-z0-9_-]{43}$/.test(v);
function invalid():never{throw new CodedError('invalid_account_move',unreadableServerResponse);}
function ended():never{throw new CodedError('cancelled','This account move request ended.');}
function expiry(raw:unknown,maximum:number,now:number){if(!currentUTCTimestamp(raw)||Date.parse(raw)<=now||Date.parse(raw)>now+maximum+60000)invalid();return raw;}
export function parseMoveCollisionDetails(raw:unknown):MoveCollisionDetails{
 if(!obj(raw)||!text(raw.existingUsername,64)||!text(raw.serverName,120)||Object.keys(raw).some(key=>!['existingUsername','serverName'].includes(key)))invalid();
 return Object.freeze({existingUsername:raw.existingUsername,serverName:raw.serverName});
}
export function parseSignInSettings(raw:unknown):SignInSettings{
 if(!obj(raw)||!['portico','local'].includes(String(raw.signIn))||!obj(raw.owner)||!Object.hasOwn(raw,'move')||raw.webPath!=='/settings?scope=server&section=signin')invalid();
 const owner=raw.owner;if(!text(owner.accountId,128)||!text(owner.username,64)||typeof owner.displayName!=='string'||owner.displayName.length>120||/[\p{Cc}\p{Cf}]/u.test(owner.displayName)||owner.email!==undefined&&!text(owner.email,254))invalid();
 const move=raw.move===null?null:parseAccountMove(raw.move);
 if(move&&(move.stage==='moving_to_local'&&raw.signIn!=='local'||move.stage==='moving_to_portico'&&raw.signIn!=='portico'))invalid();
 return Object.freeze({signIn:raw.signIn as SignInKind,owner:Object.freeze({accountId:owner.accountId,username:owner.username,displayName:owner.displayName,...(owner.email===undefined?{}:{email:owner.email as string})}),move,webPath:raw.webPath});
}
export function parseAccountMove(raw:unknown):AccountMove{
 if(!obj(raw)||!text(raw.id,128)||!['portico','local'].includes(String(raw.from))||!['portico','local'].includes(String(raw.to))||raw.from===raw.to||typeof raw.startOver!=='boolean'||!['preview','moving_to_portico','moving_to_local','complete','cancelled'].includes(String(raw.stage))||!count(raw.revision)||raw.revision<1||!text(raw.serverName,120)||typeof raw.ownAddressRequired!=='boolean')invalid();
 for(const key of ['people','profiles','invitations','waiting'])if(!count(raw[key]))invalid();
 if(Number(raw.waiting)>Number(raw.people)||raw.ownAddressRequired!==(raw.to==='local')||raw.stage==='moving_to_local'&&raw.to!=='local'||raw.stage==='moving_to_portico'&&raw.to!=='portico')invalid();
 const hasRetained=Object.hasOwn(raw,'retainedProfiles'),hasRemoved=Object.hasOwn(raw,'removedProfiles');
 if(hasRetained!==hasRemoved||hasRetained&&(!raw.startOver||!count(raw.retainedProfiles)||!count(raw.removedProfiles)||!Number.isSafeInteger(Number(raw.retainedProfiles)+Number(raw.removedProfiles))||Number(raw.retainedProfiles)+Number(raw.removedProfiles)!==raw.profiles))invalid();
 // Start over deliberately has no device count; a guided move must report its real count.
 if(raw.startOver?Object.hasOwn(raw,'devices'):!count(raw.devices))invalid();
 return Object.freeze({id:raw.id,from:raw.from as SignInKind,to:raw.to as SignInKind,startOver:raw.startOver,stage:raw.stage as AccountMove['stage'],revision:raw.revision,people:raw.people as number,profiles:raw.profiles as number,invitations:raw.invitations as number,waiting:raw.waiting as number,serverName:raw.serverName,ownAddressRequired:raw.ownAddressRequired,...(raw.startOver?{}:{devices:raw.devices as number}),...(hasRetained?{retainedProfiles:raw.retainedProfiles as number,removedProfiles:raw.removedProfiles as number}:{})});
}
export function parseMovePasswordTicket(raw:unknown,now=Date.now()):MovePasswordTicket{
 if(!obj(raw)||!secret(raw.ticket)||Object.keys(raw).some(key=>!['ticket','expiresAt'].includes(key)))invalid();
 return Object.freeze({ticket:raw.ticket,expiresAt:expiry(raw.expiresAt,10*60000,now)});
}
export function parseMoveCodePreview(raw:unknown,now=Date.now()):MoveCodePreview{
 if(!obj(raw)||!text(raw.username,64)||!['portico','local'].includes(String(raw.signIn))||Object.keys(raw).some(key=>!['username','signIn','expiresAt'].includes(key)))invalid();
 return Object.freeze({username:raw.username,signIn:raw.signIn as SignInKind,expiresAt:expiry(raw.expiresAt,7*86400000,now)});
}
export function parseAccountMoveCodes(raw:unknown,now=Date.now()):readonly AccountMoveCode[]{
 if(!obj(raw)||!Array.isArray(raw.items)||raw.items.length>10000)invalid();
 const seen=new Set<string>();return Object.freeze(raw.items.map(row=>{
  if(!obj(row)||!text(row.accountId,128)||seen.has(row.accountId)||!text(row.username,64)||!secret(row.code))invalid();seen.add(row.accountId);
  if(row.serverId!==undefined){if(typeof row.serverId!=='string')invalid();try{identityCode(row.serverId);}catch{invalid();}}
  return Object.freeze({accountId:row.accountId,username:row.username,code:row.code,expiresAt:expiry(row.expiresAt,7*86400000,now),...(row.serverId===undefined?{}:{serverId:row.serverId as string})});
 }));
}
export type AccountMoveWaiting=Readonly<{accountId:string;username:string;expiresAt?:string}>;
/** The waiting people for the active move: exact ids the codes route accepts,
 * bounded and unique, with the usernames the owner recognises. No code, hash or
 * password material is part of this read. */
export function parseAccountMoveWaiting(raw:unknown):Readonly<{items:readonly AccountMoveWaiting[];truncated:boolean}>{
 if(!obj(raw)||!Array.isArray(raw.items)||raw.items.length>2000||typeof raw.truncated!=='boolean')invalid();
 const seen=new Set<string>();
 const now=Date.now();
 const items=Object.freeze(raw.items.map(row=>{
  if(!obj(row)||!text(row.accountId,128)||seen.has(row.accountId)||!text(row.username,64)||!row.username)invalid();
  seen.add(row.accountId);
  // The existing code's own expiry, bounded to today's seven-day lifetime; an
  // absent field means no working code, never a substituted date.
  const expiresAt=row.expiresAt===undefined?undefined:expiry(row.expiresAt,7*86400000,now);
  return Object.freeze({accountId:row.accountId,username:row.username,...(expiresAt===undefined?{}:{expiresAt})});
 }));
 return Object.freeze({items,truncated:raw.truncated});
}
export type MoveCodeLink=Readonly<{code:string;id?:string}>;
/** Codes live in memory only. Scrub even an invalid fragment before any await/analytics.
 * The optional public id is an expectation for the existing first-contact proof,
 * never authority to redeem a code or a replacement for that proof. */
export function consumeAccountMoveFragment(location:Pick<Location,'pathname'|'search'|'hash'>,history:Pick<History,'replaceState'>):MoveCodeLink|undefined{
 const raw=location.hash,search=location.search,path=location.pathname;history.replaceState(null,'',path);if(search)invalid();if(!raw)return;
 const params=new URLSearchParams(raw.slice(1)),code=params.get('code'),id=params.get('id');
 if(!secret(code)||params.getAll('code').length!==1||params.getAll('id').length>1||[...params.keys()].some(key=>!['code','id'].includes(key)))invalid();
 if(id!==null){try{identityCode(id);}catch{invalid();}}
 return Object.freeze({code,...(id===null?{}:{id})});
}
/** Legacy code-only callers must refuse an identity-bearing link rather than
 * silently drop its expected server. Use consumeAccountMoveFragment for those. */
export function consumeMoveCodeFragment(location:Pick<Location,'pathname'|'search'|'hash'>,history:Pick<History,'replaceState'>):string|undefined{
 const link=consumeAccountMoveFragment(location,history);if(link?.id!==undefined)invalid();return link?.code;
}
export function accountMoveLink(serverOrigin:string,code:string,serverId?:string):string{
 if(!secret(code))invalid();if(serverId!==undefined){try{identityCode(serverId);}catch{invalid();}}
 return new URL('/sign-in#code='+code+(serverId===undefined?'':'&id='+serverId),routeOrigin(serverOrigin)).href;
}

/** Cancellation never replays a mutation. A late session is retired through the platform's
 * captured authority; secrets never enter snapshots, URLs, storage or error messages here. */
class MoveTransport {
 private epoch=0;private controller?:AbortController;private disposed=false;private mutating=false;
 protected readonly api:ProfileAPI;protected readonly options:MoveClientOptions;
 constructor(api:ProfileAPI,options:MoveClientOptions){this.api=api;this.options=options;if(!text(options.serverId,128)||!Number.isFinite(options.timeoutMs??15000)||(options.timeoutMs??15000)<=0||(options.timeoutMs??15000)>120000)invalid();}
 cancel(){++this.epoch;this.controller?.abort();this.controller=undefined;}
 dispose(){this.cancel();this.disposed=true;}
 protected grant(raw:unknown):DirectSignIn{const result=parseDirectSignIn(raw);if(result.kind==='signed-in'&&result.snapshot.serverId!==this.options.serverId)invalid();return result;}
 protected async call<T>(path:string,method:string,body:unknown,parse:(raw:unknown)=>T,signal?:AbortSignal,grant?:(value:T)=>DirectSignIn|undefined):Promise<T>{
  if(this.disposed||!this.options.isCurrent())ended();if(this.mutating)throw new CodedError('move_request_in_progress','Wait for this account move request to finish.');
  this.cancel();const epoch=this.epoch,controller=this.controller=new AbortController(),mutation=method!=='GET';if(mutation)this.mutating=true;
  const current=()=>!this.disposed&&epoch===this.epoch&&!controller.signal.aborted&&this.options.isCurrent();
  const abort=()=>controller.abort();if(signal?.aborted)abort();else signal?.addEventListener('abort',abort,{once:true});
  let timer:ReturnType<typeof setTimeout>|undefined;let off=()=>{};
  try{
   throwIfAborted(controller.signal);
   const pending=this.api.request<unknown>(path,method,body,controller.signal).then(async raw=>{
    const value=parse(raw);if(!current()){const late=grant?.(value);if(late?.kind==='signed-in')await this.options.retireLateGrant(late.session);ended();}return value;
   });
   return await Promise.race([pending,new Promise<never>((_,reject)=>{
    const fail=()=>reject(new CodedError('cancelled','This account move request ended.'));off=()=>controller.signal.removeEventListener('abort',fail);controller.signal.addEventListener('abort',fail,{once:true});
    timer=setTimeout(()=>{reject(new CodedError('move_timeout','The account move could not be confirmed. Check its current state before trying again.'));controller.abort();},this.options.timeoutMs??15000);
   })]);
  }finally{clearTimeout(timer);off();signal?.removeEventListener('abort',abort);if(this.controller===controller)this.controller=undefined;if(mutation)this.mutating=false;}
 }
}
export class MemberAccountMoveClient extends MoveTransport {
 password(username:string,password:string,signal?:AbortSignal){if(!text(username,64)||!text(password,72))invalid();return this.call(routePath('post_auth_move_password'),'POST',routeBody('post_auth_move_password',{username,password}),parseMovePasswordTicket,signal);}
 previewCode(code:string,signal?:AbortSignal){if(!secret(code))invalid();return this.call(routePath('post_auth_move_code'),'POST',routeBody('post_auth_move_code',{code}),parseMoveCodePreview,signal);}
 redeemLocal(code:string,username:string,password:string,signal?:AbortSignal){if(!secret(code)||!text(username,64)||!text(password,72))invalid();return this.call(routePath('post_auth_move_local'),'POST',routeBody('post_auth_move_local',{code,username,password}),raw=>this.grant(raw),signal,value=>value);}
 redeemPortico(proof:MoveCredential,porticoIdentity:RouteRequest<'post_auth_move_portico'>['porticoIdentity'],signal?:AbortSignal){
  if(!obj(proof)||(secret(proof.code)?proof.ticket!==undefined:!secret(proof.ticket)||proof.code!==undefined))invalid();
  return this.call(routePath('post_auth_move_portico'),'POST',routeBody('post_auth_move_portico',{...proof,porticoIdentity}),raw=>this.grant(raw),signal,value=>value);
 }
}
/** Phones/TV use this surface only. The producer still rechecks live owner + main profile. */
export class ReadOnlySignInClient extends MoveTransport {
 private readonly capabilities:()=>ManagementCapabilities;
 constructor(api:ProfileAPI,options:Pick<MoveClientOptions,'serverId'|'isCurrent'|'timeoutMs'>&{capabilities:()=>ManagementCapabilities}){
  super(api,{...options,retireLateGrant:async()=>{}});this.capabilities=options.capabilities;
 }
 read(signal?:AbortSignal):Promise<SignInSettings>{
  if(this.capabilities().manageSignIn!==true)throw new CodedError('forbidden','Sign-in settings require the owner’s main profile.');
  return this.call(routePath('get_sign_in_settings'),'GET',undefined,raw=>{if(this.capabilities().manageSignIn!==true)ended();return parseSignInSettings(raw);},signal);
 }
}
export class AccountMoveClient extends MoveTransport {
 current(signal?:AbortSignal):Promise<AccountMove|null>{return this.call(routePath('get_sign_in_move'),'GET',undefined,raw=>{if(!obj(raw)||!Object.hasOwn(raw,'move'))invalid();return raw.move===null?null:parseAccountMove(raw.move);},signal);}
 preview(to:SignInKind,startOver:boolean,signal?:AbortSignal){if(!['portico','local'].includes(to)||typeof startOver!=='boolean')invalid();return this.call(routePath('post_sign_in_move_preview'),'POST',routeBody('post_sign_in_move_preview',{to,startOver}),parseAccountMove,signal);}
 confirm(input:RouteRequest<'post_sign_in_move_confirm'>,signal?:AbortSignal):Promise<AccountMoveConfirmation>{
  if(!text(input.moveId,128)||!count(input.expectedRevision)||input.expectedRevision<1)invalid();
  return this.call(routePath('post_sign_in_move_confirm'),'POST',routeBody('post_sign_in_move_confirm',input),raw=>{
   if(!obj(raw)||Object.hasOwn(raw,'move')===Object.hasOwn(raw,'signIn'))invalid();
   return raw.signIn!==undefined?Object.freeze({kind:'local' as const,signIn:this.grant(raw.signIn)}):Object.freeze({kind:'portico' as const,move:parseAccountMove(raw.move)});
  },signal,value=>value.kind==='local'?value.signIn:undefined);
 }
 cancelPreview(move:AccountMove,signal?:AbortSignal){if(move.stage!=='preview')invalid();return this.call(encodedRoutePath('post_sign_in_move_cancel',{id:encodeURIComponent(move.id)}),'POST',routeBody('post_sign_in_move_cancel',{expectedRevision:move.revision}),()=>undefined,signal);}
 finish(move:AccountMove,signal?:AbortSignal){if(!['moving_to_local','moving_to_portico'].includes(move.stage))invalid();return this.call(encodedRoutePath('post_sign_in_move_finish',{id:encodeURIComponent(move.id)}),'POST',routeBody('post_sign_in_move_finish',{expectedRevision:move.revision}),()=>undefined,signal);}
 /** One receipt for exactly these targets. A receipt must return one code per
  * requested account and nothing else; a row naming another server is refused. */
 codes(accountIds:readonly string[],signal?:AbortSignal,move?:Pick<AccountMove,'id'|'revision'>){if(accountIds.length>2000||accountIds.some(id=>!text(id,128))||new Set(accountIds).size!==accountIds.length)invalid();
  if(move&&(!text(move.id,128)||!count(move.revision)||move.revision<1))invalid();
  const targets=new Set(accountIds),body={accountIds:[...accountIds],...(move?{moveId:move.id,expectedRevision:move.revision}:{})};
  return this.call(routePath('post_sign_in_move_codes'),'POST',routeBody('post_sign_in_move_codes',body),raw=>{
   const rows=parseAccountMoveCodes(raw);
   if(rows.length!==targets.size)invalid();
   for(const row of rows){if(!targets.has(row.accountId))invalid();if((move||row.serverId!==undefined)&&row.serverId!==this.options.serverId)invalid();}
   return rows;
  },signal);}
 /** The exact waiting people, read through the official generated route. */
 waiting(signal?:AbortSignal){return this.call(routePath('get_sign_in_move_waiting'),'GET',undefined,parseAccountMoveWaiting,signal);}

}
