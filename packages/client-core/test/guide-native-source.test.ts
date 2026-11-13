import test from 'node:test';
import assert from 'node:assert/strict';
import {nativeGuideSource} from '../src/guide/native-source.ts';
import {legacyChannel, legacyProgramme} from '../src/guide/legacy-source.ts';
import {GuideFeatureClient} from '../src/guide/directory.ts';

const options = {kind: 'all' as const, sourceId: '', timezone: 'UTC'};
const channel = (n: number) => ({id: 'ch-' + n, name: 'Channel ' + n, number: String(n), kind: 'source', favorite: false, hidden: false});
const programme = (n: number) => ({id: 'p-' + n, title: 'A programme', startsAt: '2026-11-13T00:00:00Z', endsAt: '2026-11-13T01:00:00Z', summary: 'Actual description', episode: 2});

test('native channel rows keep their playback identity and the exact count before all pages are read', async () => {
 const paths:string[]=[];
 const api={request:async<T>(path:string):Promise<T>=>{
  paths.push(path);return {items:Array.from({length:50},(_,n)=>channel(n)),nextCursor:'later',
   sources:[{id:'aerial',name:'Aerial',kind:'live',channels:100,favorites:7,groups:[]}]} as T;
 }};
 const source=nativeGuideSource(api,{...options,includeHidden:true});
 const signal=new AbortController().signal;
 Object.defineProperty(signal,'throwIfAborted',{value:undefined}); // Native AbortSignal has no such method.
 const page=await source.channels(0,50,signal);
 assert.equal(page.total,100);assert.equal(page.items.length,50);assert.equal(paths.length,1);
 assert.equal(new URL(paths[0],'https://test').searchParams.get('hidden'),'true');
 assert.equal(legacyChannel(page.items[0])?.sourceId,'server');assert.equal(legacyChannel(page.items[0])?.generation,'1');
});

test('programmes on distant visible channels are read around those rows, never by scanning earlier guide pages', async () => {
 const paths:string[]=[];
 const api={request:async<T>(path:string):Promise<T>=>{
  paths.push(path);const query=new URL(path,'https://test').searchParams;
  const at=Number(query.get('around')!.slice(3));
  return {items:Array.from({length:11},(_,n)=>({channel:channel(at-5+n),programmes:[programme(at-5+n)]}))} as T;
 }};
 const ids=Array.from({length:20},(_,n)=>'ch-'+(10000+n)), signal=new AbortController().signal;
 Object.defineProperty(signal,'throwIfAborted',{value:undefined});
 const answer=await nativeGuideSource(api,options).programs(ids,Date.parse(programme(0).startsAt),Date.parse(programme(0).endsAt),signal);
 assert.equal(paths.length,2);assert.ok(paths.every(path=>path.startsWith('/v1/live/guide?')&&!path.includes('cursor=')));
 for(const id of ids){assert.equal(answer[id].length,1);const raw=legacyProgramme(answer[id][0]);assert.equal(raw?.channelId,id);assert.equal(raw?.description,'Actual description');assert.equal(raw?.newEvidence,'unknown');}
});

test('programme details and reminder reads keep real ids and only the supplied airings', async () => {
 const paths:string[]=[], current=programme(3);
 const api={request:async<T>(path:string):Promise<T>=>{
  paths.push(path);
  if(path==='/v1/live/reminders')return {items:[{programme:current.id,channel:'Channel 3',channelId:'ch-3',title:current.title,startsAt:current.startsAt,endsAt:current.endsAt},
   {programme:'not-on-screen',channel:'Other',channelId:'other',title:'Other',startsAt:current.startsAt,endsAt:current.endsAt}]} as T;
  assert.equal(path,'/v1/live/programmes/p-3');
  return {...current,channel:channel(3),canRecord:false,reminder:true,airings:[{...programme(4),channel:'Channel 4',channelId:'ch-4'}]} as T;
 }};
 const features=new GuideFeatureClient(api,'server');
 try{
  const detail=await features.programme(current.id);
  assert.equal(detail.channel.recordAvailable,false);assert.equal(detail.channel.sourceId,'server');assert.equal(detail.airings[0].programme.id,'p-4');
  const reminders=await features.remindersFor([{channel:detail.channel,programme:detail.programme},...detail.airings]);
  assert.deepEqual(reminders.items.map(item=>item.id),['p-3']);assert.equal(reminders.items[0].revision,0,'the server has no reminder revision');
  assert.deepEqual(paths,['/v1/live/programmes/p-3','/v1/live/reminders']);
 }finally{features.dispose();}
});

test('disposing the viewer reader rejects a late programme response', async () => {
 let finish!:(value:unknown)=>void,signal:AbortSignal|undefined;
 const api={request:<T>(_path:string,_method?:string,_body?:unknown,owned?:AbortSignal):Promise<T>=>{signal=owned;return new Promise(resolve=>{finish=value=>resolve(value as T);});}};
 const features=new GuideFeatureClient(api,'server'), pending=features.programme('p-1');
 features.dispose();assert.equal(signal?.aborted,true);
 finish({...programme(1),channel:channel(1),canRecord:true,reminder:false,airings:[]});
 await assert.rejects(pending,error=>(error as Error).name==='AbortError');
});


test('native reminder commands use the programme id, own cancellation and show the two-minute due window', async () => {
 const paths:unknown[]=[];
 const startsAt=new Date(Date.now()+60_000).toISOString(), endsAt=new Date(Date.now()+3600_000).toISOString();
 const api={request:async<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>=>{
  assert.ok(signal);assert.equal(signal.aborted,false);paths.push([path,method,body]);return undefined as T;
 }};
 const features=new GuideFeatureClient(api,'server');
 try{
  const airing={channel:{id:'ch-3',sourceId:'server',provenance:'live-source' as const,generation:'1',name:'Three',number:'3',group:''},
   programme:{id:'p-3',channelId:'ch-3',title:'Soon',start:startsAt,end:endsAt,lineage:'interval-only' as const,seriesId:'',episodeId:'',newEvidence:'unknown' as const,description:''}};
  const reminder=await features.remindProgramme(airing);
  assert.equal(reminder.id,'p-3');assert.equal(reminder.revision,0);assert.equal(reminder.due,true);
  await features.dismissProgramme(reminder);
  assert.deepEqual(paths,[['/v1/live/reminders/p-3','PUT',undefined],['/v1/live/reminders/p-3','DELETE',undefined]]);
 }finally{features.dispose();}
});
