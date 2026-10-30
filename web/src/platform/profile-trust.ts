import {parseTrustedProfile,trustScopeKey,type ProfileScope,type TrustedProfile} from '@core/profile-management';
import {BrowserProtectedStore} from './server-connections';

/** Only the non-secret installation identifier survives in localStorage. */
export function browserInstallation():string{
 try{const key='portico.profile-installation.v1';let id=localStorage.getItem(key);if(!id){id=crypto.randomUUID();localStorage.setItem(key,id);}return id;}catch{return '';}
}

/**
 * "Remember this profile" handles (a trust token that skips the profile PIN on this installation)
 * are device credentials. SEC-12: they live in IndexedDB encrypted under a non-extractable
 * AES-GCM key (`BrowserProtectedStore`), never as script-readable plaintext; no plaintext localStorage trust records are read. Reads are synchronous from a copy held in
 * memory, loaded at start (`browserTrustLoaded` before relying on one); until it has loaded, a
 * profile simply asks for its PIN.
 */
type TrustMap=Record<string,TrustedProfile>;
type Scope=ProfileScope&{serverId:string;profileId:string;installationId:string};
const MAX=64;
let cache:TrustMap={},store:BrowserProtectedStore<TrustMap>|undefined,loaded:Promise<void>|undefined;
const vault=()=>store??=new BrowserProtectedStore<TrustMap>('portico.profile-trust.v2');

/** Loads protected trust handles; no legacy plaintext records are consumed. */
export function browserTrustLoaded():Promise<void>{
 loaded??=(async()=>{try{cache={...(await vault().read())??{},...cache};}catch{/* Private storage can be refused: trust lasts only for this page. */}})();
 return loaded;
}
if(typeof indexedDB!=='undefined'&&typeof navigator!=='undefined'&&navigator.locks)void browserTrustLoaded();

function persist(update:(current:TrustMap)=>TrustMap){
 void browserTrustLoaded().then(()=>vault().change(current=>{const next=update({...current});const keys=Object.keys(next);for(const k of keys.slice(0,Math.max(0,keys.length-MAX)))delete next[k];return keys.length?next:undefined;})).catch(()=>{});
}

export function readBrowserTrust(scope:Scope):TrustedProfile|undefined{
 const key=trustScopeKey(scope),raw=cache[key];if(!raw)return undefined;
 try{return parseTrustedProfile(raw,scope);}catch{forgetBrowserTrust(scope);return undefined;}
}
export function saveBrowserTrust(proof:TrustedProfile){const key=trustScopeKey(proof);cache={...cache,[key]:proof};persist(current=>({...current,[key]:proof}));}
export function forgetBrowserTrust(scope:Scope){try{const key=trustScopeKey(scope);const {[key]:_gone,...rest}=cache;cache=rest;persist(current=>{delete current[key];return current;});}catch{}}
