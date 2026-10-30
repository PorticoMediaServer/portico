import {encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {parseServerInvitation,parsePeopleInvitationPage} from './direct-account.ts';
/**
 * Wire shapes for server administration: access tiers, invitations, per-member
 * limits, devices, API keys, message logs and connectivity.
 *
 * These parsers are the client's boundary against the server's administration
 * documents. Nothing here decides policy — the tier rules, the rating ladder and
 * the limit semantics all live on the server, and a client that guessed at them
 * would disagree with the server the moment either changed. What a parser does
 * is refuse a document it does not recognise, so a renderer never sees a
 * half-typed object.
 */
import {firstOfEach,keepReadable,oneOfOr} from './tolerant.ts';
import {parseCIDR} from './network-locality.ts';
export type AccessTier='owner'|'admin'|'member';
export type InvitationState='pending'|'accepted'|'revoked'|'expired';
export type LogLevel='error'|'warn'|'info'|'debug';
export type LogCategory='server'|'playback'|'scan'|'network'|'client';
export type RemoteSignInPolicy='allow'|'owner-only'|'off';
export type DiscoveryState='stopped'|'advertising'|'announcing'|'degraded'|'unavailable';

/**
 * Ratings a person's limit can be set to without naming a rating system, lowest first. The
 * server compares every limit by the age a rating stands for (PG and TV-PG are both eight and
 * up), so the order here is by age, and a limit may equally be any value of a published rating
 * system (`GET /v1/rating-systems`) sent with its `ratingSystem`.
 */
export const contentRatings=['TV-Y','G','TV-G','TV-Y7','PG','TV-PG','PG-13','TV-14','R','TV-MA','NC-17'] as const;
export const accessTiers=['owner','admin','member'] as const;
export const logLevels=['error','warn','info','debug'] as const;
export const logCategories=['server','playback','scan','network','client'] as const;


export type ScheduleWindow=Readonly<{days:readonly number[];startMinute:number;endMinute:number}>;
export type AccessSchedule=Readonly<{timezone:string;windows:readonly ScheduleWindow[]}>;
export type ChannelPolicy=Readonly<{mode:'all'|'allow'|'deny';channels:readonly string[]}>;
export type TagPolicy=Readonly<{deniedLabels:readonly string[]}>;
export type StreamLimitMode='inherit'|'unlimited'|'limit';
export type StreamLimitFacts=Readonly<{serverMaxStreams:number|null;effectiveMaxStreams:number|null}>;
export type MemberLimits=Readonly<{
 accessTokens:boolean;maxProfiles:number;allowRemote:boolean;conversion:'server'|'audio'|'none';
 maxStreams:number;maxStreamsMode:StreamLimitMode;remoteBitrateKbps:number;
 /** The rating system `maxContentRating` is a value of, or '' when it is a plain rating. */
 ratingSystem:string;maxContentRating:string;
 /** What the limit resolves to: the youngest age the rating admits, or -1 for no limit. Set by the server. */
 maximumAge:number;
 allowUnrated:boolean;
 schedule:AccessSchedule;channelPolicy:ChannelPolicy;tagPolicy:TagPolicy}>;
export type LimitsDocument=Readonly<{accountId:string;revision:number;updatedAt?:string;limits:MemberLimits}> & StreamLimitFacts;
/**
 * `disabledAt` and `disabledReason` are present while the account is off: when it was turned off,
 * and whether an owner or administrator did it (`owner`) or the person left at their Portico
 * Account (`left`).
 */
export type AccessMember=Readonly<{accountId:string;username:string;email?:string;displayName?:string;profileId:string;role:AccessTier;disabled:boolean;
 disabledAt?:string;disabledReason?:'owner'|'left'|'did_not_move';
 hostedAccountId?:string;notes?:string;joinedVia?:string;joinedAt?:string;profileNames?:readonly string[];lastActiveAt?:string;liveState?:'idle'|'watching'|'listening';
 revision:number;allowedLibraries:readonly string[];limitsRevision:number;limits:MemberLimits}> & StreamLimitFacts;
export type Invitation=Readonly<{id:string;email?:never;notes?:string;serverId?:string;role:'member'|'admin';allowedLibraries:readonly string[];state:InvitationState;
 createdAt:string;expiresAt:string;acceptedAt?:string;accountId?:string;revision:number;code?:string;
 /** The one Portico Account this invitation is for, when the owner named one. */
 porticoUsername?:string}>;
/**
 * One member's device as an administrator sees it. This is the device record a sign-in is bound
 * to. `username`, `app` and `appVersion` say whose it is and what it runs.
 */
export type AccessDevice=Readonly<{id:string;accountId:string;username:string;profileId:string;profileName?:string;name:string;platform:string;app:string;appVersion:string;lastNetwork:string;liveState:'idle'|'watching'|'listening';
 firstSeenAt:string;lastSeenAt:string;revision:number;sessionCount?:number}>;
export type LogRecord=Readonly<{sequence:string;at:string;level:LogLevel;category:LogCategory;message:string}>;
export type LogRetention=Readonly<{category:LogCategory;days:number}>;
export const LOG_DETAIL_MINUTES = [15,30,60,120,240] as const;
export type LogDetailMinutes = typeof LOG_DETAIL_MINUTES[number];
export type LogSettings=Readonly<{revision:number;logLevel:LogLevel;effectiveLevel:LogLevel;debugWindowUntil?:string;logDetailMinutes?:LogDetailMinutes;
 retention:readonly LogRetention[];levels:readonly string[];categories:readonly string[]}>;
export type ClientLogUpload=Readonly<{id:string;accountId:string;profileId:string;deviceId:string;platform:string;
 appVersion?:string;receivedAt:string;bytes:number;body?:string}>;
export type ConnectivityPolicy=Readonly<{revision:number;remoteSignInPolicy:RemoteSignInPolicy;remoteBitrateLimitKbps:number;
 connectionLimit:number;advertiseUrl:string;lanNetworks:readonly string[];accessUrls:readonly string[];lanDiscoveryEnabled:boolean;dlnaEnabled:boolean;
 trustedProxies:readonly string[];uploadCapacityKbps:number;pausedSessionTimeoutMinutes:number;advertisedInterface:string;
 /** The owner's own address switch: the drawn "Use your own address" row edits this row of the same policy. Optional for typed consumers that predate it; the parser always supplies it. */
 ownAddressEnabled?:boolean;
 customCertificatePath:string;customCertificateKeyPath:string;customCertificateDomain:string;
 /** "Secure connections": `required` refuses plain HTTP for everyone; `preferred` (the default) allows it. */
 secureConnections:'preferred'|'required'}>;
export type ConnectivityAddress=Readonly<{address:string;family:'ipv4'|'ipv6'|'hostname';class:'public'|'lan'|'loopback';source:'interface'|'accessUrl'}>;
export type ConnectivityInterface=Readonly<{name:string;up:boolean;loopback:boolean;virtual:boolean;addresses:readonly string[]}>;
export type CustomCertificateStatus=Readonly<{serving:boolean;state:'none'|'ready'|'error';errorCode?:string;domain?:string;notAfter?:string;issuer?:string}>;
export type ConnectivityTLS=Readonly<{configured:boolean;state?:string;errorCode?:string;dnsName?:string;issuer?:string;
 notAfter?:string;tlsReady:boolean;publiclyTrusted:boolean;listenerBound:boolean;listenPort:number;routeUrl?:string;reachability?:string;customCertificate?:CustomCertificateStatus}>;
export type ConnectivityDiscovery=Readonly<{enabled:boolean;supported:boolean;state:DiscoveryState;detail?:string;serviceType?:string}>;
export type ConnectionDocument=Readonly<{secure:boolean;remote:boolean;plainHttpOutsideHome:boolean;clientAddress:string;accessUrls:readonly string[];
 /** The owner's LAN networks (the one definition of home) and "Secure connections", when the server says. */
 lanNetworks?:readonly string[];secureConnections?:'preferred'|'required'}>;
export type ConnectivityStatus=Readonly<{connection:ConnectionDocument;observedAt:string;policy:ConnectivityPolicy;tls:ConnectivityTLS;discovery:ConnectivityDiscovery;
 identityCode?:string;localUrl?:string;addresses:readonly ConnectivityAddress[];interfaces:readonly ConnectivityInterface[];warnings:readonly string[];omittedAddresses:number;omittedInterfaces:number}>;
export type AdministrationPage<T>=Readonly<{limit:number;nextCursor:string;items:readonly T[]}>;

const obj=(x:unknown):x is Record<string,unknown>=>!!x&&typeof x==='object'&&!Array.isArray(x);
const invalid=():never=>{throw new Error('The administration response does not match this server. Refresh before continuing.');};
const str=(v:unknown,max=128,empty=false):string=>typeof v==='string'&&v.length<=max&&(empty||v.length>0)&&!/[\x00-\x1f]/.test(v)?v:invalid();
const num=(v:unknown,min=0,max=Number.MAX_SAFE_INTEGER):number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=min&&v<=max?v:invalid();
const bool=(v:unknown):boolean=>typeof v==='boolean'?v:invalid();
const when=(v:unknown):string=>{const value=str(v,64);if(!Number.isFinite(Date.parse(value)))invalid();return value;};
const optionalWhen=(v:unknown):string|undefined=>v===undefined||v===null||v===''?undefined:when(v);
const oneOf=<T extends string>(v:unknown,allowed:readonly T[]):T=>allowed.includes(v as T)?v as T:invalid();
const list=(v:unknown,max:number,each:(x:unknown)=>string):readonly string[]=>{
 if(v===undefined||v===null)return Object.freeze([]);
 if(!Array.isArray(v)||v.length>max)return invalid();
 return Object.freeze(v.map(each));
};
/**
 * The page size and the cursor stay exact, and a repeated row keeps its first appearance. On an
 * ordinary list a row this app cannot read is left out alone. The lists of who and what has access
 * (members, devices, keys) are `whole`: every row has to be readable or the page is refused. An
 * owner must never be shown a list of access with someone silently missing from it, nor a row with
 * a guessed role, trust or scope.
 */
const page=<T>(raw:unknown,each:(x:unknown)=>T,key:(row:T)=>string,whole=false):AdministrationPage<T>=>{
 if(!obj(raw)||!Array.isArray(raw.items))return invalid();
 const limit=num(raw.limit,1,1000);
 if(raw.items.length>limit)return invalid();
 return Object.freeze({limit,nextCursor:str(raw.nextCursor??'',4096,true),items:Object.freeze(firstOfEach('server-administration','page.items',whole?(raw.items as unknown[]).map(each):keepReadable('server-administration','page.items',raw.items,each),key))});
};

export function parseMemberLimits(raw:unknown):MemberLimits{
 if(!obj(raw))return invalid();
 const maxStreamsMode=oneOf(raw.maxStreamsMode,['inherit','unlimited','limit'] as const),maxStreams=num(raw.maxStreams,0,1000);
 if(maxStreamsMode==='limit'?maxStreams<1:maxStreams!==0)return invalid();
 const schedule=obj(raw.schedule)?raw.schedule:{};
 const channels=obj(raw.channelPolicy)?raw.channelPolicy:{};
 const tags=obj(raw.tagPolicy)?raw.tagPolicy:{};
 const windows=Array.isArray(schedule.windows)?schedule.windows:[];
 if(windows.length>64)invalid();
 return Object.freeze({
  accessTokens:bool(raw.accessTokens),maxProfiles:num(raw.maxProfiles,1,8),allowRemote:bool(raw.allowRemote),conversion:oneOf(raw.conversion,['server','audio','none'] as const),
  maxStreams,maxStreamsMode,
  remoteBitrateKbps:num(raw.remoteBitrateKbps??0,0,200000),
  // The rating is whatever the server stored: a value of the named system, or any rating its
  // table knows. It is bounded here and judged there; the age it resolves to comes back with it.
  ratingSystem:str(raw.ratingSystem??'',32,true),
  maxContentRating:str(raw.maxContentRating??'',32,true),
  maximumAge:raw.maximumAge===undefined?-1:num(raw.maximumAge,-1,21),
  allowUnrated:bool(raw.allowUnrated??true),
  schedule:Object.freeze({timezone:str(schedule.timezone??'',64,true),windows:Object.freeze(windows.map((w:unknown):ScheduleWindow=>{
   if(!obj(w)||!Array.isArray(w.days)||w.days.length<1||w.days.length>7)return invalid();
   return Object.freeze({days:Object.freeze(w.days.map((d:unknown)=>num(d,0,6))),startMinute:num(w.startMinute,0,1439),endMinute:num(w.endMinute,0,1440)});
  }))}),
  channelPolicy:Object.freeze({mode:oneOf(channels.mode??'all',['all','allow','deny'] as const),channels:list(channels.channels,512,x=>str(x,200))}),
  tagPolicy:Object.freeze({deniedLabels:list(tags.deniedLabels,256,x=>str(x,120))})});
}

const streamLimitFacts=(raw:Record<string,unknown>):StreamLimitFacts=>({serverMaxStreams:raw.serverMaxStreams===null?null:num(raw.serverMaxStreams,1,1000),effectiveMaxStreams:raw.effectiveMaxStreams===null?null:num(raw.effectiveMaxStreams,1,1000)});
export function parseLimitsDocument(raw:unknown,accountId?:string):LimitsDocument{
 if(!obj(raw))return invalid();
 const id=str(raw.accountId);
 if(accountId&&id!==accountId)invalid();
 return Object.freeze({accountId:id,revision:num(raw.revision,1),updatedAt:optionalWhen(raw.updatedAt),limits:parseMemberLimits(raw.limits),...streamLimitFacts(raw)});
}

export function parseAccessMember(raw:unknown):AccessMember{
 if(!obj(raw))return invalid();
 return Object.freeze({accountId:str(raw.accountId),username:str(raw.username,64),profileId:str(raw.profileId,128,true),
  ...(raw.email!==undefined?{email:str(raw.email,254,true)}:{}),...(raw.displayName!==undefined?{displayName:str(raw.displayName,120,true)}:{}),
  role:oneOf(raw.role,accessTiers),disabled:bool(raw.disabled??false),
  ...(raw.disabledAt?{disabledAt:when(raw.disabledAt)}:{}),...(raw.disabledReason?{disabledReason:oneOf(raw.disabledReason,['owner','left','did_not_move'] as const)}:{}),
  ...(raw.hostedAccountId!==undefined?{hostedAccountId:str(raw.hostedAccountId,128)}:{}),...(raw.notes!==undefined?{notes:str(raw.notes,2000,true)}:{}),
  ...(raw.joinedVia!==undefined?{joinedVia:str(raw.joinedVia,128,true)}:{}),...(raw.joinedAt?{joinedAt:when(raw.joinedAt)}:{}),
  ...(raw.profileNames!==undefined?{profileNames:list(raw.profileNames,8,x=>str(x,120))}:{}),...(raw.lastActiveAt?{lastActiveAt:when(raw.lastActiveAt)}:{}),
  ...(raw.liveState!==undefined?{liveState:oneOf(raw.liveState,['idle','watching','listening'] as const)}:{}),
  revision:num(raw.revision,1),
  allowedLibraries:list(raw.allowedLibraries,1000,x=>str(x)),limitsRevision:num(raw.limitsRevision,1),limits:parseMemberLimits(raw.limits),...streamLimitFacts(raw)});
}
export const parseAccessMemberPage=(raw:unknown)=>page(raw,parseAccessMember,row=>row.accountId,true);

/**
 * What deleting a member removes, read before the owner confirms. Media and recordings are never
 * deleted with a person. `playsKept` rows of the owner's play history stay, with the person's
 * names blanked.
 */
export type MemberDeletionPreview=Readonly<{accountId:string;username:string;role:AccessTier;revision:number;porticoAccount:boolean;
 profiles:number;devices:number;playlists:number;savedResources:number;playsKept:number}>;
export function parseMemberDeletionPreview(raw:unknown):MemberDeletionPreview{
 if(!obj(raw))return invalid();
 return Object.freeze({accountId:str(raw.accountId),username:str(raw.username,64),role:oneOf(raw.role,accessTiers),revision:num(raw.revision,1),
  porticoAccount:bool(raw.porticoAccount),profiles:num(raw.profiles),devices:num(raw.devices),playlists:num(raw.playlists),
  savedResources:num(raw.savedResources),playsKept:num(raw.playsKept)});
}
type MemberDeletionApi=Readonly<{request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>}>;
const memberPath=(accountId:string)=>encodedRoutePath("delete_direct_member", {"id": encodeURIComponent(str(accountId))});
/** Reads what deleting this member will remove. Changes nothing. */
export async function previewMemberDeletion(api:MemberDeletionApi,accountId:string,signal?:AbortSignal):Promise<MemberDeletionPreview>{
 const preview=parseMemberDeletionPreview(await api.request<unknown>(memberPath(accountId)+'/deletion-preview','GET',undefined,signal));
 return preview.accountId===accountId?preview:invalid();
}
/**
 * Deletes the member the preview describes. The server refuses (412 `revision_mismatch`) when the
 * member changed since the preview was read: read it again and confirm again.
 */
export async function deleteMember(api:MemberDeletionApi,preview:MemberDeletionPreview,signal?:AbortSignal):Promise<void>{
 await api.request<void>(memberPath(preview.accountId),'DELETE',{expectedRevision:preview.revision},signal);
}

export function parseInvitation(raw:unknown):Invitation{
 const value=parseServerInvitation(raw);
 return Object.freeze({...value,role:value.role as 'member'|'admin',state:value.state as InvitationState});
}
export const parseInvitationPage=(raw:unknown)=>{const value=parsePeopleInvitationPage(raw);return Object.freeze({...value,items:Object.freeze(value.items.map(parseInvitation))});};

export function parseAccessDevice(raw:unknown):AccessDevice{
 if(!obj(raw)||Object.hasOwn(raw,'trust'))return invalid();
 return Object.freeze({id:str(raw.id),accountId:str(raw.accountId),profileId:str(raw.profileId??'',128,true),
  username:str(raw.username??'',64,true),name:str(raw.name??'',120,true),platform:str(raw.platform??'',64,true),
  app:str(raw.app??'',64,true),appVersion:str(raw.appVersion??'',64,true),lastNetwork:str(raw.lastNetwork,256,true),liveState:oneOf(raw.liveState,['idle','watching','listening'] as const),...(raw.profileName!==undefined?{profileName:str(raw.profileName,120,true)}:{}),
  firstSeenAt:when(raw.firstSeenAt),lastSeenAt:when(raw.lastSeenAt),revision:num(raw.revision,1),
  ...(raw.sessionCount!==undefined?{sessionCount:num(raw.sessionCount)}:{})});
}
export function parseAccessDevicePage(raw:unknown):AdministrationPage<AccessDevice>{
 if(!obj(raw)||Object.hasOwn(raw,'approvalRequired'))return invalid();
 return page(raw,parseAccessDevice,row=>row.id,true);
}
/** Ends only this person's captured device family/families; future normal sign-in is allowed. */
export async function signOutMemberDevice(api:MemberDeletionApi,accountId:string,deviceId:string,signal?:AbortSignal):Promise<void>{
 await api.request(encodedRoutePath('post_direct_member_device_sign_out',{id:encodeURIComponent(str(accountId)),deviceId:encodeURIComponent(str(deviceId))}),'POST',undefined,signal);
}
export async function signOutMemberDevices(api:MemberDeletionApi,accountId:string,signal?:AbortSignal):Promise<void>{
 await api.request(encodedRoutePath('post_direct_member_devices_sign_out',{id:encodeURIComponent(str(accountId))}),'POST',undefined,signal);
}


export function parseLogRecord(raw:unknown):LogRecord{
 if(!obj(raw))return invalid();
 // The sequence is a string on the wire because it is a 64-bit counter; parsing
 // it as a number would lose precision on a long-running server.
 const sequence=str(raw.sequence,20);
 if(!/^[0-9]+$/.test(sequence))invalid();
 // A line at a level or in a category this app does not know is still a line the owner should see:
 // it reads as an informational line of the server's own category.
 return Object.freeze({sequence,at:when(raw.at),level:oneOfOr('server-administration','log.level',raw.level,logLevels,'info'),category:oneOfOr('server-administration','log.category',raw.category,logCategories,'server'),message:str(raw.message,8192,true)});
}
export function parseLogPage(raw:unknown):AdministrationPage<LogRecord>&{level:LogLevel;effectiveLevel:LogLevel}{
 if(!obj(raw))return invalid();
 return Object.freeze({...page(raw,parseLogRecord,row=>row.sequence),level:oneOfOr('server-administration','log-page.level',raw.level,logLevels,'info'),effectiveLevel:oneOfOr('server-administration','log-page.effectiveLevel',raw.effectiveLevel,logLevels,'info')});
}

/**
 * The administrator TV Troubleshooting page's closed vocabulary. Each code is one
 * exact maintenance action the server records in its own audit trail, and each is
 * emitted only by a server-wide owner or administrator path. One viewer's own
 * download work is deliberately absent, so its timing never reaches an
 * administrator. The server omits any action outside this list, and this parser
 * drops an unknown code rather than rendering a sentence that would misstate what
 * happened.
 *
 * A control code records that the action was accepted, not that the work behind it
 * finished: a cancel may still be acknowledged by the producer, a resume puts the
 * task back in the queue, and a retry admits a fresh run. Only `settings.apply`,
 * `downloads.settings`, `schedule.apply` and `alert.acknowledge` record a completed
 * change, so their copy may say the setting or alert changed.
 */
export const adminServerEventCodes=['alert.acknowledge','downloads.settings','job.cancel','job.pause','job.resume','job.retry','optimization.cancel','optimization.delete','optimization.request','optimization.retry','schedule.apply','settings.apply'] as const;
export type AdminServerEventCode=typeof adminServerEventCodes[number];
export type AdminServerEvent=Readonly<{at:string;code:AdminServerEventCode}>;

/** The bounded list is the latest eligible events, not the latest log lines. A
 * document that claims more than the eight-record bound is refused, and a record
 * carries only its time and its code: no actor, target, path, title or token. */
export function parseAdminServerEvents(raw:unknown):readonly AdminServerEvent[]{
 if(!obj(raw))return invalid();
 const items=raw.items;
 if(!Array.isArray(items))return invalid();
 if(raw.limit!==8||items.length>8)return invalid();
 const out:AdminServerEvent[]=[];
 for(const entry of items){
  if(!obj(entry))return invalid();
  if(typeof entry.code!=='string'||!(adminServerEventCodes as readonly string[]).includes(entry.code))continue;
  out.push(Object.freeze({at:when(entry.at),code:entry.code as AdminServerEventCode}));
 }
 return Object.freeze(out);
}

export function parseLogSettings(raw:unknown):LogSettings{
 if(!obj(raw))return invalid();
 if(raw.logDetailMinutes!==undefined&&(typeof raw.logDetailMinutes!=='number'||!(LOG_DETAIL_MINUTES as readonly number[]).includes(raw.logDetailMinutes)))return invalid();
 const retention=Array.isArray(raw.retention)?raw.retention:[];
 if(retention.length>logCategories.length)invalid();
 return Object.freeze({revision:num(raw.revision,1),logLevel:oneOf(raw.logLevel,logLevels),effectiveLevel:oneOf(raw.effectiveLevel,logLevels),
  debugWindowUntil:optionalWhen(raw.debugWindowUntil),...(raw.logDetailMinutes===undefined?{}:{logDetailMinutes:raw.logDetailMinutes as LogDetailMinutes}),
  retention:Object.freeze(retention.map((entry:unknown):LogRetention=>{
   if(!obj(entry))return invalid();
   return Object.freeze({category:oneOf(entry.category,logCategories),days:num(entry.days,1,90)});
  })),
  levels:list(raw.levels,16,x=>str(x,16)),categories:list(raw.categories,32,x=>str(x,32))});
}

export function parseClientLogUpload(raw:unknown):ClientLogUpload{
 if(!obj(raw))return invalid();
 return Object.freeze({id:str(raw.id,128),accountId:str(raw.accountId),profileId:str(raw.profileId??'',128,true),
  deviceId:str(raw.deviceId??'',128,true),platform:str(raw.platform??'',60,true),
  ...(raw.appVersion?{appVersion:str(raw.appVersion,60)}:{}),receivedAt:when(raw.receivedAt),bytes:num(raw.bytes,0),
  ...(typeof raw.body==='string'?{body:raw.body.length<=262144?raw.body:invalid()}:{})});
}
export const parseClientLogUploadPage=(raw:unknown)=>page(raw,parseClientLogUpload,row=>row.id);

export function parseConnectivityPolicy(raw:unknown):ConnectivityPolicy{
 if(!obj(raw))return invalid();
 return Object.freeze({revision:num(raw.revision,1),remoteSignInPolicy:oneOf(raw.remoteSignInPolicy,['allow','owner-only','off'] as const),
  remoteBitrateLimitKbps:num(raw.remoteBitrateLimitKbps??0,0,200000),
  connectionLimit:num(raw.connectionLimit,0,65536),advertiseUrl:str(raw.advertiseUrl,300,true),
  lanNetworks:list(raw.lanNetworks,64,x=>str(x,64)),accessUrls:list(raw.accessUrls,32,x=>str(x,300)),
  lanDiscoveryEnabled:bool(raw.lanDiscoveryEnabled??false),
  // Added 18 Oct; an older server has no DLNA switch, which is off.
  dlnaEnabled:bool(raw.dlnaEnabled??false),
  // Rows added 25 Sep; an older server omits them, so each has its shipped default.
  trustedProxies:list(raw.trustedProxies??[],32,x=>str(x,64)),
  uploadCapacityKbps:num(raw.uploadCapacityKbps??0,0,10000000),pausedSessionTimeoutMinutes:num(raw.pausedSessionTimeoutMinutes??0,0,1440),
  advertisedInterface:str(raw.advertisedInterface??'',64,true),ownAddressEnabled:raw.ownAddressEnabled===undefined?false:bool(raw.ownAddressEnabled),customCertificatePath:str(raw.customCertificatePath??'',1024,true),
  customCertificateKeyPath:str(raw.customCertificateKeyPath??'',1024,true),customCertificateDomain:str(raw.customCertificateDomain??'',253,true),
  secureConnections:oneOf(raw.secureConnections??'preferred',['preferred','required'] as const)});
}

function parseCustomCertificate(raw:Record<string,unknown>):CustomCertificateStatus{
 return Object.freeze({serving:bool(raw.serving??false),state:oneOf(raw.state??'none',['none','ready','error'] as const),...(raw.errorCode?{errorCode:str(raw.errorCode,64)}:{}),
  ...(raw.domain?{domain:str(raw.domain,253)}:{}),notAfter:optionalWhen(raw.notAfter),...(raw.issuer?{issuer:str(raw.issuer,253)}:{})});
}

export function parseConnectivityStatus(raw:unknown):ConnectivityStatus{
 if(!obj(raw))return invalid();
 const tls=obj(raw.tls)?raw.tls:{};
 const discovery=obj(raw.discovery)?raw.discovery:{};
  const addresses=Array.isArray(raw.addresses)?raw.addresses:[];
  const interfaces=Array.isArray(raw.interfaces)?raw.interfaces:[];
  // CD-39: the server enumerates every host interface without a cap. The whole document stays
  // readable: retain a bounded display slice with exact omitted counts instead of refusing it.
  // An address or interface this app cannot read is left out and counted with the omitted ones.
  const shownAddresses=keepReadable('server-administration','connectivity.addresses',addresses.slice(0,256),(entry:unknown):ConnectivityAddress=>{
   if(!obj(entry))return invalid();
   return Object.freeze({address:str(entry.address,253),family:oneOf(entry.family,['ipv4','ipv6','hostname'] as const),
    class:oneOf(entry.class,['public','lan','loopback'] as const),source:oneOf(entry.source,['interface','accessUrl'] as const)});
  });
  for(const entry of interfaces.slice(0,128))if(obj(entry)&&Array.isArray(entry.addresses)&&entry.addresses.length>64)invalid();
  const shownInterfaces=keepReadable('server-administration','connectivity.interfaces',interfaces.slice(0,128),(entry:unknown):ConnectivityInterface=>{
   if(!obj(entry))return invalid();
   return Object.freeze({name:str(entry.name,128),up:bool(entry.up??false),loopback:bool(entry.loopback??false),virtual:bool(entry.virtual??false),addresses:list(entry.addresses,64,x=>str(x,64))});
  });
  const omittedAddresses=addresses.length-shownAddresses.length;
  const omittedInterfaces=interfaces.length-shownInterfaces.length;
  // A custom certificate whose state this app cannot read is left off; the rest of the TLS status is kept.
  const customCertificate=obj(tls.customCertificate)?keepReadable('server-administration','connectivity.customCertificate',[tls.customCertificate],entry=>parseCustomCertificate(entry as Record<string,unknown>))[0]:undefined;
  // What the server says about this very connection is only shown: when it cannot be read, nothing is claimed about it.
  const connection=keepReadable('server-administration','connectivity.connection',[raw.connection],parseConnectionDocument)[0]??NO_CONNECTION_FACTS;
 return Object.freeze({connection,observedAt:when(raw.observedAt),policy:parseConnectivityPolicy(raw.policy),
  ...(raw.identityCode===undefined?{}:{identityCode:str(raw.identityCode,256)}),
  ...(raw.localUrl===undefined?{}:{localUrl:str(raw.localUrl,2048)}),
  tls:Object.freeze({configured:bool(tls.configured??false),...(tls.state?{state:str(tls.state,64)}:{}),
   ...(tls.errorCode?{errorCode:str(tls.errorCode,64)}:{}),...(tls.dnsName?{dnsName:str(tls.dnsName,253)}:{}),
   ...(tls.issuer?{issuer:str(tls.issuer,253)}:{}),notAfter:optionalWhen(tls.notAfter),
   tlsReady:bool(tls.tlsReady??false),publiclyTrusted:bool(tls.publiclyTrusted??false),
   listenerBound:bool(tls.listenerBound??false),listenPort:num(tls.listenPort??0,0,65535),
   ...(tls.routeUrl?{routeUrl:str(tls.routeUrl,2048)}:{}),...(tls.reachability?{reachability:str(tls.reachability,64)}:{}),
   ...(customCertificate?{customCertificate}:{})}),
  discovery:Object.freeze({enabled:bool(discovery.enabled??false),supported:bool(discovery.supported??false),
   state:oneOfOr('server-administration','connectivity.discovery.state',discovery.state,['stopped','advertising','announcing','degraded','unavailable'] as const,'unavailable'),
   ...(discovery.detail?{detail:str(discovery.detail,512)}:{}),...(discovery.serviceType?{serviceType:str(discovery.serviceType,128)}:{})}),
  addresses:Object.freeze(shownAddresses),
  interfaces:Object.freeze(shownInterfaces),
  warnings:list(raw.warnings,32,x=>str(x,128)),omittedAddresses,omittedInterfaces});
}

/**
 * grantsTier mirrors the server's single tier predicate so a client can hide a
 * control the server would refuse. It is a rendering convenience, never an
 * authorization decision: the server re-checks every request.
 */
export function grantsTier(role:string,needed:AccessTier):boolean{
 const rank=(v:string)=>v==='owner'?3:v==='admin'?2:v==='member'?1:0;
 return rank(role)>0&&rank(role)>=rank(needed);
}

/** managesTier mirrors identity.ManagesTier: an admin manages strictly below itself. */
export function managesTier(actor:string,target:string):boolean{
 if(!grantsTier(actor,'admin'))return false;
 if(actor==='owner')return target!=='owner';
 return target==='member';
}


/**
 * parseLogEvent reads one server-sent event payload from the live log tail. The
 * tail also emits ": keep-alive" comments, which an EventSource never surfaces
 * as data, so a caller only ever passes real payloads here.
 */
export function parseLogEvent(data:string):LogRecord{
 if(data.length>16384)invalid();
 let raw:unknown;
 try{raw=JSON.parse(data);}catch{return invalid();}
 return parseLogRecord(raw);
}

const NO_CONNECTION_FACTS:ConnectionDocument=Object.freeze({secure:false,remote:false,plainHttpOutsideHome:false,clientAddress:'',accessUrls:Object.freeze([])});
/**
 * What the server says about the connection a request arrived on. Every fact is only shown: a
 * yes/no that is missing or not a yes/no reads as no, an address that cannot be read as none, and
 * an advertised address that cannot be read is left out alone. Plain HTTP outside the home
 * networks is never claimed for a connection the server calls secure. The bound of 16 addresses is exact.
 */
export function parseConnectionDocument(raw:unknown):ConnectionDocument {
 if(!obj(raw))return invalid();
 const secure=raw.secure===true;
 const address=typeof raw.clientAddress==='string'&&raw.clientAddress.length<=128&&!/[\x00-\x1f]/.test(raw.clientAddress)?raw.clientAddress:'';
 const offered=raw.accessUrls===undefined||raw.accessUrls===null?[]:raw.accessUrls;if(!Array.isArray(offered)||offered.length>16)return invalid();
 return Object.freeze({secure,remote:raw.remote===true,plainHttpOutsideHome:raw.plainHttpOutsideHome===true&&!secure,clientAddress:address,accessUrls:Object.freeze(keepReadable('server-administration','connection.accessUrls',offered,x=>typeof x==='string'&&x.length>0&&x.length<=300&&!/[\x00-\x1f]/.test(x)?x:undefined)),
  ...(Array.isArray(raw.lanNetworks)&&raw.lanNetworks.length<=64?{lanNetworks:Object.freeze(raw.lanNetworks.filter((x):x is string=>typeof x==='string'&&!!parseCIDR(x)))}:{}),
  ...(raw.secureConnections==='preferred'||raw.secureConnections==='required'?{secureConnections:raw.secureConnections}:{})});
}
