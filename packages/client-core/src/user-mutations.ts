import {apiRoot} from '../../contracts/src/route-paths.ts';
/** Durable custody for the reviewed user-mutation cohort. Authentication always
 * comes from the live original family; credentials never enter this store. */
export type UserMutationScope = Readonly<{serverId:string;origin:string;authority:string;accountId:string;profileId:string;familyId:string;deviceId?:string}>;
export type UserMutationResponse = Readonly<{status:number;headers:Readonly<Record<string,string>>;body?:unknown}>;
export type UserMutationReceipt = Readonly<{key:string;operation:string;status:'applied'|'already_stopped'|'deleted'|'removed'|'partial'|'conflict'|'invalid'|'terminal';targetId?:string;revision?:number;generation?:number;positionMs?:number;positionStatus?:''|'applied'|'already_stopped'|'not_renderer'|'policy_rejected'|'unchanged';committedMs:number;expiresMs:number}>;
export type UserMutationInput = Readonly<{operation:string;method:string;path:string;target:string;intentId?:string;key?:string;recoveryDeviceClass?:string;requiresReentry?:boolean;secretFields?:readonly string[];body?:unknown;headers?:Readonly<Record<string,string>>}>;
export type UserMutationTerminalProof = Readonly<{kind:'refused';httpStatus:400|409|412|422;code:string}>;
export type UserMutationEntry = {
 key:string;operation:string;method:string;path:string;target:string;intentId:string;fingerprint:string;scope:UserMutationScope;
 recoveryDeviceClass?:string;requiresReentry?:boolean;secretFields?:readonly string[];body?:unknown;headers:Record<string,string>;state:'pending'|'review'|'quarantined'|'confirmed';receipt?:UserMutationReceipt;
 nativeStopOwner?:string;nativeStopConsumed?:boolean;attempts:number;dueMs:number;leaseOwner?:string;leaseUntilMs?:number;errorCode?:string;terminalProof?:UserMutationTerminalProof;
};
export type UserMutationState = {version:1;revision:number;entries:UserMutationEntry[]};
export interface DurableUserMutationStore {
 read():Promise<UserMutationState>;
 /** The comparison and entire replacement must commit atomically. False means
  * another owner won; no caller may emulate CAS with separate read/write. */
 compareAndSwap(expectedRevision:number,next:UserMutationState):Promise<boolean>;
}
export type UserMutationTransport = {
 send(entry:Readonly<UserMutationEntry>,signal:AbortSignal):Promise<UserMutationResponse>;
 lookup(entry:Readonly<UserMutationEntry>,signal:AbortSignal):Promise<UserMutationResponse>;
 /** Only current-state reads. It must never author another mutation. */
 recover(entry:Readonly<UserMutationEntry>,receipt:UserMutationReceipt,signal:AbortSignal):Promise<UserMutationResponse>;
 /** Optional current-state read for an unreceipted terminal refusal. Never persisted. */
 current?(entry:Readonly<UserMutationEntry>,signal:AbortSignal):Promise<UserMutationResponse>;
};
export const USER_MUTATION_MAX_ENTRIES=256,USER_MUTATION_MAX_BYTES=8*1024*1024,USER_MUTATION_MAX_REQUEST_BYTES=65536;
const leaseMs=30_000,leaseRenewMs=10_000,pollMs=1000;
// Reserve room before acceptance for a tiny receipt plus lease/retry metadata.
export const USER_MUTATION_COMPLETION_RESERVE_BYTES=2048;
const object=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const bytes=(v:unknown)=>new TextEncoder().encode(JSON.stringify(v)).length;
const clone=<T>(v:T):T=>JSON.parse(JSON.stringify(v));
const integer=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
export const emptyUserMutationState=():UserMutationState=>({version:1,revision:0,entries:[]});
export const sameUserMutationScope=(a:UserMutationScope,b:UserMutationScope)=>a.serverId===b.serverId&&a.origin===b.origin&&a.authority===b.authority&&a.accountId===b.accountId&&a.profileId===b.profileId&&a.familyId===b.familyId&&a.deviceId===b.deviceId;
export function isUserMutationKey(key:string):boolean{return /^p251_[0-9]{13}_[a-f0-9]{32}$/.test(key);}
export function newUserMutationKey(now=Date.now(),random:(out:Uint8Array)=>Uint8Array=out=>{const bytes=new Uint8Array(out.length);globalThis.crypto.getRandomValues(bytes);out.set(bytes);return out;}):string{
 if(!integer(now)||String(now).length!==13)throw new UserMutationError('mutation_clock');
 return 'p251_'+now+'_'+Array.from(random(new Uint8Array(16)),v=>v.toString(16).padStart(2,'0')).join('');
}
export class UserMutationError extends Error {
 readonly code:string;readonly key?:string;readonly receipt?:UserMutationReceipt;readonly currentState?:UserMutationResponse;readonly terminalProof?:UserMutationTerminalProof;readonly response?:UserMutationResponse;
 constructor(code:string,key?:string,receipt?:UserMutationReceipt,currentState?:UserMutationResponse,terminalProof?:UserMutationTerminalProof,response?:UserMutationResponse){super(code);this.name='UserMutationError';this.code=code;this.key=key;this.receipt=receipt;this.currentState=currentState;this.terminalProof=terminalProof;this.response=response;}
}
function canonical(value:unknown):string {
 const normalize=(v:unknown):unknown=>Array.isArray(v)?v.map(normalize):object(v)?Object.fromEntries(Object.keys(v).sort().filter(k=>v[k]!==undefined).map(k=>[k,normalize(v[k])])):v;
 return JSON.stringify(normalize(value));
}
export function userMutationFingerprint(input:UserMutationInput):string {
 return canonical({operation:input.operation,method:input.method,path:input.path,target:input.target,body:input.body,headers:input.headers??{},...(input.requiresReentry?{requiresReentry:true,secretFields:input.secretFields??[]}: {})});
}
function freeze<T>(value:T):T {
 if(value&&typeof value==='object'){for(const child of Object.values(value))freeze(child);Object.freeze(value);}return value;
}
export function userMutationSecretField(key:string):boolean {
 const normalized=key.replace(/[^a-z0-9]/gi,'').toLowerCase();
 if(normalized==='undotoken')return false; // Domain undo reference, scoped and validated by the original family.
 return /(?:token|password|passphrase|secret|apikey|privatekey|tvdbprojectkey|acoustidkey)$/.test(normalized)||['authorization','cookie'].includes(normalized);
}
/** URLs with embedded credentials must never enter durable request/fingerprint data. */
export function userMutationCredentialURL(value:unknown):boolean {
 if(typeof value!=='string')return false;
 const letter=(code:number)=>code>=65&&code<=90||code>=97&&code<=122;
 const scheme=(code:number)=>letter(code)||code>=48&&code<=57||code===43||code===45||code===46;
 let offset=0;
 for(;;){
  const separator=value.indexOf('://',offset);if(separator<0)break;
  let start=separator;while(start>0&&scheme(value.charCodeAt(start-1)))start--;
  offset=separator+3;
  if(start===separator||!letter(value.charCodeAt(start)))continue;
  let end=offset;while(end<value.length&&!/[\s"'<>]/.test(value[end]!))end++;
  const candidate=value.slice(start,end);offset=end;
  const authority=candidate.split('://')[1]?.split(/[/?#]/,1)[0]??'';
  if(authority.includes('@'))return true;
  const query=candidate.split('?')[1]?.split('#')[0];if(query===undefined)continue;
  for(const name of new URLSearchParams(query).keys()){
   const normalized=name.replace(/[^a-z0-9]/gi,'').toLowerCase();
   if(userMutationSecretField(name)||/^(?:auth|credential|credentials|user|username|pass|pwd|key|sig|signature|awsaccesskeyid|xamzcredential|xamzsignature|xgoogcredential|xgoogsignature)$/.test(normalized))return true;
  }
 }
 return false;
}
function safeRequest(value:unknown):boolean{
 if(typeof value==='string')return !userMutationCredentialURL(value);
 if(Array.isArray(value))return value.every(safeRequest);
 if(object(value))return (Object.getPrototypeOf(value)===Object.prototype||Object.getPrototypeOf(value)===null)&&Object.entries(value).every(([key,v])=>!(userMutationSecretField(key)&&v!==undefined&&v!==null&&v!=='')&&safeRequest(v));
 return value===undefined||value===null||typeof value==='boolean'||typeof value==='number'&&Number.isFinite(value);
}
export function validateUserMutationState(raw:unknown):UserMutationState {
 if(!object(raw)||raw.version!==1||!integer(raw.revision)||!Array.isArray(raw.entries)||raw.entries.length>USER_MUTATION_MAX_ENTRIES||bytes(raw)>USER_MUTATION_MAX_BYTES)throw new UserMutationError('mutation_store_damaged');
 const seen=new Set<string>();
 for(const entry of raw.entries){
  if(!object(entry)||typeof entry.key!=='string'||!isUserMutationKey(entry.key)||seen.has(entry.key)||!object(entry.scope)||!['serverId','origin','authority','accountId','profileId','familyId'].every(k=>typeof (entry.scope as Record<string,unknown>)[k]==='string'&&((entry.scope as Record<string,string>)[k].length>0))||!['pending','review','quarantined','confirmed'].includes(String(entry.state))||typeof entry.operation!=='string'||typeof entry.path!=='string'||typeof entry.method!=='string'||typeof entry.target!=='string'||typeof entry.intentId!=='string'||typeof entry.fingerprint!=='string'||!integer(entry.attempts)||!integer(entry.dueMs)||!object(entry.headers)||!safeRequest(entry.body)||!safeRequest(entry.headers)||bytes({body:entry.body,headers:entry.headers})>USER_MUTATION_MAX_REQUEST_BYTES)throw new UserMutationError('mutation_store_damaged');
  if(entry.receipt!==undefined){const receipt=parseUserMutationReceipt(entry.receipt,entry as unknown as UserMutationEntry);if(canonical(receipt)!==canonical(entry.receipt))throw new UserMutationError('mutation_store_damaged');}
  if(entry.terminalProof!==undefined&&(!object(entry.terminalProof)||entry.terminalProof.kind!=='refused'||![400,409,412,422].includes(Number(entry.terminalProof.httpStatus))||typeof entry.terminalProof.code!=='string'))throw new UserMutationError('mutation_store_damaged');
  if(entry.state==='confirmed'&&(!entry.receipt||entry.fingerprint!==''||entry.body!==undefined))throw new UserMutationError('mutation_store_damaged');
  if(entry.leaseUntilMs!==undefined&&!integer(entry.leaseUntilMs)||entry.leaseOwner!==undefined&&typeof entry.leaseOwner!=='string')throw new UserMutationError('mutation_store_damaged');
  if(entry.nativeStopOwner!==undefined&&(typeof entry.nativeStopOwner!=='string'||!entry.nativeStopOwner||entry.nativeStopOwner.length>256)||entry.nativeStopConsumed!==undefined&&typeof entry.nativeStopConsumed!=='boolean')throw new UserMutationError('mutation_store_damaged');
  if(entry.requiresReentry!==undefined&&entry.requiresReentry!==true||entry.secretFields!==undefined&&(!Array.isArray(entry.secretFields)||entry.secretFields.length>64||entry.secretFields.some((name:unknown)=>typeof name!=='string'||name.length>256)))throw new UserMutationError('mutation_store_damaged');
  if(entry.recoveryDeviceClass!==undefined&&!['web','mobile','tablet','television'].includes(String(entry.recoveryDeviceClass)))throw new UserMutationError('mutation_store_damaged');
  seen.add(entry.key);
 }
 return raw as UserMutationState;
}
export function parseUserMutationReceipt(raw:unknown,entry:Pick<UserMutationEntry,'key'|'operation'>):UserMutationReceipt{
 if(object(raw)&&object(raw.data))raw=raw.data;
 if(!object(raw)||raw.key!==entry.key||raw.operation!==entry.operation||!['applied','already_stopped','deleted','removed','partial','conflict','invalid','terminal'].includes(String(raw.status))||!integer(raw.committedMs)||!integer(raw.expiresMs)||raw.expiresMs<raw.committedMs)throw new UserMutationError('mutation_receipt_invalid',entry.key);
 for(const key of ['revision','generation','positionMs'])if(raw[key]!==undefined&&!integer(raw[key]))throw new UserMutationError('mutation_receipt_invalid',entry.key);
 if(raw.targetId!==undefined&&(typeof raw.targetId!=='string'||new TextEncoder().encode(raw.targetId).length>128))throw new UserMutationError('mutation_receipt_invalid',entry.key);
 if(raw.generation!==undefined&&(raw.generation as number)>2147483647)throw new UserMutationError('mutation_receipt_invalid',entry.key);
 if(raw.positionStatus!==undefined&&!['','applied','already_stopped','not_renderer','policy_rejected','unchanged'].includes(String(raw.positionStatus)))throw new UserMutationError('mutation_receipt_invalid',entry.key);
 // Deliberately copy only the closed receipt fields, never an expanded response.
 return {key:entry.key,operation:entry.operation,status:raw.status as UserMutationReceipt['status'],committedMs:raw.committedMs,expiresMs:raw.expiresMs,...(raw.targetId!==undefined?{targetId:raw.targetId as string}:{}),...(raw.revision!==undefined?{revision:raw.revision as number}:{}),...(raw.generation!==undefined?{generation:raw.generation as number}:{}),...(raw.positionMs!==undefined?{positionMs:raw.positionMs as number}:{}),...(raw.positionStatus!==undefined?{positionStatus:raw.positionStatus as UserMutationReceipt['positionStatus']}:{})};
}
// A malformed network receipt is unknown history, not a terminal disposition.
// Store validation still uses the strict parser directly.
function receivedUserMutationReceipt(raw:unknown,entry:Pick<UserMutationEntry,'key'|'operation'>):UserMutationReceipt {
 try{return parseUserMutationReceipt(raw,entry);}catch(error){if(error instanceof UserMutationError&&error.code==='mutation_receipt_invalid')throw new Error('mutation_receipt_pending');throw error;}
}
export function userMutationRetryAfterMs(value:string|undefined,now=Date.now()):number{
 if(!value)return 0;const trimmed=value.trim();
 if(/^[0-9]+$/.test(trimmed)){const n=Number(trimmed);return Number.isFinite(n)?Math.min(n*1000,Number.MAX_SAFE_INTEGER-now):Number.MAX_SAFE_INTEGER-now;}
 const date=Date.parse(trimmed);return Number.isFinite(date)?Math.max(0,date-now):0;
}
function header(response:UserMutationResponse,name:string):string|undefined{return Object.entries(response.headers).find(([key])=>key.toLowerCase()===name.toLowerCase())?.[1];}
function errorCode(response:UserMutationResponse):string{
 const body=response.body;return object(body)&&object(body.error)&&typeof body.error.code==='string'?body.error.code:'request_failed';
}
function refusal(entry:UserMutationEntry,response:UserMutationResponse):UserMutationError {
 const code=response.status===401||response.status===403?'mutation_authority_changed':errorCode(response);
 const proof:UserMutationTerminalProof|undefined=[400,409,412,422].includes(response.status)?{kind:'refused',httpStatus:response.status as 400|409|412|422,code}:undefined;
 return new UserMutationError(code,entry.key,undefined,undefined,proof,response);
}
type Waiter={resolve:(r:UserMutationResponse)=>void;reject:(e:unknown)=>void};
export class UserMutationCoordinator {
 private stopConsumers=new Map<string,UserMutationScope>();
 private confirmedForeground=new Map<string,number>();
 private confirmedAborts=new Set<AbortController>();
 private readonly owner:string;private readonly now:()=>number;private readonly random:()=>number;
 private readonly timers:Pick<typeof globalThis,'setTimeout'|'clearTimeout'>;
 private timer:ReturnType<typeof setTimeout>|undefined;private stopped=false;private pumping=false;
 private entries:UserMutationEntry[]=[];private listeners=new Set<()=>void>();private waiters=new Map<string,Set<Waiter>>();
 private running=new Map<string,{abort:AbortController;promise:Promise<void>;scope:UserMutationScope}>();private recovering=new Set<string>();
 private readonly options:{store:DurableUserMutationStore;scope:()=>UserMutationScope|undefined;transport:UserMutationTransport;now?:()=>number;random?:()=>number;ownerId?:string;timers?:Pick<typeof globalThis,'setTimeout'|'clearTimeout'>};
 constructor(options:{store:DurableUserMutationStore;scope:()=>UserMutationScope|undefined;transport:UserMutationTransport;now?:()=>number;random?:()=>number;ownerId?:string;timers?:Pick<typeof globalThis,'setTimeout'|'clearTimeout'>}){
  this.options=options;
  this.now=options.now??Date.now;this.random=options.random??Math.random;this.timers=options.timers??globalThis;
  this.owner=options.ownerId??newUserMutationKey(this.now());
 }
 snapshot():readonly UserMutationEntry[]{return clone(this.entries);}
 /** Read authoritative local custody after an ambiguous store result. The
  * presentation snapshot alone cannot prove that admission never committed. */
 async inspectIntent(intentId:string):Promise<Readonly<UserMutationEntry>|undefined>{
  if(this.stopped)throw new UserMutationError('mutation_scope_changed');
  const scope=this.options.scope();if(!scope)throw new UserMutationError('mutation_scope_changed');
  let state:UserMutationState;
  try{state=validateUserMutationState(await this.options.store.read());}catch(error){throw error instanceof UserMutationError?error:new UserMutationError('mutation_store_unavailable');}
  const live=this.options.scope();if(this.stopped||!live||!sameUserMutationScope(scope,live))throw new UserMutationError('mutation_scope_changed');
  const entry=state.entries.find(e=>sameUserMutationScope(e.scope,scope)&&(e.key===intentId||e.intentId===intentId));
  return entry?freeze(clone(entry)):undefined;
 }
 subscribe(listener:()=>void):()=>void{this.listeners.add(listener);return()=>this.listeners.delete(listener);}
 private publish(state:UserMutationState){this.entries=clone(state.entries);for(const listener of this.listeners){try{listener();}catch{/* A view listener cannot reject committed custody. */}}}
 private async change<T>(work:(state:UserMutationState)=>T):Promise<T>{
  for(let attempt=0;attempt<32;attempt++){
   let before:UserMutationState;
   try{before=validateUserMutationState(await this.options.store.read());}catch(error){throw error instanceof UserMutationError?error:new UserMutationError('mutation_store_unavailable');}
   if(this.stopped)throw new UserMutationError('mutation_scope_changed');
   const next=clone(before),result=work(next);
   next.revision=before.revision+1;validateUserMutationState(next);
   let swapped:boolean;try{swapped=await this.options.store.compareAndSwap(before.revision,next);}catch(error){throw error instanceof UserMutationError?error:new UserMutationError('mutation_store_unavailable');}
   if(swapped){if(!this.stopped)this.publish(next);return result;}
  }
  throw new UserMutationError('mutation_store_busy');
 }
 async start():Promise<void>{this.stopped=false;await this.reconcile();}
 /** Resume durable background reconciliation, including after a scope change. */
 async reconcile(key?:string):Promise<void>{
  if(this.stopped)return;
  if(key===undefined){await this.pump();return;}
  if(!isUserMutationKey(key))throw new UserMutationError('invalid_mutation_key',key);
  let state:UserMutationState;
  try{state=validateUserMutationState(await this.options.store.read());}catch(error){throw error instanceof UserMutationError?error:new UserMutationError('mutation_store_unavailable',key);}
  if(this.stopped)return;
  const original=state.entries.find(entry=>entry.key===key),scope=this.options.scope();
  if(!original||!scope||!sameUserMutationScope(scope,original.scope))throw new UserMutationError('mutation_scope_changed',key);
  const busy=()=>this.running.has(key)||this.recovering.has(key)||this.waiters.has(key)||(this.confirmedForeground.get(key)??0)>0;
  if(!['review','confirmed'].includes(original.state)||busy())return;
  const witness=canonical(original),abort=new AbortController();
  this.recovering.add(key);this.confirmedAborts.add(abort);
  try{
   const lookup=await this.options.transport.lookup(freeze(clone(original)),abort.signal);
   if(this.stopped)return;
   const live=this.options.scope();if(!live||!sameUserMutationScope(live,original.scope))throw new UserMutationError('mutation_scope_changed',key);
   // Unknown, expired or ambiguous history never sends, rewrites or releases a draft.
   if(lookup.status!==200)return;
   const receipt=receivedUserMutationReceipt(lookup.body,original),review=['partial','conflict','invalid','terminal'].includes(receipt.status)||receipt.positionStatus==='policy_rejected';
   await this.change(current=>{
    const entry=current.entries.find(entry=>entry.key===key),live=this.options.scope();
    if(!entry||canonical(entry)!==witness||!live||!sameUserMutationScope(live,original.scope)||this.running.has(key)||this.waiters.has(key)||(this.confirmedForeground.get(key)??0)>0)return;
    entry.receipt=receipt;entry.state=review?'review':'confirmed';delete entry.terminalProof;
    if(review)entry.errorCode=receipt.positionStatus==='policy_rejected'?'final_position_rejected':'mutation_needs_review';
    else{delete entry.body;entry.fingerprint='';entry.headers={'Idempotency-Key':entry.key};delete entry.errorCode;delete entry.leaseOwner;delete entry.leaseUntilMs;}
   });
  }finally{this.recovering.delete(key);this.confirmedAborts.delete(abort);}
 }
 /** Native result consumers wait for local foreground consumers before releasing Stop metadata. */
 async consumeConfirmedStop(key:string):Promise<boolean>{
  const scope=this.options.scope();if(!scope||!isUserMutationKey(key))return false;
  if(this.stopConsumers.has(key)){this.wake();return false;}
  if(this.stopConsumers.size>=USER_MUTATION_MAX_ENTRIES)throw new UserMutationError('mutation_store_busy',key);
  this.stopConsumers.set(key,clone(scope));await this.drainStopConsumers();this.wake();
  return !this.stopConsumers.has(key)&&!this.entries.some(entry=>entry.key===key);
 }
 private async drainStopConsumers():Promise<void>{
  if(!this.stopConsumers.size)return;
  const done=await this.change(state=>{
   const done:string[]=[],scope=this.options.scope();
   for(const [key,requestedScope]of this.stopConsumers){
    const entry=state.entries.find(entry=>entry.key===key);
    if(!scope||!sameUserMutationScope(scope,requestedScope)||!entry||!sameUserMutationScope(entry.scope,requestedScope)||['review','quarantined'].includes(entry.state)||!['stop_playback_session','stop_account_playback'].includes(entry.operation)){done.push(key);continue;}
    if(entry.state!=='confirmed'||!entry.receipt||entry.nativeStopOwner&&!entry.nativeStopConsumed||this.waiters.has(key)||this.recovering.has(key)||this.running.has(key)||(this.confirmedForeground.get(key)??0)>0)continue;
    state.entries=state.entries.filter(entry=>entry.key!==key);done.push(key);
   }return done;
  });
  for(const key of done)this.stopConsumers.delete(key);
 }
 /** Explicit caller acknowledgement; never evict unresolved or quarantined drafts. */
 async acknowledge(key:string):Promise<void>{await this.change(state=>{const entry=state.entries.find(e=>e.key===key);if(!entry)return;const scope=this.options.scope();if(entry.state!=='confirmed'||!entry.receipt||!scope||!sameUserMutationScope(entry.scope,scope))throw new UserMutationError('mutation_needs_review',key);state.entries=state.entries.filter(e=>e.key!==key);});}
 /** Only an explicit user review may release a terminal draft. Unknown history
  * (including receipt_expired), pending, and quarantined work is never released. */
 async dismissReviewed(key:string):Promise<void>{
  await this.change(state=>{const entry=state.entries.find(e=>e.key===key),scope=this.options.scope();
   const proof=entry?.receipt&&(['partial','conflict','invalid','terminal'].includes(entry.receipt.status)||entry.receipt.positionStatus==='policy_rejected')||entry?.terminalProof?.kind==='refused';
   if(!entry||entry.state!=='review'||!proof||!scope||!sameUserMutationScope(scope,entry.scope))throw new UserMutationError('mutation_needs_review',key);
   state.entries=state.entries.filter(e=>e.key!==key);
  });
 }
 dispose(){this.stopped=true;if(this.timer!==undefined)this.timers.clearTimeout(this.timer);this.timer=undefined;for(const abort of this.confirmedAborts)abort.abort();for(const {abort} of this.running.values())abort.abort();for(const [key]of this.waiters)this.reject(key,new UserMutationError('mutation_scope_changed',key));}
 private wake(delay=0){if(this.stopped)return;if(this.timer!==undefined)this.timers.clearTimeout(this.timer);this.timer=this.timers.setTimeout(()=>{this.timer=undefined;void this.pump().catch(()=>{this.wake(1000);});},Math.min(60_000,Math.max(0,delay)));}
 async run(input:UserMutationInput,signal?:AbortSignal):Promise<UserMutationResponse>{
  if(this.stopped)throw new UserMutationError('mutation_scope_changed');
  const liveScope=this.options.scope();if(!liveScope)throw new UserMutationError('mutation_scope_changed');
  const scope=clone(liveScope);
  if(signal?.aborted)throw new DOMException('The request was cancelled.','AbortError');
  // Capture caller-owned objects synchronously, before the first store await.
  try {
   if(!safeRequest(input.body)||!safeRequest(input.headers??{}))throw new Error();
   input=clone(input);
   const path=new URL(input.path,'http://mutation.invalid');
   if(!input.path.startsWith(apiRoot)||path.pathname+path.search!==input.path||path.hash||input.path.includes('\\')||!['POST','PUT','PATCH','DELETE'].includes(input.method)||!input.operation||!input.target)throw new Error();
   const origin=new URL(scope.origin);if(origin.origin!==scope.origin||origin.username||origin.password)throw new Error();
   // Mounted transports supply only these semantic headers; credentials stay live.
   if(Object.keys(input.headers??{}).some(k=>!['content-type','if-match','if-unmodified-since'].includes(k.toLowerCase())))throw new Error();
   if([...path.searchParams.keys()].some(k=>/token|password|secret|authorization|key/i.test(k)))throw new Error();
  }catch{throw new UserMutationError('mutation_not_queued');}
  const original=userMutationFingerprint(input);
  if(new TextEncoder().encode(original).length>USER_MUTATION_MAX_REQUEST_BYTES)throw new UserMutationError('mutation_not_queued');
  const now=this.now(),key=input.key??newUserMutationKey(now),intentId=input.intentId??key;
  if(!isUserMutationKey(key))throw new UserMutationError('invalid_mutation_key');
  const entry=await this.change(state=>{
   const conflicting=state.entries.find(e=>e.key===key&&!sameUserMutationScope(e.scope,scope));if(conflicting)throw new UserMutationError('idempotency_conflict',key);
   const prior=state.entries.find(e=>sameUserMutationScope(e.scope,scope)&&(e.key===key||e.intentId===intentId));
   if(prior){
    // A known applied intent cannot send again. Its request/fingerprint has been
    // erased; compare only retained identity metadata before current readback.
    const sameIdentity=prior.operation===input.operation&&prior.method===input.method&&prior.path===input.path&&prior.target===input.target;
    const generationChanged=object(prior.body)&&object(input.body)&&prior.body.expectedGeneration!==undefined&&input.body.expectedGeneration!==undefined&&prior.body.expectedGeneration!==input.body.expectedGeneration;
    // Native/JS final evidence can differ at handover. The original captured Stop
    // wins only for the identical occurrence identity; its body is never merged.
    const existingStop=sameIdentity&&input.operation==='stop_playback_session'&&prior.intentId===intentId&&!generationChanged;
    const matches=prior.state==='confirmed'?sameIdentity:existingStop||prior.fingerprint===original;
    if(!matches)throw new UserMutationError('idempotency_conflict',prior.key);return clone(prior);
   }
   const conflict=state.entries.find(e=>sameUserMutationScope(e.scope,scope)&&e.target===input.target&&e.state==='review'&&(e.receipt?.status==='conflict'||e.terminalProof?.kind==='refused'&&[409,412].includes(e.terminalProof.httpStatus)));
   if(conflict)throw new UserMutationError('mutation_needs_review',conflict.key,conflict.receipt);
   if(state.entries.length>=USER_MUTATION_MAX_ENTRIES)throw new UserMutationError('mutation_not_queued');
   const body=input.body===undefined?undefined:clone(input.body),headers={...input.headers,'Idempotency-Key':key};
   if(object(body))for(const name of ['operationId','idempotencyKey','requestId'])if(name in body)body[name]=key;
   const next:UserMutationEntry={...(input.recoveryDeviceClass?{recoveryDeviceClass:input.recoveryDeviceClass}:{}),...(input.requiresReentry?{requiresReentry:true,secretFields:clone(input.secretFields??[])}:{}),key,operation:input.operation,method:input.method,path:input.path,target:input.target,intentId,fingerprint:original,scope:clone(scope),...(body===undefined?{}:{body}),headers,state:'pending',attempts:0,dueMs:now};
   if(bytes({body,headers})>USER_MUTATION_MAX_REQUEST_BYTES)throw new UserMutationError('mutation_not_queued');
   state.entries.push(next);if(bytes(state)+state.entries.filter(e=>e.state==='pending').length*USER_MUTATION_COMPLETION_RESERVE_BYTES>USER_MUTATION_MAX_BYTES)throw new UserMutationError('mutation_not_queued');return clone(next);
  });
  if(this.stopped)throw new UserMutationError('mutation_scope_changed',entry.key);
  if(entry.state==='review'||entry.state==='quarantined')throw new UserMutationError(entry.errorCode??'mutation_needs_review',entry.key,entry.receipt);
  if(entry.state==='confirmed'&&entry.receipt){
   const current=this.options.scope();if(!current||!sameUserMutationScope(current,entry.scope))throw new UserMutationError('mutation_scope_changed',entry.key);
   const abort=new AbortController(),cancel=()=>abort.abort();
   this.confirmedForeground.set(entry.key,(this.confirmedForeground.get(entry.key)??0)+1);this.confirmedAborts.add(abort);
   signal?.addEventListener('abort',cancel,{once:true});
   try{
    if(signal?.aborted)abort.abort();if(abort.signal.aborted)throw new DOMException('The request was cancelled.','AbortError');
    const response=await this.options.transport.recover(freeze(entry),entry.receipt,abort.signal);
    if(this.stopped)throw new UserMutationError('mutation_scope_changed',entry.key);
    if(abort.signal.aborted)throw new DOMException('The request was cancelled.','AbortError');
    const live=this.options.scope();if(!live||!sameUserMutationScope(live,entry.scope))throw new UserMutationError('mutation_scope_changed',entry.key);return response;
   }finally{
    signal?.removeEventListener('abort',cancel);this.confirmedAborts.delete(abort);
    const remaining=(this.confirmedForeground.get(entry.key)??1)-1;if(remaining>0)this.confirmedForeground.set(entry.key,remaining);else this.confirmedForeground.delete(entry.key);
    this.wake();
   }
  }

  return new Promise((resolve,reject)=>{
   const waiters=this.waiters.get(entry.key)??new Set<Waiter>();this.waiters.set(entry.key,waiters);
   const onAbort=()=>{waiters.delete(waiter);if(waiters.size===0)this.waiters.delete(entry.key);reject(new DOMException('The request was cancelled.','AbortError'));};
   const waiter:Waiter={resolve:r=>{signal?.removeEventListener('abort',onAbort);resolve(r);},reject:e=>{signal?.removeEventListener('abort',onAbort);reject(e);}};
   waiters.add(waiter);signal?.addEventListener('abort',onAbort,{once:true});if(signal?.aborted)onAbort();this.wake();
  });
 }
 private reject(key:string,error:unknown){const set=this.waiters.get(key);this.waiters.delete(key);for(const waiter of set??[])waiter.reject(error);}
 private resolve(key:string,response:UserMutationResponse){const set=this.waiters.get(key);this.waiters.delete(key);for(const waiter of set??[])waiter.resolve(response);}
 private async pump():Promise<void>{
  if(this.stopped||this.pumping)return;this.pumping=true;
  try{
   await this.drainStopConsumers();
   if(this.stopped)return;
   const scope=this.options.scope(),now=this.now();let nextDue=now+pollMs;
   for(const {abort,scope:original}of this.running.values())if(!scope||!sameUserMutationScope(scope,original))abort.abort();
   const picked=await this.change(state=>{
    const picks:UserMutationEntry[]=[];
    for(const entry of state.entries){
     if(entry.state!=='pending')continue;
     if(!scope||!sameUserMutationScope(entry.scope,scope)){
      entry.state='quarantined';entry.errorCode='mutation_scope_changed';delete entry.leaseOwner;delete entry.leaseUntilMs;continue;
     }
     // A live owner renews through the same aggregate CAS as acquisitions.
     if(this.running.has(entry.key)&&entry.leaseOwner===this.owner)entry.leaseUntilMs=now+leaseMs;
    }
    let occupied=state.entries.filter(e=>e.state==='pending'&&(e.leaseUntilMs??0)>now).length;
    const leasedTargets=new Set(state.entries.filter(e=>e.state==='pending'&&(e.leaseUntilMs??0)>now).map(e=>e.target));
    const targets=new Set<string>();
    for(const entry of state.entries){
     if(entry.state!=='pending')continue;
     if(targets.has(entry.target))continue;targets.add(entry.target);
     if(this.running.has(entry.key))continue;
     const ready=Math.max(entry.dueMs,entry.leaseUntilMs??0);
     if(ready>now){nextDue=Math.min(nextDue,ready);continue;}
     // Count leases in the shared state, not this tab's private running map.
     if(occupied>=2||leasedTargets.has(entry.target))continue;
     entry.leaseOwner=this.owner;entry.leaseUntilMs=now+leaseMs;entry.attempts++;
     picks.push(clone(entry));occupied++;
    }return picks;
   });
   if(this.stopped)return;
   for(const entry of this.entries){
    if(entry.state==='quarantined')this.reject(entry.key,new UserMutationError('mutation_scope_changed',entry.key));
    if(entry.state==='confirmed'&&entry.receipt&&this.waiters.has(entry.key)&&!this.running.has(entry.key)&&!this.recovering.has(entry.key)){
     const current=this.options.scope();if(!current||!sameUserMutationScope(current,entry.scope)){this.reject(entry.key,new UserMutationError('mutation_scope_changed',entry.key));continue;}
     this.recovering.add(entry.key);const receipt=entry.receipt;
     void this.options.transport.recover(freeze(clone(entry)),receipt,new AbortController().signal).then(response=>{const live=this.options.scope();if(!live||!sameUserMutationScope(live,entry.scope))this.reject(entry.key,new UserMutationError('mutation_scope_changed',entry.key,receipt));else this.resolve(entry.key,response);},()=>this.reject(entry.key,new UserMutationError('mutation_readback_unavailable',entry.key,receipt))).finally(()=>{this.recovering.delete(entry.key);this.wake();});
    }
    if(entry.state==='review'&&!this.running.has(entry.key))this.reject(entry.key,new UserMutationError(entry.errorCode??'mutation_needs_review',entry.key,entry.receipt));
   }
   for(const entry of picked){
    if(this.stopped)return;
    const abort=new AbortController(),promise=Promise.resolve().then(()=>this.perform(freeze(entry),abort.signal)).finally(()=>{this.running.delete(entry.key);this.wake();});
    this.running.set(entry.key,{abort,promise,scope:entry.scope});void promise.catch(()=>{/* Failed storage retains the original durable entry. */});
   }
   this.wake(Math.min(nextDue-this.now(),this.running.size?leaseRenewMs:pollMs));
  }finally{this.pumping=false;}
 }
 private async update(entry:UserMutationEntry,change:(current:UserMutationEntry)=>void):Promise<boolean>{
  return this.change(state=>{const current=state.entries.find(e=>e.key===entry.key);if(current&&current.state==='pending'&&current.leaseOwner===this.owner){change(current);delete current.leaseOwner;delete current.leaseUntilMs;return true;}return false;});
 }
 private async complete(entry:UserMutationEntry,receipt:UserMutationReceipt,response?:UserMutationResponse,signal?:AbortSignal):Promise<void>{
  const review=['partial','conflict','invalid','terminal'].includes(receipt.status)||receipt.positionStatus==='policy_rejected';
  const reviewCode=receipt.positionStatus==='policy_rejected'?'final_position_rejected':'mutation_needs_review';
  const persisted=await this.update(entry,current=>{current.receipt=receipt;current.state=review?'review':'confirmed';if(!review){delete current.body;current.fingerprint='';current.headers={'Idempotency-Key':current.key};}else current.errorCode=reviewCode;});
  if(!persisted)throw new Error('mutation_lease_changed');
  const acceptedScope=this.options.scope();if(!acceptedScope||!sameUserMutationScope(acceptedScope,entry.scope)){this.reject(entry.key,new UserMutationError('mutation_scope_changed',entry.key,receipt));return;}
  if(review){let currentState:UserMutationResponse|undefined;try{currentState=await this.options.transport.recover(entry,receipt,signal??new AbortController().signal);}catch{/* The draft and receipt remain available for review. */}this.reject(entry.key,new UserMutationError(reviewCode,entry.key,receipt,currentState));return;}
  if(this.waiters.has(entry.key)){
   const scope=this.options.scope();if(!scope||!sameUserMutationScope(scope,entry.scope)){this.reject(entry.key,new UserMutationError('mutation_scope_changed',entry.key,receipt));return;}
   if(!response||response.body===undefined){try{response=await this.options.transport.recover(entry,receipt,signal??new AbortController().signal);}catch{this.reject(entry.key,new UserMutationError('mutation_readback_unavailable',entry.key,receipt));return;}}
   const currentScope=this.options.scope();if(!currentScope||!sameUserMutationScope(currentScope,entry.scope)){this.reject(entry.key,new UserMutationError('mutation_scope_changed',entry.key,receipt));return;}
   this.resolve(entry.key,response);
  }
 }
 private async perform(entry:UserMutationEntry,signal:AbortSignal):Promise<void>{
  let retryFloor=0;
  try{
   if(this.stopped)return;
   const current=this.options.scope();if(!current||!sameUserMutationScope(current,entry.scope))throw new UserMutationError('mutation_scope_changed',entry.key);
   if(entry.attempts>1||entry.requiresReentry){
    const lookup=await this.options.transport.lookup(entry,signal);if(this.stopped)return;retryFloor=userMutationRetryAfterMs(header(lookup,'retry-after'),this.now());
    if(lookup.status>=200&&lookup.status<300){await this.complete(entry,receivedUserMutationReceipt(lookup.body,entry),undefined,signal);return;}
    if(lookup.status===404&&retryFloor>0)throw new Error('mutation_lookup_pending');
    if(lookup.status!==404||errorCode(lookup)!=='receipt_unknown'){if(lookup.status>=400&&lookup.status<500&&lookup.status!==429)throw refusal(entry,lookup);throw new Error('mutation_lookup_pending');}
   }
   const scope=this.options.scope();if(!scope||!sameUserMutationScope(scope,entry.scope))throw new UserMutationError('mutation_scope_changed',entry.key);
   const response=await this.options.transport.send(entry,signal);if(this.stopped)return;retryFloor=userMutationRetryAfterMs(header(response,'retry-after'),this.now());
   if(response.status>=200&&response.status<300){
    const live=this.options.scope();if(!live||!sameUserMutationScope(live,entry.scope))throw new UserMutationError('mutation_scope_changed',entry.key);
    const lookup=await this.options.transport.lookup(entry,signal);if(this.stopped)return;retryFloor=Math.max(retryFloor,userMutationRetryAfterMs(header(lookup,'retry-after'),this.now()));
    if(lookup.status>=200&&lookup.status<300){await this.complete(entry,receivedUserMutationReceipt(lookup.body,entry),response,signal);return;}
    if(lookup.status>=400&&lookup.status<500&&lookup.status!==429&&lookup.status!==404)throw refusal(entry,lookup);
    throw new Error('mutation_receipt_pending');
   }
   if(response.status>=400&&response.status<500&&response.status!==429)throw refusal(entry,response);
   throw new Error('mutation_transport_pending');
  }catch(error){
   if(this.stopped)return;
   if(error instanceof UserMutationError&&error.code.startsWith('mutation_store_')){this.reject(entry.key,new UserMutationError(error.code,entry.key));throw error;}
   if(error instanceof UserMutationError){
    const persisted=await this.update(entry,current=>{current.state=['mutation_scope_changed','mutation_authority_changed','unauthorized'].includes(error.code)?'quarantined':'review';current.errorCode=error.code;if(error.terminalProof)current.terminalProof=error.terminalProof;});
    if(persisted){let currentState:UserMutationResponse|undefined;const scope=this.options.scope();if(this.options.transport.current&&scope&&sameUserMutationScope(scope,entry.scope))try{currentState=await this.options.transport.current(entry,signal);}catch{/* Current-state read failure does not discard the draft. */}
     this.reject(entry.key,new UserMutationError(error.code,entry.key,error.receipt,currentState,error.terminalProof,error.response));}return;
   }
   const base=[1000,2000,5000,10000,30000][Math.min(entry.attempts-1,4)],floor=Math.max(base,retryFloor),delay=Math.min(Number.MAX_SAFE_INTEGER-this.now(),floor+Math.max(1,Math.ceil(floor*(0.01+0.24*this.random()))));
   try{const persisted=await this.update(entry,current=>{current.dueMs=this.now()+delay;});if(persisted&&entry.attempts>=5)this.reject(entry.key,new UserMutationError('mutation_pending',entry.key));}catch(storeError){this.reject(entry.key,new UserMutationError('mutation_store_unavailable',entry.key));throw storeError;}
  }
 }
}
