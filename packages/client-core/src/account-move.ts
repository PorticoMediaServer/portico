import {throwIfAborted} from './abort-lifetime.ts';
import {serverPath} from './query/server-api.ts';
import {routeBody} from '../../contracts/src/routes.ts';
import {routePath, encodedRoutePath, type RouteRequest} from '../../contracts/src/route-paths.ts';
import {CodedError,unreadableServerResponse} from './server-messages.ts';
import {currentUTCTimestamp} from './local-session.ts';
import {parseDirectSignIn,signInDevice,type DirectSignIn,type ProfileAPI,type ManagementCapabilities} from './profile-management.ts';
import type {LocalSession} from './index.ts';
import type {SignInKind} from './app-sessions.ts';
import {identityCode,routeOrigin} from './route-identity.ts';

export type MoveDirection='to_portico'|'to_local';
export type AccountMove=Readonly<{id:string;from:SignInKind;to:SignInKind;startOver:boolean;stage:'preview'|'moving_to_portico'|'moving_to_local'|'complete'|'cancelled';revision:number;people:number;profiles:number;invitations:number;waiting:number;retainedProfiles?:number;removedProfiles?:number;serverName:string;ownAddressRequired:boolean;devices?:number}>;
export type MovePasswordTicket=Readonly<{ticket:string;expiresAt:string}>;
/** Who a move code is for, and which way the server is moving. The rebuilt server keeps the code's expiry to itself. */
export type MoveCodePreview=Readonly<{username:string;signIn:SignInKind;expiresAt?:string}>;
export type AccountMoveCode=Readonly<{accountId:string;username:string;code:string;expiresAt:string;serverId?:string}>;
export type AccountMoveConfirmation=Readonly<{kind:'local';signIn:DirectSignIn}|{kind:'portico';move:AccountMove}>;
export type MoveCredential=Readonly<{ticket:string;code?:never}|{code:string;ticket?:never}>;
export type MoveCollisionDetails=Readonly<{existingUsername:string;serverName:string}>;
/** `move.waiting`: how many accounts still wait, from the server's list; `total`: everyone the move began with, only when the server recorded it. */
export type OwnerSignInSettings=Readonly<{signIn:SignInKind;owner:Readonly<{accountId:string;username:string;email?:string}>;move:Readonly<{to:SignInKind;since:string;waiting:number;total?:number}>|null}>;
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
/** A move code as the rebuilt server makes it: twelve letters and digits, typed with or without its dashes (K7QP-2MXD-9HRT). */
const moveCode=(v:unknown):v is string=>typeof v==='string'&&(/^[A-Za-z0-9]{12}$/.test(v.replace(/[-\s]/g,''))||secret(v));
/** `POST /v1/auth/move/check`: the person a proof names, and what they do next. */
function parseMoveCheck(raw:unknown):MoveCodePreview{
 if(!obj(raw)||!text(raw.username,64)||!['choose_password','portico_account'].includes(String(raw.next)))invalid();
 return Object.freeze({username:raw.username,signIn:raw.next==='portico_account'?'portico':'local'});
}
function invalid():never{throw new CodedError('invalid_account_move',unreadableServerResponse);}
function ended():never{throw new CodedError('cancelled','This account move request ended.');}
// No client-clock requirements: an expiry is the server's to enforce; this device's clock never bounds it.
function expiry(raw:unknown){if(!currentUTCTimestamp(raw))invalid();return raw;}
export function parseMoveCollisionDetails(raw:unknown):MoveCollisionDetails{
 if(!obj(raw)||!text(raw.existingUsername,64)||!text(raw.serverName,120)||Object.keys(raw).some(key=>!['existingUsername','serverName'].includes(key)))invalid();
 return Object.freeze({existingUsername:raw.existingUsername,serverName:raw.serverName});
}
export function parseOwnerSignInSettings(raw:unknown):OwnerSignInSettings{
 if(!obj(raw)||!['portico','local'].includes(String(raw.kind))||!obj(raw.owner))invalid();
 const owner=raw.owner;
 if(!text(owner.id,128)||!text(owner.username,64)||owner.email!==undefined&&!text(owner.email,254))invalid();
 let move:OwnerSignInSettings['move']=null;
 if(raw.move!==undefined){
  if(!obj(raw.move)||!['portico','local'].includes(String(raw.move.to)))invalid();
  if(raw.waiting!==undefined&&!Array.isArray(raw.waiting))invalid();
  const total=raw.move.total;
  move=Object.freeze({to:raw.move.to as SignInKind,since:expiry(raw.move.since),waiting:Array.isArray(raw.waiting)?raw.waiting.length:0,...(typeof total==='number'&&Number.isSafeInteger(total)&&total>0?{total}:{})});
 }
 return Object.freeze({signIn:raw.kind as SignInKind,owner:Object.freeze({accountId:owner.id,username:owner.username,...(owner.email===undefined?{}:{email:owner.email as string})}),move});
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
export function parseMovePasswordTicket(raw:unknown):MovePasswordTicket{
 if(!obj(raw)||!secret(raw.ticket)||Object.keys(raw).some(key=>!['ticket','expiresAt'].includes(key)))invalid();
 return Object.freeze({ticket:raw.ticket,expiresAt:expiry(raw.expiresAt)});
}
export function parseMoveCodePreview(raw:unknown):MoveCodePreview{
 if(!obj(raw)||!text(raw.username,64)||!['portico','local'].includes(String(raw.signIn))||Object.keys(raw).some(key=>!['username','signIn','expiresAt'].includes(key)))invalid();
 return Object.freeze({username:raw.username,signIn:raw.signIn as SignInKind,expiresAt:expiry(raw.expiresAt)});
}
export function parseAccountMoveCodes(raw:unknown):readonly AccountMoveCode[]{
 if(!obj(raw)||!Array.isArray(raw.items)||raw.items.length>10000)invalid();
 const seen=new Set<string>();return Object.freeze(raw.items.map(row=>{
  if(!obj(row)||!text(row.accountId,128)||seen.has(row.accountId)||!text(row.username,64)||!secret(row.code))invalid();seen.add(row.accountId);
  if(row.serverId!==undefined){if(typeof row.serverId!=='string')invalid();try{identityCode(row.serverId);}catch{invalid();}}
  return Object.freeze({accountId:row.accountId,username:row.username,code:row.code,expiresAt:expiry(row.expiresAt),...(row.serverId===undefined?{}:{serverId:row.serverId as string})});
 }));
}
export type AccountMoveWaiting=Readonly<{accountId:string;username:string;expiresAt?:string}>;
/** The waiting people for the active move: exact ids the codes route accepts,
 * bounded and unique, with the usernames the owner recognises. No code, hash or
 * password material is part of this read. */
export function parseAccountMoveWaiting(raw:unknown):Readonly<{items:readonly AccountMoveWaiting[];truncated:boolean}>{
 if(!obj(raw)||!Array.isArray(raw.items)||raw.items.length>2000||typeof raw.truncated!=='boolean')invalid();
 const seen=new Set<string>();
 const items=Object.freeze(raw.items.map(row=>{
  if(!obj(row)||!text(row.accountId,128)||seen.has(row.accountId)||!text(row.username,64)||!row.username)invalid();
  seen.add(row.accountId);
  // The existing code's own expiry; an
  // absent field means no working code, never a substituted date.
  const expiresAt=row.expiresAt===undefined?undefined:expiry(row.expiresAt);
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
 if(!moveCode(code)||params.getAll('code').length!==1||params.getAll('id').length>1||[...params.keys()].some(key=>!['code','id'].includes(key)))invalid();
 if(id!==null){try{identityCode(id);}catch{invalid();}}
 return Object.freeze({code,...(id===null?{}:{id})});
}
/** Legacy code-only callers must refuse an identity-bearing link rather than
 * silently drop its expected server. Use consumeAccountMoveFragment for those. */
export function consumeMoveCodeFragment(location:Pick<Location,'pathname'|'search'|'hash'>,history:Pick<History,'replaceState'>):string|undefined{
 const link=consumeAccountMoveFragment(location,history);if(link?.id!==undefined)invalid();return link?.code;
}
export function accountMoveLink(serverOrigin:string,code:string,serverId?:string):string{
 if(!moveCode(code))invalid();if(serverId!==undefined){try{identityCode(serverId);}catch{invalid();}}
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
 /** The rebuilt server's check of the owner's move code (`server-next/API.md`, `POST /v1/auth/move/check`). */
 previewCode(code:string,signal?:AbortSignal){if(!moveCode(code))invalid();return this.call(serverPath('POST /v1/auth/move/check'),'POST',{code},parseMoveCheck,signal);}
 /** Finishes a move to local sign-in with the code and the username and password chosen (`POST /v1/auth/move/complete`); the answer is the sign-in. */
 redeemLocal(code:string,username:string,password:string,signal?:AbortSignal){if(!moveCode(code)||!text(username,64)||!text(password,72))invalid();return this.call(serverPath('POST /v1/auth/move/complete'),'POST',{code,username,password,device:signInDevice()},raw=>this.grant(raw),signal,value=>value);}
}
/** Phones/TV use this surface only. The producer still rechecks live owner + main profile. */
export class OwnerSignInSettingsClient extends MoveTransport {
 private readonly capabilities:()=>ManagementCapabilities;
 constructor(api:ProfileAPI,options:Pick<MoveClientOptions,'serverId'|'isCurrent'|'timeoutMs'>&{capabilities:()=>ManagementCapabilities}){
  super(api,{...options,retireLateGrant:async()=>{}});this.capabilities=options.capabilities;
 }
 read(signal?:AbortSignal):Promise<OwnerSignInSettings>{
  if(this.capabilities().manageSignIn!==true)throw new CodedError('forbidden','Sign-in settings require the owner’s main profile.');
  return this.call(serverPath('GET /v1/admin/sign-in'),'GET',undefined,raw=>{if(this.capabilities().manageSignIn!==true)ended();return parseOwnerSignInSettings(raw);},signal);
 }
 /** Finish now (`POST /v1/admin/sign-in/finish`, the owner's own session): the accounts still waiting become "didn't move"; their profiles and history are kept. Answers how many. */
 finish(signal?:AbortSignal):Promise<number>{
  if(this.capabilities().manageSignIn!==true)throw new CodedError('forbidden','Sign-in settings require the owner’s main profile.');
  return this.call(serverPath('POST /v1/admin/sign-in/finish'),'POST',{},raw=>{if(!obj(raw)||typeof raw.notMoved!=='number'||!Number.isSafeInteger(raw.notMoved)||raw.notMoved<0)invalid();return raw.notMoved;},signal);
 }
}
/** The owner's switch of this server's kind of sign-in, Start over, Finish now and the codes for people waiting, on the
 * rebuilt server (`server-next/internal/api/sign_in_kind.go`; definition 02 rules 15 to 30). A preview holds nothing on the
 * server; the switch is one request that answers the owner's new sign-in on this device. */
export type OwnerSwitchInput=Readonly<{to:SignInKind;startOver:boolean;serverName?:string;currentPassword?:string;code?:string;username?:string;password?:string;approval?:string}>;
export type OwnerSwitchApproval=Readonly<{approvalId:string;approvalUrl:string;pollToken:string;expiresAt:string;pollAfterMs:number}>;
export type OwnerSwitchPreview=AccountMove&Readonly<{approval?:OwnerSwitchApproval;porticoUnavailable?:boolean}>;
const declined=()=>new CodedError('approval_declined','The Portico Account approval was declined or ran out. Try again.');
export class AccountMoveClient extends MoveTransport {
 /** What a switch or Start over would do, with this server's real numbers, in the shape the dialogs draw. `from` and
  * `serverName` are the page's own facts; `returnUrl` asks Portico Services for an approval when moving to Portico. */
 preview(to:SignInKind,startOver:boolean,facts:Readonly<{from:SignInKind;serverName:string;returnUrl?:string}>,signal?:AbortSignal):Promise<OwnerSwitchPreview>{
  if(!['portico','local'].includes(to)||typeof startOver!=='boolean')invalid();
  return this.call(serverPath('POST /v1/admin/sign-in/preview'),'POST',{to,startOver,...(facts.returnUrl?{returnUrl:facts.returnUrl}:{})},raw=>{
   if(!obj(raw)||raw.to!==to||raw.startOver!==startOver)invalid();
   for(const key of ['signInAgain','removed','profilesRemoved','profilesKept','signedOut','invitationsCancelled'])if(!count(raw[key]))invalid();
   const approval=obj(raw.approval)&&text(raw.approval.approvalId,256)&&text(raw.approval.approvalUrl,2048)&&text(raw.approval.pollToken,512)?Object.freeze({approvalId:raw.approval.approvalId,approvalUrl:raw.approval.approvalUrl,pollToken:raw.approval.pollToken,expiresAt:String(raw.approval.expiresAt??''),pollAfterMs:count(raw.approval.pollAfterMs)?raw.approval.pollAfterMs:2000}):undefined;
   const n=(key:string)=>raw[key] as number;
   return Object.freeze({id:'preview',from:facts.from,to,startOver,stage:'preview' as const,revision:1,people:startOver?n('removed'):n('signInAgain'),
    profiles:n('profilesKept')+(startOver?n('profilesRemoved'):0),invitations:n('invitationsCancelled'),waiting:0,serverName:facts.serverName,ownAddressRequired:to==='local',
    ...(startOver?{retainedProfiles:n('profilesKept'),removedProfiles:n('profilesRemoved')}:{devices:n('signedOut')}),
    ...(approval?{approval}:{}),...(raw.porticoUnavailable===true?{porticoUnavailable:true}:{})});
  },signal);
 }
 /** Confirms the switch or Start over. The answer is the owner's new sign-in on this device; everyone else's ended. */
 switchTo(input:OwnerSwitchInput,signal?:AbortSignal):Promise<DirectSignIn>{
  if(!['portico','local'].includes(input.to))invalid();
  if(input.to==='local'&&(!text(input.username,64)||!text(input.password,72)))invalid();
  if(input.to==='portico'&&!text(input.approval,8192))invalid();
  const body={to:input.to,device:signInDevice(),...(input.startOver?{startOver:true,serverName:input.serverName??''}:{}),
   ...(input.currentPassword?{currentPassword:input.currentPassword}:{}),...(input.code?{code:input.code}:{}),
   ...(input.to==='local'?{username:input.username,password:input.password}:{approval:input.approval})};
  return this.call(serverPath('POST /v1/admin/sign-in/switch'),'POST',body,raw=>this.grant(raw),signal,value=>value);
 }
 /** Waits for the owner to approve with their Portico Account in another tab, and answers the approval to send. */
 async approval(approval:OwnerSwitchApproval,wait:(ms:number)=>Promise<void>,signal?:AbortSignal):Promise<string>{
  for(let tries=0;tries<600;tries++){
   const answer=await this.call(serverPath('POST /v1/hosted/approvals/{id}/poll',{id:approval.approvalId}),'POST',{pollToken:approval.pollToken},raw=>{if(!obj(raw)||!text(raw.state,64))invalid();return raw as {state:string;approval?:unknown;pollAfterMs?:unknown};},signal);
   if(answer.state==='approved'&&text(answer.approval,8192))return answer.approval;
   if(answer.state!=='pending'&&answer.state!=='waiting')throw declined();
   await wait(count(answer.pollAfterMs)&&answer.pollAfterMs>0?Math.min(answer.pollAfterMs,10000):approval.pollAfterMs);
   if(signal?.aborted)ended();
  }
  throw declined();
 }
 /** The move in progress and the people still waiting (`GET /v1/admin/sign-in`), in the shape the panel draws. */
 current(signal?:AbortSignal):Promise<AccountMove|null>{return this.call(serverPath('GET /v1/admin/sign-in'),'GET',undefined,raw=>{
  if(!obj(raw)||!['portico','local'].includes(String(raw.kind))||!Array.isArray(raw.waiting))invalid();
  if(!obj(raw.move))return null;
  const to=raw.move.to;if(to!=='local'&&to!=='portico')invalid();
  const waiting=raw.waiting.length,total=count(raw.move.total)?Math.max(raw.move.total,waiting):waiting;
  return Object.freeze({id:'move',from:(to==='local'?'portico':'local') as SignInKind,to,startOver:false,stage:to==='local'?'moving_to_local' as const:'moving_to_portico' as const,revision:1,
   people:total,profiles:0,invitations:0,waiting,serverName:'',ownAddressRequired:to==='local'});
 },signal);}
 /** Finish now: the accounts still waiting become "Didn't move" (rule 28). */
 finish(_move?:AccountMove,signal?:AbortSignal){return this.call(serverPath('POST /v1/admin/sign-in/finish'),'POST',{},raw=>{if(!obj(raw)||!count(raw.notMoved))invalid();return raw.notMoved as number;},signal);}
 /** A new code for each named person waiting, replacing any earlier one; shown once (rule 25). */
 async codes(accountIds:readonly string[],signal?:AbortSignal):Promise<readonly AccountMoveCode[]>{
  if(accountIds.length>2000||accountIds.some(id=>!text(id,128))||new Set(accountIds).size!==accountIds.length)invalid();
  const out:AccountMoveCode[]=[];
  for(const id of accountIds)out.push(await this.call(serverPath('POST /v1/admin/members/{id}/move-code',{id}),'POST',{},raw=>{
   if(!obj(raw)||raw.id!==id||!text(raw.username,64)||!text(raw.code,128))invalid();
   return Object.freeze({accountId:id,username:raw.username,code:raw.code,expiresAt:expiry(raw.expiresAt)});
  },signal));
  return Object.freeze(out);
 }
 /** The people waiting, with when their code stops working. */
 waiting(signal?:AbortSignal){return this.call(serverPath('GET /v1/admin/sign-in'),'GET',undefined,raw=>{
  if(!obj(raw)||!Array.isArray(raw.waiting)||raw.waiting.length>10000)invalid();
  return Object.freeze({items:Object.freeze(raw.waiting.map(row=>{if(!obj(row)||!text(row.id,128)||!text(row.username,64))invalid();
   return Object.freeze({accountId:row.id,username:row.username,...(row.codeExpiresAt===undefined?{}:{expiresAt:expiry(row.codeExpiresAt)})});})),truncated:false});
 },signal);}
}
