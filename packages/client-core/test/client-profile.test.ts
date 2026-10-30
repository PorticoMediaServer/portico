import test from 'node:test';
import assert from 'node:assert/strict';
import {androidDeclaredProfile,androidProfile,appleDeclaredProfile,appleProfile,castDeclaredProfile,capabilityDocumentProblem,engineRendersSubtitle,fireTvDeclaredProfile,mpvEngine,probeWebProfile,rokuDeclaredProfile,tizenDeclaredProfile,vegaDeclaredProfile,webDeclaredProfile,webosDeclaredProfile,withMpvEngine,withViewerSettings,type WebProbeEnvironment} from '../src/client-profile.ts';

test('every declared table is a document the server accepts',()=>{
 const tables=[webDeclaredProfile(),appleDeclaredProfile({television:true}),appleDeclaredProfile({television:false}),androidDeclaredProfile(),androidDeclaredProfile({television:true}),fireTvDeclaredProfile(),vegaDeclaredProfile(),webosDeclaredProfile(),tizenDeclaredProfile(),rokuDeclaredProfile(),rokuDeclaredProfile({uhd:false}),castDeclaredProfile('chromecast'),castDeclaredProfile('google_tv_4k'),castDeclaredProfile('nest_hub')];
 for(const d of tables){
  const native=d.engines.native;
  assert.equal(capabilityDocumentProblem(d),null,d.client.family+' '+(d.client.model??''));
  assert.equal(d.version,2);assert.equal(d.engineSetting,'automatic');assert.equal(d.engines.mpv,undefined);
  assert.ok(native.transports!.some(t=>t.transport==='hls_ts'&&t.video!.includes('h264')&&t.audio!.includes('aac')),'every device can take the last-resort conversion');
  assert.deepEqual(native.subtitleRendering,['text'],'no table claims a renderer the app does not have');
  assert.ok(native.name,'the engine is named');
 }
 assert.equal(appleDeclaredProfile({television:true}).engines.native.transports!.some(t=>t.containers?.includes('mkv')),false,'AVPlayer has never played Matroska');
 assert.deepEqual(appleDeclaredProfile({television:true}).engines.native.video!.find(v=>v.codec==='hevc')!.codecTags,['hvc1','dvh1'],'AVFoundation takes HEVC under hvc1 only');
 assert.equal(appleDeclaredProfile({television:true}).engines.native.embeddedAudioSwitching,true);
 assert.ok(castDeclaredProfile('chromecast').engines.native.video!.every(v=>v.codec!=='hevc'));
 assert.ok(castDeclaredProfile('google_tv_4k').engines.native.video!.find(v=>v.codec==='hevc')!.dynamicRanges!.includes('dolby_vision'));
 const googleTV=castDeclaredProfile('google_tv_4k').engines.native,streamer=castDeclaredProfile('google_tv_streamer').engines.native;
 assert.equal(googleTV.video!.some(v=>v.codec==='av1'),false,'the older 4K model does not inherit Streamer AV1');
 assert.equal(googleTV.transports!.some(t=>t.video?.includes('av1')),false,'transport offers cannot invent the absent decoder');
 assert.ok(streamer.video!.some(v=>v.codec==='av1'),'Streamer retains its declared AV1 decoder');
 assert.ok(streamer.transports!.some(t=>t.transport==='hls_fmp4'&&t.video?.includes('av1')),'Streamer retains AV1 over fMP4 HLS');
 // Passthrough is beside the decoders, never one of them.
 for(const d of [vegaDeclaredProfile(),webosDeclaredProfile()]){
  assert.deepEqual(d.engines.native.audioPassthrough!.map(a=>a.codec),['ac3','eac3']);
  assert.equal(d.engines.native.audio!.some(a=>a.codec==='ac3'||a.codec==='eac3'),false);
 }
 assert.deepEqual(rokuDeclaredProfile().engines.native.audioPassthrough!.map(a=>a.codec),['ac3','eac3','dts'],'Roku passes the DTS core to the sink');
 assert.equal(rokuDeclaredProfile().engines.native.audio!.some(a=>a.codec==='ac3'||a.codec==='eac3'||a.codec==='dts'),false);
});

test('the Roku document declares what its models take, per VIDEO.md VC-5b',()=>{
 const uhd=rokuDeclaredProfile();
 const native=uhd.engines.native,video=native.video!;
 const hevc=video.find(v=>v.codec==='hevc')!,h264=video.find(v=>v.codec==='h264')!;
 // MKV direct, HEVC 10-bit and HLG on the 4K models.
 assert.ok(native.transports!.find(t=>t.transport==='direct'&&t.containers!.includes('mkv'))!.video!.includes('hevc'));
 assert.deepEqual(hevc.bitDepths,[8,10]);assert.ok(hevc.dynamicRanges!.includes('hlg'));
 // Dolby Vision is declared only for the models that report it: 5 on HEVC
 // (`dvhe.05`) and 9 on H.264 (`dvav.09`).
 assert.equal(uhd.display.dynamicRanges!.includes('dolby_vision'),false,'a model that has not reported Dolby Vision does not claim it');
 assert.deepEqual(hevc.dolbyVisionProfiles,[],'no Dolby Vision until roDeviceInfo reports it');
 const dv=rokuDeclaredProfile({dolbyVision:true});
 assert.deepEqual(dv.engines.native.video!.find(v=>v.codec==='hevc')!.dolbyVisionProfiles,[5]);
 assert.deepEqual(dv.engines.native.video!.find(v=>v.codec==='h264')!.dolbyVisionProfiles,[9]);
 assert.ok(dv.engines.native.video!.find(v=>v.codec==='hevc')!.codecTags!.includes('dvh1'));
 assert.ok(dv.display.dynamicRanges!.includes('dolby_vision'));
 // HDR10+ arrives on AV1, and only on the models that decode AV1.
 assert.equal(uhd.display.dynamicRanges!.includes('hdr10plus'),false);
 assert.equal(video.some(v=>v.codec==='av1'),false);
 const av1=rokuDeclaredProfile({av1:true});
 const av1Codec=av1.engines.native.video!.find(v=>v.codec==='av1')!;
 assert.deepEqual(av1Codec.bitDepths,[8,10]);assert.ok(av1Codec.dynamicRanges!.includes('hdr10plus'));
 assert.ok(av1.engines.native.transports!.find(t=>t.transport==='direct')!.video!.includes('av1'));
 // DTS passthrough names the core it can hand over, and Atmos rides E-AC-3.
 const dts=native.audioPassthrough!.find(a=>a.codec==='dts')!;
 assert.deepEqual(dts.variants,['dts_core']);assert.equal(dts.maxChannels,8);
 assert.deepEqual(native.audioPassthrough!.find(a=>a.codec==='eac3')!.objectAudio,['atmos']);
 assert.equal(h264.dolbyVisionProfiles!.length,0,'an SDR H.264 model claims no Dolby Vision');
});

test('the validator mirrors the server',()=>{
 const d=webDeclaredProfile();
 assert.equal(capabilityDocumentProblem({...d,version:1 as any}),'version');
 assert.equal(capabilityDocumentProblem({...d,client:{family:'Web Browser'}}),'client.family');
 assert.equal(capabilityDocumentProblem({...d,engineSetting:'vlc' as any}),'engineSetting');
 assert.equal(capabilityDocumentProblem({...d,overrides:{forceStereo:'yes' as any}}),'overrides');
 assert.equal(capabilityDocumentProblem({...d,display:{dynamicRanges:['hdr11' as any]}}),'display');
 assert.equal(capabilityDocumentProblem({...d,engines:{} as any}),'engines.native');
 assert.equal(capabilityDocumentProblem({...d,engines:{native:{...d.engines.native,evidence:'trust me' as any}}}),'engines.native.evidence');
 assert.equal(capabilityDocumentProblem({...d,engines:{native:{...d.engines.native,transports:[{transport:'rtsp' as any}]}}}),'engines.native.transports');
 assert.equal(capabilityDocumentProblem({...d,engines:{native:{...d.engines.native,subtitleRendering:['teletext' as any]}}}),'engines.native.subtitleRendering');
 assert.equal(capabilityDocumentProblem({...d,engines:{native:d.engines.native,mpv:{evidence:'declared',video:'all' as any}}}),'engines.mpv.video.undefined');
});

test('the viewer’s player setting and overrides are stamped on the document',()=>{
 const d=androidDeclaredProfile({television:true});
 const stamped=withViewerSettings(d,{engine:'native',overrides:{forceStereo:true,disableHdr:true}});
 assert.equal(stamped.engineSetting,'native');
 assert.deepEqual(stamped.overrides,{forceStereo:true,disablePassthrough:false,disableHdr:true});
 assert.equal(capabilityDocumentProblem(stamped),null);
 assert.equal(withViewerSettings(d,{engine:'mpv'}).engineSetting,'automatic','mpv cannot be chosen on a device that does not carry it');
 assert.equal(withViewerSettings(withMpvEngine(d),{engine:'mpv'}).engineSetting,'mpv');
 assert.deepEqual(withViewerSettings(d,{}).overrides,{forceStereo:false,disablePassthrough:false,disableHdr:false});
});

test('the mpv engine reads everything, draws every subtitle kind and respects the device’s decoders',()=>{
 const apple=appleProfile({television:true},{hardwareDecode:{hevc:true,dolbyVision:true},hdrModes:{hdr10:true,hlg:true,dolbyVision:true},eligibleForHDRPlayback:true,audio:{maxOutputChannels:8,route:'HDMI'}});
 const d=withMpvEngine(apple,{version:'0.39'});
 assert.equal(capabilityDocumentProblem(d),null);
 const mpv=d.engines.mpv!;
 assert.equal(mpv.name,'mpv');assert.equal(mpv.version,'0.39');
 assert.ok(mpv.transports!.find(t=>t.transport==='direct'&&t.video?.length)!.containers!.includes('mkv'));
 assert.deepEqual(mpv.subtitleRendering,['text','ass','pgs','vobsub','dvb']);
 assert.equal(mpv.toneMapsHdr,true);assert.equal(mpv.embeddedAudioSwitching,true);
 for(const kind of ['ass','pgs','vobsub','dvb'] as const){assert.equal(engineRendersSubtitle(d,'mpv',kind),true);assert.equal(engineRendersSubtitle(d,'native',kind),false);}
 assert.ok(mpv.audio!.some(a=>a.codec==='dts')&&mpv.audio!.some(a=>a.codec==='truehd'),'mpv decodes what AVPlayer cannot');
 const hevc=mpv.video!.find(v=>v.codec==='hevc')!;
 assert.equal(hevc.maxHeight,2304,'hardware HEVC keeps the native limits');assert.deepEqual(hevc.bitDepths,[8,10]);
 assert.deepEqual(hevc.dynamicRanges,['sdr','hdr10','hlg'],'no Dolby Vision unless the build shows it');
 assert.equal(mpv.video!.find(v=>v.codec==='vp9')!.maxHeight,1080,'software decoding stops at the software ceiling');
 assert.equal(mpv.video!.find(v=>v.codec==='mpeg2video')!.interlaced,true);
 assert.deepEqual(mpv.audioPassthrough,[],'no passthrough unless the build has it');
 // A trimmed build and a Dolby Vision build say so.
 const built=mpvEngine(apple.engines.native,{missingAudio:['truehd'],libass:false,dolbyVisionProfiles:[5,8],softwareMaxHeight:720});
 assert.equal(built.audio!.some(a=>a.codec==='truehd'),false);
 assert.deepEqual(built.subtitleRendering,['text','pgs','vobsub','dvb']);
 assert.ok(built.video!.find(v=>v.codec==='hevc')!.dynamicRanges!.includes('dolby_vision'));
 assert.equal(built.video!.find(v=>v.codec==='av1')!.maxHeight,720);
});

const chromeLike=(extra:Partial<WebProbeEnvironment>={}):WebProbeEnvironment=>({
 canPlayType:type=>/avc1\.64|mp4a|audio\/mpeg|opus|vorbis|flac|vp09\.00|av01\.0\.08M\.08/.test(type)?'probably':'',
 mediaSourceSupports:type=>/avc1\.64|mp4a|audio\/mpeg|opus|flac|vp09|av01/i.test(type),
 decodingInfo:async q=>({supported:!/hvc1|dvh1/.test(q.contentType)&&q.transferFunction===undefined,smooth:true,powerEfficient:true}),
 matchMedia:()=>false,screen:{width:2560,height:1440},audioOutputChannels:2,embeddedAudioTracks:false,identity:{platform:'chrome',engine:'blink',engineVersion:'130.0'},...extra});

test('a Chromium desktop: no HEVC, AV1 and VP9 found, stereo out',async()=>{
 const d=await probeWebProfile(chromeLike());
 const p=d.engines.native;
 assert.equal(capabilityDocumentProblem(d),null);
 assert.equal(p.evidence,'probed');assert.equal(p.name,'blink');assert.equal(p.version,'130.0');
 assert.equal((d.client as any).engine,undefined,'the engine is named on the engine, not the device');
 assert.deepEqual(p.video!.map(v=>v.codec).sort(),['av1','h264','vp9']);
 assert.equal(p.video!.find(v=>v.codec==='h264')!.bitDepths!.includes(10),false,'Hi10P is not decoded by browsers');
 assert.equal(d.audioOutput!.maxChannels,2);
 assert.ok(p.transports!.find(t=>t.transport==='direct'&&t.containers!.includes('webm'))!.video!.includes('vp9'));
 assert.equal(p.transports!.some(t=>t.video?.includes('hevc')),false);
 assert.equal(p.embeddedAudioSwitching,false);assert.equal(p.toneMapsHdr,false);
 assert.deepEqual(p.subtitleRendering,['text']);
});

test('Safari on an HDR Mac: HEVC, HDR10, Dolby Vision, native HLS and surround out',async()=>{
 const d=await probeWebProfile({
  canPlayType:type=>/avc1\.64|hvc1|dvh1|mp4a|ac-3|ec-3|alac|audio\/mpeg|flac|mpegurl/.test(type)?'probably':'',
  mediaSourceSupports:type=>/avc1\.64|hvc1|dvh1|mp4a|ac-3|ec-3|flac/.test(type),
  decodingInfo:async()=>({supported:true,smooth:true,powerEfficient:true}),
  matchMedia:q=>/dynamic-range: high|p3/.test(q),screen:{width:3456,height:2234},audioOutputChannels:6,embeddedAudioTracks:true,tonemapsHdr:true,identity:{platform:'safari'}});
 const p=d.engines.native;
 assert.equal(capabilityDocumentProblem(d),null);
 const hevc=p.video!.find(v=>v.codec==='hevc')!;
 assert.deepEqual(hevc.bitDepths,[8,10]);
 assert.ok(hevc.dynamicRanges!.includes('hdr10')&&hevc.dynamicRanges!.includes('dolby_vision'));
 assert.deepEqual(hevc.dolbyVisionProfiles,[5,8]);
 assert.equal(d.audioOutput!.maxChannels,6);
 assert.deepEqual(d.display!.dynamicRanges,['sdr','hdr10','hlg','dolby_vision']);
 assert.ok(p.transports!.find(t=>t.transport==='hls_fmp4')!.video!.includes('hevc'));
 assert.ok(p.transports!.find(t=>t.transport==='hls_ts')!.audio!.includes('eac3'));
 assert.equal(p.embeddedAudioSwitching,true);assert.equal(p.toneMapsHdr,true);
});

test('a probe that throws or hangs leaves the declared floor',async()=>{
 const d=await probeWebProfile({canPlayType:()=>{throw new Error('x');},mediaSourceSupports:()=>{throw new Error('y');},decodingInfo:()=>new Promise(()=>{}),matchMedia:()=>{throw new Error('z');}});
 assert.equal(capabilityDocumentProblem(d),null);
 assert.ok(d.engines.native.video!.some(v=>v.codec==='h264'));
 assert.ok(d.engines.native.transports!.some(t=>t.transport==='direct'&&t.video!.includes('h264')));
});

test('Apple facts widen and narrow the declared table',()=>{
 const options={television:true,osVersion:'18.0'};
 const tv=appleProfile(options,{model:'AppleTV14,1',hardwareDecode:{hevc:true,av1:false,dolbyVision:true},videoEligibility:{dolbyVision5:true,dolbyVision81:true,dolbyVision84:true},hdrModes:{hdr10:true,hlg:true,dolbyVision:true},eligibleForHDRPlayback:true,screen:{width:3840,height:2160,maxFrameRate:60},audio:{maxOutputChannels:8,route:'HDMI',spatial:false}});
 assert.equal(capabilityDocumentProblem(tv),null);
 assert.equal(tv.engines.native.evidence,'mixed');
 assert.deepEqual(tv.engines.native.video!.find(v=>v.codec==='hevc')!.dolbyVisionProfiles,[5,8]);
 assert.equal(tv.audioOutput!.maxChannels,8);assert.equal(tv.audioOutput!.route,'hdmi');
 assert.equal(appleProfile(options,{audio:{route:'BluetoothA2DPOutput'}}).audioOutput!.route,'bluetooth');
 assert.equal(appleProfile(options,{audio:{route:'AirPlay'}}).audioOutput!.route,'airplay');
 const sdr=appleProfile(options,{hdrModes:{hdr10:true},eligibleForHDRPlayback:false,audio:{maxOutputChannels:2}});
 assert.deepEqual(sdr.engines.native.video!.find(v=>v.codec==='hevc')!.dynamicRanges,['sdr'],'without hardware HEVC a display not in an HDR mode is sent SDR');
 assert.equal(sdr.engines.native.toneMapsHdr,false);
 const m3=appleProfile({television:false},{hardwareDecode:{hevc:true,av1:true}});
 assert.ok(m3.engines.native.transports!.find(t=>t.transport==='hls_fmp4')!.video!.includes('av1'));
 assert.equal(appleProfile(options,null).engines.native.evidence,'declared');
 // Hardware HEVC decodes HDR10/HLG (and Dolby Vision with its decoder) for an SDR screen too: the
 // decoder lists the ranges, the display stays SDR, and the engine says it tone-maps.
 const sdrScreen=appleProfile(options,{hardwareDecode:{hevc:true,dolbyVision:true},videoEligibility:{dolbyVision5:true,dolbyVision81:true,dolbyVision84:true},hdrModes:{},eligibleForHDRPlayback:false,audio:{maxOutputChannels:2}});
 assert.deepEqual(sdrScreen.engines.native.video!.find(v=>v.codec==='hevc')!.dynamicRanges,['sdr','hdr10','hlg','dolby_vision']);
 assert.deepEqual(sdrScreen.display!.dynamicRanges,['sdr']);
 assert.equal(sdrScreen.engines.native.toneMapsHdr,true,'device tone mapping is declared, not implied');
 // The Apple TV HD decodes H.264 up to 1080p.
 const hd=appleProfile(options,{model:'AppleTV5,3',hardwareDecode:{hevc:false}});
 assert.equal(hd.engines.native.video!.find(v=>v.codec==='h264')!.maxHeight,1080);
 assert.equal(capabilityDocumentProblem(hd),null);
 assert.equal(appleDeclaredProfile(options).engines.native.toneMapsHdr,undefined);
});

test('Android facts replace the decoder table; passthrough is listed beside the decoders',()=>{
 const d=androidProfile({television:true},{model:'SHIELD',decoders:[{codec:'h264',maxWidth:4096,maxHeight:2160,maxLevel:52},{codec:'hevc',tenBit:true,maxWidth:4096,maxHeight:2160,hdr:['hdr10','dolby_vision'],dolbyVisionProfiles:[5,7,8]},{codec:'vc1',maxWidth:1920,maxHeight:1080}],audioDecoders:[{codec:'aac',maxChannels:8},{codec:'ac3',maxChannels:6},{codec:'opus'},{codec:'flac',maxChannels:0}],displayHdr:['hdr10','dolby_vision'],passthrough:[{codec:'truehd',maxChannels:8,objectAudio:['atmos']},{codec:'ac3',maxChannels:6}],audio:{maxOutputChannels:8,route:'hdmi'}});
 const p=d.engines.native;
 assert.equal(capabilityDocumentProblem(d),null);
 assert.ok(p.transports!.find(t=>t.transport==='direct'&&t.containers!.includes('mkv'))!.audio!.includes('truehd'));
 assert.deepEqual(p.audioPassthrough!.map(a=>a.codec),['truehd','ac3']);
 assert.equal(p.audio!.some(a=>a.codec==='truehd'),false,'a codec the device only passes through is not a decoder');
 assert.equal(p.audio!.find(a=>a.codec==='ac3')!.maxChannels,6,'a codec the device also decodes stays decodable');
 assert.equal(p.audio!.find(a=>a.codec==='opus')!.maxChannels,6,'no stated limit keeps the declared one');
 assert.equal(p.audio!.find(a=>a.codec==='flac')!.maxChannels,8,'"any channel count" is not stereo');
 assert.deepEqual(p.video!.find(v=>v.codec==='hevc')!.dolbyVisionProfiles,[5,7,8]);
 assert.equal(d.audioOutput!.route,'hdmi');
 assert.deepEqual(p.subtitleRendering,['text']);
 assert.deepEqual(androidProfile({},{decoders:[{codec:'h264'}],subtitleRendering:['ass','pgs']}).engines.native.subtitleRendering,['text','ass','pgs']);
 assert.equal(androidProfile({},null).engines.native.evidence,'declared');
});

test('an SDR display gets HDR10 only from an engine that tone-maps it',async()=>{
 const base={
  canPlayType:(type:string)=>/avc1|hvc1|mp4a/.test(type)?'probably':'',
  mediaSourceSupports:(type:string)=>/avc1|hvc1|mp4a/.test(type),
  decodingInfo:async()=>({supported:true,smooth:true,powerEfficient:true}),
  matchMedia:()=>false,identity:{platform:'web'}};
 const hevcOf=(d:Awaited<ReturnType<typeof probeWebProfile>>)=>d.engines.native.video!.find(v=>v.codec==='hevc')!;
 const firefox=await probeWebProfile(base);
 assert.deepEqual(hevcOf(firefox).dynamicRanges,['sdr']);assert.equal(firefox.engines.native.toneMapsHdr,false);
 const safari=await probeWebProfile({...base,tonemapsHdr:true});
 assert.ok(hevcOf(safari).dynamicRanges!.includes('hdr10'));assert.equal(safari.engines.native.toneMapsHdr,true);assert.deepEqual(safari.display!.dynamicRanges,['sdr']);
 const hdrScreen=await probeWebProfile({...base,matchMedia:(q:string)=>/dynamic-range: high/.test(q)});
 assert.ok(hevcOf(hdrScreen).dynamicRanges!.includes('hdr10'));
});

test('a codec with only a software decoder is held to 1080p',()=>{
 const d=androidProfile({},{decoders:[{codec:'h264',hardware:true,maxWidth:3840,maxHeight:2160},{codec:'av1',hardware:false,maxWidth:7680,maxHeight:4320},{codec:'vp9',hardware:false}]});
 const of=(codec:string)=>d.engines.native.video!.find(v=>v.codec===codec)!;
 assert.equal(of('h264').maxHeight,2160);
 assert.equal(of('av1').maxHeight,1080);assert.equal(of('av1').maxWidth,1920);
 assert.equal(of('vp9').maxHeight,1080,'a software decoder that states no limit still has one');
});

test('an engine can declare an audio sample-depth ceiling',()=>{
 const d=appleDeclaredProfile({television:true});
 d.engines.native.audio!.find(a=>a.codec==='flac')!.maxBitDepth=16;
 assert.equal(capabilityDocumentProblem(d),null);
 d.engines.native.audio!.find(a=>a.codec==='flac')!.maxBitDepth=65;
 assert.equal(capabilityDocumentProblem(d),'engines.native.audio.flac');
});

test('a decoder that does 4K only at 30 states its operating points',async()=>{
 const d=androidProfile({},{decoders:[{codec:'h264',hardware:true,maxWidth:3840,maxHeight:2160,maxFrameRate:30,limits:[{maxWidth:3840,maxHeight:2160,maxFrameRate:30},{maxWidth:1920,maxHeight:1080,maxFrameRate:60}]}]});
 assert.equal(capabilityDocumentProblem(d),null);
 assert.deepEqual(d.engines.native.video!.find(v=>v.codec==='h264')!.limits,[{maxWidth:3840,maxHeight:2160,maxFrameRate:30},{maxWidth:1920,maxHeight:1080,maxFrameRate:60}]);
 const web=await probeWebProfile(chromeLike({decodingInfo:async q=>({supported:!/hvc1|dvh1/.test(q.contentType)&&q.transferFunction===undefined,smooth:q.width<=3840&&!(q.width>=3840&&q.framerate>30),powerEfficient:true})}));
 assert.deepEqual(web.engines.native.video!.find(v=>v.codec==='h264')!.limits,[{bitDepths:[8],maxWidth:3840,maxHeight:2160,maxFrameRate:30},{bitDepths:[8],maxWidth:1920,maxHeight:1080,maxFrameRate:60}]);
 const bad=androidProfile({},{decoders:[{codec:'h264',hardware:true,maxWidth:1920,maxHeight:1080}]});
 bad.engines.native.video![0]!.limits=[{}];
 assert.equal(capabilityDocumentProblem(bad),'engines.native.video.h264.limits');
});

test('4K is asked at a level that covers 4K, so a decoder that answers by level is not under-declared',async()=>{
 const asked:string[]=[];
 // A decoder that refuses 3840×2160 at level 4.0 and takes it at 5.1, as some televisions and Vega do.
 const d=await probeWebProfile(chromeLike({decodingInfo:async q=>{if(q.width===3840)asked.push(q.contentType);return {supported:!(q.width===3840&&/avc1\.640028|av01\.0\.08M|vp09\.00\.40/.test(q.contentType))&&!/hvc1|dvh1/.test(q.contentType),smooth:q.width<=3840,powerEfficient:true};}}));
 assert.ok(asked.some(type=>type.includes('avc1.640033')),'H.264 at level 5.1');
 assert.equal(asked.some(type=>type.includes('avc1.640028')),false);
 for(const codec of ['h264','av1','vp9'])assert.equal(d.engines.native.video!.find(v=>v.codec===codec)!.maxHeight,2160,codec);
});


test('Windows HEVC is granted by the runtime, with exact sample entry and transport',async()=>{
 for(const browser of ['blink','gecko']){
  const asked:string[]=[];
  const d=await probeWebProfile({identity:{engine:browser,os:'windows'},canPlayType:t=>t.includes('hev1.')?'probably':'',mediaSourceSupports:null,
   decodingInfo:async(q,type)=>{asked.push(type!);return {supported:q.contentType.includes('hev1.'),smooth:q.width<=3840&&q.framerate<=30,powerEfficient:true};}});
  const hevc=d.engines.native.video!.find(v=>v.codec==='hevc')!;
  assert.ok(hevc);assert.deepEqual(hevc.codecTags,['hev1']);assert.equal(hevc.maxHeight,2160);
  assert.ok(asked.every(t=>t==='file'),'a file-only decoder is never asked with media-source');
  assert.equal(d.engines.native.transports!.some(t=>t.transport==='hls_fmp4'),false);
 }
 const no=await probeWebProfile({identity:{engine:'blink',os:'windows'},canPlayType:()=>'',mediaSourceSupports:()=>false});
 assert.equal(no.engines.native.video!.some(v=>v.codec==='hevc'),false,'OS or UA never grants a decoder');
});

test('8K8bit does not grant 8K10bit or 4K60 when only 1080p60 is smooth',async()=>{
 const d=await probeWebProfile({canPlayType:t=>t.includes('hvc1')?'probably':'',mediaSourceSupports:t=>t.includes('hvc1'),
  decodingInfo:async q=>({supported:true,smooth:q.contentType.includes('hvc1.2.')?q.width<=1920:q.framerate<=30||q.width<=1920,powerEfficient:true})});
 const hevc=d.engines.native.video!.find(v=>v.codec==='hevc')!;
 assert.equal(hevc.maxWidth,7680);
 assert.ok(hevc.limits!.some(l=>l.bitDepths![0]===8&&l.maxWidth===7680&&l.maxFrameRate===30));
 assert.ok(hevc.limits!.filter(l=>l.bitDepths![0]===10).every(l=>l.maxWidth!<=1920));
 assert.ok(hevc.limits!.filter(l=>l.maxWidth!>1920).every(l=>l.maxFrameRate===30));
 assert.equal(capabilityDocumentProblem(d),null);
});

test('Dolby Vision requires the exact runtime report and HDR display; MIME/base HEVC alone is insufficient',async()=>{
 const env:WebProbeEnvironment={canPlayType:t=>/hvc1|dvh1/.test(t)?'probably':'',mediaSourceSupports:t=>/hvc1|dvh1/.test(t),matchMedia:()=>true,
  decodingInfo:async q=>({supported:!q.contentType.includes('dvh1.05'),smooth:q.width<=3840,powerEfficient:true})};
 const yes=await probeWebProfile(env),hevc=yes.engines.native.video!.find(v=>v.codec==='hevc')!;
 assert.deepEqual(hevc.dolbyVisionProfiles,[8]);assert.ok(yes.display!.dynamicRanges!.includes('dolby_vision'));
 const no=await probeWebProfile({...env,decodingInfo:async q=>({supported:!q.contentType.includes('dvh1'),smooth:true,powerEfficient:true})});
 assert.equal(no.engines.native.video!.find(v=>v.codec==='hevc')!.dolbyVisionProfiles,undefined);
 const sdr=await probeWebProfile({...env,matchMedia:()=>false});
 assert.equal(sdr.engines.native.video!.find(v=>v.codec==='hevc')!.dolbyVisionProfiles,undefined);
});


test('explicit runtime operating-point denial is not replaced with a fabricated 1080p ceiling',async()=>{
 const d=await probeWebProfile({canPlayType:()=> 'probably',mediaSourceSupports:()=>true,decodingInfo:async()=>({supported:false,smooth:false,powerEfficient:false})});
 assert.deepEqual(d.engines.native.video,[]);
 assert.equal(d.engines.native.audio!.find(a=>a.codec==='eac3')!.objectAudio,undefined,'codec support alone is not Atmos evidence');
 assert.equal(capabilityDocumentProblem(d),null);
});


test('Apple exact MIME and route facts narrow DV and JOC without invented output channels',()=>{
 const options={television:true};
 const guessed=appleProfile(options,{hardwareDecode:{hevc:true,dolbyVision:true},hdrModes:{dolbyVision:true},audio:{spatial:true,maxOutputChannels:2,route:'HDMI'}});
 assert.deepEqual(guessed.engines.native.video!.find(v=>v.codec==='hevc')!.dolbyVisionProfiles,[]);
 assert.equal(guessed.audioOutput!.maxChannels,2);
 assert.equal(guessed.engines.native.audio!.find(a=>a.codec==='eac3')!.objectAudio,undefined);
 const exact=appleProfile(options,{videoEligibility:{dolbyVision5:true,dolbyVision81:true,dolbyVision84:true},audio:{maximumOutputNumberOfChannels:2,outputNumberOfChannels:2,spatial:true,route:'HDMI',eac3JocEligible:true}});
 assert.deepEqual(exact.engines.native.video!.find(v=>v.codec==='hevc')!.dolbyVisionProfiles,[5,8]);
 assert.equal(exact.audioOutput!.maxChannels,2);assert.deepEqual(exact.engines.native.audio!.find(a=>a.codec==='eac3')!.objectAudio,['atmos']);
 assert.equal(exact.engines.native.audio!.some(a=>a.codec==='truehd'),false);
 const only81=appleProfile(options,{videoEligibility:{dolbyVision81:true,dolbyVision84:false}}).engines.native.video!.find(v=>v.codec==='hevc')!;
 assert.deepEqual(only81.dolbyVisionProfiles,[8]);assert.deepEqual(only81.dolbyVisionProfile8Compatibility,[1]);
 const only84=appleProfile(options,{videoEligibility:{dolbyVision84:true}}).engines.native.video!.find(v=>v.codec==='hevc')!;
 assert.deepEqual(only84.dolbyVisionProfiles,[8]);assert.deepEqual(only84.dolbyVisionProfile8Compatibility,[4]);
 assert.equal(appleProfile(options,{audio:{maximumOutputNumberOfChannels:1,spatial:true}}).audioOutput!.maxChannels,1);
 assert.equal(appleDeclaredProfile(options).engines.native.audio!.find(a=>a.codec==='eac3')!.objectAudio,undefined);
});
