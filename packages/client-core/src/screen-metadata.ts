import {unreadableServerResponse} from './server-messages.ts';
import {firstOfEach,keepReadable,noteSkipped,oneOfOr} from './tolerant.ts';
import type {MetadataReviewCandidate,MetadataReviewData,MetadataReviewScope,MetadataReviewTarget,MetadataReviewStatus,MetadataReviewAction} from './metadata-review.ts';

export type ScreenProvider='tmdb'|'tvdb'|'anilist';
export type ScreenIdentity=Readonly<{provider:ScreenProvider;type:'movie'|'show'|'episode'|'anime'|'season'|'person'|'collection'|'company'|'studio'|'character';id:string}>;
export type ScreenOrder=Readonly<{id:string;name:string}>;
export type ScreenPolicy=Readonly<{libraryId:string;libraryKind:'movie'|'tv'|'anime';revision:number;consentRevision:number;confirmed:boolean;enabled:boolean;providers:readonly ScreenProvider[];availableProviders:readonly ScreenProvider[];language:string;region:string;refreshMode:'replace_unlocked'|'fill_missing';disclosure:string;disclosureVersion:string}>;
export type ScreenPolicyInput=Readonly<{enabled:boolean;providers:readonly ScreenProvider[];language:string;region:string;refreshMode:'replace_unlocked'|'fill_missing';confirmRemote?:boolean;disclosureVersion:string}>;
export type ScreenRelation=Readonly<{kind:string;target:ScreenIdentity;name?:string}>;
export type ScreenField=Readonly<{name:string;value:unknown;provider:string;sourceKind:string;sourceUrl:string;confidence:number;observedAt:string;language:string;region:string;selectionRevision:number}>;
export type ScreenMetadataContext=Readonly<{policy:ScreenPolicy;explanation:string;selected:ScreenIdentity|null;selectionMode:string;selectionRevision:number;accepted?:Readonly<{identity:ScreenIdentity;title:string;relations:readonly ScreenRelation[];orders:readonly ScreenOrder[]}>;acceptedAt:string;acceptedConfidence:number;fields:readonly ScreenField[];localIssues:readonly string[];seasons:readonly Readonly<{number:number;anilistId:string;revision:number}>[];parentId:string;episodeStatus:string}>;
const providers:readonly string[]=['tmdb','tvdb','anilist'];
const statuses:readonly string[]=['pending','searching','pending_children','needs_selection','matched','matched_work','unmatched','needs_consent','source_unavailable','provider_disabled','needs_parent_match','needs_season_mapping','identity_conflict','manual_preserved','delegated_tvdb','needs_order','unavailable'];
const obj=(v:unknown):v is Record<string,unknown>=>typeof v==='object'&&v!==null&&!Array.isArray(v);
const text=(v:unknown,n=4096):v is string=>typeof v==='string'&&v.length<=n&&!/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(v);
const int=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
const score=(v:unknown):v is number=>typeof v==='number'&&Number.isFinite(v)&&v>=0&&v<=1;
function invalid():never{throw Object.assign(new Error(unreadableServerResponse),{code:'invalid_metadata_review',retryable:false});}
function list(v:unknown,max:number):unknown[]{if(!Array.isArray(v)||v.length>max)invalid();return v;}
function string(v:unknown,max=4096):string{if(!text(v,max))invalid();return v;}
function identifier(v:unknown):ScreenIdentity{if(!obj(v)||!providers.includes(v.provider as string)||!['movie','show','episode','anime','season','person','collection','company','studio','character'].includes(v.type as string)||typeof v.id!=='string'||!/^[1-9][0-9]{0,15}$/.test(v.id)||!Number.isSafeInteger(Number(v.id))||(v.provider==='anilist'?(!['anime','studio','character','person'].includes(v.type as string)||Number(v.id)>2147483647):!['movie','show','episode','season','person','collection','company'].includes(v.type as string)))invalid();return Object.freeze({provider:v.provider as ScreenProvider,type:v.type as ScreenIdentity['type'],id:v.id});}
function url(v:unknown):string{const value=string(v,2048);if(!value)return '';try{const u=new URL(value);if(u.protocol!=='https:'||u.username||u.password||u.search||u.hash||!['www.themoviedb.org','thetvdb.com','anilist.co'].includes(u.hostname))invalid();return value;}catch{invalid();}}
/** An episode order that cannot be read is not offered; the rest are. */
function orderList(v:unknown):readonly ScreenOrder[]{return Object.freeze(keepReadable('screen-metadata','orders',list(v??[],64),o=>{if(!obj(o))invalid();const id=string(o.id,160),name=string(o.name,2048);if(!id)invalid();return Object.freeze({id,name});}));}
/** The providers this app knows, each once. One a newer server adds is left out of the list; the list stays short. */
function providerList(v:unknown):readonly ScreenProvider[]{const given=list(v,16),kept=[...new Set(given.filter(x=>providers.includes(x as string)))] as ScreenProvider[];if(kept.length!==given.length)noteSkipped('screen-metadata','providers',given.length-kept.length);return Object.freeze(kept);}
function policy(raw:unknown,t:MetadataReviewTarget):ScreenPolicy{
 if(!obj(raw)||raw.libraryId!==t.libraryId||!['movie','tv','anime'].includes(raw.libraryKind as string)||!int(raw.revision)||raw.revision<1||!int(raw.consentRevision)||raw.consentRevision<1||typeof raw.confirmed!=='boolean'||typeof raw.enabled!=='boolean'||!['replace_unlocked','fill_missing'].includes(raw.refreshMode as string))invalid();
 return Object.freeze({libraryId:t.libraryId,libraryKind:raw.libraryKind as ScreenPolicy['libraryKind'],revision:raw.revision,consentRevision:raw.consentRevision,confirmed:raw.confirmed,enabled:raw.enabled,providers:providerList(raw.providers),availableProviders:providerList(raw.availableProviders),language:string(raw.language,64),region:string(raw.region,2),refreshMode:raw.refreshMode as ScreenPolicy['refreshMode'],disclosure:string(raw.disclosure,8192),disclosureVersion:string(raw.disclosureVersion,128)});
}
export function screenTarget(t:MetadataReviewTarget):boolean{return t.kind==='movie'||t.provider==='screen';}
export function parseScreenMetadata(raw:unknown,s:MetadataReviewScope,t:MetadataReviewTarget):MetadataReviewData{
 if(!obj(raw)||raw.serverId!==s.serverId||raw.libraryId!==t.libraryId||raw.entityId!==t.entityId||raw.kind!==(t.kind==='show'?'show':'item')||!text(raw.viewerFence,1024)||!raw.viewerFence||!int(raw.revision)||raw.revision<1||!int(raw.selectionRevision)||raw.selectionRevision<1||!int(raw.attempts)||typeof raw.status!=='string')invalid();
 // A status this app does not know reads as unresolved, and an action it does not know is not offered: the candidates and what can be done still come from the server.
 const status=oneOfOr('screen-metadata','status',raw.status,statuses as readonly MetadataReviewStatus[],'unresolved');
 const offered=list(raw.actions,16);let actions=[...new Set(offered.filter(a=>['select','retry','search','policy','map_season'].includes(a as string)))] as MetadataReviewAction[];if(actions.length!==offered.length)noteSkipped('screen-metadata','actions',offered.length-actions.length);
 const p=policy(raw.policy,t);
 let selected:ScreenIdentity|null=null;if(!obj(raw.selected))invalid();if(raw.selected.id!=='')selected=identifier(raw.selected);
 // A candidate that cannot be read is left out alone, and a repeated one keeps its first row.
 const candidates:MetadataReviewCandidate[]=firstOfEach('screen-metadata','candidates',keepReadable('screen-metadata','candidates',list(raw.candidates,75),(c):MetadataReviewCandidate=>{
  if(!obj(c)||!text(c.key,64)||!/^[0-9a-f]{64}$/.test(c.key)||!providers.includes(c.provider as string)||!score(c.confidence)||!int(c.strongSignals)||typeof c.contradiction!=='boolean'||!int(c.year))invalid();
  const identity=identifier({provider:c.provider,type:c.type,id:c.providerId});const reasons=list(c.reasons,16).map(r=>string(r,256));
  return Object.freeze({id:c.key,title:string(c.title,2048),subtitle:[String(c.provider).toUpperCase(),c.year?String(c.year):'',string(c.format,64)].filter(Boolean).join(' · '),overview:string(c.overview,8192),confidence:c.confidence,reasons:Object.freeze(reasons),observedAt:string(c.observedAt,128),provenance:Object.freeze({year:c.year}),screen:Object.freeze({identity,orders:orderList(c.orders),contradiction:c.contradiction,strongSignals:c.strongSignals,sourceKind:string(c.sourceKind,128),sourceUrl:url(c.sourceUrl),attribution:string(c.attribution,4096)})});
 }),c=>c.id);
 // With no readable candidate there is nothing to select, so Select is not offered.
 if(!candidates.length)actions=actions.filter(a=>a!=='select');
 let accepted:ScreenMetadataContext['accepted'];if(raw.accepted!==undefined){const a=raw.accepted;if(!obj(a))invalid();const relations=keepReadable('screen-metadata','accepted.relations',list(a.relations??[],256),r=>{if(!obj(r))invalid();return Object.freeze({kind:string(r.kind,100),target:identifier(r.target),...(r.name!==undefined?{name:string(r.name,2048)}:{})});});accepted=Object.freeze({identity:identifier(a.identity),title:string(a.title,2048),relations:Object.freeze(relations),orders:orderList(a.orders)});}
 // A field, a season mapping or a local issue that cannot be read is left out alone.
 const fields=keepReadable('screen-metadata','fields',list(raw.fields??[],64),f=>{if(!obj(f)||!score(f.confidence)||!int(f.selectionRevision)||JSON.stringify(f.value??null).length>524288)invalid();return Object.freeze({name:string(f.name,128),value:f.value,provider:string(f.provider,64),sourceKind:string(f.sourceKind,128),sourceUrl:url(f.sourceUrl),confidence:f.confidence,observedAt:string(f.observedAt,128),language:string(f.language,64),region:string(f.region,2),selectionRevision:f.selectionRevision});});
 const seasons=keepReadable('screen-metadata','seasons',list(raw.seasons??[],100),v=>{if(!obj(v)||!int(v.number)||v.number>99||!int(v.revision)||!text(v.anilistId,16)||v.anilistId!==''&&!/^[1-9][0-9]{0,15}$/.test(v.anilistId))invalid();return Object.freeze({number:v.number,anilistId:v.anilistId,revision:v.revision});});
 const context:ScreenMetadataContext=Object.freeze({policy:p,explanation:string(raw.explanation,4096),selected,selectionMode:string(raw.selectionMode,128),selectionRevision:raw.selectionRevision,...(accepted?{accepted}:{}),acceptedAt:string(raw.acceptedAt,128),acceptedConfidence:score(raw.acceptedConfidence)?raw.acceptedConfidence:0,fields:Object.freeze(fields),localIssues:Object.freeze(keepReadable('screen-metadata','localIssues',list(raw.localIssues??[],16),v=>string(v,128))),seasons:Object.freeze(seasons),parentId:string(raw.parentId,256),episodeStatus:string(raw.episodeStatus,128)});
 const chosen=candidates.find(c=>c.screen?.identity.provider===selected?.provider&&c.screen?.identity.id===selected?.id&&c.screen?.identity.type===selected?.type);
 return Object.freeze({scope:Object.freeze({...s,libraryId:t.libraryId,entityId:t.entityId,viewerFence:raw.viewerFence}),provider:'screen',revision:raw.revision,status,selectedId:chosen?.id??'',order:string(raw.order,160),orders:Object.freeze((chosen?.screen?.orders??accepted?.orders??[]).map(o=>o.id)),actions:Object.freeze(actions),candidates:Object.freeze(candidates),attempts:raw.attempts,nextAttempt:string(raw.nextAttempt,128),providerError:string(raw.error,4096),attribution:string(raw.attribution,4096),screen:context});
}

/** An episode order as it is stored, taken apart: the provider's order and what is added to each file's own number (0 when files are numbered by season). */
export function readTmdbOrder(order:string):Readonly<{base:string;offset:number}> {
 const m=/^tmdb-absolute:(.+):(-?\d{1,7})$/.exec(order);
 return m?{base:m[1]!,offset:Number(m[2])}:{base:order,offset:0};
}
/** Whether an order can carry a numbering offset: the provider's own order, or one of its alternate orders. */
export const orderTakesOffset=(order:string):boolean=>order==='official'||/^tmdb-group:[A-Za-z0-9_-]{1,128}$/.test(order);
/** Explicit anime numbering: local episode number plus offset is its position in the provider order. */
export function tmdbAbsoluteOrder(order:string,offset:number):string {
 if(!Number.isSafeInteger(offset)||Math.abs(offset)>1000000||!(order==='official'||/^tmdb-group:[A-Za-z0-9_-]{1,128}$/.test(order)))invalid();
 return offset===0?order:`tmdb-absolute:${order}:${offset}`;
}
