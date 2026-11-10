import {unreadableServerResponse} from './server-messages.ts';
/** Device records, remembered browser accounts and the tvOS Top Shelf token. (The feed itself is read and
 * checked by the tvOS extension and the Android TV module, natively; nothing in JS parses it.)
 *
 * An installation id is generated once per install and kept. It is not a
 * fingerprint and it grants nothing, but it is the only thing standing between a
 * stranger and this installation's remembered-account list, so it must be a real
 * secret: `newInstallationId` produces 256 bits of randomness, and every parser
 * here refuses a shorter one rather than accepting a guessable value.
 *
 * The tokens themselves stay in the client. The server keeps only the account
 * descriptors and which one was used last. */
export type Device=Readonly<{id:string;installationId:string;name:string;platform:string;app:string;appVersion:string;ip:string;firstSeen:string;lastSeen:string;lastProfileId:string;rememberAccount:boolean;current:boolean;sessions:number;network?:'home'|'away'}>;
export type RememberedAccount=Readonly<{accountId:string;username:string;displayName:string;avatarVersion:number;automaticSignIn:boolean;lastUsed:string}>;

const obj=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const text=(v:unknown,max=256):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x1f\x7f]/.test(v);
const id=(v:unknown):v is string=>typeof v==='string'&&v.length>0&&v.length<=128&&/^[A-Za-z0-9_-]+$/.test(v);
const bool=(v:unknown):v is boolean=>typeof v==='boolean';
const count=(v:unknown,min=0,max=1e6):v is number=>typeof v==='number'&&Number.isInteger(v)&&v>=min&&v<=max;
const instant=(v:unknown):v is string=>typeof v==='string'&&v.length<=40&&!Number.isNaN(Date.parse(v));
const only=(v:Record<string,unknown>,keys:readonly string[]):void=>{if(Object.keys(v).some(k=>!keys.includes(k)))fail('unexpected field');};
function fail(reason:string):never{throw Object.assign(new Error(unreadableServerResponse),{code:'invalid_device_document',detail:reason,retryable:false});}

/** An installation id must be long enough to be unguessable, because the
 * remembered-account list is readable by anyone who can present one. */
export const validInstallationId=(v:unknown):v is string=>typeof v==='string'&&v.length>=32&&v.length<=128&&/^[A-Za-z0-9_-]+$/.test(v);

/** newInstallationId generates one. Callers persist it for the life of the
 * install; regenerating it orphans the remembered-account list. */
export function newInstallationId():string{
 const raw=new Uint8Array(32);
 crypto.getRandomValues(raw);
 let text='';
 for(const byte of raw)text+='ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_'[byte&63];
 return text;
}

/** Reads a device (`Device` in the generated contract). `network` is "home" or "away", where the
 * device was last used from; the server never sends an address, so `ip` is always empty. A device
 * list never names another device's installation id. `sessions` is 1 while the device is signed in. */
export function parseDevice(raw:unknown):Device{
 if(!obj(raw)||!id(raw.id))fail('device identity');
 if(!text(raw.name,120)||raw.name==='')fail('name');
 if(!instant(raw.createdAt)||!instant(raw.lastSeenAt))fail('timestamps');
 const optional=(value:unknown)=>text(value,64)?value as string:'';
 return Object.freeze({id:raw.id as string,installationId:'',name:raw.name as string,platform:optional(raw.platform),app:optional(raw.app),appVersion:optional(raw.appVersion),ip:'',firstSeen:raw.createdAt as string,lastSeen:raw.lastSeenAt as string,lastProfileId:'',rememberAccount:false,current:raw.current===true,sessions:raw.signedIn===true?1:0,
  ...(raw.network==='home'||raw.network==='away'?{network:raw.network}:{})});
}

/** Devices arrive most recently active first. A device this app cannot read is left out; at most
 * one may be `current`, or the list cannot tell the person which device they are holding. */
export function parseDevices(raw:unknown):readonly Device[]{
 if(!obj(raw)||!Array.isArray(raw.items))fail('device list');
 const devices:Device[]=[];
 for(const row of raw.items){try{devices.push(parseDevice(row));}catch{/* shown without the row it cannot read */}}
 if(new Set(devices.map(d=>d.id)).size!==devices.length)fail('duplicate device ids');
 if(devices.filter(d=>d.current).length>1)fail('more than one current device');
 return Object.freeze(devices);
}

export function parseRememberedAccounts(raw:unknown):readonly RememberedAccount[]{
 if(!obj(raw)||!Array.isArray(raw.items)||raw.items.length>10)fail('remembered accounts');
 only(raw,['items']);
 const accounts=raw.items.map(entry=>{
  if(!obj(entry))fail('remembered account');only(entry,['accountId','username','displayName','avatarVersion','avatarUrl','automaticSignIn','lastUsed']);
  if(!id(entry.accountId)||!text(entry.username,64)||entry.username===''||!text(entry.displayName,120))fail('remembered account fields');
  if(!count(entry.avatarVersion,0)||!bool(entry.automaticSignIn)||!instant(entry.lastUsed))fail('remembered account state');
  return Object.freeze({accountId:entry.accountId as string,username:entry.username as string,displayName:entry.displayName as string,avatarVersion:entry.avatarVersion as number,automaticSignIn:entry.automaticSignIn as boolean,lastUsed:entry.lastUsed as string});
 });
 if(new Set(accounts.map(a=>a.accountId)).size!==accounts.length)fail('duplicate remembered accounts');
 // Exactly one account per installation may sign in automatically; two would
 // make which one signs in a race.
 if(accounts.filter(a=>a.automaticSignIn).length>1)fail('more than one automatic sign-in');
 return Object.freeze(accounts);
}


/** What `POST /v1/topshelf/token` returns to the signed-in tvOS app: the feed
 * token it hands the shelf extension, valid for thirty days (the app renews it
 * whenever it is opened), and the feed's server-relative path. */
export type TopShelfToken=Readonly<{token:string;expiresAt:string;feedUrl:string}>;
export function parseTopShelfToken(raw:unknown):TopShelfToken{
 if(!obj(raw))fail('token');only(raw,['token','expiresAt','feedUrl']);
 if(typeof raw.token!=='string'||raw.token.length<16||raw.token.length>8192||!/^[A-Za-z0-9_.~-]+$/.test(raw.token))fail('token value');
 if(!instant(raw.expiresAt))fail('token expiry');
 if(typeof raw.feedUrl!=='string'||!raw.feedUrl.startsWith('/')||raw.feedUrl.length>256)fail('feed path');
 return Object.freeze({token:raw.token as string,expiresAt:raw.expiresAt as string,feedUrl:raw.feedUrl as string});
}

