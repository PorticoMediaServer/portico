import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackService} from '../src/index.ts';
import {SubtitleService,validateSubtitlePlan} from '../src/subtitles.ts';
import type {PlaybackApi,PlaybackSession} from '../src/index.ts';
import type {LibraryContentApi} from '../src/library-content.ts';
const session:PlaybackSession={id:'session',generation:1,streamUrl:'/v1/media/original',mode:'direct',duration:100,resumeSeconds:2};
const flush=()=>new Promise<void>(r=>setImmediate(r));
const resource={id:'sub',sourceId:'source',revision:1,scope:'personal',language:'en',title:'ASS',format:'ass',origin:'upload',rights:'Own',offsetUs:'0',canManage:true,enabled:true,renderer:'burn_in',default:false,forced:true} as const;
function plan(revision=1,burned=false):any{return {version:1,sessionId:'session',generation:burned?2:1,sourceId:'source',revision,catalogRevision:1,renderer:burned?'burn_in':'external_text',mode:burned?'track':'off',offAvailable:true,offsetUs:'0',selected:burned?{...resource,pinned:true}:null,resources:[resource],discovered:[],...(burned?{presentation:{sessionId:'session',generation:2,mode:'hls',streamUrl:'/v1/media/new/master.m3u8',positionUs:'2000000'}}:{})};}
const catalog={version:1,itemId:'movie',revision:1,canShare:false,sources:[{id:'source',durationUs:'100000000',inventoryRevision:1,available:true,timingKnown:true}],resources:[resource],discovered:[],provider:{id:'opensubtitles',enabled:false}};
test('subtitle rendition handoff preserves occurrence, pause, pending seek and exact session identity',async()=>{
 let creates=0,stops=0;const api:PlaybackApi={createPlayback:async()=>{creates++;return session;},stopPlayback:async()=>{stops++;},progressPlayback:async()=>{}};
 const player=new PlaybackService(api,()=> 'request');await player.play('movie');const intent=player.getSnapshot().intentId;player.ready(intent);player.pause();player.seek(37);
 const p=plan(2,true).presentation;assert.equal(player.installSubtitlePresentation(p),true);const now=player.getSnapshot();
 assert.equal(creates,1);assert.equal(stops,0);assert.equal(now.intentId,intent);assert.equal(now.session?.id,'session');assert.equal(now.session?.generation,2);assert.equal(now.pendingSeek?.positionSeconds,37);assert.equal(now.intent,'paused');
 assert.equal(player.installSubtitlePresentation(p),true);assert.equal(player.installSubtitlePresentation({...p,generation:1}),false);assert.equal(player.installSubtitlePresentation({...p,sessionId:'other',generation:3}),false);
 player.leave();await flush();
});
test('burn-in wire plans never expose private assets as text documents or origin URLs',()=>{
 const valid=plan(2,true);assert.equal(validateSubtitlePlan(valid).renderer,'burn_in');
 for(const change of [(p:any)=>p.documentUrl='/v1/media/subtitles/grant/sub/1',(p:any)=>p.presentation.streamUrl='https://origin.invalid/private',(p:any)=>p.presentation.generation=3,(p:any)=>p.selected.renderer='external_text']){const p=structuredClone(valid);change(p);assert.throws(()=>validateSubtitlePlan(p));}
});
test('a plan read that names a newer generation installs that presentation once, and only for the same session',async()=>{
 const installs:any[]=[];let current=plan();
 const api:LibraryContentApi={request:async(path:string)=> (path.includes('/playback/')?current:catalog) as any};
 const service=new SubtitleService(api,{itemId:'movie',sessionId:'session',generation:1},()=> 'fixed',{install:p=>{installs.push(p);return true;}});
 service.start();await flush();assert.equal(installs.length,0);current=plan(2,true);await service.refresh();assert.equal(service.getSnapshot().plan?.generation,2);assert.equal(installs.length,1);assert.equal(service.target.generation,2);
 await service.refresh();assert.equal(installs.length,1);service.stop();
});
test('the subtitle service reads and manages but never selects: selection is a change of the session',()=>{
 const service=new SubtitleService({request:async()=>catalog as any},{itemId:'movie',sessionId:'session',generation:1},()=> 'op');
 assert.equal('choose' in service,false);assert.equal('import' in service,false);
});
test('reading an unchanged subtitle generation never replays a historical presentation',async()=>{
 let installs=0;
 const api:LibraryContentApi={request:async(path:string)=> (path.includes('/playback/')?plan(2,true):catalog) as any};
 const service=new SubtitleService(api,{itemId:'movie',sessionId:'session',generation:2},()=> 'same',{install:()=>{installs++;return true;}});
 service.start();await flush();assert.equal(service.getSnapshot().plan?.generation,2);assert.equal(installs,0);service.stop();
});
