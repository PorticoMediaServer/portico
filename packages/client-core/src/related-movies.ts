import {unreadableServerResponse} from './server-messages.ts';
/** Validated server-owned relationship rows. Clients never infer or rank them. */
import {validateContentEntry,type ContentEntry} from './library-content.ts';
import {firstOfEach,keepReadable} from './tolerant.ts';
import type {HomeApi} from './home.ts';
import {readRecommendationRowPage,RelationRowPager} from './relation-rows.ts';
/** `nextCursor` continues the row through `relatedRowPager` (its row route) to the row's end. */
export type RelatedMovieRow=Readonly<{id:string;relation:'genre'|'person'|'album'|'artist'|'book'|'author'|'show';provider:string;evidenceId:string;heading:string;entries:readonly ContentEntry[];nextCursor?:string}>;
export type RelatedMovieProjection=Readonly<{version:1;libraryId:string;libraryName:string;rows:readonly RelatedMovieRow[]}>;
const object=(v:unknown):v is Record<string,unknown>=>typeof v==='object'&&v!==null&&!Array.isArray(v);
const text=(v:unknown,max=512):v is string=>typeof v==='string'&&v.length>0&&v.length<=max&&!/[\x00-\x1f\x7f]/.test(v);
function invalid():never{throw Object.assign(new Error(unreadableServerResponse),{code:'invalid_related_movies',retryable:false});}
/** Words a row is drawn with: absent reads as empty, and words over their bound refuse what carries them. */
function words(v:unknown):string{if(v===undefined||v===null||v==='')return '';if(!text(v))invalid();return v;}
/**
 * A title's related rows. The version, the library and the bound of 3 rows are exact (a caller that
 * cannot read them shows the title without related rows). A row of a relation this app does not
 * know, or one that cannot be read, is left out alone; so is an entry that is not an available
 * title of the expected kind in this library, and a row with no entry left. A repeated row or
 * entry keeps its first appearance, and at most two genre rows and one person row are shown.
 */
type RelatedSource={libraryId:string;itemId:string;kind:string};
/** A related entry is an available title of the row's kind in the title's library, and not the title itself. */
function relatedEntry(entry:ContentEntry,source:RelatedSource):ContentEntry{
 const expectedKind=source.kind==='song'?'album':source.kind==='audiobook_file'?'book':source.kind==='episode'?'show':'movie';
 if(entry.kind!==expectedKind||entry.libraryId!==source.libraryId||entry.id===source.itemId||entry.available!==true||entry.navigation?.view!==(['album','book','show'].includes(expectedKind)?expectedKind:'item')||entry.navigation.entityId!==entry.id||entry.playback!==undefined)invalid();
 return entry;
}
/**
 * A related row read to its end: its later pages come from the title's row route under the
 * detail's fence and revision, and an entry that is not a related title is left out alone.
 */
export function relatedRowPager(api:HomeApi,source:RelatedSource,row:RelatedMovieRow,scope:Readonly<{viewerFence:string;revision:Readonly<{catalog:number;viewer:number}>}>,limit?:number):RelationRowPager{
 return new RelationRowPager({entries:row.entries,...(row.nextCursor?{nextCursor:row.nextCursor}:{})},async(cursor,signal)=>{
  const next=await readRecommendationRowPage(api,{kind:'item',id:source.itemId},{id:row.id,viewerFence:scope.viewerFence},{cursor,...(limit===undefined?{}:{limit})},signal);
  if(next.revision.catalog!==scope.revision.catalog||next.revision.viewer!==scope.revision.viewer)throw Object.assign(new Error('Reload the title to continue this row.'),{code:'stale_continuation'});
  const entries=keepReadable('related','entries',next.entries,entry=>relatedEntry(validateContentEntry(entry),source));
  return {entries,...(next.hasMore&&next.nextCursor?{nextCursor:next.nextCursor}:{})};
 });
}
export function validateRelatedMovies(value:unknown,source:{libraryId:string;itemId:string;kind:string}):RelatedMovieProjection{
 if(!object(value)||!['movie','song','audiobook_file','episode'].includes(source.kind)||value.version!==1||value.libraryId!==source.libraryId||!Array.isArray(value.rows)||value.rows.length>3)invalid();
 const relations=source.kind==='movie'?['genre','person']:source.kind==='episode'?['genre','person','show']:source.kind==='song'?['album','artist','genre']:['book','author','genre'];
 const rows=firstOfEach('related','rows',keepReadable('related','rows',value.rows,raw=>{
  if(!object(raw)||!relations.includes(raw.relation as string)||!text(raw.provider,256)||!text(raw.evidenceId,256)||raw.id!==`${raw.relation}:${raw.provider}:${raw.evidenceId}`||!Array.isArray(raw.entries)||raw.entries.length>12)invalid();
  if(raw.nextCursor!==undefined&&!text(raw.nextCursor,4096))invalid();
  const entries=firstOfEach('related','entries',keepReadable('related','entries',raw.entries,value=>relatedEntry(validateContentEntry(value),source)),e=>e.id);
  if(!entries.length)return undefined;
  return Object.freeze({id:raw.id as string,relation:raw.relation as RelatedMovieRow['relation'],provider:raw.provider,evidenceId:raw.evidenceId,heading:words(raw.heading),entries:Object.freeze(entries),...(raw.hasMore===true&&raw.nextCursor?{nextCursor:raw.nextCursor as string}:{})});
 }),r=>r.id);
 let genres=0,people=0;
 const shown=rows.filter(r=>r.relation==='genre'?++genres<=2:r.relation==='person'?++people<=1:true);
 return Object.freeze({version:1,libraryId:source.libraryId,libraryName:words(value.libraryName),rows:Object.freeze(shown)});
}
