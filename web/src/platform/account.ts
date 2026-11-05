import {BrowserAccountApi} from '@core/query/browser-account-api.ts';
export {BrowserAccountApi, issuesHostedCredentials, hostedInstallationHeaders} from '@core/query/browser-account-api.ts';
import {apiRoot, routePath} from '../../../packages/contracts/src/hosted/routes.ts';
import {parseBrowserCredentialRecord} from '@core/credential-format.ts';
import {installationHeaders} from '@core/installation.ts';
import {observeResponseClock} from '@core/server-clock.ts';
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
