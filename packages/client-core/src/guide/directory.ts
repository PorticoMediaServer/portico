import {routePath,encodedRoutePath} from "../../../contracts/src/route-paths.ts";
import {GUIDE_PROTOCOL, parseGuideProgramme, parseGuideChannels, parseGuideSources, type ChannelApi, type GuideChannel, type GuideSource,type GuideProgramme} from '../channel-guide.ts';
import {unreadableServerResponse} from '../server-messages.ts';
import {keepReadable} from '../tolerant.ts';

export type GuideDirectory = Readonly<{viewerFence:string;revision:string;total:number;offset:number;limit:number;channels:readonly GuideChannel[];sources:readonly GuideSource[]}>;
export type GuideSourceSummary = GuideSource & Readonly<{position:number;recordAvailable:boolean;guideDays:number;channelCount:number;favoriteCount:number;groups:readonly string[];groupCounts:readonly Readonly<{name:string;count:number}>[];unavailableReasons:Readonly<Record<string,number>>}>;
export type GuideSourceSummaries = Readonly<{viewerFence:string;revision:string;sources:readonly GuideSourceSummary[]}>;
const object=(v:unknown):v is Record<string,unknown>=>v!==null&&typeof v==='object'&&!Array.isArray(v);
const text=(v:unknown,max=256):v is string=>typeof v==='string'&&v.length>0&&v.length<=max&&!/[\x00-\x1f\x7f]/.test(v);
const whole=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
function invalid():never{throw Object.assign(new Error(unreadableServerResponse),{code:'invalid_guide_directory',retryable:false});}
export function parseGuideDirectory(raw:unknown,serverId:string,offset:number|undefined,limit:number):GuideDirectory{
 if(!object(raw)||raw.protocolVersion!==GUIDE_PROTOCOL||raw.serverId!==serverId||!object(raw.directory))invalid();
 const d=raw.directory;
 if(!text(d.viewerFence)||!text(d.revision)||!whole(d.total)||!whole(d.offset)||(offset!==undefined&&d.offset!==offset)||d.limit!==limit||!Array.isArray(d.channels)||d.channels.length>limit||d.channels.length>Math.max(0,d.total-d.offset))invalid();
 if(offset===undefined&&((d.total>0&&d.offset>=d.total)||(d.total===0&&d.offset!==0)))invalid();
 const sources=parseGuideSources(d.sources),channels=parseGuideChannels(d.channels,sources);
 return {viewerFence:d.viewerFence,revision:d.revision,total:d.total,offset:d.offset,limit,channels,sources};
}
export function parseGuideSourceSummaries(raw:unknown,serverId:string):GuideSourceSummaries{
 if(!object(raw)||raw.protocolVersion!==GUIDE_PROTOCOL||raw.serverId!==serverId||!text(raw.viewerFence)||!text(raw.revision)||!Array.isArray(raw.sources)||raw.sources.length>65)invalid();
 // Each source is read with its own summary, so one that cannot be read is left out alone and never shifts another's counts.
 const seen=new Set<string>();
 const sources=keepReadable('guide','source-summaries',raw.sources as unknown[],(value):GuideSourceSummary=>{
  const source=parseGuideSources([value])[0];
  if(!source||!object(value)||seen.has(source.provenance+'\0'+source.id))invalid();
  seen.add(source.provenance+'\0'+source.id);
  if(!whole(value.position)||typeof value.recordAvailable!=='boolean'||!whole(value.guideDays)||value.guideDays>31||!whole(value.channelCount)||!whole(value.favoriteCount)||value.favoriteCount>value.channelCount||!Array.isArray(value.groups)||value.groups.length>2000||!value.groups.every(v=>text(v,120))||!Array.isArray(value.groupCounts)||value.groupCounts.length>2000||!object(value.unavailableReasons)||Object.keys(value.unavailableReasons).length>64)invalid();
  const channelCount=value.channelCount;
  const groupCounts=value.groupCounts.map(v=>{if(!object(v)||!text(v.name,120)||!whole(v.count)||v.count>channelCount)invalid();return{name:v.name,count:v.count};});
  const unavailableReasons:Record<string,number>={};let unavailable=0;
  for(const [reason,count] of Object.entries(value.unavailableReasons)){if(!text(reason,64)||!whole(count))invalid();unavailable+=count;unavailableReasons[reason]=count;}
  if(unavailable>value.channelCount)invalid();
  return {...source,position:value.position,recordAvailable:value.recordAvailable,guideDays:value.guideDays,channelCount:value.channelCount,favoriteCount:value.favoriteCount,groups:value.groups as string[],groupCounts,unavailableReasons};
 });
 return {viewerFence:raw.viewerFence,revision:raw.revision,sources};
}
export async function fetchGuideSourceSummaries(api:ChannelApi,serverId:string,timezone:string,signal?:AbortSignal,includeServerHidden=false):Promise<GuideSourceSummaries>{
 return parseGuideSourceSummaries(await api.request(routePath("get_guide_sources") + "?" + (new URLSearchParams({timezone,...(includeServerHidden?{includeServerHidden:"true"}:{})})),'GET',undefined,signal),serverId);
}

export type GuideAiring=Readonly<{channel:Pick<GuideChannel,'id'|'sourceId'|'provenance'|'generation'|'name'|'number'|'group'>;programme:GuideProgramme}>;
export type GuideAirings=Readonly<{groups:readonly Readonly<{key:string;title:string;airings:readonly GuideAiring[]}>[];nextCursor:string;revision:string;viewerFence:string;start:string;end:string;timezone:string;today:string}>;
export type GuideNowNextPage=GuideDirectory&Readonly<{observedAt:string;expiresAt:string;nextCursor:string}>;
export type GuideReminder=Readonly<{id:string;revision:number;airing:GuideAiring;due:boolean}>;
export type GuideReminders=Readonly<{items:readonly GuideReminder[];nextCursor:string;revision:string}>;
export type GuideAiringsQuery=Readonly<{start:string;end:string;search?:string;seriesId?:string;title?:string;channelId?:string;sourceId?:string;kind?:'live-source'|'library-channel'|'all';cursor?:string;limit?:number}>;
const token=(v:unknown,max=4096):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x1f\x7f]/.test(v);
const instant=(v:unknown):v is string=>typeof v==='string'&&Number.isFinite(Date.parse(v))&&/^\d{4}-\d\d-\d\dT/.test(v);
function airing(v:unknown):GuideAiring{
 if(!object(v)||!object(v.channel))invalid();const c=v.channel;
 if(!text(c.id)||!text(c.sourceId)||!text(c.generation)||!text(c.name)||!token(c.number,32)||!token(c.group,120)||(c.provenance!=='live-source'&&c.provenance!=='library-channel'))invalid();
 return {channel:{id:c.id,sourceId:c.sourceId,generation:c.generation,name:c.name,number:c.number,group:c.group,provenance:c.provenance},programme:parseGuideProgramme(v.programme,c.id)};
}
export function parseGuideAirings(v:unknown):GuideAirings{
 if(!object(v)||!Array.isArray(v.groups)||v.groups.length>50||!token(v.nextCursor)||!text(v.revision)||!text(v.viewerFence)||!instant(v.start)||!instant(v.end)||!text(v.timezone)||!text(v.today,10))invalid();
 let count=0;const seen=new Set<string>();
 const groups=v.groups.map(g=>{if(!object(g)||!text(g.key,1200)||!text(g.title,512)||!Array.isArray(g.airings)||g.airings.length>50)invalid();const airings=g.airings.map(raw=>{if(++count>50)invalid();const a=airing(raw),key=a.channel.provenance+':'+a.channel.sourceId+':'+a.programme.id;if(seen.has(key)||Date.parse(a.programme.start)>=Date.parse(v.end as string)||Date.parse(a.programme.end)<=Date.parse(v.start as string))invalid();seen.add(key);return a;});return {key:g.key,title:g.title,airings};});
 return {groups,nextCursor:v.nextCursor,revision:v.revision,viewerFence:v.viewerFence,start:v.start,end:v.end,timezone:v.timezone,today:v.today};
}
function reminder(v:unknown):GuideReminder{if(!object(v)||!text(v.id,64)||!whole(v.revision)||v.revision<1||typeof v.due!=='boolean')invalid();return {id:v.id,revision:v.revision,due:v.due,airing:airing(v.airing)};}
export function parseGuideReminders(v:unknown):GuideReminders{if(!object(v)||!Array.isArray(v.items)||v.items.length>50||!token(v.nextCursor)||!text(v.revision))invalid();return {items:v.items.map(reminder),nextCursor:v.nextCursor,revision:v.revision};}
/** One instance belongs to one authenticated viewer lifetime. Disposing or an
 * authority refusal aborts all its requests; it holds no cross-profile cache. */
export class GuideFeatureClient{
 private api:ChannelApi;private serverId:string;private closed=false;private requests=new Set<AbortController>();
 constructor(api:ChannelApi,serverId:string){this.api=api;this.serverId=serverId;}
 dispose(){this.closed=true;for(const request of this.requests)request.abort();this.requests.clear();}
 private async read(path:string,field:string,method='GET',body?:unknown):Promise<unknown>{
  if(this.closed)throw new Error(unreadableServerResponse);const controller=new AbortController();this.requests.add(controller);
  try{const raw=await this.api.request<unknown>(path,method,body,controller.signal);if(this.closed||controller.signal.aborted||!object(raw)||raw.protocolVersion!==GUIDE_PROTOCOL||raw.serverId!==this.serverId||!(field in raw))invalid();return raw[field];}
  catch(error){if(object(error)&&(error.status===401||error.status===403))this.dispose();throw error;}finally{this.requests.delete(controller);}
 }
 async airings(query:GuideAiringsQuery):Promise<GuideAirings>{const params=new URLSearchParams();for(const [key,value]of Object.entries(query))if(value!==undefined)params.set(key,String(value));return parseGuideAirings(await this.read(routePath('get_guide_airings')+'?'+params,'airings'));}
 async nowNext(onNow:boolean,query:{kind?:'all'|'live-source'|'library-channel';sourceId?:string;group?:string;cursor?:string;limit?:number}={}):Promise<GuideNowNextPage>{
  const limit=query.limit??37;const params=new URLSearchParams({limit:String(limit)});for(const[key,value]of Object.entries(query))if(value!==undefined)params.set(key,String(value));
  const p=await this.read(routePath(onNow?'get_guide_on_now':'get_guide_now_next')+'?'+params,'page');if(!object(p)||!instant(p.observedAt)||!instant(p.expiresAt)||!token(p.nextCursor)||!Array.isArray(p.channels)||p.channels.length>limit)invalid();
  // The directory parser deliberately accepts rows only. Decode the bounded
  // programmes separately, then attach them to those exact directory rows.
  const channels=p.channels.map(v=>{if(!object(v)||!Array.isArray(v.programmes)||v.programmes.length>(onNow?1:3))invalid();return v;});
  const d=parseGuideDirectory({protocolVersion:GUIDE_PROTOCOL,serverId:this.serverId,directory:{...p,channels:channels.map(c=>({...c,programmes:[]}))}},this.serverId,undefined,limit);
  const byKey=new Map(channels.map(c=>[String(c.provenance)+':'+c.sourceId+':'+c.id,c]));
  return {...d,channels:d.channels.map(c=>({...c,programmes:(byKey.get(c.provenance+':'+c.sourceId+':'+c.id)!.programmes as unknown[]).map(v=>parseGuideProgramme(v,c.id))})),observedAt:p.observedAt,expiresAt:p.expiresAt,nextCursor:p.nextCursor};
 }
 async reminders(cursor='',limit=30):Promise<GuideReminders>{return parseGuideReminders(await this.read(routePath('get_guide_reminders')+'?'+new URLSearchParams({cursor,limit:String(limit)}),'result'));}
 async remind(value:GuideAiring,expectedRevision=0):Promise<GuideReminder>{const c=value.channel;return reminder(await this.read(routePath('put_guide_reminders'),'result','PUT',{sourceId:c.sourceId,channelId:c.id,generation:c.generation,programmeId:value.programme.id,kind:c.provenance,expectedRevision}));}
 async dismiss(value:GuideReminder):Promise<void>{const raw=await this.read(encodedRoutePath('delete_guide_reminders_id',{id:value.id}),'result','DELETE',{expectedRevision:value.revision});if(!object(raw)||raw.deleted!==true)invalid();}
}
