import {routePath} from "../../contracts/src/route-paths.ts";
import {unreadableServerResponse} from './server-messages.ts';
import type {I18n, MessageId} from '../../i18n/src/index.ts';
/** Where the OpenSubtitles key and account in force come from and how OpenSubtitles last answered. Never a secret. */
export type OpenSubtitlesStatus=Readonly<{keySource:'owner'|'environment'|'none';accountSource:'owner'|'environment'|'none';account:'none'|'incomplete'|'not_checked'|'signed_in'|'rejected';problem:string;checkedAt:string;retryAt:string;downloadsRemaining:number;downloadsAllowed:number;downloadsResetAt:string}>;
/** The OpenSubtitles fields say whether the owner saved their own key and password (never sent back) and the account name. */
export type ArtworkPreference = 'folders' | 'online';
export type ArtworkLanguage = 'same' | 'en' | 'textless';
export type ArtworkMaxEdge=1280|1920|2560;
const isArtworkMaxEdge=(value:unknown):value is ArtworkMaxEdge=>value===1280||value===1920||value===2560;
export type MetadataLibrarySettings=Readonly<{id:string;name:string;kind:string;revision:string;agent:'online'|'local';agentOptions:readonly ('online'|'local')[];lyricsSource:'files'|'lrclib';episodeThumbnailMode:'provider_then_made'|'provider'|'off';scrubPreviews:boolean;canScrub:boolean;managed:boolean}>;
export type MetadataLibraryChange=Readonly<{id:string;expectedRevision:string;agent?:MetadataLibrarySettings['agent'];lyricsSource?:MetadataLibrarySettings['lyricsSource'];episodeThumbnailMode?:MetadataLibrarySettings['episodeThumbnailMode'];scrubPreviews?:boolean}>;
function parseLibraries(raw:unknown):readonly MetadataLibrarySettings[]{
 if(raw===undefined)return Object.freeze([]);
 if(!Array.isArray(raw)||raw.length>512)throw new Error(unreadableServerResponse);
 const seen=new Set<string>();
 return Object.freeze(raw.map(value=>{
  if(!value||typeof value!=='object'||Array.isArray(value))throw new Error(unreadableServerResponse);
  const v=value as Record<string,unknown>;
  if(typeof v.id!=='string'||!v.id||v.id.length>128||seen.has(v.id)||typeof v.name!=='string'||v.name.length>100||typeof v.kind!=='string'||v.kind.length>32||typeof v.revision!=='string'||!/^[a-f0-9]{64}$/.test(v.revision)||typeof v.agent!=='string'||!['local','online'].includes(v.agent)||!Array.isArray(v.agentOptions)||v.agentOptions.length<1||v.agentOptions.length>2||new Set(v.agentOptions).size!==v.agentOptions.length||!v.agentOptions.every(a=>a==='local'||a==='online')||!v.agentOptions.includes(v.agent)||typeof v.lyricsSource!=='string'||!['files','lrclib'].includes(v.lyricsSource)||typeof v.episodeThumbnailMode!=='string'||!['provider_then_made','provider','off'].includes(v.episodeThumbnailMode)||typeof v.scrubPreviews!=='boolean'||typeof v.canScrub!=='boolean'||typeof v.managed!=='boolean')throw new Error(unreadableServerResponse);
  seen.add(v.id);
  return Object.freeze({id:v.id,name:v.name,kind:v.kind,revision:v.revision,agent:v.agent,agentOptions:Object.freeze([...v.agentOptions]),lyricsSource:v.lyricsSource,episodeThumbnailMode:v.episodeThumbnailMode,scrubPreviews:v.scrubPreviews,canScrub:v.canScrub,managed:v.managed}) as MetadataLibrarySettings;
 }));
}
export type MetadataRefreshPolicy='regular'|'recent'|'never';
export type MetadataFallbackLanguage='en'|'original'|'blank';
export type MetadataAnimeTitles='language'|'english'|'romaji'|'native';
/** Server › Metadata › Refresh all: when it was last asked for, and whether its paced sweep is still waking titles. */
export type MetadataRefreshAll=Readonly<{running:boolean;requestedAt:string}>;
/** Advanced: which held titles are refreshed on their own, what fills a gap in the metadata language, AniList's title form,
 * cast and crew photos, and the owner's AcoustID key (never sent back; the source says which key is in force). */
export type MetadataAdvanced=Readonly<{refreshPolicy:MetadataRefreshPolicy;fallbackLanguage:MetadataFallbackLanguage;animeTitles:MetadataAnimeTitles;castPhotos:boolean;acoustidKeyConfigured:boolean;acoustidKeySource:'owner'|'environment'|'none';refreshAll:MetadataRefreshAll}>;
const advancedWords={refreshPolicy:['regular','recent','never'],fallbackLanguage:['en','original','blank'],animeTitles:['language','english','romaji','native']} as const;
export type MetadataSettings=Readonly<{advanced?:MetadataAdvanced;artworkLanguage?:ArtworkLanguage;artworkPreference?:ArtworkPreference;onlineLookups?:boolean;defaultLanguage?:string;defaultRegion?:string;librariesComplete?:boolean;libraries?:readonly MetadataLibrarySettings[];revision:number;localArtworkLimitBytes:number;localArtworkUsedBytes:number;includeAdult:boolean;artworkMaxEdge:ArtworkMaxEdge;tmdbCredentialConfigured:boolean;tvdbCredentialConfigured:boolean;openSubtitlesKeyConfigured:boolean;openSubtitlesUsername:string;openSubtitlesPasswordConfigured:boolean;openSubtitles:OpenSubtitlesStatus}>;
/** A credential left out is unchanged; an empty one goes back to the server's own (for the username: no account). */
export type MetadataSettingsChange=Readonly<{refreshPolicy?:MetadataRefreshPolicy;fallbackLanguage?:MetadataFallbackLanguage;animeTitles?:MetadataAnimeTitles;castPhotos?:boolean;acoustidKey?:string;artworkLanguage?:ArtworkLanguage;artworkPreference?:ArtworkPreference;onlineLookups?:boolean;defaultLanguage?:string;defaultRegion?:string;libraries?:readonly MetadataLibraryChange[];expectedRevision:number;localArtworkLimitBytes:number;includeAdult:boolean;artworkMaxEdge?:ArtworkMaxEdge;tmdbReadToken?:string;tvdbProjectKey?:string;openSubtitlesApiKey?:string;openSubtitlesUsername?:string;openSubtitlesPassword?:string}>;
export function parseMetadataSettings(raw:unknown):MetadataSettings{
 if(typeof raw!=='object'||raw===null||Array.isArray(raw))throw new Error(unreadableServerResponse);
 const v=raw as Record<string,unknown>;
 if(v.artworkLanguage!==undefined&&v.artworkLanguage!=='same'&&v.artworkLanguage!=='en'&&v.artworkLanguage!=='textless')throw new Error(unreadableServerResponse);
 if(v.artworkPreference!==undefined&&v.artworkPreference!=='folders'&&v.artworkPreference!=='online')throw new Error(unreadableServerResponse);
 if(v.onlineLookups!==undefined&&typeof v.onlineLookups!=='boolean')throw new Error(unreadableServerResponse);
 if(v.defaultLanguage!==undefined&&(typeof v.defaultLanguage!=='string'||!/^([a-z]{2})$/.test(v.defaultLanguage))||v.defaultRegion!==undefined&&(typeof v.defaultRegion!=='string'||!/^[A-Z]{2}$/.test(v.defaultRegion)))throw new Error(unreadableServerResponse);
 if(!isArtworkMaxEdge(v.artworkMaxEdge))throw new Error(unreadableServerResponse);
 if(typeof v.revision!=='number'||!Number.isSafeInteger(v.revision)||v.revision<1||typeof v.localArtworkLimitBytes!=='number'||!Number.isSafeInteger(v.localArtworkLimitBytes)||v.localArtworkLimitBytes<1||typeof v.includeAdult!=='boolean'||typeof v.tmdbCredentialConfigured!=='boolean'||typeof v.tvdbCredentialConfigured!=='boolean')throw new Error(unreadableServerResponse);
 if(typeof v.localArtworkUsedBytes!=="number"||!Number.isSafeInteger(v.localArtworkUsedBytes)||v.localArtworkUsedBytes<0)throw new Error(unreadableServerResponse);
 const advanced=parseAdvanced(v);
 return Object.freeze({...(advanced?{advanced}:{}),...(v.artworkLanguage==='same'||v.artworkLanguage==='en'||v.artworkLanguage==='textless'?{artworkLanguage:v.artworkLanguage}:{}),...(v.artworkPreference==='folders'||v.artworkPreference==='online'?{artworkPreference:v.artworkPreference}:{}),...(typeof v.onlineLookups==='boolean'?{onlineLookups:v.onlineLookups}:{}),...(typeof v.defaultLanguage==='string'?{defaultLanguage:v.defaultLanguage}:{}),...(typeof v.defaultRegion==='string'?{defaultRegion:v.defaultRegion}:{}),librariesComplete:v.librariesComplete===true,libraries:v.librariesComplete===true?parseLibraries(v.libraries):Object.freeze([]),localArtworkUsedBytes:v.localArtworkUsedBytes,revision:v.revision,localArtworkLimitBytes:v.localArtworkLimitBytes,includeAdult:v.includeAdult,artworkMaxEdge:v.artworkMaxEdge,tmdbCredentialConfigured:v.tmdbCredentialConfigured,tvdbCredentialConfigured:v.tvdbCredentialConfigured,
  openSubtitlesKeyConfigured:v.openSubtitlesKeyConfigured===true,openSubtitlesUsername:typeof v.openSubtitlesUsername==='string'&&v.openSubtitlesUsername.length<=512?v.openSubtitlesUsername:'',openSubtitlesPasswordConfigured:v.openSubtitlesPasswordConfigured===true,openSubtitles:parseOpenSubtitlesStatus(v.openSubtitles)});
}
const word=<T extends string>(v:unknown,allowed:readonly T[],fallback:T):T=>allowed.includes(v as T)?v as T:fallback;
/** The Advanced producers arrive together; a server without them (or with a value this version cannot read) offers none of their rows. */
function parseAdvanced(v:Record<string,unknown>):MetadataAdvanced|undefined{
 const known=<T extends string>(value:unknown,allowed:readonly T[]):value is T=>allowed.includes(value as T);
 if(!known(v.refreshPolicy,advancedWords.refreshPolicy)||!known(v.fallbackLanguage,advancedWords.fallbackLanguage)||!known(v.animeTitles,advancedWords.animeTitles)||typeof v.castPhotos!=='boolean'||typeof v.acoustidKeyConfigured!=='boolean')return undefined;
 const all=(typeof v.refreshAll==='object'&&v.refreshAll!==null&&!Array.isArray(v.refreshAll)?v.refreshAll:{}) as Record<string,unknown>;
 return Object.freeze({refreshPolicy:v.refreshPolicy,fallbackLanguage:v.fallbackLanguage,animeTitles:v.animeTitles,castPhotos:v.castPhotos,acoustidKeyConfigured:v.acoustidKeyConfigured,
  acoustidKeySource:word(v.acoustidKeySource,['owner','environment','none'] as const,'none'),refreshAll:Object.freeze({running:all.running===true,requestedAt:instant(all.requestedAt)})});
}
const instant=(v:unknown)=>typeof v==='string'&&v.length<=40&&Number.isFinite(Date.parse(v))?v:'';
const count=(v:unknown)=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0?v:-1;
/** A status that cannot be read is "nothing known", never a reason to refuse the settings page. */
export function parseOpenSubtitlesStatus(raw:unknown):OpenSubtitlesStatus{
 const v=(typeof raw==='object'&&raw!==null&&!Array.isArray(raw)?raw:{}) as Record<string,unknown>;
 const sources=['owner','environment','none'] as const;
 return Object.freeze({keySource:word(v.keySource,sources,'none'),accountSource:word(v.accountSource,sources,'none'),account:word(v.account,['none','incomplete','not_checked','signed_in','rejected'] as const,'none'),
  problem:typeof v.problem==='string'&&/^[a-z_]{1,64}$/.test(v.problem)?v.problem:'',checkedAt:instant(v.checkedAt),retryAt:instant(v.retryAt),downloadsRemaining:count(v.downloadsRemaining),downloadsAllowed:count(v.downloadsAllowed),downloadsResetAt:instant(v.downloadsResetAt)});
}
export type OpenSubtitlesSummary=Readonly<{tone:'healthy'|'warning'|'danger'|'neutral';text:string;downloads?:string}>;
/** A time that may be tomorrow: the time alone within the day, otherwise with its date. */
function when(i18n:I18n,at:string,now:number):string{const t=Date.parse(at);return t-now<20*3600_000?i18n.time(t):i18n.dateTime(t);}
/** What Server › Metadata says about OpenSubtitles: whether it is set up, works, and how many downloads are left, in the owner's words. */
export function openSubtitlesSummary(status:OpenSubtitlesStatus,i18n:I18n,now=Date.now()):OpenSubtitlesSummary{
 const {t}=i18n;
 const downloads=status.downloadsRemaining<0?undefined:status.downloadsAllowed>0?t('server.metadata.openSubtitlesDownloadsOf',{remaining:status.downloadsRemaining,allowed:status.downloadsAllowed}):t('server.metadata.openSubtitlesDownloadsLeft',{remaining:status.downloadsRemaining});
 const later=(id:MessageId,at:string)=>at?t(id,{time:when(i18n,at,now)}):t('server.metadata.openSubtitlesLater');
 if(status.keySource==='none')return Object.freeze({tone:'neutral',text:t('server.metadata.openSubtitlesNotSetUp')});
 if(status.account==='rejected'||status.problem==='provider_sign_in_failed')return Object.freeze({tone:'danger',text:t('server.metadata.openSubtitlesAccountRejected')});
 const problems:Record<string,()=>OpenSubtitlesSummary>={
  provider_key_rejected:()=>({tone:'danger',text:t('server.metadata.openSubtitlesKeyRejected')}),
  provider_sign_in_required:()=>({tone:'warning',text:t('server.metadata.openSubtitlesNeedsAccount')}),
  provider_quota_exhausted:()=>({tone:'warning',text:later('server.metadata.openSubtitlesQuotaUsed',status.downloadsResetAt||status.retryAt)}),
  provider_rate_limited:()=>({tone:'warning',text:later('server.metadata.openSubtitlesSlowDown',status.retryAt)}),
  provider_unreachable:()=>({tone:'warning',text:t('server.metadata.openSubtitlesUnreachable')}),
 };
 const problem=problems[status.problem];
 if(problem)return Object.freeze({...problem(),...(downloads?{downloads}:{})});
 if(status.account==='incomplete')return Object.freeze({tone:'warning',text:t('server.metadata.openSubtitlesNoPassword')});
 if(!status.checkedAt)return Object.freeze({tone:'neutral',text:t('server.metadata.openSubtitlesNotTested')});
 return Object.freeze({tone:'healthy',text:status.account==='signed_in'?t('server.metadata.openSubtitlesSignedIn'):t('server.metadata.openSubtitlesKeyWorks'),...(downloads?{downloads}:{})});
}
type Request=<T>(path:string,method?:string,body?:unknown)=>Promise<T>;
export function metadataSettingsService(request:Request){const path=routePath("get_metadata_settings");return Object.freeze({async read(){return parseMetadataSettings(await request<unknown>(path));},async save(change:MetadataSettingsChange){if(change.artworkMaxEdge!==undefined&&!isArtworkMaxEdge(change.artworkMaxEdge))throw new Error(unreadableServerResponse);return parseMetadataSettings(await request<unknown>(path,'PUT',change));},
 /** Refresh all: every title is refreshed from the providers in the background, within the daily allowance. */
 async refreshAll(){return parseMetadataSettings(await request<unknown>(routePath("refresh_all_metadata"),'POST'));},
 /** Test connection: OpenSubtitles is asked now whether the saved key and account work. */
 async checkOpenSubtitles(){return parseOpenSubtitlesStatus(await request<unknown>(routePath("check_opensubtitles"),'POST'));}});}
const GIBIBYTE=2**30;
/** The stored artwork limit as its field shows it, in gigabytes: a whole number where it is one, otherwise up to two places. */
export function artworkLimitText(bytes:number):string{const gb=bytes/GIBIBYTE;return Number.isInteger(gb)?String(gb):gb.toFixed(2).replace(/0+$/,'').replace(/\.$/,'');}
/** What a typed limit stores, or nothing when it is not a size the server takes (0.1 GB to a million). A comma is read as the decimal mark. */
export function artworkLimitBytes(text:string):number|undefined{const typed=text.trim().replace(',','.');if(!/^\d+(\.\d+)?$/.test(typed))return undefined;const gb=Number(typed);return gb>=0.1&&gb<=1_000_000?Math.round(gb*GIBIBYTE):undefined;}
