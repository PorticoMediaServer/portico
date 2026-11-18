import {nativeGuideProgramme, readNativeProgramme} from './native-source.ts';
import {readGuide, readReminders, setReminder} from '../live-tv.ts';
import {serverNow} from '../server-clock.ts';
import {readNextGuideSummaries} from './next-source.ts';
import type {ChannelApi, GuideChannel, GuideSource, GuideProgramme} from '../channel-guide.ts';
import {unreadableServerResponse} from '../server-messages.ts';

export type GuideSourceSummary = GuideSource & Readonly<{position:number;recordAvailable:boolean;guideDays:number;channelCount:number;favoriteCount:number;groups:readonly string[];groupCounts:readonly Readonly<{name:string;count:number}>[];unavailableReasons:Readonly<Record<string,number>>}>;
export type GuideSourceSummaries = Readonly<{viewerFence:string;revision:string;sources:readonly GuideSourceSummary[]}>;
const object=(v:unknown):v is Record<string,unknown>=>v!==null&&typeof v==='object'&&!Array.isArray(v);
function invalid():never{throw Object.assign(new Error(unreadableServerResponse),{code:'invalid_guide_directory',retryable:false});}
export async function fetchGuideSourceSummaries(api:ChannelApi,_serverId:string,_timezone:string,signal?:AbortSignal,_includeServerHidden=false):Promise<GuideSourceSummaries>{
 // Source availability includes this profile's hidden channels: hiding every channel must not remove Live TV from navigation.
 // The viewer read never includes channels hidden by the owner.
 return readNextGuideSummaries(api,true,signal);
}

export type GuideAiring=Readonly<{channel:Pick<GuideChannel,'id'|'sourceId'|'provenance'|'generation'|'name'|'number'|'group'>;programme:GuideProgramme}>;
export type GuideReminder=Readonly<{id:string;revision:number;airing:GuideAiring;due:boolean}>;
export type GuideReminders=Readonly<{items:readonly GuideReminder[];nextCursor:string;revision:string}>;
/** One instance belongs to one authenticated viewer lifetime. Disposing or an
 * authority refusal aborts all its requests; it holds no cross-profile cache. */
export class GuideFeatureClient{
 private api:ChannelApi;private closed=false;private requests=new Set<AbortController>();
 constructor(api:ChannelApi,_serverId?:string){this.api=api;}
 dispose(){this.closed=true;for(const request of this.requests)request.abort();this.requests.clear();}
 private async currentRead<T>(read:(signal:AbortSignal)=>Promise<T>):Promise<T>{
  if(this.closed)throw new Error(unreadableServerResponse);const controller=new AbortController();this.requests.add(controller);
  try{const raw=await read(controller.signal);if(this.closed||controller.signal.aborted)invalid();return raw;}
  catch(error){if(object(error)&&(error.status===401||error.status===403))this.dispose();throw error;}finally{this.requests.delete(controller);}
 }
 /** The programme sheet's reads on the rebuilt server. */
 programme(id:string){return this.currentRead(signal=>readNativeProgramme(this.api,id,signal));}
 /** The channel's next programmes are different from repeat airings of this programme. */
 later(channel:GuideChannel,programme:GuideProgramme):Promise<readonly GuideAiring[]>{
  return this.currentRead(async signal=>{
   const from=new Date(programme.end),to=new Date(from.getTime()+12*60*60*1000);
   const page=await readGuide(this.api,{from,to},{around:channel.id,...(channel.hidden?{hidden:true}:{})},undefined,signal);
   const row=page.items.find(row=>row.channel.id===channel.id);
   if(!row)invalid();
   return row.programmes.filter(next=>Date.parse(next.startsAt)>=from.getTime()).slice(0,4)
    .map(next=>({channel,programme:nativeGuideProgramme(channel.id,next)}));
  });
 }

 async remindersFor(airings:readonly GuideAiring[]):Promise<GuideReminders>{
  const page=await this.currentRead(signal=>readReminders(this.api,undefined,signal));
  const byId=new Map(airings.map(airing=>[airing.programme.id,airing]));
  return {revision:'',nextCursor:page.nextCursor??'',items:page.items.flatMap(item=>{
   const airing=byId.get(item.programme);return airing?[{id:item.programme,revision:0,airing,due:serverNow()>=Date.parse(item.startsAt)-120_000}]:[];
  })};
 }
 /** A reminder belongs to its real programme id; the rebuilt PUT/DELETE are idempotent. */
 async remindProgramme(airing:GuideAiring):Promise<GuideReminder>{
  await this.currentRead(signal=>setReminder(this.api,airing.programme.id,true,signal));
  return {id:airing.programme.id,revision:0,airing,due:serverNow()>=Date.parse(airing.programme.start)-120_000};
 }
 async dismissProgramme(value:GuideReminder):Promise<void>{
  await this.currentRead(signal=>setReminder(this.api,value.airing.programme.id,false,signal));
 }
}
