import {IndexedDBUserMutationStore} from './user-mutations';
import {HttpLocalApi, type LocalSession} from '@core/index.ts';
import {RouteConnection,sessionRoute} from '@core/route-connection';
import {RouteError} from '@core/route-identity';
import {restoreNativeSession,forgetNativeSession,savedConnectionDamaged,type RememberedServer,type ServerConnectionStorage,type HostedConnectionSource,type ConnectionEnvironment} from '@core/server-connections';
import {browserAccount} from './account';
import {AppSessionStore, type AppSessionRecord, type NewServerConnection} from '@core/app-sessions.ts';
import {browserDiscoveryProvider} from './discovery-provider';
import {currentWebEnvironment} from './environment';
import {DEFAULT_INSECURE_CONNECTIONS,type ConnectionPolicy} from '@core/connection-policy.ts';
import {routeJSON} from '@core/route-identity';
import {routePath} from '../../../packages/contracts/src/routes.ts';
export {installBrowserDiscoveryProvider} from './discovery-provider';

/** Version 1 is encrypted with a non-exportable key. Version 2 is plain JSON, used where the
 * page has no `crypto.subtle` (a server's web app over plain HTTP on the LAN): HTTP is
 * first-class, the browser's per-origin storage still isolates it, and on plain HTTP anyone who
 * can read the traffic already sees these tokens in transit. */
type Vault={version:1;iv:Uint8Array;cipher:ArrayBuffer}|{version:2;plain:string};
const canEncrypt=()=>!!globalThis.crypto?.subtle;
/** Native credentials are never ordinary drafts/localStorage. The non-exportable
 * AES key and authenticated encrypted record are separate IndexedDB values. */
export class BrowserProtectedStore<RecordType> {
 constructor(private readonly databaseName='portico.server-connections.v1', private readonly maximumBytes=192000, private readonly encryptedOnly=false){}
 private requireEncryption(){if(this.encryptedOnly&&!canEncrypt())throw new RouteError('secure_context_required','This continuation requires protected browser storage.');}
 private async transaction<T>(mode:IDBTransactionMode,work:(store:IDBObjectStore,done:(v:T)=>void,failed:(error:unknown)=>void)=>void):Promise<T>{
  return new Promise((resolve,reject)=>{let db:IDBDatabase|undefined,tx:IDBTransaction|undefined,done=false,value:T;const finish=(error?:unknown)=>{if(done)return;done=true;clearTimeout(timer);db?.close();error?reject(new Error('The saved server connection could not be read or written.')):resolve(value);};const timer=setTimeout(()=>{try{tx?.abort();}catch{}finish(new Error());},5000);
   const request=indexedDB.open(this.databaseName,1);request.onupgradeneeded=()=>request.result.createObjectStore('vault');request.onerror=()=>finish(request.error);request.onblocked=()=>finish(new Error());request.onsuccess=()=>{db=request.result;if(done){db.close();return;}try{tx=db.transaction('vault',mode,mode==='readwrite'?{durability:'strict'}:undefined);if(mode==='readwrite'&&tx.durability!=='strict')throw new Error();tx.oncomplete=()=>finish();tx.onabort=()=>finish(tx?.error??new Error('The saved server transaction was aborted.')); tx.onerror=()=>finish(tx?.error??new Error('The saved server transaction failed.'));work(tx.objectStore('vault'),v=>{value=v;},error=>{try{tx?.abort();}catch{}finish(error??new Error());});}catch(e){try{tx?.abort();}catch{}finish(e);}};
  });
 }
 private exclusive<T>(work:()=>Promise<T>):Promise<T>{return navigator.locks.request(this.databaseName+':selected',{signal:AbortSignal.timeout(15000)},work);}
 private values(){return this.transaction<{key?:CryptoKey;record?:Vault}>('readonly',(store,done)=>{const key=store.get('key'),record=store.get('selected');record.onsuccess=()=>done({key:key.result,record:record.result});});}
 /** The record is there and can never be read again: the wrong shape, no key, failed
  * authentication, not a record. Waiting does not mend that, unlike a database that is busy or
  * blocked, so it is reported by its own code and `change` lets a new save take its place. */
 private damaged(){return new RouteError('invalid_saved_connection','Saved server credentials are incomplete.');}
 /** The stored key is one this vault can use: an AES-GCM secret that may encrypt and decrypt.
  * Anything else in its place can never open the record, and is replaced with it. */
 private usable(key:unknown):key is CryptoKey{
  const k=key as {type?:unknown;algorithm?:{name?:unknown};usages?:unknown}|null;
  return !!k&&typeof k==='object'&&k.type==='secret'&&k.algorithm?.name==='AES-GCM'&&Array.isArray(k.usages)&&k.usages.includes('encrypt')&&k.usages.includes('decrypt');
 }
 private async decode(values:{key?:CryptoKey;record?:Vault}):Promise<RecordType|undefined>{
  if(!values.record)return;
  if(values.record.version===2){if(this.encryptedOnly||typeof values.record.plain!=='string'||values.record.plain.length>this.maximumBytes+16)throw this.damaged();try{return JSON.parse(values.record.plain);}catch{throw this.damaged();}}
  // An encrypted record where nothing can decrypt it is as good as absent; the next save replaces it.
  if(!canEncrypt())return;
  if(!this.usable(values.key)||values.record.version!==1||!(values.record.iv instanceof Uint8Array)||values.record.iv.length!==12||!(values.record.cipher instanceof ArrayBuffer)||values.record.cipher.byteLength>this.maximumBytes+16)throw this.damaged();
  let clear:ArrayBuffer;
  // Failed authentication is the same answer every time; any other failure to decrypt is not known to be.
  try{clear=await crypto.subtle.decrypt({name:'AES-GCM',iv:new Uint8Array(values.record.iv),additionalData:new TextEncoder().encode(this.databaseName)},values.key,values.record.cipher);}catch(e){throw (e as {name?:unknown}|null)?.name==='OperationError'?this.damaged():e;}
  try{return JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(clear));}catch{throw this.damaged();}finally{new Uint8Array(clear).fill(0);}
 }
 read(){this.requireEncryption();return this.exclusive(async()=>{this.requireEncryption();return this.decode(await this.values());});}
 /** The whole read, decision and write in one database transaction. Where the page has no
  * `crypto.subtle` it has no Web Locks either (plain HTTP), so `exclusive` only orders work
  * inside this tab; the database's own transactions are what order two tabs. Nothing here needs
  * to wait on anything but the database, because the record is plain JSON in that case, so the
  * transaction stays open from the read to the write and another tab's change cannot fall between. */
 private changeInOneTransaction(update:(current:RecordType|undefined)=>RecordType|undefined,beforeCommit?:()=>void):Promise<void>{
  return this.transaction<void>('readwrite',(store,done,failed)=>{
   const read=store.get('selected');
   read.onsuccess=()=>{
    try{
     const record=read.result as Vault|undefined;
     // An encrypted record cannot be opened here, and a plain one that does not parse is damaged:
     // both read as absent, exactly as `decode` and `change` treat them.
     let current:RecordType|undefined;
     if(record?.version===2&&typeof record.plain==='string'&&record.plain.length<=this.maximumBytes+16){try{current=JSON.parse(record.plain);}catch{current=undefined;}}
     const next=update(current);
     if(next===current){done();return;}
     beforeCommit?.();
     if(!next){store.delete('selected');done();return;}
     const plain=JSON.stringify(next);if(new TextEncoder().encode(plain).length>this.maximumBytes)throw new Error('Saved server routes exceeded their limit.');
     store.put({version:2,plain} satisfies Vault,'selected');done();
    }catch(error){failed(error);}
   };
  });
 }
 change(update:(current:RecordType|undefined)=>RecordType|undefined,beforeCommit?:()=>void):Promise<void>{this.requireEncryption();return this.exclusive(async()=>{
  this.requireEncryption();
  if(!canEncrypt())return this.changeInOneTransaction(update,beforeCommit);
  const values=await this.values();
  // A damaged record is treated as absent: a save replaces it, and an update that saves nothing
  // (a cache update, forgetting a sign-in that is not there) leaves it exactly as it is.
  let current:RecordType|undefined,damaged=false;
  try{current=await this.decode(values);}catch(e){if(!savedConnectionDamaged(e))throw e;damaged=true;}
  const next=update(current);
  if(next===current)return;
  if(!next){await this.transaction<void>('readwrite',(store,done)=>{beforeCommit?.();store.delete('selected');done();});return;}
  const bytes=new TextEncoder().encode(JSON.stringify(next));if(bytes.length>this.maximumBytes)throw new Error('Saved server routes exceeded their limit.');
  let key=this.usable(values.key)?values.key:undefined;if(!key){if(values.record&&!damaged)throw new Error('The credential encryption key is missing.');key=await crypto.subtle.generateKey({name:'AES-GCM',length:256},false,['encrypt','decrypt']);}
  const iv=crypto.getRandomValues(new Uint8Array(12));let cipher:ArrayBuffer;try{cipher=await crypto.subtle.encrypt({name:'AES-GCM',iv,additionalData:new TextEncoder().encode(this.databaseName)},key,bytes);}finally{bytes.fill(0);}
  await this.transaction<void>('readwrite',(store,done)=>{beforeCommit?.();if(key!==values.key)store.put(key,'key');store.put({version:1,iv,cipher} satisfies Vault,'selected');done();});
 });}
}
let sessions:AppSessionStore|undefined;
const sessionListeners=new Set<()=>void>();let sessionChannel:BroadcastChannel|undefined;
const notifySessions=()=>{for(const listener of [...sessionListeners]){try{listener();}catch{/* A read hint cannot fail an already committed credential write. */}}};
function listenForSessionChanges(){
 if(typeof window==='undefined'||sessionChannel||typeof BroadcastChannel==='undefined')return;
 try{
  sessionChannel=new BroadcastChannel('portico.app-session-change.v1');
  sessionChannel.addEventListener('message',event=>{if(event.data==='changed')notifySessions();});
 }catch{/* Focus/pageshow still re-read storage when cross-tab messaging is unavailable. */}
}
/** A noncredential read hint only. Consumers re-read the canonical selection
 * and compare their captured scope; token rotation alone is not retirement. */
export function observeBrowserSessionChanges(changed:()=>void){
 listenForSessionChanges();sessionListeners.add(changed);
 window.addEventListener('focus',changed);window.addEventListener('pageshow',changed);
 return()=>{sessionListeners.delete(changed);window.removeEventListener('focus',changed);window.removeEventListener('pageshow',changed);};
}
export function browserAppSessions():AppSessionStore {
 if(sessions)return sessions;
 const vault=new BrowserProtectedStore<AppSessionRecord>('portico.app-sessions.v1',4*1024*1024);
 listenForSessionChanges();
 return sessions=new AppSessionStore({read:()=>vault.read(),change:async (update,beforeCommit)=>{
  let changed=false;
  await vault.change(current=>{const next=update(current);changed=next!==current;return next;},beforeCommit);
  if(changed){notifySessions();try{sessionChannel?.postMessage('changed');}catch{/* The durable write remains successful. */}}
 }});
}
let listening=false,active:RouteConnection|undefined,networkRevision=0;
/** This page's connection policy (P2-69 O-156). At web.getportico.tv every plain-HTTP route is
 * mixed content: the shared race does not send Chromium's Local Network Access target, so no
 * private-literal exemption is claimed. A server's own page already chose its transport. */
export function browserConnectionPolicy():ConnectionPolicy{
 return Object.freeze({insecure:DEFAULT_INSECURE_CONNECTIONS,platform:currentWebEnvironment().hostedWeb?'web-hosted':'web-server',webPrivateLiteralHttp:false});
}
/** The server's own answer about this connection (home or away, its policy), read without credentials. */
const readBrowserConnection=(origin:string,signal:AbortSignal)=>globalThis.fetch(origin+routePath('get_connection'),{method:'GET',redirect:'error',credentials:'omit',referrerPolicy:'no-referrer',signal,headers:{Accept:'application/json'}}).then(response=>routeJSON(response,signal,8192));
export function browserConnectionEnvironment(serverId?:string,admission?:NewServerConnection):ConnectionEnvironment {
 const discover=browserDiscoveryProvider(),store=browserAppSessions(),environment:ConnectionEnvironment={storage:serverId?store.connectionStorage(serverId,admission):store.selectedStorage(),policy:browserConnectionPolicy,readConnection:readBrowserConnection,...(discover?{discover}:{})};
 if(listening)return environment;listening=true;
 const changed=()=>active?.networkChanged('browser:'+ ++networkRevision);
 addEventListener('online',changed);addEventListener('offline',changed);
 const connection=(navigator as Navigator&{connection?:EventTarget}).connection;connection?.addEventListener('change',()=>{activeApi?.setTransportClass(linkType());changed();});
 document.addEventListener('visibilitychange',()=>active?.setForeground(!document.hidden));
 addEventListener('focus',()=>active?.foregroundActivity());
 document.addEventListener('pointerdown',()=>active?.foregroundActivity(),{passive:true});
 document.addEventListener('keydown',()=>active?.foregroundActivity());
 return environment;
}
/* A browser only knows its link on some platforms (Chromium on Android and ChromeOS); elsewhere it says nothing, which the server treats as a valid answer. */
const linkType=()=>(navigator as Navigator&{connection?:{type?:string}}).connection?.type;
let activeApi:HttpLocalApi|undefined;
export function trackBrowserConnection(api:HttpLocalApi){activeApi=api;api.setDeviceClass('web');api.setTransportClass(linkType());const next=api.getRouteConnection();if(active&&active!==next){const retired=active;setTimeout(()=>retired.dispose(),15000);}active=next;active?.setForeground(!document.hidden);}
export function browserHostedSource(accountId:string):HostedConnectionSource {
 const central=browserAccount();return {origin:central.api.origin,accountId,request:async<T>(path:string,signal:AbortSignal)=>{
  // Called only for explicit directory selection or failed/slow cached-route
  // recovery. Healthy local navigation/playback never renews Hosted authority.
  await central.service.synchronize();const session=await central.service.accessSession();if(session.account.id!==accountId)throw new Error('The saved server belongs to a different Portico Account.');return central.api.send<T>(path,'GET',undefined,signal,session.accessToken);
 }};
}
export async function restoreBrowserConnection(expected:LocalSession){
 const env=browserConnectionEnvironment(expected.viewer.serverId);
 return restoreNativeSession(env,record=>{if(!record.hosted)return;const central=browserAccount();return record.hosted.origin===central.api.origin?browserHostedSource(record.hosted.accountId):undefined;},expected);
}
export async function forgetBrowserConnection(session:LocalSession){const env=browserConnectionEnvironment(session.viewer.serverId);const captured=sessionRoute(session.accessToken);await forgetNativeSession(env,session);const retained=await env.storage.read();if(retained&&sessionRoute(retained.session.accessToken)===captured)return;if(active===captured)active=undefined;setTimeout(()=>{if(active!==captured)captured?.dispose();},16000);}

export function mountBrowserUserMutations(api: HttpLocalApi, session: LocalSession) {
 api.setUserMutationSession(session);
 return api.configureUserMutations(new IndexedDBUserMutationStore());
}
