/* The Cast receiver uses server-next's one-use sign-in code and playback routes.
 * Credentials stay on this receiver; sender and public CAF status carry no grant.
 */
(function () {
 'use strict';
 const namespace='urn:x-cast:tv.getportico.cast';
 const context=window.cast?.framework?.CastReceiverContext.getInstance();
 const manager=context?.getPlayerManager();
 const id=()=>Array.from(window.crypto.getRandomValues(new Uint8Array(16)),x=>x.toString(16).padStart(2,'0')).join('');
 const validId=value=>typeof value==='string'&&/^[A-Za-z0-9._:-]{1,128}$/.test(value)&&/[A-Za-z0-9]/.test(value);
 const view=(method,...args)=>window.porticoCastView?.[method]?.(...args);
 const copy=key=>window.porticoCastText?.t('cast.receiver.'+key)||'';
 let credential, controller, stamp, pending, playing, metadata, timer, renewal, serial=Promise.resolve(), sequence=0;
 const stopsKey='portico.cast.stops.v1',stopRecords=new Map(),stopCommands=new Map();
 const persistStops=()=>localStorage.setItem(stopsKey,JSON.stringify([...stopRecords.values()]));
 let installation;try{installation=localStorage.getItem('portico.cast.installation');}catch{}
 if(!/^[a-f0-9]{32}$/.test(installation||'')){installation=id();try{localStorage.setItem('portico.cast.installation',installation);}catch{}}
 const send=(sender,body)=>{try{context?.sendCustomMessage(namespace,sender,body);}catch{}};
 const position=()=>Math.max(0,Number(manager?.getCurrentTimeSec())||0);
 const current=owner=>playing===owner;
 const serially=work=>{const result=serial.then(work);serial=result.catch(()=>{});return result;};
 function origin(value){const parsed=new URL(value||window.location.origin);if(parsed.protocol!=='https:'||parsed.username||parsed.password||parsed.pathname!=='/'||parsed.search||parsed.hash)throw Error('invalid_origin');return parsed.origin;}
 async function request(auth,path,method='GET',body,headers={}){
  if(!/^\/v1\//.test(path))throw Error('invalid_path');
  const cancel=new AbortController(), timeout=setTimeout(()=>cancel.abort(),15000);
  try{
   const response=await fetch(auth.origin+path,{method,headers:{...(body===undefined?{}:{'Content-Type':'application/json'}),...(auth.session?{Authorization:'Bearer '+auth.session.accessToken}:{}),...headers},body:body===undefined?undefined:JSON.stringify(body),signal:cancel.signal,credentials:'omit',redirect:'error',referrerPolicy:'no-referrer'});
   const text=await response.text();if(text.length>1<<20)throw Error('invalid_document');
   let value;try{value=text?JSON.parse(text):undefined;}catch{}
   if(!response.ok){const error=Error(value?.error?.message||'request_failed');error.status=response.status;error.retry=response.headers.get('Retry-After');throw error;}
   return {value,status:response.status,headers:response.headers};
  }finally{clearTimeout(timeout);}
 }
 async function api(auth,path,method,body,headers){
  if(new Date(auth.session.expiresAt).getTime()<Date.now()+30000)await refresh(auth);
  try{return await request(auth,path,method,body,headers);}catch(error){if(error.status!==401)throw error;await refresh(auth);return request(auth,path,method,body,headers);}
 }
 async function refresh(auth){
  if(renewal?.auth===auth)return renewal.work;
  auth.refreshId ||= id();persistStops();
  const work=request({origin:auth.origin},'/v1/auth/refresh','POST',{refreshToken:auth.session.refreshToken,installationId:installation,requestId:auth.refreshId}).then(answer=>{if(!answer.value?.accessToken||!answer.value?.refreshToken)throw Error('invalid_session');auth.session=answer.value;delete auth.refreshId;persistStops();});
  renewal={auth,work};try{await work;}finally{if(renewal?.work===work)renewal=undefined;}
 }
 function identity(owner=playing){return owner&&stamp?{controller:stamp,occurrence:{sessionId:owner.session.id,generation:owner.session.presentation.generation,itemId:owner.session.itemId}}:undefined;}
 function same(a,b){return !!a&&!!b&&JSON.stringify(a)===JSON.stringify(b);}
 function status(sender=controller){
  if(!sender||sender!==controller)return;
  if(!playing){send(sender,{type:'status',controller:stamp,itemId:'',paused:true});return;}
  const audio=manager.getAudioTracksManager(),text=manager.getTextTracksManager();
  const tracks=values=>(values||[]).slice(0,32).map(t=>({id:t.trackId,language:t.language||'',name:t.name||''}));
  send(sender,{type:'status',controller:stamp,...(playing.requestId?{attemptId:playing.requestId}:{}),occurrence:identity().occurrence,itemId:playing.session.itemId,versionId:playing.session.versionId,positionSeconds:position(),durationSeconds:Math.max(0,Number(manager.getDurationSec?.())||0),paused:manager.getPlayerState()!=='PLAYING',clockKnown:true,audioTracks:tracks(audio.getTracks()),textTracks:tracks(text.getTracks())});
 }
 function draw(){if(!playing)return;view('playing',{meta:metadata,state:manager.getPlayerState().toLowerCase(),positionSeconds:position(),durationSeconds:manager.getDurationSec?.(),remoteKeysAvailable:true});}
 function command(type,fields){const value=Object.assign({type,requestId:++sequence},fields);local.add(value);manager.sendLocalMediaRequest(value);}
 const local=new WeakSet(), addresses=new Map();
 function mediaPath(value){if(typeof value!=='string'||!/^\/v1\/media\/[^\\\s]+$/.test(value)||value.includes('..'))throw Error('invalid_media_url');return value;}
 async function artwork(owner,path){
  if(typeof path!=='string'||!/^\/v1\/[^\\\s#]+$/.test(path)||path.includes('..'))return;
  const cancel=new AbortController(),timeout=setTimeout(()=>cancel.abort(),10000);
  try{
   const response=await fetch(owner.auth.origin+path,{headers:{Authorization:'Bearer '+owner.auth.session.accessToken},signal:cancel.signal,credentials:'omit',redirect:'error',referrerPolicy:'no-referrer'});
   const kind=(response.headers.get('Content-Type')||'').split(';')[0];
   if(!response.ok||!/^image\/(png|jpeg|webp|avif|gif)$/.test(kind)||Number(response.headers.get('Content-Length'))>256<<10)return;
   const blob=await response.blob();if(blob.size>256<<10)return;
   const image=await new Promise(resolve=>{const reader=new FileReader();reader.onload=()=>resolve(reader.result);reader.onerror=()=>resolve('');reader.readAsDataURL(blob);});
   if(current(owner)&&typeof image==='string'){view('artwork',image,['song','track','album','audiobook'].includes(metadata?.kind));owner.image=image;}
  }catch{}finally{clearTimeout(timeout);}
 }
 async function adopt(owner,session){
  const p=session?.presentation;if(!validId(session?.id)||!validId(session?.itemId)||!Number.isSafeInteger(p?.generation)||p.generation<1)throw Error('invalid_session');
  if(!current(owner))return;
  owner.session=session;
  const contentId='portico-'+id();addresses.set(contentId,owner.auth.origin+mediaPath(p.url));while(addresses.size>4)addresses.delete(addresses.keys().next().value);
  const types={mp3:'audio/mpeg',flac:'audio/flac',m4a:'audio/mp4',m4b:'audio/mp4',aac:'audio/aac',adts:'audio/aac',ogg:'audio/ogg',opus:'audio/ogg',wav:'audio/wav',webm:'video/webm',mp4:'video/mp4',m4v:'video/mp4',mov:'video/mp4'};
  let container=p.container;const render=p.audioRender;if(session.kind==='audio'&&render?.url===p.url)container=render.container;
  const media={contentId,streamType:'BUFFERED',contentType:p.mode==='stream'?'application/x-mpegurl':types[container]};
  if(!media.contentType)throw Error('unsupported_container');
  if(p.mode==='stream')media.hlsSegmentFormat=media.hlsVideoSegmentFormat=container==='fmp4_hls'?'FMP4':'TS';
  if(metadata?.title)media.metadata={metadataType:session.kind==='audio'?3:0,title:metadata.title,...(metadata.subtitle?{subtitle:metadata.subtitle}:{})};
  const load={media,currentTime:Math.max(0,p.startPositionMs||0)/1000,autoplay:session.state!=='paused'};
  const sub=(p.subtitles||[]).find(s=>s.trackId===p.subtitle?.trackId&&s.format==='webvtt');
  if(sub){media.tracks=[{trackId:1,type:'TEXT',subtype:'SUBTITLES',trackContentId:owner.auth.origin+mediaPath(sub.url),trackContentType:'text/vtt'}];load.activeTrackIds=[1];}
  owner.contentId=contentId;owner.adopted=false;local.add(load);await manager.load(load);
  if(!current(owner))return;
  owner.adopted=true;draw();schedule(owner,0);
 }
 async function plainStop(owner,at){
  if(!owner||owner.stopped)return;
  const key=owner.auth.origin+'|'+owner.auth.session.sessionId+'|'+owner.session.id;
  owner.stopBody ||= Number.isFinite(at)?{positionMs:Math.round(Math.max(0,at)*1000)}:{};
  const record=stopRecords.get(key)||{auth:owner.auth,session:{id:owner.session.id},stopBody:owner.stopBody,due:Date.now()};
  if(!stopRecords.has(key)&&stopRecords.size>=32)throw Error('stop_store_full');
  stopRecords.set(key,record);persistStops();
  const path='/v1/playback/sessions/'+encodeURIComponent(owner.session.id);
  try{
   await api(owner.auth,path,'DELETE',owner.stopBody);owner.stopped=true;stopRecords.delete(key);persistStops();
   owner.onStopped?.();
  }catch(error){
   if(error.status>=400&&error.status<500&&error.status!==429){stopRecords.delete(key);persistStops();throw error;}
   const seconds=Number(error.retry),date=Date.parse(error.retry);const delay=error.retry!==null&&Number.isFinite(seconds)?seconds*1000:Number.isFinite(date)?date-Date.now():1000;
   record.due=Date.now()+Math.max(1000,delay);persistStops();
   setTimeout(()=>{void plainStop(owner).catch(()=>view('notice',copy('notice.stopFailed')));},Math.max(1000,delay));throw error;
  }
 }
 async function stop(owner=playing){
  if(!owner||owner.stopped)return;
  const at=current(owner)?position():undefined;
  if(current(owner)){playing=undefined;clearTimeout(timer);command('STOP');view('idle');status();}
  await plainStop(owner,at);
  if(owner.auth!==credential)void request(owner.auth,'/v1/auth/session','DELETE').catch(()=>{});
 }
 function schedule(owner,delay){clearTimeout(timer);if(current(owner))timer=setTimeout(()=>{void report(owner).catch(()=>{});},Math.max(250,delay));}
 async function report(owner,state){
  if(!current(owner))return;
  const now=performance.now(),status=state||(manager.getPlayerState()==='PLAYING'?'playing':manager.getPlayerState()==='BUFFERING'?'buffering':'paused');
  if(owner.since!==undefined)owner.played+=Math.max(0,now-owner.since);
  owner.since=status==='playing'?now:undefined;
  try{
   const answer=await api(owner.auth,'/v1/playback/sessions/'+encodeURIComponent(owner.session.id)+'/progress','PUT',{state:status,positionMs:Math.round(position()*1000),playClockMs:Math.floor(owner.played)});
   if(!current(owner))return;
   if(answer.value?.state==='ended'){playing=undefined;command('STOP');view('idle');return;}
   if(answer.value?.generation&&answer.value.generation!==owner.session.presentation.generation){const next=await api(owner.auth,'/v1/playback/sessions/'+encodeURIComponent(owner.session.id));if(current(owner))await adopt(owner,next.value);}
  }catch(error){if(current(owner)&&(error.status===401||error.status===403||error.status===404)){playing=undefined;command('STOP');view('error','unavailable');return;}}
  if(current(owner)){draw();statusToController();schedule(owner,owner.session.lease?.reportEveryMs||10000);}
 }
 const statusToController=()=>status();
 async function change(owner,body){
  if(!current(owner))return;
  const answer=await api(owner.auth,'/v1/playback/sessions/'+encodeURIComponent(owner.session.id),'PATCH',{...body,positionMs:Math.round(position()*1000)});
  if(current(owner)&&answer.value.presentation.generation!==owner.session.presentation.generation)await adopt(owner,answer.value);
 }
 async function startSession(auth,body,stillCurrent){
  const key=id(),until=Date.now()+30000;let answer;
  try{
   for(;;){
    if(!stillCurrent()||Date.now()>until)throw Error('start_timeout');
    try{answer=await api(auth,'/v1/playback/sessions','POST',body,{'Idempotency-Key':key});}
    catch(error){if(error.status>=400&&error.status<500&&error.status!==429)throw error;if(Date.now()>until)throw error;await new Promise(resolve=>setTimeout(resolve,1000));continue;}
    if(!stillCurrent())throw Error('controller_retired');
    if(answer.status!==202)return answer;
    await new Promise(resolve=>setTimeout(resolve,Math.max(250,answer.value?.retryAfterMs||500)));
   }
  }catch(error){if(validId(answer?.value?.id))await plainStop({auth,session:answer.value}).catch(()=>{});throw error;}
 }
 async function load(sender,message){
  if(!validId(message.itemId))throw Error('invalid_item');
  const auth=credential,previous=playing;
  metadata=Object.fromEntries(['title','subtitle','seriesTitle','artist','albumTitle','kind'].filter(key=>typeof message[key]==='string').map(key=>[key,message[key].slice(0,200)]));
  for(const key of ['season','episode'])if(Number.isSafeInteger(message[key])&&message[key]>=0)metadata[key]=message[key];
  view('loading',metadata);
  const body={itemId:message.itemId,startPositionMs:Math.round(Math.max(0,Number(message.startSeconds)||0)*1000)};
  if(validId(message.versionId))body.versionId=message.versionId;
  if(validId(message.audioTrackId))body.audio={trackId:message.audioTrackId};
  if(message.subtitleTrackId==='off')body.subtitles={trackId:null};else if(validId(message.subtitleTrackId))body.subtitles={trackId:message.subtitleTrackId};
  if(validId(message.queue?.queueId)){
   const cloned=await api(auth,'/v1/queues','POST',{source:{queue:message.queue.queueId},startAt:message.itemId},{'Idempotency-Key':id()});
   if(!validId(cloned.value?.queue?.id)||!validId(cloned.value?.queue?.current?.entryId))throw Error('invalid_queue');
   delete body.itemId;body.queue={queueId:cloned.value.queue.id,entryId:cloned.value.queue.current.entryId};
  }
  const answer=await startSession(auth,body,()=>auth===credential&&sender===controller);
  const owner={auth,session:answer.value,played:0,requestId:message.requestId};
  try{if(previous)await stop(previous);playing=owner;await adopt(owner,answer.value);void artwork(owner,message.artworkPath);if(!current(owner)||sender!==controller)throw Error('controller_retired');
   if(manager.getPlayerState()!=='PLAYING'){await new Promise((resolve,reject)=>{owner.onPlaying=resolve;setTimeout(()=>reject(Error('playback_not_ready')),10000);});}
   if(!current(owner)||sender!==controller)throw Error('controller_retired');
   await report(owner,'playing');
   send(sender,{type:'loaded',requestId:message.requestId,itemId:answer.value.itemId});status(sender);
  }catch(error){await plainStop(owner).catch(()=>{});if(current(owner))playing=undefined;throw error;}
 }
 async function finished(owner){
  if(!current(owner))return;
  await report(owner,'ended');
  const queue=owner.session.queue;
  if(queue){
   const head=await api(owner.auth,'/v1/queues/'+encodeURIComponent(queue.queueId));
   if(!current(owner))return;
   if(head.value?.current?.entryId!==queue.entryId)throw Error('queue_changed');
   if(head.value.nextEntryId){
    const next=await api(owner.auth,'/v1/queues/'+encodeURIComponent(queue.queueId)+'/advance','POST',{reason:'completion',fromEntryId:queue.entryId,sessionId:owner.session.id},{'If-Match':'"'+head.value.revision+'"','Idempotency-Key':id()});
    if(!current(owner)||!validId(next.value?.current?.entryId))return;
    metadata=undefined;view('splash');
    const body={queue:{queueId:queue.queueId,entryId:next.value.current.entryId},startFrom:'beginning'};
    const answer=await startSession(owner.auth,body,()=>current(owner));
    const successor={auth:owner.auth,session:answer.value,played:0};
    await plainStop(owner);if(!current(owner)){await plainStop(successor);return;}
    try{const item=await api(owner.auth,'/v1/items/'+encodeURIComponent(answer.value.itemId));metadata={title:item.value.title,kind:item.value.kind};successor.artworkPath=item.value.images?.poster||item.value.posterUrl;}catch{metadata=undefined;}
    playing=successor;try{await adopt(successor,answer.value);void artwork(successor,successor.artworkPath);}catch(error){await stop(successor);throw error;}status();return;
   }
  }
  await stop(owner);
 }
 async function install(candidate,sender){
  const sameViewer=credential&&candidate.origin===credential.origin&&['accountId','profileId','serverId'].every(key=>candidate.session.viewer?.[key]===credential.session.viewer?.[key]);
  const old=credential;
  if(!sameViewer&&playing)await stop();
  credential=candidate;
  if(old&&(!playing||playing.auth!==old))void request(old,'/v1/auth/session','DELETE').catch(()=>{});
  await api(candidate,'/v1/devices/current/capabilities','PUT',await probeAudioOutput(buildProfile(context)));
  if(controller&&controller!==sender)send(controller,{type:'replaced'});
  controller=sender;stamp={bindingId:id(),controllerGeneration:1,connectionGeneration:1};
  view('takeover',null);send(sender,{type:'paired',controller:stamp});status(sender);
 }
 async function pair(sender,message){
  const candidate={origin:origin(message.origin)};
  const result=await request(candidate,'/v1/cast/redeem','POST',{code:message.code,device:{installationId:installation,name:'Google Cast',platform:'cast',app:'portico-cast'}});
  if(!result.value?.session?.accessToken||!result.value.session.refreshToken||!result.value.session.viewer?.profileId)throw Error('invalid_session');candidate.session=result.value.session;
  const sameViewer=credential&&candidate.origin===credential.origin&&['accountId','profileId','serverId'].every(key=>candidate.session.viewer[key]===credential.session.viewer[key]);
  if(playing&&!sameViewer&&sender!==controller){
   if(pending){await request(candidate,'/v1/auth/session','DELETE').catch(()=>{});send(sender,{type:'pair-failed',code:'tv_busy'});return;}
   const present=context.getSenders().some(s=>s.id===controller),timeout=present?30000:10000;
   const held={candidate,sender};pending=held;
   view('takeover',{requester:String(message.displayName||'').slice(0,64),controllerPresent:present,timeoutMs:timeout,remoteKeysAvailable:true});
   send(sender,{type:'pair-pending'});if(present)send(controller,{type:'takeover-requested',requester:String(message.displayName||'').slice(0,64)});
   held.timer=setTimeout(()=>{if(pending===held)void answerTakeover(!present);},timeout);return;
  }
  await install(candidate,sender);
 }
 async function answerTakeover(allow){const held=pending;if(!held)return;pending=undefined;clearTimeout(held.timer);view('takeover',null);if(allow)await install(held.candidate,held.sender);else{await request(held.candidate,'/v1/auth/session','DELETE').catch(()=>{});send(held.sender,{type:'pair-failed',code:'tv_busy'});}}
 async function receive(event){
  let message=event.data;if(typeof message==='string'){if(message.length>65536)return;try{message=JSON.parse(message);}catch{return;}}
  if(!message||typeof message!=='object')return;
  const sender=event.senderId;
  if(message.type==='pair'){try{await pair(sender,message);}catch{send(sender,{type:'pair-failed',code:'pair_failed'});}return;}
  if(sender!==controller){if(message.type==='pause'){command('PAUSE');return;}if(message.type==='stop'&&!message.target){await stop();return;}send(sender,{type:'busy'});return;}
  if(message.type==='takeover'){await answerTakeover(message.allow===true);return;}
  if(message.type==='status'){status(sender);return;}
  if(message.type==='cancel-load'){if(same(message.controller,stamp)&&playing?.requestId===message.requestId)await stop(playing);return;}
  if(message.type==='load'&&!same(message.controller,stamp)){send(sender,{type:'load-failed',requestId:message.requestId});return;}
  if(message.type==='stop'){const held=stopCommands.get(message.requestId)||[...stopCommands.values()].find(value=>same(value.target,message.target));if(held){if(same(held.target,message.target)){stopCommands.set(message.requestId,held);while(stopCommands.size>32)stopCommands.delete(stopCommands.keys().next().value);if(held.confirmed)send(sender,{type:'stopped',requestId:message.requestId,target:held.target});}return;}}
  if(message.target&&!same(message.target,identity())){send(sender,{type:'control-failed',requestId:message.requestId,target:message.target});return;}
  switch(message.type){
   case 'load':try{await load(sender,message);}catch{view('error','unavailable');send(sender,{type:'load-failed',requestId:message.requestId});}break;
   case 'play':command('PLAY');break;
   case 'pause':command('PAUSE');break;
   case 'seek':if(playing&&Number.isFinite(message.positionSeconds))await change(playing,{seek:{positionMs:Math.round(Math.max(0,message.positionSeconds)*1000),id:id()}});break;
   case 'audio':manager.getAudioTracksManager().setActiveById(message.trackId);break;
   case 'subtitles':manager.getTextTracksManager().setActiveByIds(message.trackIds||[]);break;
   case 'stop':{const target=identity(),owner=playing;if(!target||!validId(message.requestId))break;const held={target,confirmed:false};stopCommands.set(message.requestId,held);while(stopCommands.size>32)stopCommands.delete(stopCommands.keys().next().value);owner.onStopped=()=>{held.confirmed=true;for(const [requestId,value]of stopCommands)if(value===held)send(sender,{type:'stopped',requestId,target});};await stop(owner);break;}
  }
  draw();status();
 }
  var progressiveAudioProbes = {aac:'audio/mp4; codecs="mp4a.40.2"',mp3:'audio/mpeg',opus:'audio/ogg; codecs="opus"',vorbis:'audio/ogg; codecs="vorbis"',flac:'audio/flac',pcm:'audio/wav; codecs="1"'};
  function buildProfile(context) {
    var caps = {};
    try { caps = context ? context.getDeviceCapabilities() || {} : {}; } catch (_) { /* no output facts */ }
    function supports(codec, width, height, fps) { try { var parts=codec.split('; codecs=');return !!context && context.canDisplayType(parts[0],parts[1]?parts[1].replace(/"/g,''):'',width,height,fps); } catch (_) { return false; } }
    var client = {family:'cast',form:'cast',platform:'cast',app:'portico-cast'};
    try {
      var info = context && typeof context.getDeviceInformation === 'function' ? context.getDeviceInformation() : null;
      if (info && typeof info['model-name'] === 'string' && info['model-name'].trim() && info['model-name'].trim().toLowerCase()!=='unknown') client.model=info['model-name'].trim().slice(0,64);
    } catch (_) { /* a codec probe is not a device identity */ }
    var fourK = supports('video/mp4; codecs="hev1.2.4.L153.B0"', 3840, 2160, 60);
    var ranges = ['sdr'];
    if (caps.is_hdr_supported === true) ranges.push('hdr10');
    var width = fourK ? 3840 : 1920, height = fourK ? 2160 : 1080;
    // Every Chromecast since the 2nd generation decodes H.264 High@4.2 1080p60; probe it rather than assume 30 fps (CAST-08).
    var h264At60 = supports('video/mp4; codecs="avc1.64002A"', 1920, 1080, 60);
    var video = [{codec:'h264',bitDepths:[8],maxWidth:1920,maxHeight:1080,maxLevel:h264At60?42:41,maxFrameRate:h264At60?60:30,dynamicRanges:['sdr'],evidence:h264At60?'probed':'declared'},{codec:'vp8',bitDepths:[8],maxWidth:1920,maxHeight:1080,maxFrameRate:30,dynamicRanges:['sdr'],evidence:'declared'}];
    [['hevc','hev1.2.4.L153.B0'],['vp9','vp09.02.10.10'],['av1','av01.0.08M.10']].forEach(function (pair) {
      if (supports('video/mp4; codecs="' + pair[1] + '"', width, height, 60)) video.push({codec:pair[0],bitDepths:[8,10],maxWidth:width,maxHeight:height,maxFrameRate:60,dynamicRanges:ranges.filter(function (x) {return x !== 'dolby_vision' || pair[0] === 'hevc';}),dolbyVisionProfiles:[],evidence:'probed'});
    });
    var hevc = video.filter(function(v) { return v.codec === 'hevc'; })[0];
    if (hevc && caps.is_dv_supported === true) {
      hevc.dolbyVisionProfiles = [5,8].filter(function(profile) { return supports('video/mp4; codecs="dvhe.0' + profile + '.07"', width, height, 30); });
      if (hevc.dolbyVisionProfiles.length) { hevc.dynamicRanges.push('dolby_vision'); ranges.push('dolby_vision'); }
    }
    // Do not turn a MIME/codec answer into a fabricated decoded channel count. The SDK's
    // passthrough flag is separate from ordinary E-AC-3 acceptance, notably on newer Streamers.
    var audio = [];
    [['aac','audio/mp4; codecs="mp4a.40.2"'],['mp3','audio/mpeg'],['opus','audio/ogg; codecs="opus"'],['vorbis','audio/ogg; codecs="vorbis"'],['flac','audio/flac'],['pcm','audio/wav; codecs="1"']].forEach(function(pair) {
      var probed = supports(pair[1]);
      // Keep a conservative declared operating point when this platform has no
      // exact rate query. Google's published FLAC ceiling is 96 kHz/24 bit;
      // the other defaults are app limits, not a claim about the output sink.
      if (probed || pair[0] === 'aac' || pair[0] === 'mp3') audio.push({codec:pair[0],maxChannels:2,maxSampleRate:pair[0]==='flac'?96000:48000,
        ...(pair[0]==='flac'?{maxBitDepth:24}:pair[0]==='pcm'?{maxBitDepth:16}:{}),evidence:probed?'mixed':'declared'});
    });
    var passthrough = [];
    [['ac3','ac-3',6],['eac3','ec-3',8]].forEach(function (pair) {
      if (supports('audio/mp4; codecs="' + pair[1] + '"')) {
        var atmos = pair[0] === 'eac3' && caps.is_dolby_atmos_supported === true;
        passthrough.push({codec:pair[0],variants:pair[0] === 'ac3' ? ['ac3'] : atmos ? ['eac3','eac3_joc'] : ['eac3'],maxChannels:pair[2],evidence:'probed',objectAudio:atmos ? ['atmos'] : []});
      }
    });
    if (caps.display_supported === false) passthrough = [];
    var fmp4Audio=audio.filter(function(a){return ({aac:'mp4a.40.2',mp3:'mp4a.69',flac:'fLaC',opus:'Opus'})[a.codec]&&supports('audio/mp4; codecs="'+({aac:'mp4a.40.2',mp3:'mp4a.69',flac:'fLaC',opus:'Opus'})[a.codec]+'"');}).map(function(a){return a.codec;}).concat(passthrough.map(function(a){return a.codec;}));
    var hlsAudio=audio.filter(function(a){return ['aac','mp3'].indexOf(a.codec)>=0;}).map(function(a){return a.codec;}).concat(passthrough.map(function(a){return a.codec;}));
    var names=video.map(function (v) {return v.codec;});
    var transports=[{transport:'direct',containers:['mp4','m4v','mov'],video:names.filter(function (c) {return c!=='vp9'&&c!=='vp8';}),audio:fmp4Audio},{transport:'hls_ts',containers:['mpegts'],video:['h264'],audio:hlsAudio},{transport:'hls_fmp4',containers:['fmp4'],video:names.filter(function (c) {return c!=='vp9'&&c!=='vp8';}),audio:fmp4Audio}];
    var webmAudio=audio.filter(function(a){return ['opus','vorbis'].indexOf(a.codec)>=0&&supports('audio/webm; codecs="'+a.codec+'"');}).map(function(a){return a.codec;});
    transports.push({transport:'direct',containers:['webm'],video:names.filter(function(c){return c==='vp8'||c==='vp9'||c==='av1';}),audio:webmAudio});
    // Each container tuple belongs only to the decoder that was admitted for it.
    // The previous Cartesian list wrongly declared, for example, PCM in Ogg and
    // FLAC in an unprobed MP4 container.
    audio.forEach(function(a){
      var containers=({aac:['aac','adts'],mp3:['mp3'],flac:['flac'],opus:['ogg','opus'],vorbis:['ogg'],pcm:['wav']})[a.codec];
      if(containers&&(supports(progressiveAudioProbes[a.codec])||a.codec==='aac'||a.codec==='mp3'))transports.push({transport:'direct',containers:containers,video:[],audio:[a.codec]});
      var mp4Codec=({aac:'mp4a.40.2',mp3:'mp4a.69',flac:'fLaC',opus:'Opus'})[a.codec];
      if(mp4Codec&&supports('audio/mp4; codecs="'+mp4Codec+'"'))transports.push({transport:'direct',containers:['m4a','m4b','mp4'],video:[],audio:[a.codec]});
    });
    // The capability document (version 2): the device, and what its one player engine can do.
    // Display presence and codec acceptance do not identify the active audio route.
    return {version:2,client:client,engineSetting:'automatic',display:{dynamicRanges:ranges},audioOutput:{route:'',maxChannels:0,spatial:false},engines:{native:{name:'caf-shaka',evidence:'mixed',video:video,audio:audio,audioPassthrough:passthrough,transports:transports,subtitleRendering:['text']}}};
  }
  /** CAF's type probe has no channel/rate arguments. Where the receiver also exposes the
   * MediaCapabilities API, ask exact decoded configurations; silence/timeout stays stereo. */
  async function probeAudioOutput(profile) {
    var capabilities = typeof navigator !== 'undefined' && navigator.mediaCapabilities;
    if (!capabilities || typeof capabilities.decodingInfo !== 'function') return profile;
    await Promise.all(profile.engines.native.audio.map(async function(codec) {
      var mime = progressiveAudioProbes[codec.codec];
      if (!mime) return;
      async function accepts(channels, rate) {
        var timer;
        try { return (await Promise.race([capabilities.decodingInfo({type:'file',audio:{contentType:mime,channels:String(channels),samplerate:rate,bitrate:({mp3:320000,aac:256000,opus:192000,vorbis:192000})[codec.codec]||Math.min(8000000,rate*channels*(codec.maxBitDepth||16))}}),new Promise(function(resolve){timer=setTimeout(function(){resolve(null);},1500);})]))?.supported === true; }
        catch (_) { return false; } finally { clearTimeout(timer); }
      }
      // A bounded set of actual configurations; a positive 192 kHz answer does
      // not stand in for an untested 176.4 kHz operating point.
      for (var channels of [8,6,2]) { if (await accepts(channels,48000)) { codec.maxChannels=channels;break; } }
      var rates=[8000,11025,12000,16000,22050,24000,32000,44100,48000,88200,96000,176400,192000];
      var ceiling=({flac:96000,opus:48000,mp3:48000,aac:96000})[codec.codec]||192000;
      var accepted=await Promise.all(rates.filter(function(rate){return rate<=ceiling;}).map(async function(rate){return await accepts(codec.maxChannels,rate)?rate:0;}));
      accepted=accepted.filter(function(rate){return rate>0;});
      if(accepted.length){codec.sampleRates=accepted;codec.maxSampleRate=Math.max.apply(null,accepted);codec.evidence='mixed';}
    }));
    // Decoding support does not measure the active output route or its channels.
    return profile;
  }
 if(!context||!manager){view('idle');return;}
 manager.setMediaUrlResolver(media=>{const address=addresses.get(media.contentId);if(!address)throw Error('unknown_deck');return address;});
 const types=cast.framework.messages.MessageType,events=cast.framework.events.EventType;
 for(const type of ['LOAD','PLAY','SEEK','GET_STATUS','QUEUE_GET_ITEMS','QUEUE_GET_ITEM_IDS'])if(types[type])manager.setMessageInterceptor(types[type],request=>local.has(request)||request.senderId===controller?request:null);
 if(types.LOAD)manager.setMessageInterceptor(types.LOAD,request=>local.has(request)?request:null);
 for(const type of ['PRELOAD','QUEUE_INSERT','QUEUE_UPDATE','QUEUE_REMOVE','QUEUE_REORDER','QUEUE_LOAD'])if(types[type])manager.setMessageInterceptor(types[type],()=>null);
 if(types.STOP)manager.setMessageInterceptor(types.STOP,request=>{if(local.has(request))return request;return serially(async()=>{await stop();return request;}).catch(()=>null);});
 if(types.SEEK)manager.setMessageInterceptor(types.SEEK,request=>{if(local.has(request))return request;if(request.senderId!==controller||!playing||!Number.isFinite(request.currentTime))return null;void serially(()=>change(playing,{seek:{positionMs:Math.round(Math.max(0,request.currentTime)*1000),id:id()}}));return null;});
 // Public Cast status uses opaque deck ids, never a grant-bearing media address.
 for(const type of ['MEDIA_STATUS','CLOUD_STATUS'])if(types[type])manager.setMessageInterceptor(types[type],()=>({type,playerState:playing?manager.getPlayerState():'IDLE',...(playing?{media:{contentId:playing.contentId,...(metadata?.title?{metadata:{metadataType:playing.session.kind==='audio'?3:0,title:metadata.title}}:{})}}:{})}));
 context.addCustomMessageListener(namespace,event=>serially(()=>receive(event)).catch(()=>{const kind=event.data?.type;if(kind==='seek')view('notice',copy('notice.seekFailed'));else if(kind==='stop')view('notice',copy('notice.stopFailed'));else view('error','unavailable');}));
 for(const type of ['PLAYING','PAUSE','BUFFERING'])if(events[type])manager.addEventListener(events[type],()=>{if(playing){if(type==='PLAYING'){playing.onPlaying?.();playing.onPlaying=undefined;}draw();void report(playing).catch(()=>{});}});
 if(events.MEDIA_FINISHED)manager.addEventListener(events.MEDIA_FINISHED,event=>{const owner=playing,media=manager.getMediaInformation(),element=document.getElementById('player');if(event?.endedReason!==cast.framework.events.EndedReason.END_OF_STREAM||!owner?.adopted||owner.finished||media?.contentId!==owner.contentId||element?.ended!==true)return;owner.finished=true;void serially(async()=>{if(!current(owner))return;await finished(owner);}).catch(()=>{void stop(owner).catch(()=>{});view('error','unavailable');});});
 if(events.ERROR)manager.addEventListener(events.ERROR,event=>{const owner=playing,generation=owner?.session.presentation.generation;if(!owner||owner.recovering)return;owner.recovering=true;const code=[103,301,311,312].includes(Number(event?.detailedErrorCode))?'network_error':'decode_error';void serially(()=>current(owner)&&owner.session.presentation.generation===generation?change(owner,{failure:{code}}):undefined).catch(()=>{view('error','decode');void stop(owner).catch(()=>{});}).finally(()=>{owner.recovering=false;});});
 document.addEventListener('keydown',event=>{if(!pending)return;if(event.key==='Enter')void serially(()=>answerTakeover(true));if(event.key==='Escape'||event.key==='Backspace')void serially(()=>answerTakeover(false));});
 window.addEventListener('unload',()=>{clearTimeout(timer);});
 try{const saved=JSON.parse(localStorage.getItem(stopsKey)||'[]');if(Array.isArray(saved)&&saved.length<=32)for(const record of saved){if(!validId(record.session?.id)||!record.auth?.session?.accessToken||!record.auth.session.refreshToken||origin(record.auth.origin)!==record.auth.origin||!Number.isFinite(record.due))continue;const key=record.auth.origin+'|'+record.auth.session.sessionId+'|'+record.session.id;stopRecords.set(key,record);setTimeout(()=>{void plainStop(record).catch(()=>{});},Math.max(0,record.due-Date.now()));}}catch{}
 view('idle');context.start({disableIdleTimeout:true});
})();
