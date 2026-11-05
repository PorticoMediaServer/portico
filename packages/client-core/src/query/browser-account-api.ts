import {apiRoot, routePath} from '../../../contracts/src/hosted/routes.ts';
import {installationHeaders} from '../installation.ts';
import {observeResponseClock} from '../server-clock.ts';
import {browserCsrfCookieName, browserCsrfShape, exactBrowserCsrfCookie} from '../browser-csrf.ts';
import {parseAccountSession} from '../hosted-security.ts';
import {ApiError, retryAfterSecondsFromHeaders, transportJSON, type CredentialApi, type HostedSession} from '../index.ts';
export type BrowserAuthority = {familyId:string};

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
    path===routePath('app_authorization_review')||/^\/v1\/app-authorizations\/[A-Za-z0-9_-]{1,128}\/decision$/.test(path)
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
      const sentAt=Date.now();
      const response=await fetch(this.origin+routePath('session_refresh'),{
        method:'OPTIONS',mode:'cors',credentials:'omit',redirect:'error',cache:'no-store',signal:AbortSignal.timeout(15000),
      });
      observeResponseClock(this.origin,response,sentAt);
      if(!response.ok)throw new ApiError(response.status,'csrf_metadata_unavailable','Could not reach your Portico Account.',response.status>=500,retryAfterSecondsFromHeaders(response.headers));
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
    const sentAt=Date.now();
    const response=await fetch(this.origin+path,{
      method,credentials:'include',redirect:'error',
      signal:signal?AbortSignal.any([signal,AbortSignal.timeout(15000)]):AbortSignal.timeout(15000),
      headers:{'Content-Type':'application/json',...installation,...(token?{Authorization:'Bearer '+token}:{}),...(csrf?{'X-CSRF-Token':csrf}:{})},...(payload===undefined?{}:{body:payload}),
    });
    observeResponseClock(this.origin,response,sentAt);
    this.keepCsrf(response);
    if(!response.ok){
      const payload=await response.json().catch(()=>undefined);
      throw new ApiError(response.status,payload?.error?.code??payload?.code??'request_failed',payload?.error?.message??'Could not reach your Portico Account.',payload?.error?.retryable??response.status>=500,retryAfterSecondsFromHeaders(response.headers));
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
