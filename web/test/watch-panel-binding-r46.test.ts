import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '@i18n';
import {parseOptions,parseSession} from '@core/playback-v1/types.ts';
import type {PlayerOffersData} from '@core/player-offers.ts';
import type {SubtitlePlan,SubtitleResource} from '@core/subtitles.ts';
import type {WebPlaybackRuntimeSnapshot,WebShellPlaybackRuntime} from '../src/platform/playback';
import {phoneOptionRows,watchPanelModel} from '../src/screens/watch/panel-model.ts';
import {settledPlaybackChanged} from '../src/screens/watch/screen-state.ts';
import {roomActionAllowed} from '../src/screens/watch/room-guard.ts';
const scope={serverId:'server',authority:'local' as const,accountId:'account',profileId:'profile',controllerId:'controller',controllerEpoch:'epoch',commandLaneId:'lane'};
const fence={kind:'queue' as const,playback:{scope,run:1},sessionId:'session',itemId:'movie',sourceId:'source',entryId:'entry',generation:1};
function fixture(){
 const options=parseOptions({itemId:'movie',kind:'movie',versions:[
  {id:'v1',label:'First',durationMs:120000,parts:[],video:[],audio:[{id:'a1',codec:'aac',title:'Wrong version'}],subtitles:[]},
  {id:'v2',label:'Chosen',durationMs:120000,parts:[],video:[{id:'v0',codec:'h264',height:1080,interlaced:false}],audio:[
   {id:'a3',codec:'ac3',language:'en',channels:6,layout:'5.1',default:false},
   {id:'a4',codec:'aac',language:'en',title:'Commentary',channels:2,layout:'stereo',default:true}],subtitles:[]}],qualities:[{mode:'original'},{mode:'automatic'},{mode:'fixed',id:'720p-low'}],chapters:[],markers:[]});
 const session=parseSession({id:'session',revision:'1',kind:'vod',role:'local',state:'playing',itemId:'movie',versionId:'v2',queue:{queueId:'queue',entryId:'entry'},
  presentation:{generation:1,mode:'direct',url:'/v1/media/current',startPositionMs:0,subtitles:[],decision:{},audioTrackId:'a3',quality:{mode:'automatic',id:'720p-low'}}});
 const resource:SubtitleResource={id:'real-subtitle',sourceId:'source',revision:1,scope:'personal',language:'en',title:'',format:'srt',origin:'sidecar',rights:'owned',offsetUs:'-500000',canManage:true,enabled:true,renderer:'external_text',default:false,forced:false};
 const plan:SubtitlePlan={version:1,sessionId:'session',generation:1,sourceId:'source',revision:1,catalogRevision:1,renderer:'external_text',mode:'track',offAvailable:true,offsetUs:'-500000',selected:resource,resources:[resource,{...resource,id:'fractional',offsetUs:'500'}],discovered:[]};
 const offers:PlayerOffersData={scope:{serverId:'server',libraryId:'library',itemId:'movie',viewerFence:'detail-viewer'},revision:'offers-1',preparedVersions:[],preparedOffersRevision:'',subtitlePlan:plan,subtitlePlanUnavailableReason:null,
  current:{sessionId:'session',generation:1,state:'playing',sourceId:'source',delivery:'direct',audioSelection:'platform_default',subtitleSelection:'track',qualityId:'720p-low'},
  sources:[{id:'source',available:true,container:'mkv',videoCodec:'h264',audioCodec:'ac3',width:1920,height:1080,duration:120,factsRevision:1,factsStatus:'known',relation:'unknown',boundary:'whole_source',enabled:false,reason:'source_selection_unavailable',
   streams:[{index:3,type:'audio',codec:'ac3',default:false,forced:false,enabled:true,reason:''},{index:4,type:'audio',codec:'aac',default:true,forced:false,enabled:false,reason:'track_selection_unavailable'}],
   qualities:[{id:'original',kind:'original',label:'Original',enabled:true},{id:'auto',kind:'automatic',label:'Automatic',enabled:true},{id:'720p-low',kind:'fixed',label:'720p',maxVideoBitrateBps:2000000,enabled:true},{id:'1080p-high',kind:'fixed',label:'1080p',enabled:false}]}],
  controls:[{id:'audio',enabled:true,reason:''},{id:'subtitles',enabled:true,reason:''},{id:'quality',enabled:true,reason:''}],markers:null,deliveryPolicy:null,offersRevision:'a'.repeat(64),transcodingEnabled:true};
 let live:WebPlaybackRuntimeSnapshot={authority:{...scope,epoch:1,viewerFence:'home-viewer'},sequence:1,disposed:false,fence,nativeVideoSurfaceActive:false,playback:{} as never,dock:{} as never,presentations:[],queueView:null,session,options,offers,presentation:{rate:1.5} as never};
 const calls:unknown[][]=[];
 const runtime={getSnapshot:()=>live,speedChoices:()=>[.5,1,1.5],selectAudio:async(...args:unknown[])=>{calls.push(['audio',...args]);},selectSubtitle:async(...args:unknown[])=>{calls.push(['subtitle',...args]);},setQuality:async(...args:unknown[])=>{calls.push(['quality',...args]);},setSpeed:(...args:unknown[])=>{calls.push(['speed',...args]);}} as unknown as WebShellPlaybackRuntime;
 return {runtime,calls,get live(){return live;},set:(next:WebPlaybackRuntimeSnapshot)=>{live=next;},model:()=>watchPanelModel(runtime,live,defaultI18n)};
}

test('the parent uses the actual selected grouped version and selected stream, never its first/default track',()=>{
 const f=fixture(),model=f.model();assert.ok(model,'detail and Home viewer fences are independent opaque scopes');
 assert.deepEqual(model.audio.map(r=>r.id),['a3','a4']);assert.equal(model.audio[0].primary,'English');assert.equal(model.audio[1].primary,'Commentary');
 assert.equal(model.audio[0].selected,true);assert.equal(model.audio[1].selected,false);assert.equal(model.audio[1].unavailable,true);
 assert.equal(model.audioLabel,'English');assert.equal(model.subtitles.find(row=>row.selected)?.id,'resource:real-subtitle');
 assert.equal(model.automatic,true);assert.equal(model.qualityLabel,'720p');assert.equal(model.quality.find(row=>row.selected)?.id,'auto');
 assert.equal(model.speed.find(row=>row.selected)?.id,'1.5');assert.equal(model.speed.find(row=>row.id==='1')?.label,'Normal');assert.deepEqual(f.calls,[]);
});

test('real source choices dispatch exact wire identities and full fence through the one runtime',async()=>{
 const f=fixture(),model=f.model()!;
 await model.selectAudio('a3');await model.selectSubtitle('resource:real-subtitle');await model.selectSubtitle('off');await model.selectQuality('auto');model.selectSpeed('1.5');
 assert.deepEqual(f.calls,[['audio',fence,'a3'],['subtitle',fence,'real-subtitle'],['subtitle',fence,null],['quality',fence,'automatic'],['speed',fence,1.5]]);
 await assert.rejects(model.selectAudio('a4'),/unavailable/);await assert.rejects(model.selectSubtitle('resource:fractional'),/unavailable/);
 await assert.rejects(model.selectQuality('1080p-high'),/unavailable/);assert.throws(()=>model.selectSpeed('3'),/unavailable/);
 assert.equal(f.calls.length,5);
});

test('unknown or foreign source/session/options facts produce no parent panel grant',()=>{
 for(const mutate of [
  (f:ReturnType<typeof fixture>)=>({...f.live,options:null}),
  (f:ReturnType<typeof fixture>)=>({...f.live,offers:{...f.live.offers!,scope:{...f.live.offers!.scope,serverId:'foreign'}}}),
  (f:ReturnType<typeof fixture>)=>({...f.live,session:{...f.live.session!,versionId:'missing'}}),
  (f:ReturnType<typeof fixture>)=>({...f.live,session:{...f.live.session!,itemId:'foreign'}}),
  (f:ReturnType<typeof fixture>)=>({...f.live,session:{...f.live.session!,queue:{queueId:'queue',entryId:'foreign'}}}),
  (f:ReturnType<typeof fixture>)=>({...f.live,offers:{...f.live.offers!,current:{...f.live.offers!.current!,generation:2}}}),
 ]){const f=fixture();f.set(mutate(f));assert.equal(f.model(),null);assert.deepEqual(f.calls,[]);}
 const f=fixture();f.set({...f.live,offers:{...f.live.offers!,subtitlePlan:null}});assert.deepEqual(f.model()!.subtitles,[],'absence never becomes a fake Off row');
});

test('same-title replacement and refreshed original metadata invalidate old row commands before dispatch',async()=>{
 for(const mutate of [
  (f:ReturnType<typeof fixture>)=>({...f.live,fence:{...fence,generation:2}}),
  (f:ReturnType<typeof fixture>)=>({...f.live,session:{...f.live.session!,revision:'2'}}),
  (f:ReturnType<typeof fixture>)=>({...f.live,options:{...f.live.options!}}),
  (f:ReturnType<typeof fixture>)=>({...f.live,disposed:true}),
  (f:ReturnType<typeof fixture>)=>({...f.live,authority:{...f.live.authority,viewerFence:'replaced-home-viewer'}}),
 ]){const f=fixture(),model=f.model()!;f.set(mutate(f));assert.equal(model.isCurrent(),false);await assert.rejects(model.selectAudio('a3'),{name:'AbortError'});assert.deepEqual(f.calls,[]);}
});

test('an unknown selected audio stays unselected while Automatic uses the actual bound offer rung',()=>{
 const f=fixture();f.set({...f.live,session:{...f.live.session!,presentation:{...f.live.session!.presentation,audioTrackId:undefined,quality:{mode:'automatic'}}}});
 const model=f.model()!;assert.ok(model.audio.every(row=>!row.selected));assert.equal(model.audioLabel,undefined);assert.equal(model.qualityLabel,'720p');assert.equal(model.automatic,true);assert.equal(model.quality.find(row=>row.id==='auto')?.selected,true);
});


test('Off availability and Options applicability remain real command preconditions',async()=>{
 const f=fixture();f.set({...f.live,options:{...f.live.options!,qualities:f.live.options!.qualities.filter(q=>q.mode!=='automatic')},
  offers:{...f.live.offers!,subtitlePlan:{...f.live.offers!.subtitlePlan!,offAvailable:false}}});
 const model=f.model()!;assert.equal(model.subtitles.find(row=>row.id==='off')?.unavailable,true);
 assert.equal(model.quality.find(row=>row.id==='auto')?.enabled,false);
 await assert.rejects(model.selectSubtitle('off'),/unavailable/);await assert.rejects(model.selectQuality('auto'),/unavailable/);assert.deepEqual(f.calls,[]);
 f.set({...f.live,session:{...f.live.session!,presentation:{...f.live.session!.presentation,quality:{mode:'automatic',id:'original'}}}});
 assert.equal(f.model()!.qualityLabel,'Original');
});


test('an unreported rung never becomes Original, while an explicit Original remains known',()=>{
 const f=fixture();f.set({...f.live,session:{...f.live.session!,presentation:{...f.live.session!.presentation,quality:{mode:'automatic'}}},
  offers:{...f.live.offers!,current:{...f.live.offers!.current!,qualityId:'unreported'}}});
 const model=f.model()!;assert.equal(model.qualityLabel,undefined);assert.equal(model.qualityAccessibleLabel,undefined);assert.equal(model.automatic,true);
 f.set({...f.live,offers:{...f.live.offers!,current:{...f.live.offers!.current!,qualityId:'original'}}});assert.equal(f.model()!.qualityLabel,'Original');
});

test('a rendered room Leave cannot act after its binding holds another room',async()=>{
 const fence={kind:'queue' as const,playback:{scope,run:1},sessionId:'session',itemId:'movie',sourceId:'source',entryId:'entry',generation:1};
 const runtime={groupPlaybackMatches:()=>true};
 let leaves=0;const leave=async()=>{leaves++;};
 const act=async(room:{phase:string;group?:{id:string}},current:boolean)=>{
  if(!roomActionAllowed(runtime as never,current,fence as never,room,'old-room','entry'))return;
  await leave();
 };
 await act({phase:'live',group:{id:'old-room'}},true);assert.equal(leaves,1);
 // The borrowed binding entered another room without a render; the old handler must not leave it.
 await act({phase:'live',group:{id:'new-room'}},true);assert.equal(leaves,1);
 await act({phase:'ended',group:{id:'old-room'}},true);assert.equal(leaves,1);
 await act({phase:'live',group:{id:'old-room'}},false);assert.equal(leaves,1);
 // Runtime playback identity alone never grants the action after the handler went stale.
 assert.equal(roomActionAllowed({groupPlaybackMatches:()=>false} as never,true,fence as never,{phase:'live',group:{id:'old-room'}},'old-room','entry'),false);
});

test('the phone options rows carry only real current choices in drawn order',()=>{
 const f=fixture(),model=f.model()!;
 const rows=phoneOptionRows(model,defaultI18n.t);
 assert.deepEqual(rows.map(row=>row.id),['subtitles','audio','quality','speed']);
 assert.equal(rows[0].value,model.subtitleLabel);
 assert.equal(rows[1].value,'English');
 assert.equal(rows[2].value,'Automatic');assert.equal(rows[2].detail,'720p');
 assert.equal(rows[3].value,'1.5×');
 f.set({...f.live,offers:{...f.live.offers!,subtitlePlan:null}});
 assert.deepEqual(phoneOptionRows(f.model()!,defaultI18n.t).map(row=>row.id),['audio','quality','speed']);
});

test('the quality panel names only the real current conversion',()=>{
 const f=fixture();assert.equal(f.model()!.qualityNote,undefined);
 f.set({...f.live,session:{...f.live.session!,presentation:{...f.live.session!.presentation,decision:{video:{action:'transcode',to:'av1'}}}}});
 assert.equal(f.model()!.qualityNote,'Converting to 720p');
 f.set({...f.live,session:{...f.live.session!,presentation:{...f.live.session!.presentation,decision:{video:{action:'direct'}}}}});
 assert.equal(f.model()!.qualityNote,'Playing the original file');
});

test('screen state resets on a settled runtime or playback run, never a blank frame',()=>{
 const runtime={},settled={runtime,key:JSON.stringify({scope:'s',run:1})};
 assert.equal(settledPlaybackChanged(undefined,runtime,{scope:'s',run:1}),true);
 assert.equal(settledPlaybackChanged(settled,runtime,undefined),false,'a temporary missing fence keeps the panel');
 assert.equal(settledPlaybackChanged(settled,runtime,{scope:'s',run:1}),false,'a quality generation change keeps the panel');
 assert.equal(settledPlaybackChanged(settled,runtime,{scope:'s',run:2}),true,'a new playback run resets even for the same title');
 assert.equal(settledPlaybackChanged(settled,{}, {scope:'s',run:1}),true,'a new runtime resets even for the same item and run');
});
