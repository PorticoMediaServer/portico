import {routePath as hostedRoutePath, encodedRoutePath as hostedEncodedRoutePath, routeBody as hostedRouteBody} from "../../contracts/src/hosted/routes.ts";
import {serviceProblem, serviceText} from './presentation/service-text.ts';
import type {MessageId} from '../../i18n/src/index.ts';
import {firstOfEach, keepReadable} from './tolerant.ts';
import {randomId} from './random-id.ts';
/** Review one account's sign-in families. No credentials or device guesses in state: a device fact the app did not report is null. */
export interface AccountSessionReviewApi {request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>}
export type AccountSessionScope=Readonly<{accountId:string;familyId:string}>;
export type AccountSessionRow=Readonly<{id:string;mode:'browser'|'native';createdAt:string;expiresAt:string;revokedAt?:string;status:'active'|'revoked'|'expired';current:boolean;deviceName:string|null;platform:string|null;app:string|null;lastUsedAt:string|null}>;
export type AccountSessionError=Readonly<{code:string;message:string;retryable:boolean}>;
export type AccountSessionReviewSnapshot=Readonly<{scope:AccountSessionScope;phase:'idle'|'loading'|'ready'|'error'|'ended';items:readonly AccountSessionRow[];observedAt:string;nextCursor:string;hasPrevious:boolean;pending:string|null;endingOthers:boolean;retryId:string|null;retryOthers:boolean;error:AccountSessionError|null;mutationError:AccountSessionError|null;notice:string}>;
const object=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const text=(v:unknown,max=256):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x1f\x7f]/.test(v);
const id=(v:unknown):v is string=>text(v,128)&&v.length>0;
const date=(v:unknown):v is string=>text(v,128)&&/^\d{4}-\d{2}-\d{2}T/.test(v)&&Number.isFinite(Date.parse(v));
// A device fact that cannot be read is unknown; it never hides the sign-in it describes.
const fact=(v:unknown)=>text(v,120)&&v.trim()!==''?v:null;
class Failure extends Error{code:string;retryable:boolean;constructor(code:string,message:string,retryable=false){super(message);this.code=code;this.retryable=retryable;}}
function invalid():never{throw new Failure('invalid_session_review','The account service returned invalid session information.');}
function errorInfo(e:unknown):AccountSessionError{const v=e as {code?:unknown;retryable?:unknown};return Object.freeze({code:id(v?.code)?v.code:'request_failed',message:serviceProblem(e,'account','accountSessions.error.load','load'),retryable:v?.retryable===true});}
function parse(value:unknown,scope:AccountSessionScope){
 // A field this app does not know is ignored. The retired `legacySessionsPresent` field is known: it marks an older review this list cannot stand in for, so it is still refused.
 if(!object(value)||value.accountId!==scope.accountId||value.currentFamilyId!==scope.familyId||!date(value.observedAt)||!text(value.nextCursor,2048)||'legacySessionsPresent' in value||!Array.isArray(value.items)||value.items.length>40)invalid();
 // Another sign-in's row that cannot be read is left out. This sign-in's own row, and any row whose `current` mark is not plainly false, has to be exact or the whole list is refused.
 let own=false;
 const rows=keepReadable('account-sessions','items',value.items,given=>{if(!object(given))return undefined;
  // This list is where someone looks for a sign-in they do not recognise, so another sign-in is never hidden for being of a kind this
  // app does not know: an unknown kind reads as an app's, and an unknown state as still active (so it is shown and can be ended).
  const raw:Record<string,unknown>=given.id===scope.familyId?given:{...given,mode:['browser','native'].includes(given.mode as string)?given.mode:typeof given.mode==='string'?'native':given.mode,status:['active','expired','revoked'].includes(given.status as string)?given.status:typeof given.status==='string'?(given.revokedAt!==undefined?'revoked':'active'):given.status};
  if(!id(raw.id)||!['browser','native'].includes(raw.mode as string)||!['active','expired','revoked'].includes(raw.status as string)||!date(raw.createdAt)||!date(raw.expiresAt)||raw.revokedAt!==undefined&&!date(raw.revokedAt)||typeof raw.current!=='boolean'||raw.current!==(raw.id===scope.familyId)||raw.current&&raw.status!=='active'||raw.revokedAt!==undefined&&raw.status!=='revoked'){if(raw.id===scope.familyId||raw.current!==false)own=true;return undefined;}return Object.freeze({id:raw.id,mode:raw.mode,createdAt:raw.createdAt,expiresAt:raw.expiresAt,...(raw.revokedAt?{revokedAt:raw.revokedAt}:{}),status:raw.status,current:raw.current,deviceName:fact(raw.deviceName),platform:fact(raw.platform),app:fact(raw.app),lastUsedAt:date(raw.lastUsedAt)?raw.lastUsedAt:null}) as AccountSessionRow;});
 if(own||rows.filter(row=>row.current).length>1||value.nextCursor&&value.items.length!==40)invalid();
 const items=firstOfEach('account-sessions','items',rows,row=>row.id);
 return {items:Object.freeze(items),observedAt:value.observedAt,nextCursor:value.nextCursor};
}
/** How a device list names one sign-in. Name and app come from what its app reported; an unknown name says only what the
 * sign-in mode proves (a browser or an app), and an unknown app or form is null rather than a guess. */
export type AccountSessionDevice=Readonly<{name:string;app:string|null;form:'phone'|'tv'|'computer'|null}>;
const platformApp:Readonly<Record<string,MessageId>>={web:'services.workspace.appWeb',tvos:'services.workspace.appAppleTV',ios:'services.workspace.appIPhone',androidtv:'services.workspace.appAndroidTV'};
export function accountSessionDevice(row:AccountSessionRow):AccountSessionDevice{
 const p=row.platform,known=p?platformApp[p]:undefined;
 return Object.freeze({name:row.deviceName??serviceText(row.mode==='browser'?'web.hostedAccount.aBrowser':'web.hostedAccount.anApp'),app:known?serviceText(known):row.app,
  form:p==='ios'||p==='android'?'phone':p==='tvos'||p==='androidtv'||p==='firetv'?'tv':p==='web'||p==='macos'||p==='windows'||p==='linux'?'computer':null});
}
export class AccountSessionReviewService{
 private api:AccountSessionReviewApi;private scope:AccountSessionScope;private timeout:number;private disposed=false;private generation=0;private read=0;private controls=new Set<AbortController>();private listeners=new Set<()=>void>();private state:AccountSessionReviewSnapshot;private pages=[''];private page=0;private command?:{key:string;promise:Promise<void>};private retry?:{familyId:string}|{operationId:string};
 constructor(o:{api:AccountSessionReviewApi;scope:AccountSessionScope;timeoutMs?:number}){if(!id(o.scope.accountId)||!id(o.scope.familyId))throw new Error('An account and sign-in session are required.');this.scope=Object.freeze({...o.scope});this.api=o.api;this.timeout=o.timeoutMs??15000;if(!Number.isFinite(this.timeout)||this.timeout<1||this.timeout>120000)throw new Error('Invalid session request timeout.');this.state=Object.freeze({scope:this.scope,phase:'idle',items:Object.freeze([]),observedAt:'',nextCursor:'',hasPrevious:false,pending:null,endingOthers:false,retryId:null,retryOthers:false,error:null,mutationError:null,notice:''});}
 getSnapshot=()=>this.state;subscribe=(fn:()=>void)=>{this.listeners.add(fn);return()=>{this.listeners.delete(fn);};};
 private publish(p:Partial<AccountSessionReviewSnapshot>){if(this.disposed)return;this.state=Object.freeze({...this.state,...p});this.listeners.forEach(fn=>fn());}
 private active(){if(this.disposed)throw new Error('Account session review is closed.');}
 dispose(){this.disposed=true;this.generation++;this.controls.forEach(c=>c.abort());this.controls.clear();this.listeners.clear();this.retry=undefined;}
 refresh():Promise<void>{this.active();return this.command?.promise??this.load(0);}
 next():Promise<void>{this.active();if(this.command||this.state.phase!=='ready'||!this.state.nextCursor)throw new Error('No next session page.');this.pages[this.page+1]=this.state.nextCursor;return this.load(this.page+1);}
 previous():Promise<void>{this.active();if(this.command||this.state.phase!=='ready'||this.page===0)throw new Error('No previous session page.');return this.load(this.page-1);}
 private ended(){this.retry=undefined;this.publish({phase:'ended',items:Object.freeze([]),pending:null,endingOthers:false,retryId:null,retryOthers:false,error:null,mutationError:null,notice:serviceText('accountSessions.youWereSignedOut')});}
 private async load(page:number){const gen=this.generation,read=++this.read;this.publish({phase:'loading',items:Object.freeze([]),error:null,nextCursor:'',hasPrevious:false});try{const raw=await this.request(hostedRoutePath("account_sessions_list") + (this.pages[page]?'?cursor='+encodeURIComponent(this.pages[page]):''));if(this.disposed||gen!==this.generation||read!==this.read)return;const value=parse(raw,this.scope);this.page=page;if(page===0)this.pages=[''];this.publish({...value,phase:'ready',hasPrevious:page>0});}catch(e){if(this.disposed||gen!==this.generation||read!==this.read)return;if((e as {status?:number})?.status===401)this.ended();else this.publish({phase:'error',error:errorInfo(e)});}}
 revoke(familyId:string):Promise<void>{this.active();if(this.command){if(this.command.key==='family:'+familyId)return this.command.promise;throw new Error('Wait for the current session request.');}if(this.retry)throw new Error('Resolve the previous session request first.');if(this.state.phase!=='ready'||!this.state.items.some(row=>row.id===familyId&&row.status==='active'))throw new Error('Refresh sessions before ending this sign-in.');return this.run(familyId);}
 /** Ends every other sign-in of the account in one request, including ones on later pages. This sign-in stays. */
 revokeOthers():Promise<void>{this.active();if(this.command){if(this.command.key==='others')return this.command.promise;throw new Error('Wait for the current session request.');}if(this.retry)throw new Error('Resolve the previous session request first.');if(this.state.phase!=='ready')throw new Error('Refresh sessions before ending other sign-ins.');return this.runOthers(randomId());}
 /** Replays the exact unconfirmed request: the same sign-in, or the same operation for every other sign-in. */
 retryRevoke():Promise<void>{this.active();const retry=this.retry;if(!retry)throw new Error('No session request to retry.');return 'familyId' in retry?this.run(retry.familyId):this.runOthers(retry.operationId);}
 private run(familyId:string):Promise<void>{
  if(this.command)return this.command.promise;const gen=this.generation;this.read++;this.retry={familyId};this.publish({pending:familyId,retryId:null,retryOthers:false,mutationError:null,notice:''});let done!:()=>void;const promise=new Promise<void>(resolve=>{done=resolve;});this.command={key:'family:'+familyId,promise};
  void(async()=>{try{const raw=await this.request(hostedEncodedRoutePath("account_session_delete", {"family": encodeURIComponent(familyId)}),'DELETE');if(this.disposed||gen!==this.generation)return;if(!object(raw)||raw.id!==familyId||raw.revoked!==true||raw.current!==(familyId===this.scope.familyId))invalid();this.retry=undefined;if(raw.current){this.ended();return;}this.publish({notice:serviceText('accountSessions.ended')});await this.load(0);
  }catch(e){if(this.disposed||gen!==this.generation)return;if((e as {status?:number})?.status===401){this.ended();return;}const error=errorInfo(e);const ambiguous=error.retryable||['timeout','request_failed','invalid_session_review'].includes(error.code);if(!ambiguous)this.retry=undefined;this.publish({mutationError:error,retryId:this.retry?familyId:null});await this.load(0);
  }finally{if(!this.disposed&&gen===this.generation){this.command=undefined;this.publish({pending:null});}done();}})();return promise;
 }
 private runOthers(operationId:string):Promise<void>{
  if(this.command)return this.command.promise;const gen=this.generation;this.read++;this.retry={operationId};this.publish({endingOthers:true,retryId:null,retryOthers:false,mutationError:null,notice:''});let done!:()=>void;const promise=new Promise<void>(resolve=>{done=resolve;});this.command={key:'others',promise};
  void(async()=>{try{const raw=await this.request(hostedRoutePath("account_sessions_revoke_others"),'POST',hostedRouteBody("account_sessions_revoke_others",{operationId}));if(this.disposed||gen!==this.generation)return;
   // The receipt must name this account, this sign-in as the one kept, and this operation; anything else is not a confirmation.
   if(!object(raw)||raw.accountId!==this.scope.accountId||raw.currentFamilyId!==this.scope.familyId||raw.operationId!==operationId||typeof raw.revokedCount!=='number'||!Number.isSafeInteger(raw.revokedCount)||raw.revokedCount<0)invalid();this.retry=undefined;this.publish({notice:serviceText('accountSessions.othersEnded',{count:raw.revokedCount})});await this.load(0);
  }catch(e){if(this.disposed||gen!==this.generation)return;if((e as {status?:number})?.status===401){this.ended();return;}const error=errorInfo(e);const ambiguous=error.retryable||['timeout','request_failed','invalid_session_review'].includes(error.code);if(!ambiguous)this.retry=undefined;this.publish({mutationError:error,retryOthers:Boolean(this.retry)});await this.load(0);
  }finally{if(!this.disposed&&gen===this.generation){this.command=undefined;this.publish({endingOthers:false});}done();}})();return promise;
 }
 private async request(path:string,method='GET',body?:unknown):Promise<unknown>{const c=new AbortController();this.controls.add(c);let timer:ReturnType<typeof setTimeout>|undefined;let abort:(()=>void)|undefined;try{return await Promise.race([this.api.request(path,method,body,c.signal),new Promise<never>((_,reject)=>{abort=()=>reject(new Failure('cancelled','Session request cancelled.'));c.signal.addEventListener('abort',abort,{once:true});timer=setTimeout(()=>{reject(new Failure('timeout','The request timed out. Its outcome is not confirmed. Retry the same session request.',true));c.abort();},this.timeout);})]);}finally{if(timer)clearTimeout(timer);if(abort)c.signal.removeEventListener('abort',abort);this.controls.delete(c);}}
}
