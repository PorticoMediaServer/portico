import {unreadableServerResponse} from './server-messages.ts';
import {sessionRoute} from './route-connection.ts';
import type {ProfileSelectionInput} from './profile-management.ts';
import {parseLocalSession} from './local-session.ts';
import { ACCOUNT_PROFILE_READ_LIMIT } from './profile-limits.ts';
/** Scoped Portico Account server/profile admission. No playback or active-viewer ownership. */
import type {HostedServer,LocalSession} from './index.ts';
export type SelectionAccount=Readonly<{accountId:string;sessionId:string}>;
export type SelectionProfile=Readonly<{id:string;name:string;eligible:boolean;art?:string;primary?:boolean;pinRequired?:boolean;pinRevision?:number;revision?:number;unavailableReason?:'membership_required'|'membership_revoked'|'server_restriction'}>;
export type ServerProfiles=Readonly<{accountId:string;serverId:string;policyRevision:number;localRevision:number;items:readonly SelectionProfile[]}>;
export type SelectionContext=Readonly<{accountId:string;sessionId:string;serverId:string;profileId:string}>;
export type SelectedSession=Readonly<{session:LocalSession;server:Readonly<HostedServer>;context:SelectionContext;/** In-memory handoff fence, never persisted as credential data. */isCurrent?:()=>boolean}>;
export interface SessionSelectionApi {
 listServers(signal:AbortSignal):Promise<{items:HostedServer[]}>;
 serverProfiles(serverId:string,signal:AbortSignal):Promise<unknown>;
 /** Adapter validates HTTPS/explicit development loopback, server key and the ticket chain. Never retries an ambiguous non-idempotent attach. */
 attach(input:Readonly<{accountId:string;server:Readonly<HostedServer>;profileId:string;selection?:ProfileSelectionInput}>,signal:AbortSignal):Promise<LocalSession>;
 /** P09 adapter seam: return the already verified logical origin for this exact
  * credential (sessionRoute(token)?.logicalOrigin), not a newly selected route. */
 selectedServer?(session:LocalSession,server:Readonly<HostedServer>):Readonly<HostedServer>;
 /** Uses the captured server, never the subsequently selected server. */
 revokeLocal(session:LocalSession,server:Readonly<HostedServer>,signal:AbortSignal):Promise<void>;
}
export type SelectionError=Readonly<{code:string;message:string;retryable:boolean}>;
export type SessionSelectionSnapshot=Readonly<{generation:number;account:SelectionAccount;phase:'idle'|'loadingServers'|'servers'|'loadingProfiles'|'profiles'|'switching'|'selected'|'error';servers:readonly Readonly<HostedServer>[];server:Readonly<HostedServer>|null;profiles:ServerProfiles|null;selected:SelectionContext|null;error:SelectionError|null;cleanupError:SelectionError|null}>;
const obj=(v:unknown):v is Record<string,unknown>=>typeof v==='object'&&v!==null&&!Array.isArray(v);
const str=(v:unknown,max=512):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x1f\x7f]/.test(v);
const id=(v:unknown):v is string=>str(v,256)&&!!v;
const integer=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
class Failure extends Error {code:string;retryable:boolean;constructor(code:string,message:string,retryable=false){super(message);this.code=code;this.retryable=retryable;}}
const info=(e:unknown):SelectionError=>{const v=e as {code?:unknown;retryable?:unknown};return Object.freeze({code:id(v?.code)?v.code:'request_failed',message:e instanceof Error?e.message:'Session selection failed.',retryable:v?.retryable===true});};
function invalid():never{throw new Failure('invalid_selection',unreadableServerResponse);}
function account(v:SelectionAccount):SelectionAccount{if(!obj(v)||!id(v.accountId)||!id(v.sessionId))throw new Error('A Portico Account and stable session-family identity are required.');return Object.freeze({accountId:v.accountId,sessionId:v.sessionId});}
function servers(raw:unknown):readonly Readonly<HostedServer>[]{if(!obj(raw)||!Array.isArray(raw.items)||raw.items.length>1000)invalid();const items=raw.items.map(v=>{if(!obj(v)||!id(v.id)||!str(v.name,2048)||!str(v.baseUrl,4096)||!str(v.publicKey,4096)||!v.publicKey||!integer(v.policyRevision))invalid();if(v.routes!==undefined&&(!obj(v.routes)||!str(v.routes.payload,65536)||!v.routes.payload||!str(v.routes.signature,1024)||!v.routes.signature))invalid();return Object.freeze({id:v.id,name:v.name,baseUrl:v.baseUrl,publicKey:v.publicKey,policyRevision:v.policyRevision,...(obj(v.routes)?{routes:Object.freeze({payload:v.routes.payload as string,signature:v.routes.signature as string})}:{})});});if(new Set(items.map(v=>v.id)).size!==items.length)invalid();return Object.freeze(items);}
function profiles(raw:unknown,a:SelectionAccount,s:HostedServer):ServerProfiles{if(!obj(raw)||raw.accountId!==a.accountId||raw.serverId!==s.id||!integer(raw.policyRevision)||raw.policyRevision<s.policyRevision||!integer(raw.localRevision)||!Array.isArray(raw.items)||raw.items.length>ACCOUNT_PROFILE_READ_LIMIT)invalid();const items=raw.items.map(v=>{if(!obj(v)||!id(v.id)||!str(v.name,2048)||typeof v.eligible!=='boolean')invalid();if(v.eligible?v.unavailableReason!==undefined:!['membership_required','membership_revoked','server_restriction'].includes(v.unavailableReason as string))invalid();if(v.pinRequired!==undefined&&typeof v.pinRequired!=='boolean'||v.pinRevision!==undefined&&(!integer(v.pinRevision)||v.pinRevision<1)||v.revision!==undefined&&(!integer(v.revision)||v.revision<1)||v.primary!==undefined&&typeof v.primary!=='boolean'||v.art!==undefined&&!str(v.art,32))invalid();return Object.freeze({id:v.id,name:v.name,eligible:v.eligible,...(v.pinRequired===undefined?{}:{pinRequired:v.pinRequired as boolean}),...(v.pinRevision===undefined?{}:{pinRevision:v.pinRevision as number}),...(v.revision===undefined?{}:{revision:v.revision as number}),...(v.art===undefined?{}:{art:v.art as string}),...(v.primary===undefined?{}:{primary:v.primary as boolean}),...(v.unavailableReason===undefined?{}:{unavailableReason:v.unavailableReason as SelectionProfile['unavailableReason']})});});if(new Set(items.map(v=>v.id)).size!==items.length)invalid();return Object.freeze({accountId:a.accountId,serverId:s.id,policyRevision:raw.policyRevision,localRevision:raw.localRevision,items:Object.freeze(items)});}
/** Pure validation used by real attachment selection; unknown fields are never retained. */
export function parseSelectedLocalSession(raw:unknown,c:SelectionContext):LocalSession {
 let session:LocalSession;
 try{session=parseLocalSession(raw);}catch{invalid();}
 const v=session.viewer;
 if(v.authority!=='hosted'||v.accountId!==c.accountId||v.serverId!==c.serverId||v.profileId!==c.profileId)throw new Failure('session_identity_mismatch','The returned session does not match the selected account, profile and server.');
 return session;
}
export class SessionSelectionService {
 private api:SessionSelectionApi;private bound:SelectionAccount;private timeout:number;private onSelected:(result:SelectedSession)=>void|Promise<void>;private state:SessionSelectionSnapshot;private generation=0;private disposed=false;private controllers=new Set<AbortController>();private listeners=new Set<()=>void>();private operation?:{key:string;promise:Promise<void>};
 constructor(o:{api:SessionSelectionApi;account:SelectionAccount;onSelected:(result:SelectedSession)=>void|Promise<void>;timeoutMs?:number}){this.api=o.api;this.bound=account(o.account);this.onSelected=o.onSelected;this.timeout=o.timeoutMs??15000;if(!Number.isFinite(this.timeout)||this.timeout<=0||this.timeout>120000)throw new Error('Invalid session selection deadline.');this.state=this.empty();}
 private empty():SessionSelectionSnapshot{return Object.freeze({generation:this.generation,account:this.bound,phase:'idle',servers:Object.freeze([]),server:null,profiles:null,selected:null,error:null,cleanupError:null});}
 getSnapshot=():SessionSelectionSnapshot=>this.state;
 subscribe=(fn:()=>void):(()=>void)=>{if(this.disposed)return()=>{};this.listeners.add(fn);return()=>{this.listeners.delete(fn);};};
 private publish(p:Partial<SessionSelectionSnapshot>):void{if(this.disposed)return;this.state=Object.freeze({...this.state,...p});for(const fn of this.listeners)fn();}
 private active():void{if(this.disposed)throw new Error('Session selection is disposed.');}
 private fence():void{this.generation++;for(const c of this.controllers)c.abort();this.controllers.clear();this.operation=undefined;}
 cancel():void{this.active();this.fence();this.publish(this.empty());}
 setAccount(next:SelectionAccount,api:SessionSelectionApi):void{this.active();const checked=account(next);this.fence();this.bound=checked;this.api=api;this.publish(this.empty());}
 dispose():void{this.fence();this.disposed=true;this.listeners.clear();}
 // A ready read snapshot is immediately actionable by subscribers. Release its
 // single-flight slot before publishing; the old run may only clear its own slot.
 loadServers():Promise<void>{this.active();if(this.operation?.key==='servers')return this.operation.promise;this.fence();this.publish({...this.empty(),phase:'loadingServers'});const api=this.api;return this.run('servers',async(c,current)=>{const data=servers(await this.deadline(api.listServers(c.signal),c));if(current()){this.operation=undefined;this.publish({phase:'servers',servers:data});}});}
 selectServer(serverId:string):Promise<void>{this.active();if(this.operation?.key==='profiles:'+serverId)return this.operation.promise;const server=this.state.servers.find(v=>v.id===serverId);if(!server)throw new Error('Select a server from the current directory.');this.fence();this.publish({generation:this.generation,phase:'loadingProfiles',server,profiles:null,selected:null,error:null});const api=this.api,a=this.bound;return this.run('profiles:'+serverId,async(c,current)=>{const data=profiles(await this.deadline(api.serverProfiles(serverId,c.signal),c),a,server);if(current()){this.operation=undefined;this.publish({phase:'profiles',profiles:data});}});}
 /** Read-only reconciliation; never repeats a ticket/attach after uncertain outcome. */
 reconcile():Promise<void>{this.active();return this.state.server?this.selectServer(this.state.server.id):this.loadServers();}
 switchProfile(profileId:string,selection?:ProfileSelectionInput):Promise<void>{
  if(selection?.pin!==undefined&&selection.pin!==''&&!/^\d{4}$/.test(selection.pin))throw new Error('Enter the four-digit profile PIN.');
  this.active();const key='attach:'+profileId;if(this.operation?.key===key)return this.operation.promise;if(this.operation)throw new Error('Wait for the current selection request.');
  const server=this.state.server,data=this.state.profiles;if(this.state.phase!=='profiles'||!server||!data?.items.some(p=>p.id===profileId&&p.eligible))throw new Error('Select a currently eligible profile.');
  const a=this.bound,api=this.api,context=Object.freeze({accountId:a.accountId,sessionId:a.sessionId,serverId:server.id,profileId});this.publish({phase:'switching',error:null,selected:null});
  return this.run(key,async(c,current)=>{
   let raw:LocalSession|undefined,offered=false,revoked=false,capturedServer=server;
   const cleanup=async()=>{if(!raw||revoked||offered)return;revoked=true;const cleanupController=new AbortController();try{await this.deadline(api.revokeLocal(raw,capturedServer,cleanupController.signal),cleanupController);}catch(e){if(current())this.publish({cleanupError:info(e)});}};
   try{
    // This continuation remains alive even when the deadline wins and the transport ignores abort.
    const pending=Promise.resolve().then(()=>{if(!current()||c.signal.aborted)throw new Failure('cancelled','Session selection cancelled before attachment.');return api.attach({accountId:a.accountId,server,profileId,...(selection?{selection:Object.freeze({...selection})}:{})},c.signal);}).then(async value=>{raw=value;try{capturedServer=this.captureServer(api,value,server);}catch(e){await cleanup();throw e;}if(!current()||c.signal.aborted)await cleanup();return value;});
    const result=await this.deadline(pending,c);if(!current()||c.signal.aborted){await cleanup();return;}
    const session=parseSelectedLocalSession(result,context);
    if(!current()||c.signal.aborted||this.bound.accountId!==a.accountId||this.bound.sessionId!==a.sessionId){await cleanup();return;}
    // The accepting adapter owns the durable handoff, including revocation of
    // an unpublished candidate on failure. Do not declare success until it has
    // finished; disposing the chooser during its player fence is expected.
    await this.onSelected(Object.freeze({session,server:capturedServer,context,isCurrent:()=>current()&&!c.signal.aborted}));offered=true;
    if(current())this.publish({phase:'selected',selected:context,error:null});
   }catch(e){await cleanup();if(current()){const error=info(e);throw new Failure(error.code==='timeout'||error.code==='request_failed'?'attach_ambiguous':error.code,error.code==='timeout'||error.code==='request_failed'?'The attachment outcome is uncertain. Refresh eligible profiles before explicitly trying again.':error.message,true);}}
  });
 }
 private captureServer(api:SessionSelectionApi,session:LocalSession,server:Readonly<HostedServer>):Readonly<HostedServer>{
  const selected=api.selectedServer?.(session,server)??{...server,baseUrl:sessionRoute(session.accessToken)?.logicalOrigin??server.baseUrl};
  if(selected.id!==server.id||selected.publicKey!==server.publicKey)throw new Failure('session_identity_mismatch','The selected route changed the server identity.');
  return Object.freeze({...server,baseUrl:selected.baseUrl});
 }
 private run(key:string,work:(controller:AbortController,current:()=>boolean)=>Promise<void>):Promise<void>{const gen=this.generation,c=new AbortController();this.controllers.add(c);const current=()=>!this.disposed&&this.generation===gen;let resolve!:()=>void;const promise=new Promise<void>(r=>{resolve=r;});this.operation={key,promise};void(async()=>{try{await work(c,current);}catch(e){if(current())this.publish({phase:'error',profiles:null,error:info(e)});}finally{this.controllers.delete(c);if(current()&&this.operation?.promise===promise)this.operation=undefined;resolve();}})();return promise;}
 private async deadline<T>(operation:Promise<T>,c:AbortController):Promise<T>{let timer:ReturnType<typeof setTimeout>|undefined;let listener:(()=>void)|undefined;try{return await Promise.race([operation,new Promise<never>((_,reject)=>{listener=()=>reject(new Failure('cancelled','Session selection cancelled.'));c.signal.addEventListener('abort',listener,{once:true});if(c.signal.aborted)listener();timer=setTimeout(()=>{reject(new Failure('timeout','Session selection timed out.',true));c.abort();},this.timeout);})]);}finally{if(timer)clearTimeout(timer);if(listener)c.signal.removeEventListener('abort',listener);}}
}
