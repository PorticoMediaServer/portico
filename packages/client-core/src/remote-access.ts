import type {MessageId, MessageValues} from '../../i18n/src/index.ts';
import {serviceText} from './presentation/service-text.ts';
import {routeBody} from "../../contracts/src/routes.ts";
import {routePath} from "../../contracts/src/route-paths.ts";
import type {HttpLocalApi} from './index.ts';
import {MAX_ROUTE_CANDIDATES,routeOrigin} from './route-identity.ts';
import {keepReadable} from './tolerant.ts';
/**
 * Owner remote-access settings. A save sends the whole object back, so fields a server added
 * (lanSharing, ipv6Open) must round-trip. ipv6Open defaults to allowed: proven IPv6 is published,
 * and false is the owner's disable override.
 */
export type RemoteConfiguration={revision:string;enabled:boolean;mapping:boolean;pcp:boolean;natpmp:boolean;upnp:boolean;publicPort:number;gateway:string;lanSharing?:boolean;
 /** Allow proven inbound IPv6 (the default); false is the owner's disable override. */
 ipv6Open?:boolean};
/** Why the server's global IPv6 address is (or isn't) published. */
export type RemoteIPv6State='pinhole'|'firewall_open'|'verified'|'disabled'|'unverified';
/** Something the owner should know about remote access (P2-67 Gate 2). `browser_restricted_port`:
 * web browsers refuse this port, though the apps can use it. Clients word it from code and port;
 * `message` is the server's English fallback for codes this client does not know. */
export type RemoteWarning=Readonly<{code:string;port:number;baseUrl?:string;message:string}>;
export type RemoteAccess={warnings:readonly RemoteWarning[];authorityId:string;generation:string;config:RemoteConfiguration;state:string;errorCode?:string;topology:{gateway?:string;localAddress?:string;lan:string[];public:string[];bind:string;errorCode?:string};candidates:{baseUrl:string;class:'lan'|'public'|'manual';state:string;verifiedAt?:string}[];mappings:{protocol:string;state:string;errorCode?:string;externalPort:number;expiresAt:string}[];ipv6?:RemoteIPv6State|string;checkedAt?:string;nextAttempt?:string};
const object=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const counter=(v:unknown)=>typeof v==='string'&&/^[1-9][0-9]{0,18}$/.test(v)&&BigInt(v)<=9223372036854775807n;
export function parseRemoteAccess(v:unknown):RemoteAccess{
 if(!object(v)||typeof v.authorityId!=='string'||v.authorityId.length>1024||!counter(v.generation)||typeof v.state!=='string'||!object(v.config)||!object(v.topology)||!Array.isArray(v.candidates)||v.candidates.length>MAX_ROUTE_CANDIDATES||!Array.isArray(v.mappings)||v.mappings.length>32)throw new Error('The remote-access response was incomplete.');
 const c=v.config;if(!counter(c.revision)||['enabled','mapping','pcp','natpmp','upnp'].some(k=>typeof c[k]!=='boolean')||['lanSharing','ipv6Open'].some(k=>c[k]!==undefined&&typeof c[k]!=='boolean')||(v.ipv6!==undefined&&(typeof v.ipv6!=='string'||v.ipv6.length>32))||!Number.isInteger(c.publicPort)||Number(c.publicPort)<1||Number(c.publicPort)>65535||typeof c.gateway!=='string'||c.gateway.length>64)throw new Error('The remote-access configuration was incomplete.');
 for(const key of ['lan','public'])if(!Array.isArray(v.topology[key])||(v.topology[key] as unknown[]).length>8||(v.topology[key] as unknown[]).some(x=>typeof x!=='string'||x.length>64))throw new Error('The topology response was incomplete.');
 // An address or a gateway mapping this app cannot read is left out alone: a route of a class it does not know is never shown as another class.
 const candidates=keepReadable('remote-access','candidates',v.candidates,row=>{if(!object(row)||typeof row.baseUrl!=='string'||routeOrigin(row.baseUrl)!==row.baseUrl||!['lan','public','manual'].includes(String(row.class))||typeof row.state!=='string')throw new Error('The remote route response was incomplete.');return row;});
 const mappings=keepReadable('remote-access','mappings',v.mappings,row=>{if(!object(row)||typeof row.protocol!=='string'||typeof row.state!=='string'||!Number.isInteger(row.externalPort)||typeof row.expiresAt!=='string')throw new Error('The gateway mapping response was incomplete.');return row;});
 if(v.warnings!==undefined&&(!Array.isArray(v.warnings)||v.warnings.length>16))throw new Error('The remote-access warnings were incomplete.');
 const warnings=keepReadable('remote-access','warnings',(v.warnings as unknown[]|undefined)??[],row=>{if(!object(row)||typeof row.code!=='string'||row.code.length>64||!Number.isInteger(row.port)||Number(row.port)<0||Number(row.port)>65535||typeof row.message!=='string'||row.message.length>300||(row.baseUrl!==undefined&&(typeof row.baseUrl!=='string'||row.baseUrl.length>300)))throw new Error('The remote-access warning was incomplete.');return Object.freeze({code:row.code,port:Number(row.port),message:row.message,...(typeof row.baseUrl==='string'?{baseUrl:row.baseUrl}:{})});});
 return {...v,candidates,mappings,warnings:Object.freeze(warnings)} as unknown as RemoteAccess;
}
export async function loadRemoteAccess(api:HttpLocalApi,signal?:AbortSignal){return parseRemoteAccess(await api.request(routePath("get_networking_remote"),'GET',undefined,signal));}
export async function saveRemoteAccess(api:HttpLocalApi,status:RemoteAccess,config:RemoteConfiguration,signal?:AbortSignal){
 if(config.revision!==status.config.revision)throw new Error('Reload remote-access settings before saving.');
 return parseRemoteAccess(await api.request(routePath("post_networking_remote_config"),'POST',routeBody("post_networking_remote_config", {authorityId:status.authorityId,config}),signal));
}
export async function checkRemoteAccess(api:HttpLocalApi,signal?:AbortSignal){return parseRemoteAccess(await api.request(routePath("post_networking_remote_check"),'POST',routeBody("post_networking_remote_check", {}),signal));}
type RemoteTranslate = (key: MessageId, values?: MessageValues) => string;
const summaryKeys: Readonly<Record<string, MessageId>> = {
  reachable: 'remoteAccess.summary.reachable',
  disabled: 'remoteAccess.summary.disabled',
  claim_required: 'remoteAccess.summary.claim_required',
  certificate_pending: 'remoteAccess.summary.certificate_pending',
  manual_only: 'remoteAccess.summary.manual_only',
  hosted_unavailable: 'remoteAccess.summary.hosted_unavailable',
  remote_unavailable: 'remoteAccess.summary.remote_unavailable',
  checking: 'remoteAccess.summary.checking',
  lan_only: 'remoteAccess.summary.lan_only',
};
const helpKeys: Readonly<Record<string, MessageId>> = {
  listener_loopback_only: 'remoteAccess.help.listener_loopback_only',
  gateway_not_found: 'remoteAccess.help.gateway_not_found',
  gateway_outside_listener: 'remoteAccess.help.gateway_outside_listener',
  no_public_route: 'remoteAccess.help.no_public_route',
  external_proof_or_hosted_unavailable: 'remoteAccess.help.external_proof_or_hosted_unavailable',
  mapping_not_authorized: 'remoteAccess.help.mapping_not_authorized',
  mapping_unsupported: 'remoteAccess.help.mapping_unsupported',
  mapping_conflict: 'remoteAccess.help.mapping_conflict',
};
/** All status copy comes from the viewer's catalogue; an unknown state stays generic. */
export function remoteAccessSummary(status: RemoteAccess, t: RemoteTranslate = serviceText): string {
  return t(Object.hasOwn(summaryKeys, status.state) ? summaryKeys[status.state] : 'remoteAccess.summary.other');
}
export function remoteAccessHelp(code?: string, t: RemoteTranslate = serviceText): string {
  if (!code) return '';
  return t(Object.hasOwn(helpKeys, code) ? helpKeys[code] : 'remoteAccess.help.other');
}

/** The owner-facing line for each warning: known codes in the client's own words, others as the server wrote them. */
export function remoteWarningLines(status:Pick<RemoteAccess,'warnings'>,t:RemoteTranslate):readonly string[]{
 return status.warnings.map(w=>w.code==='browser_restricted_port'?t('remoteAccess.warning.browserRestrictedPort',{port:w.port}):w.message);
}
