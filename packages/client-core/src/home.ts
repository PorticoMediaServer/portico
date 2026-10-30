import {routeBody} from "../../contracts/src/routes.ts";
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {routePath, encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {unreadableServerResponse} from './server-messages.ts';
/** Server-composed home rows. Clients render membership, order and policy as published; they never rank, hide or page a row themselves. */
import {validateContentEntry, type ContentEntry} from './library-content.ts';
import {parseBrowseNode, type BrowseNode, type BrowseSortSelection} from './browse.ts';
import {firstOfEach, keepReadable, noteSkipped, oneOfOr} from './tolerant.ts';
import {parseGuideChannels, parseGuideSources, type GuideChannel, type GuideSource} from './channel-guide.ts';

export type HomeArtworkShape = 'poster' | 'square' | 'landscape';
export type HomeRevision = Readonly<{catalog: number; viewer: number}>;
export type HomeActionState = Readonly<{watchlisted:boolean;favorite:boolean;watched:boolean;rating:number|null;personalRevision:number;containerRevision:number}>;
/** Selection commands may expand a container to its permitted playable members.
 * They do not imply a saved flag on the displayed aggregate itself. */
export type HomeSelectionActions = Readonly<{
 play:boolean;queue:boolean;playlist:boolean;collection:boolean;myList:boolean;favorite:boolean;watched:boolean;
 download:boolean;edit:boolean;refresh:boolean;delete:boolean;
}>;
/** Server grants for implemented commands. Platform support is applied separately. */
export type HomeActions = Readonly<{
 play:boolean;queue:boolean;playlist:boolean;collection:boolean;myList:boolean;favorite:boolean;watched:boolean;played:boolean;rating:boolean;
 editMetadata:boolean;refreshMetadata:boolean;delete:boolean;download:boolean;details:boolean;removeFromContinue?:boolean;notInterested?:boolean;state:HomeActionState;selection:HomeSelectionActions;
}>;
/** Genuine parent-show facts for a returned Home episode, independent of the
 * episode's own year/genres and of its selection or playback identity. */
export type HomeTitleContext = Readonly<{kind:'show';year?:number;genres:readonly string[];seasonCount?:number}>;
export type HomeEntry = ContentEntry & Readonly<{homeActions?:HomeActions;homeTitleContext?:HomeTitleContext}>;
export type HomeChannelActions = Readonly<{play:boolean;favorite:boolean}>;
/** A server-authored label (CON-19): a catalogue message id, its parameters, and the US English
 * fallback for a code this client doesn't know. Name rows with `serverTextLabel`. */
export type ServerText = Readonly<{code: string; params?: Readonly<Record<string, string>>; fallback: string}>;
export type HomeSeeAll = Readonly<
 {type:'library';libraryId:string;pivot:string;query?:BrowseNode;sort:readonly BrowseSortSelection[]} |
 {type:'row';rowId:string} | {type:'saved_view';resourceId:string} |
 {type:'collection';resourceId:string;libraryId:string} | {type:'guide';group?:string;sourceId?:string}
>;
export type HomeReason = Readonly<{kind:string;text:ServerText;source?:Readonly<{id:string;name:string}>;libraryId?:string}>;
export type HomeSpotlight = Readonly<{entry:HomeEntry;reason:HomeReason;resumeTarget?:HomeEntry}>;
export type HomeResume = Readonly<{entryId:string;target:HomeEntry}>;
export type HomePeriod = Readonly<{days:number;start:string;end:string;metric:string;text:ServerText}>;
export type HomeCollection = Readonly<{collection:Readonly<{id:string;libraryId:string;name:string;itemCount:number;revision:number}>;mediaKind:'movie';actions?:HomeActions;members:readonly HomeEntry[];backdropUrl?:string;firstYear?:number;lastTitle?:string}>;
/** The server resolves marks over the entire authorized source, before filtering or paging. */
export type HomeChannelMark = Readonly<{text:string;seed:string}>;
export type HomeChannel = Readonly<{channel:GuideChannel & Readonly<{mark:HomeChannelMark}>;source:GuideSource;actions:HomeChannelActions}>;
export type HomeLive = Readonly<{revision:string;observedAt:string;expiresAt:string;sort:'favorites';group:string;groups:readonly Readonly<{name:string;count:number}>[];channels:readonly HomeChannel[]}>;
export type HomeRow = Readonly<{
  id: string; viewerFence: string; title: string; titleText?: ServerText; kind: string; artworkShape: HomeArtworkShape; explanation?: string; why?:ServerText; whyTitles?:readonly Readonly<{id:string;name:string}>[]; displayTargets?:readonly Readonly<{membershipId:string;entryId:string}>[]; endpoint: string;
  /** `for_you` on the personal rows: the layout arranges and hides them together as the row `for_you`. */
  family?: string;
  deferred?: boolean; children?: readonly HomeRow[];
  reason?:HomeReason; resumes?:readonly HomeResume[]; collection?:HomeCollection; live?:HomeLive; period?:HomePeriod; subtitleText?:ServerText;
  platforms?:readonly ('web'|'phone'|'tv')[];
  seeAll?: HomeSeeAll;
  libraryId?: string; privacySensitivity: string; policyState: string;
  relation?: string; provider?: string; evidenceId?: string;
  priority: number;
  /** How long the server would let the row be reused. No client reads it; absent when the server does not send it. */
  cacheTtlMs?: number;
  required: boolean; hideable: boolean; reorderable: boolean; critical: boolean; cursorCapable: boolean;
  entries: readonly HomeEntry[]; total: number; start: number; limit: number; hasMore: boolean;
  nextCursor: string; anchorId?: string; revision: HomeRevision;
}>;
export type HomeLayout = Readonly<{revision: number; rowOrder: readonly string[]; hiddenRowIds: readonly string[]}>;
export type HomeLayoutRowView = Readonly<{availabilityText?:ServerText;id: string; title: string; titleText?: ServerText; kind: string; artworkShape: HomeArtworkShape; libraryId?: string; required: boolean; hideable: boolean; reorderable: boolean; hidden: boolean}>;
export type HomeLayoutView = Readonly<HomeLayout & {rows: readonly HomeLayoutRowView[]}>;
/** Frozen type dependency of presentation/home-hero.ts, removed with the old UI in Phase 10. */
export type HomeHeroRef = Readonly<{rowId: string; entryId: string; selectionKey?: string}>;
export type HomeDocument = Readonly<{
  serverId: string; viewerFence: string;
  /** Changes only when who the viewer is or what they may see changes; a library added or
   * removed changes `viewerFence` alone. Absent from older servers. */
  privacyFence?: string; rows: readonly HomeRow[]; spotlight:readonly HomeSpotlight[]; hero?: HomeHeroRef; layout: HomeLayout; revision: HomeRevision; generatedAt: string;
}>;
export type HomeRowPage = Readonly<{limit?: number; cursor?: string; start?: number; revision?: string; anchorId?: string;group?:string;sourceId?:string}>;
export type HomeLayoutIntent = Readonly<{expectedRevision: number; rowOrder: readonly string[]; hiddenRowIds: readonly string[]; idempotencyKey?: string}>;
export interface HomeApi {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>}

const shapes = ['poster', 'square', 'landscape'] as const;
const object = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);
const text = (v: unknown, max = 512): v is string => typeof v === 'string' && v.length <= max && !/[\x00-\x1f\x7f]/.test(v);
const id = (v: unknown): v is string => text(v, 256) && (v as string).length > 0;
const whole = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0;

function invalid(): never {
  throw Object.assign(new Error(unreadableServerResponse), {code: 'invalid_home', retryable: false});
}

/** An absent or unreadable offer grants nothing. Personal state never implies a grant. */
export function parseHomeActions(value:unknown):HomeActions|undefined {
  if(value===undefined||value===null)return undefined;
  if(!object(value)||!object(value.state)){noteSkipped('home','actions');return undefined;}
  const s=value.state;
  if(typeof s.watchlisted!=='boolean'||typeof s.favorite!=='boolean'||typeof s.watched!=='boolean'||!whole(s.personalRevision)||!whole(s.containerRevision)||(s.rating!==null&&(typeof s.rating!=='number'||!Number.isFinite(s.rating)||s.rating<0||s.rating>10))){noteSkipped('home','actions.state');return undefined;}
  const flags=['play','queue','playlist','collection','myList','favorite','watched','played','rating','editMetadata','refreshMetadata','delete','download','details'] as const;
  const offers=Object.fromEntries(flags.map(flag=>[flag,value[flag]===true])) as Omit<HomeActions,'state'|'selection'>;
  const selectionFlags=['play','queue','playlist','collection','myList','favorite','watched','download','edit','refresh','delete'] as const;
  const suppliedSelection:Record<string,unknown>=object(value.selection)?value.selection:{};
  const selection=Object.freeze(Object.fromEntries(selectionFlags.map(flag=>[flag,suppliedSelection[flag]===true])) as HomeSelectionActions);
  return Object.freeze({...offers,...(value.notInterested===true?{notInterested:true}:{}),...(value.removeFromContinue===true?{removeFromContinue:true}:{}),selection,state:Object.freeze({watchlisted:s.watchlisted,favorite:s.favorite,watched:s.watched,rating:s.rating as number|null,personalRevision:s.personalRevision,containerRevision:s.containerRevision})});
}

function parseHomeTitleContext(value:unknown):HomeTitleContext|undefined {
  if(value===undefined||value===null)return undefined;
  if(!object(value)||value.kind!=='show'||!Array.isArray(value.genres)||value.genres.length>2||value.genres.some(genre=>!id(genre))||
    value.year!==undefined&&(!whole(value.year)||value.year<1)||value.seasonCount!==undefined&&!whole(value.seasonCount)){
    noteSkipped('home','titleContext');return undefined;
  }
  return Object.freeze({kind:'show',genres:Object.freeze([...value.genres] as string[]),
    ...(value.year===undefined?{}:{year:value.year as number}),...(value.seasonCount===undefined?{}:{seasonCount:value.seasonCount as number})});
}

export function validateHomeEntry(value:unknown):HomeEntry {
  const entry=validateContentEntry(value);
  const actions=object(value)?parseHomeActions(value.homeActions):undefined;
  const context=entry.kind==='episode'&&object(value)?parseHomeTitleContext(value.homeTitleContext):undefined;
  return Object.freeze({...entry,...(actions?{homeActions:actions}:{}),...(context?{homeTitleContext:context}:{})});
}

/**
 * A ServerText, or undefined when absent. Text whose code or fallback cannot be read is refused;
 * parameters that cannot be read are left out, and the fallback then names the row.
 */
export function parseServerText(value: unknown): ServerText | undefined {
  if (value === undefined || value === null) return undefined;
  if (!object(value) || !text(value.code, 128) || !text(value.fallback, 1024)) invalid();
  if (value.params === undefined || value.params === null) return Object.freeze({code: value.code, fallback: value.fallback});
  if (!object(value.params) || Object.keys(value.params).length > 16) { noteSkipped('home', 'titleText.params'); return Object.freeze({code: value.code, fallback: value.fallback}); }
  const params: Record<string, string> = {};
  for (const [name, param] of Object.entries(value.params)) {
    if (!id(name) || !text(param, 1024)) { noteSkipped('home', 'titleText.params'); continue; }
    params[name] = param;
  }
  return Object.freeze({code: value.code, params: Object.freeze(params), fallback: value.fallback});
}

/** A row's title text, left out (the plain title then names the row) when it cannot be read. */
function titleTextOf(value: unknown): ServerText | undefined {
  try { return parseServerText(value); } catch { noteSkipped('home', 'titleText'); return undefined; }
}
/** Text a row is drawn with: absent reads as empty, and text over its bound refuses the row. */
function words(value: unknown, max: number): string {
  if (value === undefined || value === null) return '';
  if (!text(value, max)) invalid();
  return value;
}

function revision(value: unknown): HomeRevision {
  if (!object(value) || !whole(value.catalog) || !whole(value.viewer)) invalid();
  return Object.freeze({catalog: value.catalog, viewer: value.viewer});
}

const instant=(v:unknown):v is string=>text(v,128)&&Number.isFinite(Date.parse(v));
export function parseHomeReason(value:unknown):HomeReason {
  if(!object(value)||!id(value.kind))invalid();
  const label=parseServerText(value.text)??invalid();
  let source:HomeReason['source'];
  if(value.source!==undefined){
    if(!object(value.source)||!id(value.source.id)||!text(value.source.name,2048))invalid();
    source=Object.freeze({id:value.source.id,name:value.source.name});
  }
  if(value.kind==='because_you_watched'&&!source)invalid();
  if(value.libraryId!==undefined&&!id(value.libraryId))invalid();
  return Object.freeze({kind:value.kind,text:label,...(source?{source}:{}),...(value.libraryId===undefined?{}:{libraryId:value.libraryId as string})});
}
export function parseHomeSpotlight(value:unknown):HomeSpotlight {
  if(!object(value))invalid();
  const entry=validateHomeEntry(value.entry);
  if(!['movie','show'].includes(entry.kind)||[...(entry.overview??'')].length>300)invalid();
  const reason=parseHomeReason(value.reason);
  const resumeTarget=value.resumeTarget===undefined?undefined:validateHomeEntry(value.resumeTarget);
  if(resumeTarget&&(entry.kind!=='show'||resumeTarget.kind!=='episode'||resumeTarget.show?.id!==entry.id||!resumeTarget.playback))invalid();
  return Object.freeze({entry,reason,...(resumeTarget?{resumeTarget}:{})});
}
function parseHomeCollection(value:unknown):HomeCollection {
  if(!object(value)||!object(value.collection)||value.mediaKind!=='movie'||!Array.isArray(value.members)||value.members.length>4)invalid();
  const c=value.collection;
  if(!id(c.id)||!id(c.libraryId)||!text(c.name,1024)||!whole(c.itemCount)||!whole(c.revision)||c.itemCount<value.members.length)invalid();
  const members=Object.freeze(value.members.map(v=>validateHomeEntry(v)));
  if(members.some(e=>e.libraryId!==c.libraryId||e.kind!=='movie'))invalid();
  if(value.firstYear!==undefined&&(!whole(value.firstYear)||value.firstYear<1))invalid();
  if(value.backdropUrl!==undefined&&!text(value.backdropUrl,4096)||value.lastTitle!==undefined&&!text(value.lastTitle,2048))invalid();
  const actions=parseHomeActions(value.actions);
  return Object.freeze({collection:Object.freeze({id:c.id,libraryId:c.libraryId,name:c.name,itemCount:c.itemCount,revision:c.revision}),mediaKind:'movie',members,...(actions?{actions}:{}),
    ...(value.backdropUrl===undefined?{}:{backdropUrl:value.backdropUrl as string}),...(value.firstYear===undefined?{}:{firstYear:value.firstYear as number}),...(value.lastTitle===undefined?{}:{lastTitle:value.lastTitle as string})});
}
function parseHomeLive(value:unknown):HomeLive {
  if(!object(value)||!id(value.revision)||!instant(value.observedAt)||!instant(value.expiresAt)||Date.parse(value.expiresAt)<=Date.parse(value.observedAt)||value.sort!=='favorites'||!text(value.group,120)||!Array.isArray(value.groups)||value.groups.length>2000||!Array.isArray(value.channels)||value.channels.length>50)invalid();
  const observedAt=value.observedAt;
  const groups=Object.freeze(value.groups.map(v=>{
    if(!object(v)||!id(v.name)||!whole(v.count))invalid();
    return Object.freeze({name:v.name,count:v.count});
  }));
  const channels=Object.freeze(value.channels.map(v=>{
    if(!object(v)||!object(v.channel)||!object(v.channel.mark)||!id(v.channel.mark.text)||!id(v.channel.mark.seed)||!Array.isArray(v.channel.programmes)||v.channel.programmes.length>1)invalid();
    const source=parseGuideSources([v.source])[0];
    if(!source)invalid();
    const channel=parseGuideChannels([v.channel],[source],{kind:source.provenance,start:observedAt,end:new Date(Date.parse(observedAt)+1000).toISOString(),timezone:'UTC',search:'',sourceId:source.id})[0];
    if(!channel||channel.hidden||channel.programmes.length!==v.channel.programmes.length||channel.programmes.some(p=>Date.parse(p.start)>Date.parse(observedAt)||Date.parse(p.end)<=Date.parse(observedAt)))invalid();
    const actions=Object.freeze({play:object(v.actions)&&v.actions.play===true&&channel.tuneAvailable,favorite:object(v.actions)&&v.actions.favorite===true});
    return Object.freeze({source:Object.freeze(source),actions,channel:Object.freeze({...channel,programmes:Object.freeze(channel.programmes),mark:Object.freeze({text:v.channel.mark.text,seed:v.channel.mark.seed})})});
  }));
  const keys=channels.map(v=>v.channel.provenance+':'+v.channel.sourceId+':'+v.channel.id);
  if(new Set(keys).size!==keys.length)invalid();
  return Object.freeze({revision:value.revision,observedAt,expiresAt:value.expiresAt,sort:'favorites',group:value.group,groups,channels});
}
export const hasHomeRowContent=(row:HomeRow):boolean=>row.entries.length>0||!!row.collection?.members.length||!!row.live?.channels.length;

export function validateHomeSeeAll(value:unknown):HomeSeeAll {
 if(!object(value))invalid();
 if(value.type==='row' && id(value.rowId))return Object.freeze({type:'row',rowId:value.rowId});
 if(value.type==='saved_view' && id(value.resourceId))return Object.freeze({type:'saved_view',resourceId:value.resourceId});
 if(value.type==='collection' && id(value.resourceId) && id(value.libraryId))return Object.freeze({type:'collection',resourceId:value.resourceId,libraryId:value.libraryId});
 if(value.type==='guide') {
  if(value.group!==undefined&&!id(value.group)||value.sourceId!==undefined&&!id(value.sourceId))invalid();
  return Object.freeze({type:'guide',...(value.group===undefined?{}:{group:value.group as string}),...(value.sourceId===undefined?{}:{sourceId:value.sourceId as string})});
 }
 if(value.type!=='library'||!id(value.libraryId)||!id(value.pivot)||!Array.isArray(value.sort)||value.sort.length<1||value.sort.length>3)invalid();
 const sort=value.sort.map(s=>{if(!object(s)||!id(s.field)||(s.direction!=='asc'&&s.direction!=='desc'))invalid();return Object.freeze({field:s.field,direction:s.direction}) as BrowseSortSelection;});
 return Object.freeze({type:'library',libraryId:value.libraryId,pivot:value.pivot,sort:Object.freeze(sort),...(value.query===undefined?{}:{query:parseBrowseNode(value.query)})});
}

/**
 * One Home row. Its id, cursor, revision and the bound on its entries are exact. Everything it is
 * only drawn with has a neutral reading: an artwork shape this app does not know is a poster, a
 * missing flag is off, a missing count is 0, and an entry that cannot be read is left out alone.
 */
export function validateHomeRow(value: unknown): HomeRow {
  return parseHomeRow(value, true);
}

function parseHomeRow(value: unknown, allowChildren: boolean): HomeRow {
  if (!object(value) || !id(value.id) || !id(value.viewerFence)) invalid();
  if (!Array.isArray(value.entries) || value.entries.length > 200 || !text(value.nextCursor ?? '', 4096)) invalid();
  const entries = firstOfEach('home', 'entries', keepReadable('home', 'entries', value.entries, entry => validateHomeEntry(entry)), entry => entry.id);
  const flag = (name: string) => value[name] === true;
  const amount = (name: string) => (whole(value[name]) ? value[name] as number : 0);
  const required = flag('required');
  const row: Record<string, unknown> = {
    id: value.id, viewerFence: value.viewerFence, title: words(value.title, 1024), kind: words(value.kind, 256),
    artworkShape: oneOfOr('home', 'row.artworkShape', value.artworkShape, shapes, 'poster'), endpoint: words(value.endpoint, 256),
    privacySensitivity: words(value.privacySensitivity, 256), policyState: words(value.policyState, 256), priority: amount('priority'),
    // A required row is never hideable, whatever the two flags say together.
    required, hideable: flag('hideable') && !required,
    reorderable: flag('reorderable'), critical: flag('critical'), cursorCapable: flag('cursorCapable'),
    // The total is at least the entries kept.
    entries: Object.freeze(entries), total: Math.max(amount('total'), entries.length), start: amount('start'), limit: amount('limit'),
    hasMore: flag('hasMore'), nextCursor: value.nextCursor ?? '', revision: revision(value.revision),
  };
  if (value.deferred === true) row.deferred = true;
  if (value.children !== undefined) {
    if (!allowChildren || value.id !== 'for_you' || !Array.isArray(value.children) || value.children.length > 5) invalid();
    row.children = Object.freeze(value.children.map(child => {
      const parsed = parseHomeRow(child, false);
      if (parsed.family !== 'for_you' || !parsed.id.startsWith('for_you:') || parsed.deferred) invalid();
      return parsed;
    }));
  }
  if (value.reason !== undefined) row.reason = parseHomeReason(value.reason);
  if (value.collection !== undefined) row.collection = parseHomeCollection(value.collection);
  if (value.live !== undefined) row.live = parseHomeLive(value.live);
  if (value.period !== undefined) {
    const p=value.period;
    if(!object(p)||!whole(p.days)||p.days<1||!instant(p.start)||!instant(p.end)||Date.parse(p.start)>=Date.parse(p.end)||!id(p.metric))invalid();
    row.period=Object.freeze({days:p.days,start:p.start,end:p.end,metric:p.metric,text:parseServerText(p.text)??invalid()});
  }
  if (value.resumes !== undefined) {
    if(!Array.isArray(value.resumes)||value.resumes.length>100)invalid();
    row.resumes=Object.freeze(value.resumes.map(v=>{
      if(!object(v)||!id(v.entryId)||!entries.some(e=>e.id===v.entryId))invalid();
      const target=validateHomeEntry(v.target);
      if(!target.playback)invalid();
      return Object.freeze({entryId:v.entryId,target});
    }));
  }
  if(value.platforms!==undefined){
    if(!Array.isArray(value.platforms)||value.platforms.length>3||value.platforms.some(p=>!['web','phone','tv'].includes(p)))invalid();
    row.platforms=Object.freeze([...value.platforms]);
  }
  if(value.displayTargets!==undefined){
    if(!Array.isArray(value.displayTargets)||value.displayTargets.length>60)invalid();
    row.displayTargets=Object.freeze(value.displayTargets.map(v=>{if(!object(v)||!id(v.membershipId)||!id(v.entryId)||!entries.some(entry=>entry.id===v.entryId))invalid();return Object.freeze({membershipId:v.membershipId,entryId:v.entryId});}));
  }
  if (whole(value.cacheTtlMs)) row.cacheTtlMs = value.cacheTtlMs;
  const titleText = titleTextOf(value.titleText);
  if (titleText) row.titleText = titleText;
  const subtitleText=titleTextOf(value.subtitleText);
  if(subtitleText)row.subtitleText=subtitleText;
  if(value.why!==undefined)row.why=titleTextOf(value.why);
  if(value.whyTitles!==undefined){
    if(!Array.isArray(value.whyTitles)||value.whyTitles.length>3)invalid();
    row.whyTitles=Object.freeze(value.whyTitles.map(v=>{if(!object(v)||!id(v.id)||!text(v.name,2048))invalid();return Object.freeze({id:v.id,name:v.name});}));
  }
  for (const field of ['explanation', 'libraryId', 'relation', 'provider', 'evidenceId', 'anchorId', 'family'] as const) {
    if (text(value[field], 1024)) row[field] = value[field];
    else if (value[field] !== undefined && value[field] !== null) noteSkipped('home', 'row.' + field);
  }
  if (value.seeAll !== undefined && value.seeAll !== null) {
    try { row.seeAll = validateHomeSeeAll(value.seeAll); } catch { noteSkipped('home', 'row.seeAll'); }
  }
  return Object.freeze(row) as HomeRow;
}

/** A row as a content section heading: its title code and params when it has them (`sectionHeading`,
 * `serverTextLabel` name it), its id and title otherwise. */
export function homeRowHeading(row: Pick<HomeRow, 'id' | 'title' | 'titleText'>): Readonly<{key: string; fallback: string; params?: Readonly<Record<string, string>>}> {
  const code = row.titleText?.code;
  if (!code) return {key: row.id, fallback: row.title};
  return row.titleText?.params ? {key: code, fallback: row.title, params: row.titleText.params} : {key: code, fallback: row.title};
}

/**
 * The personal rows (Recommendations P2): (`for_you:<generator>[:<key>]`) are one
 * family in the layout, arranged and hidden together as the row `for_you`. Customize Home lists the
 * family, never a generator.
 */
export const PERSONAL_FAMILY = 'for_you';
export const isPersonalRowId = (rowId: string) => rowId.startsWith(PERSONAL_FAMILY + ':');

/**
 * Saved views on Home (Recommendations P6): the layout order may name `view:<savedResourceId>`;
 * Home then shows that view as a row (kind `saved_view`), under its name, in its own sort. A
 * viewer adds one by appending its id to the order and removes it by dropping the id; a view they
 * can no longer read is simply absent.
 */
export const HOME_VIEW_PREFIX = 'view:';
export const homeViewRowId = (resourceId: string) => HOME_VIEW_PREFIX + resourceId;
/** The saved view a Home row shows, or undefined for any other row. */
export const homeViewResourceId = (rowId: string): string | undefined => (rowId.startsWith(HOME_VIEW_PREFIX) && rowId.length > HOME_VIEW_PREFIX.length ? rowId.slice(HOME_VIEW_PREFIX.length) : undefined);

/** The saved layout. Its revision and list bounds are exact; an entry that is not an id, or a repeated id, is left out. */
export function validateHomeLayout(value: unknown): HomeLayout {
  if (!object(value) || !whole(value.revision) || !Array.isArray(value.rowOrder) || !Array.isArray(value.hiddenRowIds)) invalid();
  if (value.rowOrder.length > 64 || value.hiddenRowIds.length > 64) invalid();
  const ids = (part: string, list: unknown[]) => firstOfEach('home', part, keepReadable('home', part, list, entry => (id(entry) ? entry : undefined)), entry => entry);
  return Object.freeze({revision: value.revision, rowOrder: Object.freeze(ids('layout.rowOrder', value.rowOrder)), hiddenRowIds: Object.freeze(ids('layout.hiddenRowIds', value.hiddenRowIds))});
}

export function validateHomeLayoutView(value: unknown): HomeLayoutView {
  const layout = validateHomeLayout(value);
  if (!object(value) || !Array.isArray(value.rows) || value.rows.length > 64) invalid();
  // A row that cannot be read is left out of Customize Home alone; contradictory flags take the reading that keeps a row visible.
  const rows = firstOfEach('home', 'layout.rows', keepReadable('home', 'layout.rows', value.rows as unknown[], entry => {
    if (!object(entry) || !id(entry.id)) invalid();
    const row = entry as Record<string, unknown>;
    const required = row.required === true, hideable = row.hideable === true && !required;
    const titleText = titleTextOf(row.titleText);
    return Object.freeze({
      ...(row.availabilityText?{availabilityText:titleTextOf(row.availabilityText)}:{}),
      id: row.id as string, title: words(row.title, 1024), ...(titleText ? {titleText} : {}), kind: words(row.kind, 256), artworkShape: oneOfOr('home', 'layout.artworkShape', row.artworkShape, shapes, 'poster'),
      ...(id(row.libraryId) ? {libraryId: row.libraryId} : {}),
      required, hideable, reorderable: row.reorderable === true, hidden: row.hidden === true && hideable,
    });
  }), row => row.id);
  return Object.freeze({...layout, rows: Object.freeze(rows)});
}

/**
 * Home. The server, the viewer fence, the layout, the revision and the bound on rows are exact.
 * A row that cannot be read is left out alone, a repeated row keeps its first appearance, and a
 * row the viewer hid or an empty row that is not critical is not shown.
 */
/** The fence a whole client shell is keyed on: the privacy fence, or on an older server the viewer
 * fence. A shell rebuilds only when this changes; a library added or removed just refreshes. */
export function homeShellFence(document: Pick<HomeDocument, 'viewerFence' | 'privacyFence'>): string {
  return document.privacyFence ?? document.viewerFence;
}
export function validateHomeDocument(value: unknown): HomeDocument {
  if (!object(value) || !id(value.serverId) || !id(value.viewerFence) || !Array.isArray(value.rows) || value.rows.length > 64 || (value.spotlight !== undefined && (!Array.isArray(value.spotlight) || value.spotlight.length>5))) invalid();
  // R-10 (O-159 two-tier parsing): spotlights are presentation, not identity or safety. One this
  // client cannot read is left out alone; an absent list is no spotlight. Home itself still shows.
  const spotlight=Object.freeze(keepReadable('home', 'spotlight', (value.spotlight ?? []) as unknown[], parseHomeSpotlight));
  const layout = validateHomeLayout(value.layout);
  const readable = keepReadable('home', 'rows', value.rows, raw => {
    const row = validateHomeRow(raw);
    // An entry of a kind this app does not know leaves its row in place; a row the server sent empty is kept only when critical.
    if(row.viewerFence!==value.viewerFence)invalid();
    return {row, shown: !layout.hiddenRowIds.includes(row.id) && ((raw as {entries: unknown[]}).entries.length > 0 || hasHomeRowContent(row) || row.critical || row.deferred)};
  });
  // A Home none of whose rows could be read is refused rather than shown as empty.
  if (!readable.length && value.rows.length) invalid();
  const rows = firstOfEach('home', 'rows', readable.filter(read => read.shown).map(read => read.row), row => row.id);
  if (value.privacyFence !== undefined && !id(value.privacyFence)) invalid();
  return Object.freeze({serverId: value.serverId, viewerFence: value.viewerFence, ...(value.privacyFence !== undefined ? {privacyFence: value.privacyFence as string} : {}), rows: Object.freeze(rows), spotlight, layout, revision: revision(value.revision), generatedAt: words(value.generatedAt, 128)});
}

function pageQuery(page: HomeRowPage): string {
  const query = new URLSearchParams();
  if (page.limit !== undefined) query.set('limit', String(page.limit));
  if (page.cursor) query.set('cursor', page.cursor);
  if (page.start !== undefined) query.set('start', String(page.start));
  if (page.revision) query.set('revision', page.revision);
  if (page.anchorId) query.set('anchorId', page.anchorId);
  if (page.group) query.set('group',page.group);
  if (page.sourceId) query.set('sourceId',page.sourceId);
  const text = query.toString();
  return text ? '?' + text : '';
}

export async function fetchHome(api: HomeApi, limit?: number, signal?: AbortSignal): Promise<HomeDocument> {
  return validateHomeDocument(await api.request<RouteResponse<"get_home">>(routePath("get_home") + (limit === undefined ? '' : '?limit=' + limit), 'GET', undefined, signal));
}

export async function fetchHomeLayout(api: HomeApi, signal?: AbortSignal): Promise<HomeLayoutView> {
  return validateHomeLayoutView(await api.request<RouteResponse<"get_home_layout">>(routePath("get_home_layout"), 'GET', undefined, signal));
}

/** Start the next server page with about ten loaded cards left. */
export const HOME_ROW_PREFETCH_REMAINING = 10;
/** Skeleton cards represent a pending page, never an invented true end. */
export function homeRowPagingState(row: HomeRow, lastVisibleIndex: number, pending: boolean): Readonly<{prefetch: boolean; skeletons: number; exhausted: boolean}> {
  const count = row.live?.channels.length ?? row.entries.length;
  const more = row.nextCursor.length > 0;
  return Object.freeze({prefetch: more && !pending && count - 1 - lastVisibleIndex <= HOME_ROW_PREFETCH_REMAINING, skeletons: pending && more ? 1 : 0, exhausted: !more});
}

/** These server rows reject cursors from any different selected membership. */
export function hasFencedHomeMembership(rowId: string): boolean {
  return rowId.length > 0;
}

/** Keep the loaded prefix and append the server page until nextCursor is empty. */
export function appendHomeRowPage(row: HomeRow, page: HomeRow): HomeRow {
  if(row.id!==page.id||row.viewerFence!==page.viewerFence)invalid();
  if(!row.live&&(row.revision.catalog!==page.revision.catalog||row.revision.viewer!==page.revision.viewer))throw Object.assign(new Error('The row revision changed.'),{code:'stale_continuation'});
  if(row.live||page.live){
    if(!row.live||!page.live||row.live.revision!==page.live.revision||row.live.observedAt!==page.live.observedAt||row.live.group!==page.live.group)invalid();
    const key=(v:HomeChannel)=>v.channel.provenance+':'+v.channel.sourceId+':'+v.channel.id;
    const seen=new Set(row.live.channels.map(key));
    const channels=Object.freeze([...row.live.channels,...page.live.channels.filter(v=>!seen.has(key(v)))]);
    const expiresAt=Date.parse(page.live.expiresAt)<Date.parse(row.live.expiresAt)?page.live.expiresAt:row.live.expiresAt;
    return Object.freeze({...row,live:Object.freeze({...row.live,channels,expiresAt}),nextCursor:page.nextCursor,hasMore:page.hasMore,total:page.total});
  }
  const seen = new Set(row.entries.map(e => e.id));
  const entries = [...row.entries, ...page.entries.filter(e => !seen.has(e.id))];
  const resumes=page.resumes?Object.freeze([...(row.resumes??[]),...page.resumes.filter(v=>!row.resumes?.some(old=>old.entryId===v.entryId))].filter(v=>entries.some(e=>e.id===v.entryId))):row.resumes;
  const targets=[...(row.displayTargets??[])];
  for(const target of page.displayTargets??[]){
    const previous=targets.find(v=>v.membershipId===target.membershipId||v.entryId===target.entryId);
    if(previous&&(previous.membershipId!==target.membershipId||previous.entryId!==target.entryId))throw Object.assign(new Error('The row display target changed.'),{code:'stale_continuation'});
    if(!previous)targets.push(target);
  }
  return Object.freeze({...row, entries, ...(resumes?{resumes}:{}), ...(targets.length?{displayTargets:Object.freeze(targets)}:{}), nextCursor: page.nextCursor, hasMore: page.hasMore, total: page.total});
}

export async function fetchHomeRow(api: HomeApi, rowId: string, page: HomeRowPage = {}, signal?: AbortSignal): Promise<HomeRow> {
  if (page.cursor && (page.start !== undefined || page.anchorId)) throw new Error('A home row pages by cursor or by anchored range, not both.');
  const row = validateHomeRow(await api.request<RouteResponse<"get_home_row">>(encodedRoutePath("get_home_row", {"id": encodeURIComponent(rowId)}) + (pageQuery(page)), 'GET', undefined, signal));
  if (row.id !== rowId) invalid();
  return row;
}

export async function saveHomeLayout(api: HomeApi, intent: HomeLayoutIntent, signal?: AbortSignal): Promise<HomeLayout> {
  return validateHomeLayout(await api.request<RouteResponse<"put_home_layout">>(routePath("put_home_layout"), 'PUT', routeBody("put_home_layout", {
    expectedRevision: intent.expectedRevision, idempotencyKey: intent.idempotencyKey ?? '',
    rowOrder: [...intent.rowOrder], hiddenRowIds: [...intent.hiddenRowIds],
  }), signal));
}

export async function resetHomeLayout(api: HomeApi, idempotencyKey = '', signal?: AbortSignal, expectedRevision?: number): Promise<HomeLayout> {
  return validateHomeLayout(await api.request<RouteResponse<"post_home_layout_reset">>(routePath("post_home_layout_reset"), 'POST', routeBody("post_home_layout_reset", {idempotencyKey, ...(expectedRevision === undefined ? {} : {expectedRevision})}), signal));
}
