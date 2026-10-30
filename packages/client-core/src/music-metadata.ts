import type {MessageId, MessageValues} from '../../i18n/src/index.ts';
import {serviceText} from './presentation/service-text.ts';
import {routeBody} from "../../contracts/src/routes.ts";
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {unreadableServerResponse} from './server-messages.ts';
import {keepReadable,noteSkipped} from './tolerant.ts';
/** Bounded music/local-book projections. No provider requests or client ranking. */
export type MusicPolicy = Readonly<{revision:number;localMode:'off'|'supplement'|'prefer';musicBrainzEnabled:boolean;acoustidEnabled:boolean;acoustidConfigured:boolean;fingerprintAllowed:boolean}>;
export type MusicPolicyInput = Pick<MusicPolicy,'localMode'|'musicBrainzEnabled'|'acoustidEnabled'>;
export type MusicMatchObservation = Readonly<{fingerprintStatus:string;providerError:string;confidence:number;margin:number;strongSignals:number;algorithm:string}>;
export type MusicEvidenceDetails = Readonly<{status?:string;packaging?:string;script?:string;language?:string;barcode?:string;credits:readonly Readonly<{id:string;name:string;joinPhrase:string}>[];labels:readonly Readonly<{name:string;catalogNumber:string}>[];isrcs:readonly string[];relations:readonly Readonly<{type:string;name:string;id:string;targetType:string;attributes:readonly string[]}>[]}>;
export type LocalBookMetadata = Readonly<{authors:readonly string[];narrators:readonly string[];series:readonly Readonly<{name:string;position?:string}>[];identifiers:readonly Readonly<{scheme:string;value:string}>[];edition?:string;publisher?:string;language?:string;description?:string;publishedYear?:number;sources:Readonly<Record<string,string>>}>;
const obj=(v:unknown):v is Record<string,unknown>=>typeof v==='object'&&v!==null&&!Array.isArray(v);
const text=(v:unknown,max=4096):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(v);
const mbid=(v:unknown):v is string=>typeof v==='string'&&/^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i.test(v);
function invalid():never{throw Object.assign(new Error(unreadableServerResponse),{code:'invalid_music_metadata'});}
function list(v:unknown,max=128):unknown[]{if(!Array.isArray(v)||v.length>max)invalid();return v;}
/** The strings in a list that can be read; one that cannot is left out alone. The list's bound is exact, and a list the server leaves out is empty. */
function strings(v:unknown,max=128,part='strings'):readonly string[]{return Object.freeze(keepReadable('music-metadata',part,list(v??[],max),s=>text(s)?s:undefined));}
export function parseMusicPolicy(v:unknown):MusicPolicy {
 if(!obj(v)||typeof v.revision!=='number'||!Number.isSafeInteger(v.revision)||v.revision<1||!['off','supplement','prefer'].includes(v.localMode as string))invalid();
 for(const k of ['musicBrainzEnabled','acoustidEnabled','acoustidConfigured','fingerprintAllowed'])if(typeof v[k]!=='boolean')invalid();
 return Object.freeze({revision:v.revision,localMode:v.localMode,musicBrainzEnabled:v.musicBrainzEnabled,acoustidEnabled:v.acoustidEnabled,acoustidConfigured:v.acoustidConfigured,fingerprintAllowed:v.fingerprintAllowed}) as MusicPolicy;
}
export function parseMusicObservation(v:unknown):MusicMatchObservation {
 if(!obj(v)||!text(v.fingerprintStatus,64)||!text(v.providerError,64)||!text(v.algorithm,64)||typeof v.strongSignals!=='number'||!Number.isSafeInteger(v.strongSignals)||v.strongSignals<0||v.strongSignals>16)invalid();
 for(const k of ['confidence','margin'])if(typeof v[k]!=='number'||!Number.isFinite(v[k])||(v[k] as number)<0||(v[k] as number)>1)invalid();
 return Object.freeze({fingerprintStatus:v.fingerprintStatus,providerError:v.providerError,confidence:v.confidence,margin:v.margin,strongSignals:v.strongSignals,algorithm:v.algorithm}) as MusicMatchObservation;
}
/** A release's details as the provider published them. Every part is only shown: a fact, credit, label, relation or code that cannot be read is left out alone. */
export function parseMusicDetails(v:unknown):MusicEvidenceDetails {
 if(!obj(v))invalid();const optional:Record<string,string>={};for(const k of ['status','packaging','language','script','barcode'])if(v[k]!==undefined&&v[k]!==null){if(text(v[k],256))optional[k]=v[k] as string;else noteSkipped('music-metadata','details.'+k);}
 const credits=keepReadable('music-metadata','details.credits',list(v.credits??[]),c=>{if(!obj(c)||!obj(c.artist)||!mbid(c.artist.id)||!text(c.artist.name,2048)||!text(c.name,2048)||!text(c.joinphrase,256))invalid();return Object.freeze({id:c.artist.id,name:c.name||c.artist.name,joinPhrase:c.joinphrase});});
 const labels=keepReadable('music-metadata','details.labels',list(v.labels??[]),l=>{if(!obj(l)||!text(l['catalog-number'],512))invalid();if(l.label!==undefined&&l.label!==null&&(!obj(l.label)||!text(l.label.name,2048)||l.label.id!==''&&!mbid(l.label.id)))invalid();return Object.freeze({catalogNumber:l['catalog-number'],name:obj(l.label)?l.label.name as string:''});});
 const relations=keepReadable('music-metadata','details.relations',list(v.relations??[]),r=>{if(!obj(r)||!text(r.type,256)||!text(r['target-type'],256))invalid();let name='',id='';if(r.artist!==undefined){if(!obj(r.artist)||!mbid(r.artist.id)||!text(r.artist.name,2048))invalid();name=r.artist.name;id=r.artist.id;}if(r.work!==undefined){if(!obj(r.work)||!mbid(r.work.id)||!text(r.work.title))invalid();name=r.work.title;id=r.work.id;}return Object.freeze({type:r.type,targetType:r['target-type'],name,id,attributes:strings(r.attributes??[],128,'details.relation.attributes')});});
 const isrcs=Object.freeze(keepReadable('music-metadata','details.isrcs',list(v.isrcs??[]),code=>typeof code==='string'&&/^[A-Z]{2}[A-Z0-9]{3}[0-9]{7}$/.test(code)?code:undefined));
 return Object.freeze({...optional,credits:Object.freeze(credits),labels:Object.freeze(labels),isrcs,relations:Object.freeze(relations)});
}
/** What a book's own files say about it. Every part is only shown: a fact, a series, an identifier or a source this app cannot read is left out alone. */
export function parseLocalBook(v:unknown):LocalBookMetadata {
 if(!obj(v))invalid();const given=v.sources===undefined||v.sources===null?{}:v.sources;if(!obj(given)||Object.keys(given).length>32)invalid();const sources:Record<string,string>={};for(const [key,value] of Object.entries(given)){if(text(key,64)&&['embedded','opf','nfo','portico_json'].includes(value as string))sources[key]=value as string;else noteSkipped('music-metadata','book.sources');}
 const fields:Record<string,string>={};for(const k of ['edition','publisher','language','description'])if(v[k]!==undefined&&v[k]!==null){if(text(v[k]))fields[k]=v[k] as string;else noteSkipped('music-metadata','book.'+k);}
 const series=keepReadable('music-metadata','book.series',list(v.series??[],64),s=>{if(!obj(s)||!text(s.name)||s.position!==undefined&&!text(s.position,128))invalid();return Object.freeze({name:s.name,...(s.position!==undefined?{position:s.position as string}:{})});});
 const identifiers=keepReadable('music-metadata','book.identifiers',list(v.identifiers??[],64),i=>{if(!obj(i)||!text(i.scheme,64)||!text(i.value,1024))invalid();return Object.freeze({scheme:i.scheme,value:i.value});});
 const publishedYear=typeof v.publishedYear==='number'&&Number.isSafeInteger(v.publishedYear)&&v.publishedYear>0&&v.publishedYear<=9999?v.publishedYear:undefined;
 return Object.freeze({...fields,...(publishedYear===undefined?{}:{publishedYear}),authors:strings(v.authors,128,'book.authors'),narrators:strings(v.narrators,128,'book.narrators'),series:Object.freeze(series),identifiers:Object.freeze(identifiers),sources:Object.freeze(sources)});
}
type MusicMetadataTranslate = (key: MessageId, values?: MessageValues) => string;
const evidenceKeys: Readonly<Record<string, MessageId>> = {
 provider_name_search: 'musicMetadata.evidence.provider_name_search',
 owner_review_required: 'musicMetadata.evidence.owner_review_required',
 automatic_evidence_match: 'musicMetadata.evidence.automatic_evidence_match',
 exact_title: 'musicMetadata.evidence.exact_title',
 exact_artist: 'musicMetadata.evidence.exact_artist',
 duration_match: 'musicMetadata.evidence.duration_match',
 duration_conflict: 'musicMetadata.evidence.duration_conflict',
 acoustid_recording_match: 'musicMetadata.evidence.acoustid_recording_match',
 isrc_match: 'musicMetadata.evidence.isrc_match',
 isrc_conflict: 'musicMetadata.evidence.isrc_conflict',
 release_year_match: 'musicMetadata.evidence.release_year_match',
 release_year_conflict: 'musicMetadata.evidence.release_year_conflict',
 barcode_match: 'musicMetadata.evidence.barcode_match',
 barcode_conflict: 'musicMetadata.evidence.barcode_conflict',
 country_match: 'musicMetadata.evidence.country_match',
 country_conflict: 'musicMetadata.evidence.country_conflict',
 track_count_match: 'musicMetadata.evidence.track_count_match',
 track_count_conflict: 'musicMetadata.evidence.track_count_conflict',
 edition_conflict: 'musicMetadata.evidence.edition_conflict',
 local_evidence_conflict: 'musicMetadata.evidence.local_evidence_conflict',
};
const fingerprintKeys: Readonly<Record<string, MessageId>> = {
 unsupported_algorithm: 'musicMetadata.fingerprint.unsupported_algorithm',
 not_applicable: 'musicMetadata.fingerprint.not_applicable',
 disabled: 'musicMetadata.fingerprint.disabled',
 scan_policy_disallows: 'musicMetadata.fingerprint.scan_policy_disallows',
 credential_required: 'musicMetadata.fingerprint.credential_required',
 not_available: 'musicMetadata.fingerprint.not_available',
 stale_or_invalid: 'musicMetadata.fingerprint.stale_or_invalid',
 provider_disabled: 'musicMetadata.fingerprint.provider_disabled',
 provider_backoff: 'musicMetadata.fingerprint.provider_backoff',
 provider_unavailable: 'musicMetadata.fingerprint.provider_unavailable',
 no_match: 'musicMetadata.fingerprint.no_match',
 matched: 'musicMetadata.fingerprint.matched',
 ready: 'musicMetadata.fingerprint.ready',
};
/** Known codes read from the viewer's catalogue. A code this build does not know keeps its own
 * words (the current fallback) rather than borrowing wording that may not describe it. */
export function musicEvidenceLabel(reason:string,t:MusicMetadataTranslate=serviceText):string {
 return Object.hasOwn(evidenceKeys,reason)?t(evidenceKeys[reason]):reason.replaceAll('_',' ');
}
export function fingerprintStatusLabel(status:string,t:MusicMetadataTranslate=serviceText):string {
 return Object.hasOwn(fingerprintKeys,status)?t(fingerprintKeys[status]):status.replaceAll('_',' ');
}

export type LocalAudioPolicy=Readonly<{revision:number;localMode:'prefer'|'supplement'|'off'}>;
export function parseLocalAudioPolicy(v:unknown):LocalAudioPolicy {
 if(!obj(v)||!Number.isSafeInteger(v.revision)||Number(v.revision)<1||!['prefer','supplement','off'].includes(v.localMode as string))invalid();
 return Object.freeze({revision:v.revision,localMode:v.localMode}) as LocalAudioPolicy;
}
export async function saveLocalBookPolicy(api:{request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>},itemId:string,libraryId:string,policy:LocalAudioPolicy,localMode:LocalAudioPolicy['localMode'],signal:AbortSignal):Promise<void> {
 parseLocalAudioPolicy({...policy,localMode});
 if(!itemId||!libraryId||itemId.length>256||libraryId.length>256||signal.aborted)throw new Error('Local metadata scope changed.');
 let abort:(()=>void)|undefined;
 try {
 const out=await Promise.race([api.request<RouteResponse<"post_items_id_metadata_local_audio_policy">>(encodedRoutePath("post_items_id_metadata_local_audio_policy", {"id": encodeURIComponent(itemId)}),'POST',routeBody("post_items_id_metadata_local_audio_policy", {libraryId,expectedRevision:policy.revision,localMode}),signal),new Promise<never>((_,reject)=>{abort=()=>reject(new Error('Local metadata request cancelled. Refresh to confirm the current policy.'));signal.addEventListener('abort',abort,{once:true});if(signal.aborted)abort();})]);
 if(signal.aborted)throw new Error('Local metadata request cancelled. Refresh to confirm the current policy.');
 if(!obj(out)||out.entityId!==itemId||out.status!=='saved')invalid();
 } finally {if(abort)signal.removeEventListener('abort',abort);}
}
