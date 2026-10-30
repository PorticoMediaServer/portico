import {encodedRoutePath, routePath} from "../../contracts/src/route-paths.ts";
import {unreadableServerResponse} from './server-messages.ts';
/** Canonical person pages. The server owns person identity, credit membership, ordering and
 * paging; this module only rejects a payload that does not match what it asked for. */
import {validateContentEntry,type ContentEntry,type ContentScope,type LibraryContentApi} from './library-content.ts';
import {firstOfEach,keepReadable} from './tolerant.ts';
export type PersonRole='cast'|'crew'|'all';
export type Person=Readonly<{birthPlace?:string;biographySource?:string;id:string;name:string;sortName:string;biography:string;birthDate:string;deathDate:string;portraitUrl:string;roles:readonly string[];knownFor:readonly ContentEntry[];providerIds:Readonly<Record<string,string>>;revision:number}>;
export type PersonCredit=Readonly<{roles:readonly Readonly<{role:string;department:string}>[];media:ContentEntry;role:string;character:string;department:string;creditKind:'cast'|'crew'}>;
export type PersonPage=Readonly<{groups:readonly Readonly<{id:string;kind?:string;department?:string;total:number}>[];serverId:string;viewerFence:string;revision:Readonly<{catalog:number;viewer:number}>;person:Person;credits:readonly PersonCredit[];pageInfo:Readonly<{total:number;nextCursor:string}>}>;
export type PersonSummary=Readonly<{id:string;name:string;sortName:string;portraitUrl:string;roles:readonly string[];creditCount:number}>;
export type PeopleDirectory=Readonly<{serverId:string;viewerFence:string;revision:Readonly<{catalog:number;viewer:number}>;q:string;people:readonly PersonSummary[]}>;
export const MAX_PERSON_CREDITS=100;
export const MAX_KNOWN_FOR=8;
const roles:readonly PersonRole[]=['cast','crew','all'];
const object=(v:unknown):v is Record<string,unknown>=>typeof v==='object'&&v!==null&&!Array.isArray(v);
const text=(v:unknown,max=4096):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(v);
const line=(v:unknown,max=512):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x1f\x7f]/.test(v);
const id=(v:unknown):v is string=>line(v,256)&&v.length>0;
const count=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
class Failure extends Error{code:string;constructor(code:string,message:string){super(message);this.code=code;}}
function invalid():never{throw new Failure('invalid_person',unreadableServerResponse);}
function array(v:unknown,max:number):unknown[]{if(!Array.isArray(v)||v.length>max)invalid();return v;}
/** Role labels: the ones that can be read, each once. A list the server leaves out is empty; the bound is exact. */
function labels(v:unknown,max:number):readonly string[]{return Object.freeze([...new Set(keepReadable('people','roles',array(v??[],max),x=>line(x,256)&&x?x:undefined))]);}
/** A person's ids at the providers: a pair that cannot be read is left out; the bound of 16 is exact. */
function providers(v:unknown):Readonly<Record<string,string>>{
 if(v===undefined||v===null)return Object.freeze({});
 if(!object(v))invalid();
 const out:Record<string,string>={};
 const keys=Object.keys(v);
 if(keys.length>16)invalid();
 for(const key of keys){if(id(key)&&id(v[key]))out[key]=v[key] as string;}
 return Object.freeze(out);
}
/** Words a person is drawn with: absent reads as empty, and words over their bound refuse what carries them. */
function words(v:unknown,max:number,body=false):string{if(v===undefined||v===null)return '';if(!(body?text(v,max):line(v,max)))invalid();return v as string;}
/** The person the page is about. The id, the name and the revision are exact; every other fact is empty when it is not sent, and a known-for title that cannot be read is left out alone. */
function person(v:unknown):Person{
 if(!object(v)||!id(v.id)||!line(v.name,1024)||!v.name||!count(v.revision))invalid();
 const knownFor=firstOfEach('people','knownFor',keepReadable('people','knownFor',array(v.knownFor??[],MAX_KNOWN_FOR),validateContentEntry),e=>e.id);
 return Object.freeze({...(line(v.birthPlace,2048)&&v.birthPlace?{birthPlace:v.birthPlace}:{}),...(line(v.biographySource,256)&&v.biographySource?{biographySource:v.biographySource}:{}),id:v.id,name:v.name,sortName:words(v.sortName,1024),biography:words(v.biography,20000,true),birthDate:words(v.birthDate,64),deathDate:words(v.deathDate,64),portraitUrl:words(v.portraitUrl,4096),roles:labels(v.roles,16),knownFor:Object.freeze(knownFor),providerIds:providers(v.providerIds),revision:v.revision});
}
/**
 * One page of a person's credits. The server, the viewer fence, the revisions, the person asked
 * for, the cursor and the page bound are exact. A credit that cannot be read, is of a kind this
 * app does not know, or is not of the role asked for is left out alone, and the total is at least
 * the credits kept.
 */
export function parsePersonPage(raw:unknown,scope:ContentScope,request:Readonly<{role?:PersonRole;department?:string;kind?:string;limit:number;personId:string}>):PersonPage{
 if(!object(raw)||raw.serverId!==scope.serverId||!id(raw.viewerFence)||!object(raw.revision)||!object(raw.pageInfo))invalid();
 if(!count(raw.revision.catalog)||!count(raw.revision.viewer))invalid();
 const subject=person(raw.person);
 if(subject.id!==request.personId)invalid();
 const sent=array(raw.credits,request.limit);
 const credits=keepReadable('people','credits',sent,v=>{
  if(!object(v)||(v.creditKind!=='cast'&&v.creditKind!=='crew'))invalid();
  // The server classifies the credit; a client that guessed from department
  // would disagree with the server's own role filter.
  if(request.role==='cast'&&v.creditKind!=='cast'||request.role==='crew'&&v.creditKind!=='crew')invalid();
  const roles=keepReadable('people','credit.roles',array(v.roles??[],128),role=>{if(!object(role)||!line(role.role,512)||!line(role.department,256))invalid();return Object.freeze({role:role.role,department:role.department});});
  return Object.freeze({roles:Object.freeze(roles),media:validateContentEntry(v.media),role:words(v.role,512),character:words(v.character,512),department:words(v.department,256),creditKind:v.creditKind});
 });
 const info=raw.pageInfo;
 if(!line(info.nextCursor??'',4096))invalid();
 // A cursor only follows a full page, so a server cannot keep a client paging through empty ones.
 if(info.nextCursor&&sent.length<request.limit)invalid();
 const groups=keepReadable('people','groups',array(raw.groups??[],256),g=>{if(!object(g)||!id(g.id)||!count(g.total))invalid();return Object.freeze({id:g.id,total:g.total,...(line(g.kind,128)&&g.kind?{kind:g.kind}:{}),...(line(g.department,128)&&g.department?{department:g.department}:{})});});
 return Object.freeze({groups:Object.freeze(groups),serverId:raw.serverId as string,viewerFence:raw.viewerFence,revision:Object.freeze({catalog:raw.revision.catalog,viewer:raw.revision.viewer}),person:subject,credits:Object.freeze(credits),pageInfo:Object.freeze({total:Math.max(count(info.total)?info.total:0,credits.length),nextCursor:(info.nextCursor??'') as string})});
}
/** People matching a name. The server, the viewer fence, the revisions and the page bound are exact; a person who cannot be read is left out alone and a repeated one keeps the first appearance. */
export function parsePeopleDirectory(raw:unknown,scope:ContentScope,limit:number):PeopleDirectory{
 if(!object(raw)||raw.serverId!==scope.serverId||!id(raw.viewerFence)||!object(raw.revision))invalid();
 if(!count(raw.revision.catalog)||!count(raw.revision.viewer))invalid();
 const people=firstOfEach('people','people',keepReadable('people','people',array(raw.people,limit),v=>{
  if(!object(v)||!id(v.id)||!line(v.name,1024)||!v.name)invalid();
  return Object.freeze({id:v.id,name:v.name,sortName:words(v.sortName,1024),portraitUrl:words(v.portraitUrl,4096),roles:labels(v.roles,16),creditCount:count(v.creditCount)?v.creditCount:0});
 }),p=>p.id);
 return Object.freeze({serverId:raw.serverId as string,viewerFence:raw.viewerFence,revision:Object.freeze({catalog:raw.revision.catalog,viewer:raw.revision.viewer}),q:words(raw.q,512),people:Object.freeze(people)});
}
export function personPath(personId:string,options:Readonly<{role?:PersonRole;department?:string;kind?:string;limit?:number;cursor?:string|null}>={}):string{
 if(!id(personId))throw new Error('A person id is required.');
 const limit=options.limit??40;
 if(!Number.isInteger(limit)||limit<1||limit>MAX_PERSON_CREDITS)throw new Error('Person credits page 1 to 100 rows.');
 if(options.role!==undefined&&!roles.includes(options.role))throw new Error('Unknown credit role.');
 const params=new URLSearchParams({limit:String(limit)});
 if(options.department&&options.kind||options.role&&(options.department||options.kind))throw new Error('Choose one person credit group.');
 if(options.department)params.set('department',options.department);if(options.kind)params.set('kind',options.kind);
 if(options.role)params.set('role',options.role);
 if(options.cursor)params.set('cursor',options.cursor);
 return encodedRoutePath("get_people_id", {"id": encodeURIComponent(personId)}) + "?" + (params);
}
export function peopleSearchPath(q:string,limit=25):string{
 if(typeof q!=='string'||!q.trim())throw new Error('A person name query is required.');
 if(!Number.isInteger(limit)||limit<1||limit>MAX_PERSON_CREDITS)throw new Error('People search returns 1 to 100 rows.');
 return routePath("get_people") + "?" + (new URLSearchParams({q:q.split(/\p{White_Space}+/u).filter(Boolean).join(' '),limit:String(limit)}));
}
/** The portrait address is server-authored; a client never composes a provider URL. */
export function personPortraitPath(personId:string,size:'thumbnail'|'full'='thumbnail'):string{
 if(!id(personId))throw new Error('A person id is required.');
 return encodedRoutePath("get_people_id_portrait", {"id": encodeURIComponent(personId)})+(size==='thumbnail'?'?size=thumbnail':'');
}
export async function readPerson(api:LibraryContentApi,scope:ContentScope,personId:string,options:Readonly<{role?:PersonRole;department?:string;kind?:string;limit?:number;cursor?:string|null;signal?:AbortSignal}>={}):Promise<PersonPage>{
 const limit=options.limit??40;
 const raw=await api.request<unknown>(personPath(personId,{role:options.role,department:options.department,kind:options.kind,limit,cursor:options.cursor}),'GET',undefined,options.signal);
 return parsePersonPage(raw,scope,{role:options.role,limit,personId});
}
export async function readPeople(api:LibraryContentApi,scope:ContentScope,q:string,limit=25,signal?:AbortSignal):Promise<PeopleDirectory>{
 return parsePeopleDirectory(await api.request<unknown>(peopleSearchPath(q,limit),'GET',undefined,signal),scope,limit);
}
