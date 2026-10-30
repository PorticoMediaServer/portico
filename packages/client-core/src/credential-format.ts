import type {CredentialContext,CredentialRecord} from './credentials.ts';
import type {HostedSession} from './index.ts';
import {currentUTCTimestamp,utcOrderKey} from './local-session.ts';

export type BrowserCredentialAuthority=Readonly<{familyId:string}>;
const object=(value:unknown):value is Record<string,unknown>=>!!value&&typeof value==='object'&&!Array.isArray(value);
function invalid():never{throw new Error('Saved account state is not the current credential format.');}
function text(value:unknown,max=256):string{if(typeof value!=='string'||!value.length||value.length>max||/[\x00-\x1f\x7f]/.test(value))invalid();return value;}
function keys(value:Record<string,unknown>,required:readonly string[],optional:readonly string[]=[]){if(required.some(key=>!Object.hasOwn(value,key))||Object.keys(value).some(key=>!required.includes(key)&&!optional.includes(key)))invalid();}
function context(raw:unknown):CredentialContext {
 if(!object(raw))invalid();keys(raw,[],['profileId','server']);const result:CredentialContext={};
 if(raw.profileId!==undefined)result.profileId=text(raw.profileId);
 if(raw.server!==undefined){
  if(!object(raw.server))invalid();const s=raw.server;keys(s,['id','name','baseUrl','publicKey','policyRevision']);
  const baseUrl=text(s.baseUrl,4096),url=new URL(baseUrl);
  if(url.origin!==baseUrl||url.username||url.password||!(url.protocol==='https:'||(url.protocol==='http:'&&['127.0.0.1','localhost','[::1]'].includes(url.hostname)))||typeof s.policyRevision!=='number'||!Number.isSafeInteger(s.policyRevision)||s.policyRevision<0)invalid();
  result.server={id:text(s.id),name:text(s.name,2048),baseUrl,publicKey:text(s.publicKey,4096),policyRevision:s.policyRevision};
 }
 return result;
}
function session(raw:unknown,mode:'native'|'browser',stored:boolean):HostedSession {
 if(!object(raw))invalid();keys(raw,['accessToken','expiresAt','familyId','refreshExpiresAt','account'],['refreshToken']);
 const familyId=text(raw.familyId,128);
 if(!currentUTCTimestamp(raw.expiresAt)||!currentUTCTimestamp(raw.refreshExpiresAt)||utcOrderKey(raw.expiresAt)>utcOrderKey(raw.refreshExpiresAt)||!object(raw.account))invalid();
 keys(raw.account,['id','username','displayName']);
 const common={familyId,expiresAt:raw.expiresAt,refreshExpiresAt:raw.refreshExpiresAt,
  account:{id:text(raw.account.id),username:text(raw.account.username),displayName:text(raw.account.displayName,2048)}};
 if(mode==='native')return {...common,accessToken:text(raw.accessToken,16384),refreshToken:text(raw.refreshToken,16384)};
 if(raw.refreshToken!==undefined||typeof raw.accessToken!=='string'||raw.accessToken.length>16384||/[\x00-\x1f\x7f]/.test(raw.accessToken)||(stored&&raw.accessToken!==''))invalid();
 // Browser persistence intentionally contains coordination metadata, never access/refresh credentials.
 return {...common,accessToken:''};
}
function authority(raw:unknown,mode:'native'|'browser'):string|BrowserCredentialAuthority {
 if(mode==='native')return text(raw,16384);
 if(!object(raw))invalid();keys(raw,['familyId']);return{familyId:text(raw.familyId,128)};
}
function parse(raw:unknown,mode:'native'|'browser',stored:boolean):CredentialRecord<string|BrowserCredentialAuthority> {
 if(!object(raw))invalid();keys(raw,['version','revocations'],['active']);
 if(raw.version!==1||!Array.isArray(raw.revocations)||raw.revocations.length>16)invalid();
 const result:CredentialRecord<string|BrowserCredentialAuthority>={version:1,revocations:raw.revocations.map(value=>authority(value,mode))};
 if(raw.active===undefined)return result;
 if(!object(raw.active))invalid();keys(raw.active,['session','authority','context'],['pendingRequestId']);
 const activeSession=session(raw.active.session,mode,stored),activeAuthority=authority(raw.active.authority,mode);
 if(mode==='native'?activeAuthority!==activeSession.refreshToken:(activeAuthority as BrowserCredentialAuthority).familyId!==activeSession.familyId)invalid();
 const pending=raw.active.pendingRequestId;
 if(pending!==undefined&&(typeof pending!=='string'||!/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(pending)))invalid();
 result.active={session:activeSession,authority:activeAuthority,context:context(raw.active.context),...(pending===undefined?{}:{pendingRequestId:pending})};
 return result;
}
/** Refuse outdated or malformed records; never synthesize missing fields or rewrite an old schema. */
export function parseNativeCredentialRecord(raw:unknown):CredentialRecord<string>{return parse(raw,'native',true) as CredentialRecord<string>;}
export function parseBrowserCredentialRecord(raw:unknown,stored=true):CredentialRecord<BrowserCredentialAuthority>{return parse(raw,'browser',stored) as CredentialRecord<BrowserCredentialAuthority>;}

/** The native bridge reports only a missing key as null. Empty stored text is invalid. */
export function parseNativeCredentialJSON(raw:unknown):CredentialRecord<string>|undefined {
 if(raw===null)return undefined;
 if(typeof raw!=='string'||raw.length===0||raw.length>262144)invalid();
 return parseNativeCredentialRecord(JSON.parse(raw));
}
