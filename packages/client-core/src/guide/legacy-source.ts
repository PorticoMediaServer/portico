import {routePath} from "../../../contracts/src/route-paths.ts";
/**
 * Shared guide adapter: current servers page globally ordered channel rows and
 * summarize sources without loading programmes; visible programme blocks name
 * their channels explicitly.
 *
 * Rows and programmes keep validated native handles for tune/record/preferences.
 */
import {parseChannelGuide, type ChannelApi, type ChannelGuide, type GuideChannel, type GuideProgramme, type GuideRoute} from '../channel-guide.ts';
import {fetchGuideSourceSummaries, parseGuideDirectory} from './directory.ts';
import type {ChannelSource, GuideChannelRow, GuideDataSource, GuideProgram} from './types.ts';

export type LegacyGuideOptions = Readonly<{
  /** `all`: live sources, then Library Channels (All channels, §2.0). */
  kind: 'live-source' | 'library-channel' | 'all';
  /** '' for every source of the kind. */
  sourceId: string;
  timezone: string;
  search?: string;
  group?: string;
  favorites?: boolean;
  /** Owners: include hidden channels (to unhide them). */
  includeHidden?: boolean;
  includeServerHidden?: boolean;
  /**
   * `server` keeps the server's order. `number`: sources in the owner's order, each by channel
   * number; `name` interleaves sources by name (§2.2).
   */
  sort?: 'server' | 'number' | 'name' | 'favorites';
  now?: () => number;
}>;

const iso = (ms: number) => new Date(ms).toISOString().replace('.000Z', 'Z');

type GuideSelection = Readonly<{channels?: readonly string[]; rowsOnly?: boolean}>;

function query(route: GuideRoute, cursor: string, select: GuideSelection = {}): string {
  const q = new URLSearchParams({kind: route.kind, start: route.start, end: route.end, timezone: route.timezone, search: route.search, sourceId: route.sourceId, limit: select.channels ? '50' : '30', cursor});
  if (select.channels) q.set('channels', select.channels.join(','));
  if (select.rowsOnly) q.set('programmes', 'none');
  if (route.favorites !== undefined) q.set('favorites', String(route.favorites));
  if (route.includeServerHidden) q.set('includeServerHidden', 'true');
  if (route.includeHidden) q.set('includeHidden', 'true');
  if (route.group) q.set('group', route.group);
  return routePath("get_guide") + "?" + (q.toString());
}

/** Every page of one window, in order. */
async function walk(api: ChannelApi, serverId: string, route: GuideRoute, signal?: AbortSignal, select: GuideSelection = {}): Promise<ChannelGuide[]> {
  const pages: ChannelGuide[] = [];
  let cursor = '';
  for (let i = 0; i < 200; i++) {
    const page = parseChannelGuide(await api.request<unknown>(query(route, cursor, select), 'GET', undefined, signal), serverId, route);
    pages.push(page);
    if (!page.nextCursor) return pages;
    if (page.nextCursor===cursor) throw new Error('The guide repeated a continuation.');
    cursor = page.nextCursor;
  }
  throw Object.assign(new Error('The guide listing requires server windowing.'),{code:'guide_windowing_required',retryable:false});
}

// The handles this adapter attached, so the accessors never trust an object they didn't make.
const nativeChannels = new WeakSet<object>();
const nativeProgrammes = new WeakSet<object>();

/** The `/v1/guide` channel behind a row this adapter made (for tuning, recording and favorites). */
export function legacyChannel(row: GuideChannelRow): GuideChannel | undefined {
  const n = row.native;
  return n !== null && typeof n === 'object' && nativeChannels.has(n) ? n as GuideChannel : undefined;
}

/** The `/v1/guide` programme behind a program this adapter made (for recording). */
export function legacyProgramme(p: GuideProgram): GuideProgramme | undefined {
  const n = p.native;
  return n !== null && typeof n === 'object' && nativeProgrammes.has(n) ? n as GuideProgramme : undefined;
}

export function legacyChannelRow(c: GuideChannel): GuideChannelRow {
  nativeChannels.add(c);
  return {
    id: c.id, sourceId: c.provenance === 'library-channel' ? 'library' : c.sourceId, kind: c.provenance === 'library-channel' ? 'library' : 'live',
    number: c.number, name: c.name, group: c.group, ...(c.logoPath ? {logoUrl: c.logoPath} : {}), favorite: c.favorite,
    ...(c.mark ? {mark: c.mark} : {}),
    tuneAvailable: c.tuneAvailable, ...(c.tuneUnavailableReason ? {tuneUnavailableReason: c.tuneUnavailableReason} : {}),
    recordAvailable: c.recordAvailable, guide: 'full', native: c,
  };
}

export function legacyProgram(p: GuideProgramme): GuideProgram {
  nativeProgrammes.add(p);
  return {
    id: p.id, channelId: p.channelId, title: p.title, start: Date.parse(p.start), end: Date.parse(p.end),
    ...(p.seriesId ? {seriesId: p.seriesId} : {}),
    ...(p.subtitle ? {subtitle: p.subtitle} : {}),
    ...(p.episode ? {episode: p.episode} : {}),
    ...(p.categories !== undefined ? {categories: p.categories} : {}),
    ...(p.rating ? {rating: p.rating.value} : {}),
    ...(p.year !== undefined ? {year: p.year} : {}),
    ...(p.starRating ? {starRating: p.starRating} : {}),
    ...(p.image ? {image: p.image} : {}),
    flags: {live: p.flags?.live === true, new: p.flags?.new === true || p.newEvidence === 'new', premiere: p.flags?.premiere === true, repeat: p.flags?.repeat === true || p.newEvidence === 'repeat'},
    ...(p.recordingId ? {recording: {id: p.recordingId, state: p.recordingState ?? ''}} : {}),
    ...(p.description ? {description: p.description} : {}),
    native: p,
  };
}

/** The directory sends only the channel rows on screen; programmes use named
 * channels and time windows. */
export function legacyGuideSource(api:ChannelApi,serverId:string,o:LegacyGuideOptions):GuideDataSource {
 let revision:string|undefined,viewerFence:string|undefined;
 const rows=new Map<string,GuideChannel>();
 const route=(kind:'live-source'|'library-channel',start:number,end:number):GuideRoute=>({kind,start:iso(start),end:iso(end),timezone:o.timezone,search:o.search??'',sourceId:o.kind==='all'?'':o.sourceId,...(o.group?{group:o.group}:{}),...(o.favorites!==undefined?{favorites:o.favorites}:{}),...(o.includeServerHidden?{includeServerHidden:true}:{}),...(o.includeHidden?{includeHidden:true}:{})});
 return {
  reset(){revision=undefined;viewerFence=undefined;rows.clear();},
 async neighbor(channel,delta,signal){
  const native=legacyChannel(channel);if(!native)return undefined;
  for(let attempt=0;attempt<2;attempt++){
   const q=new URLSearchParams({kind:o.kind,sourceId:o.sourceId,timezone:o.timezone,sort:o.sort??'server',limit:'1',anchorChannelId:native.id,anchorSourceId:native.sourceId,anchorProvenance:native.provenance,direction:delta>0?'next':'previous'});
   if(o.search)q.set('search',o.search);
   if(o.group)q.set('group',o.group);if(o.favorites!==undefined)q.set('favorites',String(o.favorites));if(o.includeServerHidden)q.set('includeServerHidden','true');if(o.includeHidden)q.set('includeHidden','true');if(revision)q.set('revision',revision);
   try{
    const page=parseGuideDirectory(await api.request(routePath("get_guide_channels") + "?" + (q),'GET',undefined,signal),serverId,undefined,1);
    if(signal.aborted)throw signal.reason??new Error('aborted');
    if(revision&&revision!==page.revision||viewerFence&&viewerFence!==page.viewerFence)throw Object.assign(new Error('The guide changed. Refresh this view.'),{code:'guide_refresh_required',retryable:true});
    revision=page.revision;viewerFence=page.viewerFence;
    const next=page.channels[0];
    if(!next||!next.tuneAvailable||next.id===native.id&&next.provenance===native.provenance&&next.sourceId===native.sourceId)return undefined;
    return legacyChannelRow(next);
   }catch(error){
    if(attempt===0&&(error as {code?:string})?.code==='guide_refresh_required'){revision=undefined;viewerFence=undefined;rows.clear();continue;}
    throw error;
   }
  }
  return undefined;
 },
  async channels(from,limit,signal){
   if(!Number.isSafeInteger(from)||from<0||!Number.isInteger(limit)||limit<1||limit>50)throw new Error('Use a bounded channel directory page.');
   const q=new URLSearchParams({kind:o.kind,sourceId:o.sourceId,timezone:o.timezone,sort:o.sort??'server',offset:String(from),limit:String(limit)});
   if(o.search)q.set('search',o.search);
   if(o.group)q.set('group',o.group);if(o.favorites!==undefined)q.set('favorites',String(o.favorites));if(o.includeServerHidden)q.set('includeServerHidden','true');if(o.includeHidden)q.set('includeHidden','true');if(revision)q.set('revision',revision);
   const page=parseGuideDirectory(await api.request(routePath("get_guide_channels") + "?" + (q),'GET',undefined,signal),serverId,from,limit);
    if(signal.aborted)throw signal.reason??new Error('aborted');
    if(revision&&revision!==page.revision||viewerFence&&viewerFence!==page.viewerFence)throw Object.assign(new Error('The guide changed. Refresh this view.'),{code:'guide_refresh_required',retryable:true});
    revision=page.revision;viewerFence=page.viewerFence;
    for(const channel of page.channels){rows.delete(channel.id);rows.set(channel.id,channel);}
    while(rows.size>300)rows.delete(rows.keys().next().value!);
    return {items:page.channels.map(legacyChannelRow),total:page.total,revision:page.revision};
  },
  async programs(channelIds,start,end,signal){
   const out:Record<string,GuideProgram[]>=Object.fromEntries(channelIds.map(id=>[id,[]]));
   const kinds=o.kind==='all'?['live-source','library-channel'] as const:[o.kind];
   const reads:Promise<ChannelGuide[]>[]=[];
   for(const kind of kinds){const ids=channelIds.filter(id=>!rows.has(id)||rows.get(id)!.provenance===kind);for(let i=0;i<ids.length;i+=50)reads.push(walk(api,serverId,route(kind,start,end),signal,{channels:ids.slice(i,i+50)}));}
   for(const pages of await Promise.all(reads))for(const page of pages)for(const channel of page.channels)if(channel.id in out)out[channel.id]=channel.programmes.map(legacyProgram);
   if(signal.aborted)throw signal.reason??new Error('aborted');
   return out;
  },
 };
}

export type GuideCatalog=Readonly<{groups:readonly string[];favorites:boolean;unavailableReasons:readonly string[]}>;
/** Filter chips and the server-wide availability explanation use summaries,
 * never a thousand-row fetch. Group counts are combined across selected sources. */
export async function legacyGuideCatalog(api:ChannelApi,serverId:string,o:LegacyGuideOptions,signal?:AbortSignal):Promise<GuideCatalog>{
  const summary=await fetchGuideSourceSummaries(api,serverId,o.timezone,signal);
  const sources=summary.sources.filter(s=>o.kind==='all'||(o.kind==='library-channel')===(s.provenance==='library-channel')).filter(s=>!o.sourceId||o.sourceId==='library'&&s.provenance==='library-channel'||s.id===o.sourceId);
  const groups=new Map<string,number>();let total=0,unavailable=0;
  const reasons=new Set<string>();
  for(const source of sources){total+=source.channelCount;for(const group of source.groupCounts)groups.set(group.name,(groups.get(group.name)??0)+group.count);for(const [reason,count] of Object.entries(source.unavailableReasons)){unavailable+=count;if(count)reasons.add(reason);}}
  return {groups:[...groups].sort((a,b)=>b[1]-a[1]).slice(0,6).map(([name])=>name),favorites:sources.some(s=>s.favoriteCount>0),unavailableReasons:total>0&&unavailable===total?[...reasons]:[]};
}

/** The refresh states in which a source's guide is out of date; any other state is fine. */
const staleRefreshStates: ReadonlySet<string> = new Set(['degraded', 'credentials-required']);

/** A live source's guide state from its `/v1/guide` refresh state. */
export function legacyGuideState(refreshState: string): 'ready' | 'refreshing' | 'stale' {
  return refreshState === 'refreshing' ? 'refreshing' : staleRefreshStates.has(refreshState) ? 'stale' : 'ready';
}

/**
 * The Channels entries from today's API: each live source by name, plus "Library Channels" when
 * the library-channel guide has any channel. `libraryName` is the catalogue's name for it.
 */
export async function legacyChannelSources(api: ChannelApi, serverId: string, timezone: string, libraryName: string, now = Date.now(), signal?: AbortSignal): Promise<ChannelSource[]> {
    const summaries=await fetchGuideSourceSummaries(api,serverId,timezone,signal);
    return summaries.sources.filter(s=>s.channelCount>0).map(s=>({id:s.provenance==='library-channel'?'library':s.id,name:s.provenance==='library-channel'?libraryName:s.name,type:s.provenance==='library-channel'?'library':'live',position:s.position,recordAvailable:s.recordAvailable,guideState:legacyGuideState(s.refreshState),...(s.availableStart?{availableStart:Date.parse(s.availableStart)}:{}),...(s.availableEnd?{availableEnd:Date.parse(s.availableEnd)}:{}),guideDays:s.guideDays}));
}
