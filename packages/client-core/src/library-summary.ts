import {routePath} from '../../contracts/src/route-paths.ts';
import type {RouteResponse} from '../../contracts/src/route-paths.ts';
import {noteSkipped} from './tolerant.ts';
export type LibraryPublicationKind='book'|'magazine'|'newspaper';
/** Explicitly classified, visible Reading titles; separate from the library's ebook total. */
export type LibraryPublicationCount=Readonly<{kind:LibraryPublicationKind;count:number;atLeast:boolean}>;
export type LibrarySummary=Readonly<{counts:readonly Readonly<{kind:string;count:number;atLeast:boolean}>[];publicationCounts?:readonly LibraryPublicationCount[];coverUrls:readonly string[];latestArtwork:string;firstScan:boolean;empty:boolean;emptyKnown:boolean}>;
const object=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const text=(v:unknown):v is string=>typeof v==='string'&&v.length<=8192&&!/[\x00-\x1f\x7f]/.test(v);
const artwork=(v:unknown):v is string=>text(v)&&(v===''||v.startsWith('/')&&!v.startsWith('//'));
function invalid():never{throw new Error('Invalid library summary.');}
const publicationKinds:readonly LibraryPublicationKind[]=['book','magazine','newspaper'];
/** Published classified totals, bounded and unique. A malformed optional list is left out whole:
 * the core summary still reads, and no count is inferred from another kind's total. */
function publicationCounts(v:unknown):readonly LibraryPublicationCount[]|undefined{
 if(v===undefined||v===null)return undefined;
 if(!Array.isArray(v)||v.length>3){noteSkipped('library-summary','publicationCounts');return undefined;}
 const seen=new Set<string>(),out:LibraryPublicationCount[]=[];
 for(const row of v){
  if(!object(row)){noteSkipped('library-summary','publicationCounts');return undefined;}
  const kind=publicationKinds.find(known=>known===row.kind);
  if(!kind||typeof row.count!=='number'||!Number.isSafeInteger(row.count)||row.count<0||row.count>20000||typeof row.atLeast!=='boolean'||seen.has(kind)){noteSkipped('library-summary','publicationCounts');return undefined;}
  seen.add(kind);
  out.push(Object.freeze({kind,count:row.count,atLeast:row.atLeast}));
 }
 return Object.freeze(out);
}
export function parseLibrarySummary(raw:unknown):LibrarySummary{
 if(!object(raw)||!Array.isArray(raw.counts)||raw.counts.length>3||!Array.isArray(raw.coverUrls)||raw.coverUrls.length>3||!artwork(raw.latestArtwork)||typeof raw.firstScan!=='boolean'||typeof raw.empty!=='boolean'||typeof raw.emptyKnown!=='boolean')invalid();
 const counts=raw.counts.map(value=>{if(!object(value)||!text(value.kind)||!value.kind||typeof value.count!=='number'||!Number.isSafeInteger(value.count)||value.count<0||value.count>20000||typeof value.atLeast!=='boolean')invalid();return Object.freeze({kind:value.kind,count:value.count,atLeast:value.atLeast});});
 if(new Set(counts.map(row=>row.kind)).size!==counts.length||!raw.coverUrls.every(artwork))invalid();
 const classified=publicationCounts(raw.publicationCounts);
 return Object.freeze({counts:Object.freeze(counts),...(classified?{publicationCounts:classified}:{}),coverUrls:Object.freeze([...raw.coverUrls] as string[]),latestArtwork:raw.latestArtwork,firstScan:raw.firstScan,empty:raw.empty,emptyKnown:raw.emptyKnown});
}
/** A single viewer-scoped read for library counts and artwork. This holds no
 * persistent cache and never reads administration settings or scan progress. */
export async function readLibrarySummaries(api:{request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>},signal?:AbortSignal):Promise<readonly Readonly<{id:string;name:string;kind:string;summary:LibrarySummary}>[]>{
 const raw=await api.request<RouteResponse<'get_libraries'>>(routePath('get_libraries'),'GET',undefined,signal);
 if(!object(raw)||!Array.isArray(raw.items)||raw.items.length>2048)invalid();
 return Object.freeze(raw.items.map(row=>{if(!object(row)||!text(row.id)||!text(row.name)||!text(row.kind))invalid();return Object.freeze({id:row.id,name:row.name,kind:row.kind,summary:parseLibrarySummary(row.summary)});}));
}
