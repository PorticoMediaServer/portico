const test=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs'),crypto=require('node:crypto');
const source=fs.readFileSync(require('node:path').join(__dirname,'../receiver.js'),'utf8');
async function harness(options={}){
 const calls=[],loads=[],sent=[],views=[],events={},interceptors={},timers=new Map(),storage=new Map(),nodes={player:{ended:false}};
 let listener,timer=0,state='IDLE',at=12,starts=0,stops=0,advanced=false,origin='https://server.example';
 const document={getElementById:id=>nodes[id]??={addEventListener(){}},addEventListener(){}};
 let activeAudio=options.activeAudio??null,activeText=options.activeText?[...options.activeText]:[];
 const audioTracks=options.audioTracks||[],textTracks=options.textTracks||[];
 const audioManager={getTracks:()=>audioTracks,getActiveId:()=>activeAudio,setActiveById:id=>{activeAudio=id;}};
 const textManager={getTracks:()=>textTracks,getActiveIds:()=>activeText,setActiveByIds:ids=>{activeText=[...ids];}};
 const manager={getCurrentTimeSec:()=>at,getDurationSec:()=>120,getPlayerState:()=>state,getMediaInformation:()=>loads.at(-1)?.media,setMediaUrlResolver:fn=>manager.resolve=fn,
  load:async load=>{loads.push(load);state=options.loadState||'PLAYING';nodes.player.ended=false;if(load.activeTrackIds)activeText=[...load.activeTrackIds];},sendLocalMediaRequest:message=>{if(message.type==='STOP')state='IDLE';if(message.type==='PAUSE')state='PAUSED';if(message.type==='PLAY')state='PLAYING';},setMessageInterceptor:(type,fn)=>interceptors[type]=fn,addEventListener:(type,fn)=>events[type]=fn,
  getAudioTracksManager:()=>audioManager,getTextTracksManager:()=>textManager};
 const context={getPlayerManager:()=>manager,getDeviceCapabilities:()=>({}),getDeviceInformation:()=>({}),canDisplayType:()=>false,getSenders:()=>[{id:'phone'}],addCustomMessageListener:(_ns,fn)=>listener=fn,sendCustomMessage:(_ns,sender,body)=>sent.push({sender,body}),start(){}};
 const types=Object.fromEntries(['LOAD','PLAY','SEEK','GET_STATUS','QUEUE_GET_ITEMS','QUEUE_GET_ITEM_IDS','PRELOAD','QUEUE_INSERT','QUEUE_UPDATE','QUEUE_REMOVE','QUEUE_REORDER','QUEUE_LOAD','STOP','MEDIA_STATUS','CLOUD_STATUS'].map(v=>[v,v]));
 const eventTypes=Object.fromEntries(['PLAYING','PAUSE','BUFFERING','MEDIA_FINISHED','ERROR'].map(v=>[v,v]));
 const session=n=>({id:'ps_'+n,kind:'vod',itemId:'film',versionId:'version',state:'playing',lease:{reportEveryMs:10000},presentation:{generation:1,url:'/v1/media/grant'+n+'/source',mode:'direct',container:'mp4',subtitles:options.subtitles||[],...(options.subtitle?{subtitle:options.subtitle}:{}),startPositionMs:0}});
 const cast={framework:{CastReceiverContext:{getInstance:()=>context},messages:{MessageType:types},events:{EventType:eventTypes,EndedReason:{END_OF_STREAM:'END_OF_STREAM'}}}};
 const winListeners={};
 const sandbox={window:{cast,location:{origin},crypto:crypto.webcrypto,addEventListener:(type,fn)=>{winListeners[type]=fn;},porticoCastView:new Proxy({},{get:(_,method)=>value=>views.push({method,value})}),porticoCastText:{t:key=>key}},cast,document,navigator:{},localStorage:{getItem:key=>storage.get(key)??null,setItem:(key,value)=>{if(options.throwStorage)throw Error('quota');storage.set(key,value);},removeItem:key=>storage.delete(key)},performance:{now:()=>1000},URL,AbortController,Uint8Array,console,
  setTimeout:(fn,ms)=>{timers.set(++timer,{fn,ms});return timer;},clearTimeout:id=>timers.delete(id),
  fetch:async(url,request)=>{
   const path=new URL(url).pathname,body=request.body?JSON.parse(request.body):undefined;calls.push({path,body,method:request.method,headers:request.headers});
   let value={},status=200,headers={};
   if(path==='/v1/cast/redeem')value={session:{accessToken:'receiver-only',refreshToken:'receiver-refresh',expiresAt:'2099-01-01T00:00:00Z',sessionId:'signin',viewer:{serverId:'server',accountId:'account',profileId:body.code==='OTHER'?'other':'profile'}}};
   else if(path==='/v1/devices/current/capabilities')status=204;
   else if(path==='/v1/auth/session')status=204;
   else if(path==='/v1/playback/sessions'&&request.method==='POST'){
    if(options.startWait&&!starts++){status=202;value={retryAfterMs:500};}else{value=session(++starts);if(body.queue){value.queue=body.queue;if(body.queue.entryId==='second')value.itemId='next';}status=201;}
   }else if(path==='/v1/queues'&&request.method==='POST'){value={queue:{id:'cloned',revision:'1',current:{entryId:'first'}}};status=201;}
   else if(path==='/v1/queues/cloned')value={id:'cloned',revision:'1',current:{entryId:advanced?'second':'first'},...(advanced?{}:{nextEntryId:'second'})};
   else if(path==='/v1/queues/cloned/advance'){advanced=true;value={id:'cloned',revision:'2',current:{entryId:'second'}};}
   else if(path==='/v1/items/next')value={id:'next',title:'Next film',kind:'movie'};
   else if(path.endsWith('/progress'))value={generation:1,reportEveryMs:10000};
   else if(request.method==='DELETE'){
    if(options.stopFailPath&&path.endsWith(options.stopFailPath)){status=options.stopFailStatus||503;headers=status===503?{'Retry-After':'120'}:{};}
    else if(options.stop503&&!stops++){status=503;headers={'Retry-After':'120'};}else status=204;
   }else if(request.method==='PATCH'){
    if(options.patchStatus){status=options.patchStatus;value={error:{message:'patch_failed'}};}
    else{value=session(starts);const gen=options.patchKeepGeneration?1:2;value.presentation={...value.presentation,generation:gen,url:gen===1?value.presentation.url:'/v1/media/changed/source',startPositionMs:body.seek?.positionMs||0};}
   }
   else throw Error('unexpected '+path);
   return new Response(status===204?null:JSON.stringify(value),{status,headers});
  }};
 sandbox.window.localStorage=sandbox.localStorage;
 vm.runInNewContext(source,sandbox);
 const message=async(body,sender='phone')=>{await listener({senderId:sender,data:body});};
 const pair=async(code='CODE',sender='phone')=>{await message({type:'pair',code,origin,displayName:sender},sender);return sent.findLast(v=>v.sender===sender&&v.body.type==='paired')?.body.controller;};
 const load=async(itemId='film',extra={})=>{const stamp=sent.findLast(v=>v.sender==='phone'&&v.body.type==='paired').body.controller;await message({type:'load',itemId,startSeconds:3,requestId:'load-1',title:'Film',controller:stamp,...extra});};
 return {calls,loads,sent,views,storage,timers,events,winListeners,manager,interceptors,nodes,setState:next=>{state=next;},message,pair,load,finish:async()=>{nodes.player.ended=true;state='IDLE';events.MEDIA_FINISHED({endedReason:'END_OF_STREAM'});for(let i=0;i<6;i++)await new Promise(setImmediate);},fire:async(ms)=>{for(const [key,held] of [...timers])if(held.ms===ms){timers.delete(key);held.fn();}for(let i=0;i<6;i++)await new Promise(setImmediate);}};
}
test('pairs with its own scoped sign-in, publishes capabilities and uses rebuilt progress and seek',async()=>{
 const h=await harness();await h.pair();await h.load();
 const redeemed=h.calls.find(c=>c.path==='/v1/cast/redeem');assert.deepEqual(redeemed.body.device,{installationId:redeemed.body.device.installationId,name:'Google Cast',platform:'cast',app:'portico-cast'});
 assert(h.calls.some(c=>c.path==='/v1/devices/current/capabilities'&&c.method==='PUT'));
 const started=h.calls.find(c=>c.path==='/v1/playback/sessions');assert.deepEqual(started.body,{itemId:'film',startPositionMs:3000});assert(started.headers['Idempotency-Key']);
 assert(h.calls.some(c=>c.path.endsWith('/progress')&&c.method==='PUT'&&c.body.state==='playing'));
 assert(h.loads[0].media.contentId.startsWith('portico-'));assert(!JSON.stringify(h.sent).includes('receiver-only'));assert(!JSON.stringify(h.sent).includes('/v1/media/'));
 assert.equal(h.manager.resolve(h.loads[0].media),'https://server.example/v1/media/grant1/source');
 const remote=h.sent.findLast(c=>c.body.type==='status').body;await h.message({type:'seek',positionSeconds:40,target:{controller:remote.controller,occurrence:remote.occurrence}});
 assert.deepEqual(h.calls.find(c=>c.method==='PATCH').body.seek.positionMs,40000);assert.equal(h.loads.length,2);
 assert(!h.calls.some(c=>/observer|observation|user-mutations|bootstrap|controllers\//.test(c.path)));
});
test('finishing sends a plain DELETE with the observed final position',async()=>{
 const h=await harness();await h.pair();await h.load();await h.finish();
 const stopped=h.calls.find(c=>c.method==='DELETE'&&c.path.startsWith('/v1/playback/sessions/'));
 assert(stopped);assert.deepEqual(stopped.body,{positionMs:12000});assert(h.views.some(v=>v.method==='idle'));
 assert.equal(JSON.parse(h.storage.get('portico.cast.stops.v1')).length,0);
});
test('an unanswered stop stays with its exact owner, waits Retry-After, and never stops its successor',async()=>{
 const h=await harness({stop503:true});await h.pair();await h.load();const old=h.sent.findLast(c=>c.body.type==='status').body,target={controller:old.controller,occurrence:old.occurrence};
 await h.message({type:'stop',requestId:'stop-1',target});
 assert.equal(JSON.parse(h.storage.get('portico.cast.stops.v1')).length,1);assert([...h.timers.values()].some(v=>v.ms===120000));
 await h.message({type:'stop',requestId:'stop-retry',target});await h.load();const successor=h.loads.at(-1).media.contentId;await h.fire(120000);assert(h.sent.some(c=>c.body.type==='stopped'&&c.body.requestId==='stop-retry'));
 const stops=h.calls.filter(c=>c.method==='DELETE'&&c.path.startsWith('/v1/playback/sessions/'));assert.equal(stops.length,2);assert(stops.every(c=>c.path.endsWith('/ps_1')));
 assert.equal(h.manager.getMediaInformation().contentId,successor);assert.equal(h.manager.getPlayerState(),'PLAYING');
 await h.message({type:'stop',requestId:'stop-1',target});assert.equal(h.calls.filter(c=>c.method==='DELETE'&&c.path.startsWith('/v1/playback/')).length,2);assert(h.sent.some(c=>c.body.type==='stopped'&&c.body.requestId==='stop-1'));
});
test('another sender cannot load or read private status, and a different viewer must be allowed before takeover',async()=>{
 const h=await harness();await h.pair();await h.load();const before=h.loads.length;
 await h.message({type:'load',itemId:'film',controller:{}},'stranger');await h.message({type:'status'},'stranger');assert.equal(h.loads.length,before);assert(!h.sent.some(v=>v.sender==='stranger'&&v.body.type==='status'));
 await h.pair('OTHER','second');assert(h.sent.some(v=>v.sender==='second'&&v.body.type==='pair-pending'));assert.equal(h.manager.getPlayerState(),'PLAYING');
 await h.message({type:'takeover',allow:false});assert(h.sent.some(v=>v.sender==='second'&&v.body.type==='pair-failed'));assert.equal(h.manager.getPlayerState(),'PLAYING');
});

test('a preparing start repeats the same body and key until it is ready',async()=>{
 const h=await harness({startWait:true});await h.pair();const loaded=h.load();await new Promise(setImmediate);assert.equal(h.loads.length,0);await h.fire(500);await loaded;
 const starts=h.calls.filter(c=>c.path==='/v1/playback/sessions'&&c.method==='POST');assert.equal(starts.length,2);assert.equal(starts[0].headers['Idempotency-Key'],starts[1].headers['Idempotency-Key']);assert.deepEqual(starts[0].body,starts[1].body);assert.equal(h.loads.length,1);
});
test('queue playback clones onto the receiver device and completion advances with the rebuilt queue protocol',async()=>{
 const h=await harness();await h.pair();await h.load('film',{queue:{queueId:'sender-queue',entryId:'sender-entry'}});await h.finish();
 assert.deepEqual(h.calls.find(c=>c.path==='/v1/queues').body,{source:{queue:'sender-queue'},startAt:'film'});
 const moved=h.calls.find(c=>c.path==='/v1/queues/cloned/advance');assert.deepEqual(moved.body,{reason:'completion',fromEntryId:'first',sessionId:'ps_1'});assert.equal(moved.headers['If-Match'],'"1"');
 assert.equal(h.loads.length,2);assert.equal(h.loads.at(-1).media.metadata.title,'Next film');assert(h.calls.some(c=>c.path==='/v1/playback/sessions/ps_1'&&c.method==='DELETE'));
});

test('persistStops swallows storage failures so playback teardown still completes',async()=>{
 const h=await harness({throwStorage:true});await h.pair();await h.load();
 const remote=h.sent.findLast(c=>c.body.type==='status').body,target={controller:remote.controller,occurrence:remote.occurrence};
 await h.message({type:'stop',requestId:'stop-storage',target});
 assert(h.calls.some(c=>c.method==='DELETE'&&c.path==='/v1/playback/sessions/ps_1'));
 assert(h.sent.some(c=>c.body.type==='stopped'&&c.body.requestId==='stop-storage'));
 assert(h.views.some(v=>v.method==='idle'));
});

test('queue advance cleans up the successor session if stopping the finished owner fails',async()=>{
 const h=await harness({stopFailPath:'/ps_1',stopFailStatus:400});await h.pair();
 await h.load('film',{queue:{queueId:'sender-queue',entryId:'sender-entry'}});
 await h.finish();
 const deletes=h.calls.filter(c=>c.method==='DELETE'&&c.path.startsWith('/v1/playback/sessions/')).map(c=>c.path);
 assert(deletes.includes('/v1/playback/sessions/ps_1'),'attempted to stop finished owner');
 assert(deletes.includes('/v1/playback/sessions/ps_2'),'cleaned up successor after owner stop failed');
 assert.equal(h.loads.length,1,'successor was never adopted after owner stop failure');
});

test('the 10s load readiness timer is cleared on PLAYING, on ERROR, and on unload',async()=>{
 const h=await harness({loadState:'BUFFERING'});await h.pair();
 const pendingLoad=h.load();
 for(let i=0;i<6;i++)await new Promise(setImmediate);
 const readyEntry=[...h.timers.entries()].find(([,t])=>t.ms===10000);
 assert(readyEntry,'10s load timer armed while buffering');
 h.setState('PLAYING');h.events.PLAYING();
 await pendingLoad;
 assert(!h.timers.has(readyEntry[0]),'10s load timer cleared once PLAYING arrives');
 assert(h.sent.some(m=>m.body.type==='loaded'&&m.body.requestId==='load-1'));
 assert(!h.sent.some(m=>m.body.type==='load-failed'));

 const hErr=await harness({loadState:'BUFFERING'});await hErr.pair();
 const failedLoad=hErr.load();
 for(let i=0;i<6;i++)await new Promise(setImmediate);
 const errEntry=[...hErr.timers.entries()].find(([,t])=>t.ms===10000);
 assert(errEntry);
 hErr.events.ERROR({detailedErrorCode:311});
 await failedLoad;
 assert(!hErr.timers.has(errEntry[0]),'10s load timer cleared on load error');
 assert(hErr.sent.some(m=>m.body.type==='load-failed'&&m.body.requestId==='load-1'));
 assert(hErr.calls.some(c=>c.method==='DELETE'&&c.path==='/v1/playback/sessions/ps_1'));

 const hUnload=await harness({loadState:'BUFFERING'});await hUnload.pair();
 const unloadedLoad=hUnload.load();
 for(let i=0;i<6;i++)await new Promise(setImmediate);
 const unloadEntry=[...hUnload.timers.entries()].find(([,t])=>t.ms===10000);
 assert(unloadEntry);
 hUnload.winListeners.unload();
 await unloadedLoad;
 assert(!hUnload.timers.has(unloadEntry[0]),'10s load timer cleared on unload');
});

test('audio and subtitle track switches report active tracks in status and PATCH the server',async()=>{
 const subtitles=[{trackId:'sub_en',format:'webvtt',url:'/v1/media/sub/en.vtt',language:'en',title:'English'},{trackId:'sub_fr',format:'webvtt',url:'/v1/media/sub/fr.vtt',language:'fr',title:'French'}];
 const h=await harness({
  patchKeepGeneration:true,
  subtitles,
  subtitle:{trackId:'sub_en',kind:'subtitles',delivery:'sidecar'},
  audioTracks:[{trackId:10,language:'en',name:'English',customData:{trackId:'aud_en'}},{trackId:11,language:'fr',name:'French',customData:{trackId:'aud_fr'}}],
  textTracks:[{trackId:1,language:'en',name:'English'},{trackId:2,language:'fr',name:'French'}],
  activeAudio:10,
 });
 await h.pair();await h.load();
 const initial=h.sent.findLast(m=>m.body.type==='status').body;
 assert.equal(initial.activeAudioTrackId,10);
 assert.equal(initial.activeTextTrackId,1);
 assert.equal(initial.audioTracks.find(t=>t.id===10).active,true);
 assert.equal(initial.textTracks.find(t=>t.id===1).active,true);

 const target={controller:initial.controller,occurrence:initial.occurrence};
 await h.message({type:'audio',trackId:11,target});
 const afterAudio=h.sent.findLast(m=>m.body.type==='status').body;
 assert.equal(afterAudio.activeAudioTrackId,11);
 assert.equal(afterAudio.audioTracks.find(t=>t.id===11).active,true);
 assert.equal(afterAudio.audioTracks.find(t=>t.id===10).active,undefined);
 assert.deepEqual(h.calls.findLast(c=>c.method==='PATCH').body,{audio:{trackId:'aud_fr'},positionMs:12000});

 await h.message({type:'subtitles',trackIds:[2],target});
 const afterSub=h.sent.findLast(m=>m.body.type==='status').body;
 assert.equal(afterSub.activeTextTrackId,2);
 assert.equal(afterSub.textTracks.find(t=>t.id===2).active,true);
 assert.deepEqual(h.calls.findLast(c=>c.method==='PATCH').body,{subtitles:{trackId:'sub_fr'},positionMs:12000});

 await h.message({type:'subtitles',trackIds:[],target});
 const afterOff=h.sent.findLast(m=>m.body.type==='status').body;
 assert.equal(afterOff.activeTextTrackId,null);
 assert(afterOff.textTracks.every(t=>!t.active));
 assert.deepEqual(h.calls.findLast(c=>c.method==='PATCH').body,{subtitles:{trackId:null},positionMs:12000});
});

test('track switch PATCH rejection does not crash playback into an error screen',async()=>{
 const h=await harness({
  patchStatus:400,
  audioTracks:[{trackId:1,language:'en',name:'English'},{trackId:2,language:'es',name:'Spanish'}],
  activeAudio:1,
 });
 await h.pair();await h.load();
 const initial=h.sent.findLast(m=>m.body.type==='status').body;
 await h.message({type:'audio',trackId:2,target:{controller:initial.controller,occurrence:initial.occurrence}});
 assert(!h.views.some(v=>v.method==='error'),'PATCH failure does not force receiver into error state');
 assert.equal(h.sent.findLast(m=>m.body.type==='status').body.activeAudioTrackId,2);
});
