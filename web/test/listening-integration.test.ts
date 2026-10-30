import test from 'node:test';
import assert from 'node:assert/strict';
import {queueScope,playbackFence,sameFence,type QueuePlaybackFence} from '../../packages/player-core/src/index.ts';
import {PlayerChaptersService} from '@core/player-chapters.ts';
import {LyricsService} from '@core/lyrics.ts';
import {saveQueueAsPlaylist,type QueuePlaylistSave} from '@core/playback-v1/queue.ts';
import type {V1QueueEditPage,V1QueueEditReader} from '@core/playback-v1/v1-queue-player.ts';
import {createWebListeningPort,type WebListeningLifetime} from '../src/platform/playback/listening-port.ts';
import {createQueueEditOpening} from '../src/platform/playback/queue-edit-adapter.ts';
import type {WebPlaybackRuntimeSnapshot,WebShellPlaybackRuntime,WebQueueListSnapshot,WebQueueListPort} from '../src/platform/playback';
import {ListeningOwner} from '../src/screens/watch/listening/listening-owner.ts';
import {componentModule} from './helpers/component-harness.mjs';
import {all,createRenderer,one,tree} from './helpers/interaction-harness.mjs';
import {PlaybackMediaOwner} from '../src/shell/playback-media-owner.ts';
import {PlaybackPresentations,fenceKey,localVideoSurfaceActive,playbackPresentation,retainedPlaybackNotice} from '../src/shell/playback-model.ts';
import {dispatchWatch,watchClock,watchSeek,watchSeekBounds} from '../src/screens/watch/model.ts';
import {defaultI18n} from '@i18n';
const tick=async()=>{for(let i=0;i<60;i++)await Promise.resolve();};
function deferred<T>(){let resolve!:(value:T)=>void,reject!:(error:unknown)=>void;const promise=new Promise<T>((r,e)=>{resolve=r;reject=e;});return{promise,resolve,reject};}
const scope=queueScope({serverId:'server',authority:'local',accountId:'account',profileId:'profile',controllerId:'controller',controllerEpoch:'epoch',commandLaneId:'lane'});
function makeFence(itemId='item',sourceId='source'):QueuePlaybackFence{return playbackFence({kind:'queue',playback:{scope,run:7},sessionId:'session',generation:2,itemId,entryId:'entry',sourceId});}
function chaptersWire(later=false){const indexes=later?[101]:[1,2];return{scope:{serverId:'server',libraryId:'library',itemId:'item',sessionId:'session',generation:2,sourceId:'source',viewerFence:'viewer'},revision:'revision',status:'available',reason:null,durationMs:3030000,
  totalCount:101,nextCursor:later?'':'offered-chapters',chapters:indexes.map(index=>({id:`source:${index}`,index,title:`Chapter ${index}`,startMs:(index-1)*30000,endMs:index*30000,action:{id:'seek',positionMs:(index-1)*30000}}))};}
function lyricsWire(plain=false,empty=false){
  const document={format:plain?'text':'lrc',text:'First\nSecond',lines:[{atMs:plain?null:12000,text:'First'},{atMs:plain?null:15000,text:'Second'}],embeddedOffsetMs:0};
  const resource={id:'lyric',revision:1,scope:'private',canManage:false,deleted:false,language:'en',offsetMs:plain?0:500,format:document.format,digest:'c'.repeat(64),provenance:{origin:'sidecar',providerId:'',label:'Supplied text',rights:''},createdAt:'2026-10-27T00:00:00Z',document};
  const source={id:'source',version:'b'.repeat(64),startMs:10000,durationMs:90000,sourceDurationMs:100000,local:true};
  return{scope:{serverId:'server',libraryId:'library',itemId:'item',sessionId:'session',sessionGeneration:2,viewerFence:'a'.repeat(64)},sources:[source],source,resources:[resource],selection:{revision:1,resource:empty?null:resource},canShare:false,providers:{lrclib:false,configured:true,revision:1}};
}
function harness({kind='audiobook' as 'music'|'audiobook',lyricsEnabled=true,expanded=true}={}){
  const authority={serverId:'server',authority:'local' as const,accountId:'account',profileId:'profile',epoch:1,viewerFence:'viewer'};
  const original=makeFence();let current=true,version='1',counter=0,returning=false,failChapters=false,failEdit=false,lyricMode:'lrc'|'text'|'404'|'error'|'empty'='lrc';let badLyricScope=false;
  let editWait:ReturnType<typeof deferred<void>>|undefined;
  let chapterWait:ReturnType<typeof deferred<unknown>>|undefined,lyricWait:ReturnType<typeof deferred<unknown>>|undefined,saveWait:ReturnType<typeof deferred<QueuePlaylistSave>>|undefined;
  let editDispose:(()=>void)|undefined,saveAbort:(()=>void)|undefined,policyWait:ReturnType<typeof deferred<void>>|undefined;
  let selectionRevision=1;let choiceWait:ReturnType<typeof deferred<unknown>>|undefined;
  const sourceToken={};let source:object=sourceToken;let onCurrent:(()=>void)|undefined;
  const runtimeListeners=new Set<()=>void>(),listListeners=new Set<()=>void>();
  const calls={chapters:[] as string[],lyrics:0,choices:[] as unknown[],seek:[] as number[],actions:[] as any[],captures:0,opens:0,collapses:0,mounts:0,unmounts:0,edits:[] as unknown[],policies:[] as unknown[],saveKeys:[] as string[],saveNames:[] as string[],reads:0,next:0,previous:0};
  const subject={kind,item:{id:'item',kind:kind==='music'?'song':'audiobook_file',title:'Actual title',artist:'Artist',author:'Author',narrator:'Narrator'}};
  let live:any={authority,sequence:1,disposed:false,nativeVideoSurfaceActive:false,fence:original,subject,playback:{playbackRun:4,phase:'ready',intent:'playing',positionSeconds:12,duration:3030},
    presentation:{fence:original,subject,kind,expanded,playing:true,positionSeconds:12,durationSeconds:3030,rate:1,volume:.5,muted:false,controls:['play','pause','seek','volume'],policy:null},
    dock:{local:{kind,fence:original,expanded,positionSeconds:12,playing:true,queue:null},remote:null,parkedAudio:null,resumeVideo:null},
    presentations:[],queueView:{queue:{id:'queue',revision:version,repeat:'off',shuffled:false}},options:null,offers:null,
    session:{id:'session',kind:'audio',role:'local',itemId:'item',versionId:'version',queue:{queueId:'queue',entryId:'entry'},presentation:{generation:2}}};
  const rows=(start:number)=>Array.from({length:Math.min(50,81-start)},(_,offset)=>{const position=start+offset;return{entryId:position===0?'entry':`entry-${position}`,itemId:'same-song',kind:'song',title:'Same title',position,available:position!==4};});
  let list:WebQueueListSnapshot;
  const listSnapshot=():WebQueueListSnapshot=>Object.freeze({current:true,queueId:'queue',revision:version,currentEntryId:'entry',change:'idle',more:31,entries:Object.freeze(rows(0).map(row=>({entryId:row.entryId,title:row.title,playing:row.position===0,hidden:false,pending:false,canPlay:false,canMoveUp:row.position>1&&row.available,canMoveDown:row.position>0&&row.available,canRemove:row.position>0&&row.available})))});
  list=listSnapshot();const emit=()=>{for(const listener of [...runtimeListeners])listener();};
  const updateQueue=()=>{version=String(Number(version)+1);list=listSnapshot();live={...live,queueView:{queue:{...live.queueView.queue,revision:version}}};for(const listener of [...listListeners])listener();emit();};
  const rawList:WebQueueListPort={getSnapshot:()=>list,subscribe(listener){listListeners.add(listener);return()=>{listListeners.delete(listener);};},isCurrent:()=>current,
    play:async()=>{throw new Error('audio_row_play_unavailable');},move:async()=>{throw new Error('use_actual_window');},remove:async()=>{throw new Error('use_actual_window');}};
  const runtime:any={getSnapshot:()=>live,subscribe(listener:()=>void){runtimeListeners.add(listener);return()=>{runtimeListeners.delete(listener);};},
    open(fence:QueuePlaybackFence){assert.ok(sameFence(fence,live.fence));calls.opens++;live={...live,dock:{...live.dock,local:{...live.dock.local,expanded:true}},presentation:{...live.presentation,expanded:true}};emit();},
    collapse(fence:QueuePlaybackFence){assert.ok(sameFence(fence,live.fence));calls.collapses++;live={...live,dock:{...live.dock,local:{...live.dock.local,expanded:false}},presentation:{...live.presentation,expanded:false}};emit();},
    speedChoices:()=>kind==='audiobook'?[.75,1,1.25,1.5]:[],setSpeed(_f:QueuePlaybackFence,rate:number){live={...live,presentation:{...live.presentation,rate}};emit();},
    sleepTimer:()=>undefined,setSleepTimer(_f:QueuePlaybackFence,minutes:number){calls.actions.push({kind:'sleep',minutes});},
    onAction:async(action:any)=>{calls.actions.push(action);},mount(){calls.mounts++;return()=>{calls.unmounts++;};},
    policyAction:async()=>{},replay:async()=>{},stop:async()=>{},equalizerPort:undefined,
    captureListening(fence:QueuePlaybackFence,lifetime:WebListeningLifetime){
      calls.captures++;const captured=live,capturedSource=source;
      const owns=()=>current&&source===capturedSource&&live.authority===captured.authority&&sameFence(live.fence,fence)&&live.playback.playbackRun===captured.playback.playbackRun&&!live.dock.remote;
      const api={request:async<T,>(path:string,_method?:string,_body?:unknown,signal?:AbortSignal)=>{signal?.throwIfAborted();calls.chapters.push(path);if(failChapters)throw new Error('read_failed');return(chapterWait?await chapterWait.promise:chaptersWire(path.includes('cursor=')))as T;},
        requestLyrics:async<T,>(_path:string,method?:string,body?:unknown,signal?:AbortSignal)=>{signal?.throwIfAborted();if(method==='PUT'){calls.choices.push(body);selectionRevision++;return (choiceWait?await choiceWait.promise:{revision:selectionRevision,resource:lyricsWire().resources[0]}) as T;}calls.lyrics++;if(lyricMode==='404')throw Object.assign(new Error('empty'),{status:404});if(lyricMode==='error')throw new Error('failed');const wire=lyricsWire(lyricMode==='text',lyricMode==='empty');if(badLyricScope)wire.scope.libraryId='wrong-library';wire.selection.revision=selectionRevision;return(lyricWait?await lyricWait.promise:wire)as T;}};
      return createWebListeningPort({fence,lifetime,target:{libraryId:'library',itemId:'item',sessionId:'session',sessionGeneration:2,sourceId:'source',versionId:'version',playbackRun:4,queueId:'queue',entryId:'entry'},current:owns,
        request:api.request,requestLyrics:api.requestLyrics,chaptersTarget:{libraryId:'library',itemId:'item',sessionId:'session',sessionGeneration:2,sourceId:'source'},
        makeChapters:api=>new PlayerChaptersService({api,scope:{serverId:'server',viewerId:'viewer'},timeoutMs:1000}),
        makeLyrics:api=>new LyricsService(api,{serverId:'server',viewerId:'viewer'},{libraryId:'library',itemId:'item',sessionId:'session',sessionGeneration:2,sourceId:'source'}),
        lyricsEnabled:()=>lyricsEnabled,observe:retire=>{const check=()=>{if(!owns())retire();};runtimeListeners.add(check);return()=>{runtimeListeners.delete(check);};},queue:rawList,
        seek:async(seconds,command)=>{if(!command.isCurrent()||command.signal.aborted)throw new DOMException('Aborted','AbortError');calls.seek.push(seconds);},
        policy:async(_target,revision,intent,command)=>{if(!command.isCurrent())throw new DOMException('Aborted','AbortError');assert.equal(revision,list.revision);calls.policies.push(intent);if(policyWait)await policyWait.promise;live={...live,queueView:{queue:{...live.queueView.queue,...intent.action==='repeat'?{repeat:intent.repeat}:{shuffled:intent.shuffled}}}};updateQueue();},
        save:async(queueId,name,command,onProgress)=>{command.signal.addEventListener('abort',()=>saveAbort?.(),{once:true});return saveQueueAsPlaylist({saveAsPlaylist:async(id,frozenName,key)=>{
          command.signal.throwIfAborted();assert.equal(queueId,'queue');assert.equal(id,queueId);calls.saveKeys.push(key!);calls.saveNames.push(frozenName);
          if(calls.saveKeys.length===1&&saveWait)return{playlistId:'playlist',revision:'1',entries:1,total:81,state:'saving'};
          return saveWait?await saveWait.promise:{playlistId:'playlist',revision:'2',entries:81,total:81,state:'saved'};
        }},queueId,name,{signal:command.signal,onProgress,waitMs:()=>0});},
        openEdit:(_shown,command)=>{let page:V1QueueEditPage|undefined,disposed=false;
          const issue=(start:number):V1QueueEditPage=>Object.freeze({start,total:81,revision:version,items:Object.freeze(rows(start)),canPrevious:start>0,...start<50?{nextCursor:'offered-edit-50'}:{}});
          const check=()=>{if(disposed||!owns()||!command.isCurrent()||command.signal.aborted)throw new DOMException('Aborted','AbortError');};
          const reader:V1QueueEditReader={read:async()=>{check();calls.reads++;return page=issue(0);},next:async shown=>{check();assert.equal(shown,page);calls.next++;return page=issue(50);},previous:async shown=>{check();assert.equal(shown,page);calls.previous++;return page=issue(0);},
            edit:async(shown,intent)=>{check();assert.equal(shown,page);calls.edits.push({shown,intent});if(editWait){await editWait.promise;check();}if(failEdit)throw new Error('edit_refused');updateQueue();return page=issue(shown.start);},dispose(){disposed=true;editDispose?.();}};
          return createQueueEditOpening({reader,current:owns,lifetime:command,currentEntryId:'entry',hidden:()=>false});},
      });
    },
  };
  const owner=()=>new ListeningOwner({runtime,snapshot:live,isCurrent:()=>{onCurrent?.();return current;},lyricsEnabled});
  return{runtime:runtime as WebShellPlaybackRuntime,owner,calls,get snapshot(){return live as WebPlaybackRuntimeSnapshot;},get list(){return list;},
    badLyricsScope(){badLyricScope=true;},failChapters(value:boolean){failChapters=value;},failEdit(value:boolean){failEdit=value;},lyricsMode(value:typeof lyricMode){lyricMode=value;},holdChapters(){return chapterWait=deferred<unknown>();},holdLyrics(){return lyricWait=deferred<unknown>();},releaseEdit(){editWait=undefined;},holdEdit(){return editWait=deferred<void>();},holdChoice(){return choiceWait=deferred<unknown>();},holdSave(){return saveWait=deferred<QueuePlaylistSave>();},
    retire(){current=false;emit();},remote(){live={...live,dock:{...live.dock,remote:{phase:'playing'}}};emit();},
    changeRun(){live={...live,playback:{...live.playback,playbackRun:live.playback.playbackRun+1}};emit();},changeAuthority(){live={...live,authority:{...live.authority}};emit();},replace(){source={};const fence=makeFence('successor','new-source');live={...live,fence,presentation:{...live.presentation,fence}};emit();},
    set onEditDispose(fn:(()=>void)|undefined){editDispose=fn;},set onSaveAbort(fn:(()=>void)|undefined){saveAbort=fn;},holdPolicy(){return policyWait=deferred<void>();},set onCurrent(fn:(()=>void)|undefined){onCurrent=fn;},get expanded(){return live.dock.local.expanded;},set onRead(fn:(()=>void)|undefined){runtime.getSnapshot=()=>{if(!returning){returning=true;try{fn?.();}finally{returning=false;}}return live;};},
  };
}

test('actual owner + facade + chapter service opens an audiobook, reaches offered page101, seeks part time, changes options and collapses without new playback',async()=>{
  const f=harness(),owner=f.owner();try{
    owner.commitExpanded(true);await tick();let shown=owner.getSnapshot();assert.equal(shown.chapters?.data?.chapters[0].index,1);
    await owner.chapterPage(shown,shown.chapters!,'next');shown=owner.getSnapshot();const chapter=shown.chapters!.data!.chapters[0];assert.equal(chapter.index,101);
    await owner.chapter(shown,shown.chapters!,chapter);assert.deepEqual(f.calls.seek,[3000]);assert.ok(f.calls.chapters[1].includes('cursor=offered-chapters'));
    owner.setSpeed(shown,f.snapshot,1.25);owner.setSleep(shown,f.snapshot,15);assert.equal(f.snapshot.presentation?.rate,1.25);
    const old=shown;owner.close(shown);assert.equal(f.expanded,false);assert.equal(f.calls.collapses,1);assert.equal(f.calls.captures,1);
    f.runtime.open(f.snapshot.fence!);owner.commitExpanded(true);await assert.rejects(owner.chapter(old,old.chapters!,chapter),{name:'AbortError'});
    assert.equal(f.calls.captures,1);assert.equal(f.calls.chapters.length,2);
  }finally{owner.dispose();}
});

test('chapter failure stays retryable; late source replacement cannot publish or seek the successor',async()=>{
  const f=harness();f.failChapters(true);const owner=f.owner();try{
    owner.commitExpanded(true);await tick();const failed=owner.getSnapshot();assert.equal(failed.chapters?.phase,'error');
    f.failChapters(false);await owner.chapterPage(failed,failed.chapters!,'retry');const ready=owner.getSnapshot();assert.equal(ready.chapters?.phase,'ready');
    const pending=f.holdChapters(),next=owner.chapterPage(ready,ready.chapters!,'next');f.replace();pending.resolve(chaptersWire(true));await next.catch(()=>{});
    assert.equal(owner.getSnapshot().current,false);assert.deepEqual(f.calls.seek,[]);
  }finally{owner.dispose();}
});

for(const mode of ['lrc','text','404','error'] as const)test(`actual lyrics service ${mode} is lazy, read-only without choose, and seeks only an accepted line`,async()=>{
  const f=harness({kind:'music'}),owner=f.owner();try{
    owner.commitExpanded(true);await tick();assert.equal(f.calls.lyrics,0);f.lyricsMode(mode);owner.show(owner.getSnapshot(),'lyrics');await tick();const shown=owner.getSnapshot();
    assert.equal(f.calls.lyrics,1);if(mode==='404'){assert.equal(shown.lyrics?.data,null);assert.equal(shown.lyrics?.busy,false);assert.ok(shown.lyrics!.generation>0);}
    else if(mode==='error'){assert.ok(shown.lyrics?.error);f.lyricsMode('lrc');await owner.lyricsRetry(shown);assert.ok(owner.getSnapshot().lyrics?.data);}
    else if(mode==='lrc'){await owner.lyric(shown,shown.lyrics!,1);assert.deepEqual(f.calls.seek,[5.5]);}
    else await assert.rejects(owner.lyric(shown,shown.lyrics!,0),/unavailable/);
  }finally{owner.dispose();}
});

test('the published scoped lyrics choice uses the exact drawn resource and refuses an old panel through pending PUT',async()=>{
  const f=harness({kind:'music'}),owner=f.owner();try{
    owner.commitExpanded(true);owner.show(owner.getSnapshot(),'lyrics');await tick();let shown=owner.getSnapshot();
    await owner.chooseLyric(shown,shown.lyrics!,shown.lyrics!.data!.resources[0]);assert.equal(f.calls.choices.length,1);
    assert.equal(owner.getSnapshot().lyrics!.data!.selection.revision,2);assert.equal(f.calls.lyrics,2);
    shown=owner.getSnapshot();const held=f.holdChoice(),choice=owner.chooseLyric(shown,shown.lyrics!,shown.lyrics!.data!.resources[0]);
    const rejected=assert.rejects(choice);owner.show(owner.getSnapshot(),'queue');held.resolve({revision:3,resource:lyricsWire().resources[0]});await rejected;await tick();
    assert.equal(f.calls.lyrics,2);assert.equal(owner.getSnapshot().panel,'queue');assert.equal(owner.getSnapshot().lyrics!.data!.selection.revision,2);
    await assert.rejects(owner.chooseLyric(shown,shown.lyrics!,shown.lyrics!.data!.resources[0]),{name:'AbortError'});assert.equal(f.calls.choices.length,2);
  }finally{owner.dispose();}
});

test('disabled lyrics issue no read and a pending old view cannot publish after retirement',async()=>{
  const disabled=harness({kind:'music',lyricsEnabled:false}),off=disabled.owner();off.commitExpanded(true);off.show(off.getSnapshot(),'lyrics');await tick();assert.equal(disabled.calls.lyrics,0);off.dispose();
  const f=harness({kind:'music'}),owner=f.owner();try{
    owner.commitExpanded(true);const pending=f.holdLyrics();owner.show(owner.getSnapshot(),'lyrics');await tick();const old=owner.getSnapshot();f.remote();pending.resolve(lyricsWire());await tick();
    assert.equal(owner.getSnapshot().current,false);await assert.rejects(owner.lyric(old,old.lyrics!,0),{name:'AbortError'});assert.deepEqual(f.calls.seek,[]);
  }finally{owner.dispose();}
});

test('actual Web adapter over a supplied V1QueueEditReader pages without draining, restricts current/unavailable rows and keeps occurrence identity',async()=>{
  const f=harness({kind:'music'}),owner=f.owner();try{
    owner.commitExpanded(true);await tick();let shown=owner.getSnapshot(),page=shown.window!.page!;assert.equal(page.rows.length,50);assert.equal(f.calls.next,0);
    await assert.rejects(owner.queueEdit(shown,page,'entry','remove'));await assert.rejects(owner.queueEdit(shown,page,'entry-4','remove'));
    await owner.queueEdit(shown,page,'','next');shown=owner.getSnapshot();page=shown.window!.page!;assert.equal(page.start,50);assert.equal(f.calls.next,1);
    await owner.queueEdit(shown,page,'entry-51',-1);shown=owner.getSnapshot();page=shown.window!.page!;
    const command=f.calls.edits[0] as any;assert.deepEqual(command.intent.entryIds,['entry-51','entry-50']);assert.ok(page.rows.every(row=>row.itemId==='same-song'));
    await owner.queueEdit(shown,page,'entry-52','remove');assert.equal(f.calls.edits.length,2);
  }finally{owner.dispose();}
});

test('busy publication reentry closes only its panel; no old policy or edit IO dispatches',async()=>{
  const f=harness({kind:'music'}),owner=f.owner();try{
    owner.commitExpanded(true);await tick();const shown=owner.getSnapshot();const off=owner.subscribe(()=>{if(owner.getSnapshot().policyBusy)owner.show(owner.getSnapshot(),'lyrics');});
    await assert.rejects(owner.policy(shown,shown.queue!,{action:'repeat',repeat:'all'}),{name:'AbortError'});off();assert.equal(f.calls.policies.length,0);
    await assert.rejects(owner.queueEdit(shown,shown.window!.page!,'entry-2','remove'),{name:'AbortError'});assert.equal(f.calls.edits.length,0);
  }finally{owner.dispose();}
});

test('accepted queue policy is the returned server fact; one save uses its original key through progress and leaves playback untouched',async()=>{
  const f=harness({kind:'music'}),owner=f.owner();try{
    owner.commitExpanded(true);await tick();let shown=owner.getSnapshot();await owner.policy(shown,shown.queue!,{action:'repeat',repeat:'all'});assert.equal(f.snapshot.queueView!.queue.repeat,'all');
    shown=owner.getSnapshot();owner.openSave(shown,shown.queue!);owner.saveName(owner.getSnapshot(),' My queue ');shown=owner.getSnapshot();const pending=f.holdSave();
    const saving=owner.save(shown,shown.save!.list,shown.save!.name.trim());await tick();const duplicate=owner.getSnapshot();await assert.rejects(owner.save(duplicate,duplicate.save!.list,'My queue'));
    assert.equal(new Set(f.calls.saveKeys).size,1);pending.resolve({playlistId:'playlist',revision:'2',entries:81,total:81,state:'saved'});await saving;
    assert.equal(new Set(f.calls.saveKeys).size,1);assert.ok(f.calls.saveNames.every(name=>name==='My queue'));assert.equal(owner.getSnapshot().save?.progress?.state,'saved');assert.equal(f.calls.actions.length,0);assert.equal(f.calls.opens,0);
  }finally{owner.dispose();}
});

test('a consumed dock offer and original expansion can never reopen or command a later same-source opening',async()=>{
  const f=harness({kind:'music',expanded:false}),owner=f.owner();try{
    owner.commitExpanded(false);const dock=owner.getSnapshot();assert.equal(owner.requestDock(dock,'lyrics'),true);assert.equal(owner.requestDock(dock,'lyrics'),false);
    owner.commitExpanded(true);const opened=owner.getSnapshot();assert.equal(opened.panel,'lyrics');owner.close(opened);owner.commitExpanded(false);
    assert.equal(owner.requestDock(dock,'lyrics'),false);const fresh=owner.getSnapshot();assert.equal(owner.requestDock(fresh,'queue'),true);owner.commitExpanded(true);
    await assert.rejects(owner.lyricsRetry(opened),{name:'AbortError'});assert.equal(f.calls.opens,2);assert.equal(f.calls.captures,1);
  }finally{owner.dispose();}
});

test('an edit refusal retains the original page as inert paint until an explicit new bounded opening',async()=>{
  const f=harness({kind:'music'}),owner=f.owner();try{
    owner.commitExpanded(true);await tick();const shown=owner.getSnapshot(),page=shown.window!.page!;f.failEdit(true);
    await assert.rejects(owner.queueEdit(shown,page,'entry-2','remove'));const failed=owner.getSnapshot();
    assert.equal(failed.queueError,true);assert.equal(failed.retainedWindow,page);assert.equal(f.calls.reads,1);
    await assert.rejects(owner.queueEdit(failed,page,'entry-3','remove'));assert.equal(f.calls.edits.length,1);
    f.failEdit(false);await owner.reloadQueue(failed);const fresh=owner.getSnapshot();assert.equal(fresh.queueError,false);assert.notEqual(fresh.window!.page,page);assert.equal(f.calls.reads,2);
    await assert.rejects(owner.queueEdit(shown,page,'entry-2','remove'));await owner.queueEdit(fresh,fresh.window!.page!,'entry-2','remove');assert.equal(f.calls.edits.length,2);
  }finally{owner.dispose();}
});

test('closing a pending save retires its progress and cleanup without affecting a new original dialog',async()=>{
  const f=harness({kind:'music'}),owner=f.owner();try{
    owner.commitExpanded(true);await tick();let shown=owner.getSnapshot();owner.openSave(shown,shown.queue!);owner.saveName(owner.getSnapshot(),'First');shown=owner.getSnapshot();
    const pending=f.holdSave(),saving=owner.save(shown,shown.save!.list,'First').catch(()=>{});await tick();owner.closeSave(owner.getSnapshot());
    shown=owner.getSnapshot();owner.openSave(shown,shown.queue!);owner.saveName(owner.getSnapshot(),'Second');const successor=owner.getSnapshot().save!;
    pending.resolve({playlistId:'old-playlist',revision:'2',entries:81,total:81,state:'saved'});await saving;await tick();
    assert.equal(owner.getSnapshot().save,successor);assert.equal(successor.name,'Second');assert.equal(successor.progress,null);assert.equal(successor.busy,false);
    owner.closeSave({...shown,save:{...successor,token:{}}});assert.equal(owner.getSnapshot().save,successor);
    await assert.rejects(owner.save({...shown,save:successor},successor.list,'First'));assert.equal(new Set(f.calls.saveKeys).size,1);
  }finally{owner.dispose();}
});

test('a true external predicate that replaces the original source cannot revive its command',async()=>{
  const f=harness({kind:'music'}),owner=f.owner();try{
    owner.commitExpanded(true);await tick();const shown=owner.getSnapshot();let once=true;
    f.onCurrent=()=>{if(once){once=false;f.replace();}};
    await assert.rejects(owner.policy(shown,shown.queue!,{action:'repeat',repeat:'all'}),{name:'AbortError'});
    assert.equal(f.calls.policies.length,0);assert.equal(owner.getSnapshot().current,false);
  }finally{owner.dispose();}
});

// Corrective owner-review vectors. Authored against the actual controller and
// public service/adapter composition; no additional behavioral run requested.
test('panel-origin speed, sleep and chapters cannot act through a replaced panel while persistent main controls retain their view',async()=>{
  const f=harness(),owner=f.owner();try{
    owner.commitExpanded(true);await tick();const main=owner.getSnapshot();
    owner.show(main,'speed');const speed=owner.getSnapshot();owner.show(speed,'sleep');const sleep=owner.getSnapshot();
    assert.throws(()=>owner.setSpeed(speed,f.snapshot,1.25,true),{name:'AbortError'});
    owner.show(sleep,'chapters');const chapters=owner.getSnapshot();assert.throws(()=>owner.setSleep(sleep,f.snapshot,15,true),{name:'AbortError'});
    owner.show(chapters,'queue');const live=owner.getSnapshot();
    await assert.rejects(owner.chapter(chapters,chapters.chapters!,chapters.chapters!.data!.chapters[0],true),{name:'AbortError'});
    await assert.rejects(owner.chapterPage(chapters,chapters.chapters!,'next',true),{name:'AbortError'});
    assert.equal(f.calls.chapters.length,1);assert.equal(f.calls.seek.length,0);assert.equal(f.calls.actions.length,0);
    owner.setSpeed(main,f.snapshot,1.25);assert.equal(f.snapshot.presentation!.rate,1.25);
    owner.show(live,'chapters');const current=owner.getSnapshot();await owner.chapter(current,current.chapters!,current.chapters!.data!.chapters[0],true);assert.deepEqual(f.calls.seek,[0]);
    assert.equal(owner.canReport(chapters,true),false);assert.equal(owner.canReport(main),true);
  }finally{owner.dispose();}
});

test('original editor disposal and save abort cleanup cannot detach a reentrant successor panel or dialog',async()=>{
  const f=harness({kind:'music'}),owner=f.owner();try{
    owner.commitExpanded(true);await tick();let once=true;
    f.onEditDispose=()=>{if(once){once=false;owner.show(owner.getSnapshot(),'queue');}};
    owner.show(owner.getSnapshot(),'lyrics');await tick();assert.equal(owner.getSnapshot().panel,'queue');assert.equal(owner.getSnapshot().window!.phase,'ready');
    let shown=owner.getSnapshot();owner.openSave(shown,shown.queue!);owner.saveName(owner.getSnapshot(),'Old');shown=owner.getSnapshot();
    const held=f.holdSave(),saving=owner.save(shown,shown.save!.list,'Old').catch(()=>{});await tick();
    f.onSaveAbort=()=>{const live=owner.getSnapshot();owner.openSave(live,live.queue!);owner.saveName(owner.getSnapshot(),'Successor');};
    owner.closeSave(owner.getSnapshot());const successor=owner.getSnapshot().save!;assert.equal(successor.name,'Successor');
    held.resolve({playlistId:'old',revision:'2',entries:81,total:81,state:'saved'});await saving;assert.equal(owner.getSnapshot().save,successor);
  }finally{f.onEditDispose=undefined;f.onSaveAbort=undefined;owner.dispose();}
});

test('old close cannot collapse a view installed synchronously from original cleanup',async()=>{
  const f=harness({kind:'music'}),owner=f.owner();try{
    owner.commitExpanded(true);await tick();const old=owner.getSnapshot();let once=true;
    f.onEditDispose=()=>{if(once){once=false;owner.commitExpanded(true);}};
    owner.close(old);await tick();assert.equal(f.calls.collapses,0);assert.equal(f.expanded,true);assert.ok(owner.getSnapshot().opening);assert.notEqual(owner.getSnapshot().opening,old.opening);
  }finally{f.onEditDispose=undefined;owner.dispose();}
});

test('dock busy publication can install a successor without an old expand or panel replacement',async()=>{
  const f=harness({kind:'music',expanded:false}),owner=f.owner();try{
    owner.commitExpanded(false);const old=owner.getSnapshot();let once=true;
    const off=owner.subscribe(()=>{if(once&&!owner.getSnapshot().dockOpening){once=false;f.runtime.open(f.snapshot.fence!);owner.commitExpanded(true);owner.show(owner.getSnapshot(),'queue');}});
    assert.equal(owner.requestDock(old,'lyrics'),false);off();await tick();assert.equal(f.calls.opens,1);assert.equal(owner.getSnapshot().panel,'queue');assert.equal(f.calls.lyrics,1);
  }finally{owner.dispose();}
});

test('policy final-currentness reentry preserves the successor operation busy state and command slot',async()=>{
  const f=harness({kind:'music'}),owner=f.owner();let successor:Promise<void>|undefined,held:ReturnType<typeof deferred<void>>|undefined;
  try{
    owner.commitExpanded(true);await tick();const reload=owner.reloadQueue.bind(owner);let final=false,once=true;
    owner.reloadQueue=async shown=>{await reload(shown);final=true;};
    f.onCurrent=()=>{if(final&&once){once=false;owner.show(owner.getSnapshot(),'queue');held=f.holdPolicy();const shown=owner.getSnapshot();successor=owner.policy(shown,shown.queue!,{action:'repeat',repeat:'one'});}};
    const shown=owner.getSnapshot();await owner.policy(shown,shown.queue!,{action:'repeat',repeat:'all'});await tick();
    assert.equal(owner.getSnapshot().policyBusy,true);assert.equal(f.calls.policies.length,2);
    held!.resolve();await successor;assert.equal(owner.getSnapshot().policyBusy,false);
  }finally{f.onCurrent=undefined;held?.resolve();await successor?.catch(()=>{});owner.dispose();}
});

test('actual ListeningPanels callbacks consume retired save/editor failures while current failures remain inline',async()=>{
  const f=harness({kind:'music'}),owner=f.owner(),reports:unknown[]=[];
  const h=createRenderer(),panels=await componentModule(new URL('../src/screens/watch/listening/ListeningPanels.tsx',import.meta.url),{
    react:h.react,'@i18n':{defaultI18n},'./ListeningQueue':{ListeningQueue:'ListeningQueue'},'./listening-owner':{listeningSleepChoices:[0,15,30,45,60,90]},
    './listening-panel.module.css':{default:{}},
  });
  const draw=()=>one(tree(h.render(()=>panels.ListeningPanels({host:{owner,state:owner.getSnapshot()},snapshot:f.snapshot,reportError:(e:unknown)=>reports.push(e)}))),'ListeningQueue').props;
  try{
    owner.commitExpanded(true);await tick();let props=draw();props.save.openDialog(props.snapshot);draw().save.changeName('Old');props=draw();
    const save=f.holdSave();props.save.confirm(props.save.original,props.save.name);await tick();
    // Cross the save's own retry turn so the held transport is genuinely awaited when it
    // rejects; the retired opening must still consume that failure without reporting.
    await new Promise<void>(resolve=>setTimeout(resolve,0));await tick();
    assert.equal(f.calls.saveKeys.length,2);// the retried save now awaits the held transport
    draw().save.close();props=draw();props.save.openDialog(props.snapshot);draw().save.changeName('Successor');
    const successor=owner.getSnapshot().save!;save.reject(new Error('old_save_rejected'));await tick();assert.equal(owner.getSnapshot().save,successor);assert.deepEqual(reports,[]);
    draw().save.close();props=draw();const edit=f.holdEdit();props.window.onEdit(props.window.snapshot.page,'entry-2','remove');await tick();draw().window.onRetry();await tick();
    const fresh=owner.getSnapshot().window;edit.reject(new Error('old_edit_rejected'));await tick();assert.equal(owner.getSnapshot().window,fresh);assert.equal(owner.getSnapshot().queueError,false);assert.deepEqual(reports,[]);
    f.releaseEdit();f.failEdit(true);props=draw();props.window.onEdit(props.window.snapshot.page,'entry-3','remove');await tick();assert.equal(owner.getSnapshot().queueError,true);assert.deepEqual(reports,[]);
    // The live failure is visible through the admitted inline model; no global
    // predecessor reporter is needed, and no error-class filtering is involved.
    assert.equal(draw().window.error,true);
  }finally{owner.dispose();}
});

/** Actual persistent WatchPlayback + actual committed host hook + audiobook
 * composition, using supplied runtime/core transports and inert DOM/media ports.
 * This establishes wiring and original lifetimes, never browser rendering. */
async function mountedPlayback(kind:'music'|'audiobook'='audiobook',settings?:{role?:'owner'|'admin'|'member';primary?:boolean;manage?:boolean;library?:boolean;facts?:boolean;enabled?:boolean;lyrics?:'lrc'|'text'|'404'|'error'|'empty';liveFacts?:boolean}){
  const f=harness({kind,expanded:false}),events=new EventTarget(),navigations:unknown[]=[];
  if(settings?.lyrics)f.lyricsMode(settings.lyrics);
  const oldDocument=globalThis.document,oldHTMLElement=globalThis.HTMLElement;globalThis.HTMLElement=class {} as typeof HTMLElement;
  const document:any=Object.assign(events,{fullscreenElement:null,fullscreenEnabled:false,pictureInPictureEnabled:false,activeElement:null});globalThis.document=document;
  const h=createRenderer({element:{isConnected:true,addEventListener(){},removeEventListener(){}}});
  const css={default:new Proxy({},{get:(_target,key)=>String(key)})};
  const ui={Button:'Button',Segmented:'Segmented',Slider:'Slider'};
  const deps={react:h.react,'@i18n':{defaultI18n},'../../../kit/controls':ui,'../../../kit/overlays':{Dialog:({open,children}:any)=>open?children:null,Banner:'Banner'},'../../../kit/states':{StateView:'StateView'},'../../../kit/artwork':{Artwork:'Artwork'},'../../../kit/Icon':{Icon:'Icon'},'../model':{dispatchWatch,watchClock,watchSeek,watchSeekBounds},'./audiobook-listening.module.css':css,'./listening-panel.module.css':css};
  const chapters=await componentModule(new URL('../src/screens/watch/listening/ListeningChapters.tsx',import.meta.url),deps);
  const lyrics=await componentModule(new URL('../src/screens/watch/listening/LyricsPanel.tsx',import.meta.url),deps);
  const queueWindow=await componentModule(new URL('../src/screens/watch/listening/ListeningQueueWindow.tsx',import.meta.url),deps);
  const queue=await componentModule(new URL('../src/screens/watch/listening/ListeningQueue.tsx',import.meta.url),{...deps,'./ListeningQueueWindow':queueWindow});
  const panels=await componentModule(new URL('../src/screens/watch/listening/ListeningPanels.tsx',import.meta.url),{...deps,'./ListeningChapters':chapters,'./LyricsPanel':lyrics,'./ListeningQueue':queue,'./listening-owner':{listeningSleepChoices:[0,15,30,45,60,90]}});
  const book=await componentModule(new URL('../src/screens/watch/listening/AudiobookListening.tsx',import.meta.url),deps);
  const bookHost=await componentModule(new URL('../src/screens/watch/listening/AudiobookHost.tsx',import.meta.url),{...deps,'./AudiobookListening':book,'./ListeningPanels':panels,'./listening-owner':{listeningSleepChoices:[0,15,30,45,60,90]}});
  const hook=await componentModule(new URL('../src/screens/watch/listening/use-listening-owner.ts',import.meta.url),{react:h.react,'./listening-owner':{ListeningOwner},'../../../shell/playback-model':{fenceKey}});
  const expansion=await componentModule(new URL('../src/screens/watch/music/expansion-owner.tsx',import.meta.url),{react:h.react,'../../../shell/playback-model':{fenceKey}});
  const equalizer=await componentModule(new URL('../src/screens/watch/music/EqualizerPanel.tsx',import.meta.url),{...deps,'../../../../../packages/player-core/src/index.ts':{sameFence}});
  const music=await componentModule(new URL('../src/screens/watch/music/NowPlaying.tsx',import.meta.url),{...deps,'./EqualizerPanel':equalizer,'../listening/ListeningPanels':panels,'../../../shell/playback-model':{fenceKey},'./signal':{watchMusicSignal:()=>undefined},'./audio-facts':{readWatchMusicAudioFacts:()=>({})},'../../../kit/media':{ListeningNotice:'ListeningNotice'},'../../../router':{router:{navigate:async()=>{}}},'./now-playing.module.css':css});
  const remote={remote:undefined,airPlayAvailable:false,cast:{available:false}},cast={controller:{getSnapshot:()=>remote,subscribe:()=>()=>{}},dispose(){}};
  let navigationWait:ReturnType<typeof deferred<void>>|undefined;
  const route={state:{location:{href:'/',pathname:'/'}},subscribe:()=>()=>{},navigate:async(destination:unknown)=>{navigations.push(destination);if(navigationWait)await navigationWait.promise;}};
  const playback=await componentModule(new URL('../src/screens/watch/WatchPlayback.tsx',import.meta.url),{
    react:h.react,'@i18n':{defaultI18n},'../../../../packages/player-core/src/index.ts':{sameFence},'./listening/use-listening-owner':hook,'./listening/AudiobookHost':bookHost,'./music/NowPlaying':music,
    './music/expansion-owner':{useMusicExpansion:expansion.useMusicExpansion,MusicExpansionSurface:'MusicExpansionSurface'},
    '../../shell/playback-model':{PlaybackPresentations,fenceKey,localVideoSurfaceActive,playbackPresentation,retainedPlaybackNotice},
    '../../shell/playback-media-owner':{PlaybackMediaOwner},'../../shell/viewport':{useShellWidth:()=>1440},
    '../../platform/settings-platform':{settingsPlatform:()=>undefined},'../account/feedback':{feedbackAvailable:()=>false,subscribeFeedback:()=>()=>{}},
    './feedback':{watchFeedbackTarget:()=>undefined},'./play-on-binding':{createWatchPlayOn:()=>cast,remotePictureKeepsCaptions:()=>true},
    '../../router':{router:route},'./still-watching':{watchStillWatching:()=>null},
    '../../kit/dock':{DockHost:'DockHost',DockNotice:'DockNotice',MediaSurface:'MediaSurface'},'./WatchScreen':{WatchScreen:'WatchScreen'},
    './use-video-gesture':{useVideoGesture:()=>({retain:false,active:false})},'./playback-host.module.css':css,
  });
  const preferences:any={effective:{'playback.showSyncedLyrics':settings?.enabled??true},registry:{fields:[]}};
  const viewer={serverId:'server',authority:'local',accountId:'account',profileId:'profile',role:settings?.role??'owner'};
  let session:any={viewer,sessionFamilyId:'family',deviceId:'device'};
  const identity:any={viewer,primaryProfile:settings?.primary??true,capabilities:{manageServer:settings?.manage??true},libraries:settings?.library===false?[]:[{id:'library',kind:'music',name:'Music'}]};
  let predicate:(()=>void)|undefined,sessionRead:(()=>void)|undefined,contextCurrent=true;
  const makeContext=():any=>({isCurrent:()=>{predicate?.();return contextCurrent;},session:()=>{sessionRead?.();return session;},api:{},epoch:1,preferences:{subscribe:()=>()=>{},getSnapshot:()=>preferences},
    ...(settings&&settings.facts!==false?{facts:{identity,preferences,serverName:'Server'}}:{})});
  let context=makeContext();
  const factListeners=new Set<()=>void>();
  if(settings?.liveFacts)context.factsSource={getSnapshot:()=>context.facts,subscribe:(listener:()=>void)=>{factListeners.add(listener);return()=>{factListeners.delete(listener);};}};
  const errors:unknown[]=[];
  const binding:any={runtime:f.runtime,artwork:{foreground:()=>undefined,ambient:()=>undefined},environment:defaultI18n,skips:{back:15,forward:30},reportError:(e:unknown)=>errors.push(e)};
  const show=()=>{let result:any;for(let i=0;i<5;i++)result=tree(h.render(()=>playback.WatchPlayback({binding,context})));return result;};
  return{f,show,errors,navigations,publishFacts(value:any){context.facts=value;for(const listener of [...factListeners])listener();},factSubscriptions:()=>factListeners.size,retireContext(){contextCurrent=false;},holdNavigation(){return navigationWait=deferred<void>();},get context(){return context;},set onCurrent(value:(()=>void)|undefined){predicate=value;},set onSessionRead(value:(()=>void)|undefined){sessionRead=value;},setSession(value:any){session=value;},replaceContext(){context=makeContext();},close(){f.retire();globalThis.document=oldDocument;globalThis.HTMLElement=oldHTMLElement;}};
}

test('actual persistent host mounts an audiobook and collapses to its retained dock with one media attachment and no extra capture',async()=>{
  const h=await mountedPlayback();try{
    h.show();await tick();let ui=h.show();const initialCaptures=h.f.calls.captures;assert.equal(h.f.calls.mounts,1);assert.equal(all(ui,'video').length,1);
    const dock=one(ui,'DockHost').props;assert.ok(dock.presentations[0].controls.includes('chapters'));
    dock.binding.onAction({kind:'chapters',fence:h.f.snapshot.fence});assert.deepEqual(h.errors,[]);assert.equal(h.f.expanded,true);await tick();ui=h.show();await tick();ui=h.show();
    const expanded=ui.filter((node:any)=>node.props?.['data-watch-audiobook']);assert.equal(expanded.length,1);assert.equal(h.f.calls.mounts,1);assert.equal(h.f.calls.captures,initialCaptures);assert.equal(all(ui,'video').length,1);
    const chapter=all(ui,'button').find((node:any)=>node.props.onClick&&tree(node.props.children).some((child:any)=>child.type==='b'&&child.props.children?.includes?.('Chapter 1')));assert.ok(chapter);
    const collapse=all(ui,'Button').find((node:any)=>node.props['aria-label']===defaultI18n.t('player.minimize'));assert.ok(collapse);collapse.props.onClick();ui=h.show();
    assert.equal(ui.filter((node:any)=>node.props?.['data-watch-audiobook']).length,0);assert.equal(h.f.calls.collapses,1);assert.equal(h.f.calls.mounts,1);assert.equal(h.f.calls.captures,initialCaptures);assert.equal(h.f.calls.unmounts,0);assert.equal(all(ui,'video').length,1);
  }finally{h.close();}
});

test('actual music dock Lyrics opens the same owned panel, seeks once, and closes without a second player',async()=>{
  const h=await mountedPlayback('music');try{
    h.show();await tick();let ui=h.show();const initialCaptures=h.f.calls.captures;const dock=one(ui,'DockHost').props;assert.ok(dock.presentations[0].controls.includes('lyrics'));assert.equal(h.f.calls.lyrics,0);
    dock.binding.onAction({kind:'lyrics',fence:h.f.snapshot.fence});assert.deepEqual(h.errors,[]);assert.equal(h.f.expanded,true);await tick();ui=h.show();await tick();ui=h.show();assert.equal(h.f.calls.lyrics,1);
    const line=all(ui,'button').find((node:any)=>node.props['aria-current']==='true');assert.ok(line);line.props.onClick();await tick();assert.equal(h.f.calls.seek.length,1);
    const close=all(ui,'Button').find((node:any)=>node.props.children?.includes?.(defaultI18n.t('action.close')));assert.ok(close);close.props.onClick();h.show();
    assert.equal(h.f.expanded,false);assert.equal(h.f.calls.mounts,1);assert.equal(h.f.calls.captures,initialCaptures);assert.equal(h.f.calls.unmounts,0);
  }finally{h.close();}
});


// FX228 original empty-state Settings leaf. SOURCE controls only, no execution.
const librarySettingsButton=(ui:any[])=>all(ui,'Button').find((node:any)=>node.props.children?.includes?.(defaultI18n.t('next.browse.librarySettings')));
async function openEmptyHost(h:Awaited<ReturnType<typeof mountedPlayback>>){
  h.show();await tick();let ui=h.show();const dock=one(ui,'DockHost').props;
  dock.binding.onAction({kind:'lyrics',fence:h.f.snapshot.fence});await tick();ui=h.show();await tick();return h.show();
}
test('actual persistent empty Lyrics host offers the original owner a Libraries management LIST action',async()=>{
  const h=await mountedPlayback('music',{lyrics:'404'});try{
    let ui=await openEmptyHost(h);const action=librarySettingsButton(ui);assert.ok(action);action.props.onClick();await tick();
    assert.deepEqual(h.navigations,[{to:'/settings',search:{scope:'server',section:'libraries',row:'libraries.list'}}]);
    assert.equal(h.f.calls.mounts,1);assert.equal(h.f.calls.opens,1);assert.equal(h.errors.length,0);
    // Ordinary accepted clock/render ticks retain the original owner and offer.
    const captures=h.f.calls.captures;ui=h.show();assert.ok(librarySettingsButton(ui));assert.equal(h.f.calls.captures,captures);
  }finally{h.close();}
});
for(const denied of [{role:'member'},{role:'admin'},{primary:false},{manage:false},{library:false},{facts:false}] as const)
  test(`actual empty Lyrics host withholds Settings for ${JSON.stringify(denied)}`,async()=>{
    const h=await mountedPlayback('music',{lyrics:'404',...denied});try{const ui=await openEmptyHost(h);assert.equal(librarySettingsButton(ui),undefined);assert.deepEqual(h.navigations,[]);}finally{h.close();}
  });
for(const lyrics of ['lrc','text','error'] as const)test(`actual ${lyrics} Lyrics host does not offer the empty-state Settings action`,async()=>{
  const h=await mountedPlayback('music',{lyrics});try{const ui=await openEmptyHost(h);assert.equal(librarySettingsButton(ui),undefined);assert.deepEqual(h.navigations,[]);}finally{h.close();}
});
test('actual host saved empty Settings A refuses after panel/context/source retirement while current B works',async()=>{
  const h=await mountedPlayback('music',{lyrics:'404'});try{
    let ui=await openEmptyHost(h);const a=librarySettingsButton(ui)!.props.onClick;
    const upNext=all(ui,'Button').find((node:any)=>node.props.children?.includes?.(defaultI18n.t('next.music.upNext')));assert.ok(upNext);upNext.props.onClick({currentTarget:undefined});h.show();a();await tick();assert.deepEqual(h.navigations,[]);
    // A fresh original context/layout capture is B; old callback A never adopts it.
    h.replaceContext();h.show();await tick();ui=h.show();const lyrics=all(ui,'Button').find((node:any)=>node.props.children?.includes?.(defaultI18n.t('player.lyrics')));assert.ok(lyrics);lyrics.props.onClick({currentTarget:undefined});await tick();ui=h.show();
    const b=librarySettingsButton(ui)!.props.onClick;a();await tick();assert.deepEqual(h.navigations,[]);b();await tick();assert.equal(h.navigations.length,1);
    h.f.replace();a();b();await tick();assert.equal(h.navigations.length,1);assert.equal(h.errors.length,0);
  }finally{h.close();}
});
test('actual original currentness predicate may replace identity while true; saved empty Settings never adopts it',async()=>{
  const h=await mountedPlayback('music',{lyrics:'404'});try{
    const ui=await openEmptyHost(h),a=librarySettingsButton(ui)!.props.onClick;let once=true;
    h.onCurrent=()=>{if(once){once=false;h.context.facts={...h.context.facts,identity:{...h.context.facts.identity}};}};
    a();await tick();assert.deepEqual(h.navigations,[]);assert.equal(h.errors.length,0);
  }finally{h.onCurrent=undefined;h.close();}
});

test('pending, preference-off and original principal/run/authority replacements never authorize empty Settings A',async()=>{
  const pending=await mountedPlayback('music',{});const response=pending.f.holdLyrics();try{
    const ui=await openEmptyHost(pending);assert.equal(librarySettingsButton(ui),undefined);response.resolve(lyricsWire());await tick();
  }finally{response.resolve(lyricsWire());pending.close();}
  const off=await mountedPlayback('music',{lyrics:'404',enabled:false});try{off.show();await tick();assert.equal(librarySettingsButton(off.show()),undefined);assert.equal(off.f.calls.lyrics,0);}finally{off.close();}
  for(const replacement of ['principal','run','authority'] as const){
    const h=await mountedPlayback('music',{lyrics:'404'});try{
      const ui=await openEmptyHost(h),a=librarySettingsButton(ui)!.props.onClick;
      if(replacement==='principal')h.setSession({...h.context.session(),viewer:{...h.context.session().viewer,profileId:'successor'}});
      else if(replacement==='run')h.f.changeRun();else h.f.changeAuthority();
      a();await tick();assert.deepEqual(h.navigations,[]);assert.equal(h.errors.length,0);
    }finally{h.close();}
  }
});
test('original currentness reentry returning true cannot dispatch after authority replacement',async()=>{
  const h=await mountedPlayback('music',{lyrics:'404'});try{
    const ui=await openEmptyHost(h),a=librarySettingsButton(ui)!.props.onClick;
    let once=true;h.onCurrent=()=>{if(once){once=false;h.f.changeAuthority();}};
    a();await tick();assert.deepEqual(h.navigations,[]);assert.equal(h.errors.length,0);
  }finally{h.onCurrent=undefined;h.close();}
});
test('pending navigation A never reports into replacement panel B; a current navigation failure remains visible',async()=>{
  const h=await mountedPlayback('music',{lyrics:'404'});try{
    let ui=await openEmptyHost(h);const pending=h.holdNavigation();librarySettingsButton(ui)!.props.onClick();await tick();
    const upNext=all(ui,'Button').find((node:any)=>node.props.children?.includes?.(defaultI18n.t('next.music.upNext')))!;upNext.props.onClick({currentTarget:undefined});h.show();
    pending.reject(new Error('retired_navigation'));await tick();assert.equal(h.errors.length,0);
  }finally{h.close();}
  const live=await mountedPlayback('music',{lyrics:'404'});try{
    const ui=await openEmptyHost(live),pending=live.holdNavigation();librarySettingsButton(ui)!.props.onClick();await tick();pending.reject(new Error('live_navigation'));await tick();assert.equal(live.errors.length,1);
  }finally{live.close();}
});

test('the original session read cannot return a stale value and authorize navigation after identity replacement',async()=>{
  const h=await mountedPlayback('music',{lyrics:'404'});try{
    const ui=await openEmptyHost(h),a=librarySettingsButton(ui)!.props.onClick;let once=true;
    h.onSessionRead=()=>{if(once){once=false;h.context.facts={...h.context.facts,identity:{...h.context.facts.identity}};}};
    a();await tick();assert.deepEqual(h.navigations,[]);assert.equal(h.errors.length,0);
  }finally{h.onSessionRead=undefined;h.close();}
});

test('actual empty scoped Lyrics preserves its original facade library; a mismatched supplied scope cannot offer Settings',async()=>{
  const valid=await mountedPlayback('music',{lyrics:'empty'});try{
    const ui=await openEmptyHost(valid),action=librarySettingsButton(ui);assert.ok(action);action.props.onClick();await tick();assert.equal(valid.navigations.length,1);
  }finally{valid.close();}
  const mismatch=await mountedPlayback('music',{lyrics:'empty'});mismatch.f.badLyricsScope();try{
    const ui=await openEmptyHost(mismatch);assert.equal(librarySettingsButton(ui),undefined);assert.deepEqual(mismatch.navigations,[]);
  }finally{mismatch.close();}
});

// Arm the last read in helper.current (2), and the final read immediately
// preceding open's navigation (5). The getter returns the ORIGINAL session;
// only original context currentness changes, with no identity/runtime event.
for(const finalRead of [2,5])test(`actual host final session read ${finalRead} retires currentness without an event; A refuses and separately admitted B works`,async()=>{
  const a=await mountedPlayback('music',{lyrics:'404'});try{
    const ui=await openEmptyHost(a),saved=librarySettingsButton(ui)!.props.onClick;let reads=0;
    a.onSessionRead=()=>{if(++reads===finalRead)a.retireContext();};
    saved();await tick();assert.equal(reads,finalRead);assert.deepEqual(a.navigations,[]);assert.equal(a.errors.length,0);
  }finally{a.onSessionRead=undefined;a.close();}
  const b=await mountedPlayback('music',{lyrics:'404'});try{
    const ui=await openEmptyHost(b);librarySettingsButton(ui)!.props.onClick();await tick();
    assert.deepEqual(b.navigations,[{to:'/settings',search:{scope:'server',section:'libraries',row:'libraries.list'}}]);assert.equal(b.errors.length,0);
  }finally{b.close();}
});


test('transport policy remains available while an expanded music panel is covered, and cannot survive collapse', async () => {
  const f = harness({kind: 'music'}), owner = f.owner();
  try {
    owner.commitExpanded(true);await tick();owner.cover(owner.getSnapshot());
    const shown = owner.getSnapshot();assert.equal(shown.panelOpening, null);
    await owner.policy(shown, shown.queue!, {action: 'shuffle', shuffled: true}, false);
    assert.equal(f.snapshot.queueView!.queue.shuffled, true);
    assert.equal(owner.getSnapshot().panelOpening, null);
    const current = owner.getSnapshot();owner.close(current);
    await assert.rejects(owner.policy(current, current.queue!, {action: 'repeat', repeat: 'all'}, false), {name: 'AbortError'});
    assert.equal(f.calls.policies.length, 1);
  } finally {owner.dispose();}
});
test('an expanded transport policy reserves its mutation slot across panel changes', async () => {
  const f = harness({kind: 'music'}), owner = f.owner();
  try {
    owner.commitExpanded(true);await tick();const held = f.holdPolicy(), shown = owner.getSnapshot();
    const pending = owner.policy(shown, shown.queue!, {action: 'repeat', repeat: 'all'}, false);
    owner.show(owner.getSnapshot(), 'lyrics');const other = owner.getSnapshot();
    assert.equal(other.policyBusy, true);
    await assert.rejects(owner.policy(other, other.queue!, {action: 'shuffle', shuffled: true}, false), {name: 'AbortError'});
    held.resolve();await pending;
    assert.equal(f.calls.policies.length, 1);assert.equal(owner.getSnapshot().policyBusy, false);
    assert.equal(owner.getSnapshot().panel, 'lyrics');
  } finally {owner.dispose();}
});


test('fact notification reissues only Lyrics Settings receipt on the same mounted media/lyrics/queue owner',async()=>{
  const h=await mountedPlayback('music',{lyrics:'404',liveFacts:true});try{
    let ui=await openEmptyHost(h),old=librarySettingsButton(ui)!.props.onClick;const captures=h.f.calls.captures,lyricReads=h.f.calls.lyrics,editReads=h.f.calls.edits.length;
    assert.equal(h.factSubscriptions(),1);
    const facts=h.context.facts;h.publishFacts({...facts,serverName:'Renamed',identity:{...facts.identity,profileName:'Renamed',libraries:facts.identity.libraries.map((row:any)=>({...row,name:'Renamed music'}))}});
    old();await tick();assert.deepEqual(h.navigations,[]);ui=h.show();assert.ok(librarySettingsButton(ui));
    assert.equal(h.f.calls.captures,captures);assert.equal(h.f.calls.lyrics,lyricReads);assert.equal(h.f.calls.edits.length,editReads);assert.equal(h.f.calls.mounts,1);assert.equal(h.f.calls.unmounts,0);
    librarySettingsButton(ui)!.props.onClick();await tick();assert.equal(h.navigations.length,1);
    h.publishFacts({...h.context.facts,identity:{...h.context.facts.identity,capabilities:{manageServer:false}}});ui=h.show();assert.equal(librarySettingsButton(ui),undefined);
  }finally{h.close();assert.equal(h.factSubscriptions(),0);}
});

test('fact replacement retires a pending Settings navigation flight without reporting its failure into the reissued offer',async()=>{
  const h=await mountedPlayback('music',{lyrics:'404',liveFacts:true});try{
    const ui=await openEmptyHost(h),held=h.holdNavigation();librarySettingsButton(ui)!.props.onClick();await tick();
    h.publishFacts({...h.context.facts,serverName:'Renamed'});assert.ok(librarySettingsButton(h.show()));
    held.reject(new Error('retired_navigation'));await tick();assert.equal(h.errors.length,0);assert.equal(h.f.calls.mounts,1);assert.equal(h.f.calls.unmounts,0);
  }finally{h.close();assert.equal(h.factSubscriptions(),0);}
});
