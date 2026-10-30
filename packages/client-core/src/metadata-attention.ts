import {isVersionedArtwork} from './artwork/versioned.ts';
import {encodedRoutePath, routePath} from "../../contracts/src/route-paths.ts";
import {unreadableServerResponse} from './server-messages.ts';
import {firstOfEach,keepReadable,noteSkipped} from './tolerant.ts';
export type AttentionIdentity=Readonly<{provider:'tmdb'|'tvdb'|'anilist';type:'movie'|'show'|'anime'|'episode';id:string}>;
export type AttentionCandidate=Readonly<{posterUrl?:string;key:string;identity:AttentionIdentity;title:string;year:number;confidence:number;reasons:readonly string[];orders:readonly Readonly<{id:string;name:string}>[]}>;
/** Film and TV titles are 'item' or 'show'; music and audiobooks are 'album', 'song' or 'book'. */
export type AttentionKind='item'|'show'|'album'|'song'|'book';
export type AttentionEntry=Readonly<{artworkUrl?:string;parentTitle?:string;parentArtist?:string;libraryId:string;entityId:string;kind:AttentionKind;itemKind:string;parentId:string;actions:readonly string[];title:string;revision:number;reason:string;confidence:number;candidates:readonly AttentionCandidate[]}>;
export type AttentionPage=Readonly<{libraryId:string;total:number;entries:readonly AttentionEntry[];nextCursor:string}>;
export type AttentionCommand=Readonly<{kind:AttentionKind;entityId:string;expectedRevision:number;action:'accept'|'search'|'id'|'dismiss'|'retry';candidateKey?:string;provider?:AttentionIdentity['provider'];providerId?:string;query?:string;order?:string}>;
const object=(v:unknown):v is Record<string,unknown>=>typeof v==='object'&&v!==null&&!Array.isArray(v);
const text=(v:unknown,max:number):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(v);
const integer=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
const score=(v:unknown):v is number=>typeof v==='number'&&Number.isFinite(v)&&v>=0&&v<=1;
function invalid():never{throw new Error(unreadableServerResponse);}
const knownActions=['accept','search','id','dismiss','parent','retry'];
const knownKinds=['item','show','album','song','book'];
/**
 * The library, the page size and the cursor stay exact, and so does each row's revision, which a
 * command sends back. A title this app cannot read is left out alone and a repeated one keeps its
 * first row. An action this app does not know is not offered. A suggested match it cannot read, or
 * one from a provider it does not know, is left out, so it can never be accepted by mistake.
 */
export function parseMetadataAttention(raw:unknown,libraryId:string):AttentionPage {
 if(!object(raw)||raw.libraryId!==libraryId||!integer(raw.total)||!Array.isArray(raw.entries)||raw.entries.length>50||!text(raw.nextCursor,128))invalid();
 const entries=firstOfEach('metadata-attention','entries',keepReadable('metadata-attention','entries',raw.entries,e=>{
  if(!object(e)||!text(e.entityId,128)||!e.entityId||!knownKinds.includes(e.kind as string)||!text(e.itemKind,64)||!text(e.parentId,128)||!Array.isArray(e.actions)||e.actions.length>16||!text(e.title,2048)||!integer(e.revision)||e.revision<1||!text(e.reason,128)||!score(e.confidence)||!Array.isArray(e.candidates)||e.candidates.length>3)invalid();
  const actions=[...new Set(e.actions.filter((a):a is string=>knownActions.includes(a as string)))];
  if(actions.length!==e.actions.length)noteSkipped('metadata-attention','entries.actions',e.actions.length-actions.length);
  const candidates=firstOfEach('metadata-attention','entries.candidates',keepReadable('metadata-attention','entries.candidates',e.candidates,c=>{
   if(!object(c)||!text(c.key,64)||!/^[0-9a-f]{64}$/.test(c.key)||!object(c.identity)||!['tmdb','tvdb','anilist'].includes(c.identity.provider as string)||!['movie','show','anime','episode'].includes(c.identity.type as string)||!text(c.identity.id,16)||!/^[1-9][0-9]{0,15}$/.test(c.identity.id)||!Number.isSafeInteger(Number(c.identity.id))||(c.identity.provider==='anilist'?(c.identity.type!=='anime'||Number(c.identity.id)>2147483647):c.identity.type==='anime')||!text(c.title,2048)||!integer(c.year)||c.year>9999||!score(c.confidence)||!Array.isArray(c.reasons)||c.reasons.length>16||!Array.isArray(c.orders)||c.orders.length>64)invalid();
   const orders=keepReadable('metadata-attention','entries.candidates.orders',c.orders,o=>{if(!object(o)||!text(o.id,160)||!text(o.name,2048))invalid();return Object.freeze({id:o.id,name:o.name});});
   const reasons=c.reasons.filter((v):v is string=>text(v,256));
   const posterUrl=text(c.posterUrl,4096)&&/^\/v1\/metadata\/(?:item|show)\/[^/]+\/art\/poster\?/.test(c.posterUrl)?c.posterUrl:undefined;
   return Object.freeze({...(posterUrl?{posterUrl}:{}),key:c.key,identity:Object.freeze({...c.identity}) as AttentionIdentity,title:c.title,year:c.year,confidence:c.confidence,reasons:Object.freeze(reasons),orders:Object.freeze(orders)});
  }),c=>c.key);
  const albumId=e.kind==='album'?e.entityId:e.kind==='song'?e.parentId:'';
  const coverPrefix=albumId?encodedRoutePath('get_metadata_kind_id_art_role',{kind:'album',id:encodeURIComponent(String(albumId)),role:'cover'})+'?v=':undefined;
  const artworkUrl=coverPrefix&&text(e.artworkUrl,4096)&&isVersionedArtwork(e.artworkUrl,coverPrefix)?e.artworkUrl:undefined;
  return Object.freeze({...(artworkUrl?{artworkUrl}:{}),...(text(e.parentTitle,2048)&&e.parentTitle?{parentTitle:e.parentTitle}:{}),...(text(e.parentArtist,2048)&&e.parentArtist?{parentArtist:e.parentArtist}:{}),libraryId:text(e.libraryId,128)?e.libraryId:libraryId,entityId:e.entityId,kind:e.kind as AttentionKind,itemKind:e.itemKind as string,parentId:e.parentId as string,actions:Object.freeze(actions),title:e.title,revision:e.revision,reason:e.reason,confidence:e.confidence,candidates:Object.freeze(candidates)});
 }),e=>e.entityId);
 return Object.freeze({libraryId,total:raw.total as number,entries:Object.freeze(entries),nextCursor:raw.nextCursor});
}
type Request=<T>(path:string,method?:string,body?:unknown)=>Promise<T>;
export function metadataAttentionService(request:Request,libraryId:string){
 const path=encodedRoutePath("get_metadata_attention", {"id": encodeURIComponent(libraryId)});
 return Object.freeze({
  async list(cursor='',limit=25,includeInProgress=true){return parseMetadataAttention(await request<unknown>(`${path}?cursor=${encodeURIComponent(cursor)}&limit=${limit}&includeInProgress=${includeInProgress}`),libraryId);},
  async act(command:AttentionCommand){await request<unknown>(path,'POST',command);},
 });
}

export type MetadataAttentionCounts=Readonly<{total:number;libraries:readonly Readonly<{libraryId:string;total:number}>[]}>;
export function parseMetadataAttentionCounts(raw:unknown):MetadataAttentionCounts {
 if(!object(raw)||!integer(raw.total)||!Array.isArray(raw.libraries)||raw.libraries.length>10000)invalid();
 const libraries=raw.libraries.map(v=>{if(!object(v)||!text(v.libraryId,128)||!v.libraryId||!integer(v.total))invalid();return Object.freeze({libraryId:v.libraryId,total:v.total});});
 return Object.freeze({total:raw.total,libraries:Object.freeze(libraries)});
}
export function serverMetadataAttentionService(request:Request){return Object.freeze({
 async counts(){return parseMetadataAttentionCounts(await request<unknown>(routePath("get_metadata_attention_counts")));},
 async list(cursor='',limit=25,includeInProgress=false){return parseMetadataAttention(await request<unknown>(routePath("get_server_metadata_attention") + "?cursor=" + (encodeURIComponent(cursor)) + "&limit=" + (limit) + "&includeInProgress=" + (includeInProgress)),'');},
});}
