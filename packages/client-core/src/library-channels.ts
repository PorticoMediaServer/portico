import {routeBody} from "../../contracts/src/routes.ts";
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {routePath} from "../../contracts/src/route-paths.ts";
import {unreadableServerResponse} from './server-messages.ts';
/** Library Channels are server-authored schedules, not client playlists. */
import type {ChannelApi} from './channel-guide';
import {boundedText,envelope,isRecord,safeCounter,utcInstant,LINEAR_PROTOCOL} from './linear-api.ts';
import {parseBrowseNode,type BrowseNode} from './browse.ts';
import {keepReadable} from './tolerant.ts';
/** What a rule plays (Spec — Custom Channels Builder §1). `filter` is the library browse
 * expression, evaluated against the movie or the show; `text` matches title and summary words;
 * pinned ids are movies, episodes or shows (a show pins all its episodes). `genres` and
 * `yearFrom`/`yearThrough` are first-version fields the server folds into `filter`. */
export type LibraryChannelQuery={libraryIds:string[];kinds:('movie'|'episode')[];showIds:string[];genres?:string[];yearFrom?:number;yearThrough?:number;recentDays:number;order:LibraryChannelOrder;limit:number;filter?:BrowseNode;text?:string;includeItemIds?:string[];excludeItemIds?:string[]};
export type LibraryChannelOrder='title'|'recent'|'oldest'|'year'|'episode'|'release'|'rating'|'least-played';
export type LibraryChannelRule={id:string;name:string;query:LibraryChannelQuery;mode:'sequential'|'shuffle-bag'|'weighted-random'|'sequential-then-shuffle';episodeMode:'none'|'in-order'|'marathon'|'randomized'|'rotate';exhaustion:'loop'|'slate';deduplicationWindow:number;maxConsecutive:number;weights:{itemId:string;weight:number}[]};
export type LibraryChannelBlock={id:string;weekdays:number[];startMinute:number;endMinute:number;priority:number;ruleId:string;fallbackRuleId:string;anchor:'channel-cursor'|'block-start';overrun:'finish'|'cut'|'fit'};
export type LibraryChannelConfig={protocolVersion:'1.0';id:string;name:string;description:string;enabled:boolean;position:number;timezone:string;seed:string;defaultRuleId:string;viewerAccess:'owner-only'|'server-members';quality:{mode:'automatic'|'original'|'limited';maxBitrate:number;maxHeight:number;allowLossy:boolean;allowHdrToSdr:boolean};logoItemId:string;overlay:{enabled:boolean;corner:string;sizePercent:number;insetPercent:number;treatment:string};rules:LibraryChannelRule[];blocks:LibraryChannelBlock[];templateId:string};
export type LibraryChannel={config:LibraryChannelConfig;revision:number;state:string;healthCode:string;generation:string;generatedThrough:string;candidateCount:number;unresolvedDurationCount:number;replacementBoundary:string};
export type LibraryChannelPreviewRule={ruleId:string;eligible:number;unresolved:number;semanticLimit:number;sample:{itemId:string;title:string;durationMs:number;/** Set for an episode: the show it belongs to (one sample per show). */showTitle?:string}[];durationMs:number;complete:boolean;scanned:number};
export type LibraryChannelPreviewEntry={id:string;startMs:number;endMs:number;itemId:string;title:string;unavailableReason:string};
/** `complete: false` means the counts stopped at the server's scan budget ("at least"). `firstDay`
 * is the first local day as saving would schedule it. */
export type LibraryChannelPreview={catalogFence:string;rules:LibraryChannelPreviewRule[];boundaryPolicy:string;blocks:readonly unknown[];window:unknown;firstDay:LibraryChannelPreviewEntry[];durationMs:number;complete:boolean};
export type LibraryChannelTemplate={id:string;name:string;description:string;minimumCandidates:number;applicable:boolean;reason:string;config:LibraryChannelConfig};
const id=(v:unknown):v is string=>typeof v==='string'&&/^[A-Za-z0-9_-]{1,128}$/.test(v);
const optionalID=(v:unknown):v is string=>v===''||id(v);
function invalid():never{throw new Error(unreadableServerResponse)}
function list(v:unknown,max:number,predicate:(x:unknown)=>boolean):v is unknown[]{return Array.isArray(v)&&v.length<=max&&v.every(predicate)}
function one(v:unknown,choices:readonly string[]){return typeof v==='string'&&choices.includes(v)}
/** Lists the server may omit or send as null (omitempty, nil slices) read as empty. */
function optionalList(v:Record<string,unknown>,key:string,max:number,predicate:(x:unknown)=>boolean):boolean{
 if(v[key]===undefined||v[key]===null){v[key]=[];return true;}
 return list(v[key],max,predicate);
}
function query(v:unknown):v is LibraryChannelQuery{
 if(!isRecord(v))return false;
 if(v.yearFrom===undefined)v.yearFrom=0;
 if(v.yearThrough===undefined)v.yearThrough=0;
 if(v.filter!==undefined&&v.filter!==null){try{parseBrowseNode(v.filter,0,'query.filter');}catch{return false;}}
 return list(v.libraryIds,64,id)&&list(v.kinds,2,x=>one(x,['movie','episode']))&&optionalList(v,'showIds',128,id)&&optionalList(v,'genres',16,x=>boundedText(x,120))&&optionalList(v,'includeItemIds',500,id)&&optionalList(v,'excludeItemIds',500,id)&&safeCounter(v.yearFrom,9999)&&safeCounter(v.yearThrough,9999)&&safeCounter(v.recentDays,36500)&&safeCounter(v.limit,1000000)&&(v.text===undefined||typeof v.text==='string'&&v.text.length<=200)&&one(v.order,['title','recent','oldest','year','episode','release','rating','least-played']);
}
function rule(v:unknown):v is LibraryChannelRule{
 if(!isRecord(v))return false;
 return id(v.id)&&boundedText(v.name,120)&&query(v.query)&&one(v.mode,['sequential','shuffle-bag','weighted-random','sequential-then-shuffle'])&&one(v.episodeMode,['none','in-order','marathon','randomized','rotate'])&&one(v.exhaustion,['loop','slate'])&&safeCounter(v.deduplicationWindow,1000)&&safeCounter(v.maxConsecutive,1000)&&v.maxConsecutive>0&&optionalList(v,'weights',1000,w=>isRecord(w)&&id(w.itemId)&&typeof w.weight==='number'&&Number.isFinite(w.weight)&&w.weight>0&&w.weight<=1000000);
}
function block(v:unknown):v is LibraryChannelBlock{
 if(!isRecord(v))return false;
 return id(v.id)&&list(v.weekdays,7,n=>safeCounter(n,6))&&safeCounter(v.startMinute,1439)&&safeCounter(v.endMinute,1439)&&Number.isSafeInteger(v.priority)&&Math.abs(Number(v.priority))<=100000&&id(v.ruleId)&&optionalID(v.fallbackRuleId)&&one(v.anchor,['channel-cursor','block-start'])&&one(v.overrun,['finish','cut','fit']);
}
export function parseLibraryChannelConfig(v:unknown):LibraryChannelConfig{
 if(!isRecord(v)||v.protocolVersion!==LINEAR_PROTOCOL||!id(v.id)||!boundedText(v.name,120)||!boundedText(v.description,2048)||typeof v.enabled!=='boolean'||!safeCounter(v.position,100000)||!boundedText(v.timezone,120)||!boundedText(v.seed,128)||!id(v.defaultRuleId)||!one(v.viewerAccess,['owner-only','server-members'])||!optionalID(v.logoItemId)||!boundedText(v.templateId,128)||!isRecord(v.quality)||!one(v.quality.mode,['automatic','original','limited'])||!safeCounter(v.quality.maxBitrate,200000000)||!safeCounter(v.quality.maxHeight,4320)||typeof v.quality.allowLossy!=='boolean'||typeof v.quality.allowHdrToSdr!=='boolean'||!isRecord(v.overlay)||typeof v.overlay.enabled!=='boolean'||!boundedText(v.overlay.corner,40)||!safeCounter(v.overlay.sizePercent,100)||!safeCounter(v.overlay.insetPercent,100)||!boundedText(v.overlay.treatment,40)||!list(v.rules,16,rule)||!list(v.blocks,64,block))invalid();
 return v as unknown as LibraryChannelConfig;
}
export function parseLibraryChannel(v:unknown):LibraryChannel{
 if(!isRecord(v))invalid();const config=parseLibraryChannelConfig(v.config);
 if(!safeCounter(v.revision)||v.revision<1||!boundedText(v.state,64)||!boundedText(v.healthCode,128)||!boundedText(v.generation,128)||!(v.generatedThrough===''||utcInstant(v.generatedThrough))||!safeCounter(v.candidateCount)||!safeCounter(v.unresolvedDurationCount)||!(v.replacementBoundary===''||utcInstant(v.replacementBoundary)))invalid();
 return {...v,config} as LibraryChannel;
}
function response(v:unknown,serverId:string){const r=envelope(v,serverId);if(r.protocolVersion!==LINEAR_PROTOCOL)invalid();return r}
export async function readLibraryChannels(api:ChannelApi,serverId:string,signal?:AbortSignal):Promise<LibraryChannel[]>{const r=response(await api.request(routePath("get_admin_library_channels"),'GET',undefined,signal),serverId);if(!Array.isArray(r.channels)||r.channels.length>64)invalid();
 // A channel is edited and saved whole, so its settings stay exact: one this app cannot read (a
 // setting from a newer server) is left out of the list alone, and the others can still be managed.
 return keepReadable('library-channels','channels',r.channels,parseLibraryChannel)}
export async function readLibraryTemplates(api:ChannelApi,serverId:string,signal?:AbortSignal):Promise<LibraryChannelTemplate[]>{const r=response(await api.request(routePath("get_admin_library_channels_templates"),'GET',undefined,signal),serverId);if(!Array.isArray(r.templates)||r.templates.length>64)invalid();return keepReadable('library-channels','templates',r.templates,v=>{if(!isRecord(v)||!id(v.id)||!boundedText(v.name,120)||!boundedText(v.description,2048)||!safeCounter(v.minimumCandidates,1000000)||typeof v.applicable!=='boolean'||!boundedText(v.reason,512))invalid();return {...v,config:parseLibraryChannelConfig(v.config)} as LibraryChannelTemplate})}
export async function previewLibraryChannel(api:ChannelApi,serverId:string,config:LibraryChannelConfig,signal?:AbortSignal):Promise<LibraryChannelPreview>{const r=response(await api.request(routePath("post_admin_library_channels_preview"),'POST',config,signal),serverId);const v=r.preview;if(!isRecord(v)||!boundedText(v.catalogFence,32768)||!boundedText(v.boundaryPolicy,256)||!Array.isArray(v.blocks)||v.blocks.length>768||!list(v.rules,16,r=>isRecord(r)&&id(r.ruleId)&&safeCounter(r.eligible)&&safeCounter(r.unresolved)&&safeCounter(r.semanticLimit,1000000)&&list(r.sample,8,s=>isRecord(s)&&id(s.itemId)&&boundedText(s.title,512)&&safeCounter(s.durationMs)&&(s.showTitle===undefined||boundedText(s.showTitle,512)))))invalid();
 // Builder fields (older servers omit them: counts were then complete and there was no first day).
 for(const rule of v.rules as Record<string,unknown>[]){if(!safeCounter(rule.durationMs??0)||typeof(rule.complete??true)!=='boolean')invalid();rule.durationMs??=0;rule.complete??=true;rule.scanned??=0;}
 if(v.firstDay===undefined||v.firstDay===null)v.firstDay=[];
 if(!list(v.firstDay,2000,e=>isRecord(e)&&boundedText(e.id,128)&&safeCounter(e.startMs)&&safeCounter(e.endMs)&&typeof e.itemId==='string'&&boundedText(e.title,512)&&typeof e.unavailableReason==='string'))invalid();
 v.durationMs??=(v.rules as {durationMs:number}[]).reduce((n,r)=>n+r.durationMs,0);v.complete??=true;
 return v as unknown as LibraryChannelPreview}
export async function saveLibraryChannel(api:ChannelApi,serverId:string,config:LibraryChannelConfig,expectedRevision:number,requestId:string,signal?:AbortSignal){return parseLibraryChannel(response(await api.request(routePath("post_admin_library_channels"),'POST',routeBody("post_admin_library_channels", {config,expectedRevision,requestId}),signal),serverId).channel)}
/** A new custom draft is server-selected from the current authorized catalog.
 * Client identities are used only for explicit subsequent editor mutations. */
export async function readLibraryDefaults(api:ChannelApi,serverId:string,timezone:string,signal?:AbortSignal):Promise<LibraryChannelConfig>{
 const v=envelope(await api.request<RouteResponse<"post_admin_library_channels_defaults">>(routePath("post_admin_library_channels_defaults"),'POST',routeBody("post_admin_library_channels_defaults", {timezone}),signal),serverId);
 return parseLibraryChannelConfig(v.config);
}
/** How a preview sample is named in "Some of what plays": its show for an episode, else its title. */
export function librarySampleLabel(sample:{title:string;showTitle?:string}):string{return sample.showTitle||sample.title;}
