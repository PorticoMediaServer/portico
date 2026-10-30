import {readBoundedJson} from './bounded-json.ts';
import type {MessageId} from '../../i18n/src/index.ts';

export type CertificateConfig = Readonly<{enabled:boolean; publicAddress:string; publicPort:number; revision:string}>;
export type CertificateStatus = Readonly<{
 configured:boolean; authorityId:string; config:CertificateConfig; state:string; errorCode?:string;
 namespace?:string; dnsName?:string; issuer?:string; environment:'production'|'staging'; orderState?:string;
 notAfter?:string; renewAt?:string; nextAttemptAt?:string; tlsReady:boolean; publiclyTrusted:boolean;
 listenerBound:boolean; listenPort:number; routeHostname?:string; routeUrl?:string; routeErrorCode?:string; reachability:'probe_required';
}>;
export type CertificateAction='status'|'config'|'retry';
export interface CertificateApi {requestCertificate<T>(action:CertificateAction,body:unknown,signal:AbortSignal):Promise<T>}

// certificate_http.go's safeCertificateCode plus certificate_manager.go's local failures.
const CERTIFICATE_FAILURES = new Set([
 'certificate_not_found','certificate_not_due','certificate_conflict','certificate_configuration_required','invalid_certificate_request',
 'acceptance_uncertain','authority_changed','creation_started','provider_rejected','provider_response_invalid','provider_order_invalid','dns_validation_failed',
 'coordinator_retry','cleanup_retry','revocation_retry','provider_retry','acme_account_configuration_or_retry','dns_or_issuer_configuration',
 'certificate_retry','local_certificate_invalid','certificate_response_invalid','issuer_environment_mismatch','public_trust_store_unavailable','local_tls_key_unavailable',
]);

/** Public reason only: provider text and unknown codes never become display text.
 * TLS readiness is independent of the latest order: an old certificate may still serve. */
export function certificateProblemMessage(status?:CertificateStatus):MessageId|undefined {
 if(!status?.config.enabled)return undefined;
 if(status.state==='expired')return 'web.connectivity.customCertExpired';
 if(status.state==='invalid')return 'settings.server.secure.invalid';
 if(status.errorCode){
  if(!CERTIFICATE_FAILURES.has(status.errorCode))return 'settings.server.secure.problemUnknown';
  if(status.tlsReady)return 'settings.server.secure.renewalFailed';
  if(['local_certificate_invalid','certificate_response_invalid','public_trust_store_unavailable','local_tls_key_unavailable'].includes(status.errorCode))return 'settings.server.secure.invalid';
  return 'settings.server.secure.obtainFailed';
 }
 if(status.routeErrorCode)return status.routeErrorCode==='route_name_retry'?'settings.server.secure.publishFailed':'settings.server.secure.problemUnknown';
 return undefined;
}

const record=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const text=(v:unknown,max=256):v is string=>typeof v==='string'&&v.length<=max&&!/[\u0000-\u001f\u007f]/.test(v);
const code=(v:unknown):v is string=>text(v,80)&&/^[a-z0-9_]*$/.test(v);
const port=(v:unknown,min=1):v is number=>typeof v==='number'&&Number.isInteger(v)&&v>=min&&v<=65535;
function fail():never{throw new Error('Invalid certificate status.');}
export function parseCertificateStatus(v:unknown):CertificateStatus {
 if(!record(v)||!record(v.config)||!text(v.authorityId,64)||!/^([a-f0-9]{64})?$/.test(v.authorityId)||!code(v.state)||!['production','staging'].includes(String(v.environment))||v.reachability!=='probe_required')fail();
 const c=v.config;
 if(typeof c.enabled!=='boolean'||!port(c.publicPort)||!text(c.publicAddress,64)||typeof c.revision!=='string'||!/^\d{1,19}$/.test(c.revision))fail();
 for(const key of ['configured','tlsReady','publiclyTrusted','listenerBound'])if(typeof v[key]!=='boolean')fail();
 if(!port(v.listenPort,0)||v.publiclyTrusted&&(!v.tlsReady||v.environment!=='production')||v.tlsReady&&!c.enabled)fail();
 for(const key of ['errorCode','routeErrorCode','orderState'])if(v[key]!==undefined&&!code(v[key]))fail();
 for(const key of ['namespace','dnsName','issuer','routeHostname','routeUrl'])if(v[key]!==undefined&&!text(v[key],512))fail();
 for(const key of ['notAfter','renewAt','nextAttemptAt'])if(v[key]!==undefined&&(!text(v[key],64)||!/^\d{4}-\d\d-\d\dT/.test(v[key])||!Number.isFinite(Date.parse(v[key]))))fail();
 if(v.namespace!==undefined&&!/^ptc-[a-z2-7]{19}[aq]$/.test(v.namespace as string))fail();
 // The direct domain is operator configuration (production portico.direct, P2-67 Gate 2): the
 // wildcard is exactly one namespace under some domain, and the route name one label under that.
 const zone=typeof v.dnsName==='string'&&typeof v.namespace==='string'&&v.dnsName.startsWith('*.'+v.namespace+'.')?v.dnsName.slice(2):undefined;
 if(v.dnsName!==undefined&&(!zone||!/^ptc-[a-z2-7]{19}[aq](?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?){2,}$/.test(zone)))fail();
 if(v.routeUrl){let u:URL;try{u=new URL(v.routeUrl as string);}catch{fail();}const label=u.hostname.split('.')[0]??'',under=u.hostname.slice(label.length+1);if(u.protocol!=='https:'||u.username||u.password||u.search||u.hash||u.pathname!=='/'||u.hostname!==v.routeHostname||!/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label)||(zone?under!==zone:!under.startsWith(String(v.namespace)+'.'))||(u.port||'443')!==String(c.publicPort))fail();}
  // Build a whitelist projection: never retain unexpected keys, CSR, tokens or provider response bodies.
  // CD-10: keep Go wire order (enabled, publicPort, publicAddress, revision) for canonical bytes.
  const out:Record<string,unknown>={configured:v.configured,authorityId:v.authorityId,config:Object.freeze({enabled:c.enabled,publicPort:c.publicPort,publicAddress:c.publicAddress,revision:c.revision}),state:v.state,environment:v.environment,tlsReady:v.tlsReady,publiclyTrusted:v.publiclyTrusted,listenerBound:v.listenerBound,listenPort:v.listenPort,reachability:'probe_required'};
 for(const k of ['errorCode','namespace','dnsName','issuer','orderState','notAfter','renewAt','nextAttemptAt','routeHostname','routeUrl','routeErrorCode'])if(v[k]!==undefined)out[k]=v[k];
 return Object.freeze(out) as CertificateStatus;
}

/** Streaming on web; RN's native fetch buffers before JS, so apply the same
 * bounded projection to its buffered text. No response body enters error text. */
export async function readCertificateResponse<T>(response:Response,signal:AbortSignal):Promise<T>{
 if(response.body&&typeof response.body.getReader==='function')return readBoundedJson<T>(response,16384,signal);
 if(!response.ok)throw Object.assign(new Error('Certificate request failed.'),{status:response.status});
 const length=response.headers.get('Content-Length');
 if(length!==null&&(!/^\d+$/.test(length)||Number(length)>16384))fail();
 if(response.headers.get('Content-Type')?.split(';')[0].trim().toLowerCase()!=='application/json')fail();
 const raw=await response.text();
 if(signal.aborted||raw.length>16384||new TextEncoder().encode(raw).byteLength>16384)fail();
 try{return JSON.parse(raw) as T;}catch{fail();}
}
export async function loadCertificate(api:CertificateApi,signal:AbortSignal):Promise<CertificateStatus>{return parseCertificateStatus(await api.requestCertificate('status',undefined,signal));}
export async function saveCertificate(api:CertificateApi,current:CertificateStatus,config:CertificateConfig,signal:AbortSignal):Promise<CertificateStatus>{
 if(!current.authorityId)throw new Error('Claim this server before configuring HTTPS.');
 // CD-10: explicit Go wire order; never spread the parsed config (wrong insertion order).
 // Outer authorityId then config; inner enabled, publicPort, publicAddress, revision.
 return parseCertificateStatus(await api.requestCertificate('config',{authorityId:current.authorityId,config:{enabled:config.enabled,publicPort:config.publicPort,publicAddress:config.publicAddress,revision:current.config.revision}},signal));
}
export async function retryCertificate(api:CertificateApi,signal:AbortSignal):Promise<CertificateStatus>{return parseCertificateStatus(await api.requestCertificate('retry',{},signal));}
export function certificateError(error:unknown):string{
 const status=record(error)?error.status:undefined;
 if(status===401||status===403)return 'Sign in with this server’s local owner account to manage automatic HTTPS.';
 if(status===404)return 'Automatic HTTPS is not configured on this server. Configure its Hosted connection first.';
 if(status===409)return 'The server claim or configuration changed. Refresh before saving again.';
 if(status===429)return 'A retry is already scheduled. The server will continue automatically.';
 return 'Certificate status could not be updated. Check the server connection and try again.';
}
export function certificateDate(value?:string):string{return value?new Date(value).toLocaleString():'Not yet available';}

