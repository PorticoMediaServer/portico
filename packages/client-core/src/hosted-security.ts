import {routeBody as hostedRouteBody} from "../../contracts/src/hosted/routes.ts";
import {routePath as hostedRoutePath, encodedRoutePath as hostedEncodedRoutePath, routePrefix as hostedRoutePrefix} from "../../contracts/src/hosted/routes.ts";
import {parseSecuritySigned,parseSecurityReturnIntent,parseSecurityReturnCode,type SecuritySigned} from './security-return.ts';
import {routeOrigin,routeBytes} from './route-identity.ts';
import {ApiError, type HostedSession} from './index.ts';

export type IdentityProvider = 'google' | 'apple';
export type AccountMode = 'browser' | 'native';
/** `nativeCompletion` (SEC-01): the service returns a native provider sign-in with a completion code
 * when the start asks for it. Only a service that says so is asked: an older one refuses unknown
 * start fields. */
export type IdentityOptions = {credentialProviderReauthentication?:'account-settings-v1';password:boolean;registration:boolean;recovery:boolean;accountSecurity:boolean;verifiedContactRequired:boolean;nativeCompletion?:'code';providers:{id:IdentityProvider;enabled:boolean;native:boolean;nativeClientIds:string[]}[]};
export type IdentityTransaction = {transactionId:string;provider:IdentityProvider;mode:'login'|'reauthenticate'|'link';status:'started'|'exchanging'|'onboarding'|'contact_pending'|'verified'|'completed'|'proof_issued'|'failed';expiresAt:string;contactVerified:boolean;needsUsername:boolean;onboardingToken?:string;suggestedName?:string;verifiedEmail?:string;mfaRequired:boolean;securityReturn?:true};
export type Registration = {kind:'registration';registrationId:string;status:'contact_pending'|'verified'|'completed';revision:string;maskedAddress:string;expiresAt:string;contactVerified:boolean};
export type MFAChallenge = {mfaRequired:true;challengeId:string;challengeToken:string;expiresAt:string};
export type ContactChallenge = {challengeId:string;maskedAddress:string;expiresAt:string};
export type SecurityPurpose = 'password_set_or_change'|'account_deletion_preview'|'email_change'|'provider_link'|'provider_unlink'|'mfa_enroll'|'mfa_disable'|'recovery_codes'|'server_transfer';
export type SecurityIntent = {purpose:SecurityPurpose;target:string;operationId:string};
/** A80 (Justin's rule): only credential changes (password, email, second factor) need a fresh proof;
 * account management (server removal and transfer, provider link and unlink, the deletion preview,
 * export) needs the signed-in session only. The final deletion is confirmed by typing the email. */
export function securityPurposeNeedsProof(purpose:SecurityPurpose):boolean{return purpose==='password_set_or_change'||purpose==='email_change'||purpose==='mfa_enroll'||purpose==='mfa_disable'||purpose==='recovery_codes';}
export type AccountScope = {accountId:string;familyId:string};
export type RecentProof = AccountScope & {proof:string;expiresAt:string};
export type SecurityState = AccountScope & {contactAddress:string;privateEmail:boolean;hasPassword:boolean;mfaEnabled:boolean;recoveryCodesRemaining:number;providers:{id:string;provider:IdentityProvider;revision?:string}[]};
export type DeletionPreview = {deletionToken:string;expiresAt:string;ownedServers:{id:string;name:string}[];affectedServers:{id:string;name:string}[];canDelete:boolean};
export type SecurityResult = AccountScope & {status:string;signOut?:boolean;challengeId?:string;maskedAddress?:string;resendsRemaining?:number;expiresAt?:string;secret?:string;otpAuthUri?:string;recoveryCodes?:string[];deletionPreview?:DeletionPreview};
export type OIDCStart = {transactionId:string;expiresAt:string;mode:'login'|'reauthenticate'|'link';authorizationUrl?:string;nativeNonce?:string};
export type HostedSecurityReturnStart = OIDCStart & {provider:IdentityProvider;intentId:string;registeredOrigin:string;state:string;context:SecuritySigned};
export type SecurityCompletion = {recentProof?:RecentProof;securityResult?:SecurityResult};
/** `completion` is a native provider return's completion code (SEC-01), sent as `X-Portico-Completion`. */
export type AccountTransport = (path:string,method:string,body:unknown,signal?:AbortSignal,token?:string,completion?:string)=>Promise<unknown>;

const object=(v:unknown):v is Record<string,unknown>=>v!==null&&typeof v==='object'&&!Array.isArray(v);
const invalid=():never=>{throw new Error('The account service returned an invalid response. Start this operation again.');};
const text=(v:unknown,max=256):string=>{if(typeof v!=='string'||!v.length||v.length>max||/[\x00-\x1f\x7f]/.test(v))return invalid();return v;};
const identifier=(v:unknown):string=>{const s=text(v,160);if(!/^[A-Za-z0-9_-]+$/.test(s))return invalid();return s;};
const timestamp=(v:unknown):string=>{const s=text(v,64);if(!/^\d{4}-\d\d-\d\dT.*Z$/.test(s)||!Number.isFinite(Date.parse(s)))return invalid();return s;};
const boolean=(v:unknown):boolean=>typeof v==='boolean'?v:invalid();
const provider=(v:unknown):IdentityProvider=>v==='google'||v==='apple'?v:invalid();
const scoped=(v:Record<string,unknown>):AccountScope=>({accountId:identifier(v.accountId),familyId:identifier(v.familyId)});
const pathID=(v:string)=>encodeURIComponent(identifier(v));
export const sameAccountScope=(a:AccountScope,b:AccountScope)=>a.accountId===b.accountId&&a.familyId===b.familyId;

export function parseIdentityOptions(v:unknown):IdentityOptions {
 if(!object(v)||!Array.isArray(v.providers)||v.providers.length>2)return invalid();
 const providers=v.providers.map(p=>{if(!object(p))return invalid();return{id:provider(p.id),enabled:boolean(p.enabled),native:p.native===true,nativeClientIds:Array.isArray(p.nativeClientIds)&&p.nativeClientIds.length<=8?p.nativeClientIds.map(x=>text(x,255)):[]};});
 if(new Set(providers.map(p=>p.id)).size!==providers.length)return invalid();
 return{...(v.credentialProviderReauthentication==='account-settings-v1'?{credentialProviderReauthentication:'account-settings-v1' as const}:{}),password:boolean(v.password),registration:v.registration===true,recovery:v.recovery===true,accountSecurity:v.accountSecurity===true,verifiedContactRequired:boolean(v.verifiedContactRequired),...(v.nativeCompletion==='code'?{nativeCompletion:'code' as const}:{}),providers};
}
export function parseIdentityTransaction(v:unknown,expected?:string):IdentityTransaction {
 if(!object(v))return invalid();const transactionId=identifier(v.transactionId);
 if(expected&&transactionId!==expected||!['login','reauthenticate','link'].includes(String(v.mode))||!['started','exchanging','onboarding','contact_pending','verified','completed','proof_issued','failed'].includes(String(v.status)))return invalid();
 if(v.securityReturn!==undefined&&(typeof v.securityReturn!=='boolean'||v.securityReturn&&v.mode!=='reauthenticate'))return invalid();
 const contactVerified=boolean(v.contactVerified);
 // This optional fact is the current transaction's verified contact, never the account's
 // primary address. Older services and privacy-gated transaction views omit it entirely.
 if(v.verifiedEmail!==undefined&&(!contactVerified||!['onboarding','verified','completed','proof_issued'].includes(String(v.status))))return invalid();
 return{transactionId,provider:provider(v.provider),mode:v.mode as IdentityTransaction['mode'],status:v.status as IdentityTransaction['status'],expiresAt:timestamp(v.expiresAt),contactVerified,needsUsername:boolean(v.needsUsername),mfaRequired:v.mfaRequired===true,...(v.securityReturn===true&&v.mode==='reauthenticate'?{securityReturn:true as const}:{}),...(v.onboardingToken?{onboardingToken:text(v.onboardingToken,256)}:{}),...(v.suggestedName?{suggestedName:text(v.suggestedName,120)}:{}),...(v.verifiedEmail!==undefined?{verifiedEmail:text(v.verifiedEmail,320)}:{})};
}
export function parseRegistration(v:unknown):Registration {
 if(!object(v)||v.kind!=='registration'||!['contact_pending','verified','completed'].includes(String(v.status))||typeof v.revision!=='string'||!/^\d+$/.test(v.revision))return invalid();
 return{kind:'registration',registrationId:identifier(v.registrationId),status:v.status as Registration['status'],revision:v.revision,maskedAddress:text(v.maskedAddress,320),expiresAt:timestamp(v.expiresAt),contactVerified:boolean(v.contactVerified)};
}
export function parseContactChallenge(v:unknown):ContactChallenge {if(!object(v))return invalid();return{challengeId:identifier(v.challengeId),maskedAddress:text(v.maskedAddress,320),expiresAt:timestamp(v.expiresAt)};}
export function parseMFAChallenge(v:unknown):MFAChallenge {if(!object(v)||v.mfaRequired!==true)return invalid();return{mfaRequired:true,challengeId:identifier(v.challengeId),challengeToken:text(v.challengeToken,256),expiresAt:timestamp(v.expiresAt)};}
export class MFARequiredError extends Error {readonly challenge:MFAChallenge;constructor(challenge:MFAChallenge){super('Enter an authenticator code or a recovery code to finish signing in.');this.name='MFARequiredError';this.challenge=challenge;}}
/** Runtime responses keep the access token; browser persistence separately strips it. */
export function parseAccountSession(v:unknown,mode:AccountMode):HostedSession {
 if(object(v)&&v.mfaRequired===true)throw new MFARequiredError(parseMFAChallenge(v));
 if(!object(v)||!object(v.account)||Object.keys(v).some(key=>!['account','accessToken','expiresAt','familyId','refreshExpiresAt','refreshToken'].includes(key)))return invalid();
 if(mode==='browser'&&Object.hasOwn(v,'refreshToken'))return invalid();
 const expiresAt=timestamp(v.expiresAt),refreshExpiresAt=timestamp(v.refreshExpiresAt);
 if(Date.parse(expiresAt)>Date.parse(refreshExpiresAt))return invalid();
 return{account:{id:identifier(v.account.id),username:text(v.account.username,32),displayName:text(v.account.displayName,2048)},accessToken:text(v.accessToken,16384),familyId:identifier(v.familyId),expiresAt,refreshExpiresAt,...(mode==='native'?{refreshToken:text(v.refreshToken,16384)}:{})};
}
export function parseRecentProof(v:unknown,expected?:AccountScope):RecentProof {if(!object(v))return invalid();const scope=scoped(v);if(expected&&!sameAccountScope(scope,expected))return invalid();return{...scope,proof:text(v.proof,256),expiresAt:timestamp(v.expiresAt)};}
export function parseSecurityState(v:unknown,expected:AccountScope):SecurityState {
 if(!object(v)||!Array.isArray(v.providers)||v.providers.length>10||!Number.isSafeInteger(v.recoveryCodesRemaining)||Number(v.recoveryCodesRemaining)<0||Number(v.recoveryCodesRemaining)>10)return invalid();
 const scope=scoped(v);if(!sameAccountScope(scope,expected))return invalid();
 return{...scope,contactAddress:typeof v.contactAddress==='string'&&v.contactAddress===''?'':text(v.contactAddress,320),privateEmail:boolean(v.privateEmail),hasPassword:boolean(v.hasPassword),mfaEnabled:boolean(v.mfaEnabled),recoveryCodesRemaining:Number(v.recoveryCodesRemaining),providers:v.providers.map(p=>{if(!object(p))return invalid();const revision=p.revision;if(revision!==undefined&&(typeof revision!=='string'||!/^[1-9][0-9]{0,18}$/.test(revision)||BigInt(revision)<1n||BigInt(revision)>9223372036854775807n))return invalid();return{id:identifier(p.id),provider:provider(p.provider),...(typeof revision==='string'?{revision}:{})};})};
}
export function parseSecurityResult(v:unknown,expected:AccountScope):SecurityResult {
 if(!object(v))return invalid();const scope=scoped(v);if(!sameAccountScope(scope,expected))return invalid();
 const result:SecurityResult={...scope,status:text(v.status,80),signOut:v.signOut===true};
 for(const k of ['challengeId','secret','otpAuthUri'] as const)if(v[k]!==undefined)result[k]=text(v[k],2048);
 if(result.otpAuthUri){const uri=new URL(result.otpAuthUri);if(uri.protocol!=='otpauth:'||uri.hostname!=='totp'||uri.username||uri.password||uri.hash)return invalid();}
 if(v.expiresAt!==undefined)result.expiresAt=timestamp(v.expiresAt);
 if(v.maskedAddress!==undefined)result.maskedAddress=text(v.maskedAddress,320);
 if(v.resendsRemaining!==undefined){if(!Number.isSafeInteger(v.resendsRemaining)||Number(v.resendsRemaining)<0||Number(v.resendsRemaining)>5)return invalid();result.resendsRemaining=Number(v.resendsRemaining);}
 if(v.recoveryCodes!==undefined){if(!Array.isArray(v.recoveryCodes)||v.recoveryCodes.length!==10)return invalid();result.recoveryCodes=v.recoveryCodes.map(c=>text(c,80));}
 // Deletion's existing public projection is validated before rendering or accepting it.
 if(v.deletionPreview!==undefined)result.deletionPreview=parseDeletionPreview(v.deletionPreview);
 return result;
}
export type DeletionReceipt = {accountDeleted:boolean;deletionId:string;status:string;pendingServers:string[]};
export function parseDeletionReceipt(v:unknown):DeletionReceipt {
 if(!object(v)||typeof v.accountDeleted!=='boolean'||typeof v.status!=='string'||v.status.length>64)return invalid();
 const pending=Array.isArray(v.pendingServers)?v.pendingServers.slice(0,1000).filter((x):x is string=>typeof x==='string'&&x.length<=160):[];
 return{accountDeleted:v.accountDeleted,deletionId:typeof v.deletionId==='string'?v.deletionId.slice(0,160):'',status:v.status,pendingServers:pending};
}
export function parseDeletionPreview(v:unknown):DeletionPreview {
 if(!object(v))return invalid();
 const servers=(raw:unknown)=>{if(!Array.isArray(raw)||raw.length>10000)return invalid();return raw.map(s=>{if(!object(s))return invalid();return{id:identifier(s.id),name:text(s.name,2048)};});};
 if(Object.hasOwn(v,'profileCount')||Object.hasOwn(v,'requiredDisposition'))return invalid();
 return{deletionToken:text(v.deletionToken,256),expiresAt:timestamp(v.expiresAt),ownedServers:servers(v.ownedServers),affectedServers:servers(v.affectedServers),canDelete:boolean(v.canDelete)};
}
export function providerRedirect(v:unknown,p:IdentityProvider):string {
 if(!object(v))return invalid();identifier(v.transactionId);timestamp(v.expiresAt);const url=new URL(text(v.authorizationUrl,8192));
 if(url.protocol!=='https:'||url.username||url.password||url.port||url.hostname!==(p==='google'?'accounts.google.com':'appleid.apple.com'))return invalid();return url.href;
}
export function parseOIDCStart(v:unknown,p:IdentityProvider,nativeApple=false):OIDCStart {
 if(!object(v)||!['login','reauthenticate','link'].includes(String(v.mode)))return invalid();
 return{transactionId:identifier(v.transactionId),expiresAt:timestamp(v.expiresAt),mode:v.mode as OIDCStart['mode'],...(nativeApple?{nativeNonce:text(v.nativeNonce,256)}:{authorizationUrl:providerRedirect(v,p)})};
}
/** A provider sign-in's return to a native app (SEC-01 / BE-HOSTED-02): the transaction it
 * finished and, from a current Portico Account service, the completion code that lets this app
 * (and only the holder of this sign-in's private binding) see the onboarding details. */
export type AccountReturn = Readonly<{transactionId:string;completionCode?:string}>;
const completionCode=/^[A-Za-z0-9_-]{43}$/;
/**
 * The return shapes, strictly (anything else is refused):
 * - `portico://account-return?transactionId=<id>`: an older service (no code);
 * - `portico://account-return?transactionId=<id>&code=<code>`: a current service's return,
 *   continued by the account-return page into the app's sign-in browser;
 * - `<account origin>/app/account-return#tx=<id>&code=<code>`: a current service's return, as
 *   an app link (only for the Portico Account origin this sign-in started at).
 * The transaction must be the one this sign-in started. The private binding never enters a URL.
 */
export function parseAccountReturn(raw:string,expected:string,accountOrigin?:string):AccountReturn {
 if(raw.length>1024)return invalid();const u=new URL(raw);
 if(u.username||u.password)return invalid();
 if(u.protocol==='portico:'){
  const keys=[...u.searchParams.keys()];
  if(u.hostname!=='account-return'||u.pathname!==''||u.port||u.hash||!(keys.length===1&&keys[0]==='transactionId'||keys.length===2&&keys[0]==='transactionId'&&keys[1]==='code'))return invalid();
  if(u.searchParams.get('transactionId')!==expected)return invalid();
  const code=u.searchParams.get('code');
  if(code!==null&&!completionCode.test(code))return invalid();
  return{transactionId:identifier(expected),...(code!==null?{completionCode:code}:{})};
 }
 if(!accountOrigin||u.origin!==new URL(accountOrigin).origin||u.pathname!=='/app/account-return'||u.search)return invalid();
 const fragment=new URLSearchParams(u.hash.replace(/^#/,''));const keys=[...fragment.keys()].sort();
 if(keys.length!==2||keys[0]!=='code'||keys[1]!=='tx'||fragment.get('tx')!==expected||!completionCode.test(fragment.get('code')??''))return invalid();
 return{transactionId:identifier(expected),completionCode:fragment.get('code')!};
}
/** The transaction id only (older callers). */
export function nativeAccountReturn(raw:string,expected:string):string {
 return parseAccountReturn(raw,expected).transactionId;
}
/**
 * The account-return page (web, `/app/account-return` on the Portico Account origin): a current
 * service sends a native sign-in's return there. In the app's sign-in browser the page continues
 * to the app's own return, which only that browser session receives. Undefined for anything that
 * isn't a well-formed return (the page then says so and continues nowhere).
 */
export function accountReturnHandoff(href:string):string|undefined {
 try{
  const u=new URL(href);if(u.pathname!=='/app/account-return'||u.search||href.length>1024)return;
  const fragment=new URLSearchParams(u.hash.replace(/^#/,''));const keys=[...fragment.keys()].sort();
  const tx=fragment.get('tx')??'',code=fragment.get('code')??'';
  if(keys.length!==2||keys[0]!=='code'||keys[1]!=='tx'||!/^[A-Za-z0-9_-]{1,160}$/.test(tx)||!completionCode.test(code))return;
  return 'portico://account-return?'+new URLSearchParams({transactionId:tx,code}).toString();
 }catch{return;}
}

export class HostedAccountClient {
 readonly mode:AccountMode;private transport:AccountTransport;
 constructor(origin:string,mode:AccountMode,transport?:AccountTransport,binding?:string){
  const url=new URL(origin);if(url.origin!==origin||url.username||url.password||!(url.protocol==='https:'||(url.protocol==='http:'&&['127.0.0.1','localhost','[::1]'].includes(url.hostname))))throw new Error('Portico Accounts require an HTTPS service.');
  if(mode==='native'&&(!binding||!/^[A-Za-z0-9_-]{43}$/.test(binding)))throw new Error('A private native continuation is required.');
  this.mode=mode;
  this.transport=transport??(async(path,method,body,signal,token,completion)=>{
   const controller=new AbortController(),abort=()=>controller.abort();signal?.addEventListener('abort',abort,{once:true});if(signal?.aborted)abort();const timer=setTimeout(abort,15000);
   try{const response=await fetch(origin+path,{method,credentials:mode==='browser'?'include':'omit',signal:controller.signal,headers:{'Content-Type':'application/json',...(binding?{'X-Portico-Continuation':binding}:{}),...(token?{Authorization:'Bearer '+token}:{}),...(completion?{'X-Portico-Completion':completion}:{})},...(body===undefined?{}:{body:JSON.stringify(body)})});
    const raw=await response.text();if(raw.length>262144)throw new Error('The account response is too large.');let value:unknown;try{value=raw?JSON.parse(raw):undefined;}catch{throw new Error('The account service returned unreadable data.');}
    if(!response.ok){const e=object(value)&&object(value.error)?value.error:{};throw new ApiError(response.status,typeof e.code==='string'?e.code:'request_failed',typeof e.message==='string'?e.message:'The account request did not finish.',e.retryable===true||response.status>=500);}return value;
   }finally{clearTimeout(timer);signal?.removeEventListener('abort',abort);}
  });
 }
 request(path:string,method='GET',body?:unknown,signal?:AbortSignal,token?:string,completion?:string){if(!/^\/v1\/[A-Za-z0-9_/-]+$/.test(path))throw new Error('Invalid account request.');if(completion!==undefined&&!completionCode.test(completion))throw new Error('Invalid sign-in completion.');return this.transport(path,method,body,signal,token,completion);}
 async options(signal?:AbortSignal){return parseIdentityOptions(await this.request(hostedRoutePath("identity_options_get"),'GET',undefined,signal));}
 async login(username:string,password:string,requestId:string,signal?:AbortSignal){return parseAccountSession(await this.request(hostedRoutePath("session_create"),'POST',hostedRouteBody("session_create", {username,password,requestId,sessionMode:this.mode}),signal),this.mode);}
 async register(input:{username:string;displayName:string;password:string;contactAddress:string;requestId:string},signal?:AbortSignal){return parseRegistration(await this.request(hostedRoutePath("registration_create"),'POST',hostedRouteBody("registration_create", {...input,sessionMode:this.mode}),signal));}
 async registration(id?:string,signal?:AbortSignal){const v=await this.request(hostedEncodedRoutePath("registration_get", {"id": id?pathID(id):'current'}),'GET',undefined,signal);return v===null?null:parseRegistration(v);}
 async verifyRegistration(id:string,input:{code:string;username?:string;displayName?:string},signal?:AbortSignal){return parseAccountSession(await this.request(hostedEncodedRoutePath("registration_verify", {"id": pathID(id)}),'POST',input,signal),this.mode);}
 async resendRegistration(id:string,requestId:string,signal?:AbortSignal){return parseRegistration(await this.request(hostedEncodedRoutePath("registration_resend", {"id": pathID(id)}),'POST',hostedRouteBody("registration_resend", {requestId}),signal));}
 /** `completion`: the provider return's completion code (SEC-01), without which a current service
  * shows a native provider sign-in's status only. An older service ignores it. */
 async transaction(id?:string,signal?:AbortSignal,completion?:string){const v=await this.request(id?hostedEncodedRoutePath("identity_transaction_get", {"id": pathID(id)}):hostedRoutePath("identity_current"),'GET',undefined,signal,undefined,completion);return v===null?null:parseIdentityTransaction(v,id);}
 /** `completion: 'code'` (SEC-01) asks for the provider return's completion code: only for a native
  * browser sign-in, and only when the service's options say it gives one (`nativeCompletion`). */
 async start(p:IdentityProvider,input:{mode:OIDCStart['mode'];requestId:string;intent?:SecurityIntent;proof?:string;expectedIdentity?:{id:string;revision:string};nativeApple?:boolean;completion?:'code'},signal?:AbortSignal,token?:string){
  const {completion,...rest}=input;const asks=completion==='code'&&this.mode==='native'&&!input.nativeApple;
  return parseOIDCStart(await this.request(hostedEncodedRoutePath("identity_start", {"provider": p}),'POST',hostedRouteBody("identity_start", {...rest,sessionMode:this.mode,...(asks?{completion}:{})}),signal,token),p,input.nativeApple);}
 /** Distinct Security APIs never parse a Hosted session or server sign-in grant. */
 async prepareSecurityReturn(input:{context:SecuritySigned;state:string;codeChallenge:string},expiresAt:string,token:string,signal?:AbortSignal){
  return parseSecurityReturnIntent(await this.request(hostedRoutePath("account_security_return_prepare"),'POST',hostedRouteBody("account_security_return_prepare",input),signal,token),expiresAt);
 }
 async startSecurityReturn(intentId:string,requestId:string,signal?:AbortSignal):Promise<HostedSecurityReturnStart>{
  if(!/^sri_[A-Za-z0-9_-]{43}$/.test(intentId))return invalid();
  const raw=await this.request(hostedEncodedRoutePath("account_security_return_start",{id:pathID(intentId)}),'POST',hostedRouteBody("account_security_return_start",{requestId}),signal);
  if(!object(raw)||raw.intentId!==intentId||typeof raw.registeredOrigin!=='string'||!raw.registeredOrigin.startsWith('https://')||routeOrigin(raw.registeredOrigin)!==raw.registeredOrigin||typeof raw.state!=='string')return invalid();
  routeBytes(raw.state,32);const selected=provider(raw.provider),started=parseOIDCStart(raw,selected);
  if(started.mode!=='reauthenticate')return invalid();
  return {...started,provider:selected,intentId,registeredOrigin:raw.registeredOrigin,state:raw.state,context:parseSecuritySigned(raw.context)};
 }
 async finishSecurityReturn(original:HostedSecurityReturnStart,signal?:AbortSignal):Promise<string>{
  const transactionId=identifier(original.transactionId),origin=original.registeredOrigin,state=original.state;
  if(!origin.startsWith('https://')||routeOrigin(origin)!==origin)return invalid();routeBytes(state,32);
  const raw=await this.request(hostedEncodedRoutePath("identity_security_return",{id:pathID(transactionId)}),'POST',{},signal);
  if(!object(raw)||Object.keys(raw).join(',')!=='location'||typeof raw.location!=='string')return invalid();
  const returned=parseSecurityReturnCode(raw.location,{origin,state});
  if(returned.transactionId!==transactionId)return invalid();return raw.location;
 }
 async completeSecurityReturn(transactionId:string,input:{code:string;verifier:string;confirmation:SecuritySigned;factor:string},scope:AccountScope,token:string,signal?:AbortSignal):Promise<RecentProof>{
  const expected={accountId:identifier(scope.accountId),familyId:identifier(scope.familyId)},confirmation=input.confirmation;
  const captured=Object.freeze({code:input.code,verifier:input.verifier,factor:input.factor,confirmation:Object.freeze({payload:confirmation.payload,signature:confirmation.signature})});
  const raw=await this.request(hostedEncodedRoutePath("account_security_return_complete",{id:pathID(transactionId)}),'POST',hostedRouteBody("account_security_return_complete",captured),signal,token);
  if(!object(raw)||Object.keys(raw).join(',')!=='recentProof'||!raw.recentProof)return invalid();return parseRecentProof(raw.recentProof,expected);
 }
 async cancelSecurityTransaction(transactionId:string,signal?:AbortSignal){
  if(this.mode!=='browser'||!/^oid_[A-Za-z0-9_-]{43}$/.test(transactionId))return invalid();
  await this.request(hostedEncodedRoutePath("identity_security_cancel",{id:pathID(transactionId)}),'POST',{},signal);
 }
 async cancelSecurityReturn(intentId:string,token:string,signal?:AbortSignal){
  if(!/^sri_[A-Za-z0-9_-]{43}$/.test(intentId))return invalid();
  await this.request(hostedEncodedRoutePath("account_security_return_cancel",{id:pathID(intentId)}),'POST',{},signal,token);
 }
 /** `completionCode`: a native browser sign-in's provider return code (SEC-01). The service refuses
  * that sign-in's completion without it. */
 async complete(id:string,input:{username?:string;displayName?:string;onboardingToken?:string;factor?:string;completionCode?:string},signal?:AbortSignal){return parseAccountSession(await this.request(hostedEncodedRoutePath("identity_transaction_complete", {"id": pathID(id)}),'POST',input,signal),this.mode);}
 /** `completion`: a native browser provider return's completion code (SEC-01), the same one-use proof as a sign-in's. */
 async completeSecurity(id:string,factor:string,scope:AccountScope,token:string,signal?:AbortSignal,completion?:string):Promise<SecurityCompletion>{if(completion!==undefined&&!completionCode.test(completion))return invalid();const v=await this.request(hostedEncodedRoutePath("identity_transaction_complete", {"id": pathID(id)}),'POST',hostedRouteBody("identity_transaction_complete", {factor,...(completion?{completionCode:completion}:{})}),signal,token);if(!object(v))return invalid();if(v.recentProof)return{recentProof:parseRecentProof(v.recentProof,scope)};if(v.securityResult)return{securityResult:parseSecurityResult(v.securityResult,scope)};return invalid();}
 async sendContact(id:string,contactAddress:string,requestId:string,signal?:AbortSignal){return parseContactChallenge(await this.request(hostedEncodedRoutePath("identity_transaction_contact", {"id": pathID(id)}),'POST',hostedRouteBody("identity_transaction_contact", {contactAddress,requestId}),signal));}
 async verifyContact(id:string,challengeId:string,code:string,signal?:AbortSignal){return parseIdentityTransaction(await this.request(hostedEncodedRoutePath("identity_transaction_contact_verify", {"id": pathID(id)}),'POST',hostedRouteBody("identity_transaction_contact_verify", {challengeId,code}),signal),id);}
 async nativeApple(id:string,idToken:string,displayName:string,signal?:AbortSignal){return parseIdentityTransaction(await this.request(hostedEncodedRoutePath("identity_transaction_native_apple", {"id": pathID(id)}),'POST',hostedRouteBody("identity_transaction_native_apple", {idToken,displayName}),signal),id);}
 async mfa(challenge:MFAChallenge,factor:string,signal?:AbortSignal){return parseAccountSession(await this.request(hostedRoutePath("identity_mfa_complete"),'POST',hostedRouteBody("identity_mfa_complete", {challengeId:challenge.challengeId,challengeToken:challenge.challengeToken,factor}),signal),this.mode);}
 async state(scope:AccountScope,token:string,signal?:AbortSignal){return parseSecurityState(await this.request(hostedRoutePath("account_security_get"),'GET',undefined,signal,token),scope);}
 async passwordProof(intent:SecurityIntent,password:string,factor:string,scope:AccountScope,token:string,signal?:AbortSignal){return parseRecentProof(await this.request(hostedRoutePath("account_security_proof"),'POST',hostedRouteBody("account_security_proof", {intent,password,factor}),signal,token),scope);}
 /** A80: only credential changes carry a proof (see securityPurposeNeedsProof); account management sends none. */
 async action(intent:SecurityIntent,proof:string|undefined,newPassword:string,scope:AccountScope,token:string,signal?:AbortSignal){return parseSecurityResult(await this.request(hostedRoutePath("account_security_action"),'POST',hostedRouteBody("account_security_action", {...intent,...(proof?{proof}:{}),newPassword}),signal,token),scope);}
 /** A80: the deletion preview needs the signed-in session only (no password proof). */
 async deletionPreview(operationId:string,token:string,signal?:AbortSignal):Promise<DeletionPreview>{return parseDeletionPreview(await this.request(hostedRoutePath("account_deletion_preview"),'POST',hostedRouteBody("account_deletion_preview", {purpose:'account_deletion_preview',target:'',operationId}),signal,token));}
 /** A80: the final deletion is confirmed by the account's email (its username without one), typed by the person. */
 async acceptDeletion(preview:DeletionPreview,confirmation:string,token:string,signal?:AbortSignal):Promise<DeletionReceipt>{const confirm=confirmation.trim();if(!confirm||confirm.length>320)throw new Error('Type your account email to confirm.');return parseDeletionReceipt(await this.request(hostedRoutePath("account_deletion_accept"),'POST',hostedRouteBody("account_deletion_accept", {deletionToken:preview.deletionToken,disposition:'',confirmation:confirm}),signal,token));}
 /** Rotate only the original family's pending code; no address or fresh proof is supplied. */
 async resendEmailChange(challengeId:string,operationId:string,scope:AccountScope,token:string,signal?:AbortSignal){
  identifier(challengeId);
  const result=parseSecurityResult(await this.request(hostedRoutePrefix("account_security_action",3)+'email/resend','POST',{challengeId,operationId},signal,token),scope);
  if(result.status!=='contact_pending'||result.signOut||result.challengeId!==challengeId||!result.maskedAddress||!result.expiresAt||result.resendsRemaining===undefined)return invalid();
  return result;
 }
 async confirm(kind:'mfa'|'email',challengeId:string,code:string,operationId:string,scope:AccountScope,token:string,signal?:AbortSignal){return parseSecurityResult(await this.request(hostedRoutePrefix("account_security_action", 3)+kind+'/confirm','POST',{challengeId,code,operationId},signal,token),scope);}
}

/** In-memory only. Identical retries reuse an operation ID; edited input gets a new ID. */
export class AccountAttemptIds {
 private values=new Map<string,{body:string;id:string}>();
 private random:()=>Promise<string>;
 constructor(random:()=>Promise<string>){ this.random=random; }
 async for(key:string,body:unknown):Promise<string>{const serialized=JSON.stringify(body),prior=this.values.get(key);if(prior?.body===serialized)return prior.id;const id=await this.random();this.values.set(key,{body:serialized,id});return id;}
 clear(key?:string){if(key)this.values.delete(key);else this.values.clear();}
}

/** `completionCode` (SEC-01) is kept with the transaction it came back with, so a restarted app can
 * still see that sign-in's onboarding details. It lives where the binding does (the Keychain). */
export type NativeAccountContinuation={version:1;binding:string;expiresAt:string;transactionId?:string;completionCode?:string;mfa?:MFAChallenge};
export function parseNativeAccountContinuation(raw:unknown):NativeAccountContinuation|undefined {
 if(raw===null)return;
 if(typeof raw!=='string'||raw.length>8192)return invalid();const v:unknown=JSON.parse(raw);
 if(!object(v)||v.version!==1||typeof v.binding!=='string'||!/^[A-Za-z0-9_-]{43}$/.test(v.binding)||Object.keys(v).some(k=>!['version','binding','expiresAt','transactionId','completionCode','mfa'].includes(k)))return invalid();
 if(v.completionCode!==undefined&&(!v.transactionId||typeof v.completionCode!=='string'||!completionCode.test(v.completionCode)))return invalid();
 const expiresAt=timestamp(v.expiresAt);if(Date.parse(expiresAt)<=Date.now())return;
 return{version:1,binding:v.binding,expiresAt,...(v.transactionId?{transactionId:identifier(v.transactionId)}:{}),...(typeof v.completionCode==='string'?{completionCode:v.completionCode}:{}),...(v.mfa?{mfa:parseMFAChallenge(v.mfa)}:{})};
}
