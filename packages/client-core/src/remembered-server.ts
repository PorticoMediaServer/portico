import type {RememberedServer} from './server-connections.ts';
import {parseLocalSession} from './local-session.ts';
import {RouteError,routeOrigin,type ServerPin} from './route-identity.ts';
import {parseNetworkFacts,type RememberedNetworkPolicy} from './route-connection.ts';
const object=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
/** A store's (or this module's) report that the saved record is there but can never be read. Told
 * by its code, so a platform store's own error object can carry it across a native bridge. */
export const savedConnectionDamaged=(e:unknown):boolean=>(e as {code?:unknown}|null)?.code==='invalid_saved_connection';
/** The server's network policy as last read; anything unreadable is dropped and read again. */
function rememberedPolicy(raw:unknown):{policy?:RememberedNetworkPolicy}{
 if(!object(raw)||typeof raw.observedAt!=='string'||!Number.isFinite(Date.parse(raw.observedAt)))return {};
 const facts=parseNetworkFacts(raw).policy;
 return {policy:{...facts,observedAt:raw.observedAt}};
}
export function parseRememberedServer(value:unknown):RememberedServer|undefined {
 if(value===null||value===undefined)return;
 if(!object(value)||value.version!=='1'||typeof value.logicalOrigin!=='string'||!object(value.pin)||!object(value.routes)||!Array.isArray(value.routes.paired)||value.routes.paired.length>8)throw new RouteError('invalid_saved_connection','Saved server connection is incomplete. Sign in again to establish its identity.');
 const session=parseLocalSession(value.session,{},{stored:true}),pin=value.pin;
 if(pin.serverId!==session.viewer.serverId||typeof pin.publicKey!=='string'||typeof pin.fingerprint!=='string'||value.logicalOrigin!==routeOrigin(value.logicalOrigin))throw new RouteError('invalid_saved_connection','Saved server identity is incomplete.');
 const result:RememberedServer={version:'1',logicalOrigin:value.logicalOrigin,session,pin:pin as ServerPin,routes:{paired:value.routes.paired.map(v=>routeOrigin(String(v))),...(typeof value.routes.lastGood==='string'?{lastGood:routeOrigin(value.routes.lastGood)}:{}),...rememberedPolicy(value.routes.policy)}};
 if(value.pendingRefresh!==undefined){if(typeof value.pendingRefresh!=='string'||!/^[A-Za-z0-9_-]{1,128}$/.test(value.pendingRefresh))throw new RouteError('invalid_saved_connection','Saved renewal state is invalid.');result.pendingRefresh=value.pendingRefresh;}
 if(value.selectionId!==undefined){if(typeof value.selectionId!=='string'||!/^[-_A-Za-z0-9]{32}$/.test(value.selectionId))throw new RouteError('invalid_saved_connection','Saved selection identity is invalid.');result.selectionId=value.selectionId;}
 if(value.hosted!==undefined){if(!object(value.hosted)||typeof value.hosted.origin!=='string'||typeof value.hosted.accountId!=='string'||!object(value.hosted.key)||typeof value.hosted.key.keyId!=='string'||typeof value.hosted.key.publicKey!=='string'||(session.viewer.authority==='hosted'&&value.hosted.accountId!==session.viewer.accountId)||!['hosted','local'].includes(session.viewer.authority))throw new RouteError('invalid_saved_connection','Saved account route authority is incomplete.');result.hosted={origin:hostedOrigin(value.hosted.origin),accountId:value.hosted.accountId,key:{keyId:value.hosted.key.keyId,publicKey:value.hosted.key.publicKey}};}
 for(const name of ['current','previous'] as const){const v=value.routes[name];if(v!==undefined){if(!object(v)||typeof v.payload!=='string'||typeof v.signature!=='string'||v.payload.length>50000||v.signature.length!==86)throw new RouteError('invalid_saved_connection','Saved route evidence is incomplete.');result.routes[name]={payload:v.payload,signature:v.signature};}}
 return result;
}
export function hostedOrigin(origin:string):string {const value=routeOrigin(origin),u=new URL(value);if(u.protocol!=='https:'&&!['localhost','127.0.0.1','[::1]'].includes(u.hostname))throw new RouteError('invalid_account_origin','Portico Account services require HTTPS.');return value;}
