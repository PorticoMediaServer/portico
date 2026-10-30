import {apiRoot, routePath} from '../../../packages/contracts/src/hosted/routes.ts';
import {parseBrowserCredentialRecord} from '@core/credential-format.ts';
import {installationHeaders} from '@core/installation.ts';
import {browserAccountOrigin} from './account-origin.ts';
import {browserCsrfCookieName, browserCsrfShape, exactBrowserCsrfCookie} from './account-csrf.ts';
import {parseAccountSession} from '@core/hosted-security.ts';
import {
  ApiError,
  retryAfterSecondsFrom,
  transportJSON,
  CredentialSessionService,
  type CredentialApi,
  type CredentialContext,
  type CredentialRecord,
  type CredentialStorage,
  type HostedSession,
} from '@core/index.ts';

type BrowserAuthority = {familyId:string};
type BrowserRecord = CredentialRecord<BrowserAuthority>;
type StoragePort = Pick<Storage,'getItem'|'setItem'>;
const maximumBytes = 128 * 1024;

/** Only noncredential coordination state is durable. The browser owns the HttpOnly cookie. */
export class BrowserCredentialStorage implements CredentialStorage<BrowserAuthority> {
  private storage:StoragePort;
  private key:string;
  constructor(storage:StoragePort,key:string) {this.storage=storage;this.key=key;}

  private recover(raw:string|null):BrowserRecord|undefined {
    if(raw===null)return;
    // Do not erase a record we cannot inspect within the recovery budget.
    if(raw.length>4*1024*1024)throw new Error('Saved account state is too large.');
    let value:unknown;
    try {
      value=JSON.parse(raw);
      if(raw.length>maximumBytes)throw new Error('Oversized account metadata.');
      return parseBrowserCredentialRecord(value);
    } catch {
      // Browser records contain coordination metadata, never credentials. Retire
      // an obsolete/damaged active record, keeping every valid sign-out tombstone.
      const revocations:BrowserAuthority[]=[];
      const pending=(value as {revocations?:unknown})?.revocations;
      if(Array.isArray(pending)&&pending.length>16)throw new Error('Saved account sign-outs exceeded their limit.');
      if(Array.isArray(pending))for(const item of pending) {
        try {revocations.push(...parseBrowserCredentialRecord({version:1,revocations:[item]}).revocations);} catch { /* Not a usable browser-family tombstone. */ }
      }
      return {version:1,revocations};
    }
  }
  async load():Promise<BrowserRecord|undefined> {return this.recover(this.storage.getItem(this.key));}

  /** Caller holds the same cross-tab lock as credential writes. Initial reads
   * remain read-only, so a stale document cannot erase a successor's session. */
  repair() {
    const raw=this.storage.getItem(this.key),record=this.recover(raw);
    if(raw!==null&&record&&JSON.stringify(record)!==raw)this.storage.setItem(this.key,JSON.stringify(record));
  }

  async save(record:BrowserRecord) {
    const raw=JSON.stringify(parseBrowserCredentialRecord(record,false));
    if(raw.length>maximumBytes)throw new Error('Saved account state is too large.');
    this.storage.setItem(this.key,raw);
  }


}

type BrowserAccountContext = Readonly<{callerOrigin?:string}>;

/** The only Hosted requests that create a device row: each completes a sign-in and
 * inserts a session family. Ordinary management reads and refresh/revoke carry no
 * installation claim. */
const hostedCredentialPaths: readonly RegExp[] = [
  /^\/v1\/sessions$/,
  /^\/v1\/registrations\/[A-Za-z0-9_-]{1,128}\/verify$/,
  /^\/v1\/identity\/mfa\/complete$/,
  /^\/v1\/identity\/transactions\/[A-Za-z0-9_-]{1,128}\/complete$/,
];
export function issuesHostedCredentials(path: string, method: string): boolean {
  return method.toUpperCase() === 'POST' && hostedCredentialPaths.some(pattern => pattern.test(path));
}
/** Hosted records the installation claim under its accepted header names. The shared
 * client facts use Device-Platform/App-Version, so map only the two names that differ. */
export function hostedInstallationHeaders(source: Readonly<Record<string, string>> = installationHeaders()): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [from, to] of [
    ['X-Portico-Device-Name', 'X-Portico-Device-Name'],
    ['X-Portico-Device-Platform', 'X-Portico-Platform'],
    ['X-Portico-App', 'X-Portico-App'],
    ['X-Portico-App-Version', 'X-Portico-Version'],
    ['X-Portico-Installation-Id', 'X-Portico-Installation-Id'],
  ] as const) if (source[from]) out[to] = source[from];
  return out;
}

/** Only these actual Hosted handlers consume refresh cookies through refreshInput. */
function requiresBrowserCsrf(path:string,method:string):boolean {
  return method.toUpperCase()==='POST'&&(
    path===routePath('session_refresh')||path===routePath('session_revoke')||
    path==='/v1/app-authorizations'||/^\/v1\/app-authorizations\/[A-Za-z0-9_-]{1,128}\/decision$/.test(path)
  );
}

export class BrowserAccountApi implements CredentialApi<BrowserAuthority> {
  readonly origin:string;
  private pending=new Set<Promise<unknown>>();
  private readonly csrfKey:string;
  private readonly callerOrigin:string;
  private readonly legacyCsrfKey:string;
  private legacyInvalidated=false;
  private csrfCookie:string|undefined;
  private metadataFlight:Promise<void>|undefined;
  private readonly readableCookie:boolean;
  constructor(origin:string,context:BrowserAccountContext={}) {
    const url=new URL(origin);
    if(url.protocol!=='https:'&&!(url.protocol==='http:'&&['127.0.0.1','localhost','[::1]'].includes(url.hostname)))
      throw new Error('Portico Accounts require an HTTPS service.');
    this.origin=url.origin;
    // Capture these once: later page/config changes cannot borrow another origin's token.
    const caller=new URL(context.callerOrigin??globalThis.location?.origin??this.origin);
    this.callerOrigin=caller.origin;
    this.legacyCsrfKey='portico.account.csrf.v1:'+this.origin;
    this.csrfKey='portico.account.csrf.v2:'+JSON.stringify([this.origin,this.callerOrigin]);
    try{this.csrfCookie=browserCsrfCookieName(localStorage.getItem(this.csrfKey+':name'));}catch{}
    this.readableCookie=caller.hostname===url.hostname&&caller.protocol===url.protocol;
  }

  /**
   * Hosted protects the browser refresh with a double-submit token: it sets a readable
   * `…_csrf` cookie and answers every sign-in and refresh with `X-CSRF-Token`, and a refresh must
   * send that value back. It is not a credential (it is useless without the HttpOnly refresh
   * cookie). When the web shares Hosted's host (web.getportico.tv, or a loopback dev origin) the
   * cookie itself is read; otherwise the last header value, kept in localStorage across reloads.
   */
  private sameDocumentOrigin():boolean {
    try{return globalThis.location?.origin===this.callerOrigin;}catch{return false;}
  }
  private async csrfToken(discover:boolean,signal?:AbortSignal):Promise<string|undefined> {
    if(this.readableCookie){
      if(!this.csrfCookie&&discover)await this.learnCsrfCookie();
      signal?.throwIfAborted();
      // Missing/malformed/duplicate expected cookies cannot borrow stored or foreign values.
      try{return this.csrfCookie?exactBrowserCsrfCookie(document.cookie,this.csrfCookie):undefined;}catch{return;}
    }
    try{
      const kept=localStorage.getItem(this.csrfKey);
      if(kept!==null&&(!browserCsrfShape.test(kept)||!this.legacyInvalidated))return browserCsrfShape.test(kept)?kept:undefined;
      // Initial migration requires absence; a verified legacy storage event may retire
      // an older valid v2 value. A malformed v2 record never falls back to legacy.
      // Legacy storage provenance must be the real document, never API/configuration.
      if(!this.sameDocumentOrigin())return;
      const legacy=localStorage.getItem(this.legacyCsrfKey);
      if(!legacy||!browserCsrfShape.test(legacy)||!this.sameDocumentOrigin())return;
      const current=localStorage.getItem(this.csrfKey);
      if(current!==null&&(!browserCsrfShape.test(current)||!this.legacyInvalidated))return browserCsrfShape.test(current)?current:undefined;
      if(!this.sameDocumentOrigin())return;
      localStorage.setItem(this.csrfKey,legacy);
      this.legacyInvalidated=false;
      return legacy;
    }catch{}
    return undefined;
  }
  /** Called only by browserAccount's existing page-lifetime storage listener. */
  observeLegacyCsrf(event:Pick<StorageEvent,'key'|'newValue'|'storageArea'>):void {
    if(this.readableCookie||event.key!==this.legacyCsrfKey||!event.newValue||!browserCsrfShape.test(event.newValue)||!this.sameDocumentOrigin())return;
    try{
      if(event.storageArea!==localStorage||!this.sameDocumentOrigin())return;
      const legacy=localStorage.getItem(this.legacyCsrfKey);
      if(legacy!==event.newValue||!this.sameDocumentOrigin())return;
      const current=localStorage.getItem(this.csrfKey);
      if(current!==null&&!browserCsrfShape.test(current))return;
      this.legacyInvalidated=current!==legacy;
    }catch{}
  }
  private learnCsrfCookie():Promise<void> {
    if(this.metadataFlight)return this.metadataFlight;
    // Anonymous metadata only. Never retry a credential command to discover its cookie.
    const flight=(async()=>{
      const response=await fetch(this.origin+routePath('session_refresh'),{
        method:'OPTIONS',mode:'cors',credentials:'omit',redirect:'error',cache:'no-store',signal:AbortSignal.timeout(15000),
      });
      if(!response.ok)throw new ApiError(response.status,'csrf_metadata_unavailable','Could not reach your Portico Account.',response.status>=500,retryAfterSecondsFrom(response.headers.get('Retry-After')));
      this.keepCsrf(response,false);
      if(!this.csrfCookie)throw new ApiError(503,'csrf_metadata_unavailable','Could not reach your Portico Account.',true);
    })();
    this.metadataFlight=flight;
    void flight.finally(()=>{if(this.metadataFlight===flight)this.metadataFlight=undefined;}).catch(()=>{});
    return flight;
  }
  private keepCsrf(response:Response,keepToken=true) {
    const name=browserCsrfCookieName(response.headers.get('X-Portico-CSRF-Cookie'));
    if(name){this.csrfCookie=name;try{localStorage.setItem(this.csrfKey+':name',name);}catch{}}
    const token=response.headers.get('X-CSRF-Token');
    if(keepToken&&token&&browserCsrfShape.test(token)){
      try{localStorage.setItem(this.csrfKey,token);this.legacyInvalidated=false;}catch{}
      // Older tabs still coordinate the same document/API through their original key.
      if(!this.readableCookie&&this.sameDocumentOrigin()){
        try{if(this.sameDocumentOrigin())localStorage.setItem(this.legacyCsrfKey,token);}catch{}
      }
    }
  }

  request<T>(path:string,body:unknown,signal?:AbortSignal):Promise<T> {
    return this.send<T>(path,'POST',body,signal);
  }

  send<T>(path:string,method:string,body?:unknown,signal?:AbortSignal,token?:string):Promise<T> {
    const operation=this.perform<T>(path,method,body,signal,token);
    this.pending.add(operation);
    void operation.finally(()=>this.pending.delete(operation)).catch(()=>{});
    return operation;
  }

  async settled() {
    while(this.pending.size)await Promise.allSettled([...this.pending]);
  }

  private async perform<T>(path:string,method:string,body:unknown,signal?:AbortSignal,token?:string):Promise<T> {
    if(!path.startsWith(apiRoot))throw new Error('Invalid account request.');
    // Freeze command intent before metadata can yield to another UI action.
    const payload=body===undefined?undefined:transportJSON(body);
    signal?.throwIfAborted();
    const csrf=method==='GET'?undefined:await this.csrfToken(requiresBrowserCsrf(path,method),signal);
    signal?.throwIfAborted();
    const installation=issuesHostedCredentials(path,method)?hostedInstallationHeaders():{};
    const response=await fetch(this.origin+path,{
      method,credentials:'include',redirect:'error',
      signal:signal?AbortSignal.any([signal,AbortSignal.timeout(15000)]):AbortSignal.timeout(15000),
      headers:{'Content-Type':'application/json',...installation,...(token?{Authorization:'Bearer '+token}:{}),...(csrf?{'X-CSRF-Token':csrf}:{})},...(payload===undefined?{}:{body:payload}),
    });
    this.keepCsrf(response);
    if(!response.ok){
      const payload=await response.json().catch(()=>undefined);
      throw new ApiError(response.status,payload?.error?.code??payload?.code??'request_failed',payload?.error?.message??'Could not reach your Portico Account.',payload?.error?.retryable??response.status>=500,retryAfterSecondsFrom(response.headers.get('Retry-After')));
    }
    return response.status===204?undefined as T:response.json();
  }

  authorityFromSession(session:HostedSession):BrowserAuthority {
    if(!session.familyId)throw new Error('Account renewal is not available. Sign in again.');
    if(session.refreshToken)throw new Error('The account service returned a native credential to the browser.');
    return {familyId:session.familyId};
  }
  refresh(authority:BrowserAuthority,requestId:string,signal?:AbortSignal) {
    return this.request<unknown>(routePath('session_refresh'),{familyId:authority.familyId,requestId},signal).then(value=>parseAccountSession(value,'browser'));
  }
  revoke(authority:BrowserAuthority,signal?:AbortSignal) {
    return this.request<void>(routePath('session_revoke'),{familyId:authority.familyId},signal);
  }

}

let instance:{api:BrowserAccountApi;service:CredentialSessionService<BrowserAuthority>}|undefined;
export function browserAccount() {
  if(instance)return instance;
  const api=new BrowserAccountApi(browserAccountOrigin(import.meta.env, location),{callerOrigin:location.origin});
  const key='portico.account.v1:'+api.origin;
  const storage=new BrowserCredentialStorage({getItem:name=>localStorage.getItem(name),setItem:(name,value)=>localStorage.setItem(name,value)},key);
  const service=new CredentialSessionService(api,storage,async()=>crypto.randomUUID(),15000,{
    runExclusive:operation=>{
      if(!navigator.locks)throw new Error('This browser cannot safely coordinate account sessions. Use a current browser.');
      return navigator.locks.request(key,{signal:AbortSignal.timeout(20000)},async()=>{
        try{storage.repair();return await operation();}
        finally{await api.settled();}
      });
    },
  });
  const sync=()=>{void service.synchronize().catch(()=>{});};
  addEventListener('storage',event=>{api.observeLegacyCsrf(event);if(event.key===key||event.key===null)sync();});
  // Safari can restore a frozen document without delivering its missed storage
  // events. Re-read under the coordinator lock before accepting account work.
  addEventListener('pageshow',sync);
  addEventListener('visibilitychange',()=>{if(document.visibilityState==='visible')sync();});
  addEventListener('online',()=>{void service.retryRevocations().catch(()=>{});});
  // A sign-in is a 90-day window renewed by use of the account service, which a client in daily
  // use against its own server may never need. Renew it about monthly, some minutes after
  // launch at a random moment, so it is never part of a burst.
  setTimeout(()=>{void service.keepAlive().catch(()=>{});},60000+Math.random()*540000);
  instance={api,service};
  return instance;
}
