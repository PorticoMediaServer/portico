import {routeBody} from "../../contracts/src/routes.ts";
import {callServer, notOnThisServer, serverPath} from './query/server-api.ts';
import type {RequestRaw} from './home-selection/list-destinations.ts';
import {routePath} from "../../contracts/src/route-paths.ts";
/**
 * API keys, personal access tokens, and outbound webhooks.
 *
 * This module is transport and parsing only; nothing here decides policy.
 * What grants access is read whole: a list of keys, tokens or webhooks with a
 * row this app cannot read is refused, so a credential or a destination is
 * never missing from what the owner sees and no scope or state is guessed.
 * Receipts are exact too. The delivery log and the scopes on
 * offer leave out only the row or the value this app cannot read. Credential secrets pass through an explicit creation or owner recovery
 * request and are never kept by this module. Webhook secret rotation is separate.
 */
import {firstOfEach,keepReadable} from './tolerant.ts';
export type ApiAccessTransport={request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>};

const unreadable='The server sent something this app could not read.';
export class ApiAccessError extends Error{
 readonly code:string;
 constructor(code:string,message=unreadable){super(message);this.name='ApiAccessError';this.code=code;}
}
const invalid=(what:string):never=>{throw new ApiAccessError('invalid_document',`${unreadable} (${what})`);};
const object=(v:unknown):v is Record<string,unknown>=>typeof v==='object'&&v!==null&&!Array.isArray(v);
const text=(v:unknown,max:number,what:string):string=>typeof v==='string'&&v.length<=max?v:invalid(what);
const filled=(v:unknown,max:number,what:string):string=>{const s=text(v,max,what);return s.length?s:invalid(what);};
const optional=(v:unknown,max:number,what:string):string|undefined=>v===undefined||v===''?undefined:text(v,max,what);
const whole=(v:unknown,what:string):number=>typeof v==='number'&&Number.isInteger(v)&&v>=0?v:invalid(what);
const flag=(v:unknown,what:string):boolean=>typeof v==='boolean'?v:invalid(what);
const list=<T>(v:unknown,max:number,each:(raw:unknown)=>T,what:string):readonly T[]=>Array.isArray(v)&&v.length<=max?Object.freeze(v.map(each)):invalid(what);
const oneOf=<T extends string>(v:unknown,allowed:readonly T[],what:string):T=>typeof v==='string'&&(allowed as readonly string[]).includes(v)?v as T:invalid(what);
const instant=(v:unknown,what:string):string=>{const s=filled(v,40,what);return Number.isNaN(Date.parse(s))?invalid(what):s;};
const optionalInstant=(v:unknown,what:string):string|undefined=>v===undefined||v===''?undefined:instant(v,what);
const query=(values:Readonly<Record<string,string|number|undefined>>):string=>{
 const parts=Object.entries(values).filter(([,v])=>v!==undefined&&v!=='').map(([k,v])=>`${encodeURIComponent(k)}=${encodeURIComponent(String(v))}`);
 return parts.length?`?${parts.join('&')}`:'';
};

// ── Scopes ──────────────────────────────────────────────────────────────────

/** What a key or token may be given. One scope does not include another, except that
 * `admin` is everything. A scope never raises what the member behind the credential may do. */
export const apiScopes=['read','playback','library','admin'] as const;
export type ApiScope=typeof apiScopes[number];

/** Ready-made choices for a simple picker: each is the scopes a typical use needs. */
export const apiScopePresets=Object.freeze({
 read:Object.freeze(['read']) as readonly ApiScope[],
 playback:Object.freeze(['read','playback']) as readonly ApiScope[],
 library:Object.freeze(['read','playback','library']) as readonly ApiScope[],
 admin:Object.freeze(['admin']) as readonly ApiScope[],
});
export type ApiScopePreset=keyof typeof apiScopePresets;
export const apiScopePresetIds=['read','playback','library','admin'] as const satisfies readonly ApiScopePreset[];

/** presetOf names the preset whose scopes are exactly these, if one is. */
export function presetOf(scopes:readonly ApiScope[]):ApiScopePreset|undefined{
 const key=[...scopes].sort().join(',');
 return apiScopePresetIds.find(id=>[...apiScopePresets[id]].sort().join(',')===key);
}

// ── Keys and tokens ─────────────────────────────────────────────────────────

/** Every API key secret starts with `pk.` and every personal access token with `pat.`. */
export const apiKeyPrefix='pk.';
export const accessTokenPrefix='pat.';
export const isApiCredentialSecret=(secret:string):boolean=>secret.startsWith(apiKeyPrefix)||secret.startsWith(accessTokenPrefix);

export const apiCredentialStates=['active','awaiting_approval','expired','revoked','ended'] as const;
export type ApiCredentialState=typeof apiCredentialStates[number];
export type ApiCredentialKind='key'|'token';
export type ApiCredentialMember=Readonly<{accountId:string;username:string;profileId:string;profile:string}>;
export type ApiCredential=Readonly<{id:string;kind:ApiCredentialKind;name:string;scopes:readonly ApiScope[];hint:string;createdAt:string;
 expiresAt?:string;lastUsedAt?:string;state:ApiCredentialState;endedReason?:string;member:ApiCredentialMember}>;
/** The answer to a create. `secret` is present only the first time the request is answered. */
export type IssuedApiCredential=Readonly<{credential:ApiCredential;secret?:string}>;
/** `total` is the exact count under the same filters, across every page. */
export type ApiCredentialPage=Readonly<{items:readonly ApiCredential[];limit:number;total:number;nextCursor?:string}>;
export type ApiAccessOptions=Readonly<{scopes:readonly ApiScope[];maximumKeys:number;maximumTokens:number;tokensAllowed:boolean;requestsPerMinute:number;/** The query parameter a key or token may travel in on a GET, for an address that works on its own. */queryParameter:string}>;
export type NewApiCredential=Readonly<{operationId:string;name:string;scopes:readonly ApiScope[];expiresAt?:string}>;

export function parseApiCredential(raw:unknown):ApiCredential{
 if(!object(raw)||!object(raw.member))return invalid('credential');
 const scopes=list(raw.scopes,apiScopes.length,v=>oneOf(v,apiScopes,'credential scope'),'credential scopes');
 if(!scopes.length)invalid('credential scopes');
 // A listing must never carry a secret; a document that does is not one to trust.
 if('secret' in raw)invalid('credential carries a secret');
 return Object.freeze({id:filled(raw.id,128,'credential id'),kind:oneOf(raw.kind,['key','token'] as const,'credential kind'),name:filled(raw.name,160,'credential name'),
  scopes,hint:text(raw.hint,16,'credential hint'),createdAt:instant(raw.createdAt,'credential createdAt'),expiresAt:optionalInstant(raw.expiresAt,'credential expiresAt'),
  lastUsedAt:optionalInstant(raw.lastUsedAt,'credential lastUsedAt'),state:oneOf(raw.state,apiCredentialStates,'credential state'),endedReason:optional(raw.endedReason,64,'credential endedReason'),
  member:Object.freeze({accountId:filled(raw.member.accountId,128,'member account'),username:text(raw.member.username,128,'member username'),profileId:filled(raw.member.profileId,128,'member profile id'),profile:text(raw.member.profile,128,'member profile')})});
}
export function parseIssuedApiCredential(raw:unknown):IssuedApiCredential{
 if(!object(raw))return invalid('issued credential');
 const credential=parseApiCredential(raw.credential);
 const secret=optional(raw.secret,512,'credential secret');
 const prefix=credential.kind==='key'?apiKeyPrefix:accessTokenPrefix;
 if(secret!==undefined&&!secret.startsWith(prefix))invalid('credential secret');
 return Object.freeze({credential,secret});
}
export function parseApiCredentialPage(raw:unknown):ApiCredentialPage{
 if(!object(raw))return invalid('credential page');
 return Object.freeze({items:list(raw.items,100,parseApiCredential,'credential page items'),limit:whole(raw.limit,'credential page limit'),total:whole(raw.total,'credential page total'),nextCursor:optional(raw.nextCursor,512,'credential page cursor')});
}
export function parseApiAccessOptions(raw:unknown):ApiAccessOptions{
 if(!object(raw))return invalid('api access options');
 // A scope this app does not know is not offered; the ones it knows still are.
 const scopes=Object.freeze(firstOfEach('api-access','options.scopes',keepReadable('api-access','options.scopes',list(raw.scopes,16,v=>v,'scopes'),v=>object(v)?oneOf(v.id,apiScopes,'scope id'):invalid('scope')),scope=>scope));
 return Object.freeze({scopes,maximumKeys:whole(raw.maximumKeys,'maximumKeys'),maximumTokens:whole(raw.maximumTokens,'maximumTokens'),tokensAllowed:flag(raw.tokensAllowed,'tokensAllowed'),requestsPerMinute:whole(raw.requestsPerMinute,'requestsPerMinute'),queryParameter:filled(raw.queryParameter,32,'queryParameter')});
}

const API_KEYS=routePath("list_api_keys"),OPTIONS=routePath("get_api_access_options");
const id=(value:string):string=>encodeURIComponent(value);
const body=(q:NewApiCredential)=>({operationId:q.operationId,name:q.name,scopes:[...q.scopes],...(q.expiresAt?{expiresAt:q.expiresAt}:{})});

export async function getApiAccessOptions(t:ApiAccessTransport,signal?:AbortSignal):Promise<ApiAccessOptions|undefined>{
 try{return parseApiAccessOptions(await t.request<unknown>(OPTIONS,'GET',undefined,signal));}
 catch(error){if(notOnThisServer(error))return undefined;throw error;}
}
/** The owner's API keys. */
export const listApiKeys=async(t:ApiAccessTransport,cursor='',signal?:AbortSignal):Promise<ApiCredentialPage>=>parseApiCredentialPage(await t.request<unknown>(API_KEYS+query({limit:100,cursor}),'GET',undefined,signal));
/** `operationId` is one per thing the person asked for and is reused on a retry; a retry's answer has no secret. */
export const createApiKey=async(t:ApiAccessTransport,q:NewApiCredential,signal?:AbortSignal):Promise<IssuedApiCredential>=>parseIssuedApiCredential(await t.request<unknown>(API_KEYS,'POST',body(q),signal));
export const revokeApiKey=async(t:ApiAccessTransport,keyId:string,signal?:AbortSignal):Promise<ApiCredential>=>parseApiCredential(await t.request<unknown>(`${API_KEYS}/${id(keyId)}`,'DELETE',undefined,signal));
export type PersonalAccessToken=Readonly<Pick<ApiCredential,'id'|'name'|'scopes'|'createdAt'|'lastUsedAt'|'expiresAt'>>;
function parsePersonalAccessToken(raw:unknown):PersonalAccessToken{
 if(!object(raw)||'secret' in raw)return invalid('personal token');
 const scopes=list(raw.scopes,apiScopes.length,v=>oneOf(v,apiScopes,'credential scope'),'credential scopes');
 if(!scopes.length)invalid('credential scopes');
 return Object.freeze({id:filled(raw.id,128,'credential id'),name:filled(raw.name,160,'credential name'),
  scopes,createdAt:instant(raw.createdAt,'credential createdAt'),expiresAt:optionalInstant(raw.expiresAt,'credential expiresAt'),
  lastUsedAt:optionalInstant(raw.lastUsedAt,'credential lastUsedAt')});
}

/** The rebuilt server lists the member's active tokens. */
export async function listAccessTokens(t:ApiAccessTransport,cursor='',signal?:AbortSignal){
 const page=await callServer(t,'GET /v1/me/tokens',{query:{cursor},signal});
 if(!object(page))return invalid('personal tokens');
 const items=list(page.items,100,parsePersonalAccessToken,'personal tokens');
 return Object.freeze({items,nextCursor:optional(page.nextCursor,512,'credential page cursor')});
}
export async function createAccessToken(t:ApiAccessTransport,q:NewApiCredential,signal?:AbortSignal){
 const issued=await callServer(t,'POST /v1/me/tokens',{body:body(q),signal});
 if(!object(issued))return invalid('issued personal token');
 const key=parsePersonalAccessToken(issued.key);
 const secret=optional(issued.secret,512,'credential secret');
 if(secret!==undefined&&!secret.startsWith('pk_'))invalid('credential secret');
 return Object.freeze({key,secret});
}
export function revokeAccessToken(t:ApiAccessTransport,tokenId:string,signal?:AbortSignal){
 return callServer(t,'DELETE /v1/me/tokens/{id}',{id:tokenId,signal});
}
// ── Webhooks ────────────────────────────────────────────────────────────────

export const webhookEvents=['title.added','title.updated','title.removed','scan.started','scan.finished','playback.started','playback.paused','playback.resumed','playback.stopped',
 'recording.finished','recording.failed','member.signed_in','backup.succeeded','backup.failed','storage.low','update.available','server.started'] as const;
export type WebhookEvent=typeof webhookEvents[number];
export type Webhook=Readonly<{id:string;name:string;url:string;events:readonly string[];enabled:boolean;trouble?:string;createdAt:string;waiting:number;dropped:number;lastDeliveredAt?:string}>;
export type IssuedWebhook=Readonly<{webhook:Webhook;secret:string}>;
export type WebhookList=Readonly<{items:readonly Webhook[];nextCursor?:string}>;
export type WebhookDeliveryState='waiting'|'delivered'|'gave_up';
export type WebhookDelivery=Readonly<{event:string;state:WebhookDeliveryState;tries:number;createdAt:string;finishedAt?:string;nextTryAt?:string;status?:number;error?:string}>;
export type WebhookDeliveryPage=Readonly<{items:readonly WebhookDelivery[];nextCursor?:string}>;
export type WebhookTest=Readonly<{delivered:boolean;status?:number;error?:string}>;
export type NewWebhook=Readonly<{name:string;url:string;events:readonly string[]}>;
export type WebhookChange=Readonly<{name?:string;url?:string;events?:readonly string[];enabled?:boolean}>;
export type WebhookTransport=ApiAccessTransport & {requestRaw:RequestRaw};
export const webhookMaximum=20;

export function parseWebhook(raw:unknown):Webhook{
 if(!object(raw))return invalid('webhook');
 if('secret' in raw)invalid('webhook carries a secret');
 return Object.freeze({id:filled(raw.id,128,'webhook id'),name:filled(raw.name,120,'webhook name'),url:filled(raw.url,2048,'webhook url'),events:list(raw.events,64,v=>filled(v,64,'webhook event'),'webhook events'),
  enabled:flag(raw.enabled,'webhook enabled'),trouble:optional(raw.trouble,1024,'webhook trouble'),createdAt:instant(raw.createdAt,'webhook createdAt'),lastDeliveredAt:optionalInstant(raw.lastDeliveredAt,'webhook lastDeliveredAt'),
  waiting:whole(raw.waiting,'webhook waiting'),dropped:whole(raw.dropped,'webhook dropped')});
}
export function parseIssuedWebhook(raw:unknown):IssuedWebhook{
 if(!object(raw))return invalid('issued webhook');
 return Object.freeze({webhook:parseWebhook(raw.webhook),secret:filled(raw.secret,256,'webhook secret')});
}
export function parseWebhookList(raw:unknown):WebhookList{
 if(!object(raw))return invalid('webhook list');
 return Object.freeze({items:list(raw.items,100,parseWebhook,'webhook list items'),nextCursor:optional(raw.nextCursor,512,'webhook cursor')});
}
export function parseWebhookDelivery(raw:unknown):WebhookDelivery{
 if(!object(raw))return invalid('webhook delivery');
 return Object.freeze({event:filled(raw.event,64,'delivery event'),state:oneOf(raw.state,['waiting','delivered','gave_up'] as const,'delivery state'),tries:whole(raw.tries,'delivery tries'),
  createdAt:instant(raw.createdAt,'delivery createdAt'),finishedAt:optionalInstant(raw.finishedAt,'delivery finishedAt'),nextTryAt:optionalInstant(raw.nextTryAt,'delivery nextTryAt'),
  status:raw.status===undefined?undefined:whole(raw.status,'delivery status'),error:optional(raw.error,1024,'delivery error')});
}
export function parseWebhookDeliveryPage(raw:unknown):WebhookDeliveryPage{
 if(!object(raw))return invalid('webhook delivery page');
 // Log rows have no occurrence ID: equal events/timestamps may be distinct deliveries.
 return Object.freeze({items:Object.freeze(keepReadable('api-access','deliveries',list(raw.items,100,v=>v,'delivery page items'),parseWebhookDelivery)),nextCursor:optional(raw.nextCursor,512,'delivery page cursor')});
}
export const listWebhooks=async(t:ApiAccessTransport,signal?:AbortSignal):Promise<WebhookList>=>parseWebhookList(await t.request<unknown>(serverPath('GET /v1/admin/webhooks'),'GET',undefined,signal));
/** Keep this key for the exact create after an uncertain answer. */
export async function createWebhook(t:WebhookTransport,q:NewWebhook,signal:AbortSignal|undefined,operationId:string):Promise<IssuedWebhook>{
 const answer=await t.requestRaw(serverPath('POST /v1/admin/webhooks'),'POST',{body:{name:q.name,url:q.url,events:[...q.events]},headers:{'Idempotency-Key':operationId},signal});
 if(answer.status>=400){const error=answer.body as {code?:string;message?:string}|undefined;throw Object.assign(new ApiAccessError(error?.code??'request_failed',error?.message),{status:answer.status});}
 return parseIssuedWebhook(answer.body);
}
export const updateWebhook=async(t:ApiAccessTransport,webhookId:string,change:WebhookChange,signal?:AbortSignal):Promise<Webhook>=>parseWebhook(await t.request<unknown>(serverPath('PATCH /v1/admin/webhooks/{id}',{id:webhookId}),'PATCH',change,signal));
export const rotateWebhookSecret=async(t:ApiAccessTransport,webhookId:string,signal?:AbortSignal):Promise<IssuedWebhook>=>parseIssuedWebhook(await t.request<unknown>(serverPath('POST /v1/admin/webhooks/{id}/secret',{id:webhookId}),'POST',undefined,signal));
/** The fields of an edit form that differ from the webhook it was opened on: only those are sent, so another owner's change to the rest is kept. */
export function webhookEdits(was:Webhook,form:Readonly<{name:string;url:string;events:readonly string[]}>):WebhookChange{
 const out:{name?:string;url?:string;events?:readonly string[]}={};
 if(form.name!==was.name)out.name=form.name;
 if(form.url!==was.url)out.url=form.url;
 if(JSON.stringify([...form.events].sort())!==JSON.stringify([...was.events].sort()))out.events=form.events;
 return Object.freeze(out);
}
export const deleteWebhook=async(t:ApiAccessTransport,webhookId:string,signal?:AbortSignal):Promise<void>=>{await t.request<unknown>(serverPath('DELETE /v1/admin/webhooks/{id}',{id:webhookId}),'DELETE',undefined,signal);};
export async function sendWebhookTest(t:ApiAccessTransport,webhookId:string,signal?:AbortSignal):Promise<WebhookTest>{
 const raw=await t.request<unknown>(serverPath('POST /v1/admin/webhooks/{id}/test',{id:webhookId}),'POST',undefined,signal);
 if(!object(raw))return invalid('webhook test');
 return Object.freeze({delivered:flag(raw.delivered,'test delivered'),status:raw.status===undefined?undefined:whole(raw.status,'test status'),error:optional(raw.error,1024,'test error')});
}
export const listWebhookDeliveries=async(t:ApiAccessTransport,webhookId:string,options:Readonly<{cursor?:string}>={},signal?:AbortSignal):Promise<WebhookDeliveryPage>=>parseWebhookDeliveryPage(await t.request<unknown>(serverPath('GET /v1/admin/webhooks/{id}/deliveries',{id:webhookId,query:{cursor:options.cursor}}),'GET',undefined,signal));
