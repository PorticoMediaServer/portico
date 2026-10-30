import {routePath} from "../../contracts/src/route-paths.ts";
import {unreadableServerResponse} from './server-messages.ts';
import {oneOfOr} from './tolerant.ts';
/** Versioned, renderer-independent linear feature DTOs. Credentials are write-only. */
import type {ChannelApi} from './channel-guide';
export const LINEAR_PROTOCOL='1.0' as const;
export type SourceKind='m3u'|'xtream'|'hdhomerun';
export type SourceAccess='owner-only'|'server-members';
export type ChannelMapping=Readonly<{channelKey:string;guideKey:string}>;
export type RemoteSourceDraft={enabledGroups?:string[];enabledChannels?:string[];id:string;expectedRevision:number;name:string;kind:SourceKind;locator:string;guideUrl:string;username:string;password:string;confirmedLanRoots:string[];mappings:ChannelMapping[];refreshMs:number;ownerLimit:number;useDiscoveredCapacity:boolean;viewerAccess:SourceAccess};
export type RemoteSourcePreview=Readonly<{previewId:string;preview:{diagnostics:{skippedChannels:number;skippedProgrammes:number;adjustedProgrammes:number;examples:readonly string[]};channels:number;programmes:number;availableStart:string;availableEnd:string;names:string[]};discoveredCapacity:number;capacityMode:'defaulted'|'discovered'|'configured';mappingKeys:readonly {channelKey:string;name:string;guideKey:string}[];expiresAt:string}>;
export type RefreshStatus=Readonly<{sourceId:string;state:'healthy'|'degraded'|'credentials-required'|'refreshing';errorCode:string;nextRefresh:string;failures:number}>;
export function remoteSourceDraft(id:string):RemoteSourceDraft{return{id,expectedRevision:0,name:'',kind:'m3u',locator:'',guideUrl:'',username:'',password:'',confirmedLanRoots:[],mappings:[],refreshMs:21_600_000,ownerLimit:0,useDiscoveredCapacity:false,viewerAccess:'owner-only'}}
export const isRecord=(v:unknown):v is Record<string,unknown>=>v!==null&&typeof v==='object'&&!Array.isArray(v);
export const boundedText=(v:unknown,max=512):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x1f\x7f]/.test(v);
export const safeCounter=(v:unknown,max=Number.MAX_SAFE_INTEGER):v is number=>Number.isSafeInteger(v)&&Number(v)>=0&&Number(v)<=max;
export const utcInstant=(v:unknown):v is string=>typeof v==='string'&&/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?Z$/.test(v)&&Number.isFinite(Date.parse(v));
export function envelope(v:unknown,serverId:string):Record<string,unknown>{if(!isRecord(v)||v.serverId!==serverId)throw new Error(unreadableServerResponse);return v}
function bad():never{throw new Error(unreadableServerResponse)}
export function parseRemoteSourcePreview(raw:unknown,serverId:string):RemoteSourcePreview{
 const v=envelope(raw,serverId).remotePreview;if(!isRecord(v)||!boundedText(v.previewId,48)||v.previewId.length!==48||!isRecord(v.preview)||!safeCounter(v.preview.channels,100000)||v.preview.channels<1||!safeCounter(v.preview.programmes,10000000)||!boundedText(v.preview.availableStart,64)||!boundedText(v.preview.availableEnd,64)||!Array.isArray(v.preview.names)||v.preview.names.length!==Math.min(100,v.preview.channels)||v.preview.names.some(x=>!boundedText(x,256))||!safeCounter(v.discoveredCapacity,256)||!utcInstant(v.expiresAt)||!Array.isArray(v.mappingKeys)||v.mappingKeys.length!==v.preview.names.length)bad();
 const keys=new Set<string>();for(const k of v.mappingKeys){if(!isRecord(k)||!boundedText(k.channelKey,256)||!k.channelKey||keys.has(k.channelKey)||!boundedText(k.name,256)||!boundedText(k.guideKey,256))bad();keys.add(k.channelKey)}
 const d=v.preview.diagnostics;if(!isRecord(d)||!safeCounter(d.skippedChannels,100000000)||!safeCounter(d.skippedProgrammes,100000000)||!safeCounter(d.adjustedProgrammes,100000000)||!Array.isArray(d.examples)||d.examples.length>10||d.examples.some(x=>!boundedText(x,256)))bad();
 // How the capacity was decided is descriptive: a way this app does not know reads as the default.
 return {...v,capacityMode:oneOfOr('remote-source-preview','capacityMode',v.capacityMode,['defaulted','discovered','configured'] as const,'defaulted')} as unknown as RemoteSourcePreview;
}
export async function previewRemoteSource(api:ChannelApi,serverId:string,draft:RemoteSourceDraft,signal?:AbortSignal){return parseRemoteSourcePreview(await api.request(routePath("preview_remote_live_source"),'POST',draft,signal),serverId)}

/** CD-06: the local-network roots a preview asks the owner to confirm, when that is why it failed. */
export function lanConfirmationRoots(error:unknown):readonly string[]|undefined{
 if(!isRecord(error))return undefined;const code=typeof error.code==='string'?error.code:undefined;
 if(code!=='source_lan_confirmation_required')return undefined;
 const roots=(error as {confirmRoots?:unknown}).confirmRoots;return Array.isArray(roots)&&roots.length&&roots.every(r=>typeof r==='string')?roots as string[]:undefined;
}
/** The draft to preview again once the owner confirmed `roots`: every root confirmed so far, once each. */
export function withConfirmedLanRoots(draft:RemoteSourceDraft,roots:readonly string[]):RemoteSourceDraft{
 return {...draft,confirmedLanRoots:[...new Set([...draft.confirmedLanRoots,...roots])]};
}

/** Persisted acquisition settings. Credentials appear only in a write draft. */
export type PersistedRemoteConfiguration=Readonly<{kind:SourceKind;guideUrl:string;guideUrlRedacted:boolean;refreshMs:number;ownerLimit:number;useDiscoveredCapacity:boolean;username?:string;password?:string;confirmedLanRoots?:readonly string[]}>;
export function parsePersistedRemoteConfiguration(raw:unknown):PersistedRemoteConfiguration {
 if(!isRecord(raw)||!['m3u','xtream','hdhomerun'].includes(String(raw.kind))||!boundedText(raw.guideUrl,8192)||typeof raw.guideUrlRedacted!=='boolean'||!safeCounter(raw.refreshMs,86400000)||Number(raw.refreshMs)<300000||!safeCounter(raw.ownerLimit,256)||typeof raw.useDiscoveredCapacity!=='boolean')bad();
 // Never retain credentials or unknown provider fields from a response.
 return Object.freeze({kind:raw.kind as SourceKind,guideUrl:raw.guideUrl as string,guideUrlRedacted:raw.guideUrlRedacted as boolean,refreshMs:raw.refreshMs as number,ownerLimit:raw.ownerLimit as number,useDiscoveredCapacity:raw.useDiscoveredCapacity as boolean});
}
