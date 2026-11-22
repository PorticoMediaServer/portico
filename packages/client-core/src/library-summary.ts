import {routePath} from '../../contracts/src/route-paths.ts';
import type {RouteResponse} from '../../contracts/src/route-paths.ts';
import {noteSkipped} from './tolerant.ts';
import {libraryKind} from './items.ts';
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
 // A kind the library holds none of (an anime library's "0 movies") is not part of its line; a library
 // with nothing at all keeps its zero counts so the line still says so.
 const held=counts.some(row=>row.count>0)?counts.filter(row=>row.count>0):counts;
 return Object.freeze({counts:Object.freeze(held),...(classified?{publicationCounts:classified}:{}),coverUrls:Object.freeze([...raw.coverUrls] as string[]),latestArtwork:raw.latestArtwork,firstScan:raw.firstScan,empty:raw.empty,emptyKnown:raw.emptyKnown});
}
/** A library whose server sends no summary has no known counts, covers or empty state. */
const unknownSummary:LibrarySummary=Object.freeze({counts:Object.freeze([]),coverUrls:Object.freeze([]),latestArtwork:'',firstScan:false,empty:false,emptyKnown:false});
/** A summary is optional: absent or unreadable, the library still lists, with nothing known about
 * it. Only an unreadable list or library row refuses the read. */
function optionalSummary(raw:unknown):LibrarySummary{
 if(raw===undefined||raw===null)return unknownSummary;
 try{return parseLibrarySummary(raw);}catch{noteSkipped('library-summary','summary');return unknownSummary;}
}
/** A single viewer-scoped read for library counts and artwork. This holds no
 * persistent cache and never reads administration settings or scan progress. */
export async function readLibrarySummaries(api:{request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>},signal?:AbortSignal):Promise<readonly Readonly<{id:string;name:string;kind:string;summary:LibrarySummary}>[]>{
 const raw=await api.request<RouteResponse<'get_libraries'>>(routePath('get_libraries'),'GET',undefined,signal);
 if(!object(raw)||!Array.isArray(raw.items)||raw.items.length>2048)invalid();
 return Object.freeze(raw.items.map(row=>{if(!object(row)||!text(row.id)||!text(row.name)||!text(row.kind))invalid();return Object.freeze({id:row.id,name:row.name,kind:libraryKind(row.kind),summary:optionalSummary(row.summary)});}));
}

/** The word each count kind is shown with (the `lib.count` pivot). An audiobook library's titles are books (phone-app 6:
 * "8 books  7 authors"). */
const countPivots:Readonly<Record<string,string>>=Object.freeze({movie:'movies',show:'shows',episode:'episodes',artist:'artists',album:'albums',song:'songs',book:'books',audiobook:'books',author:'authors',series:'series',ebook:'books',comic:'titles'});
/** A library card's counts in the server's order, each with its word; a kind the library holds none of is left out
 * ("8 shows · 32 episodes", never "0 movies"), and so is a kind with no word. */
export function visibleLibraryCounts(counts:LibrarySummary['counts']):readonly Readonly<{kind:string;count:number;atLeast:boolean;pivot:string}>[]{
 return counts.flatMap(part=>countPivots[part.kind]&&(part.count>0||part.atLeast)?[Object.freeze({kind:part.kind,count:part.count,atLeast:part.atLeast,pivot:countPivots[part.kind]!})]:[]);
}
