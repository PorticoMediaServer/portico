import {routeBase64,routeBytes,routeOrigin,serverPin,webRouteCrypto,type RouteCrypto,type ServerPin} from './route-identity.ts';
import {currentUTCTimestamp} from './local-session.ts';
import {CodedError} from './server-messages.ts';

export type SecuritySigned=Readonly<{payload:string;signature:string}>;
export type CredentialSecurityPurpose='password_set_or_change'|'email_change'|'mfa_enroll'|'mfa_disable'|'recovery_codes';
export type SecurityPrepare=Readonly<{purpose:CredentialSecurityPurpose;target:string;origin:string;openingId:string;taskId:string;operationId:string;providerIdentityId:string;providerRevision:string;nonce:string;expiresAt:string}>;
const fields=['settingsScope','settingsSection','purpose','target','kind','serverId','publicKey','registrationId','origin','hostedOrigin','claimOperationId','accountId','actorBinding','openingId','taskId','operationId','providerIdentityId','providerRevision','nonce','issuedAt','expiresAt','authorityExpiresAt'] as const;
export type SecurityContextFacts=Readonly<Record<typeof fields[number],string>>;
const invalid=()=>new CodedError('invalid_security_return','This account security continuation no longer matches its original opening.');
const object=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v)&&[Object.prototype,null].includes(Object.getPrototypeOf(v));
const identifier=(v:unknown,min=1)=>typeof v==='string'&&v.length>=min&&v.length<=128&&/^[A-Za-z0-9_-]+$/.test(v);
const revision=(v:unknown)=>typeof v==='string'&&/^[1-9][0-9]{0,18}$/.test(v)&&BigInt(v)<=9223372036854775807n;
const utc=(v:string)=>currentUTCTimestamp(v)&&/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{0,8}[1-9])?Z$/.test(v);
const goJSON=(v:unknown)=>JSON.stringify(v).replace(/[<>&\u2028\u2029]/g,c=>'\\u'+c.charCodeAt(0).toString(16).padStart(4,'0'));

export function parseSecuritySigned(raw:unknown):SecuritySigned{
 if(!object(raw)||Object.keys(raw).sort().join(',')!=='payload,signature'||typeof raw.payload!=='string'||typeof raw.signature!=='string')throw invalid();
 routeBytes(raw.payload,undefined,6144);routeBytes(raw.signature,64);
 return Object.freeze({payload:raw.payload,signature:raw.signature});
}

/** This distinct signed kind is display/continuation authority only. It is
 * never parsed as a sign-in assertion or passed to consumeSameTabReturn. */
export async function verifySecurityContext(raw:unknown,pin:ServerPin,expected:{prepare:SecurityPrepare;hostedOrigin:string;accountId:string},options:{now?:number;crypto?:RouteCrypto}={}):Promise<SecurityContextFacts>{
 const signed=parseSecuritySigned(raw),bytes=routeBytes(signed.payload,undefined,6144),crypto=options.crypto??webRouteCrypto,now=options.now??Date.now();
 let value:unknown;try{value=JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(bytes));}catch{throw invalid();}
 if(!Number.isSafeInteger(now)||!object(value)||Object.keys(value).join(',')!==fields.join(',')||fields.some(key=>typeof value[key]!=='string')||goJSON(value)!==new TextDecoder().decode(bytes))throw invalid();
 const c=value as Record<typeof fields[number],string>,q=expected.prepare;
 if(c.settingsScope!=='account'||c.settingsSection!=='profile'||c.purpose!==q.purpose||c.target!==q.target||!['password_set_or_change','email_change','mfa_enroll','mfa_disable','recovery_codes'].includes(c.purpose)||(c.purpose==='email_change'?!c.target||c.target.length>320:c.target!=='')||c.kind!=='portico.security-return-prepare.v1'||c.serverId!==pin.serverId||c.publicKey!==pin.publicKey||!/^ret_[A-Za-z0-9_-]{43}$/.test(c.registrationId)||!identifier(c.claimOperationId)||c.accountId!==expected.accountId||
  c.origin!==q.origin||c.hostedOrigin!==expected.hostedOrigin||c.openingId!==q.openingId||c.taskId!==q.taskId||c.operationId!==q.operationId||c.providerIdentityId!==q.providerIdentityId||c.providerRevision!==q.providerRevision||c.nonce!==q.nonce||
  !identifier(c.openingId,16)||!identifier(c.taskId,16)||!identifier(c.operationId,16)||!identifier(c.providerIdentityId)||!revision(c.providerRevision)||!utc(c.issuedAt)||!utc(c.expiresAt)||!utc(c.authorityExpiresAt)||!Number.isFinite(Date.parse(q.expiresAt))||Date.parse(c.expiresAt)>Date.parse(q.expiresAt)||Date.parse(c.expiresAt)>Date.parse(c.authorityExpiresAt))throw invalid();
 for(const origin of [c.origin,c.hostedOrigin])if(!origin.startsWith('https://')||routeOrigin(origin)!==origin)throw invalid();
 routeBytes(c.actorBinding,32);routeBytes(c.nonce,32);routeBytes(c.registrationId.slice(4),32);
 const issued=Date.parse(c.issuedAt),expires=Date.parse(c.expiresAt);
 if(issued>now+60000||expires<=now||expires<=issued||expires-issued>300000)throw invalid();
 await serverPin(pin.serverId,pin.publicKey,pin.fingerprint,crypto);
 if(!await crypto.verify(routeBytes(pin.publicKey,32),routeBytes(signed.signature,64),bytes))throw invalid();
 return Object.freeze({...c});
}

export function parseSecurityReturnIntent(raw:unknown,expiresAt:string):Readonly<{intentId:string;expiresAt:string}>{
 if(!object(raw)||Object.keys(raw).sort().join(',')!=='expiresAt,intentId'||typeof raw.intentId!=='string'||!/^sri_[A-Za-z0-9_-]{43}$/.test(raw.intentId)||typeof raw.expiresAt!=='string'||!utc(raw.expiresAt)||Date.parse(raw.expiresAt)!==Date.parse(expiresAt)||Date.parse(raw.expiresAt)<=Date.now())throw invalid();
 return Object.freeze({intentId:raw.intentId,expiresAt:raw.expiresAt});
}

export function securityReturnEntry(origin:string,intentId:string):string{
 if(!origin.startsWith('https://')||routeOrigin(origin)!==origin||!/^sri_[A-Za-z0-9_-]{43}$/.test(intentId))throw invalid();
 return origin+'/account?securityIntent='+encodeURIComponent(intentId);
}

export type SecurityReturnCode=Readonly<{code:string;state:string;transactionId:string}>;
/** Only an opaque reference crosses the registered callback. Synchronous URL
 * scrubbing belongs to the platform intake before protected storage/network. */
export function parseSecurityReturnCode(href:string,original:{origin:string;state:string}):SecurityReturnCode{
 if(!original.origin.startsWith('https://')||routeOrigin(original.origin)!==original.origin)throw invalid();routeBytes(original.state,32);
 const url=new URL(href),values=new URLSearchParams(url.hash.slice(1));
 if(url.origin!==original.origin||url.pathname!=='/auth/portico-return'||url.search||url.username||url.password||!href.startsWith(original.origin+'/auth/portico-return#')||url.hash.length>2048||
  [...values.keys()].sort().join(',')!=='security,state,transaction'||['security','state','transaction'].some(key=>values.getAll(key).length!==1)||values.get('state')!==original.state||!/^scr_[A-Za-z0-9_-]{43}$/.test(values.get('security')??'')||!/^oid_[A-Za-z0-9_-]{43}$/.test(values.get('transaction')??''))throw invalid();
 const canonical=new URLSearchParams([['security',values.get('security')!],['state',original.state],['transaction',values.get('transaction')!]]).toString();
 if(url.hash!=='#'+canonical)throw invalid();
 return Object.freeze({code:values.get('security')!,state:original.state,transactionId:values.get('transaction')!});
}

export async function verifySecurityConfirmation(raw:unknown,pin:ServerPin,context:SecuritySigned,returned:SecurityReturnCode,options:{now?:number;crypto?:RouteCrypto}={}):Promise<SecuritySigned>{
 const signed=parseSecuritySigned(raw),bytes=routeBytes(signed.payload,undefined,6144),crypto=options.crypto??webRouteCrypto,now=options.now??Date.now();
 let value:unknown;try{value=JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(bytes));}catch{throw invalid();}
 if(!object(value)||Object.keys(value).join(',')!=='kind,contextDigest,transactionId,codeDigest,issuedAt,expiresAt'||goJSON(value)!==new TextDecoder().decode(bytes)||value.kind!=='portico.security-return-confirm.v1'||value.transactionId!==returned.transactionId||typeof value.issuedAt!=='string'||typeof value.expiresAt!=='string'||!utc(value.issuedAt)||!utc(value.expiresAt))throw invalid();
 const original=parseSecuritySigned(context),originalBytes=routeBytes(original.payload,undefined,6144);
 let source:unknown;try{source=JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(originalBytes));}catch{throw invalid();}
 if(!object(source)||source.kind!=='portico.security-return-prepare.v1'||source.settingsScope!=='account'||source.settingsSection!=='profile'||typeof source.expiresAt!=='string'||typeof source.authorityExpiresAt!=='string'||!utc(source.expiresAt)||!utc(source.authorityExpiresAt))throw invalid();
 const issued=Date.parse(value.issuedAt),expires=Date.parse(value.expiresAt);
 if(expires>Date.parse(source.expiresAt)||expires>Date.parse(source.authorityExpiresAt)||!await crypto.verify(routeBytes(pin.publicKey,32),routeBytes(original.signature,64),originalBytes))throw invalid();
 if(!Number.isSafeInteger(now)||issued>now+60000||expires<=now||expires<=issued||expires-issued>60000||value.contextDigest!==routeBase64(await crypto.sha256(routeBytes(context.payload,undefined,6144)))||value.codeDigest!==routeBase64(await crypto.sha256(new TextEncoder().encode(returned.code))))throw invalid();
 if(!await crypto.verify(routeBytes(pin.publicKey,32),routeBytes(signed.signature,64),bytes))throw invalid();
 return signed;
}
