// Explicit browser workflow; not part of fast Node tests or the public Cast bundle.
// Only native IPC and server API responses are simulated. CAF/Shaka, media decoding,
// media events, clocks, controls and completion are real. No physical device claim.
const fs = require('node:fs'), path = require('node:path'), cp = require('node:child_process');
const assert = require('node:assert/strict'), crypto = require('node:crypto');
const {chromium} = require(process.env.PORTICO_PLAYWRIGHT_MODULE || 'playwright');
const arg = (name, fallback) => {const i = process.argv.indexOf(name); return i < 0 ? fallback : process.argv[i + 1];};
const root = cp.execFileSync('git', ['rev-parse', '--show-toplevel'], {encoding:'utf8'}).trim();
const ref = arg('--source-ref', 'origin/main');
const source = cp.execFileSync('git', ['rev-parse', ref], {cwd:root, encoding:'utf8'}).trim();
const sourceOnMain=cp.spawnSync('git',['merge-base','--is-ancestor',source,'origin/main'],{cwd:root}).status===0;
if(!sourceOnMain)cp.execFileSync('git',['merge-base','--is-ancestor',source,'HEAD'],{cwd:root});
const scratch = arg('--scratch'), output = arg('--output'), policyFile = arg('--policy-receipt');
assert(scratch && output && policyFile, 'pass --scratch, --output and --policy-receipt');
assert(path.resolve(scratch).startsWith('/home/justin/projects/Portico/.scratch/cast-vega-media-r29/'));
const fixtureOrigin = 'https://cast.getportico.fixture';
const policyReceipt = JSON.parse(fs.readFileSync(policyFile));
const response = policyReceipt.after.responses.find(r => r.host === 'cast.getportico.tv' && r.path === '/' && r.method === 'GET');
assert(response && response.status === 200 && response.csp.includes('https://ajax.googleapis.com/ajax/libs/shaka-player/4.15.56/'));
const diagnosticEmptyStyle=process.argv.includes('--diagnostic-empty-style');
const policy = response.csp+(diagnosticEmptyStyle?"; style-src-elem 'self' https://www.gstatic.com 'sha256-47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU='":'');
const media = path.join(scratch, 'media'); fs.mkdirSync(media, {recursive:true});
const run = args => cp.execFileSync('/usr/bin/ffmpeg', ['-hide_banner','-loglevel','error','-y',...args], {env:{...process.env,TMPDIR:scratch},timeout:30000});
run(['-f','lavfi','-i','testsrc2=size=320x180:rate=24','-f','lavfi','-i','sine=frequency=440:sample_rate=48000','-t','12','-c:v','libx264','-preset','veryfast','-profile:v','high','-pix_fmt','yuv420p','-g','48','-keyint_min','48','-sc_threshold','0','-c:a','aac','-b:a','96k','-ac','2','-threads','1','-movflags','+faststart',path.join(media,'video.mp4')]);
run(['-i',path.join(media,'video.mp4'),'-c','copy','-hls_time','2','-hls_segment_type','fmp4','-hls_playlist_type','vod','-hls_fmp4_init_filename','init.mp4','-hls_segment_filename',path.join(media,'segment%02d.m4s'),path.join(media,'master.m3u8')]);
run(['-f','lavfi','-i','sine=frequency=440:sample_rate=44100','-t','3','-c:a','libmp3lame','-b:a','128k','-ac','2','-threads','1',path.join(media,'tone-1.mp3')]);
run(['-f','lavfi','-i','sine=frequency=660:sample_rate=48000','-t','4','-c:a','flac','-ac','2','-threads','1',path.join(media,'tone-2.flac')]);
const report = {source,sourceOnMain,diagnosticEmptyStyle,policyFile,policySHA256:crypto.createHash('sha256').update(fs.readFileSync(policyFile)).digest('hex'),policy,fixtureOrigin,simulated:['native IPC transport/ready handshake; no device/model/HDR facts','server API authority responses; not production server acceptance'],actual:['Google CAF/Shaka network loading','browser media bytes/decoder/events/clocks/controls/completion'],resources:[],api:[],console:[],pageErrors:[],sourceHashes:{},physical:false};
const files = new Map();
for (const file of cp.execFileSync('git',['ls-tree','-r','--name-only',source,'server/receiver/cast'],{cwd:root,encoding:'utf8'}).trim().split('\n')) {
  const name = file.replace('server/receiver/cast/',''); if (name.startsWith('test/')) continue;
  const body = cp.execFileSync('git',['show',`${source}:${file}`],{cwd:root,maxBuffer:8*1024*1024});
  files.set(name,body); report.sourceHashes[name]=crypto.createHash('sha256').update(body).digest('hex');
}
let current, serial = 0, advances = 0;
const session = (itemId, queue) => ({id:`fixture-session-${++serial}`,revision:'1',kind:'vod',role:'local',state:'playing',itemId,...(queue?{queue}:{}),lease:{reportEveryMs:10000},presentation:{generation:1,mode:itemId==='video'?'stream':'direct',container:itemId==='video'?'fmp4_hls':itemId==='tone-1'?'mp3':'flac',url:itemId==='video'?'/v1/media/fixture/master.m3u8':`/v1/media/fixture/${itemId}.${itemId==='tone-1'?'mp3':'flac'}`,startPositionMs:0,subtitles:[],decision:itemId==='video'?{video:{action:'copy'},audio:{action:'copy'}}:{audio:{action:'copy'}}}});
(async()=>{
  let browser;
  try {
    fs.rmSync(path.join(scratch,'browser-profile'),{recursive:true,force:true});
    browser = await chromium.launchPersistentContext(path.join(scratch,'browser-profile'), {executablePath:'/usr/bin/google-chrome',headless:true,viewport:{width:1920,height:1080},args:['--disable-dev-shm-usage','--autoplay-policy=no-user-gesture-required','--js-flags=--max-old-space-size=512']});
    const page = browser.pages()[0] || await browser.newPage();
    await page.addInitScript(()=>{
      window.__cafRuntime={wire:[],violations:[],events:[]};
      document.addEventListener('securitypolicyviolation',e=>window.__cafRuntime.violations.push({blockedURI:e.blockedURI,directive:e.effectiveDirective,source:e.sourceFile}));
      const Native = window.WebSocket, system='urn:x-cast:com.google.cast.system', app='urn:x-cast:tv.getportico.cast'; let socket;
      class NativeIpc extends EventTarget {
        static CONNECTING=0;static OPEN=1;static CLOSING=2;static CLOSED=3;
        constructor(url, protocols){super();if(String(url)!=='ws://localhost:8008/v2/ipc')return protocols===undefined?new Native(url):new Native(url,protocols);this.readyState=0;socket=this;queueMicrotask(()=>{this.readyState=1;this.onopen?.(new Event('open'));});}
        send(raw){const envelope=JSON.parse(raw);let body;try{body=JSON.parse(envelope.data);}catch{body=envelope.data;}window.__cafRuntime.wire.push({direction:'out',namespace:envelope.namespace,senderId:envelope.senderId,body});
          if(envelope.namespace===system&&body.type==='ready')setTimeout(()=>{deliver(system,'SystemSender',{type:'ready',applicationId:'fixture-application',applicationName:'Portico',sessionId:'fixture-native-session',deviceCapabilities:{}});deliver(system,'SystemSender',{type:'senderconnected',senderId:'fixture-sender',userAgent:''});},0);
        }
        close(){this.readyState=3;this.onclose?.(new CloseEvent('close',{code:1000,wasClean:true}));}
      }
      function deliver(namespace,senderId,body){window.__cafRuntime.wire.push({direction:'in',namespace,senderId,body});socket.onmessage?.(new MessageEvent('message',{data:JSON.stringify({namespace,senderId,data:JSON.stringify(body)})}));}
      window.WebSocket=NativeIpc;
      window.__nativeCommand=body=>deliver(app,'fixture-sender',body);
      window.__viewSample=()=>{const el=id=>document.getElementById(id),text=id=>el(id)?.textContent;return {state:el('ui')?.dataset.state,overlay:el('ui')?.dataset.overlay,audio:el('playing')?.dataset.audio,title:text('playing-title'),subtitle:text('playing-subtitle'),audioTitle:text('audio-title'),artist:text('audio-artist'),album:text('audio-album'),pillHidden:el('state-pill')?.hidden,stateLabel:text('state-label'),timePlayed:text('time-played'),timeLeft:text('time-left'),progress:el('progress-fill')?.style.width,videoControls:document.querySelector('video#player.castMediaElement')?.controls};};
      window.__cafRuntime.viewTransitions=[];
      document.addEventListener('DOMContentLoaded',()=>{const ui=document.getElementById('ui');if(ui)new MutationObserver(()=>{const sample=window.__viewSample();const previous=window.__cafRuntime.viewTransitions.at(-1);if(window.__cafRuntime.viewTransitions.length<100&&(!previous||previous.view.state!==sample.state||previous.view.overlay!==sample.overlay))window.__cafRuntime.viewTransitions.push({at:performance.now(),view:sample});}).observe(ui,{attributes:true,attributeFilter:['data-state','data-overlay']});});
      window.__mediaSample=()=>{const all=[];function walk(node){all.push(...node.querySelectorAll('video'));for(const e of node.querySelectorAll('*'))if(e.shadowRoot)walk(e.shadowRoot);}walk(document);const v=all.find(v=>Number.isFinite(v.duration)&&v.duration>0)||all[0];const m=cast.framework.CastReceiverContext.getInstance().getPlayerManager();const cueStyles=[];function styles(node){for(const e of node.querySelectorAll('#cue-style'))cueStyles.push({tag:e.tagName,text:e.textContent,sheet:!!e.sheet,rules:e.sheet?.cssRules.length});for(const e of node.querySelectorAll('*'))if(e.shadowRoot)styles(e.shadowRoot);}styles(document);return {view:window.__viewSample(),cueStyles,time:v?.currentTime,duration:v?.duration,paused:v?.paused,ended:v?.ended,readyState:v?.readyState,frames:v?.getVideoPlaybackQuality?.().totalVideoFrames,audioBytes:v?.webkitAudioDecodedByteCount,sdkTime:m.getCurrentTimeSec(),sdkDuration:m.getDurationSec(),sdkState:m.getPlayerState(),server:window.porticoCastReceiver.state()};};
    });
    page.on('console',m=>{if(true)report.console.push({type:m.type(),text:m.text(),location:m.location()});});
    page.on('pageerror',e=>report.pageErrors.push(e.message));
    page.on('response',r=>{if(/gstatic|googleapis|\/v1\/media\//.test(r.url()))report.resources.push({url:r.url(),status:r.status(),...(r.url().includes('/v1/media/')?{range:r.request().headers().range||null}: {})});});
    await page.route(fixtureOrigin+'/**',async route=>{
      const request=route.request(),u=new URL(request.url()),p=u.pathname,body=request.postDataJSON?.();
      const json=async(data,status=200)=>route.fulfill({status,contentType:'application/json',body:JSON.stringify(data)});
      if(p.startsWith('/v1/'))report.api.push({method:request.method(),path:p,...(body?.state?{state:body.state}:{}),...(body?.positionMs!==undefined?{positionMs:body.positionMs}:{})});
      if(p==='/v1/cast/redeem')return json({device:{id:'fixture-device',generation:'1'},session:{accessToken:'fixture-only-token'},deviceToken:'fixture-only-renewal',scope:{accountId:'fixture-account',profileId:'fixture-profile',authority:'hosted'}});
      if(p==='/v1/me/devices/current/capabilities')return route.fulfill({status:204});
      if(p==='/v1/playback/sessions'&&request.method()==='POST'){current=session(body.queue?'tone-1':body.itemId,body.queue);return json(current,201);}
      if(p.startsWith('/v1/items/')){const id=decodeURIComponent(p.split('/').pop());return json({id,title:id==='video'?'Synthetic video':id,kind:id==='video'?'movie':'song',...(id==='video'?{year:2002}:{song:{artist:id==='tone-1'?'Synthetic 440 Hz':'Synthetic 660 Hz',albumTitle:id==='tone-1'?'First fixture':'Second fixture'}})});}
      if(p==='/v1/queues/fixture-queue')return json({id:'fixture-queue',revision:'1',next:{available:current?.itemId==='tone-1',entryId:'two'}});
      if(p==='/v1/queues/fixture-queue:advance'){advances++;current=session('tone-2',{queueId:'fixture-queue',entryId:'two'});return json({queue:{id:'fixture-queue',revision:'2'},session:current});}
      if(/^\/v1\/playback\/sessions\/[^/]+\/timeline$/.test(p))return route.fulfill({status:204});
      if(/^\/v1\/playback\/sessions\/[^/]+$/.test(p)&&request.method()==='DELETE'){current=null;return route.fulfill({status:204});}
      if(p.startsWith('/v1/media/fixture/')){
        const name=p.slice('/v1/media/fixture/'.length);assert(!name.includes('/'));const bytes=fs.readFileSync(path.join(media,name));const type=name.endsWith('.m3u8')?'application/x-mpegURL':name.endsWith('.mp3')?'audio/mpeg':name.endsWith('.flac')?'audio/flac':'video/mp4';
        const headers={'Accept-Ranges':'bytes'};let start=0,end=bytes.length-1,status=200;
        const range=request.headers().range;if(range){const m=/^bytes=(\d+)-(\d*)$/.exec(range);assert(m,'unsupported real browser Range '+range);start=Number(m[1]);end=m[2]?Math.min(Number(m[2]),end):end;assert(start<=end&&end<bytes.length);headers['Content-Range']=`bytes ${start}-${end}/${bytes.length}`;status=206;}
        return route.fulfill({status,contentType:type,headers,body:bytes.subarray(start,end+1)});
      }
      const name=p.slice(1)||'index.html',bytes=files.get(name);if(!bytes)return route.fulfill({status:404});
      const type=name.endsWith('.html')?'text/html':name.endsWith('.js')?'text/javascript':name.endsWith('.css')?'text/css':name.endsWith('.svg')?'image/svg+xml':name.endsWith('.ttf')?'font/ttf':'application/octet-stream';
      return route.fulfill({status:200,contentType:type,headers:{'Content-Security-Policy':policy,'Referrer-Policy':'no-referrer','Cache-Control':'no-store'},body:bytes});
    });
    await page.goto(fixtureOrigin+'/',{waitUntil:'load'});
    await page.waitForFunction(()=>window.porticoCastReceiver&&window.shaka?.Player?.version&&cast.framework.CastReceiverContext.getInstance().getSystemState()==='ready',null,{timeout:15000});
    if(files.has('view.js')){await page.evaluate(()=>document.fonts.ready);assert(await page.evaluate(()=>!!window.porticoCastView&&!!document.querySelector('video#player.castMediaElement')&&!document.querySelector('cast-media-player')&&!document.querySelector('video#player').controls));}
    report.readiness=await page.evaluate(()=>({caf:cast.framework.VERSION,shaka:shaka.Player.version,state:cast.framework.CastReceiverContext.getInstance().getSystemState()}));
    await page.evaluate(()=>{cast.framework.CastReceiverContext.getInstance().setLoggerLevel(cast.framework.LoggerLevel.DEBUG);const m=cast.framework.CastReceiverContext.getInstance().getPlayerManager();for(const name of ['LOADED_DATA','PLAYING','TIME_UPDATE','MEDIA_FINISHED','ERROR'])m.addEventListener(cast.framework.events.EventType[name],e=>window.__cafRuntime.events.push({type:name,at:performance.now(),time:m.getCurrentTimeSec(),endedReason:e.endedReason,error:e.detailedErrorCode}));window.__nativeCommand({type:'pair',code:'FIXTURE',origin:''});});
    await page.waitForFunction(()=>window.__cafRuntime.wire.some(x=>x.direction==='out'&&x.body.type==='paired'),null,{timeout:15000});
    await page.evaluate(()=>window.__nativeCommand({type:'load',requestId:'fixture-video-load',itemId:'video',title:'Synthetic video',kind:'movie'}));
    await page.waitForFunction(()=>window.__mediaSample().time>0.5&&!window.__mediaSample().paused,null,{timeout:15000});
    report.playing=await page.evaluate(()=>window.__mediaSample());assert(report.playing.frames>0);if(files.has('view.js')){assert.equal(report.playing.view.state,'playing');assert.equal(report.playing.view.subtitle,'2002');}assert(Math.abs(report.playing.time-report.playing.sdkTime)<0.3);assert(report.resources.some(x=>x.url.includes('segment')&&x.status===200));
    await page.evaluate(()=>window.__nativeCommand({type:'pause'}));await page.waitForFunction(()=>window.__mediaSample().paused);if(files.has('view.js'))await page.waitForFunction(()=>!window.__viewSample().pillHidden&&window.__viewSample().stateLabel==='Paused');
    const paused=await page.evaluate(()=>window.__mediaSample());await page.waitForTimeout(500);report.paused=await page.evaluate(()=>window.__mediaSample());assert(Math.abs(paused.time-report.paused.time)<0.1);if(files.has('view.js'))assert.equal(report.paused.view.overlay,'visible');
    await page.evaluate(()=>window.__nativeCommand({type:'seek',positionSeconds:4}));await page.waitForFunction(()=>Math.abs(window.__mediaSample().time-4)<0.3);report.seek=await page.evaluate(()=>window.__mediaSample());
    await page.evaluate(()=>window.__nativeCommand({type:'play'}));await page.waitForFunction(()=>window.__mediaSample().time>4.3&&!window.__mediaSample().paused);report.resumed=await page.evaluate(()=>window.__mediaSample());
    await page.evaluate(()=>window.__nativeCommand({type:'stop'}));await page.waitForFunction(()=>!window.porticoCastReceiver.state().playbackId);if(files.has('view.js'))await page.waitForFunction(()=>window.__viewSample().state==='idle');
    await page.evaluate(()=>window.__nativeCommand({type:'load',requestId:'fixture-queue-load',itemId:'tone-1',title:'Synthetic signal',kind:'song',queue:{queueId:'fixture-queue',entryId:'one'}}));
    await page.waitForFunction(()=>window.__mediaSample().server.playbackId==='fixture-session-3'&&window.__mediaSample().time>0.3,null,{timeout:20000});
    if(files.has('view.js'))await page.waitForFunction(()=>window.__viewSample().audioTitle==='tone-2'&&window.__viewSample().artist==='Synthetic 660 Hz'&&window.__viewSample().album==='Second fixture');
    report.progressiveNext=await page.evaluate(()=>window.__mediaSample());assert.equal(advances,1);assert(!report.api.some(x=>/:prepare-next|:commit-next/.test(x.path)));
    report.runtime=await page.evaluate(()=>window.__cafRuntime);assert(report.runtime.events.some(e=>e.type==='LOADED_DATA'));assert(report.runtime.events.some(e=>e.type==='MEDIA_FINISHED'&&e.endedReason==='END_OF_STREAM'));
    assert(report.resources.some(x=>x.url==='https://ajax.googleapis.com/ajax/libs/shaka-player/4.15.56/shaka-player.compiled.js'&&x.status===200));
    assert(!report.runtime.violations.some(x=>x.blockedURI.includes('ajax.googleapis.com')));if(files.has('view.js'))assert.equal(report.runtime.violations.length,0,'composed view must have no CSP violation');
    assert(!report.runtime.wire.some(x=>x.direction==='out'&&JSON.stringify(x.body).includes('/v1/media/')),'private media URLs must not appear in CAF/custom status');
    assert.equal(report.pageErrors.length,0,'real SDK page errors must remain visible failures');
    await page.evaluate(()=>window.__nativeCommand({type:'stop'}));report.status=diagnosticEmptyStyle?'passed-diagnostic-engine-playback':'passed-engine-playback';
  } catch(e){report.status='failed';report.failure=e.stack;process.exitCode=1;}
  finally {if(browser){try{const p=browser.pages()[0];report.final=await p.evaluate(()=>({runtime:window.__cafRuntime,sample:window.__mediaSample?.(),cueStyles:[...document.querySelectorAll('#cue-style')].map(e=>({tag:e.tagName,text:e.textContent,sheet:!!e.sheet,rules:e.sheet?.cssRules.length}))}));}catch{}await browser.close();}fs.mkdirSync(path.dirname(output),{recursive:true});fs.writeFileSync(output,JSON.stringify(report,null,2)+'\n');console.log(JSON.stringify({status:report.status,readiness:report.readiness,playing:report.playing,progressiveNext:report.progressiveNext,failure:report.failure}));}
})();
